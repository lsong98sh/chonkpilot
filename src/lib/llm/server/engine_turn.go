// chat 引擎执行一整轮 turn（MOD-1 A4-P4b：引擎为唯一执行路径，无回滚开关）。
//
// 执行体 = `runEngineLoop`（`loop` 的唯一实现）：首个输入等待 → LLM step → 工具循环（含 ask /
// 同步 / 异步 / 任务树 / 落库）→ 中途插话 → 异步回执续轮 → 自动续写 → 终态。
// 恢复轮（`recoverTurnCtx`）走同一路径：上下文种子 = `tc.msgs()`（本轮全部已落库消息，
// 含 system/user/assistant/tool）→ 引擎只从历史继续下一次 LLM 调用，**不重复派发已完成工具**；
// 「assistant tool_calls 缺 tool 行」的断链在 `RunTurn` 前显式合成 `role=tool` +
// `tool_call_status=interrupted` 并落库（`engineSynthesizeDanglingToolResults`）。
//
// 边界（零消息面变更）：
//   - 终态仍由 `server.finish` 唯一收口（引擎 `turn_complete` 有意不发，避免二次终态）；
//   - 工具派发复用生产 `dispatchToolCall`（策略 / 任务树 / running+result 同行 / 问询 / 异步全部同源）；
//   - 消息落库复用 `tc.persistMessage`；工具结果已由派发 / 回执路径落库 → 引擎的
//     `Store.SaveMessage(role=tool)` 不重复写；
//   - 中途输入的**唯一消费者** = 引擎（`enginePump` 把 in / inboxCh / asyncDone 转为 engineInbox）。
package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lsong98sh/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// engineTurnCalls 记录 `runEngineLoop` 的调用次数（测试钩子：断言子会话 / 群聊零调用）。
var engineTurnCalls atomic.Int64

// enginePendingPlaceholder 是「等待异步回执期间」该 tool_call 在上下文里的占位 tool 消息
// （与数据层 `LoadMessages` 对 running 行的投影文案逐字一致；见 data/internal/kernel/viewdata.go）。
const enginePendingPlaceholder = "（工具执行中，结果稍后返回）"

// engineToolResultItem 构造一条异步工具回执输入（ask 回答 / 异步工具结果同形）。
func engineToolResultItem(callID, content string, isErr bool) chat.InboxItem {
	return chat.InboxItem{
		Type:   chat.InboxTypeToolResult,
		Result: &chat.ToolResult{CallID: callID, Content: content, IsError: isErr},
	}
}

