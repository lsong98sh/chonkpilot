package chat

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func mkEvent(t *testing.T, seq uint64, typ TurnEventType, payload any) TurnEvent {
	t.Helper()
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		raw = b
	}
	return TurnEvent{Seq: seq, Type: typ, At: time.Unix(int64(seq), 0), Payload: raw}
}

// applyAll 应用一串事件（任一失败即测试失败）。
func applyAll(t *testing.T, st TurnState, evs ...TurnEvent) TurnState {
	t.Helper()
	for _, ev := range evs {
		next, err := Apply(st, ev)
		if err != nil {
			t.Fatalf("Apply(%s seq=%d): %v", ev.Type, ev.Seq, err)
		}
		st = next
	}
	return st
}

// TestTurnStateJSONRoundTrip：状态快照可 JSON 往返（空值 / 满值都稳定）。
func TestTurnStateJSONRoundTrip(t *testing.T) {
	st := TurnState{
		Version:   TurnStateVersion,
		SessionID: "s", TurnID: "t", Status: TurnStatusAwaitingTool, Seq: 9,
		Pending:  []ToolCall{{ID: "c1", Name: "f", Arguments: `{}`}},
		Inflight: []Inflight{{OpID: "op1", CallID: "c2", Async: true}},
		Frames: []FrameState{{
			FrameID: "inbox-1", ParentFrameID: "", Depth: 1, ResumeNote: "note",
			Stash: []ToolResult{{CallID: "c3", Content: "ok"}},
		}},
		Stash:      []ToolResult{{CallID: "c4", Content: "done"}},
		ResumeNote: "外层续接",
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var back TurnState
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, st) {
		t.Fatalf("往返不一致：\n got=%+v\nwant=%+v", back, st)
	}

	empty := NewTurnState("s2", "t2")
	raw2, _ := json.Marshal(empty)
	var back2 TurnState
	if err := json.Unmarshal(raw2, &back2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back2, empty) {
		t.Fatalf("空状态往返不一致：%+v vs %+v", back2, empty)
	}
}

// TestTurnStateHappyPath：受理 → step → 工具派发/回执 → 终态。
func TestTurnStateHappyPath(t *testing.T) {
	st := NewTurnState("s", "t")
	st = applyAll(t, st,
		mkEvent(t, 1, TurnEventStart, TurnStartPayload{SessionID: "s", TurnID: "t"}),
	)
	if st.Status != TurnStatusRunning || st.Seq != 1 {
		t.Fatalf("turn_start → %+v", st)
	}

	st = applyAll(t, st,
		mkEvent(t, 2, TurnEventStepStart, nil),
		mkEvent(t, 3, TurnEventLLMDelta, map[string]any{"text": "x"}),
		mkEvent(t, 4, TurnEventLLMDone, nil),
		mkEvent(t, 5, TurnEventStepDone, StepDonePayload{
			Pending: []ToolCall{{ID: "c1", Name: "f", Arguments: `{}`}},
		}),
	)
	if st.Status != TurnStatusAwaitingTool || len(st.Pending) != 1 || st.Seq != 5 {
		t.Fatalf("step_done → %+v", st)
	}

	st = applyAll(t, st,
		mkEvent(t, 6, TurnEventToolDispatch, ToolDispatchPayload{OpID: "op1", CallID: "c1"}),
	)
	if len(st.Pending) != 0 || len(st.Inflight) != 1 || st.Status != TurnStatusAwaitingTool {
		t.Fatalf("tool_dispatch → %+v", st)
	}

	st = applyAll(t, st,
		mkEvent(t, 7, TurnEventToolResult, ToolResult{CallID: "c1", Content: "ok"}),
	)
	if len(st.Inflight) != 0 || len(st.Stash) != 1 || st.Status != TurnStatusRunning {
		t.Fatalf("tool_result → %+v", st)
	}

	st = applyAll(t, st,
		mkEvent(t, 8, TurnEventStepDone, StepDonePayload{}),
		mkEvent(t, 9, TurnEventEnd, TurnEndPayload{Status: TurnStatusCompleted}),
	)
	if st.Status != TurnStatusCompleted || !st.Status.Terminal() || st.Seq != 9 {
		t.Fatalf("turn_end → %+v", st)
	}
}

