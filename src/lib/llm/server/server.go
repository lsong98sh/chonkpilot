// Package server 是 chonkpilot-server 的 LLM 会话服务核心（对齐 21-llm-server）。
//
// 定位：**调度器**——低耦合消息驱动，llm 会话上下文（instance/session/turn/tool-call-id）层层传递：
//
//	llm-start（初始化会话）→ llm-send{type=text-user|tool-result|text-notify}（进入 LLM 的统一消息）
//	→ LLM 返回经 llm-notify{type=reason|text|tool-call} 广播给监听者（UI）
//	→ tool-call → 调 gateway（上下文带 tool-call-id）→ 结果喂回 → 循环 → eof 发 llm-complete（唯一终态）
//	→ ask_user：发 ask-user → 收 ask-user-reply → 结果喂回
//
// 数据面（会话/tasktree/快照/配置持久化）一律经总线 persist 服务（chonkpilot-data/persist，本包内嵌，
// 61-消息一览 §3）；域工具经 gateway 分组注册方法面（tools/register → mcp-tools-register，
// 61-消息一览 §5）；内置 agent 集（**app 级场景** `<exeDir>/scenarios/*/` 内的 agent，25 §8.1 #8/T6）
// 只登记到内存表供子轮次回落（25 §5/T2：agent 只"注入"不"注册"）。
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-assembly"
	"github.com/chonkpilot/chonkpilot-data/auth"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	"github.com/chonkpilot/chonkpilot-plugin"
	"github.com/chonkpilot/chonkpilot-plugin/instance"
	"github.com/chonkpilot/chonkpilot-router"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
)

// 主题（相对 kebab 串；总线命名空间前缀 "chonk." 在 mq 初始化（Options.Prefix）注入一次，
// 业务层不出现 chonk. 字面）。客户端方法面/事件按域映射（61-消息一览 §4：llm-* → session-*、
// tasks.* → task-* 等）；gateway 方法面 = mcp-<组>-<动作>（61-消息一览 §5）。
// instance-* 为 GUI 客户端实例消息（instance.Subject* 单一来源，见 chonkpilot-plugin/instance）。
const (
	// SubjectExit 实例退出（取消名下 running turn + 释放锁；注册表移除由 instanceManager 负责）。
	SubjectExit = instance.SubjectExit
	// SubjectDomainToolCall 是域工具执行回调主题（相对主题，chonk. 前缀由总线注入）：
	// gateway 命中注册域工具 → Emit {tool, args, context?} → 本模块订阅者执行后写回 v.Result。
	SubjectDomainToolCall = "domain-tool-call"
	// SubjectLLMSimple 无上下文单轮 LLM 方法面（相对主题 llm-simple；chonk. 前缀注入）。
	// 请求 {prompt[, system]} → 应答 {text}；无 session/turn 上下文（compress 摘要 / 网关
	// mcp_find 语义检索等"简单 prompt→结果"场景，61-消息一览 2026-09-08 需求更新）。
	SubjectLLMSimple = msgkeys.TopicLlmSimple
)

// domainTopics 内部消息名（方法/事件，连字符/点分隔）→ 相对域主题
// （61-消息一览 §4：llm-*/ask-user/turn-start → session-*，
// tasks.*/task-stop → task-*，prompt-optimise/optimised → prompt-*，tool-retry → tool-*，
// servers-*/server-starting → server-*）。data-* 数据面已由 persist 服务直接应答（本包不再路由）。
var domainTopics = map[string]string{
	// 方法面（客户端 → server；结果写 v.Result，无 .reply）
	msgkeys.TopicLlmStart:       "session-start",
	msgkeys.TopicLlmSend:        "session-send",
	msgkeys.TopicLlmCancel:      "session-cancel",
	msgkeys.TopicAskUserReply:   "session-ask-reply",
	msgkeys.TopicTaskStop:       msgkeys.TopicTaskStop,
	msgkeys.TopicTaskBackground: msgkeys.TopicTaskBackground,
	msgkeys.TopicTaskVerify:     msgkeys.TopicTaskVerify, // 一致性校验方法面（I-87）：主题名与内部名同名，非 data-* 域
	msgkeys.TopicToolRetry:      msgkeys.TopicToolRetry,
	msgkeys.TopicPromptOptimise: msgkeys.TopicPromptOptimise,
	// 场景向导方法面（2026-10-04；agent-wizard* 同名主题，事件面由 GUI 桥在 init-data 检测后直接下发）
	msgkeys.TopicAgentWizardProbe:    msgkeys.TopicAgentWizardProbe,
	msgkeys.TopicAgentWizardCompose:  msgkeys.TopicAgentWizardCompose,
	msgkeys.TopicAgentWizardGenerate: msgkeys.TopicAgentWizardGenerate,
	msgkeys.TopicAgentWizardSkip:     msgkeys.TopicAgentWizardSkip,
	// 事件（server → 订阅者；键为前端/internal 名，非契约主题者保留字面量）
	"llm-receive":                msgkeys.TopicSessionReceive,
	"llm-complete":               msgkeys.TopicSessionComplete,
	"llm-compress":               msgkeys.TopicSessionCompress,
	"ask-user":                   msgkeys.TopicSessionAsk,
	"turn-start":                 msgkeys.TopicSessionTurnStart,
	"tasks.started":              msgkeys.TopicTaskStarted,
	"tasks.updated":              msgkeys.TopicTaskUpdated,
	"tasks.done":                 msgkeys.TopicTaskDone,
	msgkeys.TopicServerStarting:  msgkeys.TopicServerStarting,
	msgkeys.TopicPromptOptimised: msgkeys.TopicPromptOptimised,
}

// domainSubject 内部消息名 → 相对主题（publish/reply/订阅统一入口）。
// 未知名回退原名（防御）。
func domainSubject(name string) string {
	if sub, ok := domainTopics[name]; ok {
		return sub
	}
	return name
}

// llm-send 的 type（进入 LLM 的统一消息）。
const (
	SendTypeTextUser   = "text-user"   // 用户消息（触发一轮 LLM）
	SendTypeToolResult = "tool-result" // 工具结果/ask 结果（喂回 LLM 循环）
	SendTypeTextNotify = "text-notify" // 异步工具通知（进 LLM 或提示）
)

// llm-notify 的 type（LLM 返回内容广播给监听者）。
const (
	NotifyTypeReason   = "reason"    // 思考链增量
	NotifyTypeText     = "text"      // 正式回复增量
	NotifyTypeToolCall = "tool-call" // 工具请求 {tool_call_id, tool, arguments}
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
	DisableMCP        bool                     // true = 跳过内嵌（宿主自建 gateway）
	MCPServerRoot     string                   // 系统级 capability 根（默认 exe 目录/capability）；同时作为数据层 app 根（AppDir：知识库 app 级 + 场景 `../scenarios`，25 §6）
	GatewayServers    []mcpgateway.ServerEntry // 额外下游（外部 proxied 等，与内嵌 inprocess 并存）
	GatewayManageAddr string                   // 管理 REST（空 = 禁用）

	// AsyncMode 工具异步模式（"" = 默认"auto"；"never" = 强制同步，用于 CLI）。
	// 透传给内嵌 gateway 的 Params.AsyncMode。
	AsyncMode string

	// Plugins 内嵌插件（chonkpilot-plugin-compress / chonkpilot-plugin-history 等，
	// 由宿主在 New 时传入）。server.Start 在 gateway/路由/实例视图就绪后按序
	// Start 插件（全部成功才广播 server-starting；插件失败 = 启动失败）。
	Plugins []plugin.Hook

	// LogWriter 额外日志 sink（可选；**nil = 仅 stdout，即改前行为**）。
	// 2026-09-20 增补：本包既有诊断输出一律 stdout，而 GUI 单体为 `-H windowsgui`（stdout
	// 不可见）→ 宿主（GUI）注入同一个滚动文件 sink（`<dataDir>/logs/gui.log`）后，本包与
	// 插件日志**同时落文件**（见 log.go）。库侧只收 io.Writer，不依赖宿主包。
	LogWriter io.Writer

	// WorkDir / DataDir 是**服务端启动参数**（--work-dir / --data-dir，宿主在 New 时传入；
	// 空 = 未配置）。唯一消费点 = `instance-claim`（claim.go）：desktop 省略请求 work_dir
	// 时回落此处（缺省 cwd / <work_dir>/.chonkpilot，由宿主在 flag 解析处解析，见 61 §4.1 ①）。
	// 非 claim 链路不消费（实例的 work_dir/data_dir 一律以 instance-register 登记为准）。
	WorkDir string
	DataDir string

	// Form 是本宿主的**运行形态**（61 §4.6：三形态 = desktop / gui / browser）——
	// 由**入口/宿主**按形态判定后注入（D-28：宿主壳经 `gui.Options.Form`，**非 UA、非客户端
	// 自报**，22 §1）；空 = desktop（桌面单体默认构建口径）。
	// 唯一消费点 = claim 鉴权分支：desktop 免鉴权（本机单用户）、gui/browser 要求令牌。
	Form string

	// ── 认证域（61 §4.6；阶段 2b-1）─────────────────
	// AuthDBPath 是 auth 库路径（bbolt 单文件，**与业务三级库分离**）；空 = `<exe 目录>/auth.db`
	// （app-server 的 exe 旁，22 §6.4）。
	AuthDBPath string
	// UserDataRoot 是**用户数据根**（注册时建 `<UserDataRoot>/<uid>/`，业务数据每用户一套库）；
	// 空 = `<exe 目录>/data`（2b-1 只做配置项 + 默认值；接系统认证后再议 OS home）。
	UserDataRoot string
	// AllowRegister 对应配置 `auth.allowRegister`（61 §4.6：默认 true；对外暴露必须关闭）。
	// nil = 默认（true）；false = login-register 返回 `login-failed`。
	// **例外（🆕 首启一次性初始化）**：用户库为空时首个注册**仍放行**并记 `role=admin`
	// （否则「关闭注册 + 空库」= 无任何登录路径，锁死；判定与写入同事务，见 login.go register）。
	AllowRegister *bool

	// ProjectRoots 对应配置 `auth.projectRoots`（**允许根列表**，阶段 2b-2）：`instance-claim`
	// 的 `work_dir` 落在某个允许根之下（且不在任何既有项目上）→ 视为**用户自建项目**（建
	// projects 记录 + access 关联，见 claim.go）。空 = 缺省 `<用户数据根>`（<exe 目录>/data）。
	ProjectRoots []string
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
	// 作 Params.MCPServer 传入 gateway（self 节点聚合）；用户/项目级 capability 根由 server
	// 经 servers/register（dir 类型）驱动 gateway 动态接入/注销（gateway 自建 server 扫描，
	// 见 chonkpilot-mcp-gateway；T-29）。
	mcpServer *mcp.Server     // 官方 go-sdk server（capability 能力源；nil = DisableMCP）
	mcpCfg    *mcpms.Config   // 内嵌 mcp-server 执行配置（instance-register 后注入 usr/prj 配置）
	stack     *assembly.Stack // 入口装配器产物（RB-5 L5：能力源 + 执行配置 + gateway 的持有者）
	gw        gwRuntime       // 内嵌 gateway（RB-5 L1：窄接口，不持具体类型；nil = DisableMCP）

	mu     sync.Mutex
	im     *instance.Manager   // 共通实例视图（register/heartbeat/exit，对齐 61-消息一览 §4.1）
	agents map[string]AgentDef // 内置 agent 定义注册表（**app 级场景** `<exeDir>/scenarios/*/` 内的
	// agent；键 = agent 名；值 = 定义（content = 该 agent 的提示词，AG-1 子轮次回落来源）。
	// 25 §5 / T2：agent 只"注入"不"注册" —— 本表**不再**对应 gateway 资产注册
	// （`prompts/register asset_kind=agent` 已撤）；25 §8.1 #8 / T6：来源 = app 级场景（非代码 embed）。
	// 运行态 map 一律以 **instKey(instance, id)** 为键（2026-09-19 缺口 5：按 instance 分桶，
	// 同进程多 instance 互不可见/互不取消；instance 为空 → 原键，旧语义等价，见 instancescope.go）：
	turns           map[string]*turnCtx   // instKey(instance, turn) → 会话状态机
	busy            map[string]string     // instKey(instance, session) → running turn（同 instance 同 session 互斥）
	continuePending map[string]bool       // instKey(instance, turn) → 下一个 text-user 以 Kind=continue 注入
	asks            map[string]*askWaiter // instKey(instance, ask_id) → 等待的 turn + tool-call-id + 任务节点
	tasks           *taskManager          // 任务编排（任务树/级联/批量/状态事件，§6.6）
	// taskExecCancels：task 型域工具「执行中取消」登记表（I-83 收尾，18 §3.4）：tool_call_id →
	// 执行 ctx cancelFunc。domainExecute task 分支登记、结束注销；gateway 取消（tm.cancel 补发
	// mcp-tasks-report{state:cancelled}）→ onGatewayTaskDone 查表 cancel 真停执行体
	// （tool_result 轮询等）。独立小锁：回报到达时域工具可能正执行中，避免卷入 s.mu 嵌套。
	taskExecCancelMu sync.Mutex
	taskExecCancels  map[string]context.CancelFunc
	cleaned         map[string]bool       // instanceID → 已做过遗留 running 清理
	dirNodes        map[string][]string   // instanceID → 已接入的 capability dir 节点名（幂等/退出清理）
	capNodeMeta     map[string]capNodeRef // capability dir 节点名 → 归属实例 + 契约根（T-21 重扫定位）
	capWorkDirs     map[string]string     // instanceID → work_dir（T-21 按实例重建 dir 节点用）
	appTools        map[string]bool       // app 根已注册工具名（T-21② 删除/改名回收用）
	capMu           sync.Mutex            // 串行化 capability dir 节点接入/重扫（T-21）
	execCfgMu       sync.Mutex            // 串行化 loadExecConfig（G-27：instance-register 与 prj 执行配置刷新两路并发调用，内含「读配置 → SetRuntime/SetSecurityDirs」读改写序列，并发交错即 data race；不重入，见 loadExecConfig 头注）
	capWatch        *capWatcher           // 用户/项目级 capability 根 fsnotify 监听（T-21 保存即生效）
	locksDir        string                // session 锁根（默认 ~/.chonkpilot/locks；测试可注入）
	memCache        *memoryCategoryCache  // 记忆类别清单短时 TTL 缓存（I-68 ②）
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
	plugins  []plugin.Hook // 内嵌插件（Options.Plugins 拷贝；Start 时按序 Start）
	// sweepStop 心跳超时扫描停止函数（分离形态非空；默认构建 startInstanceSweep 返回 nil
	// = 零 ticker，见 sweep_split.go / sweep_inprocess.go）。
	sweepStop func()

	// MCP 四级文件配置保存即生效（T-25）：已下发给 gateway 的集合（对账基线）+ 串行化增量对账。
	mcpMu      sync.Mutex
	mcpApplied map[string]mcpgateway.ServerEntry

	// noticeSeen 是插件失败提示判重表（同轮同类只提示一次，见 pluginnotice.go）。
	noticeSeen *noticeDedup

	// ── 认证域（61 §4.6；阶段 2b-1）─────────────────
	// auth 是认证**门面**（本期 = 本地 auth 库实现；将来可换 OS 账号 / LDAP / OIDC —— 只留装配位）；
	// authTokens 是令牌签发/吊销面（同一实现）；authStop 关库；userDataRoot 是注册建目录的根。
	// authMu/authErr 串行化**惰性打开**（ensureAuth）：打开失败缓存错误 → login-* 一律 login-failed。
	authMu       sync.Mutex
	authErr      error
	auth         auth.Authenticator
	authTokens   auth.TokenIssuer
	authz        auth.ProjectAuthorizer // 项目登记 + 用户-项目关联（claim 归属校验用；同一实现）
	authStop     func()
	userDataRoot string
}

// askWaiter 是 ask-user 等待登记（ask-user-reply 到达后喂回 LLM + 广播 ask 任务节点 done）。
type askWaiter struct {
	askID      string // ask id（无 instance 维度的旧载荷回退查找用）
	instanceID string // 归属实例（隔离键之一；ask id 全局唯一，此处仅登记归属）
	turnID     string
	toolID     string
	taskID     string // ask_user 任务节点 id（reply 到达 → tasks.done）
	// expiresAt 是等待登记过期时刻（与 payload expires_at 同源，E2-1）：到期未应答 → 惰性回收
	// （见 purgeExpiredAsksLocked），避免无答复的 ask 常驻内存。
	expiresAt time.Time
}

