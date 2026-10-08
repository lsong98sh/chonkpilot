// snapshot.Set 单事务 update-or-insert 单测（A-19 / A-15）：会话不存在 → 补建完整会话行；
// 会话已存在 → 保留其余字段（title 等与快照字段同事务读改，不互覆）。
package snapshot

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

func openSnapshotTestDB(t *testing.T) *data.DB {
	t.Helper()
	db, err := data.Open(filepath.Join(t.TempDir(), "prjusr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testSnap() data.Snapshot {
	return data.Snapshot{
		SnapshotTurn: "t1",
		History:      []data.ChatMsg{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok"}},
	}
}

// TestSetCreatesCompleteSessionRow：会话不存在时 Set 补建完整会话行（口径同 SessionEnsure），
// 且快照可读回——不产生「查得到却列不出」的幽灵会话。
func TestSetCreatesCompleteSessionRow(t *testing.T) {
	db := openSnapshotTestDB(t)
	if err := Set(db, "s1", testSnap()); err != nil {
		t.Fatalf("set: %v", err)
	}
	var rec data.Record
	if ok, err := db.Table("sessions").Get("s1", &rec); err != nil || !ok {
		t.Fatalf("get s1: ok=%v err=%v", ok, err)
	}
	if got, _ := rec["title"].(string); got != "s1" {
		t.Fatalf("补建行 title 应 = session_id：%q", got)
	}
	if got, _ := rec["parent_id"].(string); got != "" {
		t.Fatalf("补建行 parent_id 应为空串（顶层）：%q", got)
	}
	if got, _ := rec["created_at"].(string); got == "" {
		t.Fatal("补建行缺 created_at")
	}
	if got, _ := rec["updated_at"].(string); got == "" {
		t.Fatal("补建行缺 updated_at")
	}
	snap, ok, err := Get(db, "s1")
	if err != nil || !ok {
		t.Fatalf("get snapshot: ok=%v err=%v", ok, err)
	}
	if snap.SnapshotTurn != "t1" || len(snap.History) != 2 {
		t.Fatalf("快照读回不符：%+v", snap)
	}
}

// TestSetPreservesExistingFields：会话已存在时 Set 只动快照字段 + updated_at，
// title / created_at 等其余字段保留（与 SessionTitle 并发不再互丢）。
func TestSetPreservesExistingFields(t *testing.T) {
	db := openSnapshotTestDB(t)
	const createdAt = "2020-01-01T00:00:00.000000000Z"
	if err := db.Table("sessions").Upsert("s2", data.Record{
		"session_id": "s2", "title": "自定义标题", "parent_id": "",
		"created_at": createdAt, "updated_at": createdAt,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if err := Set(db, "s2", testSnap()); err != nil {
		t.Fatalf("set: %v", err)
	}
	var rec data.Record
	if ok, err := db.Table("sessions").Get("s2", &rec); err != nil || !ok {
		t.Fatalf("get s2: ok=%v err=%v", ok, err)
	}
	if got, _ := rec["title"].(string); got != "自定义标题" {
		t.Fatalf("既有 title 被覆盖：%q", got)
	}
	if got, _ := rec["created_at"].(string); got != createdAt {
		t.Fatalf("created_at 被改写：%q", got)
	}
	if got, _ := rec["updated_at"].(string); got == createdAt {
		t.Fatalf("快照写应刷新 updated_at（原语义）：%q", got)
	}
	snap, ok, err := Get(db, "s2")
	if err != nil || !ok || snap.SnapshotTurn != "t1" || len(snap.History) != 2 {
		t.Fatalf("快照读回不符：ok=%v err=%v snap=%+v", ok, err, snap)
	}
}
