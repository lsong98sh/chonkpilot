// cleanupStaleToolPairs 启动清理单测（A-17）：遗留非终态 tool_pair → interrupted（状态列与
// content.result.status 同源同步），行内其余字段保留；终态行不受影响。
package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// TestCleanupStaleToolPairs 覆盖三种非终态（pending/running/provisional）→ interrupted，
// 以及 completed 行不被波及。
func TestCleanupStaleToolPairs(t *testing.T) {
	db, err := data.Open(filepath.Join(t.TempDir(), "prjusr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tb := db.Table("messages")
	rows := map[string]data.Record{
		// 有 result 段 → 状态列与 result.status 一并改
		"m1": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "pending",
			"content":    `{"call":{"tool_call_id":"c1","name":"w1"},"result":{"content":"r1","status":"pending"}}`,
			"created_at": "2026-01-01T00:00:00.000000001Z"},
		// 无 result 段 → 仅改状态列（视图回退读状态列）
		"m2": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "running",
			"content":    `{"call":{"tool_call_id":"c2","name":"w2"}}`,
			"created_at": "2026-01-01T00:00:00.000000002Z"},
		"m3": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "provisional",
			"content":    `{"call":{"tool_call_id":"c3","name":"w3"},"result":{"content":"r3","status":"provisional"}}`,
			"created_at": "2026-01-01T00:00:00.000000003Z"},
		// 终态行 → 不在清理候选（按状态等值查询），不被波及
		"m4": {"session_id": "s", "turn_id": "t1", "role": "tool", "tool_call_status": "completed",
			"content":    `{"call":{"tool_call_id":"c4","name":"w4"},"result":{"content":"r4","status":"completed"}}`,
			"created_at": "2026-01-01T00:00:00.000000004Z"},
	}
	for k, r := range rows {
		if err := tb.Insert(k, r); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}

	cleanupStaleToolPairs(db)

	for _, id := range []string{"m1", "m2", "m3"} {
		var rec data.Record
		if ok, err := tb.Get(id, &rec); err != nil || !ok {
			t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
		}
		if got, _ := rec["tool_call_status"].(string); got != "interrupted" {
			t.Fatalf("%s 状态列应为 interrupted：got=%q", id, got)
		}
		// 行内其余字段保留（RMW 收敛不丢字段）
		if got, _ := rec["session_id"].(string); got != "s" {
			t.Fatalf("%s 其余字段被破坏：session_id=%q", id, got)
		}
		// result 段存在时 result.status 同步为 interrupted（m2 无 result 段不判）
		if id == "m2" {
			continue
		}
		tc, ok := kernel.ParseToolContent(kernel.Sval(rec["content"]))
		if !ok || tc.Result == nil {
			t.Fatalf("%s content 解析失败：%v", id, rec["content"])
		}
		if tc.Result.Status != "interrupted" {
			t.Fatalf("%s result.status 应同步 interrupted：got=%q", id, tc.Result.Status)
		}
		if tc.Result.Content == "" {
			t.Fatalf("%s result.content 不应被清空", id)
		}
	}

	var rec data.Record
	if ok, err := tb.Get("m4", &rec); err != nil || !ok {
		t.Fatalf("get m4: ok=%v err=%v", ok, err)
	}
	if got, _ := rec["tool_call_status"].(string); got != "completed" {
		t.Fatalf("终态行不应被改：got=%q", got)
	}
}

// newSessionEnv 构造测试服务与实例 Scope（目录先于 data.Reset 的 Cleanup 创建，见 tasktree 同型）。
func newSessionEnv(t *testing.T) (*Service, facade.Scope) {
	t.Helper()
	usrDir := t.TempDir()
	wd := t.TempDir()
	dd := t.TempDir()
	data.Reset()
	t.Cleanup(data.Reset)
	s := New(kernel.NewBase(nil, kernel.Options{UsrPath: filepath.Join(usrDir, "usr.db"), AppDir: usrDir}))
	return s, facade.Scope{WorkDir: wd, DataDir: dd}
}

