// 用户维护 MCP 的启动装配（21-llm-server §2.1 / D-18）：
// 把「四级文件化 MCP 配置」(`<级别>/capability/mcps/<名>.json`；同名最具体级优先、整条覆盖)
// 转换为 gateway 下游 ServerEntry，作为 Params.Servers 传入内嵌 gateway
// （RB-2：这是 gateway 接入列表的**唯一**来源——gateway lib 不读文件）。
//
// **单体（chonkpilot-gui / chonkpilot-cli 内嵌本 llm server）的 MCP server 配置一律取自
// 四级文件化视图（app / user / project / prjusr）**（2026-10-01 起旧 usr KV `mcpServers`
// 彻底废弃、代码零兼容），也不读 exe 同目录 config.json 的 `mcpServers` 段
// （该段属 mcp-server / mcp-gateway **独立 exe** 自身行为，见 25-mcp-server / 26-mcp-gateway）。
//
// 读取时机 = New（gateway 构造前），经 **data 门面**（`s.cfg` = McpAPI 的 inline 绑定，同进程
// 直调 McpList）读四级视图，**不持库句柄**（2026-09-21：原直开 usr 库 data.OpenSharedLayer 已删
// ——库句柄不出 data 组件）。门面调用是进程内直调，无"服务未 Start"时序问题。
//
// D-18 结论：① scope=global（ServerEntry.Scope 留空）；② 启用标识 = 条目 enabled 字段
// （四级视图已按名合并，同名只生效最具体级一份）；③ 保存即生效（T-25：data-mcp-refresh →
// 增量对账，servers/unregister + servers/register，零新增主题；见 reconcileUserMCPs）。
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// mcpEntry 是单条 MCP 定义（字段名对齐 servers.list 规范 / 前端表单 / 四级 MCP 配置文件的字段）。
// runtime 与 url 至少其一：有 runtime → spawned（网关拉起），仅 url → proxied（连接已运行 server）。
// runtime = 可执行/解释器（单个 token），args = 启动参数（逐个 exec 参数，不做 shell/引号解析）。
type mcpEntry struct {
	Name        string            `json:"name"`
	Runtime     string            `json:"runtime"`
	Args        []string          `json:"args"`
	URL         string            `json:"url"`
	Enabled     bool              `json:"enabled"`
	Description string            `json:"description"`
	Transport   string            `json:"transport"`
	Category    string            `json:"category"`
	Namespace   string            `json:"namespace"`
	Env         []string          `json:"env"`
	Headers     map[string]string `json:"headers"`
	Cwd         string            `json:"cwd"`
	HotTools    []string          `json:"hot_tools"`
	TimeoutSec  int               `json:"timeout"`
	// Isolate 按 workdir 隔离连接（nil = 未设置 → gateway 按 transport 推断：stdio→true / http|sse→false）；
	// 三态用指针表达，使「显式 false」与「未配置」可区分（旧配置缺该键 = 未设置，行为不变）。
	Isolate *bool `json:"isolate,omitempty"`
	// Sandbox 是 agentbox 沙箱开关（**仅 stdio** 的 spawn 生效；nil/未设置 = 不隔离 = 默认兼容）。
	// 与 Isolate 独立：Isolate 管连接池隔离，Sandbox 管「可读 / 可写目录（递归）」策略下发。
	Sandbox *bool `json:"sandbox,omitempty"`
}

// loadGatewayServers 装配下游 server 列表（启动期 / 实例注册 / 配置刷新均经此）：**四级文件化
// MCP 配置**（`<级别>/capability/mcps/<名>.json`；同名最具体级优先、整条覆盖，由数据层 McpList
// 给出）→ 过滤（enabled=false / runtime、url 皆空跳过并记日志）→ ServerEntry。
//
// instanceID 决定数据层可见的实例级根（project / prjusr）；空 = 启动期（尚无实例 → 仅 app + user
// 级），非空 = 该实例已登记（project/prjusr 级 MCP 可见）。**必须透传**：否则多实例下会因"唯一
// 实例回退"串库/失败（见 reconcileUserMCPs）。
//
// **同名跨级只生效一份**：数据层 McpList 已按名合并（整条覆盖），故同一名在最终列表中恒只出现
// 一条（gateway 侧只 spawn/注册一份，见 reconcileUserMCPs）。
func (s *Server) loadGatewayServers(instanceID string) []mcpgateway.ServerEntry {
	return mergeGatewayServers(nil, s.loadMcpFileEntries(instanceID))
}

