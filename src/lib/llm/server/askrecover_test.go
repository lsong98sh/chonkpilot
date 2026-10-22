package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAskExpiredReply：失效 / 重启丢失 ask 的应答兜底（MOD-1 M6b）。
//
//   - 缺 ask_id / 空答案 / 缺 instance_id → 明确失败（不静默）；
//   - 未落库的 ask_id → 明确失败；
//   - 已终态 ask → 明确失败（重复应答不重复开轮）；
//   - 非终态（interrupted）→ 不报错（按 61 §4.2 转普通 user 消息开新轮）。
func TestAskExpiredReply(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)

	if err := s.replyExpiredAsk("", "ask-1", "a"); err == nil {
		t.Fatal("缺 instance_id 应明确失败")
	}
	if err := s.replyExpiredAsk("ins-test", "", "a"); err == nil {
		t.Fatal("缺 ask_id 应明确失败")
	}
	if err := s.replyExpiredAsk("ins-test", "ask-1", " "); err == nil {
		t.Fatal("空答案应明确失败")
	}
	if err := s.replyExpiredAsk("ins-test", "ask-missing", "a"); err == nil {
		t.Fatal("未落库 ask_id 应明确失败")
	}

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-ask"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn("t-ask", "s-ask"); err != nil {
		t.Fatal(err)
	}
	content := `{"call":{"tool_call_id":"c1","name":"ask_user","arguments":{"question":"继续吗？"}}}`
	// 已终态：重复应答 → 明确失败。
	if _, err := st.AppendMsgMap("t-ask", map[string]any{
		"role": "tool", "content": content, "tool_call_id": "c-done",
		"tool_call_status": "completed", "ask_id": "ask-done",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.replyExpiredAsk("ins-test", "ask-done", "a"); err == nil {
		t.Fatal("已回答 ask 不应重复开轮")
	}

	// 非终态（重启后遗留 running/interrupted）→ 转新轮喂答案，不报错。
	if _, err := st.AppendMsgMap("t-ask", map[string]any{
		"role": "tool", "content": content, "tool_call_id": "c-lost",
		"tool_call_status": "interrupted", "ask_id": "ask-lost",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.replyExpiredAsk("ins-test", "ask-lost", "好的"); err != nil {
		t.Fatalf("失效 ask 应答应转新轮：%v", err)
	}
	// 新轮已落库（turn id 由宿主分配，不同于原 t-ask）。
	if latest := st.LatestTurnID("s-ask"); latest == "" || latest == "t-ask" {
		t.Fatalf("应已为该会话开新轮：latest=%q", latest)
	}
	// 收尾：等新轮退出（否则 TempDir 清理会撞上仍在用的 DB 句柄）。
	for i := 0; i < 150; i++ {
		s.mu.Lock()
		tc := s.turnBySessionLocked("ins-test", "s-ask")
		if tc != nil {
			tc.Close()
		}
		s.mu.Unlock()
		if tc == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
