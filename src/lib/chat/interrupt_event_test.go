// 中断事件字段（MOD-1 A4-P1）：`session_interrupted` 的 code / retryable / 宽限期毫秒。
package chat

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestEngineInterruptEventFields：截断（length）与断流（stream）各自的 code / retryable /
// auto_continue_delay_ms 取值；续写触发逻辑不变（仍按 AutoContinue 开关）。
func TestEngineInterruptEventFields(t *testing.T) {
	run := func(t *testing.T, script []StepEvent) *Event {
		t.Helper()
		llm := &fakeLLM{scripts: [][]StepEvent{
			script,
			{{Type: StepEventDelta, Text: "done"}, done("stop")},
		}}
		sink := &fakeSink{}
		rt := newEngine(t, llm, &fakeTools{}, sink, &fakeTurnStore{}, RuntimeConfig{
			AutoContinue: true, AutoContinueDelay: 10 * time.Millisecond,
		})
		rt.deps.Clock = newContinueClock(10 * time.Millisecond)
		if _, err := rt.RunTurn(context.Background(), "t1"); err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		ev := findEvent(sink, EventSessionInterrupted)
		if ev == nil {
			t.Fatalf("应发 session_interrupted：%+v", sink.types())
		}
		return ev
	}

	t.Run("length", func(t *testing.T) {
		ev := run(t, []StepEvent{{Type: StepEventDelta, Text: "part1"}, done("length")})
		if ev.Code != "length" || !ev.Retryable || ev.DelayMilli != 10 || ev.Text != "part1" {
			t.Fatalf("截断事件字段错：%+v", ev)
		}
	})

	t.Run("stream", func(t *testing.T) {
		ev := run(t, []StepEvent{{Type: StepEventDelta, Text: "part1"}, doneErr(errors.New("stream broken"))})
		if ev.Code != "stream" || !ev.Retryable || ev.DelayMilli != 10 || ev.Text != "part1" {
			t.Fatalf("断流事件字段错：%+v", ev)
		}
	})
}
