// 引擎内核（MOD-1 M5）：step / 工具循环 / 状态机 / 事件 / 日志 / 影子。
package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ─── 测试桩 ─────────────────────────────────────────────────────────

type fakeLLM struct {
	scripts [][]StepEvent
	reqs    []StepRequest
	err     error
}

func (f *fakeLLM) Step(_ context.Context, req StepRequest, _ StepOptions) (<-chan StepEvent, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.reqs) - 1
	ch := make(chan StepEvent, 16)
	if idx < len(f.scripts) {
		for _, ev := range f.scripts[idx] {
			ch <- ev
		}
	} else {
		ch <- StepEvent{Type: StepEventDone, Finish: "stop"}
	}
	close(ch)
	return ch, nil
}

type fakeTools struct {
	result  ToolResult
	pending bool
	fail    error
	calls   []ToolCall
}

func (f *fakeTools) Dispatch(_ context.Context, call ToolCall) (ToolOutcome, error) {
	f.calls = append(f.calls, call)
	if f.fail != nil {
		return ToolOutcome{}, f.fail
	}
	if f.pending {
		return ToolOutcome{Pending: true, OpID: "op-1"}, nil
	}
	res := f.result
	if res.CallID == "" {
		res.CallID = call.ID
	}
	return ToolOutcome{Result: res}, nil
}

func (f *fakeTools) Cancel(context.Context, string, string) (bool, error)   { return false, nil }
func (f *fakeTools) Detach(context.Context, string, string) (string, error) { return "", nil }

type fakeCatalog struct{ tools []ToolSpec }

func (f *fakeCatalog) Servers(context.Context, string) ([]ServerInfo, error) { return nil, nil }
func (f *fakeCatalog) Tools(context.Context, string) ([]ToolSpec, error)     { return f.tools, nil }

type fakeSink struct{ events []Event }

func (f *fakeSink) Emit(_ context.Context, ev Event) { f.events = append(f.events, ev) }

func (f *fakeSink) types() []EventType {
	out := make([]EventType, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

type fakeContext struct {
	c   Context
	err error
}

func (f *fakeContext) Build(context.Context, ContextRequest) (Context, error) { return f.c, f.err }

// newEngine 构造带默认桩的引擎。
func newEngine(t *testing.T, llm LLMClient, tools *fakeTools, sink *fakeSink, store *fakeTurnStore, cfg RuntimeConfig) *Runtime {
	t.Helper()
	return New(Deps{
		InstanceID: "ins", SessionID: "s1",
		Context:   &fakeContext{c: Context{SystemPrompt: "sys", Messages: []Message{{Role: "user", Content: "hi"}}}},
		Store:     store,
		Sink:      sink,
		Tools:     tools,
		Catalog:   &fakeCatalog{tools: []ToolSpec{{Name: "f"}}},
		LLM:       llm,
		Clock:     systemClock{},
		Logger:    discardLogger(),
		Lifecycle: NewLifecycle(discardLogger()),
		Config:    cfg,
	})
}

func done(finish string, calls ...ToolCall) StepEvent {
	return StepEvent{Type: StepEventDone, Finish: finish, ToolCalls: calls}
}

// ─── 用例 ───────────────────────────────────────────────────────────

// TestEngineSingleStep：无工具调用 → 一条 assistant 消息 + completed 终态。
func TestEngineSingleStep(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{{
		{Type: StepEventDelta, Text: "hello"},
		{Type: StepEventReasoning, Text: "think"},
		done("stop"),
	}}}
	sink := &fakeSink{}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, sink, store, RuntimeConfig{})

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "hello" {
		t.Fatalf("result=%+v", res)
	}
	if rt.State().Status != TurnStatusCompleted || rt.State().Seq == 0 {
		t.Fatalf("state=%+v", rt.State())
	}
	if len(llm.reqs) != 1 || len(llm.reqs[0].Messages) != 2 || llm.reqs[0].Messages[0].Role != "system" {
		t.Fatalf("StepRequest 上下文错：%+v", llm.reqs)
	}
	if len(store.messages) != 1 || store.messages[0].Content != "hello" || store.messages[0].Reasoning != "think" {
		t.Fatalf("落库消息错：%+v", store.messages)
	}
	if got := sink.types(); len(got) != 2 || got[0] != EventLLMDelta || got[1] != EventTurnComplete {
		t.Fatalf("事件序列错：%+v", got)
	}
	wantSeq := []TurnEventType{TurnEventStart, TurnEventStepStart, TurnEventStepDone, TurnEventEnd}
	if len(store.events) != len(wantSeq) {
		t.Fatalf("事件日志=%+v", store.events)
	}
	for i, ev := range store.events {
		if ev.Type != wantSeq[i] || ev.Seq != uint64(i+1) {
			t.Fatalf("事件日志[%d]=%+v want %s seq=%d", i, ev, wantSeq[i], i+1)
		}
	}
}