// runEngineLoop 用 chat 引擎执行一整轮（`loop` 的唯一实现）。
//
// 首个输入等待与落库复用生产语义：`llm-send` 投递的 user 消息在起引擎前落库（`msgs()` 才能
// 带回）；`next_turn` 消费路径（`Kick`）无新输入 —— 消息已由消费方落库。
//
// 恢复轮（A4-P4b-0）：上下文种子 = `tc.msgs()`（本轮全部已落库消息）→ 引擎只从历史继续下一次
// LLM 调用、不重复派发已完成工具；断链工具在落首个输入**之前**补齐（`engineSynthesizeDanglingToolResults`）。
func (tc *turnCtx) runEngineLoop() {
	engineTurnCalls.Add(1)

	first, ok := tc.engineFirstInput()
	if !ok {
		return
	}
	// 恢复轮：**先**补齐「assistant.tool_calls 缺 tool 行」的断链（A4-P4b-0），再落首个输入 ——
	// 顺序敏感：合成的 tool 行必须紧跟 assistant.tool_calls（否则协议变成 assistant → user → tool）。
	tc.engineSynthesizeDanglingToolResults()
	if first.Role == "user" {
		if err := tc.persistMessage(first); err != nil {
			tc.server.llmError(tc, "DB_ERROR", err.Error())
			tc.Close()
			return
		}
	}
	// 首个输入是工具结果（异常时序）：已由 FeedToolResultStatus 落库 → msgs() 从库带回，无需再处理。

	stop := make(chan struct{})
	defer close(stop)
	go tc.enginePump(stop)

	store := &engineTurnStore{tc: tc}
	// 终态文本口径 = 生产 `tc.answer`：只累计**主帧**且**无 tool_calls** 的 assistant 段
	// （插入帧的正文不进终态文本 —— 生产按帧各自的 answerBuilder 累积）。
	answer := &engineTurnAnswer{}
	lifecycle := chat.NewLifecycle(nil)
	lifecycle.Register(answer, 0, chat.FailOpen)
	eng := chat.New(chat.Deps{
		InstanceID: tc.req.InstanceID,
		SessionID:  tc.req.Session,
		Context:    engineTurnContext{tc: tc},
		Store:      store,
		Sink:       engineTurnSink{tc: tc},
		Tools:      engineTurnTools{tc: tc},
		Lifecycle:  lifecycle,
		// 下发面与生产同源（hot / 白名单 / denyTools 的过滤都在 tc.llmTools 内）。
		Catalog: newChatToolCatalog(tc.server, func(string) []ToolDef { return tc.llmTools() }),
		// 思考链转发（reasoning 增量 → `llm-receive{type:reason}`）复用 P3a 的包装；报文级
		// 重试次数 / 预算经 RuntimeConfig 透传，**不传 RetryObserver**（由 Sink 发 `llm-retry`）。
		LLM: engineStepLLM{tc: tc, inner: newChatLLMClient(tc.server, tc.llmSpec, ChatOptions{
			Model:           tc.llmModel,
			Think:           tc.llmThink,
			Effort:          tc.llmEffort,
			Temperature:     tc.llmTemperature,
			MaxTokens:       tc.llmMaxOutputToken,
			ResponseTimeout: tc.responseTimeout,
			StreamTimeout:   tc.streamTimeout,
			Images:          ImageOptions{DataRoot: imageDataRoot(tc.req.DataDir, tc.req.WorkDir)},
		})},
		Config: tc.engineRuntimeConfig(),
		Inbox:  tc.engineInbox,
	})
	res, err := eng.RunTurn(tc.ctx, tc.req.Turn)
	tc.engineFinish(res, err, answer.text())
}

// engineInterruptedToolText 是「断链工具」合成的结果文案，与数据层 `kernel.toolPlaceholder`
// 的 `interrupted` 占位**逐字一致**（重启清理后 LLM 看到的同一句话）。
const engineInterruptedToolText = "（工具已中断，未获得结果；如需重试请使用「重试」）"

// engineSynthesizeDanglingToolResults 为恢复轮补齐「assistant 有 tool_calls、但该轮缺对应 tool 行」的
// 断链（A4-P4b-0）：启动清理 `cleanupStaleToolPairs` 已把遗留非终态 tool 行标 `interrupted`，正常情况下
// 不会出现断链；清理未覆盖的极端情况（消息残缺）下引擎会把断链直接发给 LLM（协议非法）→ 此处显式
// 合成一条 `role=tool`（`tool_call_status=interrupted`，文案同生产占位）并落库，并记录 Warn。
//
// 仅恢复轮生效（普通轮无既存工具历史，无需补齐）；幂等：已存在的 tool 行（无论终态）不覆盖。
func (tc *turnCtx) engineSynthesizeDanglingToolResults() {
	if !tc.isRecovery {
		return
	}
	msgs := newSessionStore(tc.server.bus, tc.req.InstanceID).LoadMessages(tc.req.Turn)
	have := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			have[m.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, c := range m.ToolCalls {
			if c.ID == "" || have[c.ID] {
				continue
			}
			logf("[chonkpilot-server] WARN 恢复轮断链补齐：turn=%s tool_call_id=%s tool=%s 缺 tool 行 → 合成 interrupted 结果\n",
				tc.req.Turn, c.ID, c.Name)
			tc.persistToolResult(c.ID, engineInterruptedToolText, persist.ToolStatusInterrupted, nil)
			have[c.ID] = true
		}
	}
}

