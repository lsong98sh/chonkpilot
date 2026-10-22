// 引擎内核（MOD-1 M5）：turn 循环 = 上下文 → step → 工具循环 → 终态。
//
// 边界（专题 §2.2 / §3.7）：
//   - chat 内核持有：工具循环、`TurnState` 演进（只经 `Apply`）、事件日志、生命周期分发；
//   - 宿主实现端口：`ContextProvider`（读库/压缩/系统提示拼装）、`TurnStore`（落库/事件日志）、
//     `EventSink`（UI 事件）、`ToolInvoker`（MCP 派发/异步策略）、`LLMClient`（router 报文级 step）；
//   - 本文件**零 MQ / 零 data / 零 router import**；主会话 / 子会话 / 群聊 / DSL 子轮次
//     都走同一 `run`（差异只在宿主如何建实例与是否 `persist`）。
//
// 本阶段（M6f-1 / M6f-2 / M6f-3）：step/工具循环 + 事件发射 + 事件日志 + 影子记录 +
// 生命周期钩子 + 插话帧栈（`Inbox` 的 `insert` 项 → LIFO 插入帧 + 一次性续接说明）+
// 异步工具回执续轮（`Inbox` 的 `tool_result` 项 → 轮内等待 / 超时失败占位 / 期间可插话）+
// 自动续写编排（`finish_reason=length` / 已产生文本后断流 → `session_interrupted` +
// 宽限期无用户输入则同一 turn 内追加「继续」）；子会话/群聊注入仍待迁入。
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ErrMaxSteps 表示 turn 超过最大 step 数（防死循环；收敛为 failed）。
var ErrMaxSteps = errors.New("chat: max steps exceeded")

const defaultMaxSteps = 32

// maxInsertDepth 是插入帧深上限（与生产 turn.go 同值；与 MaxSteps 独立双保护）。
const maxInsertDepth = 5

// defaultPollInterval 是等待异步回执期间的 Inbox 轮询间隔缺省值（PollInterval<=0）。
const defaultPollInterval = 50 * time.Millisecond

// defaultAutoContinueDelay 是自动续写宽限期缺省值（AutoContinueDelay<=0；
// 与生产 usr `autoContinueDelaySec` 默认 600s 同值）。
const defaultAutoContinueDelay = 600 * time.Second

// defaultContinueText 是自动续写注入的 user 消息文本缺省值（AutoContinueText 为空；
// 与生产 usr `continueText` 默认「继续」同值）。
const defaultContinueText = "继续"

// continueMessageKind 是自动续写注入的 user 消息 Kind（与生产 `assembleContinueKind` 同值）。
const continueMessageKind = "continue"

// RuntimeConfig 是引擎运行参数（宿主从配置注入）。
type RuntimeConfig struct {
	MaxSteps      int               // <=0 → 32
	EventLogLevel TurnEventLogLevel // 空 → outline
	// RetryCount / RetryBudget 是**报文级重试**的宿主配置（原样透传给 StepOptions）：
	// nil = 不指定（宿主默认）；&0 = 不重试；&n = n 次；Budget 0 = 宿主默认，<0 = 关闭预算。
	// 重试判定与退避归 router；引擎只把 OnRetry 折成 EventLLMRetry（可见重试）。
	RetryCount  *int
	RetryBudget time.Duration
	// PendingWait 是异步工具回执的轮内等待上限：0 = 旧行为（Pending 立即返回）；
	// >0 = 在轮内等待 Inbox 的 tool_result（超时 → 失败占位结果续轮，不终结整轮）。
	PendingWait time.Duration
	// PendingPlaceholder 是「等待回执期间」该 tool_call 在上下文里的占位 tool 消息内容
	// （生产同形：assistant.tool_calls 之后必须紧跟 tool 行，插话帧的请求才合法）。
	// 空 = 不插占位（旧行为：消息列表在回执到达后直接追加一条 tool）。
	PendingPlaceholder string
	// PollInterval 是等待期间的 Inbox 轮询间隔（<=0 → 50ms；异步回执与续写宽限期共用）。
	PollInterval time.Duration
	// AutoContinue 是 RETRY-2 语义级续写开关：false（零值）= 关闭（旧行为）——
	// 断流 → failed、length → completed，且不发 session_interrupted。
	AutoContinue bool
	// AutoContinueDelay 是中断后的宽限期（<=0 → 600s）：期间无用户输入才自动续写。
	AutoContinueDelay time.Duration
	// AutoContinueText 是自动续写注入的 user 消息文本（空 → 「继续」）。
	AutoContinueText string
	// AutoContinueMax 是每次中断最多自动续写次数（<=0 → 1）；用尽后按中断类型收口。
	AutoContinueMax int
}

// Deps 是引擎依赖（全部为端口；nil 的 Catalog/Tools 表示无工具）。
type Deps struct {
	InstanceID string
	SessionID  string
	Context    ContextProvider
	Store      TurnStore
	Sink       EventSink
	Tools      ToolInvoker
	Catalog    ToolCatalog
	LLM        LLMClient
	Clock      Clock
	Logger     *slog.Logger
	Lifecycle  Lifecycle
	Config     RuntimeConfig
	Shadow     *Recorder // 非 nil = 影子记录 StepRequest（只记录，不接管）
	// Inbox 是 turn 中途输入端口；nil = 无中途输入（行为与 M6e 及之前逐字节一致）。
	Inbox Inbox
}

// SubTurnRequest 是无会话持久化的子轮次（DSL `llm_run`）。
type SubTurnRequest struct {
	SessionID string
	TurnID    string
	Messages  []Message
	Tools     []ToolSpec
	MaxSteps  int
}