// askTTL 是 ask-user 等待登记的存活时长（与 payload `expires_at` 同源）：30 分钟未应答即过期。
const askTTL = 30 * time.Minute

// New 构建会话服务（v2：双参，不再接收数据层 cfg——会话持久化经总线 persist，
// prompt user_config 经 opts.UsrPath 直开 usr 库；对齐 61-消息一览 §3/§4）。
func New(bus mq.Bus, opts Options) *Server {
	if opts.LLMBase == "" {
		opts.LLMBase = "http://127.0.0.1:8901/v1"
	}
	if opts.LLMModel == "" {
		opts.LLMModel = "mock"
	}
	// 额外日志 sink（可选；nil = 仅 stdout，行为同改前）：宿主注入后本包与插件日志同落文件
	// （GUI = `<dataDir>/logs/gui.log`，见 log.go）。
	setLogSink(opts.LogWriter)
	s := &Server{
		bus:             bus,
		opts:            opts,
		rt:              router.New(router.Config{}),
		gc:              newGWClient(bus),
		im:              instance.New(bus),
		turns:           make(map[string]*turnCtx),
		busy:            make(map[string]string),
		asks:            make(map[string]*askWaiter),
		taskExecCancels: make(map[string]context.CancelFunc),
		cleaned:         make(map[string]bool),
		dirNodes:        make(map[string][]string),
		capNodeMeta:     make(map[string]capNodeRef),
		capWorkDirs:     make(map[string]string),
		appTools:        make(map[string]bool),
		agents:          make(map[string]AgentDef),
		continuePending: make(map[string]bool),
	}
	s.memCache = newMemoryCategoryCache(memoryCategoryTTL)
	s.noticeSeen = newNoticeDedup()
	s.tasks = newTaskManager(s)
	s.plugins = append([]plugin.Hook{}, opts.Plugins...)
	// 内嵌 persist 数据服务（同一总线应答 data-* 面；usrPath 测试可注入）
	// Warnf（MW-11）：数据的「唯一实例回退 / 缺 instance_id」告警经本服务日志出口暴露。
	// AppDir = 系统级 capability 根（与 assembly 的 `Root` 同源，opts.MCPServerRoot）→ 数据层场景
	// 根随之 = `<AppDir>/../scenarios`（与 capability/ 平级，25 §6）；两边同源避免路径规则分叉。
	s.data = persist.New(bus, persist.Options{UsrPath: opts.UsrPath, AppDir: opts.MCPServerRoot, Warnf: logf})
	// config 域门面绑定 = inline（同进程直调）：实现 = 内嵌 persist（与 MQ handler 同源实现）
	s.cfg = s.data
	// LLM provider 对账（LR-11）：usr `llms` 全量 + exe flags 隐含默认 → router 注册集
	// （启动一次 + `data-user-config-refresh` 各一次；调用路径亦按需幂等 Register）。
	s.reconcileLLMProviders()
	// 任务层（21 §9.2 P2）：订阅既有事件 → 同步写权威表 tasktree（单写者；llm 不再自写库）
	// + 执行句柄回调（层 → 执行侧取消）。构造无失败路径；Start 见 Start()（失败只告警）。
	s.taskLayer = tasklayer.New(bus, tasklayer.Options{
		Logf:         taskLayerLogf,
		OnCancelExec: s.onTaskLayerCancelExec,
	})
	if !opts.DisableMCP {
		// 静态装配（RB-5 L5）：能力源（capability 契约）+ 执行配置 + 内嵌 gateway 由**入口
		// 装配器** `src/lib/assembly` 统一完成（gui / server 两个入口共用一份，避免装配漂移）；
		// 本函数只接成品 + 注入本层的运行期装配（四级文件 MCP 下游、执行池 sink、dir 扫描器）。
		fileServers := s.loadGatewayServers("") // 启动期尚无实例 → 空 instance_id（仅 app + user 级）
		s.applyGatewaySandboxDirs(fileServers)  // agentbox：为开启 sandbox 的条目补允许目录快照（启动期实例未注册 → 通常为空，实例注册后经 reconcile 补齐）
		servers := append([]mcpgateway.ServerEntry{}, opts.GatewayServers...)
		servers = append(servers, fileServers...)
		// 对账基线 = 本次随 Params.Servers 下发的四级文件 MCP 集合（T-25：后续保存按增量对账）。
		s.mcpApplied = make(map[string]mcpgateway.ServerEntry, len(fileServers))
		for _, e := range fileServers {
			s.mcpApplied[e.ID] = e
		}
		// 执行池控制面适配器（交付 2：层 → 执行侧，进程内直调，不经 MQ）：先建（gateway 需要
		// ExecSink 注入），gateway 建好后回填句柄。
		sink := &taskExecSink{layer: s.taskLayer}
		st, err := assembly.Build(assembly.Options{
			Bus:        bus, // 2026-09-03 去 NATS：gateway 始终复用会话总线（61-消息一览 §0.1/§5）
			Root:       opts.MCPServerRoot,
			Servers:    servers,
			AsyncMode:  opts.AsyncMode,
			ManageAddr: opts.GatewayManageAddr,
			// P3（21 §9.3）：gateway 执行态（started/detached/awaiting/error）**经层内 API 直调**
			// 纳入任务层（不新增 MQ 主题）；2026-09-18 起取消/转后台亦经本 sink 进程内直调。
			// 层未注入（库内 / gateway 单体）→ gateway 侧全部 no-op。
			ExecSink: sink,
			Logf:     logf,
		})
		// 能力源 / 执行配置恒保留（与改前「gateway 失败仍保留 mcpServer/mcpCfg」一致）
		s.stack = st
		s.mcpServer = st.Server
		s.mcpCfg = st.Config
		s.appTools = st.AppTools
		if err != nil {
			logf("[chonkpilot-server] 内嵌 gateway 失败: %v\n", err)
		} else {
			s.gw = st.Gateway
			sink.gw = st.Gateway
		}
		s.execSink = sink
	}
	return s
}

// Scan 实现 mcpgateway.ContractScanner（RB-2：gateway 不读「源」的依赖倒置注入点）：
// 扫描契约根（tools/prompts/skills/resources 四原语）→ 已注册的官方 go-sdk server，
// 供 gateway 作 dir 节点（servers/register{dir}）接入。**RB-5 L5**：实际扫描由入口装配器
// `assembly.(*Stack).Scan` 承载（与 self 能力源共用同一执行配置，I-82）；本方法保留为
// 兼容入口（`stack == nil` 时回落默认执行配置，行为与改前逐字等价）。
func (s *Server) Scan(root string) (*mcp.Server, error) {
	st := s.stack
	if st == nil {
		st = &assembly.Stack{}
	}
	return st.Scan(root)
}

// Start 启动内嵌数据服务 + gateway + 订阅客户端方法面/通知 + 注册域工具与 agent + 插件。
func (s *Server) Start(ctx context.Context) error {
	markStartupBegin() // OP-15：启动分段计时基准（仅插桩）
	// 内嵌 persist 数据服务（应答 data-* 面；先于 gateway/路由，保证数据请求可用）
	if s.data != nil {
		if err := s.data.Start(); err != nil {
			return err
		}
	}
	logStartupStage("数据服务(persist)启动")
	// 任务层（21 §9.2 P2）：订阅既有事件（task-started/updated/done、mcp-tasks-report、
	// task-deleted）→ 同步写权威表 tasktree。**失败只告警**——不进入本服务的失败路径。
	if s.taskLayer != nil {
		if err := s.taskLayer.Start(); err != nil {
			logf("[chonkpilot-task] 任务层启动失败（仅告警，主路径不受影响）: %v\n", err)
		}
	}
	logStartupStage("任务层启动")
	// 内嵌 gateway 启动（订阅 mcp-* 方法面 + 连接下游 inprocess/proxied）
	if s.gw != nil {
		if err := s.gw.Start(ctx); err != nil {
			return err
		}
	}
	logStartupStage("gateway 启动/下游连接")
	// capability 根 fsnotify 监听（T-21）：用户/项目级原语保存 → 重扫 dir 节点即时生效；
	// 系统级 app 根（<exeDir>/capability，内嵌 self 节点）→ 契约重注册 + gateway/reload 即时生效。
	// 监听失败不致命（退化为"需重注册/重启生效"的既有行为）。
	if s.gw != nil {
		if cw, err := newCapWatcher(s); err != nil {
			logf("[chonkpilot-server] capability watcher 启动失败（原语保存不热生效）: %v\n", err)
		} else {
			s.capWatch = cw
			if s.mcpCfg != nil {
				cw.watchApp(s.mcpCfg.Root) // T-21②：app 根只读，外部/构建期更新后热生效
			}
		}
	}
	logStartupStage("capability fsnotify 监听")
	// 共通实例视图（订阅 instance-register / heartbeat / exit；对齐 61-消息一览 §4.1。
	// 合并单进程形态：GUI 启动注册、进程随会话存续，无需心跳超时清理——startInstanceSweep
	// 为空实现、不调 Sweep；分离形态：起周期扫描 goroutine，心跳超时实例走 exitInstance。）
	if err := s.im.Start(); err != nil {
		return err
	}
	s.sweepStop = s.startInstanceSweep()
	logStartupStage("实例视图/心跳扫描启动")
	// 认证域（61 §4.6；阶段 2b-1）：auth 库按需惰性打开（见 login.go `ensureAuth`）——
	// 未使用的宿主不产生 auth 文件；打开失败只使 login-* 返回 login-failed，不阻断启动。
	// 域工具执行回调（gateway tools/register handler_subject → 本模块订阅执行，写回 v.Result）
	for _, sub := range []struct {
		subject string
		h       mq.VHandler
	}{
		{SubjectDomainToolCall, s.onDomainToolCall},
		{SubjectLLMSimple, s.onLLMSimple},
		{SubjectInstanceClaim, s.onInstanceClaim},         // 实例认领（请求-响应；61 §4.1 ①，阶段 2a）
		{SubjectLoginRegister, s.onLoginRegister},         // 认证域：注册（61 §4.6，阶段 2b-1）
		{SubjectLoginIn, s.onLoginIn},                     // 认证域：登录
		{SubjectLoginOut, s.onLoginOut},                   // 认证域：登出
		{SubjectLLMTestConnection, s.onLLMTestConnection}, // 设置→LLM「测试连接」只读探活（点分主题直通总线）
		{domainSubject(msgkeys.TopicLlmStart), s.onLLMStart},
		{domainSubject(msgkeys.TopicLlmSend), s.onLLMSend},
		{domainSubject(msgkeys.TopicLlmCancel), s.onLLMCancel},
		{domainSubject(msgkeys.TopicAskUserReply), s.onAskUserReply},
		{domainSubject(msgkeys.TopicTaskStop), s.onTaskStop},
		{domainSubject(msgkeys.TopicTaskBackground), s.onTaskBackground},
		{domainSubject(msgkeys.TopicTaskVerify), s.onTaskVerify}, // 一致性校验方法面（I-87；只读，层为后端）
		{domainSubject(msgkeys.TopicToolRetry), s.onToolRetry},
		{domainSubject(msgkeys.TopicPromptOptimise), s.onPromptOptimise},
		// 场景向导方法面（2026-10-04）
		{domainSubject(msgkeys.TopicAgentWizardProbe), s.onAgentWizardProbe},
		{domainSubject(msgkeys.TopicAgentWizardCompose), s.onAgentWizardCompose},
		{domainSubject(msgkeys.TopicAgentWizardGenerate), s.onAgentWizardGenerate},
		{domainSubject(msgkeys.TopicAgentWizardSkip), s.onAgentWizardSkip},
	} {
		sh, err := s.bus.On(sub.subject, 0, sub.h)
		if err != nil {
			return err
		}
		s.subs = append(s.subs, sh)
	}
	for _, sub := range []struct {
		subject string
		h       mq.Handler
	}{
		{instance.SubjectRegister, s.onInstanceRegister},              // 用户/项目级 capability 根动态注册（21-llm-server；T-29）
		{SubjectExit, s.onExit},                                       // 退出 → 取消名下 running turn + 释放锁
		{mcpgateway.SubjectTaskReport, s.onGatewayTaskDone},           // gateway 异步任务完成回报（mcp-tasks-report）→ 续轮/广播
		{mcpgateway.SubjectMCPChanged, s.onMCPChanged},                // 目录/接入变化（mcp-gateway-changed）→ 刷新工具缓存
		{msgkeys.TopicTaskDeleted, s.onTaskTreeDeleted},               // persist data-tasktree-delete 级联删除 → 内存同步（不复活）
		{msgkeys.TopicDataMemoryRefresh, s.onMemoryRefresh},           // 记忆域 save/delete 后广播（既有主题）→ 失效类别清单缓存（I-68 ②）
		{msgkeys.TopicDataUserConfigRefresh, s.onUserConfigRefresh},   // usr 配置 save/delete 后广播（既有主题）→ tool_async / llms 热生效
		{msgkeys.TopicDataMcpRefresh, s.onMCPConfigRefresh},           // MCP 四级文件配置 save/delete 后广播 → 下游 server 增量对账（保存即生效）
		{msgkeys.TopicDataPrjConfigRefresh, s.onPrjConfigRefresh},     // prj 配置 save/delete 后广播（既有主题）→ 执行配置热生效（P0-B：timeout_sec/max_concurrency/skip_dirs）
		{msgkeys.TopicDataPrjSecurityRefresh, s.onPrjSecurityRefresh}, // prj-security save/delete 后广播（既有主题）→ agentbox 允许目录热生效（security-* → mcp-server Config / gateway 上游下发）
	} {
		sh, err := s.bus.On(sub.subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
			sub.h(subj, v.Payload)
			return nil
		})
		if err != nil {
			return err
		}
		s.subs = append(s.subs, sh)
	}
	logStartupStage("方法面/事件订阅")
	// 域工具注册（gateway 分组注册面：tools/register → mcp-tools-register，61-消息一览 §5.1.1）；
	// 内置 agent 集（**app 级场景** `<exeDir>/scenarios/*/`）**只登记到内存表**（25 §5 / T2：agent
	// 不注册资产；25 §8.1 #8 / T6：来源 = app 级场景，非代码 embed）。
	// 注册失败不致命（宿主 DisableMCP/无下游时跳过），hot/目录后续刷新仍可用。
	if s.gw != nil {
		if err := s.registerDomainTools(ctx); err != nil {
			return fmt.Errorf("registerDomainTools: %w", err)
		}
	}
	logStartupStage("域工具注册")
	if s.gw != nil {
		if err := s.registerDomainAgents(ctx); err != nil {
			return fmt.Errorf("registerDomainAgents: %w", err)
		}
	}
	logStartupStage("域 agent 注册")
	// 预热工具定义缓存（gateway tools/list → ToolDef；失败降级空工具）
	preCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.gc.ListTools(preCtx)
	logStartupStage("工具定义缓存预热(gc.ListTools)")

	// 内嵌插件加载（gateway/路由/实例视图就绪后按序 Start；全部成功才广播就绪）
	deps := plugin.Deps{Bus: s.bus, Logf: s.pluginLogf, Notify: s.pluginNotify}
	for _, p := range s.plugins {
		if err := p.Start(deps); err != nil {
			return fmt.Errorf("plugin %s start: %w", p.Name(), err)
		}
	}
	logStartupStage("内嵌插件启动")
	// 就绪广播（用户要求：全部插件加载完成后发送——插件已订阅，前端/分离消费者可自检）
	s.publish(msgkeys.TopicServerStarting, map[string]any{"started_at": time.Now().Unix()})
	logStartupStage("server 启动完成")
	return nil
}

// pluginLogf 插件日志入口（统一前缀，便于区分 server 与插件）：经统一出口 logf
// （stdout + 已注入 sink，见 log.go）——插件失败/跳过一类诊断因此可落 gui.log。
// 前缀与收尾换行**逐字沿用改前实现**（插件侧格式串不带 \n）。
func (s *Server) pluginLogf(format string, args ...any) {
	logf("[chonkpilot-server][plugin] "+format+"\n", args...)
}

