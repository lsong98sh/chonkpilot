// 自动续写编排（MOD-1 M6f-3 / RETRY-2）：`finish_reason=length` / 已产生文本后断流 →
// `session_interrupted` + 宽限期等待 → 同一 turn 内追加「继续」；宽限期内用户输入 → 取消。
package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ─── 测试桩 ─────────────────────────────────────────────────────────

// continueClock 是续写宽限期用的确定性假时钟：按 duration 精确控制哪些等待立即就绪
// （同一时钟会被宽限期与 Inbox 轮询间隔复用，故不能实现成「所有 After 都立即就绪」）。
type continueClock struct {
	now  time.Time
	fire map[time.Duration]bool
}

func newContinueClock(fire ...time.Duration) *continueClock {
	c := &continueClock{
		now:  time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
		fire: map[time.Duration]bool{},
	}
	for _, d := range fire {
		c.fire[d] = true
	}
	return c
}

func (c *continueClock) Now() time.Time { return c.now }

func (c *continueClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	if c.fire[d] {
		ch <- c.now
	}
	return ch
}

// doneErr 是带流错误的 Done 事件（已连接后断流）。
func doneErr(err error, calls ...ToolCall) StepEvent {
	return StepEvent{Type: StepEventDone, Err: err, ToolCalls: calls}
}

// lastMessage 取消息切片最后一条（空切片返回零值）。
func lastMessage(msgs []Message) Message {
	if len(msgs) == 0 {
		return Message{}
	}
	return msgs[len(msgs)-1]
}

// countKind 统计指定 Kind 的消息条数。
func countKind(msgs []Message, kind string) int {
	n := 0
	for _, m := range msgs {
		if m.Kind == kind {
			n++
		}
	}
	return n
}

func hasTurnEvent(events []TurnEvent, typ TurnEventType) bool {
	for _, ev := range events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

func findEvent(sink *fakeSink, typ EventType) *Event {
	for i := range sink.events {
		if sink.events[i].Type == typ {
			return &sink.events[i]
		}
	}
	return nil
}

// ─── 用例 ───────────────────────────────────────────────────────────

// TestEngineAutoContinueOnLength：step1 `length` + "part1" → 宽限期到期 → 同一 turn 内追加
// user「继续」→ step2 正常收口；续写消息落库、终态 completed、FinalText 含两段。
func TestEngineAutoContinueOnLength(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventDelta, Text: "part1"}, done("length")},
		{{Type: StepEventDelta, Text: "done"}, done("stop")},
	}}
	sink := &fakeSink{}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, sink, store, RuntimeConfig{
		AutoContinue: true, AutoContinueDelay: 10 * time.Millisecond,
	})
	rt.deps.Clock = newContinueClock(10 * time.Millisecond)

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "stop" || res.FinalText != "part1done" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}

	// 第二次 StepRequest 末尾 = 续写 user 消息。
	if len(llm.reqs) != 2 {
		t.Fatalf("StepRequest 次数=%d want 2", len(llm.reqs))
	}
	tail := lastMessage(llm.reqs[1].Messages)
	if tail.Role != "user" || tail.Kind != continueMessageKind || tail.Content != defaultContinueText {
		t.Fatalf("第 2 次请求尾部应为续写 user 消息：%+v", tail)
	}

	// 续写消息经 SaveMessage 落库一次；部分 assistant 文本也已落库。
	if n := countKind(store.messages, continueMessageKind); n != 1 {
		t.Fatalf("续写消息落库次数=%d want 1：%+v", n, store.messages)
	}
	if n := countContent(store.messages, "part1"); n != 1 {
		t.Fatalf("部分 assistant 文本应落库一次：%+v", store.messages)
	}

	// 事件日志含 continue；EventSink 收到 session_interrupted（带部分文本）。
	if !hasTurnEvent(store.events, TurnEventContinue) {
		t.Fatalf("事件日志缺 continue：%+v", store.events)
	}
	interrupted := findEvent(sink, EventSessionInterrupted)
	if interrupted == nil || interrupted.Text != "part1" {
		t.Fatalf("EventSink session_interrupted 错：%+v", sink.events)
	}
}

// TestEngineAutoContinueOnce：连续两次 `length` + AutoContinueMax=1 → 第二次中断后不再续写，
// 按 length 语义 completed；StepRequest 恰为 2 次。
func TestEngineAutoContinueOnce(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventDelta, Text: "p1"}, done("length")},
		{{Type: StepEventDelta, Text: "p2"}, done("length")},
		{{Type: StepEventDelta, Text: "should-not-run"}, done("stop")},
	}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{
		AutoContinue: true, AutoContinueDelay: 10 * time.Millisecond, AutoContinueMax: 1,
	})
	rt.deps.Clock = newContinueClock(10 * time.Millisecond)

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "length" || res.FinalText != "p1p2" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(llm.reqs) != 2 {
		t.Fatalf("StepRequest 次数=%d want 2", len(llm.reqs))
	}
	if n := countKind(store.messages, continueMessageKind); n != 1 {
		t.Fatalf("续写次数=%d want 1：%+v", n, store.messages)
	}
}