// TestSessionTitleUpdateInPreservesFields（A-23）：SessionTitle 读改写收进 UpdateIn 单事务——
// 只改 title/updated_at，其余字段（history/snapshot_turn 等）保留；行不存在 → 报错且不建行；
// updated_at 为纳秒口径（A-11）。
func TestSessionTitleUpdateInPreservesFields(t *testing.T) {
	s, scope := newSessionEnv(t)
	inst := "ins-title"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table("sessions")
	const created = "2026-01-01T00:00:00.000000000Z"
	if err := tb.Insert("s1", data.Record{
		"session_id": "s1", "parent_id": "", "title": "old",
		"created_at": created, "updated_at": created,
		"history": `[{"x":1}]`, "snapshot_turn": "t9",
	}); err != nil {
		t.Fatalf("insert s1: %v", err)
	}

	resp, err := s.SessionTitle(facade.SessionTitleRequest{InstanceID: inst, Scope: scope, SessionID: "s1", Title: "new"})
	if err != nil || !resp.OK {
		t.Fatalf("SessionTitle: err=%v resp=%+v", err, resp)
	}
	var rec data.Record
	if ok, err := tb.Get("s1", &rec); err != nil || !ok {
		t.Fatalf("get s1: ok=%v err=%v", ok, err)
	}
	if got := kernel.Sval(rec["title"]); got != "new" {
		t.Fatalf("title 未更新：got=%q", got)
	}
	if got := kernel.Sval(rec["history"]); got != `[{"x":1}]` {
		t.Fatalf("history 被整行覆盖丢失：got=%q", got)
	}
	if got := kernel.Sval(rec["snapshot_turn"]); got != "t9" {
		t.Fatalf("snapshot_turn 被整行覆盖丢失：got=%q", got)
	}
	if _, err := time.Parse(kernel.RFC3339FixedNano, kernel.Sval(rec["updated_at"])); err != nil {
		t.Fatalf("updated_at 应为纳秒口径：%q (%v)", rec["updated_at"], err)
	}

	// 行不存在 → 保持原 not found 语义（报错、不建行）
	if _, err := s.SessionTitle(facade.SessionTitleRequest{InstanceID: inst, Scope: scope, SessionID: "ghost", Title: "x"}); err == nil {
		t.Fatal("会话不存在应报错")
	}
	var ghost data.Record
	if ok, _ := tb.Get("ghost", &ghost); ok {
		t.Fatal("not found 路径不应建行")
	}
}

// TestTurnSetSummaryCompleteNoOrphanRow（A-34）：TurnSetSummary / TurnComplete 对**不存在**的 turn
// 放弃写入（UpdateIn 的 fn 返回 nil → 不建缺 session_id/created_at/turn_id 的半成品残行）；
// 行存在时照常更新且保留其余字段。
func TestTurnSetSummaryCompleteNoOrphanRow(t *testing.T) {
	s, scope := newSessionEnv(t)
	inst := "ins-turn"
	prj, err := s.PrjUsrFor(inst, scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	tb := prj.Table("turns")

	// 行不存在：两个写面都不得建残行
	if _, err := s.TurnSetSummary(facade.TurnSetSummaryRequest{InstanceID: inst, Scope: scope, TurnID: "ghost", Summary: "s"}); err != nil {
		t.Fatalf("TurnSetSummary(ghost): %v", err)
	}
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{InstanceID: inst, Scope: scope, TurnID: "ghost2", Status: "done"}); err != nil {
		t.Fatalf("TurnComplete(ghost2): %v", err)
	}
	var rec data.Record
	if ok, _ := tb.Get("ghost", &rec); ok {
		t.Fatal("turn 不存在不应建残行（summary）")
	}
	if ok, _ := tb.Get("ghost2", &rec); ok {
		t.Fatal("turn 不存在不应建残行（complete）")
	}

	// 行存在：照常更新（RMW 保留其余字段）
	if err := tb.Insert("t1", data.Record{
		"turn_id": "t1", "session_id": "s1", "status": "running",
		"created_at": "2026-01-01T00:00:00.000000000Z",
	}); err != nil {
		t.Fatalf("insert t1: %v", err)
	}
	if _, err := s.TurnSetSummary(facade.TurnSetSummaryRequest{InstanceID: inst, Scope: scope, TurnID: "t1", Summary: "summary"}); err != nil {
		t.Fatalf("TurnSetSummary(t1): %v", err)
	}
	done := 7
	if _, err := s.TurnComplete(facade.TurnCompleteRequest{InstanceID: inst, Scope: scope, TurnID: "t1", Status: "done", FullTokens: &done}); err != nil {
		t.Fatalf("TurnComplete(t1): %v", err)
	}
	if ok, err := tb.Get("t1", &rec); err != nil || !ok {
		t.Fatalf("get t1: ok=%v err=%v", ok, err)
	}
	if kernel.Sval(rec["session_id"]) != "s1" || kernel.Sval(rec["summary"]) != "summary" || kernel.Sval(rec["status"]) != "done" {
		t.Fatalf("行存在时更新异常：%v", rec)
	}
}