// taskLayerLogf 任务层告警出口（21 §9.1：层内失败一律只告警）：经统一出口 logf
// （前缀与收尾换行逐字沿用改前实现）。
func taskLayerLogf(format string, args ...any) {
	logf("[chonkpilot-task] "+format+"\n", args...)
}

// onTaskLayerCancelExec 是**层 → 执行侧**取消回调（21 §8.1-6 / §9.2 交付物 ⑤）：
// 任务层判定某节点可取消且持有 exec 句柄时回调此处 → 经**进程内 sink**（`ExecSink.CancelExec`
// → gateway `CancelExec` → `tm.cancel` → onTaskCancel → provider.Invalidate，kill + respawn
// 归属 provider，真实打断在飞调用）。2026-09-18 前此处经 `mcp-tasks-cancel` 方法面，方法面移除后
// 改为同进程直调（不新增 MQ 主题）。层已保证只告警不阻塞主路径，本回调的失败仅记日志
// （取消结果仍由终态事件/gateway 回报收敛）。
func (s *Server) onTaskLayerCancelExec(taskID string, exec tasklayer.Exec) {
	if exec.GWTaskID == "" {
		return
	}
	if s.execSink == nil {
		logf("[chonkpilot-server] 层取消回调无执行池（未注入 sink，task=%s gw_task=%s）→ 跳过\n",
			taskID, exec.GWTaskID)
		return
	}
	if ok, err := s.execSink.CancelExec(exec.InstanceID, exec.GWTaskID); err != nil || !ok {
		logf("[chonkpilot-server] 层取消回调下发失败（task=%s gw_task=%s）: ok=%v err=%v\n",
			taskID, exec.GWTaskID, ok, err)
	}
}

// onLLMSimple 无上下文单轮 LLM 方法面（SubjectLLMSimple，mq 方法调用：请求→同主题 promise）：
// 请求 {prompt[, system], instance_id[, llm]} → 单轮无 session/turn 上下文调用 LLM → 应答 {text}。
// llm 可选（SL-2 / SL-C7，2026-09-24）：LLM 选择引用（llmref = provider name）——**不传/空 =
// 现状**（exe flags 隐含默认，载荷逐字节兼容）；命中 usr provider → 用该 provider。
// 调用方 = 同进程任何总线参与方（compress 插件摘要 / memory 沉淀 / gateway mcp_find 语义检索等），
// 不依赖插件函数注入。
// prompt/system 文本里的 {{toolchain.<key>}} 占位符按实例 usr 配置替换（如摘要 system 提示词
// capability/system/summary.md 引用工具链路径）；instance_id 缺失 → 已知 key 按未配置（空串）替换。
func (s *Server) onLLMSimple(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		Prompt     string `json:"prompt"`
		System     string `json:"system,omitempty"`
		InstanceID string `json:"instance_id,omitempty"`
		// LLM 子系统默认 LLM（可选；llmref = provider name）。空 = 不指定 → specFor(nil) 回落
		// exe flags 隐含默认，且 loadLLMProvider 对空名**不做任何配置读取**（零额外开销）。
		LLM string `json:"llm,omitempty"`
	}
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return methodErr("prompt is required")
	}
	text, err := s.llmOnceSpec(
		s.specFor(s.loadLLMProvider(req.InstanceID, req.LLM)),
		s.replaceToolchain(req.InstanceID, req.System), s.replaceToolchain(req.InstanceID, req.Prompt),
	)
	if err != nil {
		return methodErr(err.Error())
	}
	v.Result = map[string]any{"text": text}
	return nil
}