// TestEngineAutoContinueCancelledByInsert：宽限期内 Inbox 返回 insert → 插话按 M6f-1 执行
// （CommitInbox 一次），**不追加**「继续」，终态正常。
func TestEngineAutoContinueCancelledByInsert(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventDelta, Text: "part1"}, done("length")},
		{{Type: StepEventDelta, Text: "插话回答"}, done("stop")},
		{{Type: StepEventDelta, Text: "done"}, done("stop")},
	}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{
		AutoContinue: true, AutoContinueDelay: 10 * time.Millisecond,
	})
	rt.deps.Clock = newContinueClock(10 * time.Millisecond)
	rt.deps.Inbox = &fakeInbox{items: []InboxItem{insertItem("ib1", "插话")}}

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.FinalText != "part1插话回答done" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(llm.reqs) != 3 {
		t.Fatalf("StepRequest 次数=%d want 3（主帧 / 插入帧 / 主帧续跑）", len(llm.reqs))
	}
	if n := countContent(llm.reqs[1].Messages, "插话"); n != 1 {
		t.Fatalf("插入帧请求应含插话消息：%+v", llm.reqs[1].Messages)
	}
	if first := llm.reqs[2].Messages[0]; first.Role != "system" || first.Content != InsertResumeNote {
		t.Fatalf("续跑请求最前应为续接说明：%+v", first)
	}
	if len(store.inbox) != 1 || store.inbox[0] != "ib1" {
		t.Fatalf("CommitInbox=%+v want [ib1]", store.inbox)
	}
	if n := countKind(store.messages, continueMessageKind); n != 0 {
		t.Fatalf("用户已介入，不应追加「继续」：%+v", store.messages)
	}
	for _, req := range llm.reqs {
		if countKind(req.Messages, continueMessageKind) != 0 {
			t.Fatalf("请求中不应出现续写消息：%+v", req.Messages)
		}
	}
}

// TestEngineAutoContinueDisabled：AutoContinue=false（零值）+ length → 行为同现状
// （无 session_interrupted / 无续写消息，length 语义 completed）。
func TestEngineAutoContinueDisabled(t *testing.T) {
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventDelta, Text: "p1"}, done("length")},
		{{Type: StepEventDelta, Text: "should-not-run"}, done("stop")},
	}}
	sink := &fakeSink{}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{}, sink, store, RuntimeConfig{})

	res, err := rt.RunTurn(context.Background(), "t1")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Finish != "length" || res.FinalText != "p1" || rt.State().Status != TurnStatusCompleted {
		t.Fatalf("res=%+v state=%+v", res, rt.State())
	}
	if len(llm.reqs) != 1 || len(store.messages) != 1 {
		t.Fatalf("reqs=%d messages=%d want 1/1", len(llm.reqs), len(store.messages))
	}
	if findEvent(sink, EventSessionInterrupted) != nil {
		t.Fatalf("关闭时应保持旧行为（不发 session_interrupted）：%+v", sink.types())
	}
}

// TestEngineInterruptedWithPartialText：已产生文本后断流（done.Err）→ 落库部分 assistant
// + session_interrupted + 续写；关闭时保持 failed。
func TestEngineInterruptedWithPartialText(t *testing.T) {
	streamErr := errors.New("stream broken")

	t.Run("enabled", func(t *testing.T) {
		llm := &fakeLLM{scripts: [][]StepEvent{
			{{Type: StepEventDelta, Text: "part1"}, doneErr(streamErr)},
			{{Type: StepEventDelta, Text: "done"}, done("stop")},
		}}
		sink := &fakeSink{}
		store := &fakeTurnStore{}
		rt := newEngine(t, llm, &fakeTools{}, sink, store, RuntimeConfig{
			AutoContinue: true, AutoContinueDelay: 10 * time.Millisecond,
		})
		rt.deps.Clock = newContinueClock(10 * time.Millisecond)

		res, err := rt.RunTurn(context.Background(), "t1")
		if err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		if res.Finish != "stop" || res.FinalText != "part1done" || rt.State().Status != TurnStatusCompleted {
			t.Fatalf("res=%+v state=%+v", res, rt.State())
		}
		if len(llm.reqs) != 2 {
			t.Fatalf("StepRequest 次数=%d want 2", len(llm.reqs))
		}
		if len(store.messages) < 2 || store.messages[0].Role != "assistant" || store.messages[0].Content != "part1" {
			t.Fatalf("部分 assistant 文本应落库：%+v", store.messages)
		}
		if n := countKind(store.messages, continueMessageKind); n != 1 {
			t.Fatalf("续写消息落库次数=%d want 1：%+v", n, store.messages)
		}
		if findEvent(sink, EventSessionInterrupted) == nil {
			t.Fatalf("断流应发 session_interrupted：%+v", sink.types())
		}
	})

	t.Run("disabled", func(t *testing.T) {
		llm := &fakeLLM{scripts: [][]StepEvent{
			{{Type: StepEventDelta, Text: "part1"}, doneErr(streamErr)},
		}}
		store := &fakeTurnStore{}
		rt := newEngine(t, llm, &fakeTools{}, &fakeSink{}, store, RuntimeConfig{})

		_, err := rt.RunTurn(context.Background(), "t1")
		if err == nil || !strings.Contains(err.Error(), "stream broken") {
			t.Fatalf("err=%v want 流错误透传", err)
		}
		if rt.State().Status != TurnStatusFailed {
			t.Fatalf("state=%+v want failed", rt.State())
		}
		if len(llm.reqs) != 1 {
			t.Fatalf("StepRequest 次数=%d want 1", len(llm.reqs))
		}
		if len(store.messages) != 1 || store.messages[0].Role != "assistant" {
			t.Fatalf("部分文本仍应落库：%+v", store.messages)
		}
	})
}