// Runtime 是一个 session 的执行实例（一 session 一实例；实例间并发）。
type Runtime struct {
	deps  Deps
	state TurnState
	seq   uint64
	// resumeNotes 是每帧的一次性续接说明（键 = 帧 ID；主帧为空串）。插入帧出栈后给父帧标注，
	// 由父帧下一次请求临时携带（不落库、不进 messages / 快照）。
	resumeNotes map[string]string
	// heldResults 是轮内等待期间收到的、CallID 不匹配当前等待项的回执（同一 step 多路异步时
	// 后发先至）——留到下一次等待循环，避免被丢弃后该调用只能走超时占位。
	heldResults []ToolResult
	// heldInserts 是嵌套帧（skipInbox）等待自身回执期间取到的插话——按序暂存，交回外层循环
	// 建帧消费（嵌套帧不在自身等待里消费插话，也不丢弃）。
	heldInserts []InboxItem
}

// New 构造引擎实例（默认值在此收敛）。
func New(deps Deps) *Runtime {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Clock == nil {
		deps.Clock = systemClock{}
	}
	if deps.Lifecycle == nil {
		deps.Lifecycle = NewLifecycle(deps.Logger)
	}
	if deps.Config.MaxSteps <= 0 {
		deps.Config.MaxSteps = defaultMaxSteps
	}
	if deps.Config.AutoContinueDelay <= 0 {
		deps.Config.AutoContinueDelay = defaultAutoContinueDelay
	}
	if deps.Config.AutoContinueText == "" {
		deps.Config.AutoContinueText = defaultContinueText
	}
	if deps.Config.AutoContinueMax <= 0 {
		deps.Config.AutoContinueMax = 1
	}
	deps.Config.EventLogLevel = ParseTurnEventLogLevel(string(deps.Config.EventLogLevel))
	return &Runtime{deps: deps, state: NewTurnState(deps.SessionID, ""), resumeNotes: map[string]string{}}
}

// State 返回当前 turn 状态（快照副本；调试 / 测试 / 宿主观测用）。
func (rt *Runtime) State() TurnState { return rt.state }

// RunTurn 跑一轮会话 turn：读上下文 → 循环 step / 工具 → 落库 + 事件 + 事件日志。
func (rt *Runtime) RunTurn(ctx context.Context, turnID string) (TurnResult, error) {
	if strings.TrimSpace(turnID) == "" {
		return TurnResult{}, errors.New("chat: turn id required")
	}
	if rt.deps.Context == nil {
		return TurnResult{}, errors.New("chat: ContextProvider required")
	}
	if rt.deps.LLM == nil {
		return TurnResult{}, errors.New("chat: LLMClient required")
	}
	if rt.deps.Store == nil {
		return TurnResult{}, errors.New("chat: TurnStore required")
	}
	rt.state = NewTurnState(rt.deps.SessionID, turnID)
	rt.seq = 0
	rt.resumeNotes = map[string]string{}
	rt.heldResults = nil
	rt.heldInserts = nil

	c, err := rt.deps.Context.Build(ctx, ContextRequest{
		InstanceID: rt.deps.InstanceID, SessionID: rt.deps.SessionID, TurnID: turnID,
	})
	if err != nil {
		return TurnResult{}, fmt.Errorf("chat: build context: %w", err)
	}
	msgs := make([]Message, 0, len(c.Messages)+1)
	if c.SystemPrompt != "" {
		msgs = append(msgs, Message{Role: "system", Content: c.SystemPrompt})
	}
	msgs = append(msgs, c.Messages...)

	var tools []ToolSpec
	if rt.deps.Catalog != nil {
		if tools, err = rt.deps.Catalog.Tools(ctx, rt.deps.InstanceID); err != nil {
			return TurnResult{}, fmt.Errorf("chat: list tools: %w", err)
		}
	}

	return rt.run(ctx, runOpts{
		turnID: turnID, messages: msgs, tools: tools,
		maxSteps: rt.deps.Config.MaxSteps, persist: true, emit: true,
	})
}

// RunSubTurn 跑一个无持久化、无 UI 事件的子轮次（工具集由调用方显式给定）。
func (rt *Runtime) RunSubTurn(ctx context.Context, req SubTurnRequest) (TurnResult, error) {
	if strings.TrimSpace(req.TurnID) == "" {
		return TurnResult{}, errors.New("chat: sub turn id required")
	}
	if rt.deps.LLM == nil {
		return TurnResult{}, errors.New("chat: LLMClient required")
	}
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = rt.deps.SessionID
	}
	rt.state = NewTurnState(sessionID, req.TurnID)
	rt.seq = 0
	rt.resumeNotes = map[string]string{}
	rt.heldResults = nil
	rt.heldInserts = nil
	maxSteps := req.MaxSteps
	if maxSteps <= 0 {
		maxSteps = rt.deps.Config.MaxSteps
	}
	return rt.run(ctx, runOpts{
		turnID: req.TurnID, messages: req.Messages, tools: req.Tools,
		maxSteps: maxSteps, persist: false, emit: false,
	})
}

// runOpts 是主 turn / 子轮次共用的执行参数。
type runOpts struct {
	turnID   string
	messages []Message
	tools    []ToolSpec
	maxSteps int
	persist  bool // 落库 + 事件日志
	emit     bool // EventSink
	// skipInbox 让本次执行不再轮询 Inbox（用于等待回执期间执行的插入帧：
	// 回执与后续插话由外层等待循环统一轮询，避免插入帧抢走待回填结果）。
	skipInbox bool
}

// frameRun 是一轮 turn 的跨帧累加器（消息 / 工具记录 / usage / 文本 / 全局 step 计数）。
// 插入帧与主帧共享同一份：插入帧产生的 user / assistant / tool 消息对外层可见
// （生产 `msgs()` 从库内读取，同为「插入帧内容进上下文」语义）。
type frameRun struct {
	messages []Message
	records  []ToolCallRecord
	usage    Usage
	final    strings.Builder
	steps    int // 全局总 step 数（跨帧累计，受 MaxSteps 约束）
	// continues 是本轮已自动续写次数（RETRY-2 / M6f-3：受 autoContinueMax 保护，
	// 每次中断最多续写一次，避免无限循环）。
	continues int
}

