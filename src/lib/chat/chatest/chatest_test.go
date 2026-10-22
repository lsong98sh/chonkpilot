// chatest 集成测试（MOD-1 M8-C2 / L2′）：状态机 / 工具循环 / 插话帧栈 / 异步回执 / 重放等价。
// 全部内存，无 MQ / DB / 网络。
package chatest

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	chat "github.com/chonkpilot/chonkpilot-chat"
)

// turnEventTypes 提取事件日志类型序列。
func turnEventTypes(evs []chat.TurnEvent) []chat.TurnEventType {
	out := make([]chat.TurnEventType, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

// sinkEventTypes 提取对外事件类型序列。
func sinkEventTypes(evs []chat.Event) []chat.EventType {
	out := make([]chat.EventType, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

// indexOfTurnEvent 返回某事件类型首次出现的下标（未出现 → -1）。
func indexOfTurnEvent(evs []chat.TurnEvent, typ chat.TurnEventType) int {
	for i, ev := range evs {
		if ev.Type == typ {
			return i
		}
	}
	return -1
}

// hasSinkEvent 报告对外事件是否包含某类型。
func hasSinkEvent(evs []chat.Event, typ chat.EventType) bool {
	for _, ev := range evs {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// TestSingleStep：文本 + stop → completed；一条 assistant 消息；四个事件；delta + turn_complete。
func TestSingleStep(t *testing.T) {
	ctx := context.Background()
	deps := NewDeps("ins", "s1", "sys", []chat.Message{{Role: "user", Content: "hi"}},
		Text("hello", "stop"))
	rt := deps.Engine(chat.RuntimeConfig{})

	res, err := rt.RunTurn(ctx, "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "hello" || rt.State().Status != chat.TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}

	msgs := deps.Store.Messages()
	if len(msgs) != 1 || msgs[0].Role != "assistant" || msgs[0].Content != "hello" {
		t.Fatalf("落库消息错：%+v", msgs)
	}
	wantEvents := []chat.TurnEventType{
		chat.TurnEventStart, chat.TurnEventStepStart, chat.TurnEventStepDone, chat.TurnEventEnd,
	}
	if got := turnEventTypes(deps.Store.Events()); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("事件日志=%+v want %+v", got, wantEvents)
	}

	sink := deps.Sink.Events()
	if len(sink) != 2 || sink[0].Type != chat.EventLLMDelta || sink[1].Type != chat.EventTurnComplete {
		t.Fatalf("EventSink 序列错：%+v", sinkEventTypes(sink))
	}
	if sink[0].Text != "hello" || sink[1].Status != string(chat.TurnStatusCompleted) {
		t.Fatalf("EventSink 载荷错：%+v", sink)
	}

	reqs := deps.LLM.Requests()
	if len(reqs) != 1 || len(reqs[0].Messages) != 2 ||
		reqs[0].Messages[0].Role != "system" || reqs[0].Messages[0].Content != "sys" ||
		reqs[0].Messages[1].Content != "hi" {
		t.Fatalf("StepRequest 错：%+v", reqs)
	}
}

// TestToolLoop：第一次 tool_call、第二次文本 → 2 次 step；tool 消息落库；事件含 dispatch/result。
func TestToolLoop(t *testing.T) {
	ctx := context.Background()
	call := chat.ToolCall{ID: "c1", Name: "f", Arguments: `{"x":1}`}
	deps := NewDeps("ins", "s1", "sys", []chat.Message{{Role: "user", Content: "hi"}},
		ToolStep(call), Text("done", "stop"))
	deps.Tools.OnCall("c1", chat.ToolOutcome{Result: chat.ToolResult{CallID: "c1", Content: "42"}})
	rt := deps.Engine(chat.RuntimeConfig{})

	res, err := rt.RunTurn(ctx, "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "done" || rt.State().Status != chat.TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if n := len(deps.LLM.Requests()); n != 2 {
		t.Fatalf("StepRequest 次数=%d want 2", n)
	}
	if calls := deps.Tools.Calls(); len(calls) != 1 || calls[0].Name != "f" {
		t.Fatalf("Dispatch 调用错：%+v", calls)
	}

	msgs := deps.Store.Messages()
	if len(msgs) != 3 || msgs[0].Role != "assistant" || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("落库消息错：%+v", msgs)
	}
	if msgs[1].Role != "tool" || msgs[1].ToolCallID != "c1" || msgs[1].Content != "42" {
		t.Fatalf("tool 消息错：%+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "done" {
		t.Fatalf("终态 assistant 消息错：%+v", msgs[2])
	}

	events := deps.Store.Events()
	if indexOfTurnEvent(events, chat.TurnEventToolDispatch) < 0 ||
		indexOfTurnEvent(events, chat.TurnEventToolResult) < 0 {
		t.Fatalf("事件日志缺 tool_dispatch / tool_result：%+v", turnEventTypes(events))
	}
	if !hasSinkEvent(deps.Sink.Events(), chat.EventToolResult) {
		t.Fatalf("EventSink 缺 tool_result：%+v", sinkEventTypes(deps.Sink.Events()))
	}
}

// TestInsertFrameStack：主帧 part1 → 插话建帧 → 插入帧 answer → 出栈 → 主帧 done。
func TestInsertFrameStack(t *testing.T) {
	ctx := context.Background()
	deps := NewDeps("ins", "s1", "sys", []chat.Message{{Role: "user", Content: "hi"}},
		Text("part1", "stop"), Text("answer", "stop"), Text("done", "stop"))
	deps.Inbox.Push(chat.InboxItem{ID: "ib1", Type: chat.InboxTypeInsert, Content: "插话", Status: "pending"})
	rt := deps.Engine(chat.RuntimeConfig{})

	res, err := rt.RunTurn(ctx, "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.FinalText != "part1answerdone" || rt.State().Status != chat.TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if res.Frames != 0 || len(rt.State().Frames) != 0 || !deps.Inbox.Empty() {
		t.Fatalf("帧栈 / 队列应收敛：res.Frames=%d state=%+v inbox=%d", res.Frames, rt.State(), deps.Inbox.Len())
	}

	if commits := deps.Store.InboxCommits(); !reflect.DeepEqual(commits, []string{"ib1"}) {
		t.Fatalf("InboxCommits=%+v want [ib1]", commits)
	}
	if n := len(deps.LLM.Requests()); n != 3 {
		t.Fatalf("StepRequest 次数=%d want 3（主帧 / 插入帧 / 主帧续跑）", n)
	}
	if reqs := deps.LLM.Requests(); !containsContent(reqs[1].Messages, "插话") {
		t.Fatalf("插入帧请求应含插话消息：%+v", reqs[1].Messages)
	}

	iInsert := indexOfTurnEvent(deps.Store.Events(), chat.TurnEventInboxInsert)
	iConsume := indexOfTurnEvent(deps.Store.Events(), chat.TurnEventInboxConsume)
	if iInsert < 0 || iConsume < 0 || iInsert > iConsume {
		t.Fatalf("事件日志缺/乱序 inbox_insert(%d) → inbox_consume(%d)：%+v",
			iInsert, iConsume, turnEventTypes(deps.Store.Events()))
	}
}

// TestReplayEquivalent：真实运行事件 → Replay 重建状态与运行终态一致（status / seq / frames）。
func TestReplayEquivalent(t *testing.T) {
	ctx := context.Background()
	call := chat.ToolCall{ID: "c1", Name: "f"}
	deps := NewDeps("ins", "s1", "sys", []chat.Message{{Role: "user", Content: "hi"}},
		ToolStep(call), Text("done", "stop"))
	deps.Tools.OnCall("c1", chat.ToolOutcome{Result: chat.ToolResult{CallID: "c1", Content: "42"}})
	rt := deps.Engine(chat.RuntimeConfig{})

	if _, err := rt.RunTurn(ctx, "t1"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	events := deps.Store.Events()
	st, err := chat.Replay("s1", "t1", events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	run := rt.State()
	if st.Status != run.Status || st.Seq != run.Seq || len(st.Frames) != len(run.Frames) {
		t.Fatalf("重放与终态不一致：\n got=%+v\nwant=%+v", st, run)
	}
	if last := events[len(events)-1]; st.Seq != last.Seq {
		t.Fatalf("Seq=%d 末条事件=%d", st.Seq, last.Seq)
	}
	if st.Status != chat.TurnStatusCompleted {
		t.Fatalf("status=%q want completed", st.Status)
	}
}

// TestPendingToolResult：Pending 工具 → 轮内等待预置回执 → 续轮完成（只派发一次）。
func TestPendingToolResult(t *testing.T) {
	ctx := context.Background()
	call := chat.ToolCall{ID: "c1", Name: "f"}
	deps := NewDeps("ins", "s1", "sys", []chat.Message{{Role: "user", Content: "hi"}},
		ToolStep(call), Text("after", "stop"))
	deps.Tools.OnCall("c1", chat.ToolOutcome{Pending: true, OpID: "op1"})
	deps.Inbox.Push(chat.InboxItem{
		ID: "ib1", Type: chat.InboxTypeToolResult, Status: "pending",
		Result: &chat.ToolResult{CallID: "c1", Content: "ok"},
	})
	rt := deps.Engine(chat.RuntimeConfig{PendingWait: 5 * time.Second, PollInterval: 50 * time.Millisecond})

	res, err := rt.RunTurn(ctx, "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "after" || rt.State().Status != chat.TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if n := len(deps.Tools.Calls()); n != 1 {
		t.Fatalf("Dispatch 次数=%d want 1（不重复派发）", n)
	}
	msgs := deps.Store.Messages()
	if len(msgs) != 3 || msgs[1].Role != "tool" || msgs[1].Content != "ok" {
		t.Fatalf("回执应落库：%+v", msgs)
	}
}

// containsContent 报告消息切片是否含指定正文。
func containsContent(msgs []chat.Message, content string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, content) {
			return true
		}
	}
	return false
}