// engineAutoContinue 是本轮**实际生效**的自动续写开关：usr 配置 && 非子轮次
// （子轮次强制关闭，见 `turnCtx.autoContinueOff`）。
func (tc *turnCtx) engineAutoContinue() bool {
	return tc.autoContinue && !tc.autoContinueOff
}

// engineRuntimeConfig 把生产运行参数原样折成引擎配置（缺省值与生产同源）。
func (tc *turnCtx) engineRuntimeConfig() chat.RuntimeConfig {
	maxSteps := tc.maxToolIterations
	if maxSteps <= 0 {
		maxSteps = defaultMaxToolIterations
	}
	pendingWait := tc.pendingTaskTimeout
	if pendingWait <= 0 {
		pendingWait = defaultPendingTaskTimeout
	}
	retryCount := tc.retryCount
	// 生产语义：`autoContinueDelaySec=0` = 立即续写；引擎的零值表示「未设」→ 回落 600s，
	// 故显式折成正的最小正时长（`llm-interrupted.auto_continue_delay_ms` 仍为 0，与生产同形）。
	autoContinueDelay := tc.autoContinueDelay
	if autoContinueDelay <= 0 {
		autoContinueDelay = time.Nanosecond
	}
	// 子轮次（父链非空 / 子会话 / 干活子会话）不做自动续写（见 autoContinueOff）。
	autoContinue := tc.engineAutoContinue()
	return chat.RuntimeConfig{
		MaxSteps:      maxSteps,
		EventLogLevel: chat.TurnEventLogNone, // 生产不写 turn_events
		RetryCount:    &retryCount,
		RetryBudget:   tc.retryBudget,
		// 轮内等待异步回执：上限 = 生产看门狗（prj `pending_task_timeout_sec`，缺省 2h）。
		// 看门狗在引擎模式下不再单独起（等待循环本身即兜底：超时合成 failed 占位续轮）。
		PendingWait:        pendingWait,
		PendingPlaceholder: enginePendingPlaceholder,
		PollInterval:       50 * time.Millisecond,
		AutoContinue:       autoContinue,
		AutoContinueDelay:  autoContinueDelay,
		AutoContinueText:   tc.continueText,
		AutoContinueMax:    maxAutoContinue,
	}
}

// engineFirstInput 等待本轮首个输入（复用 `loop` 的输入队列语义）：`llm-send` 投递的消息；
// 或 `Kick`（next_turn 消费：消息已落库、无新输入）。ok=false = 轮次已关（取消 / 关闭）。
//
// 首次等待期间到达的插话 / 异步回执先入 engineInbox（引擎在首个 step 之后统一消费）。
func (tc *turnCtx) engineFirstInput() (ChatMsg, bool) {
	for {
		select {
		case <-tc.done:
			return ChatMsg{}, false
		case m := <-tc.in:
			return m, true
		case <-tc.kick:
			return ChatMsg{}, true
		case item := <-tc.inboxCh:
			tc.engineRouteInsert(item)
		case m := <-tc.asyncDone:
			tc.handleAsyncDone(m)
		}
	}
}

// enginePump 把生产的输入通道转成引擎 Inbox 项（loop 不再直接消费它们）。
//
// 不另起 pending 看门狗：引擎的 `PendingWait` 等待循环对未回填调用合成 failed 占位续轮
// （阈值同源 = `pending_task_timeout_sec`）。
func (tc *turnCtx) enginePump(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-tc.done:
			return
		case m := <-tc.in:
			tc.engineRouteInput(m)
		case item := <-tc.inboxCh:
			tc.engineRouteInsert(item)
		case m := <-tc.asyncDone:
			tc.handleAsyncDone(m)
		}
	}
}

