// chonkpilot-self-tools —— P6（2026-10-19）：系统级 app 根（<exeDir>/capability）的
// 5 个可执行工具（filesys_run / script_run / web_fetch / browser_run / desktop_run）的**进程外**实现。
//
// 本进程不持有会话/任务状态；只做「LLM 工具 → executor 契约执行」的适配：
//
//	LoadExecTools（扫描 *.tool.md，跳过 category=server）
//	→ plugin/v1/tools.register（name = 契约名，handler_subject = self-tool-call-ext）
//	→ plugin.tool.call（gateway → 插件桥）→ ExecTool.Call（spawn executor）
//
// 启动：由宿主（desktop / server）在插件桥就绪后 spawn，环境变量：
//
//	CHONKPILOT_PLUGIN_ADDR        = 插件桥地址（必填；UDS 路径或 tcp://）
//	CHONKPILOT_PLUGIN_ID          = 插件 id（缺省 self-tools）
//	CHONKPILOT_CAPABILITY_ROOT    = 系统 app 根（必填；<exeDir>/capability）
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/transport"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultPluginID = "self-tools"
	pluginVersion   = "1.0.0"
	// handlerSubject 与进程内域名工具回调主题分开：进程外模式 = 全部 app 工具由本进程承接。
	handlerSubject = "self-tool-call-ext"
	// toolCallTimeout 是单次外部工具调用的执行硬上限（契约 timeout=0/-1 且 no_timeout 时不施加）。
	toolCallTimeout = 30 * time.Minute
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	addr := strings.TrimSpace(os.Getenv("CHONKPILOT_PLUGIN_ADDR"))
	if addr == "" {
		logger.Error("CHONKPILOT_PLUGIN_ADDR 为空（应由宿主注入）")
		os.Exit(1)
	}
	pluginID := strings.TrimSpace(os.Getenv("CHONKPILOT_PLUGIN_ID"))
	if pluginID == "" {
		pluginID = defaultPluginID
	}
	root := strings.TrimSpace(os.Getenv("CHONKPILOT_CAPABILITY_ROOT"))
	if root == "" {
		logger.Error("CHONKPILOT_CAPABILITY_ROOT 为空（应由宿主注入）")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli, err := dial(ctx, addr)
	if err != nil {
		logger.Error("连接插件桥失败", "err", err)
		os.Exit(1)
	}
	defer cli.Close()

	if _, err := call(ctx, cli, "plugin.hello", map[string]any{
		"id": pluginID, "name": "self-tools", "version": pluginVersion,
		"features": map[string]any{"self_tools": true},
	}); err != nil {
		logger.Error("plugin.hello 失败", "err", err)
		os.Exit(1)
	}

	reg := newRegistry()
	if err := reg.load(root); err != nil {
		logger.Error("加载工具契约失败", "root", root, "err", err)
		os.Exit(1)
	}
	if err := reg.sync(ctx, cli); err != nil {
		logger.Error("注册工具失败", "err", err)
		os.Exit(1)
	}
	if _, err := call(ctx, cli, "plugin.tool.bind", map[string]any{"subject": handlerSubject}); err != nil {
		logger.Error("绑定工具主题失败", "err", err)
		os.Exit(1)
	}
	logger.Info("self-tools ready", "addr", addr, "root", root, "tools", reg.count())

	// 热重载：监听 app 根（含子目录）→ 去抖后重扫 → 工具集 diff → unregister / register。
	// 监听失败仅记日志（不退出）：工具面退化为「重启生效」。
	if w, werr := newWatcher(root, 100*time.Millisecond, func() {
		if err := reg.reload(context.Background(), cli, root); err != nil {
			logger.Warn("工具契约热重载失败（保留现有工具面）", "err", err)
		}
	}); werr != nil {
		logger.Warn("capability 根监听失败（工具契约变更需重启生效）", "root", root, "err", werr)
	} else {
		defer w.Close()
	}

	serve(cli, reg)
}

// ─── 插件桥传输 ─────────────────────────

func dial(ctx context.Context, addr string) (transport.Conn, error) {
	opts := transport.Options{Addr: addr}
	if strings.HasPrefix(addr, "tcp://") {
		opts.Backend = transport.BackendTCP
		opts.Addr = strings.TrimPrefix(addr, "tcp://")
	}
	return transport.Dial(ctx, opts)
}

func call(ctx context.Context, cli transport.Conn, method string, args any) (map[string]any, error) {
	res, err := cli.Call(ctx, method, args)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		if len(res.Errors) > 0 {
			return nil, errors.New(res.Errors[0])
		}
		return nil, errors.New("调用失败")
	}
	m, _ := res.Value.(map[string]any)
	return m, nil
}

