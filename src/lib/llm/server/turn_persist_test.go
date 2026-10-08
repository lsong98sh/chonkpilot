// turn_persist_test.go — I-176「消息增量落库」：assistant 分段落库（同段落同一行）与 tool 发起即落
// running、终态回填同一行。经内嵌 persist 数据服务落库后直连 prjusr 库断言（少而准）。
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// msgsOfTurn 直连 prjusr 库读某轮全部消息行（created_at 升序；落库断言用）。
func msgsOfTurn(t *testing.T, turn string) []data.Record {
	t.Helper()
	db, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	recs, _, err := db.Table("messages").Query(data.Query{
		Where: data.Record{"turn_id": turn}, OrderBy: "created_at",
	})
	if err != nil {
		t.Fatalf("query messages: %v", err)
	}
	return recs
}

// toolRowsOf 取某轮的 role=tool 行。
func toolRowsOf(t *testing.T, turn string) []data.Record {
	t.Helper()
	var out []data.Record
	for _, r := range msgsOfTurn(t, turn) {
		if persist.Sval(r["role"]) == "tool" {
			out = append(out, r)
		}
	}
	return out
}

// TestToolRunningRowBackfilledSameRow：工具「发起即落 running」→ 终态「就地回填同一行」
// （恒一行、created_at 保持、状态列终态）——不产生第二行（不破坏 LLM 重放/前端卡片）。
func TestToolRunningRowBackfilledSameRow(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-i176-a", "t-i176-a")

	tc := s.lookupTurn("t-i176-a")
	if tc == nil {
		t.Fatal("turn ctx not found")
	}
	tc.persistToolRunning("call-i176", "core_file_read", map[string]any{"path": "a.txt"})

	rows := toolRowsOf(t, "t-i176-a")
	if len(rows) != 1 {
		t.Fatalf("发起后 role=tool 行数=%d，want 1: %+v", len(rows), rows)
	}
	if st := persist.Sval(rows[0]["tool_call_status"]); st != "running" {
		t.Fatalf("发起后状态=%q，want running", st)
	}
	createdAt := persist.Sval(rows[0]["created_at"])

	tc.persistToolResult("call-i176", "file content", "completed", nil)

	rows = toolRowsOf(t, "t-i176-a")
	if len(rows) != 1 {
		t.Fatalf("终态回填后 role=tool 行数=%d，want 1（不得新增行）", len(rows))
	}
	if st := persist.Sval(rows[0]["tool_call_status"]); st != "completed" {
		t.Fatalf("终态状态=%q，want completed", st)
	}
	if got := persist.Sval(rows[0]["created_at"]); got != createdAt {
		t.Fatalf("回填应保留原 created_at（时间序位置不变）：%q → %q", createdAt, got)
	}
	// 结果段已回填（content = {call,result}；call 段含 tool_call_id）
	content := persist.Sval(rows[0]["content"])
	if !strings.Contains(content, "file content") || !strings.Contains(content, "call-i176") {
		t.Fatalf("回填内容应含 call+result：%s", content)
	}
}

// TestAssistantSegmentPersistSameRow：assistant 分段落库写**同一行**（key 回填），
// 恒一条消息（不因分段而多条）。
func TestAssistantSegmentPersistSameRow(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-i176-b", "t-i176-b")

	tc := s.lookupTurn("t-i176-b")
	if tc == nil {
		t.Fatal("turn ctx not found")
	}
	k1, err := tc.persistMessageKeyed(ChatMsg{Role: "assistant", Content: "第一段", Reasoning: "想"}, "")
	if err != nil || k1 == "" {
		t.Fatalf("首段落库失败: key=%q err=%v", k1, err)
	}
	k2, err := tc.persistMessageKeyed(ChatMsg{Role: "assistant", Content: "第一段第二段", Reasoning: "想"}, k1)
	if err != nil || k2 != k1 {
		t.Fatalf("分段落库应回填同一主键：k1=%q k2=%q err=%v", k1, k2, err)
	}

	var assistants []data.Record
	for _, r := range msgsOfTurn(t, "t-i176-b") {
		if persist.Sval(r["role"]) == "assistant" {
			assistants = append(assistants, r)
		}
	}
	if len(assistants) != 1 {
		t.Fatalf("assistant 行数=%d，want 1（分段写同一行）", len(assistants))
	}
	if c := persist.Sval(assistants[0]["content"]); c != "第一段第二段" {
		t.Fatalf("assistant 内容=%q，want 末次累积「第一段第二段」", c)
	}
}

// TestToolRetryBackfillsInterruptedRow：崩溃现场（发起已落 running 行）→ 启动清理标 interrupted →
// tool-retry 重跑 → 结果**回填同一行**（恒一行、终态 completed），不产生重复 tool_pair。
func TestToolRetryBackfillsInterruptedRow(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-i176-c", "t-i176-c")

	persistInterruptedTurn(t, s, "t-i176-c", "读取 a.txt", "call-r176", "core_file_read", `{"path":"a.txt"}`)
	// 崩溃现场：发起即落的 running 行（I-176 新足迹）
	st := newSessionStore(s.bus, "ins-test")
	if _, err := st.AppendMsgMap("t-i176-c", map[string]any{
		"role": "tool", "content": `{"call":{"tool_call_id":"call-r176","name":"core_file_read"}}`,
		"tool_call_id": "call-r176", "tool_call_status": "running",
	}, ""); err != nil {
		t.Fatalf("seed running row: %v", err)
	}
	// 启动清理：遗留非终态 → interrupted（可重试）
	if err := st.CleanupStaleTurns(); err != nil {
		t.Fatalf("cleanup stale: %v", err)
	}
	rows := toolRowsOf(t, "t-i176-c")
	if len(rows) != 1 || persist.Sval(rows[0]["tool_call_status"]) != "interrupted" {
		t.Fatalf("清理后应为 1 行 interrupted：%+v", rows)
	}
	makeInterruptedNode(t, s, "s-i176-c", "t-i176-c", "call-r176", "core_file_read", map[string]any{"path": "a.txt"})

	wt := watchTurn(t, s.bus, "t-i176-c")
	res := retryReply(t, s, map[string]any{"session_id": "s-i176-c", "task_id": "call-r176"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("tool-retry rejected: %+v", res)
	}
	evs := wt.waitComplete(10 * time.Second)
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("retry 后无 complete：%+v", evs)
	}

	rows = toolRowsOf(t, "t-i176-c")
	if len(rows) != 1 {
		t.Fatalf("重试回填应恒 1 行（不得重复）：%+v", rows)
	}
	if st := persist.Sval(rows[0]["tool_call_status"]); st != "completed" {
		t.Fatalf("重试后状态=%q，want completed", st)
	}
}
