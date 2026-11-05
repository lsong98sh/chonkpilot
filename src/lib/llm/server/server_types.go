package server

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-assembly"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
	mcpgateway "github.com/lsong98sh/chonkpilot-agent/gateway"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
	"github.com/lsong98sh/chonkpilot-plugin-framework/runtime/instance"
	"github.com/lsong98sh/chonkpilot-agent/router"
	"github.com/lsong98sh/chonkpilot-agent/session/runner"
	"github.com/lsong98sh/chonkpilot-agent/chat"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options 是会话服务构造参数。
type Options struct {
	LLMBase  string // OpenAI 兼容 base URL
	LLMModel string // 默认模型名

	// UsrPath usr 层 chonkpilot.db 路径（user_config 用户配置；空 = data.UserPath()）。
	// 会话/任务树等持久化经总线 persist 数据服务（本包内嵌，同样使用该路径）。
	UsrPath string

	// 内嵌 MCP server + gateway（21-llm-server：server lib 依赖并内嵌
	// mcp-server + mcp-gateway）。默认启用；宿主自建 gateway 时置 DisableMCP。
	DisableMCP     bool                     // true = 跳过内嵌（宿主自建 gateway）
	MCPServerRoot  string                   // 系统级 capability 根（默认 exe 目录/capability）；同时作为数据层 app 根（AppDir：知识库 app 级 + 场景 `../scenarios`，25 §6）
	GatewayServers []mcpgateway.ServerEntry // 额外下游（外部 proxied 等，与内嵌 inprocess 并存）
	// PRODSIM-1：已移除管理 REST 入口（`--manage-addr`）——gateway 健康检查/资产查询
	// 统一走插件桥（`plugin/v1/gateway.check` + `mcp_find`/`mcp_load`），不再单开 TCP 口。

	// AsyncMode 工具异步模式（"" = 默认"auto"；"never" = 强制同步，用于 CLI）。
	// 透传给内嵌 gateway 的 Params.AsyncMode。
	AsyncMode string

	// Plugins 内嵌插件 Hook（可选；2026-10-27 起主仓无内嵌插件，
	// `appwire.DefaultPlugins` 返回 nil，本字段仅保留装配机制供测试 fake hook 使用）。
	// server.Start 在 gateway/路由/实例视图就绪后按序 Start 插件
	// （全部成功才广播 server-starting；插件失败 = 启动失败）。
	Plugins []hook.Hook

	// LogWriter 额外日志 sink（可选；**nil = 仅 stdout，即改前行为**）。
	// 2026-09-20 增补：本包既有诊断输出一律 stdout，而 GUI 单体为 `-H windowsgui`（stdout
	// 不可见）→ 宿主（GUI）注入同一个滚动文件 sink（`<dataDir>/logs/gui.log`）后，本包与
	// 插件日志**同时落文件**（见 log.go）。库侧只收 io.Writer，不依赖宿主包。
	LogWriter io.Writer

	// WorkDir / DataDir 已移除：`instance-claim` 的 work_dir / data_dir 一律以
	// instance-register 登记为准（PRODSIM-1：单进程多项目下启动参数不是项目目录）。

	// Form 是本宿主的**运行形态**（61 §4.6）—— 由入口/宿主注入；空 = desktop。
	// PRODSIM-1：单产物只有 desktop；本地认证域（AuthDBPath / UserDataRoot / AllowRegister /
	// ProjectRoots）已整体删除（认证将来统一到 registry）。
	Form string

	// InstanceManager 是**共用实例视图**（可选；nil = 本包自建 `instance.New(bus)`）。
	// P5 步骤 1（SANDBOX-WRITE-1 C15）：入口装配器把**同一份**视图注入 workspace（工作区管理器），
	// 使 `instance-register/exit` 的投影全进程只有一份（不再各自登记）。
	InstanceManager *instance.Manager
}

