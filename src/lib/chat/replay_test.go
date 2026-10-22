// L1 重放（MOD-1 M8-C1）：Replay 与 Apply 同语义；事件不可应用时显式失败。
package chat

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestReplayMatchesApply：手工完整序列 → 重放结果与逐步 Apply 逐字段相等。
func TestReplayMatchesApply(t *testing.T) {
	events := []TurnEvent{
		mkEvent(t, 1, TurnEventStart, TurnStartPayload{SessionID: "s", TurnID: "t"}),
		mkEvent(t, 2, TurnEventStepStart, nil),
		mkEvent(t, 3, TurnEventStepDone, StepDonePayload{
			Pending: []ToolCall{{ID: "c1", Name: "f", Arguments: `{}`}},
		}),
		mkEvent(t, 4, TurnEventToolDispatch, ToolDispatchPayload{OpID: "op1", CallID: "c1"}),
		mkEvent(t, 5, TurnEventToolResult, ToolResult{CallID: "c1", Content: "ok"}),
		mkEvent(t, 6, TurnEventInboxInsert, FrameState{FrameID: "ib1", Depth: 1}),
		mkEvent(t, 7, TurnEventInboxConsume, InboxConsumePayload{FrameID: "ib1"}),
		mkEvent(t, 8, TurnEventContinue, nil),
		mkEvent(t, 9, TurnEventEnd, TurnEndPayload{Status: TurnStatusCompleted}),
	}

	want := applyAll(t, NewTurnState("s", "t"), events...)
	got, err := Replay("s", "t", events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("重放与逐步 Apply 不一致：\n got=%+v\nwant=%+v", got, want)
	}
	if got.Status != TurnStatusCompleted || got.Seq != 9 || len(got.Frames) != 0 || len(got.Pending) != 0 {
		t.Fatalf("重建状态错：%+v", got)
	}

	// 不修改入参切片。
	before := append([]TurnEvent(nil), events...)
	if _, err := Replay("s", "t", events); err != nil {
		t.Fatalf("Replay(second): %v", err)
	}
	if !reflect.DeepEqual(events, before) {
		t.Fatalf("Replay 修改了入参：%+v", events)
	}

	// 空事件 → 初始状态（pending）。
	empty, err := Replay("s2", "t2", nil)
	if err != nil {
		t.Fatalf("Replay(empty): %v", err)
	}
	if !reflect.DeepEqual(empty, NewTurnState("s2", "t2")) || empty.Status != TurnStatusPending {
		t.Fatalf("空事件应返回初始状态：%+v", empty)
	}
}

// TestReplayRejectsInvalidEvents：未知事件 / Seq 重复或回退 / 版本不符 → 错误（不静默跳过）。
func TestReplayRejectsInvalidEvents(t *testing.T) {
	cases := map[string][]TurnEvent{
		"unknown": {
			mkEvent(t, 1, TurnEventStart, nil),
			mkEvent(t, 2, TurnEventType("nope"), nil),
		},
		"duplicate seq": {
			mkEvent(t, 1, TurnEventStart, nil),
			mkEvent(t, 1, TurnEventStepStart, nil),
		},
		"rollback seq": {
			mkEvent(t, 2, TurnEventStart, nil),
			mkEvent(t, 1, TurnEventStepStart, nil),
		},
		"zero seq": {
			{Seq: 0, Type: TurnEventStepStart},
		},
		"non-terminal end": {
			mkEvent(t, 1, TurnEventStart, nil),
			mkEvent(t, 2, TurnEventEnd, TurnEndPayload{Status: TurnStatusRunning}),
		},
	}
	for name, events := range cases {
		if _, err := Replay("s", "t", events); err == nil {
			t.Fatalf("%s: 应返回错误", name)
		} else if !strings.Contains(err.Error(), "replay") {
			t.Fatalf("%s: 错误未标注重放上下文：%v", name, err)
		}
	}

	// 版本不符：入参无法携带版本（Replay 恒从当前版本起步），由 Apply 同一判据覆盖。
	st := NewTurnState("s", "t")
	st.Version = TurnStateVersion + 1
	if _, err := Apply(st, mkEvent(t, 1, TurnEventStart, nil)); err == nil {
		t.Fatal("不兼容版本应失败")
	}
}

// TestReplayEngineEvents：引擎产出的真实事件可重放（工具环）→ completed / Seq 对齐 / 帧栈空。
func TestReplayEngineEvents(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "f"}
	llm := &fakeLLM{scripts: [][]StepEvent{
		{{Type: StepEventToolCall, ToolCall: &call}, done("tool_calls", call)},
		{{Type: StepEventDelta, Text: "done"}, done("stop")},
	}}
	store := &fakeTurnStore{}
	rt := newEngine(t, llm, &fakeTools{result: ToolResult{Content: "42"}}, &fakeSink{}, store, RuntimeConfig{})

	if _, err := rt.RunTurn(context.Background(), "t1"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(store.events) == 0 {
		t.Fatal("引擎未写事件日志")
	}

	st, err := Replay("s1", "t1", store.events)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if st.Status != TurnStatusCompleted {
		t.Fatalf("status=%q want completed", st.Status)
	}
	last := store.events[len(store.events)-1]
	if st.Seq != last.Seq || st.Seq != rt.State().Seq {
		t.Fatalf("seq=%d last=%d runtime=%d", st.Seq, last.Seq, rt.State().Seq)
	}
	if len(st.Frames) != 0 || st.Frames != nil {
		t.Fatalf("frames=%+v want empty", st.Frames)
	}
	if !reflect.DeepEqual(st, rt.State()) {
		t.Fatalf("重放状态与运行终态不一致：\n got=%+v\nwant=%+v", st, rt.State())
	}
}
