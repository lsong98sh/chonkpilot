// 同轮次继续（llm-start{continue:true}）白盒验证：
//   - 复用既有 turn（不新增轮次），turn id 保持不变；
//   - 注入的 user 消息以 Kind=continue 落库（非新轮边界）；
//   - 拼接按同一轮（既有消息不重复，该 turn 全量）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordLLMServer 记录每次 LLM 请求的 messages 并固定回文本（同轮次继续拼接断言用）。
func recordLLMServer(reply string) (*httptest.Server, func() [][]ChatMsg) {
	var mu sync.Mutex
	var reqs [][]ChatMsg
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		reqs = append(reqs, body.Messages)
		mu.Unlock()
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": reply}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	return srv, func() [][]ChatMsg {
		mu.Lock()
		defer mu.Unlock()
		out := make([][]ChatMsg, len(reqs))
		copy(out, reqs)
		return out
	}
}

// waitTurnIdle 等 turn 释放（loop defer 清 busy 在 llm-complete 广播之后，continue 前须等待）。
func waitTurnIdle(t *testing.T, s *Server, session string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, busy := s.busy[instKey("ins-test", session)]
		s.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s 仍 busy（turn 未释放）", session)
}

// sessionTurnRecords 取会话全部 turn 行（data-session-history，created_at 升序）。
func sessionTurnRecords(t *testing.T, s *Server, session string) []map[string]any {
	t.Helper()
	res, err := dataRequest(s.bus, "data-session-history", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"session_id": session},
	})
	if err != nil {
		t.Fatalf("data-session-history: %v", err)
	}
	msgs, _ := res["messages"].(map[string]any)
	arr, _ := msgs["turns"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// TestTurnContinueSameTurnReuse：continue=true 复用既有 turn 的全链路断言。
func TestTurnContinueSameTurnReuse(t *testing.T) {
	llm, requests := recordLLMServer("ok-reply")
	defer llm.Close()
	s := newTestServer(t, llm)
	const session, turn = "s-cont", "t-cont"
	startTurn(t, s, session, turn)

	// 第 1 轮：正常完成
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": "first question",
	}))
	if c := lastComplete(collectTurn(t, s.bus, turn, 10*time.Second)); c == nil || c["status"] != "complete" {
		t.Fatalf("first turn not complete: %+v", c)
	}
	waitTurnIdle(t, s, session)

	// 同轮次继续：llm-start{continue:true, turn=同一 turn}
	v := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-cont", "instance_id": "ins-test", "session": session, "turn": turn,
		"continue": true,
	})).Wait()
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("continue llm-start rejected: %+v", res)
	}
	if got := res["turn"]; got != turn {
		t.Fatalf("continue turn = %v, want reuse %q", got, turn)
	}

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": "继续",
	}))
	if c := lastComplete(collectTurn(t, s.bus, turn, 10*time.Second)); c == nil || c["status"] != "complete" {
		t.Fatalf("continue turn not complete: %+v", c)
	}

	// 1) turn id 复用：该 session 仍只有一个 turn
	recs := sessionTurnRecords(t, s, session)
	if len(recs) != 1 || str(recs[0]["turn_id"]) != turn {
		t.Fatalf("turns = %+v, want 仅复用 %q（不新增轮次）", recs, turn)
	}
	// 2) turn 行状态回写：continue 完成后 complete（running → complete，无脏状态）
	if got := str(recs[0]["status"]); got != "complete" {
		t.Fatalf("turn status = %q, want complete", got)
	}
	// 3) 注入的 user 消息带 Kind=continue（落库保留）
	msgs := newSessionStore(s.bus, "ins-test").LoadMessages(turn)
	var userKinds []string
	for _, m := range msgs {
		if m.Role == "user" {
			userKinds = append(userKinds, m.Kind)
		}
	}
	if len(userKinds) != 2 || userKinds[0] != "text" || userKinds[1] != assembleContinueKind {
		t.Fatalf("user kinds = %v, want [text %s]", userKinds, assembleContinueKind)
	}
	// 4) 拼接视为同一轮（全量、无重复）：continue 请求里既有的 user/assistant 各只出现一次
	reqs := requests()
	if len(reqs) < 2 {
		t.Fatalf("llm requests = %d, want >=2", len(reqs))
	}
	lastReq := reqs[len(reqs)-1]
	count := func(sub string) int {
		n := 0
		for _, m := range lastReq {
			if strings.Contains(m.Content, sub) {
				n++
			}
		}
		return n
	}
	if n := count("first question"); n != 1 {
		t.Fatalf("continue 请求中 first question 出现 %d 次，want 1（同轮不重复）: %+v", n, lastReq)
	}
	if n := count("ok-reply"); n != 1 {
		t.Fatalf("continue 请求中上一轮回复出现 %d 次，want 1: %+v", n, lastReq)
	}
	if n := count("继续"); n != 1 {
		t.Fatalf("continue 请求中继续文本出现 %d 次，want 1: %+v", n, lastReq)
	}
}