// Server 是 LLM 会话服务（调度器）实例。
type Server struct {
	bus  mq.Bus
	opts Options
	// rt 是**统一 LLM 出网入口**（LR-11）：一次来回（`step`）= `rt.Call`，多 provider / 多协议
	// （openai / responses / anthropic / 内置兜底 echo）与连接池全部归 router；本服务的
	// provider 注册集经 `reconcileLLMProviders` 与 usr `llms` 对账（见 routercall.go）。
	rt *router.Router
	gc *gwClient

	// 内嵌数据服务（persist data 面，61-消息一览 §3）：会话/tasktree/快照/配置落库应答方。
	data *persist.Service
	// cfg 是 **data 门面**（阶段 4 第二批，41 G-34；阶段 4 internal 下沉后实现 = 内嵌 persist）：
	// 覆盖 config 域（配置 kv 面 + 用户配置）+ mcp 域（四级文件化 MCP 配置）等各域面。
	// 绑定 = inline（同进程直调，实现 = 内嵌 persist —— 与 data-<domain>-* MQ handler 同一份
	// 实现）。本包的配置读取一律经它，
	// 不再"发一条 MQ 请求给自己"（dataRequest）；**MQ 面继续存在**（前端/GUI 桥仍走总线）。
	// 类型用完整 facade.API（而非仅 ConfigAPI）：本包既读 config 域，也读 MCP 四级配置域。
	cfg facade.API
	// 内嵌 MCP 栈（2026-09-07 收敛）：官方 go-sdk server（capability 契约 RegisterContracts）
	// 由入口装配器 `assembly` 构建并作为**能力源**（prompts/skills/resources 资产面）暴露给本层，
	// 但**不再**作为 gateway 的 self 节点传入（GATEWAY-2 L5：gateway 只接 Transport + Servers）；
	// 用户/项目级 capability 根由 server 经 servers/register 以普通 stdio 下游
	// （chonkpilot-core-mcp-server --stdio --root <dir>）驱动 gateway 接入/注销。
	mcpServer *mcp.Server     // 官方 go-sdk server（capability 能力源；nil = DisableMCP）
	mcpCfg    *mcpms.Config   // 内嵌 mcp-server 执行配置（instance-register 后注入 usr/prj 配置）
	stack     *assembly.Stack // 入口装配器产物（RB-5 L5：能力源 + 执行配置 + gateway 的持有者）
	gw        gwRuntime       // 内嵌 gateway（RB-5 L1：窄接口，不持具体类型；nil = DisableMCP）
	mu       sync.Mutex
	filterMu sync.RWMutex
	filters  *pluginbridge.FilterChain
	im       *instance.Manager   // 共通实例视图（register/heartbeat/exit，对齐 61-消息一览 §4.1）
	agents   map[string]AgentDef // 内置 agent 定义注册表（**app 级场景** `<exeDir>/scenarios/*/` 内的
	// agent；键 = agent 名；值 = 定义（content = 该 agent 的提示词，AG-1 子轮次回落来源）。
	// 25 §5 / T2：agent 只"注入"不"注册" —— 本表**不再**对应 gateway 资产注册
	// （`prompts/register asset_kind=agent` 已撤）；25 §8.1 #8 / T6：来源 = app 级场景（非代码 embed）。
	// 运行态 map 一律以 **instKey(instance, id)** 为键（2026-09-19 缺口 5：按 instance 分桶，
	// 同进程多 instance 互不可见/互不取消；instance 为空 → 原键，旧语义等价，见 instancescope.go）：
	turns           runner.Registry[turnSession] // Key(instance, turn) → 会话状态机（A4-P3a-0：接口容器；TURNRUN-1 批次 5 迁入 session/runner）
	engine          *chat.Engine                // chat 无状态单例（TURNRUN-1 批次 6：端口按轮传入）
	busy            runner.Registry[string]      // Key(instance, session) → running turn（同 instance 同 session 互斥）
	continuePending map[string]bool        // instKey(instance, turn) → 下一个 text-user 以 Kind=continue 注入
	// owners / ownersMu：session → 活动运行态 owner 实例（SCOPE-1 强制不变式「同一 dialog 不得在
	// 两个窗口打开」的运行态归属表；判据 / 产生 / 拒绝 / 两条释放路径见 owner.go）。
	ownersMu sync.Mutex
	owners   map[string]string
	// inboxMu / inboxIndex：INBOX 项的内存索引（id → item）。promote / discard 载荷只带 id，
	// 需回查该 item 的 session / type（不新增 data 面「按 id 读」消息）；list/append 时写入。
	// inboxPending / inboxTerminal 见 inbox.go（LLM-P4：按会话分桶 + 终态条目有界）。
	inboxMu       sync.Mutex
	inboxIndex    map[string]facade.InboxItem
	inboxPending  map[string]map[string]string // session_id → (inbox_id → type)：仅 pending 项
	inboxTerminal []string                     // 终态项 id 的 FIFO（上限 inboxTerminalKeep，超出淘汰最旧）
	// inboxQueued：已投递到某运行轮 `inboxCh`、等引擎消费的插话项（id → true）。
	// 批量插话（2026-11-01）：相位满足时把该会话全部 pending insert 一次性投递；本表防重复投递
	// （消费 / 撤回 / 相位退回时摘除）。与 inboxIndex 同锁（inboxMu）。
	inboxQueued map[string]bool
	// taskTurnIndex：task_id → 等待该网关任务的运行 turn（键 = instKey(instance, task_id)）。
	// onGatewayTaskDone 据此 O(1) 定位等待 turn，免去按 s.mu 全表扫描 s.turns + 逐 turn 加锁探测
	// pending。与 tc.frames 的 pending 同步维护（登记 pending 时写入 / 消费回执 / 轮次收尾时摘除）。
	taskTurnIndex map[string]*turnCtx
	asks          runner.Registry[*askWaiter] // Key(instance, ask_id) → 等待的 turn + tool-call-id + 任务节点（TURNRUN-1 批次 5 迁入 session/runner）
	// askCenterSubs 是 Ask 中心订阅登记（后端唯一分发，2026-11-05）：user → Ask 中心窗口实例。
	// 同一 user 只保留一个订阅实例（后订阅覆盖，避免多端重复分发）。与 s.mu 同锁。
	askCenterSubs map[string]askCenterSub
	// askSeq 是每 workdir 的 ask 单调序号（有序队列）；与 s.mu 同锁。
	askSeq map[string]int64
	// delegated：发起轮次 → 委派子轮次句柄（键 = instKey(instance, parent_turn)）。
	// 委派子代理（task_llm）的 ctx 独立于父轮次正常收尾（见 subllm.go）；父轮次取消 /
	// task-stop 时经本表级联关闭（多层委派递归）。与 s.mu 同锁。
	delegated map[string][]*childTurn
	// groupRuns 是**运行中的群聊调度器**登记表（SUBCHAT-3 阶段 F-2）：groupID → 运行句柄。
	// 用途 = 「关窗即停」（§15.4 N23）：群聊窗口实例 `instance-exit` → 停掉由该实例驱动的调度器
	// （见 stopGroupRunsForInstance）。独立小锁（不含 IO，避免与 s.mu 锁序纠缠）。
	groupMu   sync.Mutex
	groupRuns map[string]*groupSched
	tasks     *taskManager // 任务编排（任务树/级联/批量/状态事件，§6.6）
	// taskExecCancels：task 型域工具「执行中取消」登记表（I-83 收尾，18 §3.4）：tool_call_id →
	// 执行 ctx cancelFunc。domainExecute task 分支登记、结束注销；gateway 取消（tm.cancel 补发
	// mcp-tasks-report{state:cancelled}）→ onGatewayTaskDone 查表 cancel 真停执行体
	// （tool_result 轮询等）。独立小锁：回报到达时域工具可能正执行中，避免卷入 s.mu 嵌套。
	taskExecCancelMu sync.Mutex
	taskExecCancels  map[string]context.CancelFunc
	cleaned          map[string]bool      // instanceID → 已做过遗留 running 清理
	dirNodes         map[string][]string  // instanceID → 已接入的 capability dir 节点名（幂等/退出清理）
	appTools         map[string]bool      // app 根已注册工具名（T-21② 删除/改名回收用）
	capMu            sync.Mutex           // 串行化 capability dir 节点接入/重扫（T-21）
	capApplyMu       sync.Mutex           // 串行化 capDirOp 总线应用（B-35：内存态在 capMu 内维护，总线往返在锁外，但**逐实例保序**必须跨调用串行，否则并发重扫会重复 spawn 同名节点 → tool 名冲突）
	execCfgMu        sync.Mutex           // 串行化 loadExecConfig（G-27：instance-register 与 prj 执行配置刷新两路并发调用，内含「读配置 → SetRuntime」读改写序列，并发交错即 data race；不重入，见 loadExecConfig 头注）
	capWatch         *capWatcher          // 用户/项目级 capability 根 fswatcher 监听（T-21 保存即生效）
	memCache         *memoryCategoryCache // 记忆类别清单短时 TTL 缓存（I-68 ②）
	// taskLayer 任务层（21-任务层设计方案 §9.2 P2：层为唯一权威）。层订阅既有事件、同步写
	// 权威表 tasktree（单写者；llm 不再自写库），并持有执行句柄 → 取消/转后台时回调执行侧
	// （taskExecSink.CancelExec / DetachExec → 进程内 gateway 执行池，见 §9.3）。
	// 创建 / 启动 / 写入失败一律只告警，绝不影响本服务的主路径。
	taskLayer *tasklayer.Layer
	// execSink 是执行池控制面（2026-09-18：`mcp-tasks-cancel` / `mcp-tools-background` 方法面
	// 移除后，取消/转后台改由层 + 进程内 sink 直调）。DisableMCP（无内嵌 gateway）→ nil
	// → 控制路径返回明确错误、不动作（主路径不受影响）。
	execSink mcpgateway.ExecSink
	subs     []mq.Sub
	plugins  []hook.Hook // 内嵌插件（Options.Plugins 拷贝；Start 时按序 Start）
	// sweepStop 心跳超时扫描停止函数（分离形态非空；默认构建 startInstanceSweep 返回 nil
	// = 零 ticker，见 sweep_split.go / sweep_inprocess.go）。
	sweepStop func()

	// MCP 四级文件配置保存即生效（T-25）：已下发给 gateway 的集合（对账基线）+ 串行化增量对账。
	// mcpReconcileMu 串行化**整次对账**（B-15：覆盖全程保证对账互斥），mcpMu 仅保护 mcpApplied
	// 的读写——网络调用 register/unregister 在 mcpMu 外进行，不阻塞其他 mcp 面访问。
	mcpReconcileMu sync.Mutex
	mcpMu          sync.Mutex
	mcpApplied     map[string]mcpgateway.ServerEntry

	// sandboxRules 是 gateway 可写白名单快照（C3：经 `_meta.chonkpilot.sandbox` 下发给内置插件）。
	// 在每次 applyGatewaySandboxDirs（启动 / 实例注册 / prj-security 变更）时刷新。
	sandboxMu    sync.RWMutex
	sandboxRules []mcpgateway.SandboxRule

	// noticeSeen 是插件失败提示判重表（同轮同类只提示一次，见 pluginnotice.go）。
	noticeSeen *noticeDedup

	// promptSem 是 prompt-optimise 的**每实例在飞并发**信号量（B-24：后台生成 goroutine 数量限流，
	// 上限 promptOptimiseMaxInflight；懒建，键 = instance_id）。
	promptSemMu sync.Mutex
	promptSem   map[string]chan struct{}

	// bridgeTargets 是 LLM 桥接入口的 provider → 转发目标快照（LLM-P4：原实现**每个桥接请求**都
	// 读一次 usr 配置）。重建点 = `reconcileLLMProviders`（启动 + usr 配置 refresh 广播），
	// 即与 router 的 provider 对账同源、同一时刻；快照未命中时回落直读（并顺带自愈快照）。
	bridgeMu      sync.RWMutex
	bridgeTargets map[string]BridgeTarget

	// 压缩内建（SESSION-1 阶段一）：同会话**串行 + 覆盖合并队列**（键 = instKey(instance, session)）。
	// 一个会话同一时刻只跑一个压缩任务；在飞期间的再次触发进 compressPending（只留最新），
	// 由该会话 worker 依次消费；成功 / 失败均释放（失败不影响主流程）。
	compressMu      sync.Mutex
	compressRunning map[string]bool
	compressPending map[string]compressJob
}