// TestEngineToolLoop：工具调用 → 回执入上下文 → 下一步完成。
func TestEngineToolLoop(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f", Arguments: `{"x":1}`}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{
			{Type: StepEventToolCall, ToolCall: &call},
			done("tool_calls", call),
		},
		{
			{Type: StepEventDelta, Text: "done"},
			done("stop"),
		},
	}}
	tools := &fakeTools{result: ToolResult{Content: "42"}}
	sink := &fakeSink{}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, tools, sink, store, RuntimeConfig{})

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || len(res.ToolCalls) != 1 || res.ToolCalls[0].CallID != "c1" {
		t.Fatalf("result=%+v", res)
	}
	if len(tools.calls) != 1 || tools.calls[0].Name != "f" {
		t.Fatalf("工具派发=%+v", tools.calls)
	}
	// 第二次请求：system / user / assistant(tool_call) / tool
	if len(llm.reqs) != 2 || len(llm.reqs[1].Messages) != 4 {
		t.Fatalf("第二次 StepRequest 消息数错：%+v", llm.reqs[1].Messages)
	}
	last := llm.reqs[1].Messages
	if last[2].Role != "assistant" || len(last[2].ToolCalls) != 1 || last[3].Role != "tool" || last[3].Content != "42" {
		t.Fatalf("第二次请求上下文错：%+v", last)
	}
	// 落库：assistant(tool_call) / tool / assistant(text)
	if len(store.messages) != 3 || store.messages[1].Role != "tool" || store.messages[1].ToolCallID != "c1" {
		t.Fatalf("落库消息错：%+v", store.messages)
	}
	// 事件日志含 tool_dispatch / tool_result
	var hasDispatch, hasResult bool
	for _, ev := range store.events {
		hasDispatch = hasDispatch || ev.Type == TurnEventToolDispatch
		hasResult = hasResult || ev.Type == TurnEventToolResult
	}
	if !hasDispatch || !hasResult {
		t.Fatalf("事件日志缺工具事件：%+v", store.events)
	}
	var hasToolEvent bool
	for _, e := range sink.events {
		hasToolEvent = hasToolEvent || e.Type == EventToolResult
	}
	if !hasToolEvent {
		t.Fatalf("EventSink 缺 tool_result：%+v", sink.events)
	}
}

// TestEnginePendingTool：工具挂起 → pending 返回 + awaiting_tool，不落 tool 消息。
func TestEnginePendingTool(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "ask_user"}
	llm := &fakeLLM{scripts: [][]StepEvent{{
		{Type: StepEventToolCall, ToolCall: &call},
		done("tool_calls", call),
	}}}
	tools := &fakeTools{pending: true}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, tools, &fakeSink{}, store, RuntimeConfig{})

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "pending" || rt.State().Status != TurnStatusAwaitingTool {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(store.messages) != 1 { // 只有 assistant（tool_call）
		t.Fatalf("挂起时不应落 tool 消息：%+v", store.messages)
	}
}

// TestEngineMaxSteps：超过上限 → ErrMaxSteps + failed 终态。
func TestEngineMaxSteps(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	script := []StepEvent{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)}
	llm := &fakeLLM{scripts: [][]StepEvent{script, script, script}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{result: ToolResult{Content: "ok"}}, &fakeSink{}, store, RuntimeConfig{MaxSteps: 2})

	_, err := rt.RunTurn(context.Background(), "t1")
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err=%v want ErrMaxSteps", err)
	}
	if len(llm.reqs) != 2 || rt.State().Status != TurnStatusFailed {
		t.Fatalf("reqs=%d state=%+v", len(llm.reqs), rt.State())
	}
}

