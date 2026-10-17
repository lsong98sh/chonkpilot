package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	"github.com/chonkpilot/chonkpilot-router"
)

// StartReq 是 llm-start 载荷：session/turn 由客户端分配（必填）；work_dir/data_dir
// 随 instance-register 绑定（json:"-"，不随 llm-start 传，由 server 从 instance 记录填充）。
// Parents/Agent 仅 server 内部子轮次（llm_run 的 LLM 委派步骤递归）使用：Parents = 父会话链
// （前端据此在会话树定位插入）；Agent = 子会话 persona 前缀（该 agent 的定义
// prompt / tools / llmRef / delegateCond 在 newTurnCtx 按场景 agent 读取，AG-1）。
// ScenarioID = 本轮场景 id（场景**目录名**，即场景 key；""=无偏好/默认场景）：消费为
// 场景 system_prompt 注入 + agent 定义读取（loadScenario）。
type StartReq struct {
	ReqID      string   `json:"req_id"`
	InstanceID string   `json:"instance_id"`
	Session    string   `json:"session"`
	Turn       string   `json:"turn"`
	LLMModel   string   `json:"llm,omitempty"`
	ScenarioID string   `json:"scenario_id,omitempty"`
	Think      string   `json:"think,omitempty"`
	Effort     string   `json:"effort,omitempty"`
	Parents    []string `json:"parents,omitempty"`
	Agent      string   `json:"agent,omitempty"`
	// Continue 同轮次继续（llm-start 可选字段，缺省 false）：复用该 session 最后一轮 turn
	// （未带 turn 时后端按 session 取最近一轮），不新开轮次——本 turn 恒为末段（全量拼接），
	// 注入的 user 消息以 Kind=continue 落库（非新轮边界）。
	Continue bool   `json:"continue,omitempty"`
	WorkDir  string `json:"-"`
	DataDir  string `json:"-"`
}

// SendReq 是 llm-send 载荷（进入 LLM 的统一消息）。
type SendReq struct {
	ReqID      string `json:"req_id"`
	InstanceID string `json:"instance_id"`
	Session    string `json:"session"`
	Turn       string `json:"turn"`
	Type       string `json:"type"` // text-user / tool-result / text-notify
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"` // type=tool-result 时关联的 LLM tool-call id
}

// defaultMaxToolIterations 单轮工具循环上限的缺省值（provider 未显式配置时生效）。
const defaultMaxToolIterations = 20

// 异步看门狗（P2 watchdog）：pending（task_id 回执）与 inflight（ask_user / llm_run 子作业异步
// 回喂）任一未回填时，续轮闸门（continueOpenLocked）永不开启 → 整轮永久挂起（回执丢失 / ask 过期 /
// 子作业未回填）。loop 每 pendingSweepInterval 扫描一次，对超过 pendingTaskTimeout 仍未回填的
// pending / inflight 按 failed 占位回填并摘除（复用 onAsyncDone / FeedToolResultStatus 同一路径）→
// 闸门重开、续轮。
// 取值保守（缺省 defaultPendingTaskTimeout=2h，可由项目级键 `pending_task_timeout_sec` 覆盖）：
// 长时异步（构建 / 抓取 / 编译 / 子作业）仍可正常完成，只在**回执确实丢失**时才兜底。
const (
	pendingSweepInterval = 30 * time.Second
	// defaultPendingTaskTimeout 看门狗超时缺省值（prj 键缺失 / 非正 → 回落此值）。
	defaultPendingTaskTimeout = 2 * time.Hour
)

// pendingTaskTimeoutKey 是项目级键「异步任务看门狗超时（秒）」（缺省 defaultPendingTaskTimeout）：
// 覆盖 pending 回执与 inflight（ask / 子作业）两类在飞登记的兜底判定阈值。
const pendingTaskTimeoutKey = "pending_task_timeout_sec"

// pendingTaskTimeoutOf 从**已读** prj 配置值表折算看门狗超时（纯函数，不再读配置）：
// 键存在且为正整数（秒）→ 采用；缺失 / 空 / 非数字 / 非正 → 回落 defaultPendingTaskTimeout。
func pendingTaskTimeoutOf(vals map[string]string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(vals[pendingTaskTimeoutKey])); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultPendingTaskTimeout
}

// S6 LLM 错误恢复参数（对齐 llm-error-handling.md §一）。
// llmRetryCount 为**回落默认**：usr 配置 retryCount（T-27 接线）未配置时生效；
// retryCount **显式 0 = 不重试**（P0-A，存在性判定），仅键缺失（persist 系统默认 2）才回落。
// 退避（重发间隔）**不自持**：经 `router.RetryWait`（`Retry-After` 优先 + 1s·2s·4s… 封顶 30s）计算，
// 见 retryWait；不再有 `retryDelay` 配置项（2026-10-05 定案，差异登记 41 I-156）。
const (
	llmRetryCount   = 2 // 无内容失败重发次数（方式 A）
	maxAutoContinue = 3 // length/断链自动续写上限（方式 B）
)

// asyncDoneMsg 是异步任务完成消息（gateway 回执 task-report → 回填续轮）。
// result = mcp-tasks-report 携带的终态结果（2026-09-18：`tasks/result` 方法面移除后，
// 通知面 `result_summary` 承载**结果全文** → 直接落 message 表 result 段并喂回续轮）。
type asyncDoneMsg struct {
	taskID string
	state  string // done / error / cancelled
	result string // 终态结果全文（done）/ 终态摘要（error/cancelled）
}

// pendingTool 是"转异步挂起"登记项：task_id → 发起信息（完成时落一条 role=tool 记录用）。
// movedAt = 转后台时间（RFC3339），写入结果记录的 async.moved_at。
type pendingTool struct {
	toolCallID string
	movedAt    string
}

// turnCtx 是单轮会话的状态机（调度器核心）：
// 输入队列（text-user / tool-result / text-notify）→ LLM 循环 → llm-receive 广播 →
// tool-call → gateway（上下文带 tool-call-id）→ 同步结果回喂 / 转异步挂起 → 回执续轮
// → … → llm-complete（唯一终态：complete/incomplete/error/interrupted）。
type turnCtx struct {
	server *Server
	req    StartReq
	ctx    context.Context
	cancel context.CancelFunc

	in         chan ChatMsg // 输入队列
	done       chan struct{}
	once       sync.Once
	hist       []ChatMsg // 会话历史（llm-start 组装：摘要 + 未压缩 turn，见 BuildHistory）
	iterations int       // 工具循环计数（maxToolIterations 保护）
	// kick 是「以**库内既有上下文**触发一次 LLM 循环」的内部触发（缓冲 1；子聊天首轮专用，
	// SUBCHAT-2 阶段 B-2）：交接内容已由 subchat_open handler 同步落库 → 本触发只让 loop 跑一次
	// chatOnce()（无新输入、不重复落库 → msgs() 从库带回交接消息）。loop 唯一消费点，保证
	// chatOnce 恒在 loop goroutine 内执行（与异步回执 / 工具结果续轮同一串行口径）。
	kick chan struct{}

	mu      sync.Mutex             // 保护 pending / inflight / stash / toolMsgKeys
	pending map[string]pendingTool // task_id → 转异步发起信息（挂起登记）
	// inflight 记录已派发、结果**异步回喂**的 ask_user / llm_run 子作业（tool_call_id →
	// 派发时刻）：与 pending 共同构成续轮闸门（见 continueOpenLocked）——同一批 tool_calls
	// 混有 ask/子作业 + 异步工具时，只有全部结果到齐才续轮（否则未决 tool_call 只有 running
	// 占位行，assistant.tool_calls 与 tool 结果配对破坏）。结果经 `tc.in` 回喂时在
	// deliverInput 摘除登记（**不在 feed 时摘除**，避免与 loop 消费时序竞态）。
	// 派发时刻用于看门狗兜底（reapStalePending）：超时未回填 → 按 failed 占位回填并摘除。
	inflight     map[string]time.Time
	stash        []ChatMsg               // 等待 pending 完成时合并提交的同步工具结果
	toolMsgKeys  map[string]string       // tool_call_id → role=tool 行主键（发起即落 running；终态回填同一行）
	toolCallMeta map[string]toolCallMeta // tool_call_id → 发起信息（B-05：终态回填免一次全量 LoadMessages）
	// persistedResultOK 记录本 turn **成功**落库结果的 tool_call_id（I-25 去重的硬判据）：
	// 成功 → 结果已由 msgs() 从库带回，本次输入不必重复叠加；失败 → 保留内存结果（避免丢结果）。
	persistedResultOK map[string]bool
	asyncDone         chan asyncDoneMsg // gateway 回执 task-report → loop 续轮
	continues         int               // 自动续写计数（maxAutoContinue 保护，S6）
	// retryAttempt = 本 turn **当前 LLM 调用**已执行的重试次数（RETRY-1，2026-10-17）：
	// 每次失败重发前自增并随中间态 `session-retry`（前端 llm-retry）广播；终态
	// `session-complete{status:error}` 携带该值（0 = 未重试）。仅在 loop goroutine 读写。
	retryAttempt int
	sessionLock       *sessionLock      // session 排他锁（onLLMStart 获取；Close 释放）
	result            string            // 终态输出文本（子轮次递归时父侧读取；finish 写入）
	turnErr           error             // 终态错误（子轮次递归时父侧读取；finish error 写入）
	// finishOnce 是**原子终态标志**（E2-2）：正常完成路径与 onLLMCancel（取消）可能并发到达同一轮次
	// （cancel 命中时正常路径的 complete 仍在飞）→ server.finish 经它保证终态仅落库/广播一次
	// （不二次写 CompleteTurnTokens、不二次广播 llm-complete）。与 Close 的 once 相互独立。
	finishOnce sync.Once
	// answer 跨段累积答案：自动续写（finish==length）与断链续写（resumePartial）会把同一轮正文
	// 分多段产出；每段被判定"保留"（续写前落库 / 终态）时 append，终态 `llm-complete.text` 与
	// `tc.result` 取**累积全文**（单段场景与旧行为逐字节一致）。仅 loop goroutine 读写，无需加锁。
	answer strings.Builder

	llmTemperature     *float64 // 来自 provider 配置的 LLM temperature（存在即生效，含 0；启动时加载一次）
	llmMaxOutputToken  *int     // 来自 provider 配置的 LLM maxOutputToken（最大输出 token；存在即生效，含 0；口径 Z1）
	llmMaxContextToken *int     // 来自 provider 配置的 LLM maxContextToken（上下文窗口；兜底归并窗口来源；口径 Z3）
	// LLM provider 与请求参数（P1-1：provider 配置真正生效；newTurnCtx 解析一次 → 下一个 turn 生效）：
	//   - llmSpec：本轮生效的 provider（`router.Spec`，LR-11 起经 router 出网；未命中 provider /
	//     恢复轮 = exe flags 隐含默认）；
	//   - llmModel：请求体 model（provider.model；未命中 = llm-start.llm 传入值；空 = spec.DefaultModel
	//     = exe flag -llm-model）；
	//   - llmThink/llmEffort：映射 reasoning_effort（Effort 优先；两者均空 = 不发送）。
	llmSpec   router.Spec
	llmModel  string
	llmThink  string
	llmEffort string
	// 超时/重试（来自 usr 配置，启动时加载一次；T-27 接线）：
	//   - responseTimeout/streamTimeout 传给 router `CallOptions`（首字节 / 流间隔超时）；
	//   - retryCount 用于无内容失败重发（方式 A，S6）：显式 0 = 不重试；退避值不自持（见 retryWait）。
	// 缺省 → 由 loadLLMRuntimeConfig 回落旧硬编码常量（120s/60s/2）；非正值仅超时两项回落。
	responseTimeout time.Duration
	streamTimeout   time.Duration
	retryCount      int
	// pendingTaskTimeout 异步看门狗超时（pending 回执 / inflight ask·子作业 未回填）——来自 prj 键
	// `pending_task_timeout_sec`（缺省 defaultPendingTaskTimeout）；非正 → 兜底回落缺省（见 reapStalePending）。
	pendingTaskTimeout time.Duration
	// maxToolIterations 单轮工具循环上限（来自 provider 定义；缺省 20）。
	maxToolIterations int
	// allowedTools 本轮工具白名单（来自本轮 agent 的 `tools`，AG-1/AG-C4）：nil = 不限制
	// （原样下发全部 hot 工具）；非空 → 仅白名单内工具进 LLM 工具面。每轮开始解析一次（热生效）。
	// P4 2026-10-01：解析后按**场景级别的可用工具级别矩阵**过滤（越权/不存在 → 静默剔除，
	// 见 filterWhitelistByLevel）——故此处非空集合恒为"场景级别可用 ∩ 本实例可见"的子集。
	allowedTools map[string]struct{}
	// denyTools 是本轮**硬拒**（调用面收窄）的工具名集（契约名口径）：`work_session`（群聊参与者
	// 干活子会话）排除 `subchat_finish`（SUBCHAT-3 阶段 F-2 · 群聊 §7 / D12：群聊收敛以主 agent
	// 「【已收敛】」标记 + 用户拍板为准，子 agent 自报结束无意义）。`llmTools` 下发面与 `toolAllowed`
	// 调用处**双拦**（挡模型幻觉）。空 = 不拒绝（其余会话零开销、行为逐字节不变）。
	denyTools map[string]struct{}
	// keepFullTurns / keepFullTokens / briefBudget 三段边界（项目级 `keep_full_max_turns` /
	// `keep_full_max_tokens` / `compress_token_threshold`，启动时加载一次）：完整区 = 最近 N 轮 / M token；
	// 简化区 = 再往前 brief token 累计 <= T（仅 text 投影）；算法与压缩侧共用（data.LocateZones）。
	keepFullTurns  int
	keepFullTokens int
	briefBudget    int
	// turnTokens 会话各轮**预存** token（`data-session-context` 只增伴随数组 turn_tokens，升序、
	// 排除当前轮）：组装侧三段判定**直接取预存值**（尾部对齐；缺值回退实时估算）。
	turnTokens []facade.TurnToken
	// forceFull 重试/恢复标记：本 turn 必须全量拼接（即使落在"非完整区"）。
	// 仅在 recoverTurnCtx（进程重启/死 turn 恢复）构造时置位（loop 启动前，无并发写）。
	forceFull bool
	// subOnce / subsession / sessionKind：会话行信息（**一次读取**，两级判定共用同一缓存）。
	//   - subsession = 本 turn 是否为**子会话**（DSL-4，42 §2 (253)：session.parent_id != ""）；
	//   - sessionKind = 会话行 `kind`（读侧归一：行缺 kind 且 parent_id 非空 ⇒ sub_session）。
	// 快路径 = req.Parents 非空（runChildTurn 逐级落链，恒为子会话，零查询）；慢路径 = 主会话 /
	// 恢复轮（req.Parents 空）→ 查一次会话行。**不落可变全局字段**，每 turn 只判一次。
	// 用途 = 「子会话压缩关闭」的超限提示（subsessionHint）+ **非主会话强制同步**（forceSyncTools）。
	subOnce     sync.Once
	subsession  bool
	sessionKind string
	// guideOnce / memGuideText / assetGuideText：「记忆 / 资产」两条指引**单轮只读一次**缓存
	//（msgs() 每次工具迭代都会调用，指引依赖 instance/workDir/配置，单轮内视为不变）——避免
	// 单轮内重复读配置（最多 20 次迭代 × 2 条指引）。
	guideOnce      sync.Once
	memGuideText   string
	assetGuideText string
}