// llmOnceSpec 单轮无上下文 LLM 调用（60s 超时；无可用 LLM → error），按 spec 选 provider
// （spec 来自 specFor(loadLLMProvider(...))：命中 usr provider → 该 provider；
// 未命中/未指定 → exe flags 隐含默认 `__default__`，与旧 `s.llm` 默认客户端同口径：
// -llm-base / -llm-model，openai 协议、无 apiKey）。
func (s *Server) llmOnceSpec(spec router.Spec, system, prompt string) (string, error) {
	if s.rt == nil {
		return "", errors.New("llm not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	msgs := []ChatMsg{{Role: "user", Content: prompt}}
	if system != "" {
		msgs = append([]ChatMsg{{Role: "system", Content: system}}, msgs...)
	}
	events, err := s.chat(ctx, spec, msgs, nil, ChatOptions{})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for ev := range events {
		if ev.Err != nil {
			return "", ev.Err
		}
		out.WriteString(ev.Content)
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", errors.New("empty llm result")
	}
	return strings.TrimSpace(out.String()), nil
}

// Stop 退订 + 取消全部 running turn。
func (s *Server) Stop() {
	if s.capWatch != nil {
		s.capWatch.Close()
	}
	if s.gw != nil {
		s.gw.Stop(context.Background())
	}
	if s.sweepStop != nil {
		s.sweepStop() // 分离形态：先停心跳超时扫描（默认构建为 nil，零开销）
		s.sweepStop = nil
	}
	s.im.Stop()
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	s.subs = nil
	if s.tasks != nil {
		s.tasks.stop()
	}
	// 任务层退订（P1：只订阅既有事件 + 写影子域，无自有 goroutine）
	if s.taskLayer != nil {
		s.taskLayer.Stop()
	}
	if s.data != nil {
		s.data.Stop()
	}
	// 认证域：关闭 auth 库（61 §4.6；nil = 库未打开，零开销）。
	if s.authStop != nil {
		s.authStop()
		s.authStop = nil
	}
	s.mu.Lock()
	for _, tc := range s.turns {
		tc.Close()
	}
	s.mu.Unlock()
}

// ─── tasktree 手动删除同步 ─────────────────────

// onTaskTreeDeleted persist data-tasktree-delete 级联删子树后广播（相对主题 task-deleted）
// → server 内存同步删除 + 标记（后续 persist 跳过，节点不复活）。
func (s *Server) onTaskTreeDeleted(_ string, payload []byte) {
	var msg struct {
		NodeID string `json:"node_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.NodeID == "" {
		var raw string
		if err := json.Unmarshal(payload, &raw); err == nil && raw != "" {
			msg.NodeID = raw
		}
	}
	if msg.NodeID != "" {
		s.tasks.markDeleted(msg.NodeID)
	}
}

// onMemoryRefresh persist data-memory-refresh（记忆域 save/delete 后广播，既有主题）→ 失效
// 本实例的记忆类别清单缓存（I-68 ②），下次组装指引时重新取数；载荷未带 instance_id → 全清
// （保守处置，避免漏失效导致新类别不出现）。
func (s *Server) onMemoryRefresh(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	s.memCache.invalidate(msg.InstanceID)
}

// ─── 目录/接入变化（gateway 通知） ────────────────

// onMCPChanged gateway 广播目录/接入变化（mcp-gateway-changed，kind=tool 等）
// → 刷新工具定义缓存（tools/list）。
func (s *Server) onMCPChanged(_ string, _ []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.gc.ListTools(ctx)
}

// ─── instance 管理 ────────────────────────────
// 实例注册/心跳/退出由共通 instanceManager 订阅处理（21-llm-server）；
// server 在 instance-exit 时取消名下 running turn + 释放 session 锁 + 注销 capability dir 节点。

func (s *Server) onExit(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.InstanceID == "" {
		return
	}
	s.exitInstance(msg.InstanceID)
}

// exitInstance 实例退出清理（幂等：重复调用无副作用）——显式 `instance-exit`（onExit）与
// 分离形态心跳超时（sweepStaleInstances，见 sweep_split.go）**共用同一实现**：
// 取消名下 running turn + 释放 session 锁 + 注销 capability dir 节点。
func (s *Server) exitInstance(instanceID string) {
	if instanceID == "" {
		return
	}
	s.mu.Lock()
	// 只清该 instance 名下的运行态（2026-09-19 缺口 5：按 instance 分桶 → 不误伤其他 instance）。
	for key, tc := range s.turns {
		if tc.req.InstanceID != instanceID {
			continue
		}
		tc.Close()
		delete(s.turns, key)
		delete(s.busy, instKey(instanceID, tc.req.Session))
		delete(s.continuePending, key)
	}
	// 实例退出 → 摘除该实例的**全部** ask 等待登记（E2-1：其轮次可能已被移出 s.turns，
	// 按 instance 归属整体清，不依赖 turn 存活）。
	s.clearAsksByInstanceLocked(instanceID)
	s.mu.Unlock()
	s.unregisterCapabilityNodes(instanceID)
}

// onInstanceRegister 实例注册 → 接入该实例的用户级 + 项目级 capability 根（三级根的
// 用户/项目两级；系统级 = <exeDir>/capability 由内嵌 self 节点在 New 时承载）→ 经 gateway
// 方法面 servers/register（dir 类型节点，scope=instance_id，gateway 自建官方 server +
// RegisterContracts 扫描）动态接入；目录缺失跳过、重复注册幂等。
// 注销由 onExit → servers/unregister（dir 拆除）。dir 节点工具带节点前缀
// （<instanceID>-user_ / <instanceID>-project_），归属 instance，不污染其他实例的工具面。
func (s *Server) onInstanceRegister(_ string, payload []byte) {
	var msg struct {
		InstanceID string `json:"instance_id"`
		WorkDir    string `json:"work_dir"`
	}
	_ = json.Unmarshal(payload, &msg)
	if msg.InstanceID == "" || msg.WorkDir == "" {
		return
	}
	// 执行配置注入（本实例的 usr/prj 配置 → 内嵌 mcp-server Config；见 loadExecConfig）。
	// 时序：persist 先订阅 instance-register 并登记绑定，本 handler 在其后执行（同主题按注册序），
	// 故此处经总线读 usr/prj 配置时实例已可解析。
	s.loadExecConfig(msg.InstanceID)
	if s.gw == nil {
		return // DisableMCP（宿主自建 gateway）
	}
	// agentbox（决策 42 §2 (109)）：实例的 prj `security-*` 此刻才可读 → 为开启 sandbox 的
	// MCP 条目补齐「允许目录快照」，经既有 servers/register 重下发（**零新增主题**）。
	// 仅 sandbox=true 的条目会因快照变化触发一次重注册（respawn 该上游进程）；其余条目
	// 快照保持 nil → 与已下发集合逐字段相同 → 零 diff、零动作。
	// 实例此刻**已登记**（persist 先订阅 instance-register 并登记绑定），故对账须带
	// instance_id —— 使该实例的 project/prjusr 级 MCP 正确解析（否则多实例下"唯一实例回退"串库/失败）。
	s.reconcileUserMCPs(msg.InstanceID)
	s.mu.Lock()
	s.capWorkDirs[msg.InstanceID] = msg.WorkDir
	s.mu.Unlock()
	s.registerCapabilityNodes(msg.InstanceID, msg.WorkDir)
	if s.capWatch != nil {
		s.capWatch.watchInstance(msg.InstanceID, msg.WorkDir) // T-21：监听该实例可见的 user/project/prjusr 级 capability 根
	}
}

// capNodeSpec 是要接入的一级 capability 根（节点名后缀 + 契约根目录）。
type capNodeSpec struct {
	suffix string // 节点名后缀：user / project / prjusr（节点名 = instanceID + "-" + suffix）
	root   string // 契约根（含 tools/skills/prompts/resources 四类子目录）
}

// capNodeSpecs 解析实例可见的用户级/项目级/项目私有级 capability 根（仅返回存在的目录）。
// 系统级 = <exeDir>/capability（内嵌 self 节点，New 时装配），此处不重复。
// 用户级根跨项目共享（同一 usr 根），但仍按 instance 各注册一份节点：
// 生命周期与实例对称、幂等无需引用计数（scope=instance 下异实例同名可并存），
// 且每个实例都接入同一用户根 → 跨项目可见的实际效果不丢。
//
// prjusr（项目私有级，P4 2026-10-01）：根 = <prjusr 数据根>/capability，由数据层门面
// KnowledgeRoot(kind=prjusr) 解析（与知识库/场景写读同源）；解析不出（实例未登记 / prj 打不开）
// → 跳过，不影响既有三级。
func (s *Server) capNodeSpecs(instanceID, workDir string) []capNodeSpec {
	specs := []capNodeSpec{}
	if root := persist.CapUserRoot(s.opts.UsrPath); dirExists(root) {
		specs = append(specs, capNodeSpec{suffix: "user", root: root})
	}
	if workDir != "" {
		if root := persist.CapProjectRoot(workDir); dirExists(root) {
			specs = append(specs, capNodeSpec{suffix: "project", root: root})
		}
	}
	if root := s.prjUsrCapRoot(instanceID); dirExists(root) {
		specs = append(specs, capNodeSpec{suffix: "prjusr", root: root})
	}
	return specs
}

// prjUsrCapRoot 解析实例的**项目私有级 capability 根**（<prjusr 数据根>/capability）。
// 经数据层门面 KnowledgeRoot(kind=prjusr) 解析——与知识库 / 场景的 prjusr 写读**同一根**
// （避免路径规则分叉）。解析不出（实例未登记 / prj 打不开 / project-id 缺失）→ 空串（不含该级）。
func (s *Server) prjUsrCapRoot(instanceID string) string {
	if s.data == nil || instanceID == "" {
		return ""
	}
	res, err := s.data.KnowledgeRoot(facade.KnowledgeRootRequest{
		Kind: persist.KindPrjUsr, InstanceID: instanceID,
	})
	if err != nil || res.Root == "" {
		return ""
	}
	return res.Root
}

// registerCapabilityNodes 逐根经 gateway 方法面登记 dir 节点（幂等：同名节点已登记则跳过）。
// 同名冲突不可能发生：四级根各由独立节点承载且暴露名带节点前缀
// （self_ / <instanceID>-user_ / <instanceID>-project_ / <instanceID>-prjusr_），
// gateway 仅在同 (scope, 暴露名) 时报错；
// 注册失败记日志、跳过，不影响其他节点与启动。注册后主动刷新工具缓存（见 refreshTools）。
func (s *Server) registerCapabilityNodes(instanceID, workDir string) {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	s.registerCapNodesLocked(instanceID, workDir)
}

// registerCapNodesLocked 是 registerCapabilityNodes 的无锁实现（调用方须持 capMu；
// T-21 重扫 reconcileCapabilityNodes 复用，避免自锁）。
func (s *Server) registerCapNodesLocked(instanceID, workDir string) {
	specs := s.capNodeSpecs(instanceID, workDir)
	if len(specs) == 0 {
		return
	}
	for _, spec := range specs {
		nodeName := instanceID + "-" + spec.suffix
		s.mu.Lock()
		dup := false
		for _, n := range s.dirNodes[instanceID] {
			if n == nodeName {
				dup = true
				break
			}
		}
		if !dup {
			s.dirNodes[instanceID] = append(s.dirNodes[instanceID], nodeName)
			s.capNodeMeta[nodeName] = capNodeRef{InstanceID: instanceID, Name: nodeName, Root: spec.root}
		}
		s.mu.Unlock()
		if dup {
			continue // 幂等：重复 instance-register（重连/换绑）不重复建节点
		}
		if err := s.registerDirNode(instanceID, nodeName, spec.root); err != nil {
			logf("[chonkpilot-server] capability dir 节点注册失败（跳过 %s）: %v\n", nodeName, err)
		}
	}
	// 刷新工具定义缓存：dir 节点注册 gateway 不发 mcp-gateway-changed，故此处主动重拉，
	// 使 hot=true 的用户/项目级工具进入 LLM 工具面（toolsForLLM）。
	s.refreshTools()
}

// unregisterCapabilityNodes 注销实例名下全部 capability dir 节点（onExit 对称清理）。
func (s *Server) unregisterCapabilityNodes(instanceID string) {
	if s.gw == nil {
		return // DisableMCP（无内嵌 gateway）
	}
	s.capMu.Lock()
	defer s.capMu.Unlock()
	s.mu.Lock()
	names := s.dirNodes[instanceID]
	delete(s.dirNodes, instanceID)
	delete(s.capWorkDirs, instanceID)
	for _, nodeName := range names {
		delete(s.capNodeMeta, nodeName)
	}
	s.mu.Unlock()
	for _, nodeName := range names {
		s.unregisterDirNode(instanceID, nodeName)
	}
	if len(names) > 0 {
		s.refreshTools()
	}
}

// reconcileCapabilityNodes 重扫全部在用 capability 根并重建 dir 节点（T-21：原语保存后热生效）。
// 复用既有消息面 servers/unregister + servers/register（零新增主题）：逐实例先拆除全部已登记
// 节点（含根已消失者），再按当前文件树（capNodeSpecs）重新扫描注册并刷新工具缓存。
// 由 capWatcher 去抖后调用；网关 unregister/register 亦广播 mcp-gateway-changed，工具缓存同步刷新。
func (s *Server) reconcileCapabilityNodes() {
	if s.gw == nil {
		return
	}
	s.capMu.Lock()
	defer s.capMu.Unlock()
	s.mu.Lock()
	insts := make(map[string]string, len(s.capWorkDirs))
	for k, v := range s.capWorkDirs {
		insts[k] = v
	}
	s.mu.Unlock()
	removed := false
	for inst, wd := range insts {
		s.mu.Lock()
		names := append([]string(nil), s.dirNodes[inst]...)
		delete(s.dirNodes, inst)
		for _, nodeName := range names {
			delete(s.capNodeMeta, nodeName)
		}
		s.mu.Unlock()
		if len(names) > 0 {
			removed = true
		}
		for _, nodeName := range names {
			s.unregisterDirNode(inst, nodeName)
		}
		s.registerCapNodesLocked(inst, wd) // 按当前根重建（根不存在 → 自动跳过）
	}
	if removed {
		// registerCapNodesLocked 在无根时提前返回、不刷新缓存；此处兜底确保被移除的工具退出工具面。
		s.refreshTools()
	}
}

// reloadAppContracts 重扫系统级 app capability 根（T-21②：三级根中 <exeDir>/capability 由内嵌
// self 节点在 New 时一次性承载，不能像 user/project 两级那样经 dir 节点 unregister+register 重建）。
// 链路：重新 RegisterContracts 进同一内嵌 go-sdk server（同名原语覆盖更新 = 契约改动生效、
// 新增原语注入）→ RemoveTools 回收已删除/改名的 app 工具（RegisterContracts 只增改不删）→
// 复用既有方法面 gateway/reload 让 self 节点重拉 list → 聚合工具面刷新（零新增主题）。
// 只读语义不变：UI 不可改 app 根，仅外部/构建期更新后热生效。
func (s *Server) reloadAppContracts() {
	if s.gw == nil || s.mcpServer == nil || s.mcpCfg == nil {
		return
	}
	root := s.mcpCfg.Root
	if err := mcpms.RegisterContracts(s.mcpServer, root, s.mcpCfg); err != nil {
		logf("[chonkpilot-server] app capability 契约重扫失败: %v\n", err)
		return
	}
	cur := appToolNames(root)
	s.mu.Lock()
	var stale []string
	for name := range s.appTools {
		if !cur[name] {
			stale = append(stale, name)
		}
	}
	s.appTools = cur
	s.mu.Unlock()
	if len(stale) > 0 {
		s.mcpServer.RemoveTools(stale...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.emitGateway(ctx, "gateway/reload", nil) // self 节点重拉 list（含新契约工具）
	s.refreshTools()                                 // 本端工具定义缓存同步刷新（toolsForLLM 依据）
}

// appToolNames 递归收集 app capability 根下全部 *.tool.md 的原语名（T-21② 重扫回收用；
// **RB-5 L5**：实现已收敛到入口装配器 `assembly.AppToolNames`，此处保留为包内薄别名）。
func appToolNames(root string) map[string]bool { return assembly.AppToolNames(root) }

// registerDirNode 经 gateway 方法面登记 dir 类型节点（servers/register → mcp-servers-register）。
func (s *Server) registerDirNode(instanceID, nodeName, folder string) error {
	ctx, cancel := context.WithTimeout(withInstance(context.Background(), instanceID), 15*time.Second)
	defer cancel()
	_, err := s.emitGateway(ctx, "servers/register", map[string]any{
		"name": nodeName, "dir": folder, "scope": instanceID,
	})
	return err
}

// unregisterDirNode 经 gateway 方法面注销 dir 类型节点（servers/unregister → 拆除）。
func (s *Server) unregisterDirNode(instanceID, nodeName string) {
	ctx, cancel := context.WithTimeout(withInstance(context.Background(), instanceID), 15*time.Second)
	defer cancel()
	_, _ = s.emitGateway(ctx, "servers/unregister", map[string]any{"name": nodeName, "scope": instanceID})
}

// refreshTools 重新拉取 gateway tools/list 刷新工具定义缓存（toolsForLLM 的 hot 过滤依据）。
func (s *Server) refreshTools() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = s.gc.ListTools(ctx)
}

// dirExists 判定路径存在且为目录（capability 根接入前检查；不存在则跳过）。
func dirExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// ─── llm-start / llm-send / llm-cancel / ask-user-reply ─────────────

// onLLMStart 受理轮次开始：session/turn 由客户端分配（必填，不可为空）；work_dir/data_dir
// 取 instance-register 绑定的记录（载荷不传）。幂等落库 session + turn（已存在复用，不重新分配）。
//
// Continue（同轮次继续，llm-start 可选字段）：复用该 session 最后一轮 turn（未带 turn 时
// 由后端按 session 取最近一轮），turn id 保持不变、状态回写 running；不发 turn-start（非新轮）。
// 结果直接写 v.Result（同主题 promise），无 .reply。
func (s *Server) onLLMStart(_ context.Context, _ string, v *mq.Value) error {
	var req StartReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Result = map[string]any{"accepted": false, "error": "bad request"}
		return nil
	}
	if req.Session == "" {
		v.Result = map[string]any{"accepted": false, "error": "session is required"}
		return nil
	}
	// turn 必填；例外：同轮次继续（continue=true）未带 turn → 由后端按 session 解析最近一轮。
	if req.Turn == "" && !req.Continue {
		v.Result = map[string]any{"accepted": false, "error": "session and turn are required"}
		return nil
	}
	// instance 不应为空（R-11 二次升级：instance 为空即异常，顶层错误、不进 turn）。
	if strings.TrimSpace(req.InstanceID) == "" {
		v.Result = map[string]any{"accepted": false, "error": "instance_id is required"}
		return nil
	}
	rec, ok := s.im.Lookup(req.InstanceID)
	if !ok {
		v.Result = map[string]any{"accepted": false, "error": "instance not registered"}
		return nil
	}
	req.WorkDir = rec.WorkDir // work_dir/data_dir 随 instance-id 绑定，不随 llm-start 传
	req.DataDir = rec.DataDir

	// 首次使用该 instance 时清理遗留（多 workdir 服务，启动时无从预知数据根）：
	// 会话 turn（data-session-cleanup-stale）+ 任务树遗留非终态（**任务层 Recover**，21 §9.2 ⑥）
	s.mu.Lock()
	firstUse := !s.cleaned[req.InstanceID]
	if firstUse {
		s.cleaned[req.InstanceID] = true
	}
	s.mu.Unlock()
	if firstUse {
		store := newSessionStore(s.bus, req.InstanceID)
		_ = store.CleanupStaleTurns()
		s.tasks.recoverStaleTasktree(req.InstanceID)
	}

	s.mu.Lock()
	// 同 instance 同 session 互斥（2026-09-19 缺口 5：busy 按 instance 分桶，
	// 不同 instance 的同名 session 互不阻塞）。
	if busy, exists := s.busy[instKey(req.InstanceID, req.Session)]; exists {
		s.mu.Unlock()
		v.Result = map[string]any{"accepted": false, "error": "session busy: " + busy}
		return nil
	}
	s.mu.Unlock()

	store := newSessionStore(s.bus, req.InstanceID)
	// 同轮次继续：未带 turn → 后端取该 session 最近一轮（带 turn 则直接采用）。
	if req.Continue && req.Turn == "" {
		req.Turn = store.LatestTurnID(req.Session)
		if req.Turn == "" {
			v.Result = map[string]any{"accepted": false, "error": "no turn to continue"}
			return nil
		}
	}

	// 文件锁排他（跨进程）：同 session 只能有一个活动 turn（含分离形态多客户端；崩溃由 OS 释放）
	lock, err := s.lockSession(req.Session)
	if err != nil {
		if errors.Is(err, errSessionBusy) {
			v.Result = map[string]any{"accepted": false, "error": "session busy (locked)"}
		} else {
			v.Result = map[string]any{"accepted": false, "error": "lock failed: " + err.Error()}
		}
		return nil
	}

	// 落库：session + turn（幂等；监听时检查并落库，不重新分配）——continue 复用同一 turn，
	// 回写 running 并清空 finish_reason（原 finished/interrupted 脏状态复位）。
	_ = store.EnsureSession(req.Session)
	_ = store.EnsureTurn(req.Turn, req.Session)
	if req.Continue {
		_ = store.ReopenTurn(req.Turn)
	}

	// 建立 turn 状态机（调度器）：主轮次无上一级 → ctx 根为 Background（G-18 gapA）。
	tc := newTurnCtx(context.Background(), s, req)
	tc.sessionLock = lock
	s.mu.Lock()
	// 运行态登记键 = instKey(instance, id)（缺口 5：按 instance 分桶）。
	s.busy[instKey(req.InstanceID, req.Session)] = req.Turn
	s.turns[instKey(req.InstanceID, req.Turn)] = tc
	if req.Continue {
		// 同轮次继续：首个 text-user 以 Kind=continue 注入（非新轮边界）。
		s.continuePending[instKey(req.InstanceID, req.Turn)] = true
	} else {
		delete(s.continuePending, instKey(req.InstanceID, req.Turn))
	}
	s.mu.Unlock()

	// 轮次开始广播（turn-start；主轮次 parents 空——前端/插件据此驱动会话树与 history 提交）。
	// continue 复用同一轮：不再广播，避免会话树/history 重复提交。
	if !req.Continue {
		s.emitTurnStart(req)
	}

	v.Result = map[string]any{"accepted": true, "session": req.Session, "turn": req.Turn}
	return nil
}

// onLLMSend 调度核心：text-user / text-notify / tool-result → 喂给 turn 状态机。
// 同轮次继续（llm-start{continue:true}）后首个 text-user → Kind=continue 注入。
// turn 缺省（continue 未带 turn，桥原样透传空 turn）→ 按 session 回退运行中轮次。
func (s *Server) onLLMSend(_ context.Context, _ string, v *mq.Value) error {
	var req SendReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		return nil
	}
	s.mu.Lock()
	// 按 instance 归属取轮次（缺口 5）：载荷带 instance_id → 只在本 instance 桶内命中；
	// 未带（旧调用方）→ 全桶回退（旧语义）。
	tc := s.lookupTurnLocked(req.InstanceID, req.Turn)
	if tc == nil && req.Turn == "" && req.Session != "" {
		tc = s.turnBySessionLocked(req.InstanceID, req.Session)
	}
	key := instKey(req.InstanceID, req.Turn)
	if tc != nil {
		key = instKey(tc.req.InstanceID, tc.req.Turn)
	}
	cont := s.continuePending[key]
	if cont {
		delete(s.continuePending, key)
	}
	s.mu.Unlock()
	if tc == nil {
		return nil
	}
	switch req.Type {
	case SendTypeTextUser:
		if cont {
			tc.FeedContinue(req.Content)
		} else {
			tc.FeedText(req.Content)
		}
	case SendTypeTextNotify:
		tc.FeedNotify(req.Content)
	case SendTypeToolResult:
		tc.FeedToolResult(req.ToolCallID, req.Content)
	}
	return nil
}

// onLLMCancel 用户取消：以 llm-start 分配的 session+turn 为依据定位轮次。
// S6：取消发 llm-complete{status:interrupted} 终态（chatOnce 侧 ctx 取消静默，不再发 llm-error）。
// turn 缺省（旧 RPC CancelChat 不带 turn）→ 按 session 回退运行中轮次。
func (s *Server) onLLMCancel(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		Session    string `json:"session"`
		Turn       string `json:"turn"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	// 锁内**只做定位**（缺口 5）：带 instance_id → 只在本 instance 桶内命中（**不会取消
	// 其他 instance 的同名 session/turn**）；未带 → 全桶回退（旧语义）。
	s.mu.Lock()
	tc := s.lookupTurnLocked(req.InstanceID, req.Turn)
	if tc == nil && req.Turn == "" && req.Session != "" {
		tc = s.turnBySessionLocked(req.InstanceID, req.Session)
	}
	matched := tc != nil && tc.req.Session == req.Session
	busyTurns := len(s.turns)
	s.mu.Unlock()
	if !matched {
		logf("[llm-cancel] no running turn for instance=%s session=%s turn=%s (busy turns=%d)\n",
			req.InstanceID, req.Session, req.Turn, busyTurns)
		v.Result = map[string]any{"cancelled": false, "turn": req.Turn}
		return nil
	}
	// I-35：级联取消该 turn 在飞工具/任务（含 llm_run 子树节点 + gateway 异步任务），
	// 否则用户"停止"后工具可能仍在跑。复用 task-stop 的 cancelSubtree（done 幂等：
	// 与 task-stop 不冲突、不重复广播）；P2：状态判定以层为权威、执行侧取消经层回调下发。
	// 2026-09-19：级联同样带 instance 归属（缺口 3）→ 只取消本 instance 的同 turn 任务。
	//
	// **必须在 s.mu 之外**（2026-09-22 修）：总线 Emit 是**同 goroutine 同步派发**，
	// 级联链 `cancelByTurn → 层 CancelSubtree → OnCancelExec → sink.CancelExec →
	// gateway ep.cancel → reportTaskDone → Emit(mcp-tasks-report)` 会在本 goroutine 上
	// 同步回到 `onGatewayTaskDone`，后者 `s.mu.Lock()`；持锁级联 = 同一 goroutine 对
	// 非重入互斥自锁 → 永久挂起（宿主 publish 走 WebView2 UI 线程 → 窗口整体冻结）。
	s.tasks.cancelByTurn(tc.req.InstanceID, tc.req.Turn)
	s.complete(tc, "interrupted", "interrupted", "")
	tc.Close()
	v.Result = map[string]any{"cancelled": true, "turn": req.Turn}
	return nil
}

// onAskUserReply ask 应答 → 作为 tool-result 喂回 LLM 循环；ask 任务节点 → tasks.done。
func (s *Server) onAskUserReply(_ context.Context, _ string, v *mq.Value) error {
	// 载荷字段 = ask_id（61 §4.2；I-152 已闭环，旧连字符名不再回落）。
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	askID := str(raw["ask_id"])
	answer := str(raw["answer"])
	// 按 instance 归属取走等待登记（缺口 5）：带 instance_id → 只在本 instance 桶内命中；
	// 未带（旧载荷/旧调用方）→ 全桶回退（ask id 全局唯一，旧语义等价）。
	s.mu.Lock()
	w, ok := s.takeAskLocked(str(raw["instance_id"]), askID)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if w.taskID != "" {
		s.tasks.done(w.taskID, TaskStateDone, answer, "")
	}
	s.mu.Lock()
	tc := s.lookupTurnLocked(w.instanceID, w.turnID)
	s.mu.Unlock()
	if tc != nil {
		tc.FeedToolResult(w.toolID, answer)
	}
	return nil
}

// ask 登记等待（tool-call 命中 ask_user 时调用）：广播 ask-user
// （对齐 msg-ref §4.3：ask_id/question/options/custom/session/turn/expires_at + multi/recommended）。
func (s *Server) ask(turnID, toolID string, args map[string]any, taskID string) string {
	askID := "ask-" + newID()
	expiresAt := time.Now().Add(askTTL)
	s.mu.Lock()
	// 惰性回收过期等待登记（E2-1）：无答复的 ask 到期即摘除，避免常驻内存。
	s.purgeExpiredAsksLocked(time.Now())
	// 实例归属从 turn 取（业务 payload 一律必带 instance_id，见 61-消息一览 §0.1）；
	// 登记键 = instKey(instance, ask)（缺口 5：按 instance 分桶，不与其他 instance 串味）。
	instanceID := ""
	if tc := s.lookupTurnLocked("", turnID); tc != nil {
		instanceID = tc.req.InstanceID
	}
	s.asks[instKey(instanceID, askID)] = &askWaiter{
		askID: askID, instanceID: instanceID, turnID: turnID, toolID: toolID, taskID: taskID,
		expiresAt: expiresAt,
	}
	s.mu.Unlock()
	payload := map[string]any{
		"instance_id": instanceID,
		"ask_id":      askID, "question": askQuestion(args),
		"session": s.turnSession(turnID), "turn": turnID,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	}
	if opts, ok := args["options"]; ok {
		payload["options"] = opts
	}
	if multi, ok := args["multi"].(bool); ok {
		payload["multi"] = multi
	}
	// recommended 统一为**数组**（前端 AskUser 按 Array 渲染高亮）；兼容单值字符串
	// → 单元素数组（I-41：契约/server/前端三处类型对齐）。
	if rec, ok := args["recommended"]; ok {
		switch rv := rec.(type) {
		case string:
			if rv != "" {
				payload["recommended"] = []string{rv}
			}
		case []any:
			arr := make([]string, 0, len(rv))
			for _, it := range rv {
				if s, sok := it.(string); sok && s != "" {
					arr = append(arr, s)
				}
			}
			if len(arr) > 0 {
				payload["recommended"] = arr
			}
		case []string:
			if len(rv) > 0 {
				payload["recommended"] = rv
			}
		}
	}
	s.publish("ask-user", payload)
	return askID
}

// onTaskStop 用户手动结束任务（§6.3 场景 7 / §6.6 取消）：级联定位任务子树 →
// 节点标 cancelled + 广播 tasks.done（cascade 沿 parent_id）。P2：**状态判定以任务层为权威**
// （层判定可取消集合）+ **层 → 执行侧**取消（`cancelSubtree` → 层 `CancelSubtree` 对持 exec
// 句柄的节点回调 `OnCancelExec` → `onTaskLayerCancelExec` → 进程内 sink `CancelExec(gw_task_id)`
// → gateway `tm.cancel` → onTaskCancel → provider.Invalidate，**真实打断在飞调用**，
// 21 §9.2 交付物 ⑤）。2026-09-18：执行侧下发不再经 `mcp-tasks-cancel` 方法面。
//
// id 兼容（2026-09-18）：`task_id` = server 任务节点 id（任务树 ▍ / useTaskView 传入）；
// 前端工具行「停止」在裁决态只有 LLM tool_call_id（气泡上无 server 节点 id，其
// `message.task_id` / `arbitration.task_id` 均为 gateway 执行 id）→ 载荷**可选扩参**
// `tool_call_id`，先经内存任务节点反查归一到 `task_id`（**不新增 MQ 主题**，61 §4.2）。
// 错误语义：id 缺失/未命中、已不可逆终态、执行池未注入（确有在飞执行）→ 明确报错（不静默）。
func (s *Server) onTaskStop(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		TaskID     string `json:"task_id"`
		ToolCallID string `json:"tool_call_id"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &req)

	// ① id 归一：优先 task_id（任务节点 id）；缺省按 tool_call_id 反查已登记任务节点。
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		if callID := strings.TrimSpace(req.ToolCallID); callID != "" {
			if n := s.tasks.findByToolCall(req.InstanceID, callID); n != nil {
				taskID = n.TaskID
			}
		}
	}
	if taskID == "" {
		return methodErr("task-stop: task_id 或 tool_call_id 必填，且需命中已登记任务")
	}

	// ② 层为权威：已不可逆终态（done/error/cancelled）→ 明确拒绝，不重复下发执行侧取消。
	if s.taskLayer != nil {
		if st, ok := s.taskLayer.State(taskID); ok && terminalTaskState(st) {
			return methodErr("任务已结束（" + st + "），无需停止")
		}
	}

	// ③ 可打断性门控：层持 exec 句柄（`ExecOf` → gw_task_id）时，取消经**层回调**下发执行侧；
	// 执行池未注入（sink nil）且确有在飞执行 → 明确报错（不静默，避免"点了没反应"）。
	if s.execSink == nil && s.taskLayer != nil {
		if exec, ok := s.taskLayer.ExecOf(taskID); ok && exec.GWTaskID != "" {
			return methodErr("执行池不可用（未注入 sink），无法打断任务 " + taskID)
		}
	}

	// ④ 层先标终态 + 广播 tasks.done（cascade 沿 parent_id；层内 exec_json 保留）。
	// 2026-09-19：带 instance 归属（缺口 3）→ 只取消该 instance 的节点。
	cancelled := s.tasks.cancelSubtree(req.InstanceID, taskID)
	if cancelled == 0 {
		return methodErr("任务不存在或已结束: " + taskID)
	}
	v.Result = map[string]any{"cancelled": true, "task_id": taskID}
	return nil
}

// onTaskBackground 前端「转后台」（61-消息一览 §4）：收到 {tool_call_id} → 经**层 + 进程内 sink**
// 解绑命中的在飞任务（manual）→ 返回 {task_id}。解绑后原同步调用返回 pending（turn.go 登记
// gwTaskID + turn 挂起），终态经 mcp-tasks-report 回报。
// 2026-09-18（用户决定）：不再经 gateway `tools/background` 方法面——层持有 exec 句柄
// （`ExecOf` → gw_task_id），经 sink `DetachExec` 同进程直调执行池；层未登记该执行的句柄 →
// 以 tool_call_id 交 gateway 定位（既有宽容语义）。
// 未命中 / 已终态 / 已解绑 → 执行侧回错误 → 本方法经 errors 语义上抛。
func (s *Server) onTaskBackground(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		ToolCallID string `json:"tool_call_id"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	if strings.TrimSpace(req.ToolCallID) == "" {
		return methodErr("tool_call_id is required")
	}
	ref := req.ToolCallID
	// 归属 instance（缺口 2：执行池按 instance 分桶）：载荷未带而节点命中 → 取节点归属，
	// 保证带 gw_task_id 直调执行池时能落到正确桶。
	inst := req.InstanceID
	// 硬门控（G-17）：仅 manual（同步运行但任务化、可解绑）允许转后台；
	// auto/always/never 无在飞可解绑任务（auto 超阈值自动转），直接 errors、不下发执行侧。
	// 工具名由任务节点反查；节点缺失或工具不在 tools/list 缓存（无法判定）→ 放行交执行侧判定。
	if node := s.tasks.findByToolCall(req.InstanceID, req.ToolCallID); node != nil {
		inst = node.InstanceID
		// P2：状态判定以任务层为权威（21 §9.2 交付物 ⑤）——层已判为不可逆终态 → 无需转后台。
		if s.taskLayer != nil {
			if st, ok := s.taskLayer.State(node.TaskID); ok {
				switch st {
				case TaskStateDone, TaskStateError, TaskStateCancelled:
					return methodErr("任务已结束（" + st + "），无需转后台")
				}
			}
			// 层持执行句柄（P3）→ 以 gw_task_id 直调执行池（与取消回调同源）。
			if exec, ok := s.taskLayer.ExecOf(node.TaskID); ok && exec.GWTaskID != "" {
				ref = exec.GWTaskID
			}
		}
		if mode := s.gc.AsyncMode(node.Tool); mode != "" && mode != "manual" {
			return methodErr("该工具非 manual 模式（" + mode + "），无需转后台")
		}
	}
	if s.execSink == nil {
		return methodErr("执行池不可用（未注入 sink），无法转后台")
	}
	taskID, err := s.execSink.DetachExec(inst, ref)
	if err != nil {
		return methodErr(err.Error())
	}
	v.Result = map[string]any{"task_id": taskID}
	return nil
}

func (s *Server) turnSession(turnID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tc := s.lookupTurnLocked("", turnID); tc != nil {
		return tc.req.Session
	}
	return ""
}

// onToolRetry 重试工具（不经 LLM，msg-ref §4.2 完整实现）：定位（task_id tk-* 内存直取校验
// 会话归属；回退 {session, turn}(+tool_call_id / 前端漂移 task_id=tool_call_id) 取该轮最后一条
// interrupted）→ gateway tools/call 重跑 → 终态广播 → 结果喂回该轮续轮（tool 结果进入下一条
// LLM 消息）。进程重启/死 turn（内存无节点/无运行轮次）→ 按库重建上下文（retryRecover）。
func (s *Server) onToolRetry(_ context.Context, _ string, v *mq.Value) error {
	var raw map[string]any
	_ = json.Unmarshal(v.Payload, &raw)
	session := str(raw["session"])
	if session == "" {
		session = str(raw["session_id"])
	}
	turn := str(raw["turn"])
	toolCallID := str(raw["tool_call_id"])
	taskID := str(raw["task_id"])
	instanceID := str(raw["instance_id"])
	req := retryReq{Session: session, Turn: turn, ToolCallID: toolCallID, TaskID: taskID, InstanceID: instanceID}

	node := s.retryTarget(req)
	tc := s.retryRunningTurn(node)
	if node == nil || tc == nil {
		// 内存未完整命中（节点丢失 / 轮次已死 / 进程重启）→ 按库重建（节点 + 续轮上下文）
		var herr error
		node, tc, herr = s.retryRecover(req, node)
		if herr != nil {
			v.Result = map[string]any{"ok": false, "error": herr.Error()}
			return nil
		}
	}
	if node == nil || node.Tool == "" {
		v.Result = map[string]any{"ok": false, "error": "tool not found"}
		return nil
	}
	// 上下文按节点归属还原（work_dir/data_dir 取 instance 绑定）；树归属同下发（I-90：
	// top_session = 节点所属主会话，parent = 被重试的 llm 侧节点本身）。
	ctxMsg := mcpgateway.Context{
		Session: node.SessionID, Turn: node.TurnID,
		InstanceID: node.InstanceID, ToolCallID: node.ToolCallID,
		TopSession: node.TopSession, Parent: node.TaskID,
	}
	if rec, ok := s.im.Lookup(node.InstanceID); ok {
		ctxMsg.WorkDir = rec.WorkDir
		ctxMsg.DataDir = rec.DataDir
	}
	args := node.args
	if args == nil {
		args = map[string]any{}
	}
	res, gwTaskID, err := s.gc.Call(context.Background(), node.Tool, args, ctxMsg)
	if err != nil {
		s.tasks.done(node.TaskID, TaskStateError, "", err.Error())
		v.Result = map[string]any{"ok": false, "error": err.Error()}
		return nil
	}
	if gwTaskID != "" {
		// 转后台：节点挂 gateway 任务等待完成回报（onGatewayTaskDone 续轮/广播）
		s.tasks.setGwTask(node.TaskID, gwTaskID)
		s.tasks.setState(node.TaskID, TaskStatePending)
		v.Result = map[string]any{"ok": true, "task_id": gwTaskID}
		return nil
	}
	// 同步完成：终态广播 + 结果喂回续轮（下一条 LLM 消息含 role=tool + 原 tool-call-id）
	summary := resultTextFromMap(res)
	s.tasks.done(node.TaskID, TaskStateDone, summary, "")
	if tc != nil {
		tc.FeedToolResult(node.ToolCallID, summary)
	}
	v.Result = map[string]any{"ok": true, "task_id": node.TaskID}
	return nil
}

// retryReq 是 tool-retry 定位输入（多形态兼容：{session, turn} 契约 /
// {session_id, task_id=tool_call_id} 前端现状）。
type retryReq struct {
	Session    string
	Turn       string
	ToolCallID string
	TaskID     string
	InstanceID string
}

// retryTarget 内存定位重试目标（msg-ref §4.2）：task_id（tk-* 内存 TaskID）直取并校验会话归属；
// 否则按 {session, turn}(+tool_call_id；前端漂移时 task_id 装载 tool_call_id) 取该轮最后一条
// interrupted（findRetryTarget）。未命中 → nil。
func (s *Server) retryTarget(req retryReq) *TaskNode {
	if strings.HasPrefix(req.TaskID, "tk-") {
		n := s.tasks.find(req.TaskID)
		if n == nil {
			return nil
		}
		if req.InstanceID != "" && n.InstanceID != req.InstanceID {
			return nil // 实例归属校验（缺口 3：不跨 instance 直取他人节点）
		}
		if req.Session != "" && n.SessionID != req.Session {
			return nil // 会话归属校验
		}
		return n
	}
	tcid := req.ToolCallID
	if tcid == "" {
		tcid = req.TaskID
	}
	return s.tasks.findRetryTarget(req.InstanceID, req.Session, req.Turn, tcid)
}

// retryRunningTurn 按重试目标定位运行中的轮次（turnCtx；无 → nil）。
// 按节点归属 instance 取（缺口 5：不跨 instance 命中同 turn id 的轮次）。
func (s *Server) retryRunningTurn(node *TaskNode) *turnCtx {
	if node == nil || node.TurnID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupTurnLocked(node.InstanceID, node.TurnID)
}

// retryRecover 重启/死 turn 恢复（msg-ref §4.2 retryStaleTarget/retryRecover）：
//   - node 为空 → 从库 tasktree 找 interrupted 节点（该 session；tool_call_id 匹配优先）；
//   - 轮次消息（user + 末条 assistant.tool_calls）为续轮上下文；该轮遗留的 tool_pair 已由启动
//     清理标 interrupted，重试结果**就地回填同一行**（I-176）；
//     缺失 assistant pair → 明确错误（tool not found：消息记录不完整）；
//   - 目标节点已 done / 库无 interrupted 节点 → tool not found（不重跑已完成 pair）。
//
// 返回 (恢复节点, 恢复的 turnCtx, error)；error 非 nil 时不建任何内存态。
func (s *Server) retryRecover(req retryReq, known *TaskNode) (*TaskNode, *turnCtx, error) {
	instanceID := req.InstanceID
	if instanceID == "" {
		if recs := s.im.List(); len(recs) == 1 {
			instanceID = recs[0].ID
		}
	}
	if instanceID == "" {
		return nil, nil, errors.New("tool not found（实例不可定位，无法恢复）")
	}
	if req.Session == "" || req.Turn == "" {
		return nil, nil, errors.New("tool not found（恢复需 session+turn）")
	}
	// ① 节点：内存已知直接用；否则从库 tasktree 定位 interrupted 节点
	node := known
	if node == nil {
		n, err := s.dbRetryNode(instanceID, req)
		if err != nil {
			return nil, nil, err
		}
		node = n
	}
	if node.TurnID == "" {
		node.TurnID = req.Turn // tasktree 记录不存 turn_id；续轮消息按请求 turn 读取
	}
	// ② 续轮上下文 = 该轮消息（末条 assistant.tool_calls 无 tool 结果）
	st := newSessionStore(s.bus, instanceID)
	msgs := st.LoadMessages(req.Turn)
	toolName, args, toolCallID, ok := lastToolCallPair(msgs, req.ToolCallID)
	if !ok {
		return nil, nil, errors.New("tool not found（消息记录不完整：该轮缺少 assistant.tool_calls pair，无法恢复续轮上下文）")
	}
	if node.Tool == "" {
		node.Tool = toolName
	}
	if node.args == nil {
		node.args = args
	}
	if node.ToolCallID == "" {
		node.ToolCallID = toolCallID
	}
	// ③ 登记内存任务表（沿用原 task_id；不广播，随后 done 广播终态）
	s.tasks.restoreStale(node)
	// ④ 重建运行中轮次（hist 留空——msgs() 从库读当前轮消息；续轮工具结果与
	// 该轮 assistant.tool_calls 成对进入下一条 LLM 消息）
	tc := s.recoverTurnCtx(instanceID, req.Session, req.Turn)
	return node, tc, nil
}

// dbRetryNode 从库 tasktree 定位 interrupted 目标节点（data-tasktree-list 按 top_session
// 枚举；会话内取 tool_call_id 匹配优先，其次最近一条 interrupted；无 interrupted → 不重跑）。
func (s *Server) dbRetryNode(instanceID string, req retryReq) (*TaskNode, error) {
	res, err := dataRequest(s.bus, msgkeys.TopicDataTasktreeList, map[string]any{
		"instance_id": instanceID, "data": map[string]any{"top_session": req.Session},
	})
	if err != nil {
		return nil, errors.New("tool not found（tasktree 读取失败: " + err.Error() + "）")
	}
	var match *TaskNode
	raw, _ := res["nodes"].([]any)
	for _, it := range raw {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		if str(m["session_id"]) != req.Session {
			continue
		}
		if str(m["status"]) != TaskStateInterrupted {
			continue
		}
		if req.ToolCallID != "" && str(m["tool_call_id"]) != req.ToolCallID {
			continue
		}
		n := nodeFromDBRec(m, instanceID)
		if n == nil {
			continue
		}
		if match == nil || (req.ToolCallID == "" && n.ToolCallID == req.TaskID) {
			match = n
		}
		if req.ToolCallID != "" && match.ToolCallID == req.ToolCallID {
			break
		}
	}
	if match == nil {
		return nil, errors.New("tool not found（无 interrupted 目标节点）")
	}
	return match, nil
}

// nodeFromDBRec 把 data-tasktree-list 节点记录还原为内存 TaskNode（可重试中断节点）。
func nodeFromDBRec(m map[string]any, instanceID string) *TaskNode {
	taskID := str(m["node_id"])
	if taskID == "" {
		taskID = str(m["task_id"])
	}
	if taskID == "" {
		return nil
	}
	title := str(m["title"])
	if title == "" {
		title = str(m["name"])
	}
	n := &TaskNode{
		TaskID:     taskID,
		ToolCallID: str(m["tool_call_id"]),
		ParentID:   str(m["parent_node_id"]),
		TopSession: str(m["top_session"]),
		Kind:       str(m["kind"]),
		SessionID:  str(m["session_id"]),
		InstanceID: instanceID,
		Name:       title,
		Simplified: title,
		State:      TaskStateInterrupted,
	}
	if k := n.Kind; k == "" {
		n.Kind = TaskKindTool
	}
	return n
}

// recoverTurnCtx 重建运行中轮次（进程重启/死 turn 恢复专用）：hist 仅含**系统提示词层**
// （与新建轮同一构造，见 resolveTurnScenario）——msgs() 再从库加载当前轮（= 被重试的 turn）
// 全部消息，后续 FeedToolResult 的 tool 结果与该轮 assistant.tool_calls 成对进入下一条 LLM
// 消息；终态 llm-complete 沿用原 {session, turn}。该轮标记 forceFull=true：即使被重试 turn
// 落在"非维持轮"范围也必须全量拼接。
func (s *Server) recoverTurnCtx(instanceID, session, turn string) *turnCtx {
	// ctx 绑定 instance_id（同 newTurnCtx）：恢复后的 gateway 方法调用同样携带实例归属。
	ctx, cancel := context.WithCancel(withInstance(context.Background(), instanceID))
	// WorkDir/DataDir 随 instance-id 绑定（同 onLLMStart）：恢复轮的 gateway 上下文与
	// **记忆 / 资产指引**（msgs() 的 memoryGuide/assetGuide 依赖 tc.req.WorkDir）依赖它
	// ——此前为空 → 指引缺失。
	req := StartReq{InstanceID: instanceID, Session: session, Turn: turn}
	if rec, ok := s.im.Lookup(instanceID); ok {
		req.WorkDir = rec.WorkDir
		req.DataDir = rec.DataDir
	}
	// 系统提示词三层（**与新建轮同一构造**，见 resolveTurnScenario）：恢复轮无 scenario/agent
	// （未持久化）→ desc/agents 为空 → 仅注入全局层 + 目录层（同「通用模式」）。
	sc := s.resolveTurnScenario(instanceID, "", "")
	var hist []ChatMsg
	if sc.sysPrompt != "" {
		hist = []ChatMsg{{Role: "system", Content: sc.sysPrompt}}
	}
	// LLM 配置：恢复轮无 provider name（llm-start.llm 未落库）→ temperature/maxOutputToken 无来源
	// （不发送）；base/apiKey/model 走 turnCtx 默认（回落 exe flags / 客户端默认模型）。
	// 工具循环上限回落缺省 20，否则 0 会让首轮 chatOnce 立即命中 TOOL_LOOP_LIMIT。
	llmMaxIter := defaultMaxToolIterations
	// 超时/重试同源加载（T-27 接线；缺省已回落旧硬编码常量）。
	respTO, streamTO, retryCnt := s.loadLLMRuntimeConfig(instanceID)
	tc := &turnCtx{
		server:    s,
		req:       req,
		ctx:       ctx,
		cancel:    cancel,
		in:        make(chan ChatMsg, 64),
		done:      make(chan struct{}),
		hist:      hist,
		pending:   make(map[string]pendingTool),
		asyncDone: make(chan asyncDoneMsg, 8),

		llmSpec:           s.exeDefaultSpec(), // 无 provider name → exe flags 隐含默认；request model 走 spec.DefaultModel
		responseTimeout:   respTO,
		streamTimeout:     streamTO,
		retryCount:        retryCnt,
		maxToolIterations: llmMaxIter,
		keepFullTurns:     s.loadKeepFullTurns(instanceID),
		keepFullTokens:    s.loadKeepFullTokens(instanceID),
		briefBudget:       s.loadBriefBudget(instanceID),
		// 重试/恢复的 turn 强制全量拼接（即使落在"非完整区"范围）。
		forceFull: true,
	}
	go tc.loop()
	s.mu.Lock()
	// 登记键 = instKey(instance, id)（缺口 5：按 instance 分桶）。
	s.busy[instKey(instanceID, session)] = turn
	s.turns[instKey(instanceID, turn)] = tc
	s.mu.Unlock()
	return tc
}

// lastToolCallPair 从轮次消息取最后一条 assistant 的 tool_call（配对重跑）：
// 返回 (工具名, 参数 map, tool-call-id, ok)。toolCallID 给定 → 精确匹配。
func lastToolCallPair(msgs []ChatMsg, toolCallID string) (string, map[string]any, string, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		for j := len(msgs[i].ToolCalls) - 1; j >= 0; j-- {
			tc := msgs[i].ToolCalls[j]
			if toolCallID != "" && tc.ID != toolCallID {
				continue
			}
			return tc.Function.Name, parseArgs(tc.Function.Arguments), tc.ID, true
		}
	}
	return "", nil, "", false
}

// onGatewayTaskDone gateway 异步任务完成回报（mcp-tasks-report，B1 独立主题）：
// ① 任务节点状态同步：命中未终态节点 → 广播 tasks.done（已终态如 cancelled 不重复广播）；
// ② 命中等待该 task 的 turn → 回填续轮（结果被本轮取走）；
// ③ 未命中等待 turn（调用方已离开）→ 广播 tool-notify（notice=completion）→ 前端追加 🔔 通知（I-34）。
func (s *Server) onGatewayTaskDone(_ string, payload []byte) {
	var ev struct {
		TaskID        string `json:"task_id"`
		ToolCallID    string `json:"tool_call_id"`
		Tool          string `json:"tool"`
		State         string `json:"state"`
		ResultSummary string `json:"result_summary"`
		Session       string `json:"session"`
		Turn          string `json:"turn"`
		InstanceID    string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &ev)
	// ⓪ I-83 收尾（18 §3.4）：取消回报 → 按工具执行取消表**真停执行体**（task 型域工具
	// tool_result 轮询等；gateway 侧 Emit 同步派发不中断 handler，故经回报通道反向通知）。
	// 未命中 = 执行体已结束 / 未登记（无 tool_call_id 或非 task 型）→ 无操作（幂等）。
	if ev.State == "cancelled" {
		s.cancelTaskExec(ev.ToolCallID)
	}
	node := s.tasks.findByGwTask(ev.InstanceID, ev.TaskID)
	if node == nil && ev.ToolCallID != "" {
		// 非 detached 任务未登记 gwTaskID → 按 tool_call_id 兜底定位（I-62：超时裁决取消回报）
		node = s.tasks.findByToolCall(ev.InstanceID, ev.ToolCallID)
	}
	// ① 任务节点终态同步（findByGwTask / findByToolCall 定位 server 编排节点；done 幂等：
	// 已终态不重复广播、不覆盖）
	if node != nil && !terminalTaskState(node.State) {
		switch ev.State {
		case "done":
			s.tasks.done(node.TaskID, TaskStateDone, ev.ResultSummary, "")
		case "cancelled":
			s.tasks.done(node.TaskID, TaskStateCancelled, "", ev.ResultSummary)
		default: // error
			s.tasks.done(node.TaskID, TaskStateError, "", ev.ResultSummary)
		}
	}
	// ② 命中等待 turn → 续轮（缺口 5：按回报载荷的 instance 归属限定，不跨 instance 命中）
	s.mu.Lock()
	var tc *turnCtx
	for _, t := range s.turns {
		if ev.InstanceID != "" && t.req.InstanceID != ev.InstanceID {
			continue
		}
		t.mu.Lock()
		_, ok := t.pending[ev.TaskID]
		t.mu.Unlock()
		if ok {
			tc = t
			break
		}
	}
	s.mu.Unlock()
	if tc != nil {
		select {
		case tc.asyncDone <- asyncDoneMsg{taskID: ev.TaskID, state: ev.State, result: ev.ResultSummary}:
		case <-tc.done:
		}
		return
	}
	// ③ 调用方已离开 → 完成通知（任务节点已由 ① 更新广播）
	s.notifyTaskCompletion(node, ev.Session, ev.Turn, ev.InstanceID, ev.Tool, ev.TaskID, ev.State)
}

// notifyTaskCompletion 调用方已离开（无等待 turn）的异步任务完成通知：广播 tool-notify
// （notice=completion）→ 前端 MessageList.onCompletionNotice 追加 🔔 user-notify 气泡
// （结果尚未被本轮取走，可再经 tool_result 获取）。用户主动取消（cancelled）不通知。
// instance_id/session_id 由任务节点（session → server 编排映射，TaskNode.InstanceID/SessionID）
// 取，缺省回落 gateway 完成回报载荷；实例字段必带（61-消息一览 §0.1），缺失不广播。
func (s *Server) notifyTaskCompletion(node *TaskNode, session, turn, instanceID, tool, taskID, state string) {
	if node != nil {
		if session == "" {
			session = node.SessionID
		}
		if turn == "" {
			turn = node.TurnID
		}
		if instanceID == "" {
			instanceID = node.InstanceID
		}
		if node.TaskID != "" {
			taskID = node.TaskID // 通知/去重以 server 编排节点 id 为准（tool_result 可直接解析）
		}
		if node.Name != "" {
			tool = node.Name
		} else if tool == "" {
			tool = node.Tool
		}
		if node.State == TaskStateCancelled {
			state = "cancelled"
		}
	}
	if state == "cancelled" || instanceID == "" || taskID == "" {
		return
	}
	if tool == "" {
		tool = "任务"
	}
	msg := "🔔 异步任务已完成：" + tool + "（task_id=" + taskID + "）。该任务结果尚未获取，可调用 tool_result(id=\"" + taskID + "\") 获取完整结果。"
	if state != "done" {
		msg = "🔔 异步任务执行失败：" + tool + "（task_id=" + taskID + "）。"
	}
	s.publish(msgkeys.TopicToolNotify, map[string]any{
		"instance_id": instanceID,
		"session_id":  session,
		"turn_id":     turn,
		"task_id":     taskID,
		"notice":      "completion",
		"message":     msg,
		"message_id":  "msg-notify-" + taskID,
	})
}

// notify 发布 llm-receive（与 llm-send 对应：LLM 返回内容广播给监听者）。
func (s *Server) notify(tc *turnCtx, typ string, payload map[string]any) {
	ev := map[string]any{
		"type": typ, "payload": payload,
		"instance_id": tc.req.InstanceID, "session": tc.req.Session, "turn": tc.req.Turn,
	}
	s.publish("llm-receive", ev)
}

// visibleTools 组装某 instance **可见**的全部工具定义（**不过滤 hot**）：
// 工具面取自 gwClient **按 instance 分桶**的可见集（RB-5 L2：global ∪ 归属当前 instance；
// 与改前「全量 tools/list 后每次按 scope 过滤」逐项同序）。
// 工具契约描述里的 {{toolchain.<key>}} 占位符在此替换为用户配置的工具链路径
// （仅当确有占位符才读 usr 配置；替换只改本次下发的副本，不动 gateway 缓存）。
//
// 供两处消费：① `toolsForLLM`（缺省下发面 = hot 子集）；② `llmTools` 的白名单分支
// （25 §4.3：白名单非空 → 白名单里的工具**不论是否 hot**都下发，见 T1 缺陷修正）。
func (s *Server) visibleTools(instance string) []ToolDef {
	visible := s.gc.ToolsFor(instance)
	out := make([]ToolDef, 0, len(visible))
	var vars map[string]string // 懒加载（无占位符则不发 data-user-config-load）
	for _, t := range visible {
		if strings.Contains(t.Description, mcpms.ToolchainPlaceholder) {
			if vars == nil {
				vars = s.toolchainVars(instance)
			}
			t.Description = mcpms.ReplaceToolchain(t.Description, vars)
		}
		out = append(out, t)
	}
	return out
}

// toolsForLLM 组装发给 LLM 的**缺省**工具列表（21-llm-server · 25 §4.1）：
// = visibleTools 筛 `hot==true`（hot 工具 ∪ gateway in-memory meta 工具；实例级 capability
// dir 节点工具不污染其他 instance 的 LLM 工具面）。白名单非空时改走 `llmTools` 的白名单分支。
func (s *Server) toolsForLLM(instance string) []ToolDef {
	visible := s.visibleTools(instance)
	out := make([]ToolDef, 0, len(visible))
	for _, t := range visible {
		if !t.Hot {
			continue
		}
		out = append(out, t)
	}
	return out
}

// finish 发送唯一终态（对齐 21-llm-server）：
// llm-complete（唯一终态）落库 → 写快照（hist + 本轮消息，经 persist data-snapshot-set）
// → 发 llm-complete → 发 llm-compress。
// retryable 为**可选**字段（S21）：非 nil → 随 llm-complete 携带真实可重试分类；nil → 不带该字段。
func (s *Server) finish(tc *turnCtx, status, finishReason, code, message, text string, retryable *bool) {
	// 原子终态（E2-2）：正常完成路径与 onLLMCancel（取消）可能并发到达同一轮次——finishOnce
	// 保证终态（落库 CompleteTurnTokens + 快照 + llm-complete/llm-compress 广播）**仅发生一次**；
	// 其后到达者（如取消命中已终态轮次）静默跳过，不二次写库、不二次广播（覆盖已 complete 的 turn 行）。
	emitted := false
	tc.finishOnce.Do(func() { emitted = true })
	if !emitted {
		return
	}
	// 终态结果记录（递归子轮次父侧在 <-tc.done 后读取；先于 Close 写入可见）
	tc.result = text
	if status == "error" {
		msg := message
		if msg == "" {
			msg = code
		}
		tc.turnErr = errors.New(msg)
	}
	st := newSessionStore(s.bus, tc.req.InstanceID)
	// 本轮全部消息（最后一段；快照拼接与预存 token 共用一份，避免重复读库）。
	cur := st.LoadMessages(tc.req.Turn)
	// P3：轮次结束时计算并**预存**本轮完整态/简化态 token 数（判定/拼接后续直接累加，免重复估算）。
	fullTok, briefTok := turnTokenCounts(cur)
	_ = st.CompleteTurnTokens(tc.req.Turn, status, finishReason, &fullTok, &briefTok)
	// 写快照（唯一终态写）：snapshot_turn = 本轮（快照覆盖到最后一条 turn）
	msgs := append(append([]ChatMsg{}, tc.hist...), cur...)
	_ = st.SetSnapshot(tc.req.Session, msgs, tc.req.Turn)
	ev := map[string]any{
		"instance_id": tc.req.InstanceID, "session": tc.req.Session, "turn": tc.req.Turn,
		"status": status, "finish_reason": finishReason,
		// parents：主轮次空、子轮次（递归）为父链——history 插件据此只提交主 session 快照
		"parents": tc.req.Parents,
	}
	if code != "" {
		ev["code"] = code
	}
	if message != "" {
		ev["message"] = message
	}
	if text != "" {
		ev["text"] = text
	}
	// retryable（S21 定稿新增**可选**字段）：错误终态携带该 turn 的真实可重试分类
	//（= *LLMError.Retryable：超时/网络/429/5xx → true；协议/鉴权等 → false），
	// 前端据此决定"静默自动续写"还是"错误气泡 + 手动继续"；正常完成/取消不带
	//（旧订阅方按缺省处理，向后兼容；语义见 61-消息一览 §4.3 session-complete）。
	if retryable != nil {
		ev["retryable"] = *retryable
	}
	s.publish("llm-complete", ev)
	// 压缩扩展点事件：唯一终态落库后发出（处置由 chonkpilot-plugin-compress；无订阅者 = 不压缩）
	compressEv := map[string]any{
		"instance_id": tc.req.InstanceID, "work_dir": tc.req.WorkDir, "data_dir": tc.req.DataDir,
		"session": tc.req.Session, "last_turn": tc.req.Turn, "snapshot_turn": tc.req.Turn,
	}
	// P1（2026-09-25，口径 Z4）：对外载荷字段统一 snake_case —— **只增**可选字段 `max_context_token` /
	// `max_output_token`——兜底归并的**真窗口**来源与**输出预留**（各自 = provider `maxContextToken` /
	// `maxOutputToken`，见 21 §3.3 / 28 §3.1）；未配置（nil）→ 不带该键 → 插件按"兜底不启用"处理
	// （与旧载荷逐字节等价）。插件侧对 camelCase `maxContextToken` / 更早 `window` **只读兼容**。
	// （替代上批以 provider `maxTokens`（输出上限）当窗口代理 + 常量 4096 预留的误用。）
	if tc.llmMaxContextToken != nil {
		compressEv["max_context_token"] = *tc.llmMaxContextToken
	}
	if tc.llmMaxOutputToken != nil {
		compressEv["max_output_token"] = *tc.llmMaxOutputToken
	}
	s.publish("llm-compress", compressEv)
}

// complete 正常/取消终态（无错误码；不带 retryable——正常完成/取消无重试语义）。
func (s *Server) complete(tc *turnCtx, status, finishReason, text string) {
	s.finish(tc, status, finishReason, "", "", text, nil)
}

// retryableField 构造 llm-complete 的可选 retryable 指针（nil = 不带该字段）。
func retryableField(v bool) *bool { return &v }

// llmError 错误终态（不可重试语义）：收敛到 llm-complete{status:error, code, message,
// retryable:false}（llm-error 已移除）。用于无 *LLMError 分类来源的错误
// （EMPTY_REPLY / TOOL_LOOP_LIMIT / DB_ERROR）——空回复应"提示 + 手动继续"，不静默续写。
func (s *Server) llmError(tc *turnCtx, code, message string) {
	s.finish(tc, "error", "", code, message, "", retryableField(false))
}

// llmErrorRetryable 错误终态（带真实分类，S21）：retryable = *LLMError.Retryable
// （超时/网络/429/5xx → true；协议/鉴权 → false），前端据此决定是否自动续写。
func (s *Server) llmErrorRetryable(tc *turnCtx, code, message string, retryable bool) {
	s.finish(tc, "error", "", code, message, "", retryableField(retryable))
}

func (s *Server) publish(typ string, payload any) {
	b, _ := json.Marshal(payload)
	s.bus.Emit(context.Background(), domainSubject(typ), b)
}

// emitTurnStart 广播轮次开始：session-turn-start{instance_id, session, turn, parents}（相对主题）。
// 主轮次（llm-start 受理）与子轮次（llm_run 的 LLM 委派递归，runChildTurn）都广播；
// parents：主轮次空、子轮次为父会话链。前端据此维护会话树（parents 定位插入子节点）；
// history 插件据此（parents 空 = 主 session）触发 git 快照提交。
// 注：不能用 session-start（方法面 llm-start）广播——它由域方法面订阅（Start → onLLMStart）
// 受理，广播同主题会被自身方法路由误受理；turn-start 是独立轮次事件。
func (s *Server) emitTurnStart(req StartReq) {
	s.publish("turn-start", map[string]any{
		"instance_id": req.InstanceID,
		"session":     req.Session,
		"turn":        req.Turn,
		"parents":     req.Parents,
	})
}

// llmProtocolOpenAI / llmProtocolResponses 是已知的协议取值（P1-2）：openai = OpenAI 兼容
// Chat Completions（/chat/completions，默认）；responses = OpenAI Responses API（/responses）。
// protocol 字段空/缺省 → openai；未知值 → warn 并按 openai 处理（不改变请求行为、不中断）。
const (
	llmProtocolOpenAI    = ProtocolOpenAI
	llmProtocolResponses = ProtocolResponses
)

// llmProtocolKnown 判断 protocol 是否为已知协议（未知值 → loadLLMProvider warn 并按 openai 处理）。
func llmProtocolKnown(p string) bool {
	return p == llmProtocolOpenAI || p == llmProtocolResponses || p == ProtocolEcho
}

// builtinFallbackName 是**内置兜底** provider 的保留名（D-30）：协议 `echo` —— **不发任何 HTTP**，
// 回显本轮最后一条真实用户消息（`llm.go` 的 `chatEcho`，无 baseUrl/apiKey、不注册/不下发任何工具）。
//
// **不再作为「可配置 provider」暴露**（2026-09-22 用户拍板 D-30）：不进 usr `llms` 表、
// 不出现在配置面（LLM 设置页与聊天选择器均不列出）、不参与「设为默认」。
// 仅当 `llm-start.llm` 命中本保留名时给出只读配置（兼容既有 usr `defaultLLM: "echo"` 记录），
// 即**兜底语义保留、配置面收窄**。
//
// **「无任何可用 provider」的自动兜底归 router**（`src/lib/router` 的 `builtinFallback`：
// `Call` 的 `Provider` 为空或等于保留名 → 回落内置 echo）；llm 侧接线归 LR-11（见 40 §LR），
// 届时本保留名解析随旧 `LLMClient` 路径一并迁出。
const builtinFallbackName = "echo"

// builtinFallbackProvider 返回内置兜底的只读 provider 配置（protocol = echo，代码写死）：仅保留名
// 命中；其余 → nil（调用方回落 exe flags）。每次返回新副本，调用方之间零共享。
func builtinFallbackProvider(name string) *llmProviderCfg {
	if name != builtinFallbackName {
		return nil
	}
	return &llmProviderCfg{Name: builtinFallbackName, Protocol: ProtocolEcho, Model: builtinFallbackName}
}

// llmProviderCfg 是命中的 LLM provider 记录（llm-start.llm = provider name，usr 配置 llms 数组）。
// P1-1：让配置真正生效——baseUrl/apiKey/model 供客户端与请求体；temperature/maxOutputToken
// 按**存在性**判定（允许 0 生效；缺键 = 不发送）；thinking/reasoningEffort 决定是否发送
// reasoning_effort；maxToolIterations 单轮工具循环上限（0 = 未配置 → 调用方回落缺省 20）。
// P1-2：Protocol 决定请求协议（openai/responses；空/未知 → openai）。
// 口径 Z1（2026-09-25）：`maxOutputToken` = 最大输出 token（由旧 `maxTokens` 改名）；`maxContextToken` = 上下文窗口。
type llmProviderCfg struct {
	Name              string
	Protocol          string   // 协议类型（归一后：openai / responses）
	BaseURL           string   // 空 → 回落 exe flag -llm-base
	APIKey            string   // 空 → 不发送 Authorization
	Model             string   // 空 → 回落 exe flag -llm-model
	Temperature       *float64 // 存在即生效（含 0）；缺键 → 不发送
	MaxOutputToken    *int     // 最大输出 token（请求体 max_tokens；键 maxOutputToken，读时兼容旧名 maxTokens / max_tokens，口径 Z1）
	MaxContextToken   *int     // 上下文窗口大小（兜底归并窗口来源；键 maxContextToken；未配置 → 兜底不启用，口径 Z3）
	Thinking          *bool    // thinking=false → 不发送 reasoning_effort；缺键 → 由请求体决定
	ReasoningEffort   string   // thinking=true 时的 reasoning_effort（缺省 high）
	MaxToolIterations int      // >0 生效；否则回落缺省
}

// loadLLMProvider 从 user-config 加载 llm-start.llm（provider name）对应的 LLM 配置。
// 命中 usr llms → 记录配置；未命中 → **内置兜底保留名**（D-30，现仅 echo）；
// 两者均未命中 / 空 name → nil（调用方回落 exe flag 现状）。空 name → **不读配置**（零额外开销）。
func (s *Server) loadLLMProvider(instanceID string, selectedModel string) *llmProviderCfg {
	if selectedModel == "" {
		return nil
	}
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] loadLLMProvider: UserConfigGet failed: %v\n", err)
		return providerFromConfig(nil, selectedModel)
	}
	return providerFromConfig(res.Config, selectedModel)
}