// TestEngineEventLogNone：none 级别不写事件日志，但状态仍推进。
func TestEngineEventLogNone(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{{{Type: StepEventDelta, Text: "x"}, done("stop")}}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{EventLogLevel: TurnEventLogNone})

	if _, err := rt.RunTurn(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 0 {
		t.Fatalf("none 级别不应写日志：%+v", store.events)
	}
	if rt.State().Status != TurnStatusCompleted || rt.State().Seq == 0 {
		t.Fatalf("状态未推进：%+v", rt.State())
	}
}

// TestEngineShadowReplay：同一脚本两次运行 → StepRequest / 写入序列逐项相等。
func TestEngineShadowReplay(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	build := func() (*fakeLLM, *Recorder) {
		return &fakeLLM{scripts: [][]StepEvent{
			{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
			{{Type: StepEventDelta, Text: "done"}, done("stop")},
		}}, NewRecorder()
	}

	llm1, rec1 := build()
	store1 := &fakeTurnStore{}
	rt1 := newEngine(t, llm1, &fakeTools{result: ToolResult{Content: "42"}}, &fakeSink{}, store1, RuntimeConfig{})
	rt1.deps.Shadow = rec1
	rt1.deps.Clock = fixedClock{t: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	shadow1 := &ShadowTurnStore{Inner: rt1.deps.Store, Rec: rec1}
	rt1.deps.Store = shadow1
	if _, err := rt1.RunTurn(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}

	llm2, rec2 := build()
	store2 := &fakeTurnStore{}
	rt2 := newEngine(t, llm2, &fakeTools{result: ToolResult{Content: "42"}}, &fakeSink{}, store2, RuntimeConfig{})
	rt2.deps.Shadow = rec2
	rt2.deps.Clock = fixedClock{t: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	rt2.deps.Store = &ShadowTurnStore{Inner: rt2.deps.Store, Rec: rec2}
	if _, err := rt2.RunTurn(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}

	if err := Verify(rec1.Trace(), rec2.Trace()); err != nil {
		t.Fatalf("影子重放不等价：%v", err)
	}
	if s, w := rec1.Len(); s != 2 || w == 0 {
		t.Fatalf("影子轨迹规模 steps=%d writes=%d", s, w)
	}
}

// TestRunSubTurn：子轮次不落库、不发事件。
func TestRunSubTurn(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{{{Type: StepEventDelta, Text: "sub"}, done("stop")}}}
	store := &fakeTurnStore{}
	sink := &fakeSink{}
	rt := newEngine(t, llm, &fakeTools{}, sink, store, RuntimeConfig{})

	res, err := rt.RunSubTurn(context.Background(), SubTurnRequest{
		SessionID: "sub-s", TurnID: "sub-t",
		Messages: []Message{{Role: "user", Content: "问"}},
		Tools:    []ToolSpec{{Name: "g"}},
	})
	if err != nil {
		t.Fatalf("RunSubTurn: %v", err)
	}
	if res.FinalText != "sub" || res.SessionID != "sub-s" {
		t.Fatalf("result=%+v", res)
	}
	if len(store.messages) != 0 || len(store.events) != 0 || len(sink.events) != 0 {
		t.Fatalf("子轮次不应落库/发事件：msgs=%d events=%d sink=%d", len(store.messages), len(store.events), len(sink.events))
	}
	if len(llm.reqs) != 1 || len(llm.reqs[0].Messages) != 1 || llm.reqs[0].Messages[0].Content != "问" {
		t.Fatalf("子轮次上下文错：%+v", llm.reqs)
	}
}

// TestEngineHookDropTool：BeforeTool 返回 Drop → 不派发，但仍回填 tool 消息并完成。
func TestEngineHookDropTool(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
		{{Type: StepEventDelta, Text: "ok"}, done("stop")},
	}}
	tools := &fakeTools{result: ToolResult{Content: "should-not-run"}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, tools, &fakeSink{}, store, RuntimeConfig{})
	var calls []string
	rt.deps.Lifecycle.Register(&recordHook{name: "h", calls: &calls, bt: ActionDrop}, 0, FailOpen)

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("Drop 后不应派发：%+v", tools.calls)
	}
	if res.Finish != "stop" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(store.messages) != 3 || !strings.Contains(store.messages[1].Content, "skipped") {
		t.Fatalf("应回填 skipped 结果：%+v", store.messages)
	}
}