// toolCallMeta 记录一次工具发起的名称与参数（persistToolRunning 时登记，B-05），
// 供终态回填（persistToolResult）复用，避免每次工具结果都做一次全量 LoadMessages。
type toolCallMeta struct {
	name string
	args map[string]any
}

// loadSessionInfo 懒加载本 turn 的会话行信息（**只读一次**，isSubsession / forceSyncTools 共用）：
//   - 快路径：req.Parents 非空 → 必为子会话（runChildTurn 逐级追加父会话链，零查询）→
//     按现行路径口径标 `sub_session`（dsl_run 的 LLM 步骤派生，单聊方案 D23）；
//   - 慢路径：主会话 / 恢复轮（req.Parents 空）→ 经 data 门面读一次会话行，
//     kind 由数据层读侧归一（行缺 kind 且 parent_id 非空 ⇒ sub_session）。
//
// 读失败 / 会话不存在 → 按**主会话**处理（保守：不误跳过压缩/沉淀、不改既有行为）。
func (tc *turnCtx) loadSessionInfo() {
	tc.subOnce.Do(func() {
		if len(tc.req.Parents) > 0 {
			tc.subsession = true
			tc.sessionKind = facade.SessionKindSubSession
			return
		}
		if tc.server == nil || tc.server.cfg == nil || tc.req.Session == "" {
			return
		}
		resp, err := tc.server.cfg.SessionGet(facade.SessionGetRequest{
			InstanceID: tc.req.InstanceID, SessionID: tc.req.Session,
			Scope: tc.server.cfgScope(tc.req.InstanceID),
		})
		if err != nil {
			return
		}
		tc.subsession = resp.Found && resp.Session.ParentID != ""
		tc.sessionKind = resp.Session.Kind
	})
}

// isSubsession 判定本 turn 是否为子会话（会话行 parent_id != ""；见 loadSessionInfo）。
func (tc *turnCtx) isSubsession() bool {
	tc.loadSessionInfo()
	return tc.subsession
}

// forceSyncTools 判定本 turn 的工具调用是否**强制同步**（不得转后台；SUBCHAT-2 阶段 A · 单聊
// §12.5 B5 / 群聊 §16 · D40）：**非主会话（kind 非空）一律 `AsyncMode = never`**。
//
// 理由：单聊/群聊的**收口点亮**与"本轮屏障"全靠"轮次跑完"，工具一旦转后台、轮次提前结束 →
// 判定全乱。适用范围 = 该会话里的**每一次**工具调用（不区分工具自身 async 档）。主会话
// （kind 为空）返回 false → 行为逐字节不变。
func (tc *turnCtx) forceSyncTools() bool {
	tc.loadSessionInfo()
	return tc.sessionKind != ""
}