// run 是唯一执行路径。
func (rt *Runtime) run(ctx context.Context, o runOpts) (TurnResult, error) {
	frame := &ExecContext{TurnID: o.turnID, FrameKind: FrameMain}
	info := &TurnInfo{InstanceID: rt.deps.InstanceID, SessionID: rt.deps.SessionID, TurnID: o.turnID}

	// 1) 生命周期：BeforeTurn（插入帧规则由 Lifecycle 保证；本处恒主帧）。
	act, err := rt.deps.Lifecycle.BeforeTurn(ctx, frame, info)
	if err != nil {
		rt.recordQuiet(ctx, o, TurnEventEnd, TurnEndPayload{Status: TurnStatusFailed})
		rt.emitComplete(ctx, o, TurnStatusFailed, "")
		return rt.result(o, "", o.messages, nil, Usage{}, "error"), err
	}
	if act == ActionDrop {
		rt.recordQuiet(ctx, o, TurnEventEnd, TurnEndPayload{Status: TurnStatusCanceled})
		rt.emitComplete(ctx, o, TurnStatusCanceled, "")
		return rt.result(o, "", o.messages, nil, Usage{}, "canceled"), nil
	}

	// 2) turn_start 入日志（状态机起点）。
	if err := rt.record(ctx, o, TurnEventStart, TurnStartPayload{SessionID: rt.state.SessionID, TurnID: o.turnID}); err != nil {
		return rt.fail(ctx, o, err)
	}

	fr := &frameRun{messages: append([]Message(nil), o.messages...)}
	res, terminal, err := rt.runFrame(ctx, o, 0, "", fr)
	if terminal {
		return res, err
	}
	if err != nil {
		return rt.fail(ctx, o, err)
	}

	// 主帧用尽全局 step 额度：显式失败（不静默）。
	rt.recordQuiet(ctx, o, TurnEventEnd, TurnEndPayload{Status: TurnStatusFailed})
	rt.emitComplete(ctx, o, TurnStatusFailed, fr.final.String())
	return rt.result(o, fr.final.String(), fr.messages, fr.records, fr.usage, "error"), ErrMaxSteps
}