// ─── 工具注册表（含热重载 diff） ─────────────────────────

// registry 持有当前可执行工具与**已注册指纹**（指纹变化 = 先 unregister 再 register）。
type registry struct {
	mu      sync.RWMutex
	tools   map[string]*mcpms.ExecTool
	applied map[string]string // name → payload 指纹（已成功注册）
	// reloadMu 串行化热重载（去抖后仍可能有并发 fire：重扫 + 对账必须整体互斥）。
	reloadMu sync.Mutex
}

func newRegistry() *registry {
	return &registry{
		tools:   map[string]*mcpms.ExecTool{},
		applied: map[string]string{},
	}
}

func (r *registry) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

func (r *registry) get(name string) (*mcpms.ExecTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// load 重扫契约根并替换内存工具集（注册动作由 sync 完成）。
func (r *registry) load(root string) error {
	tools, err := mcpms.LoadExecTools(root, nil)
	if err != nil {
		return err
	}
	next := make(map[string]*mcpms.ExecTool, len(tools))
	for _, t := range tools {
		next[t.Name] = t
	}
	r.mu.Lock()
	r.tools = next
	r.mu.Unlock()
	return nil
}

// sync 把当前工具集与已注册集对账：删除/变化 → unregister（变化再 register）；新增/变化 → register。
func (r *registry) sync(ctx context.Context, cli transport.Conn) error {
	r.mu.RLock()
	payloads := make(map[string]map[string]any, len(r.tools))
	for name, t := range r.tools {
		payloads[name] = buildRegisterPayload(t)
	}
	applied := make(map[string]string, len(r.applied))
	for k, v := range r.applied {
		applied[k] = v
	}
	r.mu.RUnlock()

	next := make(map[string]string, len(payloads))
	for name, p := range payloads {
		next[name] = payloadFingerprint(p)
	}
	removed, upserted := diffTools(applied, next)
	for _, name := range removed {
		if _, err := call(ctx, cli, "plugin/v1/tools.unregister", unregisterPayload(name)); err != nil {
			return fmt.Errorf("unregister %s: %w", name, err)
		}
		delete(applied, name)
		r.setApplied(name, "")
	}
	for _, name := range upserted {
		// 已注册但描述/schema/meta 变化 → 先 unregister 再 register（gateway 同名注册会报重复）。
		if _, ok := applied[name]; ok {
			if _, err := call(ctx, cli, "plugin/v1/tools.unregister", unregisterPayload(name)); err != nil {
				return fmt.Errorf("unregister %s: %w", name, err)
			}
			delete(applied, name)
			r.setApplied(name, "")
		}
		if _, err := call(ctx, cli, "plugin/v1/tools.register", payloads[name]); err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
		applied[name] = next[name]
		r.setApplied(name, next[name])
	}
	return nil
}

// setApplied 更新单个工具的已注册指纹（fp=="" → 删除条目）；逐项提交，避免一次失败后
// 本地 applied 与 gateway 实际状态脱节导致下次对账重复 unregister/register。
func (r *registry) setApplied(name, fp string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if fp == "" {
		delete(r.applied, name)
		return
	}
	r.applied[name] = fp
}

// reload 重扫契约根并对账（热重载入口）。
func (r *registry) reload(ctx context.Context, cli transport.Conn, root string) error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	if err := r.load(root); err != nil {
		return err
	}
	return r.sync(ctx, cli)
}

// ─── 工具执行 ─────────────────────────

// toolCall 是 gateway 工具回调载荷中本进程消费的部分。
// 兼容两种参数键：`args`（gateway registeredProvider）与 `arguments`（契约口径）。
type toolCall struct {
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args"`
	Arguments  map[string]any `json:"arguments"`
	CallID     string         `json:"call_id"`
	InstanceID string         `json:"instance_id"`
	WorkDir    string         `json:"work_dir"`
	DataDir    string         `json:"data_dir"`
	Purpose    string         `json:"purpose"`
	NoTimeout  bool           `json:"no_timeout"`
	Context    *toolCallCtx   `json:"context"`
}

// toolCallCtx 是 turn 上下文段（gateway 经 payload.context 下发）。
type toolCallCtx struct {
	InstanceID string `json:"instance_id"`
	WorkDir    string `json:"work_dir"`
	DataDir    string `json:"data_dir"`
	Purpose    string `json:"purpose"`
}