// gatewayArgs 返回本次工具调用**下发给 gateway 的参数**：非主会话强制同步时附**调用级 async
// 覆盖**（`async=never`）——gateway `doCall` 读取后即剥离、不传下游，故不改 `mcp-tools-call`
// 载荷形状与工具入参（主会话原样返回，零开销）。**落库 / 前端展示仍用原始 args**（调用方传原参）。
func (tc *turnCtx) gatewayArgs(args map[string]any) map[string]any {
	if !tc.forceSyncTools() {
		return args
	}
	out := make(map[string]any, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out["async"] = "never"
	return out
}

// subsessionHint 为「子会话压缩关闭」时的上下文超限失败补一句可诊断说明（42 §2 (253) DSL-4 ⑤）：
// 子会话压缩关闭后上下文不压缩，超限即本轮失败（多为不可重试的协议错误，厂商原始报错不易解读）。
// 仅当「本 turn 为子会话」且项目级「压缩子会话」未开启、且错误**不可重试**时追加；其余原样返回。
func (s *Server) subsessionHint(tc *turnCtx, msg string, retryable bool) string {
	if retryable || tc == nil || !tc.isSubsession() || s.compressSubsessionOn(tc.req.InstanceID) {
		return msg
	}
	return msg + "（本 turn 为子会话，且项目「压缩子会话」未开启（设置 → 上下文管理）；子会话上下文不压缩，超限即本轮失败——如需压缩请开启该开关）"
}

// compressSubsessionOn 读项目级 `compress.subsession`（DSL-4「压缩子会话」，默认关闭）：
// 复用 memoryPrjConfig（同一份 prj 平铺表；与 compress 插件同键同读序）。读失败 / 键缺失 → false。
func (s *Server) compressSubsessionOn(instanceID string) bool {
	return memoryCfgStr(s.memoryPrjConfig(instanceID)[compressSubsessionKey]) == "true"
}

// compressSubsessionKey 是「压缩子会话」项目级开关键（默认关闭）：消费方 = 本文件超限提示 +
// compress 插件（子会话压缩门控）。键「提取子会话记忆」= memory 插件侧 `memory.subsession`（见 64 配置项一览）。
const compressSubsessionKey = "compress.subsession"

func newTurnCtx(parent context.Context, s *Server, req StartReq) *turnCtx {
	// ctx 绑定 instance_id：turn 内所有 gateway 方法调用（tools/call 等）
	// 经 instanceFromCtx 取到实例归属（业务消息一律必带，见 61 §0.1）。
	// parent = **本轮的上一级 ctx**（G-18 gapA）：主轮次传 context.Background()；子轮次
	// （runChildTurn）传发起它的那个 turn 的 ctx —— 父轮次取消/关闭时取消信号沿链下传，
	// 子轮次不再"一无所知地跑满全程"（此前恒 Background）。
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(withInstance(parent, req.InstanceID))
	// 组装历史：经总线 persist 一次取上下文（优先快照：快照 + snapshot_turn 之后 turns；
	// 无快照自动回退全量历史 summary 注入），见 sessionStore.BuildContextTokens。
	// 同轮次继续（req.Continue）：本 turn 已被快照覆盖（finish 写 snapshot_turn=本 turn），
	// 若仍走快照分支会与下方 msgs() 的 LoadMessages(本 turn) 重复——改用全量历史分支并
	// 排除本 turn，本 turn 既有消息由 msgs() 从库带回，保证该 turn 恒为末段（全量拼接）。
	hist, turnTokens := newSessionStore(s.bus, req.InstanceID).BuildContextTokens(req.Session, req.Turn, !req.Continue)
	// 场景 + agent 解析 + 系统提示词五层（**新建轮与恢复轮共用同一构造**，见 resolveTurnScenario）。
	// 会话固定参数（子聊天的 agent / scenario_id，落库在会话行）优先于请求参数。
	sc := s.resolveTurnScenario(req.InstanceID, req.Session, req.ScenarioID, req.Agent)
	if sc.sysPrompt != "" {
		hist = append([]ChatMsg{{Role: "system", Content: sc.sysPrompt}}, hist...)
	}
	// 单聊生命周期回写（§3.7 / §3.5）：**子会话新一轮开始处**，若该子聊天 `lifecycle == ready`
	// → 置回 `chatting`（`ready` 只是"最近一轮的建议"、非终态；用户继续聊 → 新一轮即回 chatting）。
	// 落点 = 轮次启动路径（本构造被 onLLMStart / startSubChatFirstTurn 共用）；判定用 sc 的
	// 会话行取值（同一次读取），主会话 / 子会话（sub_session）零开销。恢复轮（recoverTurnCtx）
	// 不走此处——它是**既有轮次**的恢复，不是新一轮。
	s.reopenSubChatOnNewTurn(req.InstanceID, req.Session, sc)
	// agent 级 LLM 引用（AG-C1 逐级回落）：agent.llmRef →（空）子系统键 →（空）现有通道
	// （req.LLMModel）→（空）defaultLLM →（空）系统默认。每轮开始解析 = 热生效（AG-C5）。
	agentLLMRef := ""
	var allowedTools map[string]struct{}
	if sc.agentDef != nil {
		agentLLMRef = sc.agentDef.LLMRef
		allowedTools = parseToolWhitelist(sc.agentDef.Tools)
		if allowedTools != nil {
			// 级别矩阵过滤（P4 2026-10-01）：越权（工具级别不在场景级别的"可用集合"内）/ 不存在
			// （不在本实例可见工具面）的名字**静默剔除**（不报错、不中断）；详见 filterWhitelistByLevel。
			allowedTools = s.filterWhitelistByLevel(req.InstanceID, sc.level, allowedTools)
		}
	}
	llmName := s.resolveAgentLLMName(req.InstanceID, agentLLMRef, "", req.LLMModel)
	// 工具面**硬拒**集（SUBCHAT-3 阶段 F-2）：`work_session`（群聊参与者干活子会话）排除
	// `subchat_finish` —— 群聊收敛以主 agent「【已收敛】」标记 + 用户拍板为准（§7 / D10 / D12）；
	// **单聊（sub_chat）不受影响**（其收口仍靠 subchat_finish）。其余会话（kind 空 / 主会话 /
	// sub_session）→ 空集，零开销、行为逐字节不变。
	var denyTools map[string]struct{}
	if sc.kind == facade.SessionKindWorkSession {
		denyTools = map[string]struct{}{subchatFinishTool: {}}
	}
	// usr 配置一次读取共用（B-37）：provider 解析 + 运行时超时/重试解析共用同一次 UserConfigGet，
	// 取代原 loadLLMProvider + loadLLMRuntimeConfig 各读一次（同源重复往返）。解析语义不变。
	usrCfg, cfgErr := s.loadUserConfig(req.InstanceID)
	if cfgErr != nil {
		logf("[chonkpilot-server] newTurnCtx: UserConfigGet failed: %v\n", cfgErr)
	}
	// LLM provider 配置（llm-start.llm = provider name）：命中 → baseUrl/apiKey/model/temperature/
	// maxOutputToken/thinking 以配置为准；未命中（nil）→ 保持现状（exe flags + 请求体 think/effort）。
	// 子轮次：llmRef 非空则用被委派 agent 的 provider，否则继承父轮次（现状）。
	pcfg := providerFromConfig(usrCfg, llmName)
	llmTemp, llmMO, llmMC, llmMaxIter := (*float64)(nil), (*int)(nil), (*int)(nil), 0
	llmModel := llmName // 未命中 provider：现状语义（传入值即请求体 model）
	llmSpec := s.specFor(pcfg)
	if pcfg != nil {
		llmTemp, llmMO, llmMC, llmMaxIter = pcfg.Temperature, pcfg.MaxOutputToken, pcfg.MaxContextToken, pcfg.MaxToolIterations
		llmModel = pcfg.Model // 空 = 回落 spec.DefaultModel（exe flag -llm-model）
	}
	if llmMaxIter <= 0 {
		llmMaxIter = defaultMaxToolIterations
	}
	llmThink, llmEffort := resolveThinkEffort(pcfg, req)
	// 加载 usr 超时/重试配置（T-27 接线）：缺省已回落旧硬编码常量（复用上面已读的 usrCfg）。
	respTO, streamTO, retryCnt := llmRuntimeConfigFrom(usrCfg)
	// 三段边界 3 键（keep_full_max_turns / keep_full_max_tokens / compress_token_threshold）
	// 一次 ConfigKVGet 共用（B-37）：同域/同读序不变，避免逐键各自往返。
	zoneVals := s.prjConfigValues(req.InstanceID, keepFullMaxTurnsKey, keepFullMaxTokensKey, briefBudgetKey, pendingTaskTimeoutKey)
	tc := &turnCtx{
		server:    s,
		req:       req,
		ctx:       ctx,
		cancel:    cancel,
		in:        make(chan ChatMsg, 64),
		done:      make(chan struct{}),
		hist:      hist,
		kick:      make(chan struct{}, 1),
		pending:   make(map[string]pendingTool),
		asyncDone: make(chan asyncDoneMsg, 8),

		llmTemperature:     llmTemp,
		llmMaxOutputToken:  llmMO,
		llmMaxContextToken: llmMC,
		llmSpec:            llmSpec,
		llmModel:           llmModel,
		llmThink:           llmThink,
		llmEffort:          llmEffort,
		responseTimeout:    respTO,
		streamTimeout:      streamTO,
		retryCount:         retryCnt,
		pendingTaskTimeout: pendingTaskTimeoutOf(zoneVals),
		maxToolIterations:  llmMaxIter,
		allowedTools:       allowedTools,
		denyTools:          denyTools,
		keepFullTurns:      keepFullTurnsOf(zoneVals),
		keepFullTokens:     keepFullTokensOf(zoneVals),
		briefBudget:        briefBudgetOf(zoneVals),
		turnTokens:         turnTokens,
	}
	go tc.loop()
	return tc
}

// turnScenario 是「本轮场景 + agent」解析 + 系统提示词构造的产物（新建轮与恢复轮**共用同一构造**，
// 见 resolveTurnScenario）。
type turnScenario struct {
	// sysPrompt 是拼好并替换占位符后的系统提示词（空 = 不注入）。
	sysPrompt string
	// level 是场景级别（app|user|project|prjusr；工具白名单级别矩阵过滤用）。
	level string
	// agentDef 是本轮生效 agent 定义（nil = 无场景 / 无 agent）。
	agentDef *scenarioAgent
	// kind / lifecycle 是**会话行**的 kind 与 lifecycle（随固定参数同一次读取带回；单聊方案 §3.7）。
	// 用途 = 子聊天生命周期回写（`ready → chatting`，见 newTurnCtx / reopenSubChatOnNewTurn）——
	// 复用同一次会话行读取，不额外查询。
	kind      string
	lifecycle string
}

// resolveTurnScenario 读场景（AG-1 / AG-2 · 25 §3，每轮读一次 = 配置热生效，AG-C5，不缓存）→
// 解析本轮生效 agent 定义 → 拼系统提示词（全局 + 目录 + 场景 + agent + **会话注入**五层，按序
// 拼接、空层跳过、占位符整段替换）。
//
// 参数优先级（单聊方案 §3.3 / §4：**会话固定参数 > 请求参数**，SUBCHAT-2 阶段 A）：优先取
// **会话行落库的固定参数**（`agent` / `scenario_id`，仅子聊天有），其次才取请求参数。
//
// **新建轮（newTurnCtx）与恢复轮（recoverTurnCtx）同口径**：二者共用本构造 —— 恢复轮虽无
// scenarioID / agent 入参（未持久化在请求里），仍按上方优先级**从会话行取回**固定参数，
// 故重启后人格不丢（不再退化为"仅全局层 + 目录层"）。**第五层天然覆盖两条路径**：判定点只有
// 这一处组装点。
func (s *Server) resolveTurnScenario(instanceID, sessionID, scenarioID, agent string) turnScenario {
	// 会话固定参数（会话行 kind / scenario_id / agent / lifecycle + 群聊 目标/剧本/收敛；见 sessionRow）。
	sess := s.sessionRow(instanceID, sessionID)
	if sess.ScenarioID != "" {
		scenarioID = sess.ScenarioID
	}
	if sess.Agent != "" {
		agent = sess.Agent
	}
	desc, level, agents := s.loadScenario(instanceID, scenarioID)
	// 本轮生效的 agent 定义：子轮次（agent 非空）= 被委派 agent —— 按**统一判据**
	// （resolveAgentDef：instance 级场景内同名 agent 优先，未命中回落 global 级 = app 级场景内的
	// 内置 agent）解析，与 llm_run 的委派判定 agentDelegable 同一来源；顶层轮次 = 主 agent
	// （AG-2，只取 llmRef / tools）。**agent 层提示词**：顶层 = 主 agent 的 prompt（主 agent =
	// `main.agent.md`，25 §3「agent 层」）；子轮次 = 被委派 agent 的 persona（childAgentSystem）。
	agentDef := mainScenarioAgent(agents)
	agentDomain := AgentDef{}
	if agent != "" {
		agentDef, agentDomain = s.resolveAgentDef(scenarioID, agents, agent)
	}
	agentLayer := ""
	if agent != "" {
		agentPrompt, delegateCond := "", ""
		if agentDef != nil {
			agentPrompt, delegateCond = agentDef.Prompt, agentDef.DelegateCond
		} else {
			agentPrompt = agentDomain.Content
		}
		agentLayer = childAgentSystem(agent, agentPrompt, delegateCond)
	} else if agentDef != nil {
		agentLayer = agentDef.Prompt
	}
	// 全局层身份名（25 §3 / 用户口径 2026-09-26）：当前 agent 的**人可读裸名**（顶层 = 场景主
	// agent；子轮次 = 被委派 agent）；通用模式（无场景 / 解析不到名）→ defaultAgentName（肥猫）。
	agentName := defaultAgentName
	if n := currentAgentName(agentDef, agentDomain); n != "" {
		agentName = n
	}
	// 系统提示词**按序拼接**（25 §3）：全局层（身份 / 运行环境，代码写死）→ 目录层（四级数据根 +
	// capability 子目录 + DSL env 用法）→ 场景层（scenario.description + 成员段）→ agent 层 →
	// **会话注入层**（第五层，仅子聊天会话非空；单聊方案 §3.3 / D37，代码内建）。
	// 空层跳过；`{{toolchain.*}}` / `{{path.*}}` 占位符对**合成后的整段**替换（覆盖任一层）。
	sysPrompt := systemPromptLayers(s.globalLayerPrompt(agentName), systemDirectoryLayer(), scenarioLayer(scenarioID, desc, agents), agentLayer, s.chatInjectionLayer(instanceID, sess))
	if sysPrompt != "" {
		sysPrompt = s.replaceToolchain(instanceID, sysPrompt)
		sysPrompt = s.replacePaths(instanceID, sysPrompt)
	}
	return turnScenario{
		sysPrompt: sysPrompt, level: level, agentDef: agentDef,
		kind: sess.Kind, lifecycle: sess.Lifecycle,
	}
}

// ── 系统提示词分层（25 §3）──────────────────────────────────────────────
//
// 按序拼接：全局层（身份 / 运行环境）→ 目录层（四级数据根 + capability 子目录 + DSL env 用法）→
// 场景层（scenario.description + 团队成员段）→ agent 层（当前 agent 的 prompt，主 agent =
// main.agent.md）。目录层**不受场景门控**；无场景（= 通用模式）注入全局层 + 目录层。
// `memoryGuide` / `assetGuide` 是**两条功能指引**、与上述层不是一回事 → 仍在 msgs() 独立注入。

// defaultAgentName = 通用模式（无场景）的默认身份名（用户口径 2026-09-26）。
const defaultAgentName = "肥猫"

// systemDirectoryDocKind 是目录层内容的出厂 system 文档 kind
// （= `capability/system/system-directory.md`，见 src/initdata/capability/system/）。
const systemDirectoryDocKind = "system-directory"

// systemDirectoryLayer 构造系统提示词的**目录层**：四级数据根（app/user/project/prjusr）与
// `{{path.*}}` 占位符含义、每级 `capability/` 子目录用途、DSL 只读 env（`CHONKPILOT_*`）用法。
// 内容 = 出厂文件 `capability/system/system-directory.md`（经 data 门面读 embed 内置回落，
// 见 OP-10）；每轮组装时读取（不入快照），缺失 → ""（该层跳过）。文中 `{{path.*}}` 由
// replacePaths 在**整段拼接后**替换为当前实例的真实绝对路径。
func systemDirectoryLayer() string {
	return strings.TrimSpace(data.SystemDoc(systemDirectoryDocKind))
}

// globalLayerPrompt 构造系统提示词的**全局层**（身份 / 运行环境）——按 25 §3「代码写死」（不落
// 文件、不 embed、不可配置）。
//
// 文案模板（用户 2026-09-26 正式口径）：`你是 {agentName}，一个全能智能体。你运行在 {env} 中。`
//   - agentName = 当前 agent 的**人可读裸名**（见 currentAgentName）；通用模式（无场景）= 肥猫；
//   - env = 运行环境，取**代码可确定的客观信息**：运行形态（Form，61 §4.6）+ 平台（runtime.GOOS）。
func (s *Server) globalLayerPrompt(agentName string) string {
	return "你是 " + agentName + "，一个全能智能体。你运行在 " + s.runEnv() + " 中。"
}

// currentAgentName 取本轮「当前 agent」的**人可读裸名**——**不带** `<场景id>/` 前缀：该前缀是
// LLM **委派引用**的消歧口径（见 scenarioLayer / membersSegment），而全局层是「我是谁」的身份
// 文案、面向人可读，故此处用裸名（与注入面/委派面的引用格式**不冲突**：后者仍带前缀）。
//   - instance 级（当前场景内 agent）→ scenarioAgent.Name；
//   - global 级（app 级场景内置 agent）→ AgentDef.Name；
//   - 皆空（无场景 / 未解析到）→ ""（调用方回落 defaultAgentName）。
func currentAgentName(a *scenarioAgent, d AgentDef) string {
	if a != nil {
		if n := strings.TrimSpace(a.Name); n != "" {
			return n
		}
	}
	return strings.TrimSpace(d.Name)
}

// runEnv 返回全局层的**运行环境**文案（客观可确定：运行形态 + 平台）。
func (s *Server) runEnv() string {
	client := "桌面客户端"
	switch s.Form() {
	case FormGui:
		client = "GUI 客户端"
	case FormBrowser:
		client = "浏览器端"
	}
	return "ChonkPilot " + client + "（" + platformName() + "）"
}

// platformName 返回当前平台的人可读名（runtime.GOOS 映射；未知则原样返回）。
func platformName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

// scenarioLayer 拼接系统提示词的**场景层**（25 §3）：`scenario.description`（"我们是一个团队…"）
// + **代码按场景 agents 自动拼接的成员段**（每条 = 名字 + 角色标签 + 描述）。两者皆空 → ""
// （调用方跳过该层；无场景 / 未选场景时 desc 与 agents 均为空 → 不注入场景层）。
//
// 命名与唯一性（2026-09-26）：成员名统一带**场景前缀**（`<场景id>/<agent名>`，scenarioID 空则裸名），
// 与 llm_run 委派引用 / agentDelegable / 子轮 system 读取**同一口径**（agent 允许跨场景重名，前缀消歧）。
func scenarioLayer(scenarioID, description string, agents []scenarioAgent) string {
	var b strings.Builder
	if d := strings.TrimSpace(description); d != "" {
		b.WriteString("【场景】" + d)
	}
	if members := membersSegment(scenarioID, agents); members != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("【团队成员】\n" + members)
	}
	return b.String()
}