// TestTurnStateAwaitingUser：ask_user 一类 → awaiting_user。
func TestTurnStateAwaitingUser(t *testing.T) {
	st := applyAll(t, NewTurnState("s", "t"),
		mkEvent(t, 1, TurnEventStart, nil),
		mkEvent(t, 2, TurnEventStepDone, StepDonePayload{AwaitingUser: true}),
	)
	if st.Status != TurnStatusAwaitingUser {
		t.Fatalf("status=%q want awaiting_user", st.Status)
	}
}

// TestTurnStateFrameLIFO：插入帧入栈 / 按 LIFO 出栈；乱序出栈 → 显式失败。
func TestTurnStateFrameLIFO(t *testing.T) {
	st := applyAll(t, NewTurnState("s", "t"),
		mkEvent(t, 1, TurnEventStart, nil),
		mkEvent(t, 2, TurnEventInboxInsert, FrameState{FrameID: "f1", Depth: 1}),
		mkEvent(t, 3, TurnEventInboxInsert, FrameState{FrameID: "f2", ParentFrameID: "f1", Depth: 2}),
	)
	if len(st.Frames) != 2 || st.Frames[1].ParentFrameID != "f1" {
		t.Fatalf("frames=%+v", st.Frames)
	}

	if _, err := Apply(st, mkEvent(t, 4, TurnEventInboxConsume, InboxConsumePayload{FrameID: "f1"})); err == nil {
		t.Fatal("乱序出栈应失败（LIFO）")
	}

	st = applyAll(t, st, mkEvent(t, 4, TurnEventInboxConsume, InboxConsumePayload{FrameID: "f2"}))
	if len(st.Frames) != 1 || st.Frames[0].FrameID != "f1" {
		t.Fatalf("consume → %+v", st.Frames)
	}
	st = applyAll(t, st, mkEvent(t, 5, TurnEventInboxConsume, InboxConsumePayload{FrameID: "f1"}))
	if len(st.Frames) != 0 {
		t.Fatalf("栈空 → %+v", st.Frames)
	}
	if _, err := Apply(st, mkEvent(t, 6, TurnEventInboxConsume, InboxConsumePayload{FrameID: "none"})); err == nil {
		t.Fatal("空栈出栈应失败")
	}
}

// TestTurnStateSeqAndUnknown：Seq 必须单调；未知事件 / 非终态 turn_end 显式失败。
func TestTurnStateSeqAndUnknown(t *testing.T) {
	st := applyAll(t, NewTurnState("s", "t"), mkEvent(t, 2, TurnEventStart, nil))

	if _, err := Apply(st, mkEvent(t, 2, TurnEventStepStart, nil)); err == nil {
		t.Fatal("重复 Seq 应失败")
	}
	if _, err := Apply(st, mkEvent(t, 1, TurnEventStepStart, nil)); err == nil {
		t.Fatal("回退 Seq 应失败")
	}
	if _, err := Apply(st, mkEvent(t, 3, TurnEventType("nope"), nil)); err == nil {
		t.Fatal("未知事件应失败（重放不得静默跳过）")
	}
	if _, err := Apply(st, mkEvent(t, 3, TurnEventEnd, TurnEndPayload{Status: TurnStatusRunning})); err == nil {
		t.Fatal("非终态 turn_end 应失败")
	}
	if _, err := Apply(st, TurnEvent{Seq: 0, Type: TurnEventStepStart}); err == nil {
		t.Fatal("Seq=0 应失败")
	}
}

