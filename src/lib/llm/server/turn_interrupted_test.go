// RETRY-2：语义级中断（session-interrupted）与自动续写排程/取消的白盒测试。
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newInterruptCtx 构造最小 turnCtx（只覆盖 raiseInterrupted / autoContinueAfter 所需字段）。
func newInterruptCtx(t *testing.T, s *Server, enabled bool, delay time.Duration) *turnCtx {
	t.Helper()
	return &turnCtx{
		server:            s,
		req:               StartReq{InstanceID: "ins-test", Session: "s-int", Turn: "t-int"},
		ctx:               context.Background(),
		done:              make(chan struct{}),
		in:                make(chan ChatMsg, 4),
		autoContinue:      enabled,
		autoContinueDelay: delay,
		continueText:      "继续",
	}
}

// subscribeEvent 订阅主题并把事件转发到返回通道；调用方用 cancel 取消订阅。
func subscribeEvent(t *testing.T, s *Server, subject string) (<-chan map[string]any, func()) {
	t.Helper()
	ch := make(chan map[string]any, 1)
	sub, err := s.bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		select {
		case ch <- m:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", subject, err)
	}
	return ch, func() { sub.Unsubscribe() }
}

// TestRaiseInterruptedPublishesPayload：中断消息载荷（disable → delay=0、不排程）。
func TestRaiseInterruptedPublishesPayload(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	tc := newInterruptCtx(t, s, false, 600*time.Second)
	ch, cancel := subscribeEvent(t, s, "session-interrupted")
	defer cancel()
	if tc.raiseInterrupted("LLM_STREAM_ERROR", "boom", true, true) {
		t.Fatal("autoContinue=false 不应排程自动续写")
	}
	var ev map[string]any
	select {
	case ev = <-ch:
	case <-time.After(time.Second):
		t.Fatal("未收到 session-interrupted")
	}
	if ev["session"] != "s-int" || ev["turn"] != "t-int" || ev["code"] != "LLM_STREAM_ERROR" {
		t.Fatalf("载荷不符：%+v", ev)
	}
	if n, _ := ev["auto_continue_delay_ms"].(float64); n != 0 {
		t.Fatalf("auto_continue_delay_ms=%v want 0（开关关闭）", ev["auto_continue_delay_ms"])
	}
	if ev["retryable"] != true {
		t.Fatalf("retryable=%v want true", ev["retryable"])
	}
}

// TestAutoContinueScheduledAndCanceled：宽限期内无操作 → 到期喂续写消息；用户操作 → 取消。
func TestAutoContinueScheduledAndCanceled(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	t.Run("到期自动续写", func(t *testing.T) {
		tc := newInterruptCtx(t, s, true, 30*time.Millisecond)
		defer close(tc.done)
		if !tc.raiseInterrupted("LLM_INCOMPLETE", "", true, true) {
			t.Fatal("应排程自动续写")
		}
		select {
		case m := <-tc.in:
			if m.Role != "user" || m.Content != "继续" {
				t.Fatalf("续写消息=%+v want user/继续", m)
			}
		case <-time.After(time.Second):
			t.Fatal("宽限期到期未收到自动续写消息")
		}
	})

	t.Run("用户操作取消", func(t *testing.T) {
		tc := newInterruptCtx(t, s, true, 40*time.Millisecond)
		defer close(tc.done)
		if !tc.raiseInterrupted("LLM_INCOMPLETE", "", true, true) {
			t.Fatal("应排程自动续写")
		}
		tc.continueSeq.Add(1) // 模拟用户发消息/插话
		select {
		case m := <-tc.in:
			t.Fatalf("用户操作后不应自动续写，实得 %+v", m)
		case <-time.After(120 * time.Millisecond):
		}
	})
}
