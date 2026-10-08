// Package mcpgateway 是 chonkpilot MCP 网关库（tools-only 聚合，纯 lib）。
//
// 定位（对齐 26-mcp-gateway，2026-08-29 收敛 tools-only；降级纯执行层）：
//   - 无独立 exe / 无条件编译 / 无 CLI；由 chonkpilot-server import 内嵌（New/Start/Stop）
//   - 上游通道 = 进程内内存 Bus（chonkpilot-lib/mq，唯一实现，2026-09-05 D1 去 NATS）：
//     复用调用方总线（Params.Bus 必填），方法面 <域>-<动词> 相对主题 + 同主题 promise
//     （Emit/On 同一条主题：订阅者写 v.Result，发送方 await 同一主题；payload 无 req_id、
//     无 -reply 主题，2026-09-05 与 mq 内核对称）；事件/心跳/退出为广播
//   - 下游 = 标准 MCP client：默认对接 chonkpilot-mcp-server（内置默认 servers.list），
//     也可经 servers.list 声明其他 server（spawned runtime= + args= / proxied url=）
//   - 原语面 = 工具路由 + 目录资产：tools 全量聚合；agent/prompt/skill/resource 经
//     mcp.register（kind）注册为目录资产（供 mcp_find/mcp_load，不参与 tool-call）；
//     下游 mcp-server 的 prompts/resources/skills 由其对标准 MCP client 直接提供
//   - 任务编排提升 server（§9.9）：gateway 为纯执行层——tools/call 执行（超时/pending/
//     后台 goroutine/取消）+ 完成回报 tasks.done 仅发 server（订阅方收敛为 server，
//     不对前端广播任务事件）；目录/接入变化 = 单一 mcp-changed 通知（tool/resource/skill/prompt/agent/mcp-server）
package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// Params 是网关配置（对齐 26-mcp-gateway，tools-only 精简版；2026-09-07 收敛：
// 能力源 = 装配方自建的官方 go-sdk server（MCPServer 参数传入），gateway 不自建 mcp-server 容器）。
type Params struct {
	// Bus 是上游总线（chonkpilot-lib/mq 进程内内存实现，唯一通道；**必填**——
	// 11-MQ与消息：内嵌模式同进程共享调用方总线，2026-09-05 D1 去 NATS 后无自建后端）。
	Bus mq.Bus
	// MCPServer 装配方传入的官方 go-sdk server（能力源：capability 契约已经
	// chonkpilot-mcp-server/server.RegisterContracts 注册；gateway 自持工具（meta/域注册）
	// 随后 AddTool 注入其上），作为内置 self 节点聚合——gateway 聚合时 tools 来自其 list。
	// nil = 装配方不提供内置能力源（仅外部下游）。
	MCPServer *mcp.Server
	// MCPConfig 是 dir 节点 / self 共享的**执行配置**（RB-2：窄接口，gateway 只经
	// `SetToolSandbox` 施加工具级沙箱开关，**不依赖具体 mcp-server 类型**）。装配方注入；
	// nil = 无共享执行配置（独立 gateway / 单测）→ 工具级沙箱开关无处施加，跳过。
	MCPConfig SandboxConfig
	// ContractScanner 是**契约目录扫描器**（RB-2：依赖倒置，同 ExecSink 手法）：gateway 不读
	// 「源」（目录/文件），`servers/register{dir}` 的「扫目录 + 建官方 server」由持有 mcp-server
	// 实现的一方（装配方）注入。nil = 未注入 → dir 注册返回明确错误（gateway 可独立运行）。
	ContractScanner ContractScanner
	// Servers 接入列表（推荐：由调用方从 usr 层 chonkpilot.db 读取后解析传入，26-mcp-gateway）。
	// RB-2：gateway **不读 servers.list 文件**——读文件由面层（exe main / 装配方）完成后传入。
	Servers []ServerEntry
	// NsPrefix 目录定义工具统一前缀（防与下游冲突）。
	NsPrefix string
	// CallTimeout 全局默认调用超时（默认 60s）。
	CallTimeout time.Duration
	// ShutdownGrace spawned 下游优雅退出宽限（默认 5s）。
	ShutdownGrace time.Duration
	// CBThreshold / CBCooldown 熔断失败阈值 / 冷却期（默认 5 / 30s）。
	CBThreshold int
	CBCooldown  time.Duration
	// MaxTasks 并发任务上限（默认 8）。
	MaxTasks int
	// AsyncMode 全局工具异步模式覆盖（"" = 使用工具 meta 默认"auto"；"never" = 强制同步，
	// 超时即失败；"always" = 强制后台；"auto"/"manual" = 同含义）。CLI 传 "never"。
	AsyncMode string
	// ManageAddr 管理 REST（仅本机）：GET /mcp/list、/mcp/check；空 = 禁用。
	ManageAddr string
	// ExecSink 是任务层窄接口（P3：gateway 执行态纳入任务层；2026-09-18：取消/转后台改由
	// 层 + 进程内 sink 提供，见 21 §9.3）。由装配方（chonkpilot-llm/server）注入层适配器；
	// **未注入 = nil → 全部 no-op**（库内调用 / 单测 / gateway 单体 exe 行为与引入前等价）。
	ExecSink ExecSink
	// DSLExecutor 是 `dsl_run` 执行器 exe 的显式路径覆盖（空 = 自动解析：exeDir 同级，
	// 或 exeDir/capability/executors/；见 dslrun.go dslExecutorPath）。契约
	// `capability/tools/core/dsl_run.tool.md` 对应的运行体由 gateway spawn（决策 42 §2 (247)）。
	DSLExecutor string
	// ServerTools 是 **category=server 契约工具**的定义单源（key = 原语名，如 dsl_run）。
	// 由装配层经 `mcp-server/server.ServerTools(root, cfg)` 注入（gateway lib 不依赖 mcp-server 包，
	// RB-2）；gateway 自持节点（dsl 等）**优先取之**为工具定义，缺失 → 回落内置定义。
	// 该类契约不再注册为 executor 工具（不出现于 self 节点），故这是其唯一定义来源。
	ServerTools map[string]*mcp.Tool
	// Logf 日志输出（默认 log.Printf）。
	Logf func(format string, args ...any)
}

// Gateway 是 MCP 网关实例。
type Gateway struct {
	params Params
	logf   func(string, ...any)

	bus mq.Bus // 上游总线（Params.Bus，唯一通道）

	reg     *registry
	tm      *execPool
	regProv *registeredProvider // 域工具注册执行器（server tools.register 的本地/远程回调）
	// tov 是工具级覆盖表（usr tool_async/tool_sandbox 的归一结果，装配方注入；见 tooloverride.go）：
	// 在 tools/list `_meta` 与 doCall 异步判定处按暴露名统一施加（含 dir 节点/第三方工具，I-82）。
	tov toolOverrideState
	// self 节点：Params.MCPServer（能力源 + gateway 注入工具）的 in-memory 会话；
	// 聚合时 tools 来自其 list（registerProvider 一次，注入工具后经 reconcileSelf 刷新）。
	self    *memNode
	selfKey string
	nodes   map[string]*dirNode // dir 目录节点（key = provKey("dir", scope, name)）句柄
	mu      sync.Mutex
	stopped bool

	mgmtSrv *http.Server
}

// New 构建网关（不启动；随后 Start）。
// Bus 为必填（进程内内存 MQ 是唯一上游实现，2026-09-05 D1 去 NATS）。
// MCPServer 可为 nil（无内置能力源）；不为 nil 时 Start 建 self 节点聚合。
func New(p Params) (*Gateway, error) {
	if p.Bus == nil {
		return nil, fmt.Errorf("gateway: Bus is required (in-process mq is the only transport)")
	}
	if p.CallTimeout <= 0 {
		p.CallTimeout = 60 * time.Second
	}
	if p.ShutdownGrace <= 0 {
		p.ShutdownGrace = 5 * time.Second
	}
	if p.CBCooldown <= 0 {
		p.CBCooldown = 30 * time.Second
	}
	if p.MaxTasks <= 0 {
		p.MaxTasks = 8
	}
	if p.Logf == nil {
		p.Logf = log.Printf
	}
	g := &Gateway{
		params:  p,
		logf:    p.Logf,
		reg:     newRegistry(),
		bus:     p.Bus,
		regProv: newRegisteredProvider(p.Bus, p.CallTimeout),
		nodes:   map[string]*dirNode{},
	}
	g.tm = newExecPool(p.MaxTasks, g.reportTaskDone, g.onTaskCancel, g.onExecState)
	return g, nil
}

// ── 任务层窄接口（P3：执行态上报 + 控制面以层为准）─────────────────────────

// onExecState 执行态上报 → 任务层（P3-①：层内 API 直调，**不走 MQ**）。
// 未注入层 → no-op；层内失败只由层自身告警（本方法无返回值，绝不影响 gateway 调用路径）。
func (g *Gateway) onExecState(ev ExecState) {
	if g.params.ExecSink == nil {
		return
	}
	g.params.ExecSink.OnExecState(ev)
}

// layerState 查任务层权威状态（P3-②）。第二个返回值 false = 未注入层 / 层未登记该执行 id
// → 调用方**回落既有 gateway 池判定**（行为等价）。
func (g *Gateway) layerState(gwTaskID string) (string, bool) {
	if g.params.ExecSink == nil || gwTaskID == "" {
		return "", false
	}
	return g.params.ExecSink.ExecStateOf(gwTaskID)
}

// layerImmutableTerminal 判定层权威状态是否**不可逆终态**（done/error/cancelled；与任务层
// `immutableState` 同口径）：层已终态 → 不再下发执行侧取消。
func layerImmutableTerminal(state string) bool {
	switch state {
	case "done", "error", "cancelled":
		return true
	}
	return false
}

