// 上下文压缩（四步策略，2026-11-14）的宿主侧单测：
//   - 2.1 → 2.2 简化的阈值切换（当前轮不得被简化）；
//   - 2.2 → 2.3 同步压缩（摘要落快照 + Meta 标识 + tc.hist 替换）；
//   - 2.4 压缩后仍超阈值 / 无历史轮 → 明确错误；
//   - 厂商超窗兜底仍可同步压缩并重试一次。
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

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	chat "github.com/lsong98sh/chonkpilot-agent/chat"
	"github.com/lsong98sh/chonkpilot-agent/session"
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

// summaryLLMServer 返回固定摘要。
func summaryLLMServer(summary string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": summary}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
}

// prepareSessionTurns 注册实例 + 建会话 + 建两个 turn 行（**不**起 live turn，避免 session busy）。
func prepareSessionTurns(t *testing.T, s *Server, sess string, turns ...string) *sessionStore {
	t.Helper()
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	time.Sleep(50 * time.Millisecond)
	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession(sess); err != nil {
		t.Fatalf("EnsureSession(%s): %v", sess, err)
	}
	for _, turn := range turns {
		if err := st.EnsureTurn(turn, sess); err != nil {
			t.Fatalf("EnsureTurn(%s): %v", turn, err)
		}
	}
	return st
}

// adaptiveSetup 造一个「一轮真实历史 + 当前轮已落库提问」的 turnCtx（快照覆盖 t1）。
// hist 同时作为 t1 消息落库（含 tool 结果，长度可控）。
func adaptiveSetup(t *testing.T, s *Server, sess, histUser, histToolResult, current string) *turnCtx {
	t.Helper()
	st := prepareSessionTurns(t, s, sess, "t1", "t2")
	// 历史轮 t1：user + assistant(tool_calls) + tool + assistant 文本。
	appendFull(t, st, "t1", ChatMsg{Role: "user", Kind: "text", Content: histUser})
	appendFull(t, st, "t1", ChatMsg{
		Role: "assistant", Content: "中间说明", Reasoning: "历史思考",
		ToolCalls: []ToolCall{{ID: "c1", Name: "core_file_read", Arguments: "{}"}},
	})
	appendFull(t, st, "t1", ChatMsg{Role: "tool", ToolCallID: "c1", Content: histToolResult})
	appendFull(t, st, "t1", ChatMsg{Role: "assistant", Content: "结论一"})
	if err := st.SetSnapshot(sess, st.LoadMessages("t1"), "t1"); err != nil {
		t.Fatalf("SetSnapshot: %v", err)
	}
	// 当前轮 t2：已落库的 user 提问（组装从库带回）。
	appendMsg(t, st, "t2", "user", current)
	return &turnCtx{
		server:          s,
		req:             StartReq{InstanceID: "ins-test", Session: sess, Turn: "t2"},
		hist:            st.LoadMessages("t1"),
		turnTokens:      []facade.TurnToken{{TurnID: "t1"}},
		compressTrigger: 1 << 30, // 先取全量口径，再按需下调
	}
}

// TestBuildAssemblySimplifiedStage：阈值介于全量与简化之间 → 走 2.2：
// 历史轮的 reasoning / tool_calls / tool 结果被去掉，当前轮提问保持完整；本轮记录简化标识。
func TestBuildAssemblySimplifiedStage(t *testing.T) {
	llm := summaryLLMServer("不应调用摘要")
	defer llm.Close()
	s := newTestServer(t, llm)
	tc := adaptiveSetup(t, s, "s-simp", strings.Repeat("历史提问", 300), strings.Repeat("工具输出", 400), "当前提问")

	fullRes, err := tc.buildAssembly()
	if err != nil {
		t.Fatalf("全量组装: %v", err)
	}
	if fullRes.Usage.Total <= 0 {
		t.Fatalf("全量总量应 > 0: %+v", fullRes.Usage)
	}
	// 阈值 = 全量 - 1 → 必然超阈值；简化态应低于全量 → 命中 2.2。
	tc.compressTrigger = fullRes.Usage.Total - 1
	res, err := tc.buildAssembly()
	if err != nil {
		t.Fatalf("简化组装不应报错: %v", err)
	}
	if !tc.simplified || tc.simplifiedFrom != "t1" {
		t.Fatalf("应记录简化标识: simplified=%v from=%q", tc.simplified, tc.simplifiedFrom)
	}
	if res.Usage.Total > tc.compressTrigger {
		t.Fatalf("简化后应回到阈值内: %+v trigger=%d", res.Usage, tc.compressTrigger)
	}
	joined := stringOf(res.Context.Messages)
	if strings.Contains(joined, "工具输出") {
		t.Fatalf("历史轮的 tool 结果应被去掉: %s", joined)
	}
	if strings.Contains(joined, "历史思考") {
		t.Fatalf("历史 reasoning 应被去掉: %s", joined)
	}
	if !strings.Contains(joined, "当前提问") {
		t.Fatalf("当前轮不得被简化: %s", joined)
	}
	// 2.2 不得改写快照（原文未降级）。
	snap := snapshotOf(t, s, "s-simp")
	if snap == nil || str(snap["snapshot_turn"]) != "t1" {
		t.Fatalf("简化不应改写快照: %+v", snap)
	}
}