// membersSegment 生成场景团队成员段（每行 = 名字 + 角色标签 + 描述；三者皆空的名目跳过）。
// 名字带场景前缀（`<场景id>/<agent名>`）——LLM 据此以该名委派（见 resolveAgentDef / agentDelegable）。
func membersSegment(scenarioID string, agents []scenarioAgent) string {
	lines := make([]string, 0, len(agents))
	for _, a := range agents {
		name, role, desc := strings.TrimSpace(a.Name), strings.TrimSpace(a.RoleTag), strings.TrimSpace(a.Description)
		if name == "" && role == "" && desc == "" {
			continue
		}
		if name != "" && scenarioID != "" {
			name = scenarioID + "/" + name
		}
		line := "- " + name
		if role != "" {
			line += "（" + role + "）"
		}
		if desc != "" {
			line += "：" + desc
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// systemPromptLayers 按 全局层 → 目录层 → 场景层 → agent 层 → 会话注入层 顺序拼接为一条 system
// 文本（空层跳过；非空层之间以空行分隔）。全空 → ""（调用方不注入 system）。
func systemPromptLayers(layers ...string) string {
	parts := make([]string, 0, len(layers))
	for _, p := range layers {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ── 会话注入层（第五层，单聊方案 §3.3 / D37；SUBCHAT-2 阶段 A）──────────
//
// 承载「子聊天」专属指令（本阶段 = 收口协议骨架：完成时调用 subchat_finish 自报）。约定：
//   - **判定依据 = 会话行 `kind`**（确定的事实，不依赖模型发挥 / 场景配置 / 工具入参）；
//   - **文本必须代码内建**（放到外部即可改、被改错就失去意义 → 改文案走代码评审）；
//   - **所有子聊天（sub_chat / group_chat）统一一套**，不按 purpose / agent 分化；
//   - 群聊在此基础上另加"参与者名单 + 角色"（见群聊方案 §8 · D43，后续阶段）。

// subChatLayerText 是会话注入层正文（代码内建常量；工具入参细节留待工具契约落地时与
// `src/initdata/capability/tools/**` 对齐，此处只写协议骨架）。
const subChatLayerText = `[子聊天协议]
你当前在一个独立的「子聊天」会话中，与用户进行一段有明确目标的讨论。
当本次讨论的目标已经达成、你认为可以收尾时，调用 subchat_finish 工具自报完成（这只是"你建议结束"，是否结束由用户决定）。
你不必等待用户催促：达成目标即可自报；用户也可随时结束本次子聊天。`

// subChatInjectionLayer 按会话 `kind` 返回会话注入层文本（非子聊天 → "" = 不注入该层）。
func subChatInjectionLayer(kind string) string {
	if !isSubChatKind(kind) {
		return ""
	}
	return subChatLayerText
}

// chatInjectionLayer 返回会话注入层（第五层）文本（非子聊天体系 → "" = 不注入该层）：
//   - `sub_chat`（单聊）→ 收口协议骨架（既有行为逐字节不变）；
//   - `group_chat`（群聊本体）/ `work_session`（参与者的干活子会话）→ **群聊注入层**（群聊方案
//     §18.2 / D43 / D51 / D52）：参与者名单 + 各自角色 + 本次目标 + 剧本 + 收敛/退出条件
//     （`work_session` 取其**父群聊会话**的注入内容）+ 推进规则 + 收口协议；**每轮注入给全体**。
//
// 文本**代码内建**（沿用单聊第五层"必须代码内建"的口径）：放到外部即可被改、被改错就失去意义。
func (s *Server) chatInjectionLayer(instanceID string, sess facade.Session) string {
	switch sess.Kind {
	case facade.SessionKindSubChat:
		return subChatLayerText
	case facade.SessionKindGroupChat:
		return s.groupChatInjectionLayer(instanceID, sess)
	case facade.SessionKindWorkSession:
		// 干活子会话：注入内容取自**父群聊会话**（名单 + 目标 + 剧本 + 收敛条件）。
		return s.groupChatInjectionLayer(instanceID, s.sessionRow(instanceID, sess.ParentID))
	default:
		return ""
	}
}

// groupChatInjectionLayer 构造**群聊注入层**文本（`group` = 群聊会话行）。
// `group.Kind != group_chat`（父会话取不到 / 类型不符）→ ""（不注入，保守）。
func (s *Server) groupChatInjectionLayer(instanceID string, group facade.Session) string {
	if group.Kind != facade.SessionKindGroupChat {
		return ""
	}
	var b strings.Builder
	b.WriteString(groupChatProtocolText)
	if roster := s.groupRosterSegment(instanceID, group.ID); roster != "" {
		b.WriteString("\n【参与者与职责】\n" + roster)
	}
	if g := strings.TrimSpace(group.Goal); g != "" {
		b.WriteString("\n【本次目标】\n" + g)
	}
	if p := strings.TrimSpace(group.Playbook); p != "" {
		b.WriteString("\n【剧本 / 推进规则】\n" + p)
	}
	if c := strings.TrimSpace(group.Convergence); c != "" {
		b.WriteString("\n【收敛 / 退出条件】\n" + c)
	}
	mainAgent := strings.TrimSpace(group.Agent)
	b.WriteString("\n" + groupChatReplyRules(mainAgent))
	// 收口协议（与单聊共用一套的既有文本；群聊里主 agent 的收敛由「已收敛」标记承担）。
	b.WriteString("\n\n" + subChatLayerText)
	return b.String()
}

// groupRosterSegment 生成群聊参与者名单段（每行 `- <agent>：<role>`；顺序 = 名单固定顺序 D3）。
// 读不到 → ""（`groupChatInjectionLayer` 跳过该段）。
func (s *Server) groupRosterSegment(instanceID, groupID string) string {
	if s == nil || s.cfg == nil || groupID == "" {
		return ""
	}
	res, err := s.cfg.GroupParticipantsLoad(facade.GroupParticipantsLoadRequest{
		InstanceID: instanceID, SessionID: groupID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] groupRosterSegment(%s): %v\n", groupID, err)
		return ""
	}
	lines := make([]string, 0, len(res.List))
	for _, p := range res.List {
		line := "- " + p.Agent
		if r := strings.TrimSpace(p.Role); r != "" {
			line += "：" + r
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// groupChatProtocolText 是群聊注入层的协议头（代码内建）。
const groupChatProtocolText = `[群聊协议]
你正在参与一个多智能体「群聊」（展示板：只有结论会上屏；你的完整思考与工具过程留在你自己的会话里）。`

// groupChatReplyRules 生成群聊「本轮该怎么回应」的推进规则说明（代码内建；`mainAgent` 空 = 不点主 agent 名）。
// 机读约定（**可机读**，供调度器判定）：
//   - **无关**：回复的**首个非空行**恰为「无关」→ 判「无关」（不上屏、不进入下一轮）；
//   - **收敛**：主 agent 回复中**单独一行**为「【已收敛】」→ 判目标达成（点亮「可以结束了」）。
func groupChatReplyRules(mainAgent string) string {
	var b strings.Builder
	b.WriteString(`【本轮该怎么回应】
- 你被叫到时，先判断本轮群聊发言是否与你**相关**：
    · **无关** → 只回复「无关」（首个非空行恰为这两个字）——不会上屏，也不会进入下一轮；
    · **相关** → 直接给出你的**结论**（有文件产物请在结论文本里带上文件路径）。
- 用你回复的**最后一行**写 @ 名单，决定下一个叫谁：@某人（可多个，如 @A @B）/ @全体 / 不写 @ = 全体。
- 收敛要有耐心：判定收敛前先看「有没有实质推进」，不得草率收敛。`)
	if a := strings.TrimSpace(mainAgent); a != "" {
		b.WriteString("\n- 主 agent（" + a + "）负责兜底与判定收敛：判目标达成时，在你回复里**单独一行**写「【已收敛】」。")
	}
	return b.String()
}

// isSubChatKind 判定会话类型是否属「子聊天」体系（`sub_chat` / `group_chat`）——与数据层
// `session.isSubChatKind` 同一口径（此处不依赖数据层内部包，故按 facade 常量比对）。
func isSubChatKind(kind string) bool {
	return kind == facade.SessionKindSubChat || kind == facade.SessionKindGroupChat
}

// sessionRow 读会话行（单聊方案 §3.2 / §3.3 / §3.7）：返回 kind / lifecycle / scenario_id / agent
// 以及群聊的 goal / playbook / convergence（SUBCHAT-3 阶段 F）。lifecycle 供子聊天生命周期回写
// （ready→chatting）复用同一次读取。
// 读失败 / 会话不存在 / 无数据门面 → 空 Session（按"普通会话"处理，保守不改既有行为）。
// kind 已经由数据层读侧归一（行缺 kind 且 parent_id 非空 ⇒ sub_session，见 wire.SessionFromWire）。
func (s *Server) sessionRow(instanceID, sessionID string) facade.Session {
	if s == nil || s.cfg == nil || sessionID == "" {
		return facade.Session{}
	}
	resp, err := s.cfg.SessionGet(facade.SessionGetRequest{
		InstanceID: instanceID, SessionID: sessionID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] sessionRow(%s): %v\n", sessionID, err)
		return facade.Session{}
	}
	if !resp.Found {
		return facade.Session{}
	}
	return resp.Session
}

// resolveThinkEffort 解析本轮的思考参数（→ ChatOptions.Think/Effort → reasoning_effort）。
// P1-1 修正「前端恒发 effort 导致 thinking=off 被短路」：后端以 provider 配置为准，并保留
// 请求体 think 的显式关闭语义。规则：
//  1. 请求体 think=off（用户当轮关闭，前端 ChatPanel/MessageList 恒发 on/off）→ 不发送；
//  2. provider 记录含 thinking：false → 不发送；true → reasoning_effort=reasoningEffort（缺省 high）；
//  3. 未命中 provider / 记录无 thinking 键 → 按请求体：think=on → effort（缺省 high）；
//     其它取值原样透传（兼容非 on/off 的调用方，保持旧行为）。
func resolveThinkEffort(pcfg *llmProviderCfg, req StartReq) (think, effort string) {
	if req.Think == "off" {
		return "", ""
	}
	if pcfg != nil && pcfg.Thinking != nil {
		if !*pcfg.Thinking {
			return "", ""
		}
		return "", effortOrDefault(pcfg.ReasoningEffort)
	}
	if req.Think == "on" {
		return "", effortOrDefault(req.Effort)
	}
	return req.Think, req.Effort
}

// effortOrDefault 思考力度缺省 high（thinking 开启但未配置 reasoningEffort 时生效）。
func effortOrDefault(v string) string {
	if strings.TrimSpace(v) == "" {
		return "high"
	}
	return v
}

// taskNodeBase 计算任务节点的树归属（turn.go / domainmcp.go 建节点共用）：
//   - 主会话（parents 空）→ (本会话, "")：顶层节点，行为不变；
//   - 子会话（llm_run 的 LLM 步骤经 runChildTurn 派生的子 turn，parents 非空）→
//     (顶层会话, 该子会话对应 kind=llm 节点的 TaskID)：子会话内产生的工具/任务节点
//     挂到该 llm 节点之下，避免恒为顶层（任务树偏平）。
//
// 顶层会话 = parents[0]（runChildTurn 逐级追加父会话链，首项恒为最顶层主会话）。
// 时序：jobdsl.llmAction 先建 llm 节点（SessionID=子会话）再 runChildTurn 进子会话执行，
// 故子会话内建工具节点时父 llm 节点必已存在；异常窗口（查不到父，如手工驱动的子会话）
// 退化为空 parent_id（顶层）。
func (s *Server) taskNodeBase(tc *turnCtx) (topSession, parentID string) {
	if len(tc.req.Parents) == 0 {
		return tc.req.Session, ""
	}
	topSession = tc.req.Parents[0]
	if parent := s.tasks.latestLLMSessionNode(tc.req.InstanceID, topSession, tc.req.Session); parent != nil {
		parentID = parent.TaskID
	}
	return topSession, parentID
}

// Close 终止轮次（幂等）：释放 session 锁 + 取消 + 关 done。
// 快照由唯一终态路径（server.finish → llm-complete）写，此处不再写。
func (tc *turnCtx) Close() {
	tc.once.Do(func() {
		if tc.sessionLock != nil {
			tc.sessionLock.Release()
		}
		tc.cancel()
		close(tc.done)
	})
}

// FeedText 用户消息进入会话（触发 LLM 一轮；kind=text 标记用户提问）。
func (tc *turnCtx) FeedText(content string) {
	tc.feed(ChatMsg{Role: "user", Kind: "text", Content: content})
}

// FeedContinue 同轮次继续：注入一条 Kind=continue 的 user 消息（非新轮边界，落库保留；
// 内容缺省用 assembleContinueText）。与 S6 自动续写注入同口径。
func (tc *turnCtx) FeedContinue(content string) {
	if strings.TrimSpace(content) == "" {
		content = assembleContinueText
	}
	tc.feed(ChatMsg{Role: "user", Kind: assembleContinueKind, Content: content})
}

// FeedNotify 异步工具通知进入会话（text-notify；kind=notify 供压缩排除，不当作用户提问）。
func (tc *turnCtx) FeedNotify(content string) {
	tc.feed(ChatMsg{Role: "user", Kind: "notify", Content: "[工具通知] " + content})
}

// FeedToolResult 工具/ask 结果喂回 LLM 循环（type=tool-result；落库 status=completed）。
func (tc *turnCtx) FeedToolResult(toolCallID, content string) {
	tc.FeedToolResultStatus(toolCallID, content, "completed")
}

// FeedToolResultStatus 同 FeedToolResult，显式指定 tool_call_status（failed/cancelled/...）：
// 先落一条 role=tool（call+result）再喂回循环续轮（保证结果落库且只出现一次）。
func (tc *turnCtx) FeedToolResultStatus(toolCallID, content, status string) {
	tc.persistToolResult(toolCallID, content, status, nil)
	tc.feed(ChatMsg{Role: "tool", ToolCallID: toolCallID, Content: content})
}

// Kick 请求 loop 以**库内既有上下文**跑一次 LLM 循环（无新输入、不重复落库）——子聊天首轮
// 专用（SUBCHAT-2 阶段 B-2）：交接内容已由 subchat_open handler 同步落库，msgs() 从库带回。
// 非阻塞（缓冲 1，重复请求合并为一次）；loop 已关（done）→ 丢弃。直接构造的 turnCtx（测试）
// 无 kick 通道 → no-op。
func (tc *turnCtx) Kick() {
	if tc.kick == nil {
		return
	}
	select {
	case tc.kick <- struct{}{}:
	default:
	}
}

// feedTimeout 是输入队列投递的最长等待（队列满时兜底，B-07）：feed 由工具/异步回执等**非 loop
// 协程**调用，队列满时无限阻塞会挂住调用方 goroutine；超时则丢弃并记录（不静默吞）。
const feedTimeout = 5 * time.Second

// feedDroppable 判定该消息在输入队列满时可否丢弃：仅「工具通知」（Role=user、Kind=notify，
// 非必需）可丢；tool 结果与「继续」（Kind=continue）及用户文本承载 LLM 必需的续轮输入，不可丢。
func feedDroppable(msg ChatMsg) bool {
	return msg.Role == "user" && msg.Kind == "notify"
}

func (tc *turnCtx) feed(msg ChatMsg) {
	select {
	case tc.in <- msg:
		return
	case <-tc.done:
		return
	default:
	}
	if feedDroppable(msg) {
		// 队列满：可丢类带超时重试，超时则丢弃并记录（仅此罕见分支才分配定时器，热路径零额外开销）。
		select {
		case tc.in <- msg:
		case <-tc.done:
		case <-time.After(feedTimeout):
			logf("[chonkpilot-server] turn %s 输入队列投递超时（满），丢弃 role=%s 消息\n", tc.req.Turn, msg.Role)
		}
		return
	}
	// 不可丢类（tool 结果 / 「继续」/ 用户文本）：**不丢**。上方非阻塞尝试已失败（队列满）→
	// **转后台 goroutine 阻塞投递**（对齐 onGatewayTaskDone 对 asyncDone 的改法）：既不丢弃，也不
	// 阻塞调用方。关键：loop 自身也会投递（chatOnce 的自动续写「继续」、reapStalePending 的兜底
	// 回填）——同步阻塞会自死锁（队列满时消费方恰是调用方自身）；非 loop 调用方则在总线同步派发
	// 线程上阻塞（宿主 UI 线程冻结）。仅 done / ctx 取消才放弃投递。
	go func() {
		select {
		case tc.in <- msg:
		case <-tc.done:
		case <-tc.ctx.Done():
		}
	}()
}

// loop 调度循环：取输入 → LLM 一轮 → 广播/工具/回喂 / gateway 回执续轮，直到 complete/error/close。
func (tc *turnCtx) loop() {
	defer func() {
		// 清理只作用于仍指向本 turnCtx 的登记：tool-retry 死 turn 恢复会沿用原 {session, turn}
		// 重建新 turnCtx（同键换实例），旧 loop 收尾不得误删新轮次的登记（进程重启后无旧 loop；
		// 超时回收 → 立即重试的场景需此守卫）。
		tc.server.mu.Lock()
		// 运行态键 = instKey(instance, id)（缺口 5：按 instance 分桶）；清理只作用于仍指向
		// 本 turnCtx 的登记，不误删其他 instance / 新轮次的登记。
		key := instKey(tc.req.InstanceID, tc.req.Turn)
		// 轮次关闭 → 摘除本轮 pending 的 task_id 运行态索引（不再有等待任务）。**无条件**执行：
		// 仅删仍指向本 turnCtx 的项（同键换实例时，旧 tc 的陈旧索引也须清掉，避免 taskTurnFor
		// 命中已关闭的旧 tc 而漏续轮）。
		tc.server.forgetPendingTaskIndexLocked(tc)
		if tc.server.turns[key] == tc {
			delete(tc.server.turns, key)
			if tc.server.busy[instKey(tc.req.InstanceID, tc.req.Session)] == tc.req.Turn {
				delete(tc.server.busy, instKey(tc.req.InstanceID, tc.req.Session))
			}
			delete(tc.server.continuePending, key)
			// 轮次关闭 → 摘除本轮的 ask 等待登记（E2-1：否则无答复的 ask 常驻内存）。
			tc.server.clearAsksByTurnLocked(tc.req.InstanceID, tc.req.Turn)
		}
		tc.server.mu.Unlock()
		tc.Close()
	}()
	// pending 回执兜底（P2 watchdog）：周期扫描超时未回执的 pending，按 failed 占位续轮。
	sweep := time.NewTicker(pendingSweepInterval)
	defer sweep.Stop()
	for {
		select {
		case <-tc.done:
			return
		case m := <-tc.asyncDone:
			tc.onAsyncDone(m)
		case <-sweep.C:
			tc.reapStalePending()
		case <-tc.kick:
			// 以库内既有上下文跑一次 LLM 循环（子聊天首轮；无新输入、不重复落库，见 Kick）。
			tc.chatOnce()
		case msg := <-tc.in:
			tc.deliverInput(msg)
		}
	}
}

// deliverInput 消费一条输入队列消息（loop goroutine 唯一调用点）：工具结果先摘除在飞登记，
// 再按续轮闸门决定「续轮」或「暂存」——闸门未开（仍有异步 pending / ask / 子作业在飞）时
// 不得续轮，否则未决 tool_call 只有 running 占位行，assistant.tool_calls 与 tool 结果配对破坏。
// 非工具消息（用户文本 / 继续 / 通知）不受闸门约束，直接进入 chatOnce。
func (tc *turnCtx) deliverInput(msg ChatMsg) {
	if msg.Role != "tool" {
		tc.chatOnce(msg)
		return
	}
	tc.clearInflight(msg.ToolCallID)
	tc.mu.Lock()
	if !tc.continueOpenLocked() {
		tc.stash = append(tc.stash, msg)
		tc.mu.Unlock()
		return
	}
	msgs := append(append([]ChatMsg{}, tc.stash...), msg)
	tc.stash = nil
	tc.mu.Unlock()
	tc.chatOnce(msgs...)
}

// reapStalePending 扫描超时未回填的 pending（回执丢失 / 未登记）与 inflight（ask 过期 / 子作业
// 未回填）——到点按 failed 占位回填并摘除，使续轮闸门重开：
//   - pending：复用 onAsyncDone 同一落库 / 续轮路径（state="timeout" → 落 failed）；
//   - inflight：经 FeedToolResultStatus 落 failed 结果 + 回喂（deliverInput 消费时摘除登记）。
func (tc *turnCtx) reapStalePending() {
	now := time.Now()
	timeout := tc.pendingTaskTimeout
	if timeout <= 0 {
		timeout = defaultPendingTaskTimeout
	}
	var stale []asyncDoneMsg
	var staleInflight []string
	tc.mu.Lock()
	for taskID, pt := range tc.pending {
		t, err := time.Parse(time.RFC3339, pt.movedAt)
		if err != nil {
			continue
		}
		if now.Sub(t) >= timeout {
			stale = append(stale, asyncDoneMsg{taskID: taskID, state: "timeout"})
		}
	}
	for callID, at := range tc.inflight {
		if now.Sub(at) >= timeout {
			staleInflight = append(staleInflight, callID)
			// 采集即**原子认领摘除**（对齐 pending 侧 onAsyncDone 的 delete）：否则同一 id 在兜底消息
			// 被 loop 消费（deliverInput → clearInflight）之前若再命中一次 sweep，会被重复兜底
			// （重复落库 / 多次续轮）。消费时的 clearInflight 对已摘除 id 幂等无操作。
			delete(tc.inflight, callID)
		}
	}
	tc.mu.Unlock()
	for _, m := range stale {
		logf("[chonkpilot-server] pending task %s 超时未回执（>%s）→ 按 failed 占位续轮\n", m.taskID, timeout)
		tc.onAsyncDone(m)
	}
	for _, callID := range staleInflight {
		logf("[chonkpilot-server] inflight %s 超时未回填（>%s）→ 按 failed 占位续轮\n", callID, timeout)
		tc.reapInflight(callID, timeout)
	}
}

// reapInflight 兜底回收一个超时未回填的 inflight（ask_user / llm_run 子作业）：
// 结果按 failed 占位落库并回喂 → deliverInput 消费时 clearInflight 摘除登记 → 闸门重开、续轮。
// 同时把该工具仍处于非终态的任务节点按 error 收口（ask 未应答 / 子作业未回填时节点悬挂）。
// 幂等：结果行按 tool_call_id 回填同一行、任务节点 done 幂等；若真实结果随后仍回填，未决 tool
// 结果经 unpersistedInputs 去重（不重复进上下文）。
func (tc *turnCtx) reapInflight(toolCallID string, timeout time.Duration) {
	if n := tc.server.tasks.findByToolCall(tc.req.InstanceID, toolCallID); n != nil && !terminalTaskState(n.State) {
		tc.server.tasks.done(n.TaskID, TaskStateError, "", "超时未回填")
	}
	msg := fmt.Sprintf("错误: 工具调用 %s 在 %s 内未收到结果（回执丢失 / 询问已过期 / 子作业未回填），已按失败占位续轮", toolCallID, timeout)
	tc.FeedToolResultStatus(toolCallID, msg, "failed")
}

// registerPending 登记「转异步挂起」（task_id → 发起信息）并同步 task_id → 本 turn 运行态索引
// （onGatewayTaskDone 定位用）。两步各自加锁（不嵌套 tc.mu 与 s.mu，避免与 onGatewayTaskDone 的
// 锁定序冲突）；即使两者之间被回执插入，onGatewayTaskDone 的索引未命中会回退全表扫描命中 pending
// → 不漏续轮。
func (tc *turnCtx) registerPending(taskID, toolCallID string) {
	tc.mu.Lock()
	tc.pending[taskID] = pendingTool{toolCallID: toolCallID, movedAt: time.Now().UTC().Format(time.RFC3339)}
	tc.mu.Unlock()
	tc.server.mu.Lock()
	if tc.server.taskTurnIndex != nil {
		tc.server.taskTurnIndex[instKey(tc.req.InstanceID, taskID)] = tc
	}
	tc.server.mu.Unlock()
}

// markInflight 登记一个已派发、结果异步回喂的 tool_call（ask_user / llm_run 子作业），
// 并记录派发时刻（看门狗超时回收用，见 reapStalePending）。
func (tc *turnCtx) markInflight(toolCallID string) {
	if toolCallID == "" {
		return
	}
	tc.mu.Lock()
	if tc.inflight == nil {
		tc.inflight = map[string]time.Time{}
	}
	tc.inflight[toolCallID] = time.Now()
	tc.mu.Unlock()
}

// clearInflight 摘除一个在飞 ask/子作业登记（工具结果被 loop 消费时调用；未知 id 幂等无操作）。
func (tc *turnCtx) clearInflight(toolCallID string) {
	if toolCallID == "" {
		return
	}
	tc.mu.Lock()
	delete(tc.inflight, toolCallID)
	tc.mu.Unlock()
}

// continueOpenLocked 报告续轮闸门是否已开：无在飞异步 pending、且无在飞 ask/子作业。
// **须持 tc.mu**。
func (tc *turnCtx) continueOpenLocked() bool {
	return len(tc.pending) == 0 && len(tc.inflight) == 0
}

// onAsyncDone 异步任务完成：落一条 role=tool 记录（call + async + result，一条记录承载
// 三段）→ 合并暂存的同步结果 → 一次喂回续轮（S4 场景 5）。
// 2026-09-18（用户决定）：结果取自保留的通知面 mcp-tasks-report（`result_summary` = 结果全文，
// 见 gateway reportTaskDone），**不再经 `tasks/result` 方法面**；落库后该记录即
// tool_result / 后续查询的读取源（message 表）。
func (tc *turnCtx) onAsyncDone(m asyncDoneMsg) {
	tc.mu.Lock()
	pt, ok := tc.pending[m.taskID]
	delete(tc.pending, m.taskID)
	tc.mu.Unlock()
	// 消费回执 → 摘除 task_id 运行态索引（onGatewayTaskDone 定位用；仅当仍指向本 turn）。
	tc.server.forgetPendingTaskIndex(tc.req.InstanceID, m.taskID, tc)
	if !ok {
		// 未知 / 重复回执（task_id 未登记或已完成消费，B-02）：直接返回——不构造
		// ToolCallID 为空串的畸形 role=tool 消息、不续轮（否则污染上下文）。
		logf("[chonkpilot-server] onAsyncDone: 未登记的 task_id %q（重复回执？）→ 忽略\n", m.taskID)
		return
	}

	status := "completed"
	resultText := m.result
	switch m.state {
	case "done":
		// 结果 = 通知面携带的终态全文（与旧 tasks/result 取回的文本同源）。
	case "cancelled":
		status = "cancelled"
		resultText = "任务结束(" + m.state + ")" // 保持既有文案（该状态下通知面摘要为取消摘要）
	default:
		status = "failed"
		resultText = "任务结束(" + m.state + ")" // 保持既有文案（错误详情由任务节点承载）
	}
	// 转异步：async 段记录 task_id + 转后台时间（一条记录，完成时落库，避免同一结果两条）。
	tc.persistToolResult(pt.toolCallID, resultText, status, &persist.ToolAsyncContent{
		TaskID: m.taskID, MovedAt: pt.movedAt,
	})

	// 本回执结果并入 stash（与在飞期间到达的同步结果一同暂存）；**仅当本 turn 已无在飞 pending**
	// 时才冲刷 stash 并续轮 —— 否则「带部分 tool 结果提前续轮」，后续回执的 tool 结果会与已续轮
	// 的 assistant.tool_calls 错配（工具结果未与 tool_calls 同批提交），破坏配对协议。
	tc.mu.Lock()
	tc.stash = append(tc.stash, ChatMsg{Role: "tool", ToolCallID: pt.toolCallID, Content: resultText})
	// 闸门 = 无在飞 pending **且** 无在飞 ask/子作业（后者由 deliverInput 在结果消费时摘除）——
	// 同一批混有 ask/llm_run 时，回执先到也不得提前续轮。
	if !tc.continueOpenLocked() {
		tc.mu.Unlock()
		return
	}
	msgs := append([]ChatMsg{}, tc.stash...)
	tc.stash = nil
	tc.mu.Unlock()
	tc.chatOnce(msgs...)
}

// llmTools 组装本轮回发给 LLM 的工具面（25 §4.3 白名单语义，T1 缺陷修正）：
//   - `agent.tools` **空（nil）** → 缺省下发面 = hot 集（`toolsForLLM`，含 meta 工具）；
//   - **非空** → 下发面 = **白名单里的工具（不论是否 hot）** —— 从**全量可见工具**（非 hot 过滤）
//     按暴露名取，修「白名单里的非 hot 工具被 hot 过滤吞掉（配了却永远传不到 LLM）」的既有缺陷。
//
// 名字口径 = LLM 工具面的**暴露名**（与 `toolAllowed` 同一名字空间）。
func (tc *turnCtx) llmTools() []ToolDef {
	if tc.allowedTools == nil {
		return tc.filterDeniedTools(tc.server.toolsForLLM(tc.req.InstanceID))
	}
	out := make([]ToolDef, 0, len(tc.allowedTools))
	for _, t := range tc.server.visibleTools(tc.req.InstanceID) {
		if _, ok := tc.allowedTools[t.Name]; ok && !tc.toolDenied(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// filterDeniedTools 从下发面剔除本轮**硬拒**工具（denyTools 空 → 原样返回，零拷贝）。
func (tc *turnCtx) filterDeniedTools(in []ToolDef) []ToolDef {
	if len(tc.denyTools) == 0 {
		return in
	}
	out := make([]ToolDef, 0, len(in))
	for _, t := range in {
		if !tc.toolDenied(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// toolDenied 判定该工具名是否被本轮**硬拒**（调用面收窄，见 turnCtx.denyTools）。名字口径 = LLM
// 工具面**暴露名**（`self_*` 前缀）或契约名（剥前缀）皆可命中；空集 → 恒 false。
func (tc *turnCtx) toolDenied(name string) bool {
	if len(tc.denyTools) == 0 {
		return false
	}
	if _, ok := tc.denyTools[name]; ok {
		return true
	}
	_, ok := tc.denyTools[strings.TrimPrefix(name, "self_")]
	return ok
}

// toolAllowed 判定本轮是否允许调用该工具（AG-C4 白名单语义；**调用处硬拒** G-45 ③）：
// allowedTools 为空（agent 未配 tools / 配 `[]` / 解析不到名字）= **不限制**（全量放行）；
// 非空 = 仅白名单内。名字口径 = LLM 工具面的**暴露名**（与 llmTools 过滤同一名字空间）。
func (tc *turnCtx) toolAllowed(name string) bool {
	if tc.toolDenied(name) {
		return false // 硬拒（work_session 排除 subchat_finish 等）先于白名单判定
	}
	if tc.allowedTools == nil {
		return true
	}
	_, ok := tc.allowedTools[name]
	return ok
}

// filterWhitelistByLevel 按**场景级别的可用工具级别矩阵**过滤白名单（P4 2026-10-01，25 §4）：
// 保留**在场景级别可用集合内**（同级或更高级）**且在本实例可见工具面内**的名字；
// 越权（级别不在矩阵里）/ 不存在（不在可见工具面）的名字**静默剔除**（不报错、不中断）。
//
// 语义边界（不得影响既有行为）：
//   - 入参 raw 非 nil（调用方已判空白名单 = nil）→ 只做**收窄**，不回退"不限制"；
//   - 场景级别无法判定（scenarioLevel 空）→ **原样返回**（保守放行，不误剔除）；
//   - **可见工具缓存为空（冷缓存 / 未预热）→ 原样返回**（放行，见下）；
//   - 工具级别无法判定（第三方 / 无 _meta.server，LevelOfNode 空）→ **放行**（不误剔除）。
//
// 冷缓存退化放行（2026-10-01）：本函数以「本实例可见工具面」为收窄基准，但该工具面来自
// gateway `tools/list` 缓存（visibleTools → gc.ToolsFor）——**缓存未预热（空）时**若照旧按
// "只保留可见集内名字"处理，会把白名单里的**合法工具全部误剔**（冷启动 / 实例刚注册尚未刷新
// 时，本轮下发面反而比场景级别矩阵更窄）。故可见集为空 → 视为"无法判定可见面"→ 原样返回
// （退化为保守放行的纯级别判定，行为与"缓存不可用"一致，不误剔）。
//
// 矩阵单源 = persist.AgentToolLevels / LevelAllowed（= capfs；与前端 AGENT_LEVEL_MATRIX 逐字一致）。
func (s *Server) filterWhitelistByLevel(instance, scenarioLevel string, raw map[string]struct{}) map[string]struct{} {
	if scenarioLevel == "" {
		return raw
	}
	visible := s.visibleTools(instance)
	if len(visible) == 0 {
		return raw // 冷缓存（可见工具面空）→ 放行，不误剔
	}
	out := make(map[string]struct{}, len(raw))
	for _, t := range visible {
		if _, ok := raw[t.Name]; !ok {
			continue
		}
		lvl := toolNodeLevel(t)
		if lvl != "" && !persist.LevelAllowed(scenarioLevel, lvl) {
			continue // 越权 → 静默剔除
		}
		out[t.Name] = struct{}{}
	}
	return out
}

// toolNodeLevel 取工具的 capability 级别（`_meta.server.node` → capfs.LevelOfNode）；
// 无 `_meta.server` / 无法判定 → ""（调用方放行）。
func toolNodeLevel(t ToolDef) string {
	srv, _ := t.Meta["server"].(map[string]any)
	node, _ := srv["node"].(string)
	return persist.LevelOfNode(node)
}

// chatOnce 执行一轮 LLM 调用并处理输出（reason/text/tool-call → gateway → 回喂）。
// inputs 是本次进入会话的新消息（用户文本 / 聚合的工具结果）。
func (tc *turnCtx) chatOnce(inputs ...ChatMsg) {
	if tc.iterations >= tc.maxToolIterations {
		tc.server.llmError(tc, "TOOL_LOOP_LIMIT", "工具调用次数超限")
		tc.Close()
		return
	}
	tc.iterations++
	for _, in := range inputs {
		if in.Role == "user" {
			if err := tc.persistMessage(in); err != nil {
				tc.server.llmError(tc, "DB_ERROR", err.Error())
				tc.Close()
				return
			}
		}
	}
	base := tc.msgs()
	msgs := append(base, tc.unpersistedInputs(inputs, base)...)

	// LLM 调用 + 无内容失败重发（方式 A，S6）：Chat 失败或流错误且无内容 → 重发同一份上下文
	var out strings.Builder
	var think strings.Builder // 思维链累积（reasoning_content 增量 → 落库 reasoning）
	var calls []ToolCall
	finish := ""
	var streamErr error
	attempt := 0
	tc.retryAttempt = 0 // RETRY-1：重试计数按「本次 LLM 调用」重置（终态/中间态均取该值）
	// assistant 增量落库（I-176）：流式过程中按「内容段」落库——段边界 = 思维链→正文→工具调用的
	// 类型切换，以及流结束；每次写到**同一行**（asstKey 回填），非每 token。无可重放内容
	//（正文 / 工具调用皆空）时不落，避免产生会被 applyReasoningRule 清空的空 assistant 行。
	asstKey := ""
	seg := ""
	flushAssistant := func() {
		if out.Len() == 0 && len(calls) == 0 {
			return
		}
		key, _ := tc.persistMessageKeyed(ChatMsg{Role: "assistant", Content: out.String(), ToolCalls: calls, Reasoning: think.String()}, asstKey)
		if key != "" {
			asstKey = key
		}
	}
	for {
		out.Reset()
		think.Reset()
		calls = nil
		finish = ""
		streamErr = nil
		events, err := tc.server.chat(tc.ctx, tc.llmSpec, msgs, tc.llmTools(), ChatOptions{
			Model:           tc.llmModel,
			Think:           tc.llmThink,
			Effort:          tc.llmEffort,
			Temperature:     tc.llmTemperature,
			MaxTokens:       tc.llmMaxOutputToken,
			ResponseTimeout: tc.responseTimeout,
			StreamTimeout:   tc.streamTimeout,
			// 图片（P2-8）：用户消息里的 `![名](dir/名)` 引用（相对数据根）→ 图片内容块（数据根受限读取）。
			Images: ImageOptions{DataRoot: imageDataRoot(tc.req.DataDir, tc.req.WorkDir)},
		})
		if err != nil {
			if tc.ctx.Err() != nil {
				return // 取消静默（llm-cancel 已发 interrupted complete）
			}
			if attempt < tc.retryCount && retryableErr(err) && probeBeforeRetry(tc.llmSpec.BaseURL) {
				attempt++
				tc.emitRetry(err, attempt)
				continue
			}
			// 真实分类（S21）：超时/网络/429/5xx → retryable=true（前端静默续写）；
			// 协议/鉴权（401/403）等 → false（前端显示错误气泡 + 手动继续）。
			// 子会话 + 「压缩子会话」关闭：上下文超限的可诊断提示（DSL-4，42 §2 (253) ⑤）。
			retryable := retryableErr(err)
			tc.server.llmErrorRetryable(tc, "LLM_REQUEST_FAILED", tc.server.subsessionHint(tc, err.Error(), retryable), retryable)
			tc.Close()
			return
		}
		for ev := range events {
			if ev.Err != nil {
				streamErr = ev.Err
				// 显式排空：生产端（forwardRouterEvents）可能仍阻塞在 `out <-`；此处 break 后
				// 不再读取会使其永久阻塞、defer close(out) 不执行（goroutine 泄漏）→ 读到通道关闭为止。
				for range events {
				}
				break
			}
			if ev.Reasoning != "" {
				if seg != "reason" { // 段切换 → 落已累积内容（I-176）
					flushAssistant()
					seg = "reason"
				}
				think.WriteString(ev.Reasoning)
				tc.server.notify(tc, NotifyTypeReason, map[string]any{"text": ev.Reasoning})
			}
			if ev.Content != "" {
				if seg != "content" {
					flushAssistant()
					seg = "content"
				}
				out.WriteString(ev.Content)
				tc.server.notify(tc, NotifyTypeText, map[string]any{"text": ev.Content})
			}
			if len(ev.ToolCalls) > 0 {
				if seg != "tool" {
					flushAssistant()
					seg = "tool"
				}
				calls = ev.ToolCalls
			}
			if ev.Done {
				finish = ev.FinishReason
			}
		}
		if streamErr != nil {
			if tc.ctx.Err() != nil {
				return
			}
			if out.Len() == 0 && attempt < tc.retryCount && retryableErr(streamErr) && probeBeforeRetry(tc.llmSpec.BaseURL) {
				attempt++
				tc.emitRetry(streamErr, attempt)
				continue
			}
		}
		break
	}

	// 空回复（stop 但无内容）：不落库，报错误（EMPTY_REPLY，61-消息一览 错误随 llm-complete 携带）
	if streamErr == nil && len(calls) == 0 && strings.TrimSpace(out.String()) == "" {
		tc.server.llmError(tc, "EMPTY_REPLY", "LLM 返回空回复")
		tc.Close()
		return
	}

	// 断链恢复（S6/S8，方式 B 同 turn 变体）：已有部分内容 → resumePartial 落库半截 + 内部续写。
	if streamErr != nil {
		tc.resumePartial(asstKey, out.String(), think.String(), calls, streamErr)
		return
	}

	// 落库 assistant 消息（含 tool_calls / reasoning；reasoning 落库供后续轮次按协议回传）。
	// 流式过程中已按「段」落过（asstKey 非空）→ 就地回填同一行；否则此处首次落库（I-176）。
	flushAssistant()

	// 轮次完成：无工具请求（stop/length 等）才发 llm-complete；
	// tool-call 的 eof（finish_reason=tool_calls / 解析出 tool_calls）不发 complete，进入工具循环。
	if len(calls) == 0 {
		// 自动续写（S6）：length 截断 → 落库 + 内部续写（Kind=continue，有上限）；耗尽 → incomplete
		if finish == "length" && out.Len() > 0 && tc.continues < maxAutoContinue {
			tc.answer.WriteString(out.String()) // 跨段累积本段正文（续写终态取全文）
			tc.continues++
			tc.feed(ChatMsg{Role: "user", Kind: assembleContinueKind, Content: assembleContinueText})
			return
		}
		status := "complete"
		if finish == "length" {
			status = "incomplete"
		}
		tc.answer.WriteString(out.String()) // 累积本段（单段场景 = 旧行为；多段续写 = 全文）
		// finish 内落库 + 写快照 + llm-compress；text 取**累积全文**（跨段续写不丢前段正文）。
		tc.server.complete(tc, status, finish, tc.answer.String())
		tc.Close()
		return
	}
	// 工具调用：逐个调 gateway（上下文带 tool-call-id），结果**聚合后一次喂回**（OpenAI 协议要求
	// tool 结果与 assistant.tool_calls 同批提交）；ask_user 结果经 ask-user-reply 异步回喂；
	// llm 型（llm_run）→ server 开子会话执行（独立 goroutine）；
	// 转异步（pending{task_id}）→ 登记挂起，turn 等 gateway 回执续轮（S4）。
	// 任务编排（§6.6）：每工具建任务节点（kind=工具/llm/ask_user；parent_id 挂父节点，
	// top_session 归属）→ 广播 tasks.started；同步工具短生命周期（started → done）。
	var tools []ChatMsg
	for _, call := range calls {
		name := call.Function.Name
		args := parseArgs(call.Function.Arguments)
		// 该工具自身的 _meta（gateway tools/list 缓存，已预热；I-60）：随 tool-call 通知与
		// role=tool 消息落库推前端，使前端无需依赖一次性 mcp-tools-timeout 事件即可本地判断
		// 裁决项（manual→detach/cancel；never→wait/cancel）。
		toolMeta := tc.server.gc.ToolMeta(name)
		notifyPayload := map[string]any{
			"tool_call_id": call.ID, "tool": name, "arguments": args,
		}
		if len(toolMeta) > 0 {
			notifyPayload["_meta"] = toolMeta
		}
		tc.server.notify(tc, NotifyTypeToolCall, notifyPayload)

		// gateway 聚合后暴露名带能力源前缀（self_*，self 节点 entry.ID 前缀）；分类按契约名
		// （剥前缀），gateway 调用仍用暴露名（route 以暴露名注册）——任务节点归属单源。
		canon := strings.TrimPrefix(name, "self_")

		// 白名单**调用处硬拒**（G-45 ③ / 42 §2 (165)）：下发面过滤（llmTools）挡不住模型幻觉
		// 调用未列工具 —— 此处按本轮 agent 的 `tools` 白名单在**调用处分发前**拒绝，不落 gateway
		// （不真正执行）；结果以失败文本回喂，使 LLM 改用白名单内工具。空白名单 = 不限制（AG-C4）。
		if !tc.toolAllowed(name) {
			errText := fmt.Sprintf("错误: 工具 %q 不在本轮 agent 的工具白名单内，已拒绝执行（可用工具见本轮 tools 参数；白名单 = 场景 agent 的「工具过滤」配置）", name)
			tc.persistToolResult(call.ID, errText, "failed", nil)
			tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: errText})
			continue
		}

		// 白名单**旁路加固**（项 5a，2026-09-25）：白名单是「调用面」过滤而非沙箱 —— 若
		// mcp_invoke 本身在白名单内，LLM 可经它转调**任意**工具绕过白名单。故白名单非空时，
		// mcp_invoke 的**目标工具**也必须落在同一白名单内；否则同样在分发前拒绝、不落 gateway
		// （不真正调用）。判白口径 = 目标名（gateway 暴露名，与白名单 / toolAllowed 同一名字
		// 空间，findFor 亦按该名路由）。目标名缺失/非法 → 不在此改写语义，交由 gateway 既有校验。
		if isMetaInvokeTool(canon) && tc.allowedTools != nil {
			if target := strings.TrimSpace(str(args["name"])); target != "" && !tc.toolAllowed(target) {
				errText := fmt.Sprintf("错误: mcp_invoke 的目标工具 %q 不在本轮 agent 的工具白名单内，已拒绝执行（白名单 = 场景 agent 的「工具过滤」配置；白名单内工具可直接调用）", target)
				tc.persistToolResult(call.ID, errText, "failed", nil)
				tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: errText})
				continue
			}
		}

		// 发起即落 running（I-176）：先落一条非终态 role=tool 行，终态由 persistToolResult
		// 回填**同一行**——使进程重启后 cleanupStaleToolPairs 能把遗留 pair 标 interrupted
		//（前端"已中断 + 可重试"生效），且同一调用恒只一行（不破坏 LLM 重放/前端卡片）。
		tc.persistToolRunning(call.ID, name, args)

		if isTaskTool(canon) {
			// task 型域工具（tool_stop/tool_result）：经 gateway 唯一执行入口
			// （21-llm-server）——内嵌 mcp-server 的域工具 handler 执行并返回文本，
			// 任务节点由 handler 建/广播（对齐 21-llm-server 任务树）。结果同步喂回本 turn。
			topSession, _ := tc.server.taskNodeBase(tc) // 树归属随调用上下文下发（I-90）
			ctxMsg := mcpgateway.Context{
				WorkDir:    tc.req.WorkDir,
				DataDir:    tc.req.DataDir,
				Session:    tc.req.Session,
				Turn:       tc.req.Turn,
				InstanceID: tc.req.InstanceID,
				ToolCallID: call.ID,
				TopSession: topSession,
			}
			// A5：非主会话强制同步（gatewayArgs 附调用级 async=never；主会话原样）。
			res, _, err := tc.server.gc.Call(tc.ctx, name, tc.gatewayArgs(args), ctxMsg)
			if err != nil {
				errText := "错误: " + err.Error()
				tc.persistToolResult(call.ID, errText, "failed", nil)
				tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: errText})
				continue
			}
			text := resultTextFromMap(res)
			tc.persistToolResult(call.ID, text, "completed", nil)
			tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: text})
			continue
		}

		// 树归属：子会话内节点挂该子会话的 kind=llm 节点之下（top_session 归主会话聚合根）；
		// 主会话节点仍为顶层（parent_id 空）。见 taskNodeBase。
		topSession, parentID := tc.server.taskNodeBase(tc)
		node := &TaskNode{
			Tool:       name,
			ToolCallID: call.ID,
			ParentID:   parentID,
			TopSession: topSession,
			// kind 按**契约名**（canon，剥 self_ 前缀，与 domainNode 同源）判定：
			// 暴露名 self_llm_run 不匹配 isLLMTool → 根节点会误落 tool（G-15）。
			Kind:       taskKindOf(canon),
			SessionID:  tc.req.Session,
			TurnID:     tc.req.Turn,
			InstanceID: tc.req.InstanceID,
		}
		// 标题取值用**契约名**（canon，剥 self_ 前缀，与 domainNode 同源）：注入展示名优先，
		// 无注入回退契约工具名（I-38；避免回退成网关暴露名 self_<契约名>）。
		node.Name, node.Purpose, node.Simplified = taskDisplay(canon, args)
		node.args = args // 保存参数（tool-retry 重跑用）
		if isAskTool(canon) {
			node.Args = args // 展示用（I-50）：ask 追问内容/选项随 tasks.* 广播给前端任务详情
		}
		node, serr := tc.server.tasks.start(node) // 广播 tasks.started
		if serr != nil {
			errText := "错误: " + serr.Error()
			tc.persistToolResult(call.ID, errText, "failed", nil)
			tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: errText})
			continue
		}

		if isAskTool(canon) {
			// ask_user → server ask 通道（广播 ask-user → 等 ask-user-reply → 回填，§6.4）
			// 登记在飞（结果经 ask-user-reply 异步回喂）→ 续轮闸门须计入（见 deliverInput）。
			tc.markInflight(call.ID)
			tc.server.ask(tc.req.Turn, call.ID, args, node.TaskID)
			continue
		}
		if isLLMTool(canon) {
			// llm 型（llm_run）→ server 开子 session/turn → 完成回填父轮次（节点 kind=llm）
			// 登记在飞（结果经 runSubJob 异步回喂）→ 续轮闸门须计入。
			tc.markInflight(call.ID)
			go tc.server.runSubJob(tc, call.ID, node, args)
			continue
		}
		ctxMsg := mcpgateway.Context{
			WorkDir:    tc.req.WorkDir,
			DataDir:    tc.req.DataDir,
			Session:    tc.req.Session,
			Turn:       tc.req.Turn,
			InstanceID: tc.req.InstanceID,
			ToolCallID: call.ID,
			// I-90 增补：树归属随调用上下文下发 → gateway 随 mcp-tasks-report 回传 →
			// 任务层在「未登记」时仍能为该执行**独立建节点**（parent = 本调用的 llm 侧节点，
			// 由调用层给出；层不猜不重排 —— 21 §4-2）。
			TopSession: topSession,
			Parent:     node.TaskID,
		}
		// 主路径工具调用**不设硬 deadline**（评估结论，2026-10-11）：本调用沿 tc.ctx（仅 WithCancel，
		// 无 deadline）出网，与 onToolRetry 的 15s 重试超时**口径不同、有意为止**——
		//   - 异步工具（gateway 契约声明 async / 超阈值转后台）经 gwClient.Call 立即返回 pending +
		//     task_id（见下方 taskID 分支），turn 挂起等回执，**不阻塞在单次 Call**，无需超时；
		//   - 同步长时工具（read/build/… 无异步能力者）会真实阻塞在本 Call 直至 gateway 完成——
		//     加硬上限会**误杀**长时工具（破坏功能），故不加；
		//   - 收敛途径：用户停止（llm-cancel → tc.cancel）或实例退出（exitInstance → tc.Close →
		//     cancel）会取消 tc.ctx，Call 随 ctx 取消返回；上游 gateway 自身的执行超时策略另行兜底。
		// A5：非主会话强制同步（gatewayArgs 附调用级 async=never → 工具不得转后台；主会话原样）。
		res, taskID, err := tc.server.gc.Call(tc.ctx, name, tc.gatewayArgs(args), ctxMsg)
		if err != nil {
			errText := "错误: " + err.Error()
			tc.persistToolResult(call.ID, errText, "failed", nil)
			tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: errText})
			tc.server.tasks.done(node.TaskID, TaskStateError, "", err.Error())
			continue
		}
		if taskID != "" {
			// 转异步：登记 task_id → 发起信息（tool-call-id + 转后台时间）；任务节点挂 gateway
			// 任务，turn 挂起等回执；结果在 onAsyncDone 落一条 role=tool（含 async 段）。
			// 同步登记 task_id → 本 turn 运行态索引（onGatewayTaskDone 免全表扫描定位）。
			tc.registerPending(taskID, call.ID)
			tc.server.tasks.setGwTask(node.TaskID, taskID)
			tc.server.tasks.setState(node.TaskID, TaskStatePending)
			continue
		}
		text := resultTextFromMap(res)
		if resultStatus(res) == "cancelled" {
			// 超时裁决「取消」的同步返回（gateway 结构化标记）：任务节点落 cancelled（而非 done，
			// I-62）；结果照常喂回续轮（LLM 需感知该工具已取消）。done 幂等：若回报路径已先落终态，
			// 此处不重复广播、也不把 cancelled 覆盖成 done。
			tc.persistToolResult(call.ID, text, "cancelled", nil)
			tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: text})
			tc.server.tasks.done(node.TaskID, TaskStateCancelled, "", text)
			continue
		}
		tc.persistToolResult(call.ID, text, "completed", nil)
		tools = append(tools, ChatMsg{Role: "tool", ToolCallID: call.ID, Content: text})
		tc.server.tasks.done(node.TaskID, TaskStateDone, text, "")
	}
	// 续轮闸门（P2）：有在飞（异步 pending / ask / 子作业）→ 本批同步结果暂存，等最后一项
	// 到齐时合并喂回（onAsyncDone / deliverInput）；闸门已开 → 同步递归续轮（结果一次性提交）。
	tc.mu.Lock()
	if !tc.continueOpenLocked() {
		tc.stash = append(tc.stash, tools...)
		tc.mu.Unlock()
		return
	}
	tc.mu.Unlock()
	if len(tools) > 0 {
		tc.chatOnce(tools...)
	}
}

