// ask 答复投递（AskUser S4 → 2026-11-10 调整）：同会话多问不再要求全部作答；
// 每条答复到达即投递，未答问题不阻塞已答内容。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// TestAskBatchDeliveryDoesNotWaitAll：同会话两问 —— 第一条答复立即投递，不等待第二条；
// 第二条到达时再投递，批次清空。
func TestAskBatchDeliveryDoesNotWaitAll(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := s.recoverTurnCtx("ins-a", "s-batch", "t-batch")
	t.Cleanup(tc.Close)

	a1 := s.ask("t-batch", "tc-1", map[string]any{"question": "Q1"}, "")
	a2 := s.ask("t-batch", "tc-2", map[string]any{"question": "Q2"}, "")
	if a1 == "" || a2 == "" {
		t.Fatal("ask 应返回 askID")
	}

	take := func(askID string) *askWaiter {
		t.Helper()
		s.mu.Lock()
		w, ok := s.takeAskLocked("", askID)
		s.mu.Unlock()
		if !ok {
			t.Fatalf("ask 登记未命中：%s", askID)
		}
		return w
	}

	text, ready := s.recordAskAnswer(take(a1), "A1")
	if !ready || !strings.Contains(text, "A1") {
		t.Fatalf("首条答复应立即投递（不等 Q2）：ready=%v text=%q", ready, text)
	}
	if strings.Contains(text, "Q2") {
		t.Fatalf("首条投递不应包含未答问题：%q", text)
	}
	s.mu.Lock()
	nBatch, open := len(s.askBatches), 0
	for _, b := range s.askBatches {
		open += len(b.open)
	}
	s.mu.Unlock()
	if nBatch != 1 || open != 1 {
		t.Fatalf("首条投递后应保留 Q2 未答批次：batches=%d open=%d", nBatch, open)
	}

	text, ready = s.recordAskAnswer(take(a2), "A2")
	if !ready || !strings.Contains(text, "A2") {
		t.Fatalf("第二条答复应立即投递：ready=%v text=%q", ready, text)
	}
	s.mu.Lock()
	nBatch = len(s.askBatches)
	s.mu.Unlock()
	if nBatch != 0 {
		t.Fatalf("全部投递后批次应删除：%d", nBatch)
	}
}

// TestAskBatchIsolatedPerSession：不同会话各自成批，互不等待。
func TestAskBatchIsolatedPerSession(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc1 := s.recoverTurnCtx("ins-a", "s-b1", "t-b1")
	tc2 := s.recoverTurnCtx("ins-a", "s-b2", "t-b2")
	t.Cleanup(tc1.Close)
	t.Cleanup(tc2.Close)

	a1 := s.ask("t-b1", "tc-1", map[string]any{"question": "Q1"}, "")
	a2 := s.ask("t-b2", "tc-2", map[string]any{"question": "Q2"}, "")

	take := func(askID string) *askWaiter {
		t.Helper()
		s.mu.Lock()
		w, ok := s.takeAskLocked("", askID)
		s.mu.Unlock()
		if !ok {
			t.Fatalf("ask 登记未命中：%s", askID)
		}
		return w
	}
	if text, ready := s.recordAskAnswer(take(a1), "A1"); !ready || !strings.Contains(text, "A1") {
		t.Fatalf("会话 1 单问应答应立即可投递：ready=%v text=%q", ready, text)
	}
	if text, ready := s.recordAskAnswer(take(a2), "A2"); !ready || !strings.Contains(text, "A2") {
		t.Fatalf("会话 2 单问应答应立即可投递：ready=%v text=%q", ready, text)
	}
}