// askWaiter 是 ask-user 等待登记（ask-user-reply 到达后喂回 LLM + 广播 ask 任务节点 done）。
type askWaiter struct {
	askID      string // ask id（无 instance 维度的旧载荷回退查找用）
	instanceID string // 归属实例（隔离键之一；ask id 全局唯一，此处仅登记归属）
	// deliverInstance 是**卡片投递目标窗口实例**（SUBCHAT-3 阶段 F-2 · 群聊 §13 D42）：由会话
	// → 实例反查解析（`session-ask.instance_id` 取此值）；应答来自该窗口（≠ 登记实例）时按它命中
	// （见 takeAskLocked）。空 / 与 instanceID 相同 = 无定向（回落 turn 实例，旧行为）。
	deliverInstance string
	turnID          string
	toolID          string
	taskID          string // ask_user 任务节点 id（reply 到达 → tasks.done）
	// dialogID 是本 ask 所属**对话**标识（61 §4.3 session-ask `dialog_id`，§0.6 必带；
	// AskUser S1-3/S1-4）：下发时解析一次并存于此，回带不一致时留痕（路由权威仍是 ask_id）。
	// 空串 = 会话 → 对话映射确实取不到（唯一允许的空值场景，见 askWithID）。
	dialogID string
	// expiresAt 是等待登记过期时刻（与 payload expires_at 同源）：**仅显式超时才有值**；
	// 零值 = 无超时（一直等）。到期由定时器主动结算（见 expireAsk），不再惰性回收。
	expiresAt time.Time
	// ownerInstance 是发起 turn 的窗口实例（session-ask.owner_instance；跨窗口作答归属判定）。
	ownerInstance string
	// workDir/user 是 ask 的 workdir+user 作用域（后端唯一分发/订阅匹配用）。
	workDir string
	user    string
	// seq 是该 workdir 内的单调序号（每 wd 有序队列）。
	seq int64
	// receivedAt 是 ask 登记时刻（unix ms；Ask 中心快照/推送展示）。
	receivedAt int64
	// payload 是 session-ask 原载荷（Ask 中心快照/推送复用，不再重新建模）。
	payload []byte
	// autoAnswer 是超时自动回复文本（优先推荐值，否则统一提示）。
	autoAnswer string
	// timer 是显式超时定时器（nil = 无超时）；取走/清理时 Stop，避免泄漏。
	timer *time.Timer
}