// Start 启动网关：建 self 节点（MCPServer 非空）→ 加载 servers.list + 接入下游 → 订阅方法面 → 管理 REST。
func (g *Gateway) Start(ctx context.Context) error {
	// self 节点：能力源（Params.MCPServer）in-memory 会话 → 工具聚合（list 来源）
	if g.params.MCPServer != nil {
		if err := g.connectSelf(ctx); err != nil {
			return fmt.Errorf("connect self: %w", err)
		}
		// gateway 自持 meta 工具注入 self（聚合 tools 来自其 list）
		g.registerMetaTools()
	}

	// dsl_run 工具：gateway 自持 in-memory 节点（暴露名 = dsl_run；spawn 独立受保护执行器，
	// 决策 42 §2 (247)）。契约（capability/tools/core/dsl_run.tool.md）为单源定义；
	// 失败不阻塞启动（仅告警，工具面缺 dsl_run）。
	if err := g.registerDSLNode(ctx); err != nil {
		g.logf("[gateway] dsl node 注册失败（dsl_run 不可用）: %v", err)
	}

	// 加载接入列表：**只接成品**（RB-2）——调用方（exe main / 装配方）读 usr db 或 servers.list
	// 后经 Params.Servers 传入；gateway lib 不读文件（空列表 = 工具面仅来自 self 能力源，不报错）。
	entries := g.params.Servers
	if len(entries) == 0 {
		g.logf("[gateway] 无 servers 配置源（Params.Servers 为空），接入列表为空，仅使用 self 能力源")
	}
	for _, e := range entries {
		if !e.Enabled {
			g.logf("[gateway] server %s disabled, skip", e.ID)
			continue
		}
		if err := g.connectServer(ctx, e); err != nil {
			g.logf("[gateway] server %s connect failed: %v", e.ID, err)
		}
	}

	// 订阅方法面 + 广播
	if err := g.subscribeAll(); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	// 管理 REST
	if g.params.ManageAddr != "" {
		g.startManage(g.params.ManageAddr)
	}
	g.logf("[gateway] started, tools=%d", len(g.reg.allTools()))
	return nil
}

// connectSelf 连接参数能力源 server 为 self 节点（global scope；聚合 = 其 tools list）。
func (g *Gateway) connectSelf(ctx context.Context) error {
	node, err := newMemNode("self", g.params.MCPServer)
	if err != nil {
		return err
	}
	g.self = node
	g.selfKey = provKey("mem", scopeGlobal, "self")
	tools, err := node.ListTools(ctx)
	if err != nil {
		node.Close()
		return fmt.Errorf("self list tools: %w", err)
	}
	ps := &providerState{
		key:    g.selfKey,
		entry:  &ServerEntry{ID: "self", Name: "内置能力源", Category: "core", Origin: OriginBuiltin},
		prov:   &memNodeProvider{node: node, inject: true}, // self=内置来源：调用以协议 _meta 透传调用上下文
		status: "connected",
		cb:     newBreaker(g.params.CBThreshold, g.params.CBCooldown, true),
		scope:  scopeGlobal,
	}
	if err := g.reg.registerProvider(ps, tools, ""); err != nil {
		node.Close()
		return err
	}
	g.logf("[gateway] self node connected: %d tools", len(tools))
	return nil
}

// reconcileSelf 刷新 self 节点工具路由（gateway 注入工具 AddTool/RemoveTools 后调用：
// 移除旧路由 → 重拉 list 注册，AddTool 已生效故新 list 含注入工具）。
func (g *Gateway) reconcileSelf() error {
	if g.self == nil || g.selfKey == "" {
		return nil
	}
	ps, ok := g.reg.provider(g.selfKey)
	if !ok {
		return nil
	}
	tools, err := g.self.ListTools(context.Background())
	if err != nil {
		return err
	}
	g.reg.removeProvider(g.selfKey)
	return g.reg.registerProvider(ps, tools, "")
}

// Stop 优雅退出：停收新请求 → 关闭下游（spawned 收尾）→ 取消 in-flight task。
// 上游 Bus 归调用方所有，不在本方法关闭（chonkpilot-lib/mq 由宿主统一生命周期管理）。
func (g *Gateway) Stop(ctx context.Context) error {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return nil
	}
	g.stopped = true
	g.mu.Unlock()

	g.logf("[gateway] stopping ...")
	// spawned/proxied 下游收尾（Close 终止 stdio 子进程）。
	for _, key := range g.reg.order {
		if ps, ok := g.reg.provider(key); ok {
			if err := ps.prov.Close(); err != nil {
				g.logf("[gateway] provider %s close: %v", key, err)
			}
		}
	}
	// 管理 REST
	if g.mgmtSrv != nil {
		_ = g.mgmtSrv.Shutdown(ctx)
	}
	return nil
}

// ─── 总线订阅 ──────────────────────────────────────────

// subscribeAll 订阅方法调用主题（2026-09-06 统一 mcp 域：相对主题 mcp-<组>-<动作>，
// chonk. 前缀由外部注入总线的 Options.Prefix 提供）。
// 方法面 = mq 对称 promise：handler 执行后写回 v.Result（失败返回 error → 收集进 v.Errors），
// 无独立 -reply 主题（2026-09-05 移除 reply 过渡期机制）。
// 方法集（2026-09-06 重构）：标准原语（list/call/get/read）+ 分组注册/管理
// （tools|prompts|resources|servers 的 register/unregister、servers/get、
// gateway/check|reload）；扩展方法**始终注册**（不再 --manage 门控）。
// 2026-09-18：`tools/background`、`tasks/list|status|result|cancel` 方法面**已移除**
// （用户决定）：转后台 / 取消改由任务层 + 进程内 sink 直调（`(*Gateway).DetachExec` /
// `(*Gateway).CancelExec`，见 tasks.go ExecSink）；异步结果以 message 表为准。
// heartbeat / instance-exit 已不消费：实例生命周期为 GUI 客户端消息（instance-*，
// 消费方 = server，61-消息一览 §4.1）；gateway 为内嵌执行层，随进程存续。
func (g *Gateway) subscribeAll() error {
	for _, m := range []string{
		"tools/list", "tools/call", "tools/wait",
		"tools/register", "tools/unregister",
		"prompts/list", "prompts/get",
		"prompts/register", "prompts/unregister",
		"resources/list", "resources/read",
		"resources/register", "resources/unregister",
		"servers/list", "servers/get", "servers/register", "servers/unregister",
		"gateway/check", "gateway/reload",
	} {
		method := m
		if err := g.sub(methodSubject(method), func(_ string, v *mq.Value) error {
			return g.dispatch(method, v)
		}); err != nil {
			return err
		}
	}
	return nil
}

// pub 统一发布到总线（相对主题；前缀由总线 Options.Prefix 注入；fire-and-forget = Emit 不 await）。
func (g *Gateway) pub(subject string, payload []byte) {
	_ = g.bus.Emit(context.Background(), subject, payload)
}

// sub 统一订阅总线（相对主题；handler 收到去前缀后的相对 subject 与 *Value——
// 可写回 v.Result 实现同主题请求-响应；返回 error 被收集进 v.Errors）。
// 写回的结果 map 统一归一为 JSON 形态（对齐旧 -reply 载荷 JSON 编解码后的纯 map/[]any 契约）。
func (g *Gateway) sub(subject string, fn func(subject string, v *mq.Value) error) error {
	_, err := g.bus.On(subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
		if err := fn(subj, v); err != nil {
			return err
		}
		if m, ok := v.Result.(map[string]any); ok {
			v.Result = resultJSON(m)
		}
		return nil
	})
	return err
}

// notifyMCPChanged 目录/接入变化通知（单一 mcp-changed，2026-09-05 合并 tool-changed ∪
// server-status-changed）：tool/resource/skill/prompt/mcp-server 任一变化（注册/注销/
// 接入/状态/重载）触发；fire-and-forget（发送方不 await）。
// instanceID = 本次变化归属实例（注册/注销取请求 instance_id，接入/退出观测取 provider scope）；
// 业务 payload 的 instance_id 一律必带（61-消息一览 §0），无实例上下文时为 ""。
func (g *Gateway) notifyMCPChanged(instanceID string, payload map[string]any) {
	payload["instance_id"] = instanceID
	g.pub(SubjectMCPChanged, marshal(payload))
}

// reportTaskDone 任务完成回报（§9.9，B1 分拆后主题 = task-report）：执行完成/失败/取消 →
// 回报 server（订阅方收敛为 server），不对前端广播任务事件——任务树/级联/状态事件由
// server 编排后广播（task-done/task-started/task-updated，21-llm-server）。
// payload 对齐 §3.9.2：{task_id, tool, state, result_summary, instance_id, session?, turn?}；
// 增补 tool_call_id（I-62：非 detached 任务未登记 gwTaskID，server 按 tool_call_id 兜底定位节点）
// 与 top_session / parent（I-90：任务树归属，供任务层在「未登记」时独立建节点；缺省为空）。
// **result_summary = 终态结果全文**（2026-09-18：`tasks/result` 方法面移除后，本通知面是该结果的
// 唯一交付通道 → 不再截断；server 据此把结果落 message 表 result 段并回填续轮）。
// 字段在锁内快照——cancel 路径由调用方 goroutine 回报，须与执行 goroutine 的终态写入互斥。
func (g *Gateway) reportTaskDone(t *ExecTask) {
	g.tm.mu.Lock()
	id, tool, state := t.ID, t.Tool, t.State
	toolCallID, owner, session, turn := t.ToolCallID, t.OwnerInstance, t.Session, t.Turn
	topSession, parent := t.TopSession, t.Parent
	summary := t.Error
	if t.Result != nil {
		for _, c := range t.Result.Content {
			if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
				summary = tc.Text
				break
			}
		}
	}
	g.tm.mu.Unlock()
	g.pub(SubjectTaskReport, marshal(map[string]any{
		"task_id":        id,
		"tool":           tool,
		"state":          state,
		"result_summary": summary,
		"instance_id":    owner,
		"session":        session,
		"turn":           turn,
		"tool_call_id":   toolCallID,
		"top_session":    topSession,
		"parent":         parent,
	}))
}

// broadcastToolsListChanged 与 broadcastServerStatus 已于 2026-09-05 合并为 notifyMCPChanged（mcp-changed）。

// ─── 方法分发 ────────────────────────────────────────────

// dispatch 分发方法调用：handler 成功写回 v.Result；失败返回 error（收集进 v.Errors）。
func (g *Gateway) dispatch(method string, v *mq.Value) error {
	switch method {
	case "tools/list":
		return g.handleToolsList(v)
	case "tools/call":
		return g.handleToolsCall(v)
	case "tools/wait":
		return g.handleToolsWait(v)
	case "servers/list":
		return g.handleServersList(v)
	case "servers/get":
		return g.handleServersGet(v)
	case "servers/register", "tools/register", "prompts/register", "resources/register":
		return g.handleRegByMethod(method, v)
	case "servers/unregister", "tools/unregister", "prompts/unregister", "resources/unregister":
		return g.handleUnregByMethod(method, v)
	case "gateway/check":
		return g.handleGatewayCheck(v)
	case "gateway/reload":
		return g.handleGatewayReload(v)
	case "prompts/list":
		return g.handlePromptsList(v)
	case "prompts/get":
		return g.handlePromptsGet(v)
	case "resources/list":
		return g.handleResourcesList(v)
	case "resources/read":
		return g.handleResourcesRead(v)
	}
	return nil
}