// TestAskResolveAfterAnsweredDoesNotRedeliver：同会话两问 —— Q1 已答（立即投递），Q2 走「完成」。
// 「完成」不进入答复集合，也不得重复投递 Q1。
func TestAskResolveAfterAnsweredDoesNotRedeliver(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := s.recoverTurnCtx("ins-a", "s-resolve-batch", "t-resolve-batch")
	t.Cleanup(tc.Close)

	a1 := s.ask("t-resolve-batch", "tc-1", map[string]any{"question": "Q1"}, "")
	a2 := s.ask("t-resolve-batch", "tc-2", map[string]any{"question": "Q2"}, "")
	if a1 == "" || a2 == "" {
		t.Fatal("ask 应返回 askID")
	}

	take := func(askID string) *askWaiter {
		t.Helper()
		s.mu.Lock()
		w, ok := s.takeAskLocked("", askID)
		s.mu.Unlock()
		if !ok {
			t.Fatalf("ask 登记未命中：%s", askID)
		}
		return w
	}
	if text, ready := s.recordAskAnswer(take(a1), "A1"); !ready || !strings.Contains(text, "A1") {
		t.Fatalf("Q1 答复应立即投递：ready=%v text=%q", ready, text)
	}

	w2 := take(a2)
	s.mu.Lock()
	text, ready := s.resolveAskBatchLocked(w2)
	nBatch := len(s.askBatches)
	s.mu.Unlock()
	if ready || text != "" {
		t.Fatalf("Q1 已投递，「完成」不应重复投递：ready=%v text=%q", ready, text)
	}
	if nBatch != 0 {
		t.Fatalf("批次应删除：%d", nBatch)
	}
}

// TestPersistUndeliveredAskAnswer：投递失败兜底 —— 答复作为 user 消息落到原轮次，
// 重载/历史可见（不重试、不新起轮）。
func TestPersistUndeliveredAskAnswer(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	const session, turn = "s-fallback", "t-fallback"
	startTurn(t, s, session, turn)
	w := &askWaiter{
		askID: "ask-fallback", instanceID: "ins-test", turnID: turn,
		sessionID: session, toolID: "tc-fallback", question: "Q",
	}
	s.persistUndeliveredAskAnswer(w, "【用户回答】\n问：Q\n答：A")

	msgs := newSessionStore(s.bus, "ins-test").LoadMessages(turn)
	found := false
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "答：A") {
			found = true
		}
	}
	if !found {
		t.Fatalf("投递失败兜底应把答复写入原轮次：%+v", msgs)
	}
}

// askTimeoutLLM 是「超时到点自动回复」全链用例的确定性 LLM：
// 首个用户消息含 `ask timeout please` → 返回 ask_user{timeout:1, recommended:["B"], context}；
// 其余请求（占位结果续轮 / 超时答复后的新一轮）→ 返回文本 `ok`。
func askTimeoutLLM() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(lastText(body.Messages), "ask timeout please") {
			llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
			return
		}
		args := jb(map[string]any{
			"question": "确认继续？",
			"options": []any{
				map[string]any{"value": "A", "label": "A"},
				map[string]any{"value": "B", "label": "B"},
			},
			"recommended": []any{"B"},
			"timeout":     1,
			"context":     "发布窗口",
		})
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": ""}, ""),
			sseChunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "tc-timeout", "type": "function",
				"function": map[string]any{"name": "ask_user", "arguments": string(args)},
			}}}, "tool_calls"),
		})
	}))
}

