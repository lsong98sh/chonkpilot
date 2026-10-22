// 影子重放（MOD-1 M4）：记录 / 比对 / 不影响生产。
package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeTurnStore 是内存 TurnStore（记录调用顺序与内容）。
type fakeTurnStore struct {
	messages []Message
	inbox    []string
	events   []TurnEvent
	failWith error
}

func (f *fakeTurnStore) SaveMessage(_ context.Context, msg Message) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.messages = append(f.messages, msg)
	return nil
}

func (f *fakeTurnStore) CommitInbox(_ context.Context, inboxID string, msg Message) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.inbox = append(f.inbox, inboxID)
	f.messages = append(f.messages, msg)
	return nil
}

func (f *fakeTurnStore) AppendEvent(_ context.Context, ev TurnEvent) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.events = append(f.events, ev)
	return nil
}

// runScriptedLoop 是测试用的确定性「引擎」：同一事件序列 → 同一 StepRequest / 写入序列。
func runScriptedLoop(ctx context.Context, events []TurnEvent, model string, rec *Recorder, st TurnStore) error {
	for _, ev := range events {
		switch ev.Type {
		case TurnEventStart:
			rec.RecordStep(StepRequest{
				InstanceID: "ins", SessionID: "s1", TurnID: "t1", Model: model,
				Messages: []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
			})
			if err := st.SaveMessage(ctx, Message{Role: "user", Kind: "text", Content: "hi"}); err != nil {
				return err
			}
		case TurnEventStepDone:
			rec.RecordStep(StepRequest{
				InstanceID: "ins", SessionID: "s1", TurnID: "t1", Model: model,
				Messages: []Message{{Role: "assistant", Content: "call", ToolCalls: []ToolCall{{ID: "c1", Name: "f", Arguments: `{}`}}}},
			})
			if err := st.SaveMessage(ctx, Message{
				Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "f", Arguments: `{}`}},
			}); err != nil {
				return err
			}
		case TurnEventToolResult:
			if err := st.CommitInbox(ctx, "inbox-1", Message{Role: "user", Content: "插话"}); err != nil {
				return err
			}
		case TurnEventEnd:
			if err := st.SaveMessage(ctx, Message{Role: "assistant", Content: "done"}); err != nil {
				return err
			}
		}
		if err := st.AppendEvent(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

func mkTurnEvents() []TurnEvent {
	return []TurnEvent{
		{Seq: 1, Type: TurnEventStart},
		{Seq: 2, Type: TurnEventStepDone},
		{Seq: 3, Type: TurnEventToolResult},
		{Seq: 4, Type: TurnEventEnd},
	}
}

// TestShadowReplayEquivalent：同一事件序列 + 同一桩 → StepRequest / 写入序列逐项相等。
func TestShadowReplayEquivalent(t *testing.T) {
	ctx := context.Background()
	events := mkTurnEvents()

	rec1 := NewRecorder()
	st1 := &fakeTurnStore{}
	if err := runScriptedLoop(ctx, events, "model-a", rec1, &ShadowTurnStore{Inner: st1, Rec: rec1}); err != nil {
		t.Fatal(err)
	}

	rec2 := NewRecorder()
	st2 := &fakeTurnStore{}
	if err := runScriptedLoop(ctx, events, "model-a", rec2, &ShadowTurnStore{Inner: st2, Rec: rec2}); err != nil {
		t.Fatal(err)
	}

	if err := Verify(rec1.Trace(), rec2.Trace()); err != nil {
		t.Fatalf("影子轨迹应逐项相等：%v", err)
	}
	steps, writes := rec1.Len()
	if steps != 2 || writes != 8 {
		t.Fatalf("trace 规模 steps=%d writes=%d want 2/8", steps, writes)
	}
}

// TestShadowReplayDivergence：任一侧请求 / 写入变化 → 明确指出差异位置。
func TestShadowReplayDivergence(t *testing.T) {
	ctx := context.Background()
	events := mkTurnEvents()

	rec1 := NewRecorder()
	_ = runScriptedLoop(ctx, events, "model-a", rec1, &ShadowTurnStore{Inner: &fakeTurnStore{}, Rec: rec1})

	rec2 := NewRecorder()
	_ = runScriptedLoop(ctx, events, "model-b", rec2, &ShadowTurnStore{Inner: &fakeTurnStore{}, Rec: rec2})

	diffs := Diff(rec1.Trace(), rec2.Trace())
	if len(diffs) != 2 { // 两次 StepRequest 的 model 都不同
		t.Fatalf("diffs=%d want 2：%+v", len(diffs), diffs)
	}
	if diffs[0].Seq != "steps[0]" || !strings.Contains(diffs[0].Want, "model-a") || !strings.Contains(diffs[0].Got, "model-b") {
		t.Fatalf("差异定位错：%+v", diffs[0])
	}
	err := Verify(rec1.Trace(), rec2.Trace())
	if err == nil || !strings.Contains(err.Error(), "steps[0]") {
		t.Fatalf("Verify 应报 steps[0]：%v", err)
	}
}

// TestShadowWriteDivergence：写入内容不同 → writes[i] 差异。
func TestShadowWriteDivergence(t *testing.T) {
	rec1 := NewRecorder()
	rec1.RecordWrite("message", "", Message{Role: "assistant", Content: "a"})
	rec2 := NewRecorder()
	rec2.RecordWrite("message", "", Message{Role: "assistant", Content: "b"})

	diffs := Diff(rec1.Trace(), rec2.Trace())
	if len(diffs) != 1 || diffs[0].Seq != "writes[0]" {
		t.Fatalf("diffs=%+v", diffs)
	}
}

// TestShadowTurnStoreDelegates：影子层只旁路记录，生产写入与错误原样透传。
func TestShadowTurnStoreDelegates(t *testing.T) {
	ctx := context.Background()
	inner := &fakeTurnStore{}
	rec := NewRecorder()
	st := &ShadowTurnStore{Inner: inner, Rec: rec}

	if err := st.SaveMessage(ctx, Message{Role: "user", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CommitInbox(ctx, "inbox-9", Message{Role: "user", Content: "y"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent(ctx, TurnEvent{Seq: 1, Type: TurnEventStart}); err != nil {
		t.Fatal(err)
	}
	if len(inner.messages) != 2 || len(inner.inbox) != 1 || len(inner.events) != 1 {
		t.Fatalf("生产写入被吞：%+v", inner)
	}
	tr := rec.Trace()
	if len(tr.Writes) != 3 || tr.Writes[0].Kind != "message" || tr.Writes[1].Kind != "inbox" || tr.Writes[2].Kind != "event" {
		t.Fatalf("影子记录序列错：%+v", tr.Writes)
	}

	// 错误原样透传（影子不接管）。
	inner.failWith = errors.New("disk full")
	if err := st.SaveMessage(ctx, Message{Role: "user"}); !errors.Is(err, inner.failWith) {
		t.Fatalf("错误未透传：%v", err)
	}

	// nil 记录器不阻断。
	bare := &ShadowTurnStore{Inner: &fakeTurnStore{}}
	if err := bare.SaveMessage(ctx, Message{Role: "user", Content: "z"}); err != nil {
		t.Fatalf("nil Recorder 不应阻断：%v", err)
	}
}

// TestRecorderTraceIsolation：Trace() 返回副本，外部修改不影响记录器。
func TestRecorderTraceIsolation(t *testing.T) {
	rec := NewRecorder()
	rec.RecordStep(StepRequest{Model: "m"})
	tr := rec.Trace()
	tr.Steps[0].Model = "hacked"

	again := rec.Trace()
	if again.Steps[0].Model != "m" {
		t.Fatalf("Trace 未隔离：%q", again.Steps[0].Model)
	}

	rec.Reset()
	if s, w := rec.Len(); s != 0 || w != 0 {
		t.Fatalf("Reset 未清空：%d/%d", s, w)
	}
}

// TestVerifyNilVsEmpty：两侧都为空 → 相等；缺失 / 多余项 → 差异。
func TestVerifyNilVsEmpty(t *testing.T) {
	if err := Verify(Trace{}, Trace{}); err != nil {
		t.Fatalf("空轨迹应相等：%v", err)
	}
	one := Trace{Steps: []StepRequest{{Model: "m"}}}
	if diffs := Diff(one, Trace{}); len(diffs) != 1 || diffs[0].Seq != "steps[0]" {
		t.Fatalf("缺失项差异错：%+v", diffs)
	}
	if diffs := Diff(Trace{}, one); len(diffs) != 1 || diffs[0].Got == "<missing>" {
		t.Fatalf("多余项差异错：%+v", diffs)
	}
}