// loadMcpFileEntries 读四级文件化 MCP 配置（经 data 门面 McpList；同名最具体级优先、整条覆盖）
// → 定义数组。instanceID 透传给数据层作实例级根解析（空 = 启动期无实例）。门面不可用 / 读取
// 失败 → 空（不阻断启动）。
func (s *Server) loadMcpFileEntries(instanceID string) []mcpEntry {
	if s.cfg == nil {
		return nil
	}
	resp, err := s.cfg.McpList(facade.McpListRequest{InstanceID: instanceID})
	if err != nil {
		logf("[chonkpilot-server] mcp 四级配置读取失败：%v\n", err)
		return nil
	}
	out := make([]mcpEntry, 0, len(resp.List))
	for _, srv := range resp.List {
		b, err := json.Marshal(srv)
		if err != nil {
			continue
		}
		var e mcpEntry
		if json.Unmarshal(b, &e) != nil {
			continue
		}
		if e.Name == "" {
			e.Name = srv.Name
		}
		out = append(out, e)
	}
	return out
}

// mergeGatewayServers 合并（可选）基底/文件定义并映射为 ServerEntry（纯函数，便于单测）：
//   - system 为可选基底来源（生产装配恒传 nil：单体不读 exe config.json，仅单测用于覆盖合并规则）；
//   - 同名以文件定义覆盖基底（基底作底层，用户定义优先）；
//   - runtime、url 皆空跳过（无连接点，不能拉起也不能代理）；enabled=false 跳过；
//   - 映射（字段名与 servers.list 规范同名）：ID/Name ← name、Runtime/Args ← runtime/args（spawned）、
//     URL ← url（proxied）、Description/Category/Namespace/Env/Headers/Cwd/HotTools/TimeoutSec
//     原样透传；Transport ← transport（"direct" 归一为空 → gateway 按 runtime/url 推断 stdio/http）、
//     Scope 留空 = global；
//   - **来源（Origin）**：system 基底 = builtin（仅供单测覆盖；生产不传）；
//     文件化 MCP 条目 = user（用户定义）；覆盖同名时来源随之为 user（安全默认）。
func mergeGatewayServers(system, entries []mcpEntry) []mcpgateway.ServerEntry {
	byName := map[string]mcpEntry{}
	originByName := map[string]string{}
	order := make([]string, 0, len(system)+len(entries))
	put := func(e mcpEntry, origin string) {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			return
		}
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = e
		originByName[name] = origin
	}
	for _, e := range system {
		put(e, mcpgateway.OriginBuiltin)
	}
	for _, e := range entries {
		put(e, mcpgateway.OriginUser) // 文件定义覆盖基底同名（来源亦归 user）
	}

	out := make([]mcpgateway.ServerEntry, 0, len(order))
	for _, name := range order {
		e := byName[name]
		runtime := strings.TrimSpace(e.Runtime)
		url := strings.TrimSpace(e.URL)
		// 规范校验：name 必填（put 已保证）+ runtime/url 至少其一。
		if runtime == "" && url == "" {
			logf("[chonkpilot-server] mcp %q runtime/url 皆空，跳过（无连接点）\n", name)
			continue
		}
		if !e.Enabled {
			logf("[chonkpilot-server] mcp %q 未启用（enabled=false），跳过\n", name)
			continue
		}
		transport := normalizeMCPTransport(e.Transport)
		// 传输语义提示（判据与 gateway buildConn 一致；真正的失败由网关报错并置 failed 状态）：
		// 显式 http/sse 必须有 url；显式 stdio 时 url 被忽略（stdio 只走 runtime）。
		if (transport == "http" || transport == "sse") && url == "" {
			logf("[chonkpilot-server] mcp %q transport=%s 缺 url（http/sse 必须带 url），网关将按连接失败处理\n", name, transport)
		}
		if transport == "stdio" && url != "" {
			logf("[chonkpilot-server] mcp %q transport=stdio：url 被忽略（%s），仅按 runtime 拉起子进程\n", name, url)
		}
		out = append(out, mcpgateway.ServerEntry{
			ID:          name,
			Name:        name,
			Description: e.Description,
			Category:    e.Category,
			Runtime:     runtime, // 非空 → 网关 spawned（拉起 + 连接）
			Args:        e.Args,  // 逐个 exec 参数（不做 shell/引号解析）
			URL:         url,     // 非空 → proxied（连接已运行 server）
			Transport:   transport,
			Enabled:     true, // 已按 enabled 过滤，此处恒为 true
			TimeoutSec:  e.TimeoutSec,
			Namespace:   e.Namespace,
			Env:         e.Env,
			Headers:     e.Headers,
			Cwd:         e.Cwd,
			HotTools:    e.HotTools,
			Isolate:     e.Isolate, // nil = 未设置 → gateway 按 transport 推断
			Sandbox:     e.Sandbox, // nil/未设置 = 不隔离（默认兼容）；仅 stdio 的 spawn 下发策略
			Scope:       "",        // global（D-18 ①）
			Origin:      originByName[name],
		})
	}
	return out
}