// engineRouteInput 把输入队列的一条消息转成引擎 Inbox 项（唯一消费者 = 引擎）。
//   - 工具结果（异步回执 / ask 回答）：已由 FeedToolResultStatus 落库 → 直接投递 tool_result
//     （同时摘除 inflight 登记，避免 watchdog / 看门狗误判）；
//   - 用户文本：按插话投递（引擎只有 insert 入口；CommitInbox 落库语义同 `tc.persistMessage`）；
//   - 其余（notify / continue）：引擎模式不消费，留痕（生产仅在轮次首个输入前出现）。
func (tc *turnCtx) engineRouteInput(msg ChatMsg) {
	if msg.Role == "tool" {
		tc.clearInflight(msg.ToolCallID)
		tc.engineInbox.Push(engineToolResultItem(msg.ToolCallID, msg.Content, false))
		return
	}
	if msg.Role == "user" && (msg.Kind == "" || msg.Kind == "text") && strings.TrimSpace(msg.Content) != "" {
		tc.engineInbox.Push(chat.InboxItem{Type: chat.InboxTypeInsert, Content: msg.Content})
		return
	}
	logf("[chonkpilot-server] 引擎模式：忽略中途输入 role=%s kind=%s（turn=%s）\n",
		msg.Role, msg.Kind, tc.req.Turn)
}

// engineRouteInsert 消费一条 inbox 项（判据同 inbox 消费语义）：空内容 → discarded；
// 已撤回 → 不消费；非可插入相位（无在飞工具）→ 降级 next_turn；否则投递给引擎建插入帧。
func (tc *turnCtx) engineRouteInsert(item facade.InboxItem) {
	if strings.TrimSpace(item.Content) == "" {
		_ = tc.server.inboxUpdate(tc.req.InstanceID, item.ID, "discarded", "", "")
		tc.server.inboxSetStatus(item.ID, "discarded")
		return
	}
	// 删除优先：用户可能已在入队后撤回（discard）→ 不得再落 message。
	if cur, ok := tc.server.inboxLookup(item.ID); ok && cur.Status != "pending" {
		return
	}
	if !tc.insertable() {
		_ = tc.server.inboxUpdate(tc.req.InstanceID, item.ID, "", "next_turn", "")
		item.Type = "next_turn"
		tc.server.inboxRemember(item)
		return
	}
	tc.engineInbox.Push(chat.InboxItem{ID: item.ID, Type: chat.InboxTypeInsert, Content: item.Content})
}

// engineFinish 按引擎结果收口（**只能有一次终态**：`Server.finish` 的 finishOnce 保证）。
// 映射与生产同口径：length → incomplete（+ 未开启续写时补发中断提示）、取消 → 静默（取消路径
// 已发 interrupted）、错误 → error（code / retryable 同生产）。
func (tc *turnCtx) engineFinish(res chat.TurnResult, err error, text string) {
	switch {
	case err != nil:
		if tc.ctx.Err() != nil {
			return // 取消静默（llm-cancel 已发 interrupted complete）
		}
		if errors.Is(err, chat.ErrMaxSteps) {
			tc.server.llmError(tc, "TOOL_LOOP_LIMIT", "工具调用次数超限")
			tc.Close()
			return
		}
		retryable := retryableErr(err)
		tc.server.llmErrorRetryable(tc, "LLM_REQUEST_FAILED",
			tc.server.subsessionHint(tc, err.Error(), retryable), retryable)
		tc.Close()
		return
	}
	if tc.ctx.Err() != nil {
		return // 取消（同上）
	}
	finish := res.Finish
	if finish == "" {
		finish = "stop"
	}
	if finish == "canceled" {
		return // 等待 / 续写期间取消 → 终态已由取消路径发出
	}
	// 空回复（stop 但无内容）：与生产同口径（不落库、EMPTY_REPLY 错误终态）。
	if strings.TrimSpace(text) == "" && finish != "length" {
		tc.server.llmError(tc, "EMPTY_REPLY", "LLM 返回空回复")
		tc.Close()
		return
	}
	if finish == "length" {
		// 引擎在 AutoContinue 关闭时不发 `session_interrupted`（其设计为续写专用事件）；
		// 生产该分支会发中断提示（delay=0、不带 message）→ 由宿主补发同形载荷。
		// 子轮次强制关闭续写（autoContinueOff）→ 同样由宿主补发。
		if !tc.engineAutoContinue() && text != "" {
			tc.publishInterrupted("LLM_INCOMPLETE", "", true, 0)
		}
		tc.server.complete(tc, "incomplete", finish, text)
		tc.shadowReplayIfEnabled()
		tc.Close()
		return
	}
	tc.server.complete(tc, "complete", finish, text)
	tc.shadowReplayIfEnabled()
	tc.Close()
}