// resumePartial 进程内断链续写（S6/S8，方式 B 的**同 turn 变体**）：一次 LLM 流读取被中断
// （streamErr != nil）时的恢复动作。
//   - 断开前**已收到部分内容**（content 非空）且未达自动续写上限（`maxAutoContinue`）→
//     落库半截 assistant（含 tool_calls / reasoning）+ 注入「继续」（Kind=continue，非新 turn 边界）
//     续写同一 turn —— 续写由 loop 的下一轮 chatOnce 承接；
//   - 否则（无内容失败 / 续写已耗尽）→ 报 `LLM_STREAM_ERROR`（携带真实分类 retryable，超时/网络/
//     429/5xx → true 由后端自动续写；协议/鉴权 → false 由前端手动「重试」）并终结本 turn。
//
// 断链发生在「工具调用已解析、尚未执行」时（`len(calls)>0`，ev.Err 紧随 tool_calls 到达）：这些
// tool_calls 未执行、无对应 tool 结果 → **丢弃**（不随 assistant.tool_calls 落库，避免产生
// 「assistant.tool_calls 无对应 tool 消息」的非法历史）且此情形**不注入「继续」**（工具已丢、
// 续写无意义）——仅保留本次已收正文后按断链错误终结本 turn。
//
// 说明：**不重发**（已收内容重发会重复输出）；本函数与既有内联逻辑逐字等价，仅抽取命名。
// asstKey 非空 = 流式过程中已按段落过该行 → 就地回填同一行（I-176）。
func (tc *turnCtx) resumePartial(asstKey, content, reasoning string, calls []ToolCall, streamErr error) {
	if len(calls) > 0 {
		// 工具调用中间断：丢弃未完成的 tool_calls（不落库、不注入「继续」），仅保留已收正文后终结。
		if content != "" {
			tc.answer.WriteString(content)
			_, _ = tc.persistMessageKeyed(ChatMsg{Role: "assistant", Content: content, Reasoning: reasoning}, asstKey)
		}
		retryable := retryableErr(streamErr)
		tc.server.llmErrorRetryable(tc, "LLM_STREAM_ERROR", tc.server.subsessionHint(tc, streamErr.Error(), retryable), retryable)
		tc.Close()
		return
	}
	if content != "" && tc.continues < maxAutoContinue {
		tc.answer.WriteString(content) // 跨段累积本段正文（断链续写终态取全文）
		tc.continues++
		_, _ = tc.persistMessageKeyed(ChatMsg{Role: "assistant", Content: content, ToolCalls: calls, Reasoning: reasoning}, asstKey)
		tc.feed(ChatMsg{Role: "user", Kind: assembleContinueKind, Content: assembleContinueText})
		return
	}
	// 续写次数耗尽（continues >= maxAutoContinue）时末段正文尚未落库、也未计入 answer（B-36）：
	// 补一次同口径落库（asstKey 口），否则与已落库前几段拼接后正文被截尾；终态取 answer，
	// 故同步累积。仅当本段有内容（content != ""）才补——无内容失败无末段可丢。
	if content != "" {
		tc.answer.WriteString(content)
		_, _ = tc.persistMessageKeyed(ChatMsg{Role: "assistant", Content: content, ToolCalls: calls, Reasoning: reasoning}, asstKey)
	}
	retryable := retryableErr(streamErr)
	tc.server.llmErrorRetryable(tc, "LLM_STREAM_ERROR", tc.server.subsessionHint(tc, streamErr.Error(), retryable), retryable)
	tc.Close()
}

