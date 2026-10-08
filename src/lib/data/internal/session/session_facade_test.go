// cleanupStaleToolPairs 启动清理单测（A-17）：遗留非终态 tool_pair → interrupted（状态列与
// content.result.status 同源同步），行内其余字段保留；终态行不受影响。
package session

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
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