// TestTurnContinueResolvesLatestTurn：continue=true 且不带 turn → 后端按 session 取最近一轮复用。
func TestTurnContinueResolvesLatestTurn(t *testing.T) {
	llm, _ := recordLLMServer("ok-reply")
	defer llm.Close()
	s := newTestServer(t, llm)
	const session, turn = "s-clatest", "t-clatest"
	startTurn(t, s, session, turn)

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": "q",
	}))
	if c := lastComplete(collectTurn(t, s.bus, turn, 10*time.Second)); c == nil || c["status"] != "complete" {
		t.Fatalf("first turn not complete: %+v", c)
	}
	waitTurnIdle(t, s, session)

	// 不带 turn 的 continue：应解析出最近一轮（同一个 turn）
	v := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-cl", "instance_id": "ins-test", "session": session,
		"continue": true,
	})).Wait()
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("continue llm-start rejected: %+v", res)
	}
	if got := res["turn"]; got != turn {
		t.Fatalf("resolved turn = %v, want %q", got, turn)
	}
	// 空 turn 的 session-send：按 session 回退运行中轮次 → 注入 Kind=continue 并完成
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session,
		"type": "text-user", "content": "继续",
	}))
	if c := lastComplete(collectTurn(t, s.bus, turn, 10*time.Second)); c == nil || c["status"] != "complete" {
		t.Fatalf("continue(no turn) not complete: %+v", c)
	}
	msgs := newSessionStore(s.bus, "ins-test").LoadMessages(turn)
	found := false
	for _, m := range msgs {
		if m.Role == "user" && m.Kind == assembleContinueKind {
			found = true
		}
	}
	if !found {
		t.Fatalf("空 turn continue 未注入 Kind=%s: %+v", assembleContinueKind, msgs)
	}
}

// TestAutoContinueAccumulatesAnswer（WP2-3）：自动续写（finish==length → 内部续写）后，终态
// `llm-complete.text` 取**跨段累积全文**（首段 + 续写段），不再只含最后一段；落库两段均在
// （未因同 asstKey 覆盖丢失）。
func TestAutoContinueAccumulatesAnswer(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n == 0 {
			// 首段：内容 + finish_reason=length → 触发自动续写
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": "第一段"}, ""),
				sseChunk(map[string]any{}, "length"),
			})
			return
		}
		// 续写段：正常结束
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "第二段"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer srv.Close()

	s := newTestServer(t, srv)
	const session, turn = "s-acc", "t-acc"
	startTurn(t, s, session, turn)
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": "q",
	}))
	evs := collectTurn(t, s.bus, turn, 10*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	if got, _ := c["text"].(string); got != "第一段第二段" {
		t.Fatalf("llm-complete.text=%q want 累积全文 %q（跨段续写丢前段？）", got, "第一段第二段")
	}
	// 落库：两段 assistant 均在（未因同 asstKey 覆盖丢失）
	msgs := newSessionStore(s.bus, "ins-test").LoadMessages(turn)
	var texts []string
	for _, m := range msgs {
		if m.Role == "assistant" {
			texts = append(texts, m.Content)
		}
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "第一段") || !strings.Contains(joined, "第二段") {
		t.Fatalf("落库 assistant 正文 = %v，want 含两段", texts)
	}
}

// TestContinueKindNotTurnBoundary：Kind=continue 的 user 消息不构成新轮边界 → 同一轮（全量）。
func TestContinueKindNotTurnBoundary(t *testing.T) {
	seq := []ChatMsg{
		{Role: "user", Kind: "text", Content: "q"},
		{Role: "assistant", Content: "part"},
		{Role: "user", Kind: assembleContinueKind, Content: assembleContinueText},
		{Role: "assistant", Content: "rest"},
	}
	n := 0
	for _, m := range seq {
		if isTurnBoundary(m) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("turn boundaries = %d, want 1（continue 不新开轮）", n)
	}
	// 单一 turn → 整轮原文（组装侧下限：最新轮恒全量）。若被误按新轮切开，前段会退化为结论
	// "part"（输出变 1+2 条），故本条仍能证伪。
	out := assembleTurns(seq, 0, 0, 0, nil, nil, false)
	if len(out) != len(seq) || out[0].Content != "q" || out[2].Content != assembleContinueText {
		t.Fatalf("assembleTurns = %+v, want 单轮全量（同一轮，未切开）", out)
	}
}