// TestBuildAssemblyCompressStage：简化也不达标 → 2.3 同步压缩：
// 摘要落快照（`[system 摘要]`、snapshot_turn = 最后历史轮、Meta 带 compress 标识）、
// 本轮历史替换为摘要，组装结果 = 摘要 + 当前轮。
func TestBuildAssemblyCompressStage(t *testing.T) {
	llm := summaryLLMServer("压缩摘要正文")
	defer llm.Close()
	s := newTestServer(t, llm)
	tc := adaptiveSetup(t, s, "s-comp", strings.Repeat("历史提问", 300), strings.Repeat("工具输出", 400), "当前提问")

	fullRes, err := tc.buildAssembly()
	if err != nil {
		t.Fatalf("全量组装: %v", err)
	}
	// 先取简化态总量：临时用全量阈值计算简化口径。
	tc.simplified = false
	tc.compressTrigger = 1 << 30
	simp := tc.composer().Build(context.Background(), session.Request{
		CurrentTurnID: tc.req.Turn, Mode: session.ModeSimplified,
	})
	// 阈值 = 简化 - 1 → 简化超阈值，进入压缩；压缩结果（摘要较小）应回到阈值内。
	tc.compressTrigger = simp.Usage.Total - 1
	res, err := tc.buildAssembly()
	if err != nil {
		t.Fatalf("压缩阶段不应报错: %v", err)
	}
	if !tc.compressed || !tc.compressAttempted {
		t.Fatalf("应记录压缩标识: compressed=%v attempted=%v", tc.compressed, tc.compressAttempted)
	}
	if len(tc.hist) != 1 || !strings.HasPrefix(tc.hist[0].Content, compressedSummaryPrefix) {
		t.Fatalf("本轮历史应替换为摘要: %+v", tc.hist)
	}
	if len(tc.turnTokens) != 0 {
		t.Fatalf("压缩后 turnTokens 应清空: %+v", tc.turnTokens)
	}
	if !strings.Contains(stringOf(res.Context.Messages), "压缩摘要正文") ||
		!strings.Contains(stringOf(res.Context.Messages), "当前提问") {
		t.Fatalf("压缩后组装结果应为摘要 + 当前轮: %s", stringOf(res.Context.Messages))
	}
	// 快照：`[system 摘要]`、snapshot_turn = t1、Meta 标识。
	snap := snapshotOf(t, s, "s-comp")
	if snap == nil || str(snap["snapshot_turn"]) != "t1" {
		t.Fatalf("压缩快照锚点应为 t1: %+v", snap)
	}
	histAny, _ := snap["history"].([]any)
	if len(histAny) != 1 {
		t.Fatalf("2.3 语义 = 全量压缩，不保留旧历史原文: %+v", histAny)
	}
	m0, _ := histAny[0].(map[string]any)
	if str(m0["role"]) != "system" || !strings.Contains(str(m0["content"]), "压缩摘要正文") {
		t.Fatalf("快照首条应为摘要: %+v", m0)
	}
	// Meta 随快照透传（不发给 LLM；门面 JSON 键为 `_meta`）。
	if !strings.Contains(stringOf(m0["_meta"]), "compressed_from") {
		t.Fatalf("摘要 Meta 应携带 compressed_from: %+v", m0)
	}
	if got := fullRes.Usage.Total; got <= 0 {
		t.Fatalf("全量总量应 > 0: %d", got)
	}
}

// TestBuildAssemblyCompactionError：阈值极低（压缩后仍超）→ 2.4 明确错误。
func TestBuildAssemblyCompactionError(t *testing.T) {
	llm := summaryLLMServer("摘要")
	defer llm.Close()
	s := newTestServer(t, llm)
	tc := adaptiveSetup(t, s, "s-err", "q1", "工具输出", "当前提问")
	tc.compressTrigger = 1 // 固定开销本身已超阈值 → 压缩后仍超
	_, err := tc.buildAssembly()
	if err == nil {
		t.Fatal("压缩后仍超阈值应报错")
	}
	if !strings.Contains(err.Error(), "context compaction failed") ||
		!strings.Contains(err.Error(), "after full/simplified/compressed stages") {
		t.Fatalf("错误文案应明确指出四步压缩失败: %v", err)
	}
}

