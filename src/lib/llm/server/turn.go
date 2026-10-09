package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime"
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

	mu           sync.Mutex              // 保护 pending / stash / toolMsgKeys
	pending      map[string]pendingTool  // task_id → 转异步发起信息（挂起登记）
	stash        []ChatMsg               // 等待 pending 完成时合并提交的同步工具结果
	toolMsgKeys  map[string]string       // tool_call_id → role=tool 行主键（发起即落 running；终态回填同一行）
	toolCallMeta map[string]toolCallMeta // tool_call_id → 发起信息（B-05：终态回填免一次全量 LoadMessages）
	asyncDone    chan asyncDoneMsg       // gateway 回执 task-report → loop 续轮
	continues    int                     // 自动续写计数（maxAutoContinue 保护，S6）
	sessionLock  *sessionLock            // session 排他锁（onLLMStart 获取；Close 释放）
	result       string                  // 终态输出文本（子轮次递归时父侧读取；finish 写入）
	turnErr      error                   // 终态错误（子轮次递归时父侧读取；finish error 写入）
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
	// maxToolIterations 单轮工具循环上限（来自 provider 定义；缺省 20）。
	maxToolIterations int
	// allowedTools 本轮工具白名单（来自本轮 agent 的 `tools`，AG-1/AG-C4）：nil = 不限制
	// （原样下发全部 hot 工具）；非空 → 仅白名单内工具进 LLM 工具面。每轮开始解析一次（热生效）。
	// P4 2026-10-01：解析后按**场景级别的可用工具级别矩阵**过滤（越权/不存在 → 静默剔除，
	// 见 filterWhitelistByLevel）——故此处非空集合恒为"场景级别可用 ∩ 本实例可见"的子集。
	allowedTools map[string]struct{}
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
	// subOnce / subsession：本 turn 是否为**子会话**（DSL-4，42 §2 (253)：session.parent_id != ""）。
	// 懒判定一次并缓存于本 turnCtx（见 isSubsession）：快路径 = req.Parents 非空（runChildTurn
	// 逐级落链，恒为子会话，零查询）；慢路径 = 主会话 / 恢复轮（req.Parents 空）→ 查一次会话行。
	// **不落可变全局字段**，每 turn 只判一次。用途 = 「子会话压缩关闭」时的上下文超限提示（见 subsessionHint）。
	subOnce    sync.Once
	subsession bool
}

// toolCallMeta 记录一次工具发起的名称与参数（persistToolRunning 时登记，B-05），
// 供终态回填（persistToolResult）复用，避免每次工具结果都做一次全量 LoadMessages。
type toolCallMeta struct {
	name string
	args map[string]any
}

