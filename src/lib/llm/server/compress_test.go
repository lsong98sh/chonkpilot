// SESSION-1 阶段一/二：压缩内建（进程内、异步）的**宿主侧**单测：
// 单飞、CAS 丢弃、超窗兜底重试一次（payload 构造 / 阈值判定 / CAS 纯函数用例已随实现搬到
// 组件包 `src/lib/chonkpilot-session/compress_test.go`）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-session"
	chat "github.com/lsong98sh/chonkpilot-chat"
)

// TestIsContextOverflow：超窗错误识别（厂商文案差异大，按关键词）。
func TestIsContextOverflow(t *testing.T) {
	for _, msg := range []string{
		"This model's maximum context length is 128000 tokens",
		"context_length_exceeded",
		"prompt is too long: 200000 tokens > 128000 maximum",
	} {
		if !isContextOverflow(errors.New(msg)) {
			t.Fatalf("应识别为超窗: %q", msg)
		}
	}
	if isContextOverflow(errors.New("401 unauthorized")) {
		t.Fatal("鉴权错误不应识别为超窗")
	}
	if isContextOverflow(nil) {
		t.Fatal("nil 不应识别为超窗")
	}
}

// slowLLMServer 慢速 mock（400ms）：用于单飞并发断言（首个压缩在飞时第二个必须被拒）。
func slowLLMServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "摘要内容"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
}

// TestCompressCommitSingleFlightAndCAS：端到端（真实快照）——
// 压缩写入 `[system 摘要] + 保留段`、`snapshot_turn` 不变；同会话第二个任务被单飞拒绝；
// CAS 基准不符时丢弃（不覆盖快照）。
func TestCompressCommitSingleFlightAndCAS(t *testing.T) {
	llm := slowLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	completeTurnOne(t, s, "s-cmp", "t1", "hello compress")

	tc := &turnCtx{
		server:        s,
		req:           StartReq{InstanceID: "ins-test", Session: "s-cmp", Turn: "t2"},
		hist:          newSessionStore(s.bus, "ins-test").LoadMessages("t1"),
		turnTokens:    []facade.TurnToken{{TurnID: "t1"}},
		keepFullTurns: 1,
		briefBudget:   1,
	}
	a := tc.buildAssembly()
	if a.LastTurnID != "t1" {
		t.Fatalf("LastTurnID = %q, want t1", a.LastTurnID)
	}
	if len(a.RetainedMessages()) == 0 {
		t.Fatalf("保留段不应为空: histTurns=%d retainFrom=%d", a.HistTurns(), a.RetainFrom())
	}
	job := session.NewJob(a, "ins-test", "s-cmp", "t2")
	// 真实 payload 还含**当前轮的用户输入**（组装在首个输入落库之后）；此处补齐，
	// 使压缩输入的「末条 user」= 当前输入（历史 = t1）。
	job.Payload = append(append([]ChatMsg{}, a.Context.Messages...),
		ChatMsg{Role: "user", Kind: "text", Content: "q-now"})

	// ① 单飞：第一个在飞（慢 LLM 400ms）→ 第二个立刻被拒。
	if !s.startCompress(job) {
		t.Fatal("首个压缩应启动")
	}
	if s.startCompress(job) {
		t.Fatal("同会话已有任务在飞 → 第二个应被拒（单飞）")
	}
	waitCompressIdle(t, s, "ins-test", "s-cmp")

	// ② 回填：首条 = system 摘要，snapshot_turn 不变（= 压缩基准 t1）。
	snap := snapshotOf(t, s, "s-cmp")
	if snap == nil {
		t.Fatal("快照缺失")
	}
	if got := str(snap["snapshot_turn"]); got != "t1" {
		t.Fatalf("snapshot_turn = %q, want t1（锚点 = LastTurnID）", got)
	}
	hist, _ := snap["history"].([]any)
	if len(hist) < 2 {
		t.Fatalf("快照应含摘要 + 保留段: %+v", snap)
	}
	m0, _ := hist[0].(map[string]any)
	if str(m0["role"]) != "system" || !strings.Contains(str(m0["content"]), compressedSummaryPrefix) {
		t.Fatalf("首条应为压缩摘要: %+v", m0)
	}
	if !strings.Contains(str(m0["content"]), "摘要内容") {
		t.Fatalf("摘要正文应来自 LLM: %q", str(m0["content"]))
	}
	foundRetained := false
	for _, h := range hist[1:] {
		if m, _ := h.(map[string]any); strings.Contains(str(m["content"]), "hello compress") {
			foundRetained = true
		}
	}
	if !foundRetained {
		t.Fatalf("保留段应含上轮原文: %+v", hist)
	}

	// ③ CAS：基准不符（t9）→ 丢弃，快照不变。
	before := snapshotOf(t, s, "s-cmp")
	ok, err := s.commitCompressed(context.Background(), session.CompressionJob{
		InstanceID: "ins-test", SessionID: "s-cmp", Anchor: "t9",
		Retain: []ChatMsg{{Role: "user", Kind: "text", Content: "should-not-appear"}},
	}, "新摘要")
	if err != nil {
		t.Fatalf("commitCompressed: %v", err)
	}
	if ok {
		t.Fatal("CAS 基准不符 → 应丢弃")
	}
	after := snapshotOf(t, s, "s-cmp")
	if str(after["snapshot_turn"]) != str(before["snapshot_turn"]) {
		t.Fatalf("CAS 丢弃后 snapshot_turn 不应变化: before=%v after=%v", before["snapshot_turn"], after["snapshot_turn"])
	}
	if strings.Contains(stringOf(after["history"]), "should-not-appear") {
		t.Fatal("CAS 丢弃后不应写入新摘要")
	}
}