// msgs 返回当前轮次 LLM 上下文：会话历史（hist，含摘要）+ 当前 turn 已落消息（created_at 升序）。
// 全量历史按 **三段结构**内置拼接（口径 X，2026-09-25）：完整区（受 `keep_full_max_turns` 轮数 +
// `keep_full_max_tokens` 完整态 token + `compress_token_threshold` 简化区预算三条件约束，与压缩侧
// 同一算法 `data.LocateZones`）整轮原文；简化区/摘要区残留 → **仅 text**（`data.BriefMessages`）；
// 被重试/恢复的 turn 强制全量（tc.forceFull）。**预存 token 优先**（tc.turnTokens，尾部对齐，
// 缺值回退实时估算）→ 生产判定真正读取预存值、不再对已预存轮重算。
// 当前 turn 恒为最后一段 → 恒全量（工具循环续轮需要完整原文；简化投影只作用于更早的 turn）。
// 快照（tc.hist）不动。
// 最后按协议规则（applyReasoningRule）决定 reasoning 回传：仅"带 tool_calls 的 assistant"
// 保留 reasoning（线格式 reasoning_content），其余清空。
// 最前拼**记忆库带出指引**（42 §2 (27)，memoryGuide）：按当前场景到记忆目录按需读取相关记忆，
// 不注入记忆全文；该指引只随本次 LLM 请求携带，不写入 hist / 快照（避免逐轮累积）。
func (tc *turnCtx) msgs() []ChatMsg {
	cur := newSessionStore(tc.server.bus, tc.req.InstanceID).LoadMessages(tc.req.Turn)
	all := append(append([]ChatMsg{}, tc.hist...), cur...)
	storedFull, storedBrief := splitTurnTokens(tc.turnTokens)
	msgs := assembleTurns(all, tc.keepFullTurns, tc.keepFullTokens, tc.briefBudget, storedFull, storedBrief, tc.forceFull)
	msgs = applyReasoningRule(msgs)
	// 指引只读一次（单轮内 memory/asset 两条指引不变）：msgs() 每轮工具迭代都会调用，缓存避免
	// 重复读配置。拼接顺序与逐次读取逐字一致：先记忆后资产（先后 prepend → 最终 [资产, 记忆, ...]）。
	tc.guideOnce.Do(func() {
		tc.memGuideText = tc.server.memoryGuide(tc.req.InstanceID, tc.req.WorkDir)
		tc.assetGuideText = tc.server.assetGuide(tc.req.InstanceID)
	})
	if tc.memGuideText != "" {
		msgs = append([]ChatMsg{{Role: "system", Content: tc.memGuideText}}, msgs...)
	}
	// 知识库资产检索指引（RB-7，2026-09-21）：与记忆指引并列；门控见 assetGuide（知识库未接入 →
	// 空串不注入）。两条路径的区别在指引正文里写明（记忆 = 文件按绝对路径读；资产 = mcp_find + mcp_load）。
	if tc.assetGuideText != "" {
		msgs = append([]ChatMsg{{Role: "system", Content: tc.assetGuideText}}, msgs...)
	}
	return msgs
}

