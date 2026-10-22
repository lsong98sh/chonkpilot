// 异步工具回执续轮（MOD-1 M6f-2）：Pending 轮内等待 / tool_result 回填 / 超时失败占位 /
// 等待期间插话。
package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ─── 测试桩 ─────────────────────────────────────────────────────────

func toolResultItem(id, callID, content string) InboxItem {
	return InboxItem{
		ID: id, Type: InboxTypeToolResult, Status: "pending",
		Result: &ToolResult{CallID: callID, Content: content},
	}
}

// pendingClock 是等待回执用的确定性假时钟：
//   - 间隔轮询（d < 1s）→ 永不就绪（回执 / 插话在首轮 poll 即命中，无需真实等待）；
//   - 超时（d >= 1s）→ timeoutFires=true 时立即就绪。
type pendingClock struct{ timeoutFires bool }

func (pendingClock) Now() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }

func (c pendingClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	if d >= time.Second && c.timeoutFires {
		ch <- c.Now()
	}
	return ch
}

// ─── 用例 ───────────────────────────────────────────────────────────

// TestEnginePendingWaitResult：Pending → 轮内等待 → 回执到达按同步路径续轮（只派发一次）。
func TestEnginePendingWaitResult(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
		{{Type: StepEventDelta, Text: "after"}, done("stop")},
	}}
	tools := &fakeTools{pending: true}
	sink := &fakeSink{}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, tools, sink, store, RuntimeConfig{
		PendingWait: 5 * time.Second, PollInterval: 50 * time.Millisecond,
	})
	rt.deps.Inbox = &fakeInbox{items: []InboxItem{toolResultItem("ib1", "c1", "ok")}}
	rt.deps.Clock = pendingClock{}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "after" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(tools.calls) != 1 {
		t.Fatalf("Dispatch 次数=%d want 1", len(tools.calls))
	}
	if len(llm.reqs) != 2 {
		t.Fatalf("StepRequest 次数=%d want 2", len(llm.reqs))
	}
	last := llm.reqs[1].Messages
	if len(last) == 0 {
		t.Fatal("第 2 次请求无消息")
	}
	toolMsg := last[len(last)-1]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "c1" || toolMsg.Content != "ok" {
		t.Fatalf("第 2 次请求尾部 tool 消息错：%+v", toolMsg)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Result.Content != "ok" || res.ToolCalls[0].Result.IsError {
		t.Fatalf("工具记录错（不应为超时占位）：%+v", res.ToolCalls)
	}
	var hasResult bool
	for _, e := range sink.events {
		hasResult = hasResult || e.Type == EventToolResult
	}
	if !hasResult {
		t.Fatalf("EventSink 缺 tool_result：%+v", sink.types())
	}
	if len(store.messages) != 3 || store.messages[1].Role != "tool" || store.messages[1].Content != "ok" {
		t.Fatalf("落库消息错：%+v", store.messages)
	}
}

// TestEnginePendingWaitTimeout：无回执 → 超时失败占位回填后续轮（整轮 completed，不中断）。
func TestEnginePendingWaitTimeout(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
		{{Type: StepEventDelta, Text: "after-timeout"}, done("stop")},
	}}
	tools := &fakeTools{pending: true}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, tools, &fakeSink{}, store, RuntimeConfig{
		PendingWait: 5 * time.Second, PollInterval: 50 * time.Millisecond,
	})
	rt.deps.Inbox = &fakeInbox{}
	rt.deps.Clock = pendingClock{timeoutFires: true}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "after-timeout" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(tools.calls) != 1 {
		t.Fatalf("Dispatch 次数=%d want 1", len(tools.calls))
	}
	if len(llm.reqs) != 2 {
		t.Fatalf("StepRequest 次数=%d want 2", len(llm.reqs))
	}
	last := llm.reqs[1].Messages
	toolMsg := last[len(last)-1]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "c1" || !strings.Contains(toolMsg.Content, "超时") {
		t.Fatalf("第 2 次请求尾部应为超时占位 tool 消息：%+v", toolMsg)
	}
	if len(res.ToolCalls) != 1 || !res.ToolCalls[0].Result.IsError ||
		!strings.Contains(res.ToolCalls[0].Result.Content, "超时") {
		t.Fatalf("超时占位结果错：%+v", res.ToolCalls)
	}
}