// handleRegByMethod 按原语分组注册（2026-09-06，取代 mcp.register 统一 kind）：
// tools/register → 注册工具；prompts/register → prompt/skill 目录资产（asset_kind 定类别，
// 缺省 prompt；25 §5/T2：agent 已撤出资产面）；resources/register → resource 资产；
// servers/register → 接入下游 server。
func (g *Gateway) handleRegByMethod(method string, v *mq.Value) error {
	var req regMsg
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	switch method {
	case "tools/register":
		return g.registerTool(v, req)
	case "prompts/register":
		kind := assetKindOf(req)
		if kind == "" {
			return methodError(-32602, "asset_kind %q 非法（want prompt|skill；agent 已撤出资产面）", req.AssetKind)
		}
		return g.registerAsset(v, req, kind)
	case "resources/register":
		return g.registerAsset(v, req, KindResource)
	case "servers/register":
		return g.registerServer(v, req)
	}
	return nil
}

// handleUnregByMethod 按原语分组注销（2026-09-06，取代 mcp.unregister 统一 kind）：
// tools/unregister {name}；prompts/unregister {name, asset_kind?}；resources/unregister {name}；
// servers/unregister {name}。
func (g *Gateway) handleUnregByMethod(method string, v *mq.Value) error {
	var req regMsg
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	switch method {
	case "tools/unregister":
		return g.unregisterTool(v, req)
	case "prompts/unregister":
		kind := assetKindOf(req)
		if kind == "" {
			return methodError(-32602, "asset_kind %q 非法（want prompt|skill；agent 已撤出资产面）", req.AssetKind)
		}
		return g.unregisterAsset(v, req, kind)
	case "resources/unregister":
		return g.unregisterAsset(v, req, KindResource)
	case "servers/unregister":
		return g.unregisterServer(v, req)
	}
	return nil
}

// assetKindOf 把 prompts/register 的资产类别（asset_kind）归一为资产目录 kind：
// prompt（缺省）/skill；非法返回空串。
//
// 25 §5/T2（2026-09-25）：**agent 已撤出资产面** —— `asset_kind=agent` 不再受理（agent 只经
// 系统提示词"注入"，不注册资产）。
func assetKindOf(req regMsg) string {
	switch req.AssetKind {
	case "", KindPrompt:
		return KindPrompt
	case KindSkill:
		return KindSkill
	}
	return ""
}

// handleServersGet 获取单个 server 视图（name/type/status/tools，3A-工具与编排）。
func (g *Gateway) handleServersGet(v *mq.Value) error {
	var req ServerReq
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	if req.Server == "" {
		return methodError(-32602, "server is required (servers/get)")
	}
	views := g.reg.serverViews(req.Server)
	if len(views) == 0 {
		return methodError(-32000, "server %s not found", req.Server)
	}
	v.Result = map[string]any{"server": views[0]}
	return nil
}

// handleGatewayCheck 健康检查（gateway 组方法，**始终注册**，2026-09-06 不再 --manage 门控；
// 取代原 manage REST /mcp/check）。回执 {ok, tools, servers}。
func (g *Gateway) handleGatewayCheck(v *mq.Value) error {
	v.Result = map[string]any{
		"ok":      true,
		"tools":   len(g.reg.allTools()),
		"servers": g.reg.serverViews(""),
	}
	return nil
}

// handleGatewayReload 刷新暴露缓存（2026-09-06 语义修正：只重拉已接入 provider 的
// tools/list 并重建路由，**不断开连接、不做 unregister+register**，动态注册的 server/tool
// 不受影响）。回执 {reloaded, refreshed, removed_tools}。
func (g *Gateway) handleGatewayReload(v *mq.Value) error {
	var req Context
	_ = v.JSON(&req)
	keys := g.reg.providerKeys()
	refreshed, removed := 0, 0
	for _, key := range keys {
		// reg:*（meta/注册工具）由注册方管理，不随重载刷新
		if strings.HasPrefix(key, "reg:") {
			continue
		}
		ps, ok := g.reg.provider(key)
		if !ok || ps.status != "connected" || ps.prov == nil {
			continue
		}
		if ps.ref != nil {
			// dir 共享节点（RB-3 ②）：工具按引用集即时展开、**不在 routes 物化** → 只重拉共享
			// 节点的工具表（不重建路由）；计数语义与旧实现一致（removed += 该引用侧工具数）。
			tools, err := ps.prov.ListTools(context.Background())
			if err != nil {
				g.logf("[gateway] reload: %s list failed: %v", key, err)
				continue
			}
			g.reg.refreshSharedNode(ps.ref, tools)
			removed += len(tools)
			refreshed++
			continue
		}
		tools, err := ps.prov.ListTools(context.Background())
		if err != nil {
			g.logf("[gateway] reload: %s list failed: %v", key, err)
			continue
		}
		// 原地替换路由（保留连接对象）：先移除旧路由再重注册同 provider
		removed += g.reg.removeProvider(key)
		if err := g.reg.registerProvider(ps, tools, g.params.NsPrefix); err != nil {
			g.logf("[gateway] reload: %s re-register failed: %v", key, err)
			continue
		}
		refreshed++
	}
	g.notifyMCPChanged(req.InstanceID, map[string]any{"kind": KindServer, "status": "reloaded"})
	g.logf("[gateway] reload done: refreshed %d providers, removed %d tools", refreshed, removed)
	v.Result = map[string]any{"reloaded": true, "refreshed": refreshed, "removed_tools": removed}
	return nil
}

// serverInfoOf 返回 provider 的 server 归属信息（挂进工具 `_meta.server`，2026-09-11
// 工具面契约变更）。providerKey = toolRoute.Provider；见 registry.serverInfoOf。
func (g *Gateway) serverInfoOf(providerKey string) map[string]any {
	return g.reg.serverInfoOf(providerKey)
}

func (g *Gateway) handleToolsList(v *mq.Value) error {
	var req ListReq
	_ = v.JSON(&req)
	// 2026-09-07 定稿：list 无参数全量（含所有 instance），每项带 scope/node——UI 按需获取并展示；
	// find/load 与 tools/call 仍按 instance 隔离（findFor/allToolsFor 保留）。
	// RB-3 ③（2026-09-22）：**按 instance 构建路径**——载荷带 instance_id（桥/客户端直取会注入）
	// → 仅返回「global ∪ 归属该 instance」；缺省（UI 全量 / llm 缓存）→ 全量路径（保留）。
	// 沙箱开关归一到执行配置（仅 builtin：self/dir 节点）——工具面一变化（含 dir 节点注册）即重算，
	// 保证 tools/call 前 Config 键位与最新路由一致（I-82）。
	g.applySandboxToConfig()
	routes := g.reg.routesAll()
	if req.InstanceID != "" {
		routes = g.reg.routesFor(req.InstanceID)
	}
	tools := make([]map[string]any, 0, len(routes))
	for _, rt := range routes {
		entry := map[string]any{"name": rt.Name, "scope": rt.Scope}
		if rt.Tool != nil {
			entry["description"] = rt.Tool.Description
			entry["inputSchema"] = rt.Tool.InputSchema
			// _meta = 工具自身 meta（契约 title/category/hot/find/async/… 或网关注入的
			// hot/category）**并挂上 server 归属信息**（2026-09-11：供 UI/LLM 一眼识别
			// 工具属于哪台 server，不必再查 servers/get）。写入副本，避免污染 provider 的 meta。
			meta := map[string]any{}
			for k, v := range rt.Tool.Meta {
				meta[k] = v
			}
			if srv := g.serverInfoOf(rt.Provider); srv != nil {
				meta["server"] = srv
			}
			// 工具级异步覆盖**再应用一次**（按暴露名；覆盖 dir 节点 / 第三方工具的孤儿键，
			// I-82）：self 节点已由 mcp-server 侧按契约名应用过同值 → 此处幂等。
			if ov, ok := g.asyncOverride(rt.Name); ok {
				applyAsyncOverrideMeta(meta, ov)
			}
			if len(meta) > 0 {
				entry["_meta"] = meta
			}
		}
		if rt.Provider != "" {
			entry["node"] = g.reg.nodeOf(rt.Provider)
		}
		tools = append(tools, entry)
	}
	v.Result = map[string]any{
		"resultType": "complete",
		"tools":      tools,
		"ttlMs":      3600,
	}
	return nil
}

func (g *Gateway) handleToolsCall(v *mq.Value) error {
	var req CallReq
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	res, code, msg := g.doCall(req)
	if code != 0 {
		return methodError(code, "%s", msg)
	}
	v.Result = res
	return nil
}

// ── 执行侧控制面（2026-09-18：取代 mcp-tools-background / mcp-tasks-cancel 方法面）──
//
// 两个公开方法 = 任务层（或装配方）对 **gateway 执行池** 的进程内控制入口（不经 MQ）：
// 取消复用既有 `ep.cancel`（→ onCancel → provider.Invalidate，真实打断在飞调用）；
// 转后台复用既有 `ep.detach`（→ 原同步调用返回 pending，终态经 mcp-tasks-report 回报）。
// 层侧经 `ExecSink.CancelExec` / `ExecSink.DetachExec` 转调（见 tasks.go）；gateway 可独立运行
// （无 sink 也可直接调用本方法）。

// DetachExec 把在飞的 manual/auto 执行解绑转后台（原 `tools/background` 语义）：
// `instanceID` = 归属实例（层侧取 exec.InstanceID；空 = 旧单池语义），`idOrCall` = gateway 任务 id
// 或 LLM tool-call id。返回任务 id；未命中 / 已终态 / 已解绑 / never（options 无 detach）→ error。
// 成功即上报执行态 detached{trigger:manual}（层落 exec_json）。
func (g *Gateway) DetachExec(instanceID, idOrCall string) (string, error) {
	if idOrCall == "" {
		return "", methodError(-32602, "tool_call_id or task_id is required")
	}
	taskID, err := g.tm.detach(instanceID, idOrCall)
	if err != nil {
		return "", methodError(-32602, "%s", err.Error())
	}
	g.logf("[gateway] exec %s detached to background (ref=%s)", taskID, idOrCall)
	// 执行态上报（P3-① 上报点 ③）：手工转后台 → detached{trigger:manual}（层落 detached + exec_json 明细）
	g.tm.emitExec(instanceID, taskID, ExecPhaseDetached, map[string]any{"trigger": "manual"})
	return taskID, nil
}