// args 返回本次调用的参数（args 优先，回退 arguments）。
func (in toolCall) args() map[string]any {
	if in.Args != nil {
		return in.Args
	}
	if in.Arguments != nil {
		return in.Arguments
	}
	return map[string]any{}
}

// context 返回调用上下文（顶层字段优先，回退 context 段）。
func (in toolCall) callContext() mcpms.CallContext {
	cx := mcpms.CallContext{
		InstanceID: in.InstanceID, WorkDir: in.WorkDir, DataDir: in.DataDir, Purpose: in.Purpose,
	}
	if in.Context != nil {
		if cx.InstanceID == "" {
			cx.InstanceID = in.Context.InstanceID
		}
		if cx.WorkDir == "" {
			cx.WorkDir = in.Context.WorkDir
		}
		if cx.DataDir == "" {
			cx.DataDir = in.Context.DataDir
		}
		if cx.Purpose == "" {
			cx.Purpose = in.Context.Purpose
		}
	}
	return cx
}

// handleCall 执行一次工具：subject 必须为本进程绑定的主题；未知 tool → 明确错误。
func handleCall(ctx context.Context, subject string, in toolCall, reg *registry) (any, error) {
	if subject != handlerSubject {
		return nil, fmt.Errorf("未知工具主题 %q（期望 %s）", subject, handlerSubject)
	}
	if in.Tool == "" {
		return nil, errors.New("tool is required")
	}
	et, ok := reg.get(in.Tool)
	if !ok {
		return nil, fmt.Errorf("未知 self 工具 %s", in.Tool)
	}
	res, err := et.Call(ctx, in.args(), in.callContext())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":     true,
		"result": callResultMap(res),
	}, nil
}

// callResultMap 把 *mcp.CallToolResult 归一为 gateway 可消费的 map{content, isError}
// （与 pluginbridge registeredProvider.toolResultFromValue 的入参形态一致）。
func callResultMap(res *mcp.CallToolResult) map[string]any {
	out := map[string]any{"content": []any{}, "isError": false}
	if res == nil {
		return out
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return out
	}
	m := map[string]any{}
	if json.Unmarshal(raw, &m) == nil {
		if c, ok := m["content"]; ok {
			out["content"] = c
		}
		if e, ok := m["isError"].(bool); ok {
			out["isError"] = e
		}
	}
	return out
}

// ─── serve 循环 ─────────────────────────

func serve(cli transport.Conn, reg *registry) {
	var wg sync.WaitGroup
	// cancels 按 call_id 登记（plugin.tool.cancel 帧 → 取消对应处理器 ctx）。
	var mu sync.Mutex
	cancels := map[string]context.CancelFunc{}
	for {
		f, err := cli.Recv()
		if err != nil {
			wg.Wait()
			return
		}
		if f.Kind == transport.KindCtl && f.Name == "plugin.tool.cancel" {
			var in struct {
				CallID string `json:"call_id"`
			}
			_ = json.Unmarshal(f.Payload, &in)
			mu.Lock()
			cancel := cancels[in.CallID]
			mu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		if f.Kind != transport.KindReq || f.Name != "plugin.tool.call" {
			continue
		}
		wg.Add(1)
		go func(f transport.Frame) {
			defer wg.Done()
			var outer struct {
				Subject string          `json:"subject"`
				Payload json.RawMessage `json:"payload"`
			}
			_ = json.Unmarshal(f.Payload, &outer)
			var in toolCall
			_ = json.Unmarshal(outer.Payload, &in)

			// no_timeout（契约 timeout=0/-1 = 无上限）→ 不设执行硬上限，仅由取消收敛；
			// 其余外部工具保留 30 分钟安全上限。
			base := context.Background()
			var ctx context.Context
			var cancel context.CancelFunc
			if in.NoTimeout {
				ctx, cancel = context.WithCancel(base)
			} else {
				ctx, cancel = context.WithTimeout(base, toolCallTimeout)
			}
			if in.CallID != "" {
				mu.Lock()
				cancels[in.CallID] = cancel
				mu.Unlock()
			}
			value, herr := handleCall(ctx, outer.Subject, in, reg)
			cancel()
			if in.CallID != "" {
				mu.Lock()
				delete(cancels, in.CallID)
				mu.Unlock()
			}
			res := transport.Frame{Kind: transport.KindRes, ID: f.ID}
			if herr != nil {
				res.Errors = []string{herr.Error()}
			} else {
				res.OK = true
				res.Result = value
			}
			_ = cli.Send(res)
		}(f)
	}
}