// TestEnginePendingWaitInsertThenResult：等待期间先插话（插入帧正常执行、CommitInbox 一次），
// 再收到原回执 → 插话已执行且回执被消费。
func TestEnginePendingWaitInsertThenResult(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
		{{Type: StepEventDelta, Text: "插话回答"}, done("stop")},
		{{Type: StepEventDelta, Text: "done"}, done("stop")},
	}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{pending: true}, &fakeSink{}, store, RuntimeConfig{
		PendingWait: 5 * time.Second, PollInterval: 50 * time.Millisecond,
	})
	rt.deps.Inbox = &fakeInbox{items: []InboxItem{
		insertItem("ib1", "插话"),
		toolResultItem("ib2", "c1", "late-ok"),
	}}
	rt.deps.Clock = pendingClock{}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "插话回答done" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(llm.reqs) != 3 {
		t.Fatalf("StepRequest 次数=%d want 3（主帧 / 插入帧 / 主帧续轮）", len(llm.reqs))
	}
	if n := countContent(llm.reqs[1].Messages, "插话"); n != 1 {
		t.Fatalf("插入帧请求应含插话消息：%+v", llm.reqs[1].Messages)
	}
	third := llm.reqs[2].Messages
	if len(third) == 0 || third[0].Role != "system" || third[0].Content != InsertResumeNote {
		t.Fatalf("续轮请求最前应为续接说明：%+v", third)
	}
	toolMsg := third[len(third)-1]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "c1" || toolMsg.Content != "late-ok" {
		t.Fatalf("续轮请求尾部回执错：%+v", toolMsg)
	}
	if len(store.inbox) != 1 || store.inbox[0] != "ib1" {
		t.Fatalf("CommitInbox=%+v want [ib1]", store.inbox)
	}
	if n := countContent(store.messages, "插话"); n != 1 {
		t.Fatalf("插话消息应落库一次：%+v", store.messages)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Result.Content != "late-ok" || res.ToolCalls[0].Result.IsError {
		t.Fatalf("工具记录应为真实回执：%+v", res.ToolCalls)
	}
	if res.Frames != 0 || len(rt.State().Frames) != 0 {
		t.Fatalf("帧栈应收敛：res.Frames=%d state=%+v", res.Frames, rt.State())
	}
}

// TestEngineNestedInsertPendingResult：插入帧**自身**的异步工具回执不得被 `skipInbox` 挡住
// （嵌套插入帧等待期间仍须取件；插话暂存交回外层循环消费）。
func TestEngineNestedInsertPendingResult(t *testing.T) {
	c1 := ToolCall{ID: "c1", Name: "f"}
	c2 := ToolCall{ID: "c2", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &c1}, done("tool_calls", c1)},
		{{Type: StepEventToolCall, ToolCall: &c2}, done("tool_calls", c2)},
		{{Type: StepEventDelta, Text: "insert-done"}, done("stop")},
		{{Type: StepEventDelta, Text: "main-done"}, done("stop")},
	}}
	tools := &fakeTools{pending: true}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, tools, &fakeSink{}, store, RuntimeConfig{
		PendingWait: 5 * time.Second, PollInterval: 50 * time.Millisecond,
	})
	rt.deps.Inbox = &fakeInbox{items: []InboxItem{
		insertItem("ib1", "插话"),
		toolResultItem("ib2", "c2", "r2"),
		toolResultItem("ib3", "c1", "r1"),
	}}
	rt.deps.Clock = pendingClock{}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "insert-donemain-done" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(tools.calls) != 2 {
		t.Fatalf("Dispatch 次数=%d want 2（不得重复派发）", len(tools.calls))
	}
	got := map[string]ToolResult{}
	for _, rec := range res.ToolCalls {
		got[rec.CallID] = rec.Result
	}
	if len(got) != 2 || got["c1"].Content != "r1" || got["c2"].Content != "r2" ||
		got["c1"].IsError || got["c2"].IsError {
		t.Fatalf("工具记录错（嵌套插入帧回执不得走超时占位）：%+v", res.ToolCalls)
	}
	if len(store.inbox) != 1 || store.inbox[0] != "ib1" {
		t.Fatalf("CommitInbox=%+v want [ib1]", store.inbox)
	}
	if res.Frames != 0 || len(rt.State().Frames) != 0 {
		t.Fatalf("帧栈应收敛：res.Frames=%d state=%+v", res.Frames, rt.State())
	}
}