// TestEngineStreamWithoutDone：流未以 Done 结束 → 显式失败。
func TestEngineStreamWithoutDone(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{{{Type: StepEventDelta, Text: "partial"}}}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{})

	_, err := rt.RunTurn(context.Background(), "t1")
	if err == nil || !strings.Contains(err.Error(), "without done") {
		t.Fatalf("err=%v", err)
	}
	if rt.State().Status != TurnStatusFailed {
		t.Fatalf("state=%+v", rt.State())
	}
}

// TestEngineLLMError：Step 返回错误 → failed 终态 + 错误透传。
func TestEngineLLMError(t *testing.T) {
	llm := &fakeLLM{err: errors.New("boom")}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, &fakeTurnStore{}, RuntimeConfig{})

	_, err := rt.RunTurn(context.Background(), "t1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
	if rt.State().Status != TurnStatusFailed {
		t.Fatalf("state=%+v", rt.State())
	}
}

// TestEngineClockUsed：事件时间来自 Clock（测试可注入确定性时钟）。
func TestEngineClockUsed(t *testing.T) {
	fixed := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	llm := &fakeLLM{scripts: [][]StepEvent{{{Type: StepEventDelta, Text: "x"}, done("stop")}}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{})
	rt.deps.Clock = fixedClock{t: fixed}

	if _, err := rt.RunTurn(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}
	for _, ev := range store.events {
		if !ev.At.Equal(fixed) {
			t.Fatalf("事件时间未取 Clock：%v", ev.At)
		}
	}
}

// TestEngineRetryEvent（M6e）：宿主配置注入 StepOptions；OnRetry → EventLLMRetry（可见重试）。
// 报文级重试仍归 router，引擎只负责把回调折成事件，turn 终态不受影响。
func TestEngineRetryEvent(t *testing.T) {
	llm := &retryLLM{
		fakeLLM: fakeLLM{scripts: [][]StepEvent{{{Type: StepEventDelta, Text: "ok"}, done("stop")}}},
		attempt: 2, wait: 1500 * time.Millisecond,
	}
	sink := &fakeSink{}
	store := &fakeTurnStore{}
	retryCount := 3
	rt := newEngine(t, llm, &fakeTools{}, sink, store, RuntimeConfig{
		MaxSteps: 4, RetryCount: &retryCount, RetryBudget: 2 * time.Second,
	})

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "ok" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}

	if len(llm.opts) != 1 {
		t.Fatalf("StepOptions 记录数=%d want 1", len(llm.opts))
	}
	if got := llm.opts[0]; got.MaxRetries == nil || *got.MaxRetries != 3 || got.RetryBudget != 2*time.Second || got.OnRetry == nil {
		t.Fatalf("StepOptions=%+v want MaxRetries=3 / RetryBudget=2s / OnRetry!=nil", got)
	}

	var retry *Event
	for i := range sink.events {
		if sink.events[i].Type == EventLLMRetry {
			retry = &sink.events[i]
			break
		}
	}
	if retry == nil || retry.Retry == nil {
		t.Fatalf("EventSink 缺 llm_retry：%+v", sink.events)
	}
	if retry.Retry.Attempt != 2 || retry.Retry.Count != 3 || retry.Retry.WaitMilli != 1500 {
		t.Fatalf("RetryInfo=%+v want attempt=2 count=3 wait_ms=1500", *retry.Retry)
	}
	if retry.SessionID != "s1" || retry.TurnID != "t1" {
		t.Fatalf("事件归属错：%+v", retry)
	}
}

// retryLLM 在返回流之前上报一次可见重试，并记录收到的 StepOptions。
type retryLLM struct {
	fakeLLM
	opts    []StepOptions
	attempt int
	wait    time.Duration
}

func (f *retryLLM) Step(ctx context.Context, req StepRequest, opts StepOptions) (<-chan StepEvent, error) {
	f.opts = append(f.opts, opts)
	if opts.OnRetry != nil {
		opts.OnRetry(f.attempt, f.wait)
	}
	return f.fakeLLM.Step(ctx, req, opts)
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time                         { return c.t }
func (c fixedClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