// isSubsession 判定本 turn 是否为子会话（42 §2 (253)：会话行 parent_id != ""）。
//   - 快路径：req.Parents 非空 → 必为子会话（runChildTurn 逐级追加父会话链，零查询）；
//   - 慢路径：主会话 / 恢复轮（req.Parents 空，如 recoverTurnCtx 重建）→ 经 data 门面读一次
//     会话行判 parent_id，结果缓存于本 turnCtx（subOnce，每 turn 至多一次）。
//
// 读失败 / 会话不存在 → 按**主会话**处理（保守：不误跳过压缩/沉淀、不改既有行为）。
func (tc *turnCtx) isSubsession() bool {
	tc.subOnce.Do(func() {
		if len(tc.req.Parents) > 0 {
			tc.subsession = true
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
	})
	return tc.subsession
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
	// 场景 + agent 解析 + 系统提示词三层（**新建轮与恢复轮共用同一构造**，见 resolveTurnScenario）。
	sc := s.resolveTurnScenario(req.InstanceID, req.ScenarioID, req.Agent)
	if sc.sysPrompt != "" {
		hist = append([]ChatMsg{{Role: "system", Content: sc.sysPrompt}}, hist...)
	}
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
	zoneVals := s.prjConfigValues(req.InstanceID, keepFullMaxTurnsKey, keepFullMaxTokensKey, briefBudgetKey)
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
		maxToolIterations:  llmMaxIter,
		allowedTools:       allowedTools,
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
}

// resolveTurnScenario 读场景（AG-1 / AG-2 · 25 §3，每轮读一次 = 配置热生效，AG-C5，不缓存）→
// 解析本轮生效 agent 定义 → 拼系统提示词（全局 + 目录 + 场景 + agent 四层，按序拼接、空层跳过、
// 占位符整段替换）。**新建轮（newTurnCtx）与恢复轮（recoverTurnCtx）同口径**：恢复轮无
// scenarioID / agent（未持久化）→ desc/agents 为空 → 仅注入全局层 + 目录层（同「通用模式」）。
func (s *Server) resolveTurnScenario(instanceID, scenarioID, agent string) turnScenario {
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
	// capability 子目录 + DSL env 用法）→ 场景层（scenario.description + 成员段）→ agent 层。
	// 空层跳过；`{{toolchain.*}}` / `{{path.*}}` 占位符对**合成后的整段**替换（覆盖任一层）。
	sysPrompt := systemPromptLayers(s.globalLayerPrompt(agentName), systemDirectoryLayer(), scenarioLayer(scenarioID, desc, agents), agentLayer)
	if sysPrompt != "" {
		sysPrompt = s.replaceToolchain(instanceID, sysPrompt)
		sysPrompt = s.replacePaths(instanceID, sysPrompt)
	}
	return turnScenario{sysPrompt: sysPrompt, level: level, agentDef: agentDef}
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

// systemPromptLayers 按 全局层 → 目录层 → 场景层 → agent 层 顺序拼接为一条 system 文本（空层跳过；
// 非空层之间以空行分隔）。全空 → ""（调用方不注入 system）。
func systemPromptLayers(global, directory, scenario, agent string) string {
	parts := make([]string, 0, 4)
	for _, p := range []string{global, directory, scenario, agent} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
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

// feedTimeout 是输入队列投递的最长等待（队列满时兜底，B-07）：feed 由工具/异步回执等**非 loop
// 协程**调用，队列满时无限阻塞会挂住调用方 goroutine；超时则丢弃并记录（不静默吞）。
const feedTimeout = 5 * time.Second

func (tc *turnCtx) feed(msg ChatMsg) {
	select {
	case tc.in <- msg:
		return
	case <-tc.done:
		return
	default:
	}
	// 队列满：带超时重试（仅此罕见分支才分配定时器，热路径零额外开销）。
	select {
	case tc.in <- msg:
	case <-tc.done:
	case <-time.After(feedTimeout):
		logf("[chonkpilot-server] turn %s 输入队列投递超时（满），丢弃 role=%s 消息\n", tc.req.Turn, msg.Role)
	}
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
	for {
		select {
		case <-tc.done:
			return
		case m := <-tc.asyncDone:
			tc.onAsyncDone(m)
		case msg := <-tc.in:
			tc.chatOnce(msg)
		}
	}
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
	var stash []ChatMsg
	if ok {
		// 仅在命中登记时取出暂存同步结果；未知回执不动 stash（其他在飞任务仍需它）。
		stash = tc.stash
		tc.stash = nil
	}
	tc.mu.Unlock()
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

	msgs := append([]ChatMsg{}, stash...)
	msgs = append(msgs, ChatMsg{Role: "tool", ToolCallID: pt.toolCallID, Content: resultText})
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
		return tc.server.toolsForLLM(tc.req.InstanceID)
	}
	out := make([]ToolDef, 0, len(tc.allowedTools))
	for _, t := range tc.server.visibleTools(tc.req.InstanceID) {
		if _, ok := tc.allowedTools[t.Name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// toolAllowed 判定本轮是否允许调用该工具（AG-C4 白名单语义；**调用处硬拒** G-45 ③）：
// allowedTools 为空（agent 未配 tools / 配 `[]` / 解析不到名字）= **不限制**（全量放行）；
// 非空 = 仅白名单内。名字口径 = LLM 工具面的**暴露名**（与 llmTools 过滤同一名字空间）。
func (tc *turnCtx) toolAllowed(name string) bool {
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
	msgs := append(base, unpersistedInputs(inputs, base)...)

	// LLM 调用 + 无内容失败重发（方式 A，S6）：Chat 失败或流错误且无内容 → 重发同一份上下文
	var out strings.Builder
	var think strings.Builder // 思维链累积（reasoning_content 增量 → 落库 reasoning）
	var calls []ToolCall
	finish := ""
	var streamErr error
	attempt := 0
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
			// 图片（P2-8）：用户消息里的 `![名](路径)` 引用 → 图片内容块（上传根受限读取）。
			Images: ImageOptions{UploadDir: imageUploadDir(tc.req.DataDir, tc.req.WorkDir)},
		})
		if err != nil {
			if tc.ctx.Err() != nil {
				return // 取消静默（llm-cancel 已发 interrupted complete）
			}
			if attempt < tc.retryCount && retryableErr(err) && probeBeforeRetry(tc.llmSpec.BaseURL) {
				attempt++
				sleepCtx(tc.ctx, retryWait(err, attempt))
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
				sleepCtx(tc.ctx, retryWait(streamErr, attempt))
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
	var pendings []string
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
			res, _, err := tc.server.gc.Call(tc.ctx, name, args, ctxMsg)
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
			tc.server.ask(tc.req.Turn, call.ID, args, node.TaskID)
			continue
		}
		if isLLMTool(canon) {
			// llm 型（llm_run）→ server 开子 session/turn → 完成回填父轮次（节点 kind=llm）
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
		res, taskID, err := tc.server.gc.Call(tc.ctx, name, args, ctxMsg)
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
			tc.mu.Lock()
			tc.pending[taskID] = pendingTool{toolCallID: call.ID, movedAt: time.Now().UTC().Format(time.RFC3339)}
			tc.mu.Unlock()
			tc.server.tasks.setGwTask(node.TaskID, taskID)
			tc.server.tasks.setState(node.TaskID, TaskStatePending)
			pendings = append(pendings, taskID)
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
	if len(pendings) > 0 {
		// 有转异步：同步结果暂存，等全部 pending 完成时合并喂回（onAsyncDone）
		tc.mu.Lock()
		tc.stash = append(tc.stash, tools...)
		tc.mu.Unlock()
		return
	}
	if len(tools) > 0 {
		tc.chatOnce(tools...) // 同步递归续轮（工具结果一次性提交）
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
// 说明：**不重发**（已收内容重发会重复输出）；本函数与既有内联逻辑逐字等价，仅抽取命名。
// asstKey 非空 = 流式过程中已按段落过该行 → 就地回填同一行（I-176）。
func (tc *turnCtx) resumePartial(asstKey, content, reasoning string, calls []ToolCall, streamErr error) {
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
	if guide := tc.server.memoryGuide(tc.req.InstanceID, tc.req.WorkDir); guide != "" {
		msgs = append([]ChatMsg{{Role: "system", Content: guide}}, msgs...)
	}
	// 知识库资产检索指引（RB-7，2026-09-21）：与记忆指引并列；门控见 assetGuide（知识库未接入 →
	// 空串不注入）。两条路径的区别在指引正文里写明（记忆 = 文件按绝对路径读；资产 = mcp_find + mcp_load）。
	if guide := tc.server.assetGuide(tc.req.InstanceID); guide != "" {
		msgs = append([]ChatMsg{{Role: "system", Content: guide}}, msgs...)
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
	}
}

// unpersistedInputs 过滤本次进入会话、尚未由 msgs() 从库带回的新消息（I-25 去重）：
//   - user 消息（含工具通知）：在 chatOnce 前置落库，msgs() 会从库带回，直接叠加会让同一条
//     在请求里出现两次（历史中的同内容消息不参与去重——只按"已落库即由库带回"这一事实判定，
//     不按内容匹配，故不会误吞与新提问同文的历史消息）；
//   - role=tool 结果：同样先落库再带回，仅保留库中无同 tool_call_id 的那条（落库失败兜底，
//     避免丢结果）。
func unpersistedInputs(inputs, base []ChatMsg) []ChatMsg {
	need := false
	for _, in := range inputs {
		if in.Role == "tool" {
			need = true
			break
		}
	}
	persisted := map[string]bool{}
	if need {
		for _, m := range base {
			if m.Role == "tool" {
				persisted[m.ToolCallID] = true
			}
		}
	}
	out := make([]ChatMsg, 0, len(inputs))
	for _, in := range inputs {
		switch in.Role {
		case "user":
			// 已前置落库 → 由 msgs() 从库带回，不重复叠加
		case "tool":
			if persisted[in.ToolCallID] {
				continue
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