// normalizeMCPTransport 归一前端 transport 取值 → gateway 传输名：
// "direct"（前端缺省，等价直连 URL）与空 → ""（gateway 按 runtime/url 推断 stdio/http）；
// 其余（sse/http/stdio）原样透传。
func normalizeMCPTransport(t string) string {
	t = strings.TrimSpace(t)
	if t == "" || strings.EqualFold(t, "direct") {
		return ""
	}
	return t
}

// ─── 保存即生效（T-25：enabled 开关 / 增删改 无需重启）────────────────

// userMCPServerSpec 把 MCP 条目映射为 servers/register 的 mcp_server 进程规格
// （字段与 servers.list / gateway regServerSpec 对齐；来源恒为 user——外部/用户定义）。
func userMCPServerSpec(e mcpgateway.ServerEntry) map[string]any {
	return map[string]any{
		"runtime":      e.Runtime,
		"args":         e.Args,
		"url":          e.URL,
		"transport":    e.Transport,
		"description":  e.Description,
		"category":     e.Category,
		"namespace":    e.Namespace,
		"env":          e.Env,
		"headers":      e.Headers,
		"cwd":          e.Cwd,
		"hot_tools":    e.HotTools,
		"timeout":      e.TimeoutSec,
		"isolate":      e.Isolate,     // nil = 未设置（JSON null）→ gateway 按 transport 推断
		"sandbox":      e.Sandbox,     // nil = 未设置（JSON null）→ 不隔离（仅 stdio spawn 下发策略）
		"sandbox_dirs": e.SandboxDirs, // agentbox 允许目录快照（security-* → {dir,writable}）
		"origin":       mcpgateway.OriginUser,
	}
}

// registerUserMCP 经 gateway 方法面登记一条 MCP 条目（servers/register，scope=global）。
// 网关内部完成 spawn/连接 + tools/list（失败返回明确错误，不阻塞其他条目）。
func (s *Server) registerUserMCP(e mcpgateway.ServerEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := s.emitGateway(ctx, "servers/register", map[string]any{
		"name": e.ID, "kind": mcpgateway.KindServer,
		"mcp_server": userMCPServerSpec(e),
	})
	return err
}

// unregisterUserMCP 经 gateway 方法面注销一条 MCP 条目（servers/unregister，scope=global）：
// 网关断连/kill 子进程并清空该条目全部工具路由。
func (s *Server) unregisterUserMCP(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := s.emitGateway(ctx, "servers/unregister", map[string]any{"name": name})
	return err
}

