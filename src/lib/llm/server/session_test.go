// sessionStore 总线化测试（A3 S2a）：会话读写原语经总线 persist 落库；同库直连断言
// （data.Prj）校验落库结果。语义覆盖原 session_test（BuildHistory summary/压缩、
// kind 往返、cleanup、快照上下文优先）。
package server

import (
	"context"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// newBusHarness 建独立总线 + persist 数据服务 + 注册 ins-test 实例
// （persist 应答 data-* 面；data.Register 直连同库供种子/断言）。
func newBusHarness(t *testing.T) mq.Bus {
	t.Helper()
	data.Reset()
	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	svc := persist.New(bus, persist.Options{UsrPath: t.TempDir() + "/usr.db"})
	if err := svc.Start(); err != nil {
		t.Fatalf("persist.Start: %v", err)
	}
	t.Cleanup(svc.Stop)
	wd := t.TempDir()
	t.Cleanup(data.Reset) // LIFO：先于 bus.Close 关缓存连接
	_ = bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": wd,
	}))
	time.Sleep(50 * time.Millisecond)
	data.Register("ins-test", wd, "") // 直连同库（种子/断言）
	return bus
}

// TestBuildHistoryNoSummary：无摘要 → 全部 turn 消息按序。
func TestBuildHistoryNoSummary(t *testing.T) {
	bus := newBusHarness(t)
	st := newSessionStore(bus, "ins-test")
	_ = st.EnsureSession("s1")
	_ = st.EnsureTurn("t1", "s1")
	_ = st.EnsureTurn("t2", "s1")
	_ = st.AppendMessage("t1", "user", "hello", "")
	_ = st.AppendMessage("t1", "assistant", "hi", "")
	_ = st.AppendMessage("t2", "user", "next", "")

	msgs := st.BuildHistory("s1", "")
	if len(msgs) != 3 {
		t.Fatalf("BuildHistory len=%d, want 3: %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "hello" || msgs[2].Content != "next" {
		t.Fatalf("order wrong: %+v", msgs)
	}
}

// TestBuildHistorySummary：最早 turn 有摘要 → 摘要代替被压缩 turn，其后未压缩 turn 全量。
func TestBuildHistorySummary(t *testing.T) {
	bus := newBusHarness(t)
	st := newSessionStore(bus, "ins-test")
	_ = st.EnsureSession("s1")
	_ = st.EnsureTurn("t1", "s1")
	_ = st.EnsureTurn("t2", "s1")
	_ = st.AppendMessage("t1", "user", "old q", "")
	_ = st.AppendMessage("t1", "assistant", "old a", "")
	_ = st.AppendMessage("t2", "user", "new q", "")
	_ = st.AppendMessage("t2", "assistant", "new a", "")
	_ = st.SetTurnSummary("t1", "用户问了旧问题，助手答了旧答案")

	msgs := st.BuildHistory("s1", "")
	// t1 被压缩 → 摘要（system）+ t2 两条
	if len(msgs) != 3 {
		t.Fatalf("BuildHistory len=%d, want 3: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || msgs[0].Content == "" {
		t.Fatalf("first msg should be summary, got %+v", msgs[0])
	}
	if msgs[1].Content != "new q" || msgs[2].Content != "new a" {
		t.Fatalf("uncompressed turns wrong: %+v", msgs)
	}
}

// TestBuildHistoryExcludeCurrent：排除当前 turn（llm-start 组装时）。
func TestBuildHistoryExcludeCurrent(t *testing.T) {
	bus := newBusHarness(t)
	st := newSessionStore(bus, "ins-test")
	_ = st.EnsureSession("s1")
	_ = st.EnsureTurn("t1", "s1")
	_ = st.EnsureTurn("t2", "s1")
	_ = st.AppendMessage("t1", "user", "hist", "")
	_ = st.AppendMessage("t2", "user", "current", "")

	msgs := st.BuildHistory("s1", "t2")
	if len(msgs) != 1 || msgs[0].Content != "hist" {
		t.Fatalf("exclude current failed: %+v", msgs)
	}
}

// TestBuildContextTokensReadsPrestored（P3 读通道）：`data-session-context` **只增**伴随数组
// turn_tokens 被组装侧读取（生产判定**真正取预存值**，不再对已预存轮另做一次实时估算读取）——
// 预存回合透出 {full, brief}；未预存回合字段缺省（消费方回退估算）。
func TestBuildContextTokensReadsPrestored(t *testing.T) {
	bus := newBusHarness(t)
	st := newSessionStore(bus, "ins-test")
	_ = st.EnsureSession("s1")
	_ = st.EnsureTurn("t1", "s1")
	_ = st.EnsureTurn("t2", "s1")
	_ = st.AppendMessage("t1", "user", "hist", "")
	_ = st.AppendMessage("t2", "user", "current", "")
	fullTok, briefTok := 1234, 56
	if err := st.CompleteTurnTokens("t1", "done", "stop", &fullTok, &briefTok); err != nil {
		t.Fatalf("CompleteTurnTokens: %v", err)
	}

	msgs, tokens := st.BuildContextTokens("s1", "", false)
	if len(msgs) != 2 {
		t.Fatalf("messages 应为 2 条：%+v", msgs)
	}
	if len(tokens) != 2 {
		t.Fatalf("turn_tokens 应为 2 项（升序）：%+v", tokens)
	}
	if tokens[0].TurnID != "t1" || tokens[0].Full == nil || *tokens[0].Full != 1234 ||
		tokens[0].Brief == nil || *tokens[0].Brief != 56 {
		t.Fatalf("t1 预存 token 应被读出：%+v", tokens[0])
	}
	if tokens[1].TurnID != "t2" || tokens[1].Full != nil || tokens[1].Brief != nil {
		t.Fatalf("t2 无预存值 → 字段缺省：%+v", tokens[1])
	}
	// 拆分（组装侧实际入参）：缺值位 = 0 → data.ResolveStoredTokens 回退实时估算。
	sf, sb := splitTurnTokens(tokens)
	if len(sf) != 2 || sf[0] != 1234 || sf[1] != 0 || sb[0] != 56 || sb[1] != 0 {
		t.Fatalf("splitTurnTokens=%v/%v", sf, sb)
	}
}

// TestSessionContextSnapshot：快照上下文优先（include_snapshot）——快照前缀 +
// snapshot_turn 之后 turns 消息；快照覆盖轮不重复展开。
func TestSessionContextSnapshot(t *testing.T) {
	bus := newBusHarness(t)
	st := newSessionStore(bus, "ins-test")
	_ = st.EnsureSession("s1")
	_ = st.EnsureTurn("t1", "s1")
	_ = st.EnsureTurn("t2", "s1")
	_ = st.AppendFull("t1", ChatMsg{Role: "user", Kind: "text", Content: "已压缩的问题"})
	_ = st.AppendFull("t2", ChatMsg{Role: "user", Kind: "text", Content: "新问题"})
	// 写快照（含 tool_calls / kind 往返）
	toolCall := ToolCall{
		ID:   "c1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "core_file_read", Arguments: `{"x":1}`},
	}
	if err := st.SetSnapshot("s1", []ChatMsg{
		{Role: "user", Kind: "text", Content: "已压缩的问题"},
		{Role: "assistant", Content: "已压缩的回答", ToolCalls: []ToolCall{toolCall}},
	}, "t1"); err != nil {
		t.Fatalf("SetSnapshot: %v", err)
	}
	msgs := st.BuildContext("s1", "", true) // include_snapshot：快照前缀 + t2
	if len(msgs) != 3 {
		t.Fatalf("context len=%d, want 3: %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "已压缩的问题" || msgs[2].Content != "新问题" {
		t.Fatalf("context order wrong: %+v", msgs)
	}
	if msgs[1].ToolCalls == nil || msgs[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("tool_calls lost in snapshot context: %+v", msgs[1])
	}
	// 无快照会话 → include_snapshot 自动回退全量历史（BuildHistory 语义）
	_ = st.EnsureSession("s2")
	_ = st.EnsureTurn("t9", "s2")
	_ = st.AppendMessage("t9", "user", "plain", "")
	back := st.BuildContext("s2", "", true)
	if len(back) != 1 || back[0].Content != "plain" {
		t.Fatalf("fallback history failed: %+v", back)
	}
}

// TestCleanupStaleTurns：启动清理把遗留 running 标 interrupted。
func TestCleanupStaleTurns(t *testing.T) {
	bus := newBusHarness(t)
	st := newSessionStore(bus, "ins-test")
	_ = st.EnsureSession("s1")
	_ = st.EnsureTurn("t-running", "s1")
	_ = st.EnsureTurn("t-done", "s1")
	_ = st.CompleteTurn("t-done", "complete", "stop")
	if err := st.CleanupStaleTurns(); err != nil {
		t.Fatalf("CleanupStaleTurns: %v", err)
	}
	prj, _ := data.PrjUsr("ins-test") // 会话/轮次落 prjusr（12-数据层），断言同库直读
	var rec data.Record
	ok, _ := prj.Table("turns").Get("t-running", &rec)
	if !ok || rec["status"] != "interrupted" {
		t.Fatalf("t-running should be interrupted: %+v", rec)
	}
	ok, _ = prj.Table("turns").Get("t-done", &rec)
	if !ok || rec["status"] != "complete" {
		t.Fatalf("t-done should stay complete: %+v", rec)
	}
}

// TestKindRoundTrip：kind（text/notify）经 AppendFull → LoadMessages 往返不丢失。
// 压缩模块（chonkpilot-plugin-compress）靠 kind 区分用户提问与工具完成通知。
func TestKindRoundTrip(t *testing.T) {
	bus := newBusHarness(t)
	st := newSessionStore(bus, "ins-test")
	_ = st.EnsureSession("s1")
	_ = st.EnsureTurn("t1", "s1")

	if err := st.AppendFull("t1", ChatMsg{Role: "user", Kind: "text", Content: "问题"}); err != nil {
		t.Fatalf("AppendFull text: %v", err)
	}
	if err := st.AppendFull("t1", ChatMsg{Role: "user", Kind: "notify", Content: "[工具通知] 完成"}); err != nil {
		t.Fatalf("AppendFull notify: %v", err)
	}
	msgs := st.LoadMessages("t1")
	if len(msgs) != 2 {
		t.Fatalf("LoadMessages len=%d", len(msgs))
	}
	if msgs[0].Kind != "text" || msgs[1].Kind != "notify" {
		t.Fatalf("kind lost in LoadMessages: %+v", msgs)
	}
}