// overflowOnceLLM 第一次调用返回超窗错误，第二次成功（用于兜底重试断言）。
type overflowOnceLLM struct {
	calls int
	reqs  []chat.StepRequest
}

func (f *overflowOnceLLM) Step(_ context.Context, req chat.StepRequest, _ chat.StepOptions) (<-chan chat.StepEvent, error) {
	f.calls++
	f.reqs = append(f.reqs, req)
	if f.calls == 1 {
		return nil, errors.New("This model's maximum context length is 128000 tokens")
	}
	ch := make(chan chat.StepEvent, 2)
	ch <- chat.StepEvent{Type: chat.StepEventDelta, Text: "retry-ok"}
	ch <- chat.StepEvent{Type: chat.StepEventDone, Finish: "stop"}
	close(ch)
	return ch, nil
}

// TestEngineStepOverflowFallback：正常请求超窗 → 用同一份 payload 压缩 → 重新组装 → 重试一次。
func TestEngineStepOverflowFallback(t *testing.T) {
	llm := slowLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	completeTurnOne(t, s, "s-of", "t1", "hello overflow")

	tc := &turnCtx{
		server:        s,
		req:           StartReq{InstanceID: "ins-test", Session: "s-of", Turn: "t2"},
		hist:          newSessionStore(s.bus, "ins-test").LoadMessages("t1"),
		turnTokens:    []facade.TurnToken{{TurnID: "t1"}},
		keepFullTurns: 1,
		briefBudget:   1,
	}
	payload := []ChatMsg{
		{Role: "system", Content: "系统提示词"},
		{Role: "user", Kind: "text", Content: "q-old"},
		{Role: "assistant", Content: "a-old"},
		{Role: "user", Kind: "text", Content: "q-now"},
	}
	fake := &overflowOnceLLM{}
	l := engineStepLLM{tc: tc, inner: fake}
	ch, err := l.Step(context.Background(), chat.StepRequest{Messages: payload}, chat.StepOptions{})
	if err != nil {
		t.Fatalf("兜底重试后不应返回错误: %v", err)
	}
	var text strings.Builder
	finish := ""
	for ev := range ch {
		text.WriteString(ev.Text)
		if ev.Type == chat.StepEventDone {
			finish = ev.Finish
		}
	}
	if fake.calls != 2 {
		t.Fatalf("应恰好重试一次（共 2 次调用），got %d", fake.calls)
	}
	if text.String() != "retry-ok" || finish != "stop" {
		t.Fatalf("重试事件应原样透传: text=%q finish=%q", text.String(), finish)
	}
	// 重试请求 = 压缩后重新组装的上下文（含新摘要 system 段）。
	retried := fake.reqs[1].Messages
	foundSummary := false
	for _, m := range retried {
		if m.Role == "system" && strings.Contains(m.Content, compressedSummaryPrefix) {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatalf("重试应使用压缩后的上下文（含摘要段）: %+v", retried)
	}
	// 压缩已回填：snapshot_turn 仍为 t1，首条为摘要。
	snap := snapshotOf(t, s, "s-of")
	if snap == nil || str(snap["snapshot_turn"]) != "t1" {
		t.Fatalf("压缩应回填快照（snapshot_turn=t1）: %+v", snap)
	}
}

// errLLM 恒返回同一个错误（用于「非超窗不兜底」断言）。
type errLLM struct {
	calls int
	err   error
}

func (f *errLLM) Step(context.Context, chat.StepRequest, chat.StepOptions) (<-chan chat.StepEvent, error) {
	f.calls++
	return nil, f.err
}

// TestEngineStepOverflowNoFallback：非超窗错误 → 不压缩、不重试，原样返回错误（主流程语义不变）。
func TestEngineStepOverflowNoFallback(t *testing.T) {
	llm := slowLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := &turnCtx{
		server: s,
		req:    StartReq{InstanceID: "ins-test", Session: "s-of2", Turn: "t1"},
	}
	boom := errors.New("401 unauthorized")
	fake := &errLLM{err: boom}
	l := engineStepLLM{tc: tc, inner: fake}
	if _, err := l.Step(context.Background(),
		chat.StepRequest{Messages: []ChatMsg{{Role: "user", Content: "q"}}}, chat.StepOptions{}); err == nil {
		t.Fatal("非超窗错误应原样返回")
	}
	if fake.calls != 1 {
		t.Fatalf("非超窗错误不应重试，got calls=%d", fake.calls)
	}
}

// ── 辅助 ─────────────────────────────────────────────

// completeTurnOne 跑完一轮（session-start + session-send → 等终态），finish 会写快照
// （snapshot_turn = 该轮）。
func completeTurnOne(t *testing.T, s *Server, session, turn, text string) {
	t.Helper()
	startTurn(t, s, session, turn)
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": text,
	}))
	evs := collectTurn(t, s.bus, turn, 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("turn %s 未完成: %+v", turn, evs)
	}
	waitTurnGone(t, s, turn)
	if snapshotOf(t, s, session) == nil {
		t.Fatalf("turn %s 完成后应有快照", turn)
	}
}

// waitCompressIdle 等待同会话压缩任务结束（单飞闸门释放）。
func waitCompressIdle(t *testing.T, s *Server, instanceID, session string) {
	t.Helper()
	key := instKey(instanceID, session)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.compressMu.Lock()
		running := s.compressRunning[key]
		s.compressMu.Unlock()
		if !running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("压缩任务未在 10s 内结束")
}

// stringOf 取快照 history 的 JSON 文本（断言用）。
func stringOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