// runFrame 执行一帧（depth=0 = 主帧）的 step 循环，直到本帧收口或全局 step 额度用尽。
//
// 插入帧（depth>0）由 pollInsert 在命中 inbox insert 时递归执行（LIFO；栈本身以
// rt.state.Frames 为唯一事实源）；插入帧收口 → 记 inbox_consume 出栈 → 给父帧标注
// 一次性续接说明 → 回父帧继续。
//
// 返回 (res, terminal, err)：terminal=true 表示整轮已终结（res 有效；depth>0 时仅
// 「工具挂起」会出现）；depth>0 且非 terminal = 已出栈，调用方继续本帧。
func (rt *Runtime) runFrame(ctx context.Context, o runOpts, depth int, frameID string, fr *frameRun) (TurnResult, bool, error) {
	frame := &ExecContext{TurnID: o.turnID, FrameKind: FrameMain}
	if depth > 0 {
		frame = &ExecContext{TurnID: o.turnID, FrameID: frameID, FrameKind: FrameInsert, Depth: depth}
	}

	// pollInsert 轮询一次中途输入；命中 insert → 建帧并递归执行。
	// fired=false = 本次无输入（或被忽略）→ 调用方继续本帧。
	pollInsert := func() (fired bool, res TurnResult, terminal bool, err error) {
		item, ok := rt.pollInbox(ctx, o, fr)
		if !ok {
			return false, TurnResult{}, false, nil
		}
		res, terminal, err = rt.runFrame(ctx, o, depth+1, item.ID, fr)
		return true, res, terminal, err
	}

	for {
		if fr.steps >= o.maxSteps {
			break
		}

		if err := rt.record(ctx, o, TurnEventStepStart, nil); err != nil {
			return TurnResult{}, false, err
		}
		// 3) 生命周期：BeforeStep。
		//    续接说明（方案 A）：插入帧出栈后本帧（或主帧）下一次请求最前面临时插一条 system；
		//    只随本次请求携带，不落库、不进 messages 切片（一次性取走）。
		stepMsgs := fr.messages
		if note := rt.takeResumeNote(frameID); note != "" {
			stepMsgs = append([]Message{{Role: "system", Content: note}}, fr.messages...)
		}
		act, err := rt.deps.Lifecycle.BeforeStep(ctx, frame, &StepRequest{
			InstanceID: rt.deps.InstanceID, SessionID: rt.deps.SessionID, TurnID: o.turnID,
			Messages: stepMsgs, Tools: o.tools,
		})
		if err != nil {
			return TurnResult{}, false, err
		}
		if act == ActionDrop {
			res, ferr := rt.finish(ctx, o, TurnStatusCanceled, "canceled", fr.final.String(), fr.messages, fr.records, fr.usage)
			return res, true, ferr
		}

		fr.steps++

		// 4) LLM step（报文级重试在 router；chat 只消费事件）。
		//    RetryCount / RetryBudget 由宿主配置注入并原样透传；OnRetry 把 router 的可见重试
		//    折成 EventLLMRetry（宿主 sink → `llm-retry`），使引擎驱动时「正在重试 n/N」不丢。
		//     Count 取宿主配置值（nil 时 0，仅供影子 / 诊断；重试次数本身归 router）。
		req := StepRequest{
			InstanceID: rt.deps.InstanceID, SessionID: rt.deps.SessionID, TurnID: o.turnID,
			Messages: stepMsgs, Tools: o.tools,
		}
		if rt.deps.Shadow != nil {
			rt.deps.Shadow.RecordStep(req)
		}
		cfg := rt.deps.Config
		ch, err := rt.deps.LLM.Step(ctx, req, StepOptions{
			MaxRetries:  cfg.RetryCount,
			RetryBudget: cfg.RetryBudget,
			OnRetry: func(attempt int, wait time.Duration) {
				retryCount := 0
				if cfg.RetryCount != nil {
					retryCount = *cfg.RetryCount
				}
				rt.emit(ctx, o, Event{Type: EventLLMRetry, Retry: &RetryInfo{
					Attempt: attempt, Count: retryCount, Wait: wait, WaitMilli: wait.Milliseconds(),
				}})
			},
		})
		if err != nil {
			return TurnResult{}, false, fmt.Errorf("chat: llm step: %w", err)
		}
		text, reasoning, calls, done := consumeStep(ch, func(delta string) {
			rt.emit(ctx, o, Event{Type: EventLLMDelta, Text: delta})
		})
		// RETRY-2 语义级中断判定（M6f-3）：
		//   - 已产生文本后断流（`done.Err`）→ 已连接后中断（不进入 failed）；
		//   - `finish_reason=length` → 输出被截断（同样视为中断）；
		//   - 其余流错误（无文本）→ 保持既有失败路径。
		interrupted := false
		if done.Err != nil {
			if text == "" {
				return TurnResult{}, false, fmt.Errorf("chat: llm step: %w", done.Err)
			}
			interrupted = true
		} else if done.Finish == "length" {
			interrupted = true
		}
		fr.usage.PromptTokens += done.Usage.PromptTokens
		fr.usage.CompletionTokens += done.Usage.CompletionTokens
		fr.usage.TotalTokens += done.Usage.TotalTokens
		fr.final.WriteString(text)

		assistant := Message{Role: "assistant", Content: text, Reasoning: reasoning, ToolCalls: calls}
		if text != "" || len(calls) > 0 {
			if o.persist {
				if err := rt.deps.Store.SaveMessage(ctx, assistant); err != nil {
					return TurnResult{}, false, fmt.Errorf("chat: save assistant message: %w", err)
				}
			}
			fr.messages = append(fr.messages, assistant)
		}

		// 5) 生命周期：AfterStep（必达）。
		if err := rt.deps.Lifecycle.AfterStep(ctx, frame, &StepResult{
			Text: text, Reasoning: reasoning, ToolCalls: calls,
			Finish: done.Finish, Usage: done.Usage,
		}); err != nil {
			return TurnResult{}, false, err
		}

		if err := rt.record(ctx, o, TurnEventStepDone, StepDonePayload{Pending: calls}); err != nil {
			return TurnResult{}, false, err
		}

		// 5b) 语义级中断（M6f-3）：部分文本已按上面路径落库；AutoContinue 开启 →
		//     `session_interrupted` + 宽限期等待（期间用户输入即取消）→ 到期追加「继续」后续 step；
		//     关闭 / 次数用尽 → 按既有语义收口（length → completed，断流 → failed）。
		if interrupted {
			if rt.deps.Config.AutoContinue {
				// 中断事件字段（对齐宿主 `session-interrupted`）：code = 截断 / 断流；
				// 语义级续写恒可重试；宽限期 = 已启用自动续写时的配置值（毫秒）。
				code := "stream"
				if done.Finish == "length" {
					code = "length"
				}
				rt.emit(ctx, o, Event{
					Type: EventSessionInterrupted, Text: text,
					Code: code, Retryable: true,
					DelayMilli: rt.deps.Config.AutoContinueDelay.Milliseconds(),
				})
				if fr.continues < rt.deps.Config.AutoContinueMax {
					res, terminal, err := rt.awaitAutoContinue(ctx, o, depth, fr)
					if err != nil {
						return TurnResult{}, false, err
					}
					if terminal {
						return res, true, nil
					}
					continue // 已续写或用户已介入 → 回到本帧循环（下一次 step）
				}
			}
			if done.Err != nil {
				return TurnResult{}, false, fmt.Errorf("chat: llm step: %w", done.Err)
			}
			if depth == 0 {
				res, ferr := rt.finish(ctx, o, TurnStatusCompleted, finishOf(done.Finish), fr.final.String(), fr.messages, fr.records, fr.usage)
				return res, true, ferr
			}
			break
		}
		// 6) 工具循环：同步 → 直接回填；Pending（异步）→ 按 `PendingWait` 决定「旧行为挂起」或
		//    「轮内等待回执」。一次只等一个 Pending（其余调用在本循环内按既有顺序继续处理；
		//    生产侧多路并发在飞 + 统一闸门的语义留生产切换时对齐）。
		for _, call := range calls {
			act, err := rt.deps.Lifecycle.BeforeTool(ctx, frame, &call)
			if err != nil {
				return TurnResult{}, false, err
			}
			var res ToolResult
			placeholder := -1
			switch {
			case act == ActionDrop:
				res = ToolResult{CallID: call.ID, Content: "[skipped by extension hook]", IsError: true}
			case rt.deps.Tools == nil:
				res = ToolResult{CallID: call.ID, Content: "[no tool invoker]", IsError: true}
			default:
				if err := rt.record(ctx, o, TurnEventToolDispatch, ToolDispatchPayload{CallID: call.ID}); err != nil {
					return TurnResult{}, false, err
				}
				outcome, derr := rt.deps.Tools.Dispatch(ctx, call)
				switch {
				case derr != nil:
					res = ToolResult{CallID: call.ID, Content: derr.Error(), IsError: true}
				case outcome.Pending && rt.canAwaitPending():
					// M6f-2：轮内等待宿主回填（不重复 Dispatch）；期间可处理插话。
					// 等待期间该调用先以占位 tool 消息进入上下文（生产同形：否则插入帧的
					// assistant.tool_calls 后没有 tool 行，请求是非法协议序列）。
					placeholder = rt.appendPendingPlaceholder(fr, call)
					out, werr := rt.awaitPendingResult(ctx, o, depth, fr, call)
					if werr != nil {
						return TurnResult{}, false, werr
					}
					if out.Terminal {
						return out.Res, true, nil
					}
					res = out.Result
				case outcome.Pending:
					// 旧行为（PendingWait<=0 / 无 Inbox）：本轮到此挂起，宿主回填后重建实例继续。
					pending := rt.result(o, fr.final.String(), fr.messages, fr.records, fr.usage, "pending")
					pending.Frames = len(rt.state.Frames)
					return pending, true, nil
				default:
					res = outcome.Result
				}
			}
			if err := rt.deliverToolResult(ctx, o, frame, fr, call, res, placeholder); err != nil {
				return TurnResult{}, false, err
			}
		}

		// 0) 中途输入轮询（下一次 step 的 BeforeStep 之前 / 本帧收口之前）：命中即先跑插入帧，
		//    出栈后回本帧继续（下一次请求携带续接说明）。主帧首步之前不轮询——生产在轮次
		//    启动时尚不存在插入帧（未开轮的插入会走 next_turn）。
		fired, res, terminal, err := pollInsert()
		if err != nil {
			return TurnResult{}, false, err
		}
		if terminal {
			return res, true, nil
		}
		if fired {
			continue
		}

		if len(calls) == 0 {
			if depth == 0 {
				res, ferr := rt.finish(ctx, o, TurnStatusCompleted, finishOf(done.Finish), fr.final.String(), fr.messages, fr.records, fr.usage)
				return res, true, ferr
			}
			break
		}
	}

	// 本帧结束：插入帧 → 出栈（inbox_consume）+ 给父帧标注一次性续接说明；主帧 → step 额度用尽。
	if depth > 0 {
		if err := rt.record(ctx, o, TurnEventInboxConsume, InboxConsumePayload{FrameID: frameID}); err != nil {
			return TurnResult{}, false, err
		}
		rt.setResumeNote(rt.currentFrameID())
	}
	return TurnResult{}, false, nil
}

