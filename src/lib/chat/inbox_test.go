// 插话帧栈（MOD-1 M6f-1）：Inbox 端口 → LIFO 插入帧 → 一次性续接说明。
package chat

import (
	"context"
	"fmt"
	"testing"
)

// ─── 测试桩 ─────────────────────────────────────────────────────────

// fakeInbox 是预置队列的非阻塞 Inbox 桩（Poll 逐条弹出；空 → ok=false）。
type fakeInbox struct {
	items []InboxItem
	polls int
}

func (f *fakeInbox) Poll(context.Context) (InboxItem, bool, error) {
	f.polls++
	if len(f.items) == 0 {
		return InboxItem{}, false, nil
	}
	item := f.items[0]
	f.items = f.items[1:]
	return item, true, nil
}

func insertItem(id, content string) InboxItem {
	return InboxItem{ID: id, Type: InboxTypeInsert, Content: content, Status: "pending"}
}

// countContent 统计消息切片中指定内容的条数（续接说明「只出现一次」判据）。
func countContent(msgs []Message, content string) int {
	n := 0
	for _, m := range msgs {
		if m.Content == content {
			n++
		}
	}
	return n
}

// ─── 用例 ───────────────────────────────────────────────────────────

// TestEngineInsertFrame：主帧 step1 → 插话建帧（LIFO）→ 插入帧回答 → 出栈 →
// 主帧下一次请求最前面携带一次性续接说明 → 收口 completed。
func TestEngineInsertFrame(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventDelta, Text: "part1"}, done("stop")},
		{{Type: StepEventDelta, Text: "answer"}, done("stop")},
		{{Type: StepEventDelta, Text: "done"}, done("stop")},
	}}
	sink := &fakeSink{}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, sink, store, RuntimeConfig{})
	rt.deps.Inbox = &fakeInbox{items: []InboxItem{insertItem("ib1", "插话")}}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "part1answerdone" {
		t.Fatalf("result=%+v", res)
	}
	if rt.State().Status != TurnStatusCompleted || res.Frames != 0 || len(rt.State().Frames) != 0 {
		t.Fatalf("终态/帧栈错：res.Frames=%d state=%+v", res.Frames, rt.State())
	}

	// StepRequest 序列 = 3 次：主帧 → 插入帧 → 主帧（续接说明只在第 3 次出现一次）。
	if len(llm.reqs) != 3 {
		t.Fatalf("StepRequest 次数=%d want 3", len(llm.reqs))
	}
	if n := countContent(llm.reqs[0].Messages, "插话"); n != 0 {
		t.Fatalf("第 1 次请求不应含插入消息：%+v", llm.reqs[0].Messages)
	}
	if n := countContent(llm.reqs[1].Messages, "插话"); n != 1 {
		t.Fatalf("第 2 次（插入帧）请求应含插入的 user 消息：%+v", llm.reqs[1].Messages)
	}
	third := llm.reqs[2].Messages
	if len(third) == 0 || third[0].Role != "system" || third[0].Content != InsertResumeNote {
		t.Fatalf("第 3 次请求最前应为续接说明：%+v", third)
	}
	if n := countContent(third, InsertResumeNote); n != 1 {
		t.Fatalf("续接说明应只出现一次（一次性）：%d", n)
	}

	// 落库：CommitInbox 一次（item ID + user 消息同事务）。
	if len(store.inbox) != 1 || store.inbox[0] != "ib1" {
		t.Fatalf("CommitInbox=%+v want [ib1]", store.inbox)
	}
	if n := countContent(store.messages, "插话"); n != 1 {
		t.Fatalf("插入消息应落库一次：%+v", store.messages)
	}

	// 事件日志：inbox_insert 在 inbox_consume 之前。
	iInsert, iConsume := -1, -1
	for i, ev := range store.events {
		if ev.Type == TurnEventInboxInsert && iInsert < 0 {
			iInsert = i
		}
		if ev.Type == TurnEventInboxConsume && iConsume < 0 {
			iConsume = i
		}
	}
	if iInsert < 0 || iConsume < 0 || iInsert > iConsume {
		t.Fatalf("事件日志缺/乱序 inbox_insert(%d) → inbox_consume(%d)：%+v", iInsert, iConsume, store.events)
	}
}

// TestEngineInsertDepthLimit：帧深上限 5（独立于 MaxSteps）→ 第 6 条不消费，整轮仍 completed。
func TestEngineInsertDepthLimit(t *testing.T) {
	items := make([]InboxItem, 0, 6)
	for i := 1; i <= 6; i++ {
		items = append(items, insertItem(fmt.Sprintf("ib%d", i), fmt.Sprintf("c%d", i)))
	}
	llm := &fakeLLM{} // 默认脚本：每步 done(stop)，无文本无工具
	store := &fakeTurnStore{}
	inbox := &fakeInbox{items: items}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{})
	rt.deps.Inbox = inbox

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if rt.State().Status != TurnStatusCompleted || res.Frames != 0 || len(rt.State().Frames) != 0 {
		t.Fatalf("终态/帧栈错：state=%+v", rt.State())
	}
	if len(store.inbox) != maxInsertDepth {
		t.Fatalf("CommitInbox 次数=%d want %d（%+v）", len(store.inbox), maxInsertDepth, store.inbox)
	}
	for i, id := range store.inbox {
		if want := fmt.Sprintf("ib%d", i+1); id != want {
			t.Fatalf("第 %d 条消费=%q want %q", i+1, id, want)
		}
	}
}

// TestEngineInsertEmptyAndNonInsertIgnored：空内容 / next_turn / tool_result 一律忽略
// （不 CommitInbox、不建帧）。
func TestEngineInsertEmptyAndNonInsertIgnored(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
		{{Type: StepEventDelta, Text: "ok"}, done("stop")},
	}}
	store := &fakeTurnStore{}
	inbox := &fakeInbox{items: []InboxItem{
		{ID: "a", Type: InboxTypeInsert, Content: "  \n\t ", Status: "pending"},
		{ID: "b", Type: "next_turn", Content: "later", Status: "pending"},
		{ID: "c", Type: "tool_result", Content: `{"ok":true}`, Status: "pending"},
	}}
	rt := newEngine(t, llm, &fakeTools{result: ToolResult{Content: "42"}}, &fakeSink{}, store, RuntimeConfig{})
	rt.deps.Inbox = inbox

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.FinalText != "ok" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(store.inbox) != 0 {
		t.Fatalf("忽略项不应 CommitInbox：%+v", store.inbox)
	}
	if len(rt.State().Frames) != 0 {
		t.Fatalf("忽略项不应建帧：%+v", rt.State().Frames)
	}
	if len(llm.reqs) != 2 {
		t.Fatalf("StepRequest 次数=%d want 2（工具环）", len(llm.reqs))
	}
}

// TestEngineNilInboxUnchanged：Deps.Inbox=nil 时既有单步行为不变（零额外事件 / 零额外请求）。
func TestEngineNilInboxUnchanged(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{{{Type: StepEventDelta, Text: "hello"}, done("stop")}}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{})
	if rt.deps.Inbox != nil {
		t.Fatalf("默认 Inbox 应为 nil")
	}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.FinalText != "hello" || res.Finish != "stop" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
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
	if len(llm.reqs) != 1 || len(store.messages) != 1 {
		t.Fatalf("reqs=%d messages=%d want 1/1", len(llm.reqs), len(store.messages))
	}
}
