// AskUser S1-3 / S1-4：`session-ask` / `ask-user-reply` 的 `dialog_id`（61 §4.2/§4.3，§0.6 必带）。
//
// L1（mq 驱动 + mq 断言）：
//   - 提问方向：`session-ask.dialog_id` = 该会话的「会话 → 对话」映射值（既有 sessionStore 备忘，
//     与 turn / message 落库同一份），非空；
//   - 应答方向：回带**同一值**的 `ask-user-reply` 被受理（无错误）→ 就地回填 tool-result →
//     答复作为后续用户消息投递（S4：不要求原轮续跑）。
//
// 边界（设计 §6.2 / §8）：`dialog_id` 只作会话标识留痕，路由权威仍是 `ask_id`；缺失/不一致
// 只记 Warn、不阻断（旧前端 / 历史载荷仍可用，见 onAskUserReply）。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// TestAskDialogIDBothDirections：session-ask 带 dialog_id（= 会话映射值），ask-user-reply 原样
// 回带同一值后被受理并驱动本轮走到终态。
func TestAskDialogIDBothDirections(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ask-dlg", "t-ask-dlg")

	// 会话 → 对话映射（一树一库：startTurn 内 EnsureSession 自举并回带 dialog_id）。
	want := s.dialogOf("ins-test", "s-ask-dlg")
	if want == "" {
		t.Fatal("会话 → 对话映射缺失（startTurn 应已自举 dialog_id）")
	}

	var askMu sync.Mutex
	var askEv map[string]any
	sub, err := s.bus.On("session-ask", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		askMu.Lock()
		askEv = m
		askMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-ask-dlg", "turn": "t-ask-dlg",
		"type": "text-user", "content": "ask please",
	}))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		askMu.Lock()
		got := askEv != nil
		askMu.Unlock()
		if got {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	askMu.Lock()
	ev := askEv
	askMu.Unlock()
	if ev == nil {
		t.Fatal("no session-ask broadcast")
	}
	if got := str(ev["dialog_id"]); got != want {
		t.Fatalf("session-ask.dialog_id=%q want %q（须 = 会话 → 对话映射值）", got, want)
	}
	askID := str(ev["ask_id"])
	if askID == "" {
		t.Fatalf("session-ask 缺 ask_id: %+v", ev)
	}

	// 应答方向：回带同一 dialog_id（前端原样回带）→ 受理无错。
	f := s.bus.Emit(context.Background(), "session-ask-reply", jb(map[string]any{
		"instance_id": "ins-test", "ask_id": askID, "answer": "y", "dialog_id": want,
	}))
	if v := f.Wait(); v.Err() != nil {
		t.Fatalf("ask-user-reply（带 dialog_id）应被受理: %v", v.Err())
	}
	// S4：答复后 tool-result 写“已回复”标记（答案只在后续用户消息里），并作为后续消息投递。
	delivered := waitAskAnswerDelivered(t, s, "s-ask-dlg", "t-ask-dlg", "答：y")
	if !strings.Contains(delivered, "确认继续？") {
		t.Fatalf("答复应携带原问题：%q", delivered)
	}
	rows := toolRowsOf(t, "t-ask-dlg")
	if len(rows) != 1 || !strings.Contains(persist.Sval(rows[0]["content"]), askAnsweredHistoryResult) {
		t.Fatalf("答复应就地写已回复标记：%v", rows)
	}
}

// TestAskReplyWithoutDialogIDStillRoutes：缺 dialog_id 的旧载荷仍按 ask_id 路由（不阻断；
// 仅记 Warn）——保证旧前端 / 历史夹具不被 §0.6 补齐打破。
func TestAskReplyWithoutDialogIDStillRoutes(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ask-old", "t-ask-old")

	var askMu sync.Mutex
	var askEv map[string]any
	sub, err := s.bus.On("session-ask", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		askMu.Lock()
		askEv = m
		askMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-ask-old", "turn": "t-ask-old",
		"type": "text-user", "content": "ask please",
	}))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		askMu.Lock()
		got := askEv != nil
		askMu.Unlock()
		if got {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	askMu.Lock()
	ev := askEv
	askMu.Unlock()
	if ev == nil {
		t.Fatal("no session-ask broadcast")
	}

	f := s.bus.Emit(context.Background(), "session-ask-reply", jb(map[string]any{
		"instance_id": "ins-test", "ask_id": str(ev["ask_id"]), "answer": "y",
	}))
	if v := f.Wait(); v.Err() != nil {
		t.Fatalf("缺 dialog_id 的应答仍应按 ask_id 路由（不阻断）: %v", v.Err())
	}
	// S4：缺 dialog_id 不阻断，答复照常就地回填并作为后续用户消息投递。
	delivered := waitAskAnswerDelivered(t, s, "s-ask-old", "t-ask-old", "答：y")
	if !strings.Contains(delivered, "确认继续？") {
		t.Fatalf("答复应携带原问题：%q", delivered)
	}
}