// deliverToolResult 按同步结果路径回填一次工具结果：tool 消息落库 → 进 messages / records →
// 发 tool_result 事件 → AfterTool 钩子 → 记 tool_result 事件日志。
// 同步结果、轮内等待到的异步回执、超时失败占位共用此路径（事件顺序一致）。
// placeholder >= 0 表示 fr.messages[placeholder] 是该调用等待期间插入的占位 tool 消息 →
// 就地回填（不追加第二条），否则追加一条新的 tool 消息。
func (rt *Runtime) deliverToolResult(ctx context.Context, o runOpts, frame *ExecContext, fr *frameRun, call ToolCall, res ToolResult, placeholder int) error {
	if res.CallID == "" {
		res.CallID = call.ID
	}
	if o.persist {
		if err := rt.deps.Store.SaveMessage(ctx, Message{
			Role: "tool", ToolCallID: res.CallID, Content: res.Content, Meta: res.Meta,
		}); err != nil {
			return fmt.Errorf("chat: save tool message: %w", err)
		}
	}
	if placeholder >= 0 && placeholder < len(fr.messages) {
		fr.messages[placeholder] = Message{Role: "tool", ToolCallID: res.CallID, Content: res.Content, Meta: res.Meta}
	} else {
		fr.messages = append(fr.messages, Message{Role: "tool", ToolCallID: res.CallID, Content: res.Content, Meta: res.Meta})
	}
	fr.records = append(fr.records, ToolCallRecord{CallID: res.CallID, Name: call.Name, Result: res})
	rt.emit(ctx, o, Event{Type: EventToolResult, ToolResult: &res})
	if err := rt.deps.Lifecycle.AfterTool(ctx, frame, &res); err != nil {
		return err
	}
	return rt.record(ctx, o, TurnEventToolResult, res)
}

// appendPendingPlaceholder 在轮内等待异步回执之前，为该调用插入一条占位 tool 消息
// （Content = RuntimeConfig.PendingPlaceholder；空 → 不插，返回 -1 保持旧行为）。
func (rt *Runtime) appendPendingPlaceholder(fr *frameRun, call ToolCall) int {
	text := rt.deps.Config.PendingPlaceholder
	if text == "" {
		return -1
	}
	fr.messages = append(fr.messages, Message{Role: "tool", ToolCallID: call.ID, Content: text})
	return len(fr.messages) - 1
}

// canAwaitPending 报告是否在轮内等待异步回执（M6f-2）：
// `PendingWait <= 0` 或无 Inbox → 旧行为（Pending 立即返回）。
func (rt *Runtime) canAwaitPending() bool {
	return rt.deps.Config.PendingWait > 0 && rt.deps.Inbox != nil
}

// awaitAutoContinue 在自动续写宽限期内等待（RETRY-2 / M6f-3）：
//   - 期间任何 Inbox 输入 → 视为用户已介入，取消本次自动续写：
//     `insert` 先走 M6f-1 插话路径（CommitInbox + 插入帧），`tool_result` 因本步已结束、
//     无待回填调用而无法按 M6f-2 回填（不重复 Dispatch），仅留痕；
//   - 宽限期到期且无输入 → 追加一条 user 续写消息（落库 + 内存 + `continue` 事件日志）；
//   - ctx 取消 → 整轮 canceled（用未取消的 ctx 收尾，保证终态可落库）。
//
// 返回 terminal=true 表示整轮已终结（res 有效）；否则调用方回到本帧循环跑下一次 step。
func (rt *Runtime) awaitAutoContinue(ctx context.Context, o runOpts, depth int, fr *frameRun) (TurnResult, bool, error) {
	interval := rt.deps.Config.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	timer := rt.deps.Clock.After(rt.deps.Config.AutoContinueDelay)
	tick := rt.deps.Clock.After(interval)

	for {
		if item, ok := rt.pollInboxRaw(ctx, o); ok {
			if item.Type == InboxTypeInsert {
				if !rt.consumeInsert(ctx, o, fr, item) {
					continue // 空内容 / 帧深超限 / 落库失败 → 未消费，继续等宽限期
				}
				// 插话已执行 → 用户已介入，插入帧跑完后取消本次自动续写。
				inner := o
				inner.skipInbox = true
				res, terminal, err := rt.runFrame(ctx, inner, depth+1, item.ID, fr)
				if err != nil {
					return TurnResult{}, false, err
				}
				if terminal {
					return res, true, nil
				}
			} else if item.Type == InboxTypeToolResult {
				rt.deps.Logger.Debug("chat: inbox input during auto-continue grace; no pending call",
					slog.String("turn_id", o.turnID), slog.String("inbox_id", item.ID))
			}
			return TurnResult{}, false, nil // 用户 / 宿主已介入 → 取消自动续写
		}

		select {
		case <-ctx.Done():
			res, err := rt.finish(context.WithoutCancel(ctx), o, TurnStatusCanceled, "canceled",
				fr.final.String(), fr.messages, fr.records, fr.usage)
			return res, true, err
		case <-timer:
			if err := rt.appendContinueMessage(ctx, o, fr); err != nil {
				return TurnResult{}, false, err
			}
			return TurnResult{}, false, nil
		case <-tick:
			tick = rt.deps.Clock.After(interval)
		}
	}
}