// CancelExec 取消执行池中的在飞执行（原 `tasks/cancel` 语义；**含已 detached 的后台任务**）。
// `instanceID` = 归属实例（层侧取 exec.InstanceID；空 = 旧单池语义），`idOrCall` = gateway 任务 id
// 或 LLM tool-call id —— **只在该 instance 桶内定位**（跨实例任务互不可见/互不可取消，缺口 2）。
// 取消前**先问层**（P3-②：层判为不可逆终态 done/error/cancelled → 拒绝，不重复下发执行侧取消；
// 层未注入 / 未登记 → 回落池判定）。
// 返回 (true, nil) = 已取消；(false, err) = 拒绝（原因见 err）。
func (g *Gateway) CancelExec(instanceID, idOrCall string) (bool, error) {
	if idOrCall == "" {
		return false, methodError(-32602, "tool_call_id or task_id is required")
	}
	if st, ok := g.layerState(idOrCall); ok && layerImmutableTerminal(st) {
		return false, methodError(-32602, "cannot cancel task in terminal state: %s", st)
	}
	if err := g.tm.cancelRef(instanceID, idOrCall); err != nil {
		return false, methodError(-32602, "%s", err.Error())
	}
	return true, nil
}

// handleToolsWait 处理「等待完成」裁决（mcp-tools-wait，统一异步模型 2026-09-13）：
// 撤销超时、继续等原调用返回并交付结果。载荷 {instance_id, tool_call_id|task_id}；
// 仅 never（options 含 wait）的非终态任务可等待。回执 {waiting: true, task_id}。
func (g *Gateway) handleToolsWait(v *mq.Value) error {
	var req struct {
		InstanceID string `json:"instance_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
		TaskID     string `json:"task_id,omitempty"`
	}
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	id := req.TaskID
	if id == "" {
		id = req.ToolCallID
	}
	if id == "" {
		return methodError(-32602, "tool_call_id or task_id is required")
	}
	taskID, err := g.tm.wait(req.InstanceID, id)
	if err != nil {
		return methodError(-32602, "%s", err.Error())
	}
	g.logf("[gateway] exec %s: user chose wait (revoke timeout)", taskID)
	v.Result = map[string]any{"waiting": true, "task_id": taskID}
	return nil
}

// runPreHooks 执行全部已注册的前置钩子（注册方经 tools/register 的 pre_hook_subject 声明；
// 2026-09-27）。与「注册工具远程回调」（§5.4）同构：Emit 到该相对主题并 await 同一主题，
// 订阅者写回 v.Result / 返回 error。载荷 {tool, context, touch_files}（context 对齐 turn 上下文 +
// work_dir；touch_files = 该工具是否「涉及文件变动」，用户口径 2026-09-28，见 resolveTouchFiles）；
// 失败 → 返回 (错误码, 消息) → 调用方拒绝该工具调用。
//
// **零开销**：无任何注册方声明钩子时 HasPreHooks() 为 false，本方法立即返回（不发消息）。
func (g *Gateway) runPreHooks(req CallReq, route *toolRoute, entry *ServerEntry) (int, string) {
	if !g.regProv.HasPreHooks() {
		return 0, ""
	}
	subjects := g.regProv.PreHookSubjects()
	if len(subjects) == 0 {
		return 0, ""
	}
	c := map[string]string{}
	if req.Session != "" {
		c["session"] = req.Session
	}
	if req.Turn != "" {
		c["turn"] = req.Turn
	}
	if req.InstanceID != "" {
		c["instance_id"] = req.InstanceID
	}
	if req.ToolCallID != "" {
		c["tool_call_id"] = req.ToolCallID
	}
	if req.TopSession != "" {
		c["top_session"] = req.TopSession
	}
	if req.Parent != "" {
		c["parent"] = req.Parent
	}
	if req.WorkDir != "" {
		c["work_dir"] = req.WorkDir
	}
	original := ""
	if route != nil {
		original = route.Original
	}
	payload := map[string]any{
		"tool":        req.Name,
		"context":     c,
		"touch_files": g.resolveTouchFiles(req.Name, original, entry),
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.params.CallTimeout)
	defer cancel()
	for _, subj := range subjects {
		v := g.bus.Emit(ctx, subj, payload).Wait()
		if err := v.Err(); err != nil {
			g.logf("[gateway] tool %s rejected by pre-hook %s: %v", req.Name, subj, err)
			return -32000, fmt.Sprintf("pre-hook %s rejected call: %v", subj, err)
		}
	}
	return 0, ""
}

// doCall 执行工具调用：路由 → 熔断 → async 判定（auto/always/never/manual）→ 统一任务化执行。
// 统一异步模型（2026-09-13，超时后交用户裁决）：
//   - always：立即后台（返回 pending），终态经 mcp-tasks-report 回报
//   - auto：**任务化启动（一次调用）**，到 async-threshold 时自动把在飞任务转后台（**不重跑**，
//     修 I-54）；提前完成则就地交付结果
//   - manual：任务化运行，到 timeout 发 mcp-tools-timeout{options:[detach,cancel]} 待裁决；
//     detach = 转后台（任务层 → 进程内 `DetachExec`），cancel = 取消（任务层 → `CancelExec`）
//   - never（第三方缺省）：任务化运行，到 timeout 发 mcp-tools-timeout{options:[wait,cancel]} 待裁决；
//     wait = 撤销超时继续等并交付，cancel = 取消；**不自动失败、不自动转后台**
//   - 配置⑤ 超时自动取消（usr `tool_async.<暴露名>.cancel_on_timeout` > 0）：manual/never 到超时点
//     时**直接取消**（不发 mcp-tools-timeout、不等用户裁决）→ 走既有取消链按执行线终止（onTaskCancel）；
//     **默认 0 = 不取消**（不配置时行为逐字节不变）；auto 语义不变（阈值命中仍转后台，不自动取消）。
//
// 裁决前任务保持 running，默认无操作保持安全（不重跑/不重复调用）；超时不计熔断失败（慢 ≠ 坏）。
// 保留键 _async/_timeout 已并入同名 async/timeout（兼容别名：仍接受 _async/_timeout 透传覆盖）。
func (g *Gateway) doCall(req CallReq) (map[string]any, int, string) {
	args := map[string]any{}
	for k, v := range req.Arguments {
		args[k] = v
	}
	// 调用级覆盖（同名 async/timeout；兼容旧保留键 _async/_timeout）
	callMode := strFrom(args, "async", strFrom(args, "_async", ""))
	delete(args, "async")
	delete(args, "_async")
	callTimeout := floatFrom(args, "timeout", floatFrom(args, "_timeout", 0))
	delete(args, "timeout")
	delete(args, "_timeout")
	// 调用展示名剥离：gateway 注入的展示用参数（LLM 必填、UI/CLI 展示），不传下游工具。
	// 命名不用 purpose，避免与工具自有 purpose（如 mcp_find 的任务目标）冲突（2026-09-11）。
	delete(args, displayNameArg)

	// scoped 路由：请求 instance 命中归属注册（遮蔽 global）；回退 global
	route, ok := g.reg.findFor(req.Name, req.InstanceID)
	if !ok {
		return nil, -32602, fmt.Sprintf("unknown tool %q", req.Name)
	}
	ps, ok := g.reg.provider(route.Provider)
	if !ok {
		return nil, -32602, "provider not found"
	}
	if !ps.cb.allow() {
		return nil, -32601, fmt.Sprintf("server %s unavailable (circuit open)", route.Provider)
	}

	// 前置打点钩子（2026-09-27）：注册方（如 history 插件）经 tools/register 的可选字段
	// pre_hook_subject 声明 → 在执行**任意**工具前先向该相对主题发一次**同步**请求；
	// 钩子失败（返回 error）→ 拒绝该工具调用（工具不执行），LLM 可见工具失败并可重试。
	// **无钩子声明时零开销**：只做一次原子计数判定，不发任何消息。
	if code, msg := g.runPreHooks(req, route, ps.entry); code != 0 {
		return nil, code, msg
	}

	// 契约默认 async 族（工具 _meta；调用级覆盖 + Params.AsyncMode 全局覆盖）
	def := toolAsyncMeta(route)
	explicitNever := def.mode == "never" // 契约显式 never：调用级不可覆盖
	mode := def.mode
	// 第三方（Origin != builtin）缺省 never：**软缺省**——工具契约未显式声明 async 时生效，
	// 调用级 async / 全局 AsyncMode 仍可覆盖（区别于契约显式 never 的锁死）。
	if softNeverDefault(route, ps.entry) {
		mode = "never"
	}
	// 工具级异步覆盖（usr `tool_async`，按**暴露名**从 gateway 侧统一施加，I-82）：
	// 覆盖 dir 节点 / 第三方工具（mcp-server 侧只按契约名覆盖 self 节点，二者不重叠）。
	// 语义（用户口径，同 mcp-server applyToolAsyncOverride）：调用级 > 本覆盖 > 契约 _meta > 软缺省；
	// 本覆盖属用户显式意图 → 解除 softNeverDefault 与契约显式 never 的锁死。
	// 阈值：显式设置即采用（含 **0/-1 = 无阈值**）；未设置 → 回落契约/超时点。
	thrSet, thrS := def.thrSet, def.thr
	cancelOnTimeout := 0 // ⑤ 超时自动取消秒数（>0 生效；0 = 不取消，用户口径 2026-10-07）
	if ov, ok := g.asyncOverride(req.Name); ok {
		if ov.Mode != "" {
			mode = ov.Mode
			explicitNever = false
		}
		if ov.ThresholdSet || ov.Threshold > 0 {
			thrS = float64(ov.Threshold)
			thrSet = true
		}
		if ov.CancelOnTimeoutSet || ov.CancelOnTimeout > 0 {
			cancelOnTimeout = ov.CancelOnTimeout
		}
	}
	if g.params.AsyncMode != "" {
		mode = g.params.AsyncMode // 全局覆盖（CLI 传 "never" 强制同步）
	}
	switch strings.ToLower(callMode) {
	case "auto", "always", "never", "manual":
		mode = strings.ToLower(callMode)
	case "true", "1":
		mode = "always"
	case "false", "0":
		mode = "auto"
	}
	if explicitNever {
		mode = "never" // 契约显式 never 不可被调用级覆盖
	}
	switch mode {
	case "auto", "always", "never", "manual":
	default:
		mode = "auto"
	}

	// 有效超时（秒）：**工具 `_meta.timeout` 显式「无上限」（0 / -1）绝对优先、不可被覆盖** ——
	// 该调用**永不设超时点**（不发 `mcp-tools-timeout`，阻塞至完成或用户取消），调用级 / server 级 /
	// 全局均不得覆盖（用户口径 2026-09-27：「无上限」必须是绝对的；第三方工具若声明了也覆盖改不了）。
	// 其余情形维持既有优先级链：调用级(`>0`) > server 级(`entry.TimeoutSec>0`) > 工具 `_meta.timeout`(`>0`) > 全局。
	timeoutSec := g.params.CallTimeout.Seconds()
	if def.timeoutSet {
		timeoutSec = def.timeout
	}
	if !(def.timeoutSet && def.timeout <= 0) {
		if ps.entry != nil && ps.entry.TimeoutSec > 0 {
			timeoutSec = float64(ps.entry.TimeoutSec)
		}
		if callTimeout > 0 {
			timeoutSec = callTimeout
		}
	}
	// 阈值（秒）：显式声明（含 0/-1 = 无阈值）优先；未声明 → 回落有效超时。
	thresholdSec := timeoutSec
	if thrSet {
		thresholdSec = thrS
	}

	execCtx := context.Background()
	execCtx = withTurnContext(execCtx, req.Context)
	run := func(ctx2 context.Context) (*mcp.CallToolResult, error) {
		res, err := ps.prov.Call(ctx2, route.Original, args)
		if ctx2.Err() == context.Canceled {
			return nil, ctx2.Err() // 用户取消：不计熔断失败
		}
		if err != nil {
			ps.cb.failure()
			return nil, err
		}
		if res.IsError {
			ps.cb.failure()
			return res, nil
		}
		ps.cb.success()
		return res, nil
	}
	spec := taskSpec{
		tool: req.Name, instanceID: req.InstanceID, session: req.Session,
		turn: req.Turn, workDir: req.WorkDir, toolCallID: req.ToolCallID,
		topSession: req.TopSession, parent: req.Parent, // I-90：任务树归属随回报回传
		providerKey: route.Provider,
	}

	// 阈值 / 超时点（秒）：auto = 转后台阈值，其余 = 超时点（随 started 上报，供层记 exec_json）。
	// limit <= 0（显式 0/-1 = 无上限）→ **不设点**：永远等，用户可随时取消。
	limit := timeoutSec
	if mode == "auto" {
		limit = thresholdSec
	}

	switch mode {
	case "always":
		spec.allowDetach = true // 已在后台：DetachExec 为幂等无害操作（保持既有宽容语义）
		t, err := g.tm.start(execCtx, spec, run)
		if err != nil {
			return nil, -32000, err.Error()
		}
		// 执行态上报（P3-① 上报点 ①）：启动 → started（层落 running + exec_json）
		g.tm.emitExec(req.InstanceID, t.ID, ExecPhaseStarted, map[string]any{"mode": mode, "threshold_s": limit})
		return pendingResult(t.ID), 0, ""
	default: // auto / manual / never：任务化启动（一次调用），到超时点按模式裁决
		spec.allowDetach = mode != "never"
		spec.allowWait = mode == "never"
		t, err := g.tm.spawn(execCtx, spec, run)
		if err != nil {
			return nil, -32000, err.Error()
		}
		// 执行态上报（P3-① 上报点 ①）：启动 → started（层落 running + exec_json）
		g.tm.emitExec(spec.instanceID, t.ID, ExecPhaseStarted, map[string]any{"mode": mode, "threshold_s": limit})
		// 无上限（工具 `_meta.timeout` 或 usr threshold 显式 0/-1）：不设超时/阈值点，
		// 等待完成或用户取消（never/manual 不再发裁决；auto 无阈值则阻塞）。用户可经任务面板取消。
		if limit <= 0 {
			select {
			case <-t.doneCh: // 完成或已被取消 → 就地交付
				return g.tm.manualResult(t), 0, ""
			case <-t.detachCh: // 用户经 UI 转后台 → pending
				g.logf("[gateway] tool %s detached to background (exec %s)", req.Name, t.ID)
				g.tm.emitExec(spec.instanceID, t.ID, ExecPhaseDetached, map[string]any{"trigger": "manual"})
				return pendingResult(t.ID), 0, ""
			}
		}
		timer := time.NewTimer(time.Duration(limit * float64(time.Second)))
		defer timer.Stop()
		select {
		case <-t.doneCh: // 提前完成（或已被取消）→ 就地交付
			return g.tm.manualResult(t), 0, ""
		case <-t.detachCh: // DetachExec（manual）/ auto 自动转后台 → pending
			g.logf("[gateway] tool %s detached to background (exec %s)", req.Name, t.ID)
			g.tm.emitExec(spec.instanceID, t.ID, ExecPhaseDetached, map[string]any{"trigger": "manual"})
			return pendingResult(t.ID), 0, ""
		case <-timer.C: // 到达超时点
			if mode == "auto" {
				// auto：把在飞任务转后台（**不重跑**），结果经 mcp-tasks-report 回报
				if derr := g.tm.autoDetach(t); derr == nil {
					g.logf("[gateway] tool %s reached threshold %v, exec %s moved to background", req.Name, limit, t.ID)
					// 执行态上报（P3-① 上报点 ②）：阈值命中自动转后台 → detached{trigger:auto, threshold_s}
					g.tm.emitExec(spec.instanceID, t.ID, ExecPhaseDetached, map[string]any{"trigger": "auto", "threshold_s": limit})
					return pendingResult(t.ID), 0, ""
				}
				// 竞态：恰在阈值处已终态/已解绑 → 走对应等待分支（避免无锁读状态）
				select {
				case <-t.doneCh:
					return g.tm.manualResult(t), 0, ""
				case <-t.detachCh:
					return pendingResult(t.ID), 0, ""
				}
			}
			// manual / never：配置⑤「超时自动取消」（cancel_on_timeout > 0）→ **不等用户裁决**，
			// 直接取消该任务（定义即「无人工介入」）——走既有取消链（tm.cancelRef → t.cancel 送 ctx
			// → onTaskCancel 按执行线终止）。auto 语义不变（阈值命中仍转后台，不自动取消）。
			if cancelOnTimeout > 0 {
				if cerr := g.tm.cancelRef(spec.instanceID, t.ID); cerr != nil {
					g.logf("[gateway] tool %s cancel_on_timeout: cancel %s failed: %v", req.Name, t.ID, cerr)
				} else {
					g.logf("[gateway] tool %s reached timeout %v → auto-cancelled (exec %s)", req.Name, limit, t.ID)
				}
				<-t.doneCh // 取消已置终态（cancel 内 markDone）→ 就地交付 cancelled 结果
				return g.tm.manualResult(t), 0, ""
			}
			// manual / never：进入「待用户裁决」——置 awaiting 态 + 发 mcp-tools-timeout，
			// 按 options 阻塞等待（I-57：awaiting 供前端切会话/刷新后恢复裁决条）。
			options := []string{"wait", "cancel"}
			if mode == "manual" {
				options = []string{"detach", "cancel"}
			}
			g.tm.enterAwaiting(spec.instanceID, t.ID, limit, options)
			// 执行态上报（P3-① 上报点 ④）：超时待用户裁决 → awaiting（层落 awaiting + exec_json 明细）
			g.tm.emitExec(spec.instanceID, t.ID, ExecPhaseAwaiting, map[string]any{
				"mode": mode, "timeout_s": limit, "options": options,
			})
			g.emitToolsTimeout(t, limit, options)
			select {
			case <-t.doneCh: // 裁决前完成 → 就地交付
				return g.tm.manualResult(t), 0, ""
			case <-t.detachCh: // 用户选 detach → 转后台
				g.logf("[gateway] tool %s user chose detach (exec %s)", req.Name, t.ID)
				return pendingResult(t.ID), 0, ""
			case <-t.waitCh: // 用户选 wait → 撤销超时，继续等原调用返回
				g.logf("[gateway] tool %s user chose wait (exec %s)", req.Name, t.ID)
				<-t.doneCh
				return g.tm.manualResult(t), 0, ""
			}
		}
	}
}

// emitToolsTimeout 发「到达超时、待用户裁决」下行事件（mcp-tools-timeout，fire-and-forget）：
// 载荷 {instance_id, tool_call_id, task_id, tool, reason:"timeout", timeout_s, options}。
func (g *Gateway) emitToolsTimeout(t *ExecTask, timeoutSec float64, options []string) {
	g.logf("[gateway] tool %s timeout %vs → awaiting user decision (exec %s, options=%v)", t.Tool, timeoutSec, t.ID, options)
	g.pub(SubjectToolsTimeout, marshal(map[string]any{
		"instance_id":  t.OwnerInstance,
		"tool_call_id": t.ToolCallID,
		"task_id":      t.ID,
		"tool":         t.Tool,
		"reason":       "timeout",
		"timeout_s":    timeoutSec,
		"options":      options,
	}))
}

// onTaskCancel 取消任务后置钩子：按 provider 的**执行线**下达终止动作（决策 42 §2 (241)），
// 使取消真实打断在飞调用（含已 detached 的后台任务）：
//   - spawned（stdio ⑥ / http·自持 ⑦ / sse·自持 ⑨）→ `Terminate` = kill + respawn（强语义）；
//   - 纯远程（http·remote ⑧ / sse·remote ⑩）→ 仅断请求（「尽力」，不能杀对端进程）；
//   - in-memory（① / ②③④ / ⑤）→ 协作式 no-op（取消已由执行池 `t.cancel()` 经 ctx 送达执行体）。
//
// 重建失败 → 发 mcp-gateway-changed{kind:server, name, status:"failed", reason}（I-58：
// 让取消触发的 respawn 失败可见；instance_id 按触发实例，无归属空串）；成功不发（状态本就
// connected，避免噪音）。
//
// 2026-09-19（实例隔离第一批，缺口 1）：取消仅作用于该任务的 **(instance, workdir)** 连接槽
// （`Terminate` 内部走 `InvalidateScoped`）→ 同 workdir 其他 instance 的连接与在飞调用
// 不再被连带打断；provider 未实现隔离维度（内存/注册型）/ 无隔离槽 → 回落全量
// （与引入前行为等价）。
func (g *Gateway) onTaskCancel(t *ExecTask) {
	if t.ProviderKey == "" {
		return
	}
	ps, ok := g.reg.provider(t.ProviderKey)
	if !ok || ps.prov == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rebuilt, err := ps.prov.Terminate(ctx, t.OwnerInstance, t.WorkDir)
	if err != nil {
		name := t.ProviderKey
		if ps.entry != nil && ps.entry.ID != "" {
			name = ps.entry.ID
		}
		g.notifyMCPChanged(t.OwnerInstance, map[string]any{
			"kind": KindServer, "name": name, "status": "failed", "reason": err.Error(),
		})
		g.logf("[gateway] provider %s respawn after cancel failed: %v", t.ProviderKey, err)
		// 执行态上报（P3-① 上报点 ⑤-异常）：取消后 respawn 失败 → error{message}（不含堆栈）;
		// 层侧状态单调（cancelled 不可逆）下该态变更会被拒绝并记日志，仅作异常留痕。
		g.tm.emitExecError(t.OwnerInstance, t.ID, err.Error(), map[string]any{"stage": "cancel-respawn"})
		return
	}
	_ = rebuilt
}

// softNeverDefault 判定第三方缺省 never：来源非 builtin（Origin != builtin）且工具契约
// **未显式声明** async。软缺省可被调用级 async / Params.AsyncMode 覆盖。
func softNeverDefault(route *toolRoute, entry *ServerEntry) bool {
	if entry != nil && entry.IsBuiltin() {
		return false
	}
	if route == nil || route.Tool == nil || route.Tool.Meta == nil {
		return true
	}
	if v, ok := route.Tool.Meta["async"].(string); ok && v != "" {
		return false
	}
	return true
}

// asyncDefaults 是路由工具 `_meta` 的 async 默认族（async / async-threshold / timeout）。
// timeoutSet / thrSet = 对应键是否**显式声明**（区分「显式 0/-1 = 无上限」与「未设置 = 回落」）。
type asyncDefaults struct {
	mode       string
	timeout    float64
	timeoutSet bool
	thr        float64
	thrSet     bool
}

// toolAsyncMeta 取路由工具 _meta 的 async 默认族。
func toolAsyncMeta(route *toolRoute) asyncDefaults {
	d := asyncDefaults{mode: "auto"}
	if route == nil || route.Tool == nil || route.Tool.Meta == nil {
		return d
	}
	m := route.Tool.Meta
	if v, ok := m["async"].(string); ok && v != "" {
		d.mode = v
	}
	if v, ok := m["async-threshold"].(float64); ok {
		d.thr = v
		d.thrSet = true
	}
	if v, ok := m["timeout"].(float64); ok {
		d.timeout = v
		d.timeoutSet = true
	}
	return d
}

func strFrom(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	if b, ok := m[key].(bool); ok && def == "" {
		if b {
			return "true"
		}
		return "false"
	}
	return def
}

func floatFrom(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return def
}

// pendingResult 构造转异步 pending 返回。
func pendingResult(taskID string) map[string]any {
	return map[string]any{
		"content": []mcp.Content{&mcp.TextContent{Text: "任务已转后台执行"}},
		"structuredContent": map[string]any{
			"status":  "pending",
			"task_id": taskID,
		},
	}
}

// callResultMap 把下游 CallToolResult 转为方法结果 map（写回 v.Result）。
func callResultMap(res *mcp.CallToolResult) map[string]any {
	m := map[string]any{"resultType": "complete"}
	if len(res.Content) > 0 {
		m["content"] = res.Content
	}
	if res.StructuredContent != nil {
		m["structuredContent"] = res.StructuredContent
	}
	if res.IsError {
		m["isError"] = true
	}
	return m
}

// toolErrorResult 工具级错误（isError:true，非协议错误）。
func toolErrorResult(msg string) map[string]any {
	return map[string]any{
		"resultType": "complete",
		"content":    []mcp.Content{&mcp.TextContent{Text: msg}},
		"isError":    true,
	}
}

// manualResultMap 取 manual 任务终态结果（同步交付用：未解绑时）
//   - done → 工具结果（callResultMap）
//   - error/cancelled → isError 结果；cancelled 另附 structuredContent.status=cancelled
//     （I-62：供 server 同步返回路径把任务节点落 cancelled 而非 done，不靠文本判定）
func manualResultMap(t *ExecTask) map[string]any {
	switch t.State {
	case "error", "cancelled":
		msg := t.Error
		if msg == "" {
			msg = "task " + t.State
		}
		m := toolErrorResult(msg)
		if t.State == "cancelled" {
			m["structuredContent"] = map[string]any{"status": "cancelled", "task_id": t.ID}
		}
		return m
	default:
		if t.Result != nil {
			return callResultMap(t.Result)
		}
		return toolErrorResult("task finished without result")
	}
}

// taskStatusMap 构造执行池状态快照（gateway 侧视图；控制面方法面已移除，保留供内部/测试诊断用）。
func taskStatusMap(t *ExecTask) map[string]any {
	m := map[string]any{
		"task_id": t.ID, "tool": t.Tool, "state": t.State,
		"created_at": t.CreatedAt, "instance_id": t.OwnerInstance,
	}
	if !t.StartedAt.IsZero() {
		m["started_at"] = t.StartedAt
	}
	if !t.DoneAt.IsZero() {
		m["done_at"] = t.DoneAt
	}
	if t.Error != "" {
		m["error"] = t.Error
	}
	// 「超时待用户裁决」态（I-57）：仅非空时输出 awaiting，供前端切会话/刷新后恢复裁决条；
	// 裁决（wait/detach/cancel）或终态后已清除 → 不输出。options 与 mcp-tools-timeout 事件一致。
	if t.awaitingOptions != nil {
		m["awaiting"] = map[string]any{
			"reason":    "timeout",
			"timeout_s": t.awaitingTimeoutS,
			"options":   t.awaitingOptions,
		}
	}
	return m
}

func (g *Gateway) handleServersList(v *mq.Value) error {
	var req ServerReq
	_ = v.JSON(&req)
	v.Result = map[string]any{"servers": g.reg.serverViews(req.Server)}
	return nil
}

// regServerSpec 是 kind=server 的进程规格（register 载荷 mcp_server，可选）——
// 带 runtime = spawned（网关拉起 + 连接 + 生命周期；stop = unregister）；
// 无 runtime 仅 url = proxied（连接已运行 server）。规格字段与 servers.list 对齐。
type regServerSpec struct {
	Runtime     string            `json:"runtime,omitempty"` // 可执行/解释器（单个 token；spawned）
	Args        []string          `json:"args,omitempty"`    // 启动参数（逐个 exec 参数，不做 shell/引号解析）
	URL         string            `json:"url,omitempty"`
	Transport   string            `json:"transport,omitempty"` // stdio/http/sse（缺省：url→http，仅 runtime→stdio）
	Description string            `json:"description,omitempty"`
	Category    string            `json:"category,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Env         []string          `json:"env,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	HotTools    []string          `json:"hot_tools,omitempty"`
	TimeoutSec  int               `json:"timeout,omitempty"`
	Isolate     *bool             `json:"isolate,omitempty"` // 按 workdir 隔离连接；缺省（null）= 按 transport 推断：stdio→true / http|sse→false
	// Sandbox/SandboxDirs：agentbox 沙箱（仅 stdio 的 spawn 下发；nil/未设置 = 不隔离）。
	// 与 Isolate 独立、互不替代（语义见 ServerEntry.Sandbox）。
	Sandbox     *bool           `json:"sandbox,omitempty"`
	SandboxDirs []agentbox.Rule `json:"sandbox_dirs,omitempty"`
	Origin      string          `json:"origin,omitempty"` // 来源：builtin/user；缺省 user（安全默认）
}

// regMsg 是 mcp.register / mcp.unregister 统一载荷（61-消息一览 §5.1.1，2026-09-05 定稿；
// 2026-09-05 方法面 promise 化：无 req_id，响应经同主题 v.Result）。
// kind 缺省 = server；tool/agent/prompt/skill/resource 见各分支。
type regMsg struct {
	InstanceID     string          `json:"instance_id,omitempty"`
	Kind           string          `json:"kind,omitempty"` // server(缺省)/tool/agent/prompt/skill/resource
	Name           string          `json:"name"`
	Dir            string          `json:"dir,omitempty"`              // kind=server：dir 类型节点（本地契约根，如 <workdir>/@mcp；gateway 自建 server 扫描）
	URL            string          `json:"url,omitempty"`              // kind=server（顶层兼容：proxied）
	Transport      string          `json:"transport,omitempty"`        // kind=server（顶层兼容）
	Namespace      string          `json:"namespace,omitempty"`        // kind=server
	MCPServer      *regServerSpec  `json:"mcp_server,omitempty"`       // kind=server：进程规格（runtime→spawned）
	Description    string          `json:"description,omitempty"`      // kind=tool/asset
	Node           string          `json:"node,omitempty"`             // kind=asset：来源节点名（缺省 "server"）
	Schema         string          `json:"schema,omitempty"`           // kind=tool（JSON Schema 文本）
	HandlerSubject string          `json:"handler_subject,omitempty"`  // kind=tool 远程回调主题
	PreHookSubject string          `json:"pre_hook_subject,omitempty"` // kind=tool 可选：前置钩子主题（执行任意工具前同步调用；失败即拒绝该调用）
	Owner          string          `json:"owner,omitempty"`            // kind=tool
	Hot            bool            `json:"hot,omitempty"`              // kind=tool
	Category       string          `json:"category,omitempty"`         // kind=tool（_meta.category 元分类）
	Async          string          `json:"async,omitempty"`            // kind=tool（_meta.async；auto 不显式透出）
	AsyncThreshold float64         `json:"async_threshold,omitempty"`  // kind=tool（_meta.async-threshold，秒）
	Timeout        *float64        `json:"timeout,omitempty"`          // kind=tool（_meta.timeout，秒；**指针区分「未设置」与「0/-1 = 无上限」**）
	AssetKind      string          `json:"asset_kind,omitempty"`       // prompts/register 资产类别：prompt|skill（缺省 prompt；2026-09-06；25 §5/T2：agent 已撤）
	Scope          string          `json:"scope,omitempty"`            // 注册归属域（2026-09-06）：缺省/"global" = 全局；instance id（uuid）= instance 级
	URI            string          `json:"uri,omitempty"`              // kind=resource
	MIMEType       string          `json:"mimetype,omitempty"`         // kind=resource
	Path           string          `json:"path,omitempty"`             // kind=asset：运行时内容来源路径（RB-4 ①；有则内容按需读盘，与 content 二选一）
	Content        string          `json:"content,omitempty"`          // kind=asset（无 path 时驻留；内嵌资产兼容兜底）
	Arguments      json.RawMessage `json:"arguments,omitempty"`        // kind=asset（原文透传）
	Origin         string          `json:"origin,omitempty"`           // kind=server：来源 builtin|user（缺省 user）；仅 builtin 注入 _meta 内部上下文
}

func (g *Gateway) registerServer(v *mq.Value, req regMsg) error {
	if req.Name == "" {
		return methodError(-32602, "name required (kind=server)")
	}
	scope := normalizeScope(req.Scope)
	// dir 类型节点（本地契约根，如 <workdir>/@mcp）：gateway 自建官方 server + RegisterContracts 扫描
	if req.Dir != "" {
		if err := g.registerDirNode(req.Name, req.Dir, scope); err != nil {
			return methodError(-32000, "%s", err.Error())
		}
		g.logf("[gateway] server (dir) registered: %s dir=%s scope=%q", req.Name, req.Dir, scope)
		v.Result = map[string]any{"registered": true, "kind": KindServer, "type": "dir", "name": req.Name}
		return nil
	}
	// 合并规格：顶层 url/transport/namespace 兼容旧载荷；mcp_server 承载完整进程规格。
	entry := &ServerEntry{
		ID: req.Name, Name: req.Name, Transport: req.Transport,
		Namespace: req.Namespace, Enabled: true, Aliases: map[string]string{},
		Scope: scope,
	}
	// 来源标注：顶层 origin 优先，其次 mcp_server.origin；缺省/未知 → user（安全默认）。
	// 仅 Origin=builtin（本仓自有/内置）才向其注入 _meta 内部上下文。
	origin := req.Origin
	if origin == "" && req.MCPServer != nil {
		origin = req.MCPServer.Origin
	}
	entry.Origin = normalizeOrigin(origin)
	if s := req.MCPServer; s != nil {
		entry.Runtime = s.Runtime
		entry.Args = s.Args
		entry.Description = s.Description
		entry.Category = s.Category
		entry.Env = s.Env
		entry.Headers = s.Headers
		entry.Cwd = s.Cwd
		entry.HotTools = s.HotTools
		entry.TimeoutSec = s.TimeoutSec
		entry.Isolate = s.Isolate // nil = 按 transport 推断（stdio→true）
		entry.Sandbox = s.Sandbox // nil/未设置 = 不隔离（默认兼容）；仅 stdio 的 spawn 生效
		entry.SandboxDirs = s.SandboxDirs
		if req.URL == "" {
			entry.URL = s.URL
		}
		if req.Transport == "" {
			entry.Transport = s.Transport
		}
		if req.Namespace == "" {
			entry.Namespace = s.Namespace
		}
	}
	if entry.URL == "" {
		entry.URL = req.URL
	}
	if entry.Transport == "" {
		entry.Transport = req.Transport
	}
	if entry.Namespace == "" {
		entry.Namespace = req.Namespace
	}
	if entry.URL == "" && entry.Runtime == "" {
		return methodError(-32602, "name/runtime or url required (kind=server): proxied 需 url，spawned 需 mcp_server.runtime")
	}
	// register = start：同名 connected/connecting → 拒绝（先 unregister 再 register = restart 编排）；
	// failed 残留（启动失败留状态）→ 清理后重连。冲突按 (scope, name)：不同 instance 可同名并存
	for _, key := range []string{provKey("spawned", scope, req.Name), provKey("proxied", scope, req.Name)} {
		if ps, ok := g.reg.provider(key); ok {
			if ps.status == "connected" || ps.status == "connecting" {
				return methodError(-32000, "server %s already registered (%s)；stop 先 unregister", req.Name, ps.status)
			}
			_ = ps.prov.Close()
			g.reg.removeProvider(key)
		}
	}
	if err := g.connectServer(context.Background(), *entry); err != nil {
		return methodError(-32000, "%s", err.Error())
	}
	g.logf("[gateway] server registered: %s", req.Name)
	v.Result = map[string]any{"registered": true, "kind": KindServer, "name": req.Name}
	return nil
}

func (g *Gateway) registerTool(v *mq.Value, req regMsg) error {
	if req.Name == "" {
		return methodError(-32602, "name is required (kind=tool)")
	}
	scope := normalizeScope(req.Scope)
	var schema map[string]any
	if req.Schema != "" {
		_ = json.Unmarshal([]byte(req.Schema), &schema)
	}
	norm, err := normalizeInputSchema(req.Name, schema)
	if err != nil {
		return methodError(-32602, "%s", err.Error())
	}
	t := &mcp.Tool{Name: req.Name, Description: req.Description, InputSchema: norm}
	// 工具 `_meta`（契约透出；字段命名对齐 mcp-server buildTool：async 为 auto/空时不显式透出，
	// async-threshold 仅 >0 透出；**timeout 键显式声明即透出，含 0/-1 = 无上限**——靠 `*float64`
	// 区分「未设置」与「显式 0/-1」）。注册载荷可含 category/async/async_threshold/timeout
	// （域工具经 llm-server tools/register 携带契约 [meta]，见 domainmcp.go registerDomainTools）。
	if req.Hot || req.Category != "" || req.Async != "" || req.AsyncThreshold > 0 || req.Timeout != nil {
		if t.Meta == nil {
			t.Meta = mcp.Meta{}
		}
		t.Meta["hot"] = req.Hot
		if req.Category != "" {
			t.Meta["category"] = req.Category
		}
		if req.Async != "" && req.Async != "auto" {
			t.Meta["async"] = req.Async
		}
		if req.AsyncThreshold > 0 {
			t.Meta["async-threshold"] = req.AsyncThreshold
		}
		if req.Timeout != nil {
			t.Meta["timeout"] = *req.Timeout
		}
	}
	owner := req.Owner
	if owner == "" {
		owner = "server"
	}
	rt := &registeredTool{
		Tool:           t,
		HandlerSubject: req.HandlerSubject,
		PreHookSubject: req.PreHookSubject,
		Owner:          owner,
		Scope:          scope,
	}
	if err := g.regProv.RegisterTool(rt); err != nil {
		return methodError(-32000, "%s", err.Error())
	}
	// 注入 self（参数能力源 server）；经 self 节点 list/路由 → handler 委托 regProv 执行
	if g.params.MCPServer == nil {
		g.regProv.UnregisterTool(scope, req.Name)
		return methodError(-32000, "tool %q register failed: no self MCPServer", req.Name)
	}
	toolName := req.Name
	g.params.MCPServer.AddTool(t, func(ctx context.Context, callReq *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := argsFromRequest(callReq.Params.Arguments)
		if err != nil {
			return toolErrorText(err.Error()), nil
		}
		// 调用上下文经协议 _meta（doCall 调用前透传）→ 重建执行 ctx（regProv 据此转发远程回调）
		return g.regProv.Call(execCtxFromMeta(ctx, callReq.Params.Meta), toolName, args)
	})
	if err := g.reconcileSelf(); err != nil {
		g.regProv.UnregisterTool(scope, req.Name)
		return methodError(-32000, "tool %q register failed: %s", req.Name, err.Error())
	}
	g.notifyMCPChanged(req.InstanceID, map[string]any{"kind": KindTool, "name": req.Name})
	g.logf("[gateway] tool injected into self: %s (owner=%s, scope=%q)", req.Name, owner, scope)
	v.Result = map[string]any{"registered": true, "kind": KindTool, "name": req.Name}
	return nil
}

func (g *Gateway) registerAsset(v *mq.Value, req regMsg, kind string) error {
	if req.Name == "" {
		return methodError(-32602, "name is required (kind=%s)", kind)
	}
	a := &catalogAsset{
		Kind: kind, Name: req.Name, Description: req.Description,
		URI: req.URI, MIMEType: req.MIMEType, Path: req.Path, Content: req.Content,
		Arguments: req.Arguments,
		Scope:     normalizeScope(req.Scope),
		Node:      req.Node,
	}
	if a.Node == "" {
		a.Node = "server" // 动态注册来源缺省 = server（域注册）
	}
	if err := g.reg.putAsset(a); err != nil {
		return methodError(-32000, "%s", err.Error())
	}
	g.notifyMCPChanged(req.InstanceID, map[string]any{"kind": kind, "name": req.Name})
	g.logf("[gateway] asset registered: %s/%s", kind, req.Name)
	v.Result = map[string]any{"registered": true, "kind": kind, "name": req.Name}
	return nil
}

func (g *Gateway) unregisterServer(v *mq.Value, req regMsg) error {
	if req.Name == "" {
		return methodError(-32602, "name is required (kind=server)")
	}
	scope := normalizeScope(req.Scope)
	// dir 类型节点优先（无 command/url 形态）
	if existed, removed := g.unregisterDirNode(req.Name, scope); existed {
		g.notifyMCPChanged(req.InstanceID, map[string]any{"kind": KindServer, "name": req.Name, "status": "unregistered"})
		g.logf("[gateway] server (dir) unregistered: %s (removed %d tools)", req.Name, removed)
		v.Result = map[string]any{
			"unregistered": true, "kind": KindServer, "name": req.Name, "removed_tools": removed,
		}
		return nil
	}
	removed := 0
	spawned := false
	for _, key := range []string{provKey("spawned", scope, req.Name), provKey("proxied", scope, req.Name)} {
		if ps, ok := g.reg.provider(key); ok {
			if strings.HasPrefix(key, "spawned:") {
				spawned = true
			}
			_ = ps.prov.Close() // spawned：kill 自持子进程（closing 置位 → 退出观测静默）
			removed += g.reg.removeProvider(key)
		}
	}
	// unregister = stop：spawned 停进程发 stopped；proxied 断连接保持 unregistered
	status := "unregistered"
	if spawned {
		status = "stopped"
	}
	g.notifyMCPChanged(req.InstanceID, map[string]any{"kind": KindServer, "name": req.Name, "status": status})
	g.logf("[gateway] server unregistered: %s (removed %d tools)", req.Name, removed)
	v.Result = map[string]any{
		"unregistered": true, "kind": KindServer, "name": req.Name, "removed_tools": removed,
	}
	return nil
}

func (g *Gateway) unregisterTool(v *mq.Value, req regMsg) error {
	if req.Name == "" {
		return methodError(-32602, "name is required (kind=tool)")
	}
	scope := normalizeScope(req.Scope)
	g.regProv.UnregisterTool(scope, req.Name)
	if g.params.MCPServer != nil {
		g.params.MCPServer.RemoveTools(req.Name)
		_ = g.reconcileSelf()
	}
	g.notifyMCPChanged(req.InstanceID, map[string]any{"kind": KindTool, "name": req.Name})
	g.logf("[gateway] tool unregistered from self: %s (scope=%q)", req.Name, scope)
	v.Result = map[string]any{"unregistered": true, "kind": KindTool, "name": req.Name}
	return nil
}

func (g *Gateway) unregisterAsset(v *mq.Value, req regMsg, kind string) error {
	g.reg.delAssetFor(normalizeScope(req.Scope), kind, req.Name)
	g.notifyMCPChanged(req.InstanceID, map[string]any{"kind": kind, "name": req.Name})
	g.logf("[gateway] asset unregistered: %s/%s", kind, req.Name)
	v.Result = map[string]any{"unregistered": true, "kind": kind, "name": req.Name}
	return nil
}

// ─── prompts / resources（原语面：经 self + dir 节点实时拉取，带 scope/type 区分）───

// handlePromptsList 2026-09-07 定稿：全量返回（节点原语 + 注册 prompt/skill 资产），
// 每项带 type（prompt/skill）+ scope + node；不带参数过滤——UI 按需分流。
// 25 §5/T2（2026-09-25）：**agent 已撤出资产面** → 本表不再列 agent。
func (g *Gateway) handlePromptsList(v *mq.Value) error {
	list := make([]any, 0, 32)
	ctx := context.Background()
	_ = g.forEachMemNodeAll(func(n *memNode, nodeName, scope string) error {
		ps, err := n.ListPrompts(ctx)
		if err != nil {
			return nil // 单节点失败不阻塞
		}
		for _, p := range ps {
			typ := "prompt"
			if p.Meta != nil {
				if tv, ok := p.Meta["type"].(string); ok && tv != "" {
					typ = tv
				}
			}
			entry := map[string]any{
				"name": p.Name, "description": p.Description,
				"type": typ, "scope": scope, "node": nodeName,
			}
			if len(p.Arguments) > 0 {
				args := make([]map[string]any, 0, len(p.Arguments))
				for _, a := range p.Arguments {
					args = append(args, map[string]any{
						"name":        a.Name,
						"description": a.Description,
						"required":    a.Required,
					})
				}
				entry["arguments"] = args
			}
			list = append(list, entry)
		}
		return nil
	})
	// 注册资产并入（prompt/skill，全量 + scope/node）；25 §5/T2：agent 已撤出资产面 → 不再列举。
	for _, kind := range []string{KindPrompt, KindSkill} {
		for _, a := range g.reg.assetsByKind(kind) {
			entry := map[string]any{
				"name": a.Name, "description": a.Description,
				"type": kind, "scope": a.Scope,
			}
			if a.Node != "" {
				entry["node"] = a.Node
			}
			if len(a.Arguments) > 0 {
				entry["arguments"] = a.Arguments
			}
			list = append(list, entry)
		}
	}
	v.Result = map[string]any{"prompts": list}
	return nil
}

// handlePromptsGet 返回单个 prompt/skill 详细内容（**节点原语**实时读取）。
// 25 §5/T2（2026-09-25）：**agent 已撤出资产面** —— 原「先 agent 资产」分支已撤（agent 不再
// 注册为资产，且不可经此读取）。
func (g *Gateway) handlePromptsGet(v *mq.Value) error {
	var req struct {
		Context
		Name string `json:"name"`
	}
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	if req.Name == "" {
		return methodError(-32602, "name is required")
	}
	// 节点原语（self + 归属 dir）
	ctx := context.Background()
	found := false
	_ = g.eachMemNode(req.InstanceID, func(n *memNode) error {
		if found {
			return nil
		}
		res, err := n.GetPrompt(ctx, req.Name, nil)
		if err != nil {
			return nil
		}
		if len(res.Messages) == 0 {
			return nil
		}
		if tc, ok := res.Messages[0].Content.(*mcp.TextContent); ok {
			found = true
			v.Result = map[string]any{
				"name":        req.Name,
				"description": "",
				"content":     tc.Text,
				"type":        "prompt",
			}
		}
		return nil
	})
	if !found {
		return methodError(-32602, "prompt %q not found", req.Name)
	}
	return nil
}

// handleResourcesList 2026-09-07 定稿：全量返回（节点原语 + 注册 resource 资产），带 scope/node。
func (g *Gateway) handleResourcesList(v *mq.Value) error {
	list := make([]any, 0, 32)
	ctx := context.Background()
	_ = g.forEachMemNodeAll(func(n *memNode, nodeName, scope string) error {
		rs, err := n.ListResources(ctx)
		if err != nil {
			return nil
		}
		for _, r := range rs {
			list = append(list, map[string]any{
				"uri":         r.URI,
				"name":        r.Name,
				"description": r.Description,
				"mimeType":    r.MIMEType,
				"scope":       scope,
				"node":        nodeName,
			})
		}
		return nil
	})
	for _, a := range g.reg.assetsByKind(KindResource) {
		entry := map[string]any{
			"uri": a.URI, "name": a.Name, "description": a.Description,
			"mimeType": a.MIMEType, "scope": a.Scope,
		}
		if a.Node != "" {
			entry["node"] = a.Node
		}
		list = append(list, entry)
	}
	v.Result = map[string]any{"resources": list}
	return nil
}

// handleResourcesRead 返回单个 resource 详细内容（self + 归属 dir 节点按 uri 读取）。
func (g *Gateway) handleResourcesRead(v *mq.Value) error {
	var req struct {
		Context
		URI string `json:"uri"`
	}
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	if req.URI == "" {
		return methodError(-32602, "uri is required")
	}
	ctx := context.Background()
	var hit *mcp.ResourceContents
	_ = g.eachMemNode(req.InstanceID, func(n *memNode) error {
		if hit != nil {
			return nil
		}
		res, err := n.ReadResource(ctx, req.URI)
		if err != nil {
			return nil
		}
		if len(res.Contents) > 0 {
			hit = res.Contents[0]
		}
		return nil
	})
	if hit == nil {
		return methodError(-32602, "resource %q not found", req.URI)
	}
	v.Result = map[string]any{
		"uri":         hit.URI,
		"name":        hit.URI,
		"description": "",
		"mimeType":    hit.MIMEType,
		"content":     hit.Text,
	}
	return nil
}

// ─── 下游接入 ────────────────────────────────────────────

// connectServer 接入一个 server（proxied/spawned）：建 client → spawn+就绪(spawned) →
// 握手 → tools/list → 注册。spawned 连上后挂退出观测（意外退出 → failed 通知 + 清缓存）。
func (g *Gateway) connectServer(ctx context.Context, e ServerEntry) error {
	prov, err := newProxyProvider(&e, g.params.CallTimeout, g.logf)
	if err != nil {
		return err
	}
	scope := normalizeScope(e.Scope)
	key := provKey("proxied", scope, e.ID)
	if e.Runtime != "" {
		key = provKey("spawned", scope, e.ID)
	}
	ps := &providerState{
		key:    key,
		entry:  &e,
		prov:   prov,
		status: "connecting",
		cb:     newBreaker(g.params.CBThreshold, g.params.CBCooldown, e.CBDisabled),
		scope:  scope,
	}
	// 先登记（连接失败也可见状态，servers/list 可查、可 reload）
	g.reg.registerFailed(ps)
	if e.Runtime != "" {
		g.notifyMCPChanged(scope, map[string]any{"kind": KindServer, "name": e.ID, "status": "starting"})
	}
	// spawned（含本地 http/sse）引导 + 握手可更久，放宽到 30s；纯 proxied 15s
	connectTimeout := 15 * time.Second
	if e.Runtime != "" {
		connectTimeout = 30 * time.Second
	}
	ctx2, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	fail := func(err error) error {
		_ = prov.Close() // spawned：kill 子进程，避免孤儿
		g.reg.setStatus(key, "failed")
		g.notifyMCPChanged(scope, map[string]any{"kind": KindServer, "name": e.ID, "status": "failed", "reason": err.Error()})
		return err
	}
	if err := prov.connect(ctx2); err != nil {
		return fail(err)
	}
	tools, err := prov.ListTools(ctx2)
	if err != nil {
		return fail(err)
	}
	if err := g.reg.registerProvider(ps, tools, g.params.NsPrefix); err != nil {
		return fail(err)
	}
	g.reg.setStatus(key, "connected")
	g.notifyMCPChanged(scope, map[string]any{"kind": KindServer, "name": e.ID, "status": "connected"})
	g.logf("[gateway] server %s connected: %d tools", key, len(tools))
	// spawned http/sse（网关自持子进程）：挂退出观测——意外退出自动清缓存 + failed 通知
	if e.Runtime != "" && prov.SpawnedSelf() {
		go g.monitorSpawned(e.ID, key, prov)
	}
	return nil
}

// monitorSpawned 观测自持 spawned 子进程（spawned http/sse）：循环观测；
// 意外退出一律重建进程 + 重连 + 重注册工具 + 发 connected（统一异步模型：取消/重建恒
// kill + respawn，2026-09-13；不再按 restart 策略分支）。
//
// 网关主动 Close（Stop/unregister）或 Invalidate 重建期间已置 closing → 静默返回。
func (g *Gateway) monitorSpawned(name, key string, prov *proxyProvider) {
	for {
		err := <-prov.WaitExit()
		g.mu.Lock()
		stopped := g.stopped
		g.mu.Unlock()
		if stopped || prov.closing.Load() {
			return
		}
		ps, ok := g.reg.provider(key)
		if !ok || ps.status != "connected" {
			return // 已被停/移除（非退出观测触发）
		}
		scope := ps.scope
		// 意外退出：恒重建 + 重注册（继续观测新进程）
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, ierr := prov.Invalidate(ctx)
		var tools []*mcp.Tool
		if ierr == nil {
			tools, ierr = prov.ListTools(ctx)
		}
		cancel()
		if ierr != nil {
			_ = prov.Close()
			removed := g.reg.removeProvider(key)
			g.notifyMCPChanged(scope, map[string]any{"kind": KindServer, "name": name, "status": "failed", "reason": ierr.Error()})
			g.logf("[gateway] spawned server %s rebuild failed, removed %d tools: %v", name, removed, ierr)
			return
		}
		g.reg.removeProvider(key)
		if rerr := g.reg.registerProvider(ps, tools, g.params.NsPrefix); rerr != nil {
			g.notifyMCPChanged(scope, map[string]any{"kind": KindServer, "name": name, "status": "failed", "reason": rerr.Error()})
			g.logf("[gateway] spawned server %s re-register failed: %v", name, rerr)
			return
		}
		g.reg.setStatus(key, "connected")
		g.notifyMCPChanged(scope, map[string]any{"kind": KindServer, "name": name, "status": "connected"})
		g.logf("[gateway] spawned server %s restarted after exit (%v)", name, err)
	}
}

// ─── 管理 REST ──────────────────────────────────────────

func (g *Gateway) startManage(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/list", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"servers": g.reg.serverViews("")})
	})
	// /mcp/tasks 已移除（2026-09-18 用户决定）：任务状态/历史控制面归任务层 + message 表，
	// gateway 不再对外暴露任务查询 REST。
	mux.HandleFunc("/mcp/check", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "tools": len(g.reg.allTools()), "servers": g.reg.serverViews("")})
	})
	g.mgmtSrv = &http.Server{Addr: addr, Handler: mux}
	go func() {
		g.logf("[gateway] manage http on %s", addr)
		if err := g.mgmtSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			g.logf("[gateway] manage http error: %v", err)
		}
	}()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