// TestAskTimeoutAutoReplyFullChain 超时到点自动回复全链（AskUser §4.3 / §4.8；61 §4.2）：
// `timeout=1` + `recommended:["B"]` → 到期后：
//   - 原 ask_user tool-result 就地写「已超时」标记（答案正文只在后续用户消息里）；
//   - 自动答复作为**用户消息**投递，且只投递一次；
//   - 收到 `ask-center-remove{reason:"timeout"}`，且不重复；
//   - asks / askBatches 全部结算清空。
func TestAskTimeoutAutoReplyFullChain(t *testing.T) {
	llm := askTimeoutLLM()
	defer llm.Close()
	s := newTestServer(t, llm)

	const session, turn = "s-ask-timeout", "t-ask-timeout"
	startTurn(t, s, session, turn)

	rec := watchAskCenter(t, s)
	if res := subscribeAskCenter(t, s, map[string]any{"instance_id": "askc-timeout"}); res["ok"] != true {
		t.Fatalf("ask-center-subscribe 失败: %v", res)
	}

	// 真实工具调用链：LLM 发起 ask_user → turn 落占位 tool-result → 登记 ask。
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": "ask timeout please",
	})).Wait()

	var askID string
	waitCond(t, 15*time.Second, "session-ask / ask-center-add", func() bool {
		p := askPayloadOf(rec.anyAdd())
		askID = str(p["ask_id"])
		return askID != ""
	})

	// 提问载荷：expires_at 必带（timeout=1）、recommended 数组、context 随题下发。
	add := rec.addFor(askID)
	p := askPayloadOf(add)
	if got := str(add["work_dir"]); got != testWorkDir {
		t.Fatalf("ask-center-add.work_dir=%q want %q", got, testWorkDir)
	}
	expires := str(p["expires_at"])
	if expires == "" {
		t.Fatalf("timeout=1 应下发 expires_at: %+v", p)
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		t.Fatalf("expires_at 不是 RFC3339: %q (%v)", expires, err)
	}
	if d := time.Until(exp); d < -time.Second || d > 2*time.Second {
		t.Fatalf("expires_at 与 timeout=1 不符：%s（距现在 %v）", expires, d)
	}
	recommended, _ := p["recommended"].([]any)
	if len(recommended) != 1 || str(recommended[0]) != "B" {
		t.Fatalf("recommended 应按规格为 [\"B\"]：%+v", p["recommended"])
	}
	if got := str(p["context"]); got != "发布窗口" {
		t.Fatalf("session-ask.context=%q want 发布窗口", got)
	}

	// 到点：自动回复取推荐值 B，Ask 中心摘除 reason=timeout。
	waitCond(t, 10*time.Second, "ask-center-remove{reason:timeout}", func() bool {
		return rec.countRemovesFor(askID) == 1
	})
	rem := rec.snapshotRemoves()[0]
	if got := str(rem["reason"]); got != "timeout" {
		t.Fatalf("ask-center-remove.reason=%q want timeout", got)
	}
	// 规格：不重复（再等一个窗口，计数仍为 1）。
	time.Sleep(300 * time.Millisecond)
	if n := rec.countRemovesFor(askID); n != 1 {
		t.Fatalf("ask-center-remove 不应重复：%d", n)
	}
	if n := rec.countAddsFor(askID); n != 1 {
		t.Fatalf("ask-center-add 不应重复：%d", n)
	}

	s.mu.Lock()
	nAsks, nBatches := len(s.asks), len(s.askBatches)
	s.mu.Unlock()
	if nAsks != 0 || nBatches != 0 {
		t.Fatalf("超时结算后 ask/批次应清空：asks=%d batches=%d", nAsks, nBatches)
	}

	// tool-result 就地写超时标记（同一行；不再是「等待回复」占位，也不复制答案正文）。
	rows := toolRowsOf(t, turn)
	if len(rows) != 1 {
		t.Fatalf("ask 应恰一行 tool 记录：%d", len(rows))
	}
	content := persist.Sval(rows[0]["content"])
	if !strings.Contains(content, askTimeoutHistoryResult) {
		t.Fatalf("超时应把 tool-result 写为超时标记：%s", content)
	}
	if strings.Contains(content, `"content":"B"`) {
		t.Fatalf("超时答案不应复制进 tool-result（避免历史双份）：%s", content)
	}
	if strings.Contains(content, "已向用户提问，正在等待回复") {
		t.Fatalf("超时后 tool-result 不应保留占位文案：%s", content)
	}
	if persist.Sval(rows[0]["ask_id"]) == "" {
		t.Fatalf("tool 行应保留 ask_id：%v", rows[0])
	}

	// 同批用户消息只投递一次（按规格文案断言：问 + 答 B）。
	delivered := waitAskAnswerDelivered(t, s, session, turn, "答：B")
	if !strings.Contains(delivered, "问：确认继续？") {
		t.Fatalf("自动答复应携带原问题：%q", delivered)
	}
	if strings.Contains(delivered, "答：A") {
		t.Fatalf("自动答复应取推荐值 B（非 A）：%q", delivered)
	}
	waitTurnIdle(t, s, session)

	n := 0
	for _, tr := range sessionTurnRecords(t, s, session) {
		for _, m := range msgsOfTurn(t, str(tr["turn_id"])) {
			if persist.Sval(m["role"]) == "user" && strings.Contains(persist.Sval(m["content"]), "答：B") {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("超时自动答复应只投递一次（同批一条用户消息），got=%d", n)
	}
}