// appendContinueMessage 追加一条自动续写 user 消息（同一 turn 内、不终结轮次）：
// 落库（persist）→ 内存 messages → `continue` 事件日志 → 本轮续写计数 +1。
func (rt *Runtime) appendContinueMessage(ctx context.Context, o runOpts, fr *frameRun) error {
	msg := Message{Role: "user", Kind: continueMessageKind, Content: rt.deps.Config.AutoContinueText}
	if o.persist {
		if err := rt.deps.Store.SaveMessage(ctx, msg); err != nil {
			return fmt.Errorf("chat: save continue message: %w", err)
		}
	}
	fr.messages = append(fr.messages, msg)
	fr.continues++
	return rt.record(ctx, o, TurnEventContinue, nil)
}

// pendingOutcome 是「轮内等待异步回执」的结果。
// Terminal=true 表示等待期间（插入帧）已终结整轮，Res 有效。
type pendingOutcome struct {
	Result   ToolResult
	Terminal bool
	Res      TurnResult
}

// awaitPendingResult 在轮内等待一次异步工具回执（M6f-2）：
//   - `tool_result` 且匹配当前调用 → 立即返回回执（CallID 为空补为当前调用）；
//     不匹配 → 暂存（heldResults）并继续等（同一 step 的另一个异步调用随后取用）；
//     无载荷 → 忽略并继续等；
//   - `insert` → 走 M6f-1 插话路径（CommitInbox + 插入帧），跑完后**继续等**原回执；
//   - 超时 → 失败占位结果（不终结整轮）；
//   - ctx 取消 → 整轮 canceled。
//
// 等待期间**不重复 Dispatch** 同一调用（Dispatch 已在工具循环内完成一次）。
func (rt *Runtime) awaitPendingResult(ctx context.Context, o runOpts, depth int, fr *frameRun, call ToolCall) (pendingOutcome, error) {
	// 同一 step 的多个异步调用按顺序等待：后发先至的回执已在上一次等待里暂存 → 直接取用。
	if res, ok := rt.takeHeldResult(call.ID); ok {
		return pendingOutcome{Result: res}, nil
	}
	wait := rt.deps.Config.PendingWait
	interval := rt.deps.Config.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	timeout := rt.deps.Clock.After(wait)
	tick := rt.deps.Clock.After(interval)

	timeoutOutcome := func() pendingOutcome {
		rt.deps.Logger.Warn("chat: tool result timeout; resume with failed placeholder",
			slog.String("turn_id", o.turnID), slog.String("call_id", call.ID), slog.Duration("wait", wait))
		return pendingOutcome{Result: ToolResult{
			CallID:  call.ID,
			Content: "错误: 工具执行超时（等待回执 " + wait.String() + "）",
			IsError: true,
		}}
	}

	for {
		if item, ok := rt.pollForResult(ctx, o); ok {
			switch item.Type {
			case InboxTypeToolResult:
				if item.Result == nil {
					break // 无回执载荷 → 忽略
				}
				res := *item.Result
				if res.CallID == "" {
					res.CallID = call.ID
				}
				if res.CallID != call.ID {
					// 只接受匹配当前待回填调用的回执；其余（同 step 的另一个异步调用）**暂存**
					// 到 heldResults，等轮到它等待时取用（不丢弃、不重复 Dispatch）。
					rt.deps.Logger.Debug("chat: hold mismatched tool_result",
						slog.String("inbox_id", item.ID), slog.String("call_id", res.CallID))
					rt.heldResults = append(rt.heldResults, res)
					break
				}
				return pendingOutcome{Result: res}, nil
			case InboxTypeInsert:
				if rt.consumeInsert(ctx, o, fr, item) {
					// 插话不打断等待：先跑完插入帧再继续等原回执。该帧不再轮询 inbox
					// （否则会抢走本循环待消费的回执）。
					inner := o
					inner.skipInbox = true
					res, terminal, err := rt.runFrame(ctx, inner, depth+1, item.ID, fr)
					if err != nil {
						return pendingOutcome{}, err
					}
					if terminal {
						return pendingOutcome{Terminal: true, Res: res}, nil
					}
				}
			}
			// 刚处理过一条输入 → 立即再取一次（不做间隔等待）。
			continue
		}

		// 超时优先于间隔轮询（两个 channel 同时就绪时不随机选择）。
		select {
		case <-timeout:
			return timeoutOutcome(), nil
		default:
		}
		select {
		case <-ctx.Done():
			// 取消语义：整轮收敛到 canceled（用未取消的 ctx 收尾，保证终态可落库）。
			res, err := rt.finish(context.WithoutCancel(ctx), o, TurnStatusCanceled, "canceled",
				fr.final.String(), fr.messages, fr.records, fr.usage)
			return pendingOutcome{Terminal: true, Res: res}, err
		case <-timeout:
			return timeoutOutcome(), nil
		case <-tick:
			tick = rt.deps.Clock.After(interval)
		}
	}
}