// splitTurnTokens 把伴随数组 turn_tokens 拆成**升序**的完整态 / 简化态 token 两数组
// （供组装侧三段定位尾部对齐取用；缺值位留 0 = 缺值 → 调用方回退实时估算）。
func splitTurnTokens(tokens []facade.TurnToken) (full, brief []int) {
	if len(tokens) == 0 {
		return nil, nil
	}
	full = make([]int, len(tokens))
	brief = make([]int, len(tokens))
	for i, t := range tokens {
		if t.Full != nil {
			full[i] = *t.Full
		}
		if t.Brief != nil {
			brief[i] = *t.Brief
		}
	}
	return full, brief
}

// persistMessage 落库 user/assistant 消息；走 AppendFullKeyed：完整保留 Kind / tool_calls /
// reasoning，供快照组装、压缩定位与协议回传。（role=tool 结果经 persistToolResult 落库。）
func (tc *turnCtx) persistMessage(msg ChatMsg) error {
	_, err := tc.persistMessageKeyed(msg, "")
	return err
}

// persistMessageKeyed 落库 user/assistant 消息；key 非空 = 就地更新该行（assistant 增量落库
// 回填同一行，I-176），空 = 新键。返回落库主键（供后续回填复用）。
func (tc *turnCtx) persistMessageKeyed(msg ChatMsg, key string) (string, error) {
	store := newSessionStore(tc.server.bus, tc.req.InstanceID)
	return store.AppendFullKeyed(tc.req.Turn, msg, key)
}