// TestBuildAssemblyNoHistoryError：超阈值且没有历史轮（只剩当前轮）→ 2.4 报错。
func TestBuildAssemblyNoHistoryError(t *testing.T) {
	llm := summaryLLMServer("摘要")
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-nohist", "t1")
	st := newSessionStore(s.bus, "ins-test")
	appendMsg(t, st, "t1", "user", "当前提问")
	tc := &turnCtx{
		server:          s,
		req:             StartReq{InstanceID: "ins-test", Session: "s-nohist", Turn: "t1"},
		compressTrigger: 1,
	}
	_, err := tc.buildAssembly()
	if err == nil {
		t.Fatal("无历史轮且超阈值应报错（不得静默截断）")
	}
	if !strings.Contains(err.Error(), "context compaction failed") {
		t.Fatalf("错误文案不符: %v", err)
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

// TestEngineStepOverflowFallback：正常请求超窗 → 用同一压缩函数同步压缩 → 重新组装 → 重试一次。
func TestEngineStepOverflowFallback(t *testing.T) {
	llm := summaryLLMServer("超窗兜底摘要")
	defer llm.Close()
	s := newTestServer(t, llm)
	st := prepareSessionTurns(t, s, "s-of", "t1", "t2")
	appendMsg(t, st, "t1", "user", "hello overflow")
	appendMsg(t, st, "t1", "assistant", "answer")
	if err := st.SetSnapshot("s-of", st.LoadMessages("t1"), "t1"); err != nil {
		t.Fatalf("SetSnapshot: %v", err)
	}
	tc := &turnCtx{
		server:     s,
		req:        StartReq{InstanceID: "ins-test", Session: "s-of", Turn: "t2"},
		hist:       st.LoadMessages("t1"),
		turnTokens: []facade.TurnToken{{TurnID: "t1"}},
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
	if !strings.Contains(stringOf(fake.reqs[1].Messages), "超窗兜底摘要") {
		t.Fatalf("重试应使用压缩后的上下文（含摘要段）: %+v", fake.reqs[1].Messages)
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
	llm := summaryLLMServer("摘要")
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

// TestCompressSynchronousOnTriggeringTurn（端到端）：显式低阈值 → 第二轮组装时同步压缩；
// 终态快照 = 摘要 + 本轮消息（snapshot_turn = 本轮），且会话原轮次消息不因压缩删除。
func TestCompressSynchronousOnTriggeringTurn(t *testing.T) {
	llm := summaryLLMServer("端到端摘要")
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	prj, release, err := data.Prj("ins-test")
	if err != nil {
		t.Fatalf("data.Prj: %v", err)
	}
	// 阈值极低：第二轮必然压缩（摘要很小，压缩后回到阈值内亦可；这里取固定开销以上的低值）。
	if err := data.SetConfig(prj, "compress_trigger_tokens", "1000000000"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	// 先按大阈值跑两轮（保证消息落库），再以低阈值跑第三轮 → 组装期同步压缩。
	completeTurnOne(t, s, "s-term", "t1", "hello one")
	completeTurnOne(t, s, "s-term", "t2", "hello two")
	if err := data.SetConfig(prj, "compress_trigger_tokens", "1"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	release()
	// 阈值 1：第三方请求（t3）组装时压缩后仍超 → 本轮 error 终态（2.4 显式报错，不静默截断）。
	startTurn(t, s, "s-term", "t3")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-term", "turn": "t3",
		"type": "text-user", "content": "hello three",
	}))
	evs := collectTurn(t, s.bus, "t3", 10*time.Second)
	done := lastComplete(evs)
	if done == nil {
		t.Fatalf("t3 应有终态: %+v", evs)
	}
	if str(done["status"]) != "error" {
		t.Fatalf("压缩后仍超阈值应 error 终态: %+v", done)
	}
	if !strings.Contains(str(done["message"]), "context compaction failed") {
		t.Fatalf("错误应到达轮终态: %+v", done)
	}
	// 原轮次消息未丢（压缩只变换快照）。
	st := s.newSessionStore("ins-test")
	joined := stringOf(st.LoadMessages("t1")) + stringOf(st.LoadMessages("t2"))
	for _, want := range []string{"hello one", "hello two"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("压缩后会话轮次丢失 %q：%s", want, joined)
		}
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

// stringOf 取消息 / 快照 history 的 JSON 文本（断言用）。
func stringOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
