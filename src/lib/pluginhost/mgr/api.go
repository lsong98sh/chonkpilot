package mgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	pluginbridge "github.com/chonkpilot/chonkpilot-lib/pluginhost/bridge"
	mq "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/bus"
)

// ── 启用表 ─────────────────────────

func (m *Manager) enabledPath() string {
	return filepath.Join(m.opts.DataDir, "plugins", "enabled.json")
}

func (m *Manager) loadEnabled() {
	raw, err := os.ReadFile(m.enabledPath())
	if err != nil {
		return
	}
	var m2 map[string]bool
	if json.Unmarshal(raw, &m2) != nil {
		return
	}
	m.mu.Lock()
	m.enabled = m2
	m.mu.Unlock()
}

func (m *Manager) saveEnabled() {
	m.mu.Lock()
	snapshot := make(map[string]bool, len(m.enabled))
	for k, v := range m.enabled {
		snapshot[k] = v
	}
	m.mu.Unlock()
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(m.enabledPath(), raw, 0o644)
}

// ── 管理消息 ─────────────────────────

func (m *Manager) installMessages() {
	on := func(topic string, fn func(ctx context.Context, v *mq.Value) error) {
		sub, err := m.opts.Bus.On(topic, 0, func(ctx context.Context, _ string, v *mq.Value) error {
			return fn(ctx, v)
		})
		if err != nil {
			m.logf("pluginmgr: 订阅 %s 失败: %v", topic, err)
			return
		}
		m.mu.Lock()
		m.subs = append(m.subs, sub)
		m.mu.Unlock()
	}
	on(TopicList, func(_ context.Context, v *mq.Value) error {
		m.mu.Lock()
		ids := append([]string(nil), m.order...)
		m.mu.Unlock()
		out := make([]pluginView, 0, len(ids))
		for _, id := range ids {
			if pv, ok := m.viewByID(id); ok {
				out = append(out, pv)
			}
		}
		v.Result = map[string]any{"plugins": out}
		return nil
	})
	on(TopicSetEnabled, func(_ context.Context, v *mq.Value) error {
		var in struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := v.JSON(&in); err != nil {
			return err
		}
		if err := m.setEnabled(in.ID, in.Enabled); err != nil {
			return err
		}
		v.Result = map[string]any{"ok": true, "id": in.ID, "enabled": in.Enabled}
		return nil
	})
	on(TopicConfigGet, func(_ context.Context, v *mq.Value) error {
		var in struct {
			ID string `json:"id"`
		}
		if err := v.JSON(&in); err != nil {
			return err
		}
		fields, values, err := m.configGet(in.ID)
		if err != nil {
			return err
		}
		v.Result = map[string]any{"fields": fields, "values": values}
		return nil
	})
	on(TopicConfigSet, func(_ context.Context, v *mq.Value) error {
		var in struct {
			ID     string         `json:"id"`
			Values map[string]any `json:"values"`
		}
		if err := v.JSON(&in); err != nil {
			return err
		}
		if err := m.configSet(in.ID, in.Values); err != nil {
			return err
		}
		v.Result = map[string]any{"ok": true, "id": in.ID}
		return nil
	})
	on(TopicCommand, func(ctx context.Context, v *mq.Value) error {
		var in struct {
			ID     string         `json:"id"`
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		}
		if err := v.JSON(&in); err != nil {
			return err
		}
		res, err := m.command(ctx, in.ID, in.Name, in.Params)
		if err != nil {
			return err
		}
		v.Result = map[string]any{"ok": true, "result": res}
		return nil
	})
	on(TopicCall, func(ctx context.Context, v *mq.Value) error {
		var in struct {
			ID     string         `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := v.JSON(&in); err != nil {
			return err
		}
		res, err := m.call(ctx, in.ID, in.Method, in.Params)
		if err != nil {
			return err
		}
		v.Result = res
		return nil
	})
}

func (m *Manager) setEnabled(id string, enabled bool) error {
	p := m.plugin(id)
	if p == nil {
		return fmt.Errorf("插件不存在: %s", id)
	}
	m.mu.Lock()
	m.enabled[id] = enabled
	m.mu.Unlock()
	m.saveEnabled()
	p.mu.Lock()
	already := p.enabled
	p.enabled = enabled
	p.mu.Unlock()
	// 让 UI 立即刷新 enabled 标记（状态另有 plugin-status 事件）。
	m.publishRegistered(p)
	if enabled {
		if already && m.running(p) {
			return nil
		}
		p.mu.Lock()
		p.stopReq = false
		p.mu.Unlock()
		if p.Manifest.Runtime.Mode == "managed" {
			m.startSupervise(p)
		} else {
			m.setStatus(p, StatusStarting, "等待外部进程连接")
		}
		return nil
	}
	go m.stopPlugin(p, true)
	return nil
}

func (m *Manager) running(p *Plugin) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status == StatusRunning || p.status == StatusStarting || p.status == StatusRegistering
}

func (m *Manager) command(ctx context.Context, id, name string, params map[string]any) (any, error) {
	if m.opts.RequestPlugin == nil {
		return nil, errors.New("宿主未启用插件命令通道")
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("command.name 不能为空")
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return m.opts.RequestPlugin(cctx, id, "plugin.command", map[string]any{"name": name, "params": params})
}

// 前端 iframe 桥：config.* / data.* / rpc / log。
func (m *Manager) call(ctx context.Context, id, method string, params map[string]any) (any, error) {
	switch method {
	case "config.get":
		fields, values, err := m.configGet(id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "fields": fields, "values": values}, nil
	case "config.set":
		values := params
		if v, ok := params["values"].(map[string]any); ok {
			values = v
		}
		if err := m.configSet(id, values); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "config.reset":
		if err := m.configReset(id); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "data.read", "data.write":
		resource, _ := params["resource"].(string)
		op, _ := params["op"].(string)
		instanceID, _ := params["instance_id"].(string)
		inner, _ := params["params"].(map[string]any)
		res, err := m.dataCall(ctx, id, resource, op, instanceID, inner, method == "data.write")
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "result": res}, nil
	case "rpc":
		if m.opts.RequestPlugin == nil {
			return nil, errors.New("宿主未启用插件 RPC")
		}
		methodName, _ := params["method"].(string)
		rpcParams, _ := params["params"].(map[string]any)
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		res, err := m.opts.RequestPlugin(cctx, id, "plugin.ui.rpc", map[string]any{"method": methodName, "params": rpcParams})
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "result": res}, nil
	case "log":
		msg, _ := params["message"].(string)
		if len(msg) > 4096 {
			msg = msg[:4096]
		}
		m.appendLog(id, msg)
		return map[string]any{"ok": true}, nil
	default:
		return nil, fmt.Errorf("未知 iframe 方法: %s", method)
	}
}

func (m *Manager) dataCall(ctx context.Context, pluginID, resource, op, instanceID string, params map[string]any, write bool) (any, error) {
	if resource == "" || op == "" {
		return nil, errors.New("data: resource/op 必填")
	}
	if instanceID == "" && resource != "storage.self" {
		return nil, errors.New("data: instance_id 必填")
	}
	if resource == "storage.self" {
		return m.storage(pluginID, op, params)
	}
	if err := m.dataGuard(pluginID, resource, op); err != nil {
		return nil, err
	}
	topic, err := pluginbridge.DataTopic(resource, op, write)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"instance_id": instanceID}
	for k, v := range params {
		payload[k] = v
	}
	return pluginbridge.EmitData(ctx, m.opts.Bus, topic, payload)
}

// ── 插件桥回调 ─────────────────────────

func (m *Manager) registerMCP(ctx context.Context, pluginID string, req pluginbridge.PluginMCPRegister) error {
	p := m.plugin(pluginID)
	if p == nil {
		return fmt.Errorf("插件未登记: %s", pluginID)
	}
	if p.Manifest.MCP.Mode == "tools" || req.Mode == "tools" {
		return errors.New("tools 模式暂未实现（请使用 mcp.mode=http）")
	}
	name := "plugin-" + pluginID
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		ns = p.Manifest.MCP.Namespace
	}
	if ns == "" {
		ns = "-"
	}
	hot := req.HotTools
	if len(hot) == 0 {
		hot = p.Manifest.MCP.HotTools
	}
	if len(hot) == 0 {
		hot = []string{"*"}
	}
	category := req.Category
	if category == "" {
		category = p.Manifest.MCP.Category
	}
	if category == "" {
		category = "plugin"
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = p.Manifest.MCP.Timeout
	}
	if m.opts.RegisterMCP != nil {
		if err := m.opts.RegisterMCP(ctx, name, req.URL, req.Token, ns, hot, category, p.Manifest.MCP.Origin, timeout); err != nil {
			m.setStatus(p, StatusError, "MCP 注册失败: "+err.Error())
			return err
		}
	}
	p.mu.Lock()
	p.tools = hot
	p.status = StatusRunning
	p.detail = ""
	p.mu.Unlock()
	m.publishRegistered(p)
	m.broadcast(TopicStatus, map[string]any{"id": p.ID, "status": StatusRunning})
	return nil
}

func (m *Manager) unregisterMCP(_ context.Context, pluginID string) error {
	if m.opts.UnregisterMCP != nil {
		m.opts.UnregisterMCP("plugin-" + pluginID)
	}
	return nil
}

func (m *Manager) registerUI(pluginID string, payload json.RawMessage) {
	p := m.plugin(pluginID)
	if p == nil {
		return
	}
	p.mu.Lock()
	p.ui = payload
	p.mu.Unlock()
	m.publishRegistered(p)
}

func (m *Manager) pluginStatus(pluginID, state, detail string) {
	p := m.plugin(pluginID)
	if p == nil {
		return
	}
	switch state {
	case StatusRunning:
		p.mu.Lock()
		p.status = StatusRunning
		p.detail = detail
		p.mu.Unlock()
		m.publishRegistered(p)
	case StatusDegraded, StatusError:
		m.setStatus(p, state, detail)
	default:
		m.setStatus(p, state, detail)
		return
	}
	m.broadcast(TopicStatus, map[string]any{"id": pluginID, "status": state, "detail": detail})
}

func (m *Manager) dataGuard(pluginID, resource, op string) error {
	p := m.plugin(pluginID)
	if p == nil || len(p.Manifest.Permissions.Data) == 0 {
		return nil // 无 manifest / 未声明 → 兼容放行（第三方 legacy）
	}
	want := resource + "." + op
	for _, perm := range p.Manifest.Permissions.Data {
		base := perm
		if i := strings.Index(base, ":"); i >= 0 {
			base = base[:i]
		}
		if base == want || base == resource+".*" || base == "*" {
			return nil
		}
	}
	return fmt.Errorf("permission_denied: %s 未声明 %s", pluginID, want)
}

// ── 配置 ─────────────────────────

func (m *Manager) pluginDataDir(id string) string {
	return filepath.Join(m.opts.DataDir, "plugins", id)
}

func (m *Manager) configGet(id string) ([]ConfigField, map[string]any, error) {
	p := m.plugin(id)
	if p == nil {
		return nil, nil, fmt.Errorf("插件不存在: %s", id)
	}
	config, secrets, err := m.readConfig(id)
	if err != nil {
		return nil, nil, err
	}
	values := map[string]any{}
	for _, f := range p.Manifest.Config.Fields {
		if f.Default != nil {
			values[f.Key] = f.Default
		}
	}
	for k, v := range config {
		values[k] = v
	}
	for _, f := range p.Manifest.Config.Fields {
		if !f.Secret {
			continue
		}
		if v, ok := secrets[f.Key]; ok {
			values[f.Key] = maskSecret(fmt.Sprint(v))
		}
	}
	return p.Manifest.Config.Fields, values, nil
}

func (m *Manager) configSet(id string, values map[string]any) error {
	p := m.plugin(id)
	if p == nil {
		return fmt.Errorf("插件不存在: %s", id)
	}
	allowed := map[string]ConfigField{}
	for _, f := range p.Manifest.Config.Fields {
		allowed[f.Key] = f
	}
	config, secrets, err := m.readConfig(id)
	if err != nil {
		return err
	}
	for k, v := range values {
		f, ok := allowed[k]
		if !ok {
			return fmt.Errorf("config_invalid: 未声明的配置键 %s", k)
		}
		if f.Secret {
			if s, _ := v.(string); strings.HasPrefix(s, "****") || s == "" {
				continue // 空/掩码 = 不修改
			}
			secrets[k] = v
			continue
		}
		config[k] = v
	}
	for _, f := range p.Manifest.Config.Fields {
		if !f.Required || f.Secret {
			continue
		}
		if v, ok := config[f.Key]; !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
			return fmt.Errorf("config_invalid: %s 必填", f.Key)
		}
	}
	for _, f := range p.Manifest.Config.Fields {
		if !f.Required || !f.Secret {
			continue
		}
		if _, ok := secrets[f.Key]; !ok {
			return fmt.Errorf("config_invalid: %s 必填", f.Key)
		}
	}
	if m.cfgStore == nil {
		// 降级：库不可用时仍以文件为准（首次直跑/测试环境）。
		dir := m.pluginDataDir(id)
		if err := writeJSONAtomic(filepath.Join(dir, "config.json"), config, 0o644); err != nil {
			return err
		}
		if err := writeJSONAtomic(filepath.Join(dir, "secrets.json"), secrets, 0o600); err != nil {
			return err
		}
	} else {
		// 权威落库（宿主 ConfigStore 端口）→ 物化运行时快照供插件读取。
		if err := writeConfigRow(m.cfgStore, pluginConfigTable, id, config); err != nil {
			return err
		}
		if err := writeConfigRow(m.cfgStore, pluginSecretTable, id, secrets); err != nil {
			return err
		}
		if err := m.materializeConfig(id); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m.broadcast(TopicConfigChanged, map[string]any{"id": id, "keys": keys})
	if m.opts.RequestPlugin != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = m.opts.RequestPlugin(ctx, id, "plugin.config.changed", map[string]any{"keys": keys})
		}()
	}
	return nil
}

func (m *Manager) configReset(id string) error {
	// 恢复默认 = 删除权威行 + 清掉运行时快照（下次读取回落到 manifest 默认值）。
	if err := deleteConfigRow(m.cfgStore, pluginConfigTable, id); err != nil {
		return err
	}
	if err := deleteConfigRow(m.cfgStore, pluginSecretTable, id); err != nil {
		return err
	}
	dir := m.pluginDataDir(id)
	_ = os.Remove(filepath.Join(dir, "config.json"))
	_ = os.Remove(filepath.Join(dir, "secrets.json"))
	return nil
}

// ── 插件自持 webui 资源（PLUGIN-UI-1，28-plugins §3A.2）─────────────────

// MethodUIAssetGet 是「宿主 → 插件」资源请求的方法名（插件侧 `OnRequest` 同名注册）。
//
// 复用既有下行请求/应答通道（transport req/res + `RequestPlugin`），**不新增 MQ 主题**。
const MethodUIAssetGet = "plugin/v1/ui.asset.get"

// uiAssetTimeout 是单次资源请求超时；超时 / 未就绪 / 未命中由宿主一律按 404 处理。
const uiAssetTimeout = 5 * time.Second

// UIAsset 向插件请求其自持 webui 资源（插件 embed 进 exe，宿主不读磁盘）。
//
// 返回 (data, found, err)：
//   - found=false → 插件未登记 / 未就绪 / 资源未命中 / 解析失败（本阶段宿主一律回 404）；
//   - err 仅供宿主日志诊断，**不向浏览器暴露细节**。
//
// 插件返回的错误信封（如 `not_found: x`）经 RequestPlugin 转成 err —— 调用方按未命中处理。
func (m *Manager) UIAsset(ctx context.Context, id, path string) ([]byte, bool, error) {
	p := m.plugin(id)
	if p == nil {
		return nil, false, fmt.Errorf("插件未登记: %s", id)
	}
	if m.opts.RequestPlugin == nil {
		return nil, false, errors.New("宿主未启用插件请求通道")
	}
	p.mu.Lock()
	status := p.status
	p.mu.Unlock()
	if status != StatusRunning {
		return nil, false, fmt.Errorf("插件未就绪: %s(%s)", id, status)
	}
	cctx, cancel := context.WithTimeout(ctx, uiAssetTimeout)
	defer cancel()
	res, err := m.opts.RequestPlugin(cctx, id, MethodUIAssetGet, map[string]any{"path": path})
	if err != nil {
		return nil, false, err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, false, err
	}
	// data 用 *[]byte：`null` / 缺字段 = nil（未命中），空文件 = 非 nil 空切片（合法）。
	var out struct {
		Data *[]byte `json:"data"`
		Size int     `json:"size"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, fmt.Errorf("ui.asset.get 响应解析失败: %w", err)
	}
	if out.Data == nil {
		return nil, false, errors.New("ui.asset.get 响应缺少 data")
	}
	if out.Size != 0 && out.Size != len(*out.Data) {
		return nil, false, fmt.Errorf("ui.asset.get size 不符: size=%d got=%d", out.Size, len(*out.Data))
	}
	return *out.Data, true, nil
}

// UIAssetVersion 返回插件自持 webui 资源的缓存版本分量 = `manifest.version#运行代次`。
//
// 版本变化（插件升级）或重启（同版本新进程 → 代次 +1）都会改变分量 →
// 宿主短缓存（键 = 插件 id + 路径 + 本分量）自动失效。
func (m *Manager) UIAssetVersion(id string) string {
	p := m.plugin(id)
	if p == nil {
		return ""
	}
	p.mu.Lock()
	gen := p.gen
	p.mu.Unlock()
	return p.Manifest.Version + "#" + strconv.FormatUint(gen, 10)
}

// ── 插件 UI 动作面（PLUGIN-UI-1 第二段）────────────────────────────

// MethodUIRPC 是宿主 → 插件的通用 RPC 信封（插件侧按 `method` 分派到 `OnRequest` 同名处理器）。
//
// UI 动作面直接复用本通道（`POST /plugins/<id>/api/<action>` → `{method:action, params:payload}`），
// **不新增** MQ 主题 / 插件能力名。
const MethodUIRPC = "plugin.ui.rpc"

// uiActionTimeout 是单次 UI 动作请求超时；超时由宿主回 504（错误信封统一）。
const uiActionTimeout = 30 * time.Second

// UIAction 把一条 UI 动作透传给**已就绪插件**（`plugin.ui.rpc`），原样返回插件结果。
//
// 参数 `action` = 插件 `OnRequest` 注册的方法名；`params` = 该动作的业务参数（不解析）。
// 插件未登记 / 未就绪 / 通道未装配 / 插件返回错误 → err（宿主据此回 5xx 错误信封）。
func (m *Manager) UIAction(ctx context.Context, id, action string, params map[string]any) (any, error) {
	if strings.TrimSpace(action) == "" {
		return nil, errors.New("ui action 不能为空")
	}
	p := m.plugin(id)
	if p == nil {
		return nil, fmt.Errorf("插件未登记: %s", id)
	}
	if m.opts.RequestPlugin == nil {
		return nil, errors.New("宿主未启用插件请求通道")
	}
	p.mu.Lock()
	status := p.status
	p.mu.Unlock()
	if status != StatusRunning {
		return nil, fmt.Errorf("插件未就绪: %s(%s)", id, status)
	}
	cctx, cancel := context.WithTimeout(ctx, uiActionTimeout)
	defer cancel()
	return m.opts.RequestPlugin(cctx, id, MethodUIRPC, map[string]any{"method": action, "params": params})
}

// ── 插件私有存储（storage.self）─────────────────────────

var storageKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func (m *Manager) storage(pluginID, op string, params map[string]any) (any, error) {
	key, _ := params["key"].(string)
	if key != "" && !storageKeyPattern.MatchString(key) {
		return nil, errors.New("storage: key 非法")
	}
	dir := filepath.Join(m.pluginDataDir(pluginID), "storage")
	switch op {
	case "list":
		entries, err := os.ReadDir(dir)
		if err != nil {
			return map[string]any{"keys": []string{}}, nil
		}
		keys := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				keys = append(keys, strings.TrimSuffix(e.Name(), ".json"))
			}
		}
		sort.Strings(keys)
		return map[string]any{"keys": keys}, nil
	case "get":
		raw, err := os.ReadFile(filepath.Join(dir, key+".json"))
		if err != nil {
			return map[string]any{"found": false}, nil
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		return map[string]any{"found": true, "value": v}, nil
	case "set":
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, writeJSONAtomic(filepath.Join(dir, key+".json"), params["value"], 0o644)
	case "del":
		_ = os.Remove(filepath.Join(dir, key+".json"))
		return map[string]any{"ok": true}, nil
	default:
		return nil, fmt.Errorf("storage: 未知操作 %s", op)
	}
}

func (m *Manager) appendLog(id, msg string) {
	path := filepath.Join(m.pluginDataDir(id), "logs", "plugin.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(time.Now().Format(time.RFC3339) + " [ui] " + msg + "\n")
}

// ── 工具函数 ─────────────────────────

func mergeFile(dst map[string]any, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			dst[k] = v
		}
	}
}

func writeJSONAtomic(path string, v any, perm os.FileMode) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, raw, perm)
}

func writeFileAtomic(path string, raw []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, perm); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