// TestTurnStateTerminalGuards：终态不可再推进。
func TestTurnStateTerminalGuards(t *testing.T) {
	st := applyAll(t, NewTurnState("s", "t"),
		mkEvent(t, 1, TurnEventStart, nil),
		mkEvent(t, 2, TurnEventEnd, TurnEndPayload{Status: TurnStatusFailed}),
	)
	if _, err := Apply(st, mkEvent(t, 3, TurnEventStepStart, nil)); err == nil {
		t.Fatal("终态后 step_start 应失败")
	}
	if _, err := Apply(st, mkEvent(t, 3, TurnEventToolDispatch, ToolDispatchPayload{CallID: "c"})); err == nil {
		t.Fatal("终态后 tool_dispatch 应失败")
	}
	// cancel 可在任意状态落终态（含已终态外的 awaiting 等；重复 cancel 由 Seq 与状态语义共同保证）。
	if st2, err := Apply(st, mkEvent(t, 3, TurnEventCancel, nil)); err != nil || st2.Status != TurnStatusCanceled {
		t.Fatalf("cancel: %+v err=%v", st2, err)
	}
}

// TestTurnStateVersion：版本不兼容 → 显式失败（调用方收敛到终态）。
func TestTurnStateVersion(t *testing.T) {
	st := NewTurnState("s", "t")
	st.Version = TurnStateVersion + 1
	if _, err := Apply(st, mkEvent(t, 1, TurnEventStart, nil)); err == nil {
		t.Fatal("不兼容版本应失败")
	}
}

// TestTurnStateImmutableApply：Apply 不得修改入参（切片别名保护）。
func TestTurnStateImmutableApply(t *testing.T) {
	st := NewTurnState("s", "t")
	st.Status = TurnStatusAwaitingTool
	st.Pending = []ToolCall{{ID: "c1", Name: "f"}}
	st.Seq = 1

	pendingBefore := append([]ToolCall(nil), st.Pending...)
	next, err := Apply(st, mkEvent(t, 2, TurnEventToolDispatch, ToolDispatchPayload{CallID: "c1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.Pending, pendingBefore) || st.Seq != 1 {
		t.Fatalf("入参被就地修改：%+v", st)
	}
	if len(next.Pending) != 0 || len(next.Inflight) != 1 {
		t.Fatalf("新状态=%+v", next)
	}
}

// TestTurnEventLogLevels：各级别写入量符合预期（none / minimal / outline / verbose）。
func TestTurnEventLogLevels(t *testing.T) {
	seq := []TurnEventType{
		TurnEventStart, TurnEventStepStart, TurnEventStepDone,
		TurnEventLLMDelta, TurnEventLLMDone,
		TurnEventToolDispatch, TurnEventToolResult, TurnEventEnd,
	}
	want := map[TurnEventLogLevel]int{
		TurnEventLogNone:    0,
		TurnEventLogMinimal: 2, // turn_start + turn_end
		TurnEventLogOutline: 6, // 除 llm_delta / llm_done
		TurnEventLogVerbose: 8,
	}
	for level, n := range want {
		got := 0
		for _, typ := range seq {
			if level.Records(typ) {
				got++
			}
		}
		if got != n {
			t.Fatalf("%s 记录量=%d want %d", level, got, n)
		}
	}
}

// TestParseTurnEventLogLevel：空 / 未知 → outline（默认）。
func TestParseTurnEventLogLevel(t *testing.T) {
	cases := map[string]TurnEventLogLevel{
		"":        TurnEventLogOutline,
		"bogus":   TurnEventLogOutline,
		"none":    TurnEventLogNone,
		"minimal": TurnEventLogMinimal,
		"outline": TurnEventLogOutline,
		"verbose": TurnEventLogVerbose,
	}
	for in, want := range cases {
		if got := ParseTurnEventLogLevel(in); got != want {
			t.Fatalf("Parse(%q)=%q want %q", in, got, want)
		}
	}
}