// ─── 端口实现 ─────────────────────────────────────────────────────────

// engineStepLLM 在宿主 LLM step 外包一层「思考链转发」：引擎内核不对外发 reasoning 事件，而生产
// 把 `reasoning_content` 增量以 `llm-receive{type:reason}` 上屏——此处保持同形（正文增量仍走 Sink
// 的 `llm_delta`，两者按到达顺序发布）。
type engineStepLLM struct {
	inner chat.LLMClient
	tc    *turnCtx
}

func (l engineStepLLM) Step(ctx context.Context, req chat.StepRequest, opts chat.StepOptions) (<-chan chat.StepEvent, error) {
	// 最终请求消息列表的思维链回传规则（生产在 tc.msgs() 内应用）：引擎自持 messages 时，
	// 追加进列表的 assistant 段仍带 reasoning → 在此按同一规则归一（幂等，不修改引擎状态）。
	req.Messages = applyReasoningRule(req.Messages)
	ch, err := l.inner.Step(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan chat.StepEvent, 8)
	go func() {
		defer close(out)
		for ev := range ch {
			if ev.Type == chat.StepEventReasoning && ev.Text != "" {
				l.tc.server.notify(l.tc, NotifyTypeReason, map[string]any{"text": ev.Text})
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// engineTurnContext 返回生产同一份逐字上下文（`tc.msgs()`：系统提示词 + 引导 + 本轮已落库消息）。
type engineTurnContext struct{ tc *turnCtx }

func (c engineTurnContext) Build(context.Context, chat.ContextRequest) (chat.Context, error) {
	return chat.Context{Messages: c.tc.msgs()}, nil
}

// engineTurnStore 把引擎写侧接到生产落库：
//   - assistant / 续写 user 消息 → `tc.persistMessage`（主键 / 分段语义不变）；
//   - tool 消息 → **跳过**（结果已由 `dispatchToolCall` / 异步回执路径落库，不重复写）；
//   - inbox 消费 → 先落消息、再置 consumed（内存索引同步）；
//   - 事件日志 no-op（生产不写 `turn_events`）。
type engineTurnStore struct {
	tc *turnCtx
}

func (s *engineTurnStore) SaveMessage(_ context.Context, msg chat.Message) error {
	if msg.Role == "tool" {
		return nil
	}
	return s.tc.persistMessage(msg)
}

func (*engineTurnStore) AppendEvent(context.Context, chat.TurnEvent) error { return nil }

func (s *engineTurnStore) CommitInbox(_ context.Context, inboxID string, msg chat.Message) error {
	if err := s.tc.persistMessage(msg); err != nil {
		return err
	}
	_ = s.tc.server.inboxUpdate(s.tc.req.InstanceID, inboxID, "consumed", "", "")
	if inboxID != "" {
		s.tc.server.inboxSetStatus(inboxID, "consumed")
	}
	return nil
}

// engineTurnSink 把引擎事件映射到既有对外主题（零新增主题）：
//   - `llm_delta` → `llm-receive{type:text}`；`llm_retry` → `llm-retry`；
//   - `session_interrupted` → `llm-interrupted`（code 折回生产口径 LLM_INCOMPLETE / LLM_STREAM_ERROR）；
//   - `turn_complete` / `tool_result` / `tool_progress` / `ask_user` → 有意忽略（终态由
//     `Server.finish` 发；工具事件由落库消息 / 任务树 / ask 卡片承载）。
type engineTurnSink struct{ tc *turnCtx }

func (s engineTurnSink) Emit(_ context.Context, ev chat.Event) {
	switch ev.Type {
	case chat.EventLLMDelta:
		if ev.Text == "" {
			return
		}
		s.tc.server.notify(s.tc, NotifyTypeText, map[string]any{"text": ev.Text})
	case chat.EventLLMRetry:
		if ev.Retry == nil {
			return
		}
		// RETRY-1：与生产 `turnCtx.OnRetry` 同形（含 retryable:true）；retryAttempt 供终态携带。
		s.tc.retryAttempt = ev.Retry.Attempt
		s.tc.server.publish("llm-retry", map[string]any{
			"instance_id": s.tc.req.InstanceID, "session": s.tc.req.Session, "turn": s.tc.req.Turn,
			"retryable":     true,
			"retry_attempt": ev.Retry.Attempt,
			"retry_count":   s.tc.retryCount,
			"retry_wait_ms": ev.Retry.WaitMilli,
		})
	case chat.EventSessionInterrupted:
		if s.tc.ctx.Err() != nil {
			return // 取消不发中断提示（生产同口径）
		}
		code, msg := "LLM_STREAM_ERROR", ev.Text
		if ev.Code == "length" {
			// 生产 length 分支的中断提示不带 message。
			code, msg = "LLM_INCOMPLETE", ""
		}
		s.tc.publishInterrupted(code, msg, ev.Retryable, time.Duration(ev.DelayMilli)*time.Millisecond)
	}
}

// engineTurnAnswer 是终态文本累积器（Lifecycle Hook）：口径与生产 `tc.answerBuilder()` 一致 ——
// 只累计**主帧**（Depth==0）且**无 tool_calls** 的 assistant 段；插入帧正文不进终态文本
// （生产按帧各自累积，插入帧的 answer 在出栈后丢弃）。
type engineTurnAnswer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (a *engineTurnAnswer) BeforeTurn(context.Context, *chat.TurnInfo) (chat.Action, error) {
	return chat.ActionContinue, nil
}

func (a *engineTurnAnswer) AfterTurn(context.Context, *chat.TurnResult) error { return nil }

func (a *engineTurnAnswer) BeforeStep(context.Context, *chat.ExecContext, *chat.StepRequest) (chat.Action, error) {
	return chat.ActionContinue, nil
}

func (a *engineTurnAnswer) AfterStep(_ context.Context, c *chat.ExecContext, res *chat.StepResult) error {
	if c != nil && c.Depth != 0 {
		return nil // 插入帧正文不计入终态文本
	}
	if res == nil || len(res.ToolCalls) > 0 {
		return nil // 工具轮的正文不进终态文本（生产同口径）
	}
	a.mu.Lock()
	a.buf.WriteString(res.Text)
	a.mu.Unlock()
	return nil
}

func (a *engineTurnAnswer) BeforeTool(context.Context, *chat.ExecContext, *chat.ToolCall) (chat.Action, error) {
	return chat.ActionContinue, nil
}

func (a *engineTurnAnswer) AfterTool(context.Context, *chat.ExecContext, *chat.ToolResult) error {
	return nil
}

// text 返回累积的终态文本（跨帧续写为全文）。
func (a *engineTurnAnswer) text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.buf.String()
}

// engineTurnTools 是引擎的工具端口：直接复用生产 `dispatchToolCall`（策略 / 任务树 / 落库 /
// 异步登记全部同源）。
type engineTurnTools struct{ tc *turnCtx }

func (t engineTurnTools) Dispatch(_ context.Context, call chat.ToolCall) (chat.ToolOutcome, error) {
	msg, pending, derr := t.tc.dispatchToolCall(call)
	if pending {
		// ask / 转异步：结果经回执到达（引擎进入 PendingWait）。
		return chat.ToolOutcome{Pending: true}, nil
	}
	return chat.ToolOutcome{Result: chat.ToolResult{
		CallID: call.ID, Content: msg.Content, IsError: derr != nil,
	}}, nil
}

func (engineTurnTools) Cancel(context.Context, string, string) (bool, error) { return false, nil }

func (engineTurnTools) Detach(context.Context, string, string) (string, error) { return "", nil }

// 编译期断言：接管端口的实现满足 chat 端口。
var (
	_ chat.ContextProvider = engineTurnContext{}
	_ chat.TurnStore       = (*engineTurnStore)(nil)
	_ chat.EventSink       = engineTurnSink{}
	_ chat.ToolInvoker     = engineTurnTools{}
)