// takeHeldResult 取走一条暂存的回执（CallID 匹配才取；未命中 → false）。
func (rt *Runtime) takeHeldResult(callID string) (ToolResult, bool) {
	for i := range rt.heldResults {
		if rt.heldResults[i].CallID == callID {
			res := rt.heldResults[i]
			rt.heldResults = append(rt.heldResults[:i], rt.heldResults[i+1:]...)
			return res, true
		}
	}
	return ToolResult{}, false
}

// pollInbox 在每个 step 结束后（= 下一个 step 的 BeforeStep 之前 / 本帧收口之前）非阻塞取
// 一条中途输入并消费其中的 insert（M6f-1）。
//
// 判定顺序对齐生产 consumeInbox：空内容丢弃 → 非 insert 忽略 → 帧深超限留痕
// （不消费，由宿主持久化为 next_turn）→ 同事务落消息 + 置 consumed（CommitInbox）→
// 记 inbox_insert。ok=true 表示已建帧，调用方须执行该插入帧。
//
// 仅在持久化轮次轮询：子轮次（RunSubTurn）不读也不写 inbox。
func (rt *Runtime) pollInbox(ctx context.Context, o runOpts, fr *frameRun) (InboxItem, bool) {
	item, ok := rt.pollInboxRaw(ctx, o)
	if !ok || !rt.consumeInsert(ctx, o, fr, item) {
		return InboxItem{}, false
	}
	return item, true
}

// pollInboxRaw 非阻塞取一条中途输入（不判定类型、不消费；等待异步回执时也用它）。
// o.skipInbox / 子轮次 / 无 Inbox → 无输入。
func (rt *Runtime) pollInboxRaw(ctx context.Context, o runOpts) (InboxItem, bool) {
	if o.skipInbox || !o.persist || rt.deps.Inbox == nil {
		return InboxItem{}, false
	}
	if item, ok := rt.takeHeldInsert(); ok {
		return item, true
	}
	item, ok, err := rt.deps.Inbox.Poll(ctx)
	if err != nil {
		rt.deps.Logger.Warn("chat: poll inbox failed; continue without insert",
			slog.String("turn_id", o.turnID), slog.String("err", err.Error()))
		return InboxItem{}, false
	}
	if !ok {
		return InboxItem{}, false
	}
	return item, true
}

// pollForResult 是「等待异步回执」期间的取件：**skipInbox 帧同样取件**（嵌套插入帧自身的
// 工具结果也经 Inbox 回填，完全不取件只能等超时）；途中取到的 insert 按序暂存
// （heldInserts），不在此建帧 —— 交回外层循环按既有语义消费。
func (rt *Runtime) pollForResult(ctx context.Context, o runOpts) (InboxItem, bool) {
	if !o.persist || rt.deps.Inbox == nil {
		return InboxItem{}, false
	}
	if !o.skipInbox {
		if item, ok := rt.takeHeldInsert(); ok {
			return item, true
		}
	}
	for {
		item, ok, err := rt.deps.Inbox.Poll(ctx)
		if err != nil {
			rt.deps.Logger.Warn("chat: poll inbox failed; continue waiting tool result",
				slog.String("turn_id", o.turnID), slog.String("err", err.Error()))
			return InboxItem{}, false
		}
		if !ok {
			return InboxItem{}, false
		}
		if o.skipInbox && item.Type == InboxTypeInsert {
			rt.heldInserts = append(rt.heldInserts, item)
			continue
		}
		return item, true
	}
}

// takeHeldInsert 取走一条暂存的插话（FIFO；空 → false）。
func (rt *Runtime) takeHeldInsert() (InboxItem, bool) {
	if len(rt.heldInserts) == 0 {
		return InboxItem{}, false
	}
	item := rt.heldInserts[0]
	rt.heldInserts = rt.heldInserts[1:]
	return item, true
}

// consumeInsert 消费一条中途插话（M6f-1）：空内容丢弃 → 非 insert 忽略 → 帧深超限留痕
// （不消费，由宿主持久化为 next_turn）→ CommitInbox（同事务落消息 + 置 consumed）→ 记 inbox_insert。
// ok=true 表示已建帧，调用方须执行该插入帧。
func (rt *Runtime) consumeInsert(ctx context.Context, o runOpts, fr *frameRun, item InboxItem) bool {
	if item.Type != InboxTypeInsert {
		return false
	}
	if strings.TrimSpace(item.Content) == "" {
		return false // 空内容丢弃：不落消息、不建帧（生产同义）
	}
	if len(rt.state.Frames) >= maxInsertDepth {
		rt.deps.Logger.Warn("chat: insert frame depth limit reached; inbox item left to host",
			slog.String("inbox_id", item.ID), slog.Int("depth", len(rt.state.Frames)))
		return false
	}
	if err := rt.deps.Store.CommitInbox(ctx, item.ID, Message{Role: "user", Kind: "text", Content: item.Content}); err != nil {
		// 落消息失败 → 不建帧（宿主侧该条仍为 pending，可重试）；不终结整轮。
		rt.deps.Logger.Warn("chat: commit inbox failed; insert skipped",
			slog.String("inbox_id", item.ID), slog.String("err", err.Error()))
		return false
	}
	if err := rt.record(ctx, o, TurnEventInboxInsert, FrameState{
		FrameID: item.ID, ParentFrameID: rt.currentFrameID(), Depth: len(rt.state.Frames) + 1,
	}); err != nil {
		return false
	}
	fr.messages = append(fr.messages, Message{Role: "user", Kind: "text", Content: item.Content})
	return true
}

// currentFrameID 返回当前栈顶帧 ID（栈空 = 主帧，返回空串）。rt.state.Frames 是唯一事实源。
func (rt *Runtime) currentFrameID() string {
	if n := len(rt.state.Frames); n > 0 {
		return rt.state.Frames[n-1].FrameID
	}
	return ""
}