// reconcileUserMCPs 重扫 MCP 四级文件配置视图（loadGatewayServers(instanceID)）并与「已下发给
// gateway 的集合」做增量对账（T-25：开关/增删改保存即生效，无需重启）：
//   - 变更/停用/删除的条目（含 enabled=false 与 runtime/url 皆空）→ servers/unregister；
//   - 新增/变更的条目 → servers/register（完整进程规格，含 cwd/env/headers/hot_tools）；
//   - **未变更条目一律不动**（不重连、不影响在飞调用）；
//   - 复用既有消息面（servers/register / servers/unregister），**零新增主题**；
//   - 单项失败只记日志并保留旧记录（下次变更重试），不影响其余条目与既有会话。
//
// instanceID 由触发方透传（instance-register 载荷 / data-mcp-refresh 载荷 / prj-security 变更载荷），
// 决定数据层可见的实例级根（project/prjusr）；**不得留空**（除启动期 Server.New 外）——留空会
// 落到"唯一实例回退"，多实例下串库/失败（见 A 缺陷修复）。
func (s *Server) reconcileUserMCPs(instanceID string) {
	if s.gw == nil {
		return
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if s.mcpApplied == nil {
		s.mcpApplied = map[string]mcpgateway.ServerEntry{}
	}
	next := map[string]mcpgateway.ServerEntry{}
	entries := s.loadGatewayServers(instanceID)
	s.applyGatewaySandboxDirs(entries) // agentbox：允许目录快照（按当前在线实例的 security-* 汇总）
	for _, e := range entries {
		next[e.ID] = e
	}
	changed := false
	// ① 注销：已下发但新集合缺失或已变更（先拆后建；register 对同名 connected 会拒绝）。
	for name, old := range s.mcpApplied {
		if nw, ok := next[name]; ok && reflect.DeepEqual(nw, old) {
			continue
		}
		if err := s.unregisterUserMCP(name); err != nil {
			logf("[chonkpilot-server] mcp %q 热注销失败（保留原状，待下次变更重试）: %v\n", name, err)
			continue
		}
		logf("[chonkpilot-server] mcp %q 已热注销\n", name)
		delete(s.mcpApplied, name)
		changed = true
	}
	// ② 注册：新集合有而（注销后）未登记者。
	for name, nw := range next {
		if old, ok := s.mcpApplied[name]; ok && reflect.DeepEqual(old, nw) {
			continue
		}
		if err := s.registerUserMCP(nw); err != nil {
			logf("[chonkpilot-server] mcp %q 热注册失败: %v\n", name, err)
			continue
		}
		logf("[chonkpilot-server] mcp %q 已热注册（热生效）\n", name)
		s.mcpApplied[name] = nw
		changed = true
	}
	if changed {
		// 网关注册/注销亦广播 mcp-gateway-changed 触发刷新；此处兜底保证本端工具面即时收敛。
		s.refreshTools()
	}
}

// onUserConfigRefresh usr 配置域保存/删除广播（data-user-config-refresh，61-消息一览 §3）→
// usr `tool_async` 工具级异步配置热生效（reloadToolAsyncOverrides）+ usr `llms` → router 动态
// 增减（reconcileLLMProviders）。非上述键的变更（theme/超时等）由增量比较天然空转，不产生任何
// 网关动作。
//
// 注：MCP 下游对账**不经**本入口（配置来源已改四级文件化视图，由 data-mcp-refresh 触发，见
// onMCPConfigRefresh）。
//
// 异步执行：refresh 广播在 persist save 的同步派发链路内（同一总线同步派发）；reconcileLLMProviders
// 纯内存可同步，但统一放后台避免阻塞保存应答。
func (s *Server) onUserConfigRefresh(_ string, _ []byte) {
	go func() {
		s.reloadToolAsyncOverrides() // 工具异步配置：变化才重注册（秒级生效，零新增主题）
		s.reconcileLLMProviders()    // usr `llms` → router 动态增减（LR-11；纯内存，零额外主题）
	}()
}

// onMCPConfigRefresh MCP 四级文件配置域保存/删除广播（data-mcp-refresh，61-消息一览 §3）→
// 下游 server 增量对账（保存即生效；reconcileUserMCPs 读四级文件视图）。
//
// 载荷带 `instance_id`（61 §3）→ 透传给对账，使目标实例的 project/prjusr 级 MCP 正确解析
// （不再落到"唯一实例回退"）。
//
// 异步执行：refresh 广播在 persist save 的同步派发链路内，对账含 spawn/connect/tools-list
// （可达数十秒）——必须放后台，否则阻塞保存应答。
func (s *Server) onMCPConfigRefresh(_ string, payload []byte) {
	var ev struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &ev)
	go s.reconcileUserMCPs(ev.InstanceID)
}