// providerFromConfig 从**已读** usr 配置对象折算 provider name 对应的 LLM 配置（纯函数，不再读配置）：
// 空 name → nil（回落 exe flags）；命中 usr llms → 记录配置；未命中 → **内置兜底保留名**（D-30，仅 echo）；
// 其余（近似名 / 已失效名 / 未配置）→ nil（保持既有「未命中即回落 exe flags」语义不变）。
// 供 loadLLMProvider（llm-start / llm-simple）与子系统级入口 promptOptimiseSpec 共用同一折算口径。
func providerFromConfig(cfg map[string]any, selectedModel string) *llmProviderCfg {
	if selectedModel == "" {
		return nil
	}
	if p := providerFromLLMs(cfg, selectedModel); p != nil {
		return p
	}
	return builtinFallbackProvider(selectedModel)
}

// providerFromLLMs 从已读 usr 配置对象取 provider name 对应的记录配置：命中返回配置；
// 未命中 / 无数据（cfg 为 nil）→ nil（由 providerFromConfig 决定后续（内置兜底保留名 / 回落 exe flag））。
func providerFromLLMs(cfg map[string]any, selectedModel string) *llmProviderCfg {
	for _, item := range asAnySlice(cfg["llms"]) {
		llm, _ := item.(map[string]any)
		if llm == nil {
			continue
		}
		name, _ := llm["name"].(string)
		if name != selectedModel {
			continue
		}
		// 协议类型（P1-2）：openai（默认，Chat Completions）/ responses（Responses API）。
		// 空/缺省 → openai（旧记录兼容）；未知值 → 明确 warn 并按 openai 兼容处理。
		protocol := strings.TrimSpace(str(llm["protocol"]))
		if protocol != "" && !llmProtocolKnown(protocol) {
			logf("[chonkpilot-server] loadLLMProvider: 未知 protocol=%q（llm=%q），按 openai 兼容处理\n", protocol, name)
		}
		cfg := &llmProviderCfg{
			Name:            name,
			Protocol:        NormalizeLLMProtocol(protocol),
			BaseURL:         strings.TrimSpace(str(llm["baseUrl"])),
			APIKey:          strings.TrimSpace(str(llm["apiKey"])),
			Model:           strings.TrimSpace(str(llm["model"])),
			ReasoningEffort: strings.TrimSpace(str(llm["reasoningEffort"])),
		}
		// 存在性判定（允许 0 生效）：键缺失才回落「不发送」。
		if t, ok := configFloat(llm, "temperature"); ok {
			cfg.Temperature = &t
		}
		// 最大输出 token（口径 Z1 改名）：新名 `maxOutputToken` 优先；读时兼容旧名
		// `maxTokens` / `max_tokens`（改名前的历史配置）→ 只读不写，不静默丢弃既有配置。
		if mt, ok := configFloat(llm, "maxOutputToken"); ok {
			n := int(mt)
			cfg.MaxOutputToken = &n
		} else if mt, ok := configFloat(llm, "maxTokens"); ok { // 读时兼容（改名前的历史键）
			n := int(mt)
			cfg.MaxOutputToken = &n
		} else if mt, ok := configFloat(llm, "max_tokens"); ok { // 读时兼容（更早的旧键名）
			n := int(mt)
			cfg.MaxOutputToken = &n
		}
		// 上下文窗口大小（口径 Z1 新增）：兜底归并窗口来源；未配置 → 兜底不启用。
		if mc, ok := configFloat(llm, "maxContextToken"); ok {
			n := int(mc)
			cfg.MaxContextToken = &n
		}
		if it, ok := configFloat(llm, "maxToolIterations"); ok && it > 0 {
			cfg.MaxToolIterations = int(it)
		}
		if b, ok := llm["thinking"].(bool); ok {
			v := b
			cfg.Thinking = &v
		}
		return cfg
	}
	return nil
}