// setResumeNote 给某帧标注「续接说明」（一次性；帧 ID 空串 = 主帧）。
func (rt *Runtime) setResumeNote(frameID string) {
	if rt.resumeNotes == nil {
		rt.resumeNotes = map[string]string{}
	}
	rt.resumeNotes[frameID] = InsertResumeNote
}

// takeResumeNote 取走某帧的续接说明（一次性；不落库、不进快照）。
func (rt *Runtime) takeResumeNote(frameID string) string {
	note := rt.resumeNotes[frameID]
	if note != "" {
		delete(rt.resumeNotes, frameID)
	}
	return note
}

// consumeStep 聚合一次 step 的流（文本 / 推理 / 工具调用 / 终态）。
// onDelta 非 nil 时逐段回调（宿主据此发光标事件）；流未以 Done 结束 → 显式错误。
func consumeStep(ch <-chan StepEvent, onDelta func(string)) (text, reasoning string, calls []ToolCall, done StepEvent) {
	var b, r strings.Builder
	sawDone := false
	for ev := range ch {
		switch ev.Type {
		case StepEventDelta:
			b.WriteString(ev.Text)
			if onDelta != nil && ev.Text != "" {
				onDelta(ev.Text)
			}
		case StepEventReasoning:
			r.WriteString(ev.Text)
		case StepEventToolCall:
			if ev.ToolCall != nil {
				calls = appendToolCall(calls, *ev.ToolCall)
			}
		case StepEventDone:
			done = ev
			sawDone = true
		}
	}
	for _, c := range done.ToolCalls {
		calls = appendToolCall(calls, c)
	}
	if !sawDone {
		done.Err = errors.New("chat: llm stream ended without done")
	}
	return b.String(), r.String(), calls, done
}

// appendToolCall 幂等追加（同 ID 覆盖，空 ID 直接追加）。
func appendToolCall(calls []ToolCall, c ToolCall) []ToolCall {
	if c.ID != "" {
		for i := range calls {
			if calls[i].ID == c.ID {
				calls[i] = c
				return calls
			}
		}
	}
	return append(calls, c)
}

// finishOf 规整 finish_reason（空 → stop）。
func finishOf(s string) string {
	if s == "" {
		return "stop"
	}
	return s
}

// finish 落 turn_end + AfterTurn + turn_complete，返回终态结果。
func (rt *Runtime) finish(ctx context.Context, o runOpts, status TurnStatus, finish, text string, msgs []Message, records []ToolCallRecord, usage Usage) (TurnResult, error) {
	if err := rt.record(ctx, o, TurnEventEnd, TurnEndPayload{Status: status}); err != nil {
		return rt.result(o, text, msgs, records, usage, finish), err
	}
	res := rt.result(o, text, msgs, records, usage, finish)
	res.Frames = len(rt.state.Frames)
	if err := rt.deps.Lifecycle.AfterTurn(ctx, &ExecContext{TurnID: o.turnID, FrameKind: FrameMain}, &res); err != nil {
		return res, err
	}
	rt.emitComplete(ctx, o, status, text)
	return res, nil
}

// fail 记录 failed 终态并返回错误。
func (rt *Runtime) fail(ctx context.Context, o runOpts, err error) (TurnResult, error) {
	rt.recordQuiet(ctx, o, TurnEventEnd, TurnEndPayload{Status: TurnStatusFailed})
	rt.emitComplete(ctx, o, TurnStatusFailed, "")
	return rt.result(o, "", nil, nil, Usage{}, "error"), err
}

// result 组装 TurnResult。
func (rt *Runtime) result(o runOpts, text string, msgs []Message, records []ToolCallRecord, usage Usage, finish string) TurnResult {
	return TurnResult{
		SessionID: rt.state.SessionID, TurnID: o.turnID,
		FinalText: text, Messages: msgs, ToolCalls: records,
		Finish: finish, Usage: usage, Frames: len(rt.state.Frames),
	}
}

// record 推进状态机并写事件日志（受 turnEventLog 分级控制）。
func (rt *Runtime) record(ctx context.Context, o runOpts, typ TurnEventType, payload any) error {
	rt.seq++
	ev := TurnEvent{Seq: rt.seq, Type: typ, At: rt.deps.Clock.Now()}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("chat: marshal %s: %w", typ, err)
		}
		ev.Payload = b
	}
	next, err := Apply(rt.state, ev)
	if err != nil {
		return err
	}
	rt.state = next
	if o.persist && rt.deps.Config.EventLogLevel.Records(typ) {
		if err := rt.deps.Store.AppendEvent(ctx, ev); err != nil {
			return fmt.Errorf("chat: append event %s: %w", typ, err)
		}
	}
	return nil
}

// recordQuiet 是崩溃路径的收尾（错误忽略，仅尽力推进状态）。
func (rt *Runtime) recordQuiet(ctx context.Context, o runOpts, typ TurnEventType, payload any) {
	_ = rt.record(ctx, o, typ, payload)
}

// emit 发对外事件（非阻塞由 EventSink 实现保证；emit=false 时不发）。
func (rt *Runtime) emit(ctx context.Context, o runOpts, ev Event) {
	if !o.emit || rt.deps.Sink == nil {
		return
	}
	if ev.InstanceID == "" {
		ev.InstanceID = rt.deps.InstanceID
	}
	if ev.SessionID == "" {
		ev.SessionID = rt.state.SessionID
	}
	if ev.TurnID == "" {
		ev.TurnID = o.turnID
	}
	if ev.At.IsZero() {
		ev.At = rt.deps.Clock.Now()
	}
	rt.deps.Sink.Emit(ctx, ev)
}

func (rt *Runtime) emitComplete(ctx context.Context, o runOpts, status TurnStatus, text string) {
	rt.emit(ctx, o, Event{Type: EventTurnComplete, Status: string(status), Text: text})
}

// systemClock 是 Clock 的系统实现。
type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
