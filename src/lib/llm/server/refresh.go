package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// reloadToolAsyncOverrides 读 usr `tool_async`（data-user-config-load，**既有主题**）并应用。
// 订阅面 = 既有 data-user-config-refresh（见 onUserConfigRefresh），**零新增主题**。
// 说明：本链路只碰 tool_async，**不重跑 loadExecConfig** —— 后者会连带用内置默认重写
// prj 执行配置（SetRuntime timeout_sec/max_concurrency/skip_dirs），会把 prj 值误重置。
func (s *Server) reloadToolAsyncOverrides() {
	if s.mcpCfg == nil {
		return
	}
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{})
	if err != nil {
		logf("[chonkpilot-server] reloadToolAsyncOverrides: UserConfigGet failed: %v\n", err)
		return
	}
	d := res.Config
	raw, _ := d[toolAsyncUserConfigKey].(string)
	s.applyToolAsync(raw)
	// executor 级沙箱开关（usr `tool_sandbox`）同链路热生效：保存/删除即改下次 tools/call 的
	// spawn 环境（无需重注册工具、零新增主题）。
	sandboxRaw, _ := d[toolSandboxUserConfigKey].(string)
	s.applyToolSandbox(sandboxRaw)
}

// onPrjConfigRefresh prj 配置域保存/删除广播（data-prj-config-refresh，**既有主题**，61-消息一览 §3；
// 同源范式见 src/lib/gui/loglevel.go 与 codegraph 插件（chonkpilot-plugins/codegraph））→ 执行配置**保存即生效**（P0-B）：
// 命中 timeout_sec / max_concurrency / skip_dirs 时重跑 loadExecConfig 注入内嵌 mcp-server Config
// （execTimeout / limiter 上限 / defaults.skip_dirs 都是 tools/call 运行期读取点，无需重开项目或重启）；
// delete（重置）→ loadExecConfig 回落 mcpms 内置默认，同样即时收敛。
// 载荷 {instance_id?, id, ids?, op, list}（persist refresh 广播）：**批量写**（61 §3.1）带 `ids`
// （全组键）→ 命中任一执行配置键即处理；缺省回落单键 `id`（向后兼容）。instance_id 缺失（无归属
// 写入）→ 对全部在线实例各重跑一次（单实例/单 workdir 典型形态等价）。
// 异步执行：广播在 persist save 的同步派发链路内，而重跑含同步派发的总线 data-* 请求——
// 同步处理会阻塞保存应答（处置同 onUserConfigRefresh）。
func (s *Server) onPrjConfigRefresh(_ string, payload []byte) {
	if s.mcpCfg == nil {
		return // DisableMCP / 无内嵌 mcp-server：无执行配置可注入
	}
	var ev struct {
		InstanceID string         `json:"instance_id"`
		ID         string         `json:"id"`
		IDs        []string       `json:"ids"`
		List       map[string]any `json:"list"`
	}
	if json.Unmarshal(payload, &ev) != nil {
		return
	}
	keys := ev.IDs
	if len(keys) == 0 {
		keys = []string{ev.ID}
	}
	hit := ""
	for _, k := range keys {
		if prjExecConfigKeys[k] {
			hit = k
			break
		}
	}
	if hit == "" {
		return // 非执行配置键（logLevel / 压缩阈值等）→ 本链路空转
	}
	ids := []string{}
	if ev.InstanceID != "" {
		ids = append(ids, ev.InstanceID)
	} else {
		for _, info := range s.im.List() {
			ids = append(ids, info.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	go func() {
		defer recoverLog("onPrjConfigRefresh")
		for _, id := range ids {
			s.loadExecConfig(id)
		}
		logf("[chonkpilot-server] prj 执行配置变更热生效: %s=%v（instance=%v）\n", hit, ev.List[hit], ids)
	}()
}

// prjConfigValues 批量读 prj config 多键值字符串（门面 ConfigKVGet **一次往返**）——
// 同域 / 同读序（prjusr → prj → usr）、同缺口语义（未配置 → ""）。
// 读取失败 → 空表（且记一次日志）；调用方按「未配置」处理（保留现值）。
func (s *Server) prjConfigValues(instanceID string, keys ...string) map[string]string {
	if len(keys) == 0 {
		return nil
	}
	res, err := s.cfg.ConfigKVGet(facade.ConfigKVGetRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID,
		Keys: keys, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] prjConfigValues: ConfigKVGet(%v) failed: %v\n", keys, err)
		return nil
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = strings.TrimSpace(res.Values[k])
	}
	return out
}

// asAnySlice 归一化「领域对象数组」取值：门面 **inline 绑定**（同进程直调）给出内核/领域形态
// `[]map[string]any`（persist 读取时不经序列化），**序列化绑定**（mq / http）给出 `[]any`。
// 两种形态都认 —— 调用方只认"对象数组"这一领域事实（23 §7：绑定由装配期选定，
// 业务代码不得依赖某一种绑定的内存形态）。
func asAnySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []map[string]any:
		out := make([]any, 0, len(t))
		for _, m := range t {
			out = append(out, m)
		}
		return out
	}
	return nil
}

// cfgScope 取实例数据根（门面请求的 Scope 提示）：本服务实例视图（instance-register 维护，
// 与 persist 自持视图同源）——门面据此在**未登记实例**的场景下也能解析数据根；未知 → 空
// （门面回落自身视图/data 绑定表）。
func (s *Server) cfgScope(instanceID string) facade.Scope {
	if instanceID == "" {
		return facade.Scope{}
	}
	info, ok := s.im.Lookup(instanceID)
	if !ok {
		return facade.Scope{}
	}
	return facade.Scope{WorkDir: info.WorkDir, DataDir: info.DataDir}
}

// newID 生成短随机 id。
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