// configFloat 从 map 取数值（总线 JSON 数字 → float64；兼容直接传入的 int）。
// 返回值 ok 表示键**存在**（0 亦存在 → 允许 0 生效）。
func configFloat(d map[string]any, key string) (float64, bool) {
	switch v := d[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// loadLLMRuntimeConfig 读 usr 配置的超时/重试三项（T-27 接线，2026-09-11 审计 B）：
// data-user-config-load 标量项（responseTimeout/streamTimeout 单位秒，retryCount 次数）。
// 缺省语义：
//   - retryCount 按**存在性**判定（P0-A，与 temperature/maxOutputToken 同范式）：显式 0 = **不重试**
//     （与前端 min:0 / i18n「0 = 不重试」一致）；键缺失 → 回落 llmRetryCount=2。
//     persist readUserConfig 对缺失键补系统默认（=2）后回读，故读到 0 只可能是用户显式存过 0。
//   - 其余两项（responseTimeout/streamTimeout）保持既有口径：读取失败 / 键缺失 / 非正值 →
//     回落常量（ResponseTimeout=120s / StreamTimeout=60s），避免行为回归。
//
// 退避（重发间隔）**不在此配置**：经 `router.RetryWait` 计算（2026-10-05 定案，`retryDelay` 已移除）。
func (s *Server) loadLLMRuntimeConfig(instanceID string) (responseTimeout, streamTimeout time.Duration, retryCount int) {
	responseTimeout, streamTimeout = ResponseTimeout, StreamTimeout
	retryCount = llmRetryCount
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] loadLLMRuntimeConfig: UserConfigGet failed: %v\n", err)
		return
	}
	d := res.Config
	if d == nil {
		return
	}
	if n, ok := configInt(d, "responseTimeout"); ok && n > 0 {
		responseTimeout = time.Duration(n) * time.Second
	}
	if n, ok := configInt(d, "streamTimeout"); ok && n > 0 {
		streamTimeout = time.Duration(n) * time.Second
	}
	// P0-A：存在性判定——显式 0 = 不重试（n >= 0 同时挡掉负数脏值）。
	if n, ok := configInt(d, "retryCount"); ok && n >= 0 {
		retryCount = n
	}
	return
}