// TestEnginePendingPlaceholderAndHeldResult：同一 step 两个异步调用——等待期间插入占位 tool 消息
// （就地回填、不重复），后发先至的回执先暂存、轮到该调用时取用（不丢弃、不超时）。
func TestEnginePendingPlaceholderAndHeldResult(t *testing.T) {
	c1 := ToolCall{ID: "c1", Name: "f"}
	c2 := ToolCall{ID: "c2", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &c1}, {Type: StepEventToolCall, ToolCall: &c2}, done("tool_calls", c1, c2)},
		{{Type: StepEventDelta, Text: "after"}, done("stop")},
	}}
	tools := &fakeTools{pending: true}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, tools, &fakeSink{}, store, RuntimeConfig{
		PendingWait: 5 * time.Second, PollInterval: 50 * time.Millisecond,
		PendingPlaceholder: "（工具执行中，结果稍后返回）",
	})
	// 回执顺序与派发顺序相反（c2 先到）→ c2 必须被暂存并在其等待循环里取用。
	rt.deps.Inbox = &fakeInbox{items: []InboxItem{
		toolResultItem("ib2", "c2", "r2"),
		toolResultItem("ib1", "c1", "r1"),
	}}
	rt.deps.Clock = pendingClock{}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "after" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(tools.calls) != 2 {
		t.Fatalf("Dispatch 次数=%d want 2（不得重复派发）", len(tools.calls))
	}
	if len(res.ToolCalls) != 2 || res.ToolCalls[0].Result.Content != "r1" || res.ToolCalls[1].Result.Content != "r2" {
		t.Fatalf("工具记录错（回执应各归其调用、均非超时占位）：%+v", res.ToolCalls)
	}
	// 第 2 次请求：每个调用恰一条 tool 消息（占位被就地回填，不产生第二条）。
	last := llm.reqs[1].Messages
	var c1Msgs, c2Msgs int
	for _, m := range last {
		if m.Role != "tool" {
			continue
		}
		switch m.ToolCallID {
		case "c1":
			c1Msgs++
			if m.Content != "r1" {
				t.Fatalf("c1 占位未回填真实结果：%+v", m)
			}
		case "c2":
			c2Msgs++
			if m.Content != "r2" {
				t.Fatalf("c2 占位未回填真实结果：%+v", m)
			}
		}
	}
	if c1Msgs != 1 || c2Msgs != 1 {
		t.Fatalf("tool 消息数 c1=%d c2=%d want 各 1：%+v", c1Msgs, c2Msgs, last)
	}
}

// TestEnginePendingNoWaitKeepsOldBehavior：PendingWait=0 → 立即返回 pending，无额外 step、不轮询 inbox。
func TestEnginePendingNoWaitKeepsOldBehavior(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
		{{Type: StepEventDelta, Text: "should-not-run"}, done("stop")},
	}}
	inbox := &fakeInbox{items: []InboxItem{toolResultItem("ib1", "c1", "ok")}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{pending: true}, &fakeSink{}, store, RuntimeConfig{})
	rt.deps.Inbox = inbox

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "pending" || rt.State().Status != TurnStatusAwaitingTool {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(llm.reqs) != 1 {
		t.Fatalf("StepRequest 次数=%d want 1", len(llm.reqs))
	}
	if inbox.polls != 0 {
		t.Fatalf("旧行为不应轮询 inbox：polls=%d", inbox.polls)
	}
	if len(store.messages) != 1 { // 只有 assistant（tool_call）
		t.Fatalf("挂起时不应落 tool 消息：%+v", store.messages)
	}
}