// toolCallInfo 在当前轮已落库消息中按 tool_call_id 反查工具名与参数（role=tool 的 call 段）。
func (tc *turnCtx) toolCallInfo(toolCallID string) (string, any) {
	if toolCallID == "" {
		return "", nil
	}
	msgs := newSessionStore(tc.server.bus, tc.req.InstanceID).LoadMessages(tc.req.Turn)
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, c := range m.ToolCalls {
			if c.ID == toolCallID {
				return c.Function.Name, parseArgs(c.Function.Arguments)
			}
		}
	}
	return "", nil
}

// persistToolRunning 发起即落一条非终态（running）role=tool 行（I-176）：content 仅含 call 段，
// 终态由 persistToolResult 回填**同一行**（主键登记于 tc.toolMsgKeys，供终态/重试回填复用）。
// 落库失败静默（不阻断执行）：此时终态仍按新键落一条完整记录（退化为旧行为）。
func (tc *turnCtx) persistToolRunning(toolCallID, name string, args map[string]any) {
	content := persist.ToolContent{
		Call: &persist.ToolCallContent{ToolCallID: toolCallID, Name: name, Arguments: args},
	}
	b, _ := json.Marshal(content)
	msg := map[string]any{
		"role":             "tool",
		"content":          string(b),
		"tool_call_id":     toolCallID,
		"tool_call_status": persist.ToolStatusRunning,
	}
	if meta := tc.server.gc.ToolMeta(name); len(meta) > 0 {
		msg["_meta"] = meta
	}
	id, err := newSessionStore(tc.server.bus, tc.req.InstanceID).AppendMsgMap(tc.req.Turn, msg, "")
	if err != nil || id == "" {
		// B-42：落库失败仅留痕（不阻断执行，语义不变；与终态侧 B-33 对称）。
		logf("[chonkpilot-server] 工具 running 落库失败（turn=%s tool_call_id=%s）: err=%v id=%q\n",
			tc.req.Turn, toolCallID, err, id)
		return
	}
	tc.mu.Lock()
	if tc.toolMsgKeys == nil {
		tc.toolMsgKeys = map[string]string{}
	}
	if tc.toolCallMeta == nil {
		tc.toolCallMeta = map[string]toolCallMeta{}
	}
	tc.toolMsgKeys[toolCallID] = id
	tc.toolCallMeta[toolCallID] = toolCallMeta{name: name, args: args}
	tc.mu.Unlock()
}

// persistToolResult 落一条 role=tool 消息（content = {call,result,async}）：
//   - call   = 发起（tool_call_id + 工具名 + 参数）；
//   - result = 结束（结果文本 + 状态）；
//   - async  = 转异步（非异步传 nil；转异步传 {task_id, moved_at}）。
//
// 一次工具调用只落**一条**记录（I-176）：发起时已落 running 行（persistToolRunning）→ 此处带
// 该行主键**就地回填**（recovered 轮次无内存主键时由 data 面按 (turn_id, tool_call_id) 复用）；
// 未落过 running（白名单拒绝 / 落库失败）→ 新键落一条完整记录。status 与 tool_call_status 同源。
func (tc *turnCtx) persistToolResult(toolCallID, result, status string, async *persist.ToolAsyncContent) {
	// 发起信息（工具名/参数）优先取本进程内存登记（persistToolRunning 时写入，B-05）：
	// 正常流程零额外读库；仅恢复轮次等未登记路径回落库内反查（toolCallInfo）。
	tc.mu.Lock()
	tcm, hasMeta := tc.toolCallMeta[toolCallID]
	key := tc.toolMsgKeys[toolCallID]
	tc.mu.Unlock()
	var (
		name string
		args any
	)
	if hasMeta {
		name, args = tcm.name, tcm.args
	} else {
		name, args = tc.toolCallInfo(toolCallID)
	}
	content := persist.ToolContent{
		Call:   &persist.ToolCallContent{ToolCallID: toolCallID, Name: name, Arguments: args},
		Result: &persist.ToolResultContent{Content: result, Status: status},
		Async:  async,
	}
	b, _ := json.Marshal(content)
	msg := map[string]any{
		"role":             "tool",
		"content":          string(b),
		"tool_call_id":     toolCallID,
		"tool_call_status": status,
	}
	// _meta：该工具自身的 meta 子集（gateway tools/list 缓存原文；I-60），随消息落库并回带
	// 前端（data-session-history / load-messages），供前端本地判断超时裁决项。
	if meta := tc.server.gc.ToolMeta(name); len(meta) > 0 {
		msg["_meta"] = meta
	}
	// 工具终态结果落库失败仅留痕（B-33）：该结果仍会喂给 LLM，但 DB（message 表）缺此条时，
	// 重载/重启后上下文出现「助理发起 tool_call 但无 tool 结果」断链；返回值语义不变。
	if _, err := newSessionStore(tc.server.bus, tc.req.InstanceID).AppendMsgMap(tc.req.Turn, msg, key); err != nil {
		logf("[chonkpilot-server] 工具结果落库失败（turn=%s key=%s）: %v\n", tc.req.Turn, key, err)
	} else {
		// 成功落库 → 登记硬判据（unpersistedInputs 据此剔除重复输入，见 I-25）。
		tc.mu.Lock()
		if tc.persistedResultOK == nil {
			tc.persistedResultOK = map[string]bool{}
		}
		tc.persistedResultOK[toolCallID] = true
		tc.mu.Unlock()
	}
}

// unpersistedInputs 过滤本次进入会话、尚未由 msgs() 从库带回的新消息（I-25 去重）：
//   - user 消息（含工具通知）：在 chatOnce 前置落库，msgs() 会从库带回，直接叠加会让同一条
//     在请求里出现两次（只按"已落库即由库带回"这一事实判定，不按内容匹配，故不会误吞与新提问
//     同文的历史消息）；
//   - role=tool 结果：硬判据 = 「本 turn **成功落库**该 tool_call_id 的结果（tc.persistedResultOK，
//     persistToolResult 成功时登记）**且** base（msgs() 从库带回）确实含该 id」——二者兼备 → 库已带回
//     → 剔除；否则（落库失败 / 读库失败致 base 缺该 id）→ 保留内存结果，避免丢结果（B-33）。
//     仅当本进程无该记录（如恢复轮次）时，退回
//     「库中同 id 行正文**非空**且与本次一致」的保守判据（正文为空可能是 running 占位，
//     不据此剔除，避免误吞真实空结果；正文一致时剔除，避免重复叠加）。
func (tc *turnCtx) unpersistedInputs(inputs, base []ChatMsg) []ChatMsg {
	need := false
	for _, in := range inputs {
		if in.Role == "tool" {
			need = true
			break
		}
	}
	baseContent := map[string]string{}
	if need {
		for _, m := range base {
			if m.Role == "tool" {
				baseContent[m.ToolCallID] = m.Content
			}
		}
	}
	ok := map[string]bool{}
	if need {
		tc.mu.Lock()
		for k, v := range tc.persistedResultOK {
			ok[k] = v
		}
		tc.mu.Unlock()
	}
	out := make([]ChatMsg, 0, len(inputs))
	for _, in := range inputs {
		switch in.Role {
		case "user":
			// 已前置落库 → 由 msgs() 从库带回，不重复叠加
		case "tool":
			// 硬判据：本 turn 成功落库 **且** base（msgs() 从库带回）确实含该 tool_call_id ——
			// 二者兼备才剔除（读库失败时 base 缺该 id，若只凭 ok 剔除会丢真实内存结果）。
			if ok[in.ToolCallID] {
				if _, has := baseContent[in.ToolCallID]; has {
					continue // 本 turn 已成功落库 → 库已带回
				}
			}
			if c, has := baseContent[in.ToolCallID]; has && c != "" && c == in.Content {
				continue // 无本进程记录（恢复轮次）→ 保守：库中正文非空且一致 → 视作已呈现
			}
			out = append(out, in)
		default:
			out = append(out, in)
		}
	}
	return out
}

// parseArgs 解析 tool-call arguments JSON（失败返回空 map）。
func parseArgs(s string) map[string]any {
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{}
	}
	return m
}

// isAskTool 判断工具是否为 ask_user 类（需要用户输入）。
func isAskTool(name string) bool {
	return name == "ask_user" || name == "user_ask" || strings.Contains(name, "ask_user")
}

// isLLMTool 判断工具是否为 llm 型：llm_run（唯一注册 llm 型，DSL 作业）——server 不转发
// gateway，直接开子 session/turn 执行（§6.6 节点 kind=llm，runSubJob 执行 DSL）。
func isLLMTool(name string) bool {
	return name == "llm_run"
}

// isMetaInvokeTool 判断工具是否为 mcp_invoke（gateway 域 meta 工具，契约名口径 = canon）：
// 该工具可转调**任意**工具 → 工具白名单必须对其「目标工具」二次判白（项 5a 旁路加固）。
func isMetaInvokeTool(name string) bool {
	return name == "mcp_invoke"
}

// askQuestion 从 ask_user 参数取问题文本。
func askQuestion(args map[string]any) string {
	if q, ok := args["question"].(string); ok {
		return q
	}
	if q, ok := args["prompt"].(string); ok {
		return q
	}
	return "请回答"
}

// ─── S6 辅助：重试判定 / 退避取值 / 网络预检 / 取消感知等待 ─────────────

// retryableErr 判断错误是否可自动重试（*LLMError.Retryable）。
func retryableErr(err error) bool {
	var le *LLMError
	if errors.As(err, &le) {
		return le.Retryable
	}
	return false
}

// retryWait 取第 attempt 次可视重试前的等待时长（attempt 从 1 起计）——**退避值归 router**：
// 经 `router.RetryWait` 计算（`Retry-After` 优先，否则 1s·2s·4s… 封顶 30s；不可重试 / nil → 0）。
// llm 侧不再自持任何退避间隔（`retryDelay` 配置已移除，差异登记 41 I-156）；
// 非 `*LLMError`（如 ctx 错误）→ 0，调用方据 `retryableErr` 已先行过滤，实际不会走到。
func retryWait(err error, attempt int) time.Duration {
	var le *LLMError
	if !errors.As(err, &le) {
		return 0
	}
	return router.RetryWait(&router.Error{Retryable: le.Retryable, RetryAfter: le.RetryAfter}, attempt)
}

// emitRetry 发布**可见重试中间态**（RETRY-1，2026-10-17）：`session-retry`
//（前端 type = `llm-retry`）→ 前端提示条显示「正在重试 n/N（等待 s 秒）」；随后按
// `router.RetryWait` 退避等待。**不落错误消息、不进聊天流**（与终态 session-complete 区分：
// 中间态 retryable=true 且 retry_wait_ms>0；终态 retry_wait_ms=0）。
func (tc *turnCtx) emitRetry(err error, attempt int) {
	wait := retryWait(err, attempt)
	tc.retryAttempt = attempt
	tc.server.publish("llm-retry", map[string]any{
		"instance_id": tc.req.InstanceID, "session": tc.req.Session, "turn": tc.req.Turn,
		"retryable": true,
		// retry_attempt = 即将执行的第几次重试（1 起）；retry_count = 允许的最大重试次数
		"retry_attempt": attempt, "retry_count": tc.retryCount,
		"retry_wait_ms": int(wait / time.Millisecond),
	})
	sleepCtx(tc.ctx, wait)
}

// probeBeforeRetry 网络连接预检（对齐 llm-error-handling.md §一）：TCP 可达才重试，未连接不重试。
func probeBeforeRetry(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := u.Host
	if host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// sleepCtx 可取消的等待（重试间隔）。
func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