// configInt 从 user-config 标量 map 取 int（总线 JSON 数字 → float64；兼容直接传 int 的单测）。
func configInt(d map[string]any, key string) (int, bool) {
	switch v := d[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// loadExecConfig 把本实例的 usr/prj 配置注入内嵌 mcp-server 执行配置（见 36-配置 §3.1）：
//   - usr 标量（pythonPath/nodePath/javaPath/chromePath/goPath/rustPath/cCompilerPath）
//     经 data-user-config-load 读 {data:{...}}
//   - prj 键（timeout_sec/max_concurrency/skip_dirs）经 data-prj-config-load 读 {data:<值字符串>}
//
// 说明（R-11 二次升级）：实例上下文（instance/workDir/dataDir）不再经 Config.SetContext 注入
// tool arguments——改由每次调用经协议 _meta 从调用链透传（gateway → mcp-server → executor env）。
//
// 合并规则：prj 覆盖 usr（本处两组键不重叠，逐项按非空/正值生效）；prj 未配置的项取
// mcpms 内置默认（见函数内 def）。
// 解释器映射：python→pythonPath、js→nodePath、java→javaPath（shell/cmd/bash/powershell/vbs
// 走执行器内置默认，不注入）；均仅路径非空才放入。
// 另：usr 工具链路径同时注入 Config.SetToolchain（capability prompt/skill 文本的
// {{toolchain.<key>}} 占位符取值，见 toolchain.go）。
// 另：usr 键 `tool_async`（结构化 JSON 对象字符串）注入 Config.SetToolAsync（工具级异步配置
// 覆盖表；有变化时触发 reloadAppContracts → 暴露 _meta 热更新，见 applyToolAsync）。
// 调用时机：instance-register（注册即注入）与 data-prj-config-refresh（prj 执行配置保存/删除
// 即重跑 → 保存即生效，P0-B；见 onPrjConfigRefresh）；usr `tool_async` 的保存/删除另走
// data-user-config-refresh（见 onUserConfigRefresh / reloadToolAsyncOverrides）。
func (s *Server) loadExecConfig(instanceID string) {
	if s.mcpCfg == nil {
		return
	}
	// G-27（2026-09-22）：本函数是「读配置 → 写运行期覆盖」的**读改写序列**，会被两路并发调用
	// （`instance-register` 的订阅回调 与 prj 配置刷新的后台 goroutine，见 onPrjConfigRefresh）；
	// 交错执行即 data race（`go test -race` 实测）。用**专用互斥**串行化整段（不改锁内语义、
	// 不触碰 `s.mu`；本函数不重入 → 无自锁）。
	s.execCfgMu.Lock()
	defer s.execCfgMu.Unlock()
	// 门面读取**去重**（2026-09-22，启动首屏优化）：本函数原为 7 次门面往返
	// （usr ×2 + prj 平铺 ×1 + prj 单键 ×3 + prj-security ×1），其中 usr 被读两遍、
	// 3 个 prj 执行键各发一次 ConfigKVGet。改为 4 次：① usr（readUserConfig）
	// ② prj 平铺（readPrjConfigList，供分层路径键）③ prj 执行键**批量一次**
	// （prjConfigValues，与逐键 ConfigKVGet 同读序/同域）④ prj-security（securityRules）。
	// 取值语义逐项不变（同门面、同读序、同键）。
	usrCfg, usrErr := s.readUserConfig(instanceID)
	prjList, prjErr := s.readPrjConfigList(instanceID)
	execVals := s.prjConfigValues(instanceID, "timeout_sec", "max_concurrency", "skip_dirs")

	var toolAsyncRaw, toolSandboxRaw string
	loaded := false
	if usrErr == nil {
		if usrCfg != nil {
			loaded = true
			toolAsyncRaw, _ = usrCfg[toolAsyncUserConfigKey].(string)
			toolSandboxRaw, _ = usrCfg[toolSandboxUserConfigKey].(string)
		}
	} else {
		logf("[chonkpilot-server] loadExecConfig: UserConfigGet failed: %v\n", usrErr)
	}
	// 路径键分层（prj > usr > 默认，2026-09-19 接线）：统一入口 layeredPathValues；解释器 env 注入
	// 与 {{toolchain.<key>}} 取值走同一结果，避免两处各读一份。
	paths := layeredPathValues(usrCfg, prjList)
	pathsOK := usrErr == nil || prjErr == nil
	pythonPath, nodePath, javaPath, chromePath := paths["pythonPath"], paths["nodePath"], paths["javaPath"], paths["chromePath"]
	toolchain := make(map[string]string, len(mcpms.ToolchainKeys))
	for _, k := range mcpms.ToolchainKeys {
		toolchain[k] = paths[toolchainUserConfigKeys[k]]
	}
	if pathsOK {
		// 读成功才应用（读失败保留现值，避免把已有取值误清空——与下方 loaded 同口径）。
		s.mcpCfg.SetToolchain(toolchain)
	}
	if loaded {
		// 读成功才应用（读失败保留现值，避免把已有覆盖误清空）。
		s.applyToolAsync(toolAsyncRaw)
		s.applyToolSandbox(toolSandboxRaw)
	}
	interpreters := map[string]string{}
	if pythonPath != "" {
		interpreters["python"] = pythonPath
	}
	if nodePath != "" {
		interpreters["js"] = nodePath
	}
	if javaPath != "" {
		interpreters["java"] = javaPath
	}
	// 基线 = 内嵌 mcp-server 内置默认（mcpms.DefaultConfig：300s / 16 / 12 个跳过目录）：
	// prj 未配置 → 显式用默认（与 mcp-tools 内置 SkipDirs 同集，行为等价）；
	// 「重置」（delete 键）后热生效重跑也回到默认，不残留旧覆盖（P0-B/C）。
	def := mcpms.DefaultConfig()
	timeoutSec := def.TimeoutSec
	if v := execVals["timeout_sec"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeoutSec = n
		}
	}
	maxConcurrency := def.MaxConcurrency
	if v := execVals["max_concurrency"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrency = n
		}
	}
	skipDirs := def.Defaults.SkipDirs
	if v := execVals["skip_dirs"]; v != "" {
		var arr []string
		if json.Unmarshal([]byte(v), &arr) == nil && len(arr) > 0 {
			skipDirs = arr // 空数组 = 未覆盖 → 保留内置默认（前端「重置」后同口径）
		}
	}
	s.mcpCfg.SetRuntime(timeoutSec, maxConcurrency, interpreters, chromePath, skipDirs)
	// agentbox 允许目录（prj `security-*`：可读 / 可写目录，递归）→ 内嵌 mcp-server Config
	// （**仅**在 usr `tool_sandbox` 把某工具开关打开时，才随 spawn executor 下发）。
	// 读失败 → 保留现值（同上方 loaded 口径，不误清空）。
	if rules, ok := s.securityRules(instanceID); ok {
		s.mcpCfg.SetSecurityDirs(rules)
	}
}

// prjExecConfigKeys 是运行期热生效的执行配置 prj 键（loadExecConfig 读取）：
// timeout_sec → tools/call 执行超时；max_concurrency → 并发限流上限；skip_dirs → 递归跳过目录集；
// 另含**路径键**（2026-09-19 接线 prj 覆盖 usr，prj > usr > 默认）：保存 prj 路径即重跑
// loadExecConfig → 解释器 env / {{toolchain.*}} 立刻取 prj 值（无需重开项目）。
var prjExecConfigKeys = map[string]bool{
	"timeout_sec":     true,
	"max_concurrency": true,
	"skip_dirs":       true,
	"javaPath":        true,
	"pythonPath":      true,
	"nodePath":        true,
	"goPath":          true,
	"rustPath":        true,
	"cCompilerPath":   true,
	"chromePath":      true,
}

// toolAsyncUserConfigKey 是**工具级异步配置**的 usr 键（64-配置项一览 §3）：值 = 结构化 JSON
// 对象字符串（persist 自由键通道，与 recent_dirs 同形态），形状 =
// `{"<工具暴露名>": {"mode": "always|never|auto|manual", "threshold": 30, "hard_timeout": 300,
// "cancel_on_timeout": 30, "touch_files": true}}`。
// key = tools/list 暴露名（内嵌 self 节点 = self_<契约名>；第三方 = <节点名>_<原名>，天然消歧）；
// threshold（秒，可选，auto/manual 档的超时转后台点）、hard_timeout（秒，可选，executor 执行
// 硬杀上限）、cancel_on_timeout（秒，可选，>0 = 到超时点自动取消；gateway 消费）、
// touch_files（bool，可选，是否涉及文件变动）。未配置的工具 = 维持契约现值；「恢复默认」= 删该工具键项。
const toolAsyncUserConfigKey = "tool_async"

// applyToolAsync 解析 usr `tool_async` 值并写入内嵌 mcp-server 覆盖表（Config.SetToolAsync）。
// raw 为空/键缺失 → 清空全部覆盖（= 恢复契约默认）；非法 JSON → 忽略并记日志、保留现值。
// 覆盖表**有变化**时触发 reloadAppContracts → 契约重注册（同名工具 AddTool 覆盖更新）
// + 既有 gateway/reload 让 self 节点重拉 list → tools/list 的 _meta.async / async-threshold
// 秒级更新（无需重启；gateway 侧 doCall 读的是重注册后的 route.Tool.Meta）。
func (s *Server) applyToolAsync(raw string) {
	if s.mcpCfg == nil {
		return
	}
	var next map[string]mcpms.ToolAsyncOverride
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &next); err != nil {
			logf("[chonkpilot-server] %s 解析失败（忽略，保留现值）: %v\n", toolAsyncUserConfigKey, err)
			return
		}
	}
	// 工具级覆盖**下沉 gateway 统一施加**（I-82）：按暴露名覆盖全部工具类（含 dir 节点/第三方
	// 工具，mcp-server 侧只按契约名覆盖 self 节点）。归一结果由 mcp-server 单口径提供，避免两处各归一。
	// RB-2：gateway 侧覆盖类型归 gateway 所有 → 在此转换（仅取 gateway 面消费的 mode/threshold）。
	if s.gw != nil {
		s.gw.SetAsyncOverrides(toGatewayAsyncOverrides(mcpms.NormalizeToolAsync(next)))
	}
	if !s.mcpCfg.SetToolAsync(next) {
		return // 覆盖表未变化（普通 usr 配置写入的常态）→ 不重注册、零抖动
	}
	s.reloadAppContracts()
}

// toGatewayAsyncOverrides 把 mcp-server 归一后的工具级异步覆盖转为 gateway 自有类型（RB-2：
// gateway lib 不依赖 chonkpilot-mcp-server 包）。携带 gateway 面消费的 mode / threshold /
// touch_files / cancel_on_timeout（hard_timeout 属执行硬上限，仅 executor 消费、不透出 → 不携带）。
func toGatewayAsyncOverrides(in map[string]mcpms.ToolAsyncOverride) map[string]mcpgateway.ToolAsyncOverride {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]mcpgateway.ToolAsyncOverride, len(in))
	for k, v := range in {
		out[k] = mcpgateway.ToolAsyncOverride{
			Mode: v.Mode, Threshold: v.Threshold, ThresholdSet: v.ThresholdSet,
			TouchFiles: v.TouchFiles, TouchFilesSet: v.TouchFilesSet,
			CancelOnTimeout: v.CancelOnTimeout, CancelOnTimeoutSet: v.CancelOnTimeoutSet,
		}
	}
	return out
}

// toolSandboxUserConfigKey 是 **executor 级沙箱开关**的 usr 键（64-配置项一览 §3，agentbox 决策
// 42 §2 (109)；2026-09-26 由工具级改为 executor 级）：值 = 结构化 JSON 对象字符串
// `{"core": true|false, "desktop": true|false, "browser": true|false}`（persist 自由键通道，
// 形态同 tool_async）。key = executor 类别（= 契约 `_meta.category`，`src/mcp-tools` 的三个
// 执行器能力目录）；**未配置 / false = 不启用隔离（默认兼容）**。**旧形态（按工具暴露名）不再生效**。
// 消费链：loadExecConfig → gateway SetSandboxOverrides → mcp-server Config.SetToolSandbox →
// callTool 按 `td.Category` 查开关 → 开启时 spawn executor 注入 CHONKPILOT_SANDBOX
// （允许目录 = prj `security-*`）。
const toolSandboxUserConfigKey = "tool_sandbox"

// applyToolSandbox 解析 usr `tool_sandbox` 并写入内嵌 mcp-server Config（SetToolSandbox）。
// raw 为空/键缺失 → 清空全部开关（= 全部 executor 不隔离，保守默认）；非法 JSON → 忽略并记日志、
// 保留现值。开关表变化无需重注册工具（开关在每次 tools/call 的 spawn 时读取，秒级生效）。
func (s *Server) applyToolSandbox(raw string) {
	if s.mcpCfg == nil {
		return
	}
	var next map[string]bool
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &next); err != nil {
			logf("[chonkpilot-server] %s 解析失败（忽略，保留现值）: %v\n", toolSandboxUserConfigKey, err)
			return
		}
	}
	// 沙箱开关**下沉 gateway 统一写入**共享执行配置（I-82）：表 key = executor 类别（全局），
	// gateway 直接转发给 mcp-server Config.SetToolSandbox（仅本仓 spawn 的 builtin executor 消费；
	// 第三方 MCP 无本仓 executor，不施加）。
	// 无 gateway（独立/测试形态）→ 回落直写（保持既有行为）。
	if s.gw != nil {
		s.gw.SetSandboxOverrides(next)
		return
	}
	s.mcpCfg.SetToolSandbox(next)
}

// securityRules 读该实例的 prj `security-*`（信任目录）并换算为 agentbox 允许目录集
// （`{dir, writable}`，语义 = 可读 / 可写目录且**递归**）。
// 契约（14 §2.3）：一条目录 = 一个 persist key（不透明 id），value = JSON 字符串
// `{"dir","writable"}`；list 返回平铺 map（已剥 `security-` 前缀）。
// 返回 (规则集, 读取是否成功)：读取失败 → ok=false（调用方保留现值，不误清空）；
// 单条 value 解析失败 → 跳过该条并记日志（不影响其余条目）。
func (s *Server) securityRules(instanceID string) ([]agentbox.Rule, bool) {
	res, err := s.cfg.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjSecurity, InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] securityRules: ConfigKVList(prj-security) failed: %v\n", err)
		return nil, false
	}
	list := res.List
	if len(list) == 0 {
		return nil, true
	}
	// 保序（map 遍历无序 → 按 key 排序，保证同配置下策略 JSON 稳定，避免无谓 diff/respawn）
	keys := make([]string, 0, len(list))
	for k := range list {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rules := make([]agentbox.Rule, 0, len(keys))
	for _, k := range keys {
		raw := list[k]
		var r agentbox.Rule
		if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &r) != nil || strings.TrimSpace(r.Dir) == "" {
			logf("[chonkpilot-server] securityRules: 条目 %s 解析失败（跳过）: %q\n", k, raw)
			continue
		}
		rules = append(rules, r)
	}
	return rules, true
}

// gatewaySandboxRules 汇总**全部在线实例**的 security-* 允许目录（按目录+可写去重、保序），
// 作为 gateway 上游 server 的 agentbox 策略快照。上游 server 是 global 作用域（无实例归属），
// 单体典型形态只有一个实例；多实例并存时按并集放宽（保守：合并只增不减）。
func (s *Server) gatewaySandboxRules() []agentbox.Rule {
	seen := map[string]bool{}
	var out []agentbox.Rule
	for _, info := range s.im.List() {
		rules, ok := s.securityRules(info.ID)
		if !ok {
			continue
		}
		for _, r := range rules {
			key := r.Dir + "\x00" + strconv.FormatBool(r.Writable)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

// applyGatewaySandboxDirs 为**开启 sandbox** 的 usr mcps 条目补上允许目录快照；
// 未开启（nil/false）的条目保持 nil（与既有已下发集合逐字段相同 → 零 diff、不触发重连）。
func (s *Server) applyGatewaySandboxDirs(entries []mcpgateway.ServerEntry) {
	need := false
	for _, e := range entries {
		if e.Sandbox != nil && *e.Sandbox {
			need = true
			break
		}
	}
	if !need {
		return
	}
	rules := s.gatewaySandboxRules()
	for i := range entries {
		if entries[i].Sandbox != nil && *entries[i].Sandbox {
			entries[i].SandboxDirs = rules
		}
	}
}

// onPrjSecurityRefresh prj-security 域 save/delete 广播（data-prj-security-refresh，**既有主题**，
// 61-消息一览 §3 的 `data-<domain>-refresh` 家族）→ agentbox 允许目录**保存即生效**：
//   - instance 归属明确（security-* 恒带 instance_id）→ 对该实例重跑 loadExecConfig
//     （security-* → mcp-server Config.SetSecurityDirs）；
//   - 并触发 usr mcps 增量对账（上游 server 的 sandbox 目录快照变化 → 重注册下发，零新增主题）。
//
// 异步执行：广播在 persist save 的同步派发链路内（处置同 onPrjConfigRefresh）。
// 注：executor 侧的允许目录在**下次 tools/call spawn 时**生效（每次调用都会重读 Config），
// 无需重启或重开会话。
func (s *Server) onPrjSecurityRefresh(_ string, payload []byte) {
	if s.mcpCfg == nil {
		return
	}
	var ev struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.InstanceID == "" {
		return
	}
	go func() {
		s.loadExecConfig(ev.InstanceID)
		s.reconcileUserMCPs(ev.InstanceID)
		logf("[chonkpilot-server] prj-security 变更热生效（agentbox 允许目录）: instance=%s\n", ev.InstanceID)
	}()
}

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
// 同源范式见 chonkpilot-gui/loglevel.go 与 chonkpilot-plugin-codegraph）→ 执行配置**保存即生效**（P0-B）：
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
