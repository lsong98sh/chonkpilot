package sessionstore

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

const t0 = "2026-10-24T10:00:00.000000001Z"

// openCatalog 建一个临时 prjusr 库。
func openCatalog(t *testing.T) *data.DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "prjusr", "chonkpilot.db")
	db, err := data.OpenLayer(p, data.LayerPrjUsr)
	if err != nil {
		t.Fatalf("open prjusr: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedLegacy 造两棵树（root1+child1 / root2）+ 各域数据。
func seedLegacy(t *testing.T, cat *data.DB) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	must(cat.Table("sessions").Upsert("root1", data.Record{
		"session_id": "root1", "title": "Root One", "kind": "main", "parent_id": "",
		"created_at": t0, "updated_at": t0,
		"history": `[{"role":"user","content":"hi"}]`, "snapshot_turn": "t1",
	}))
	must(cat.Table("sessions").Upsert("child1", data.Record{
		"session_id": "child1", "title": "Child 1", "kind": "sub_chat", "parent_id": "root1",
		"created_at": t0, "updated_at": t0,
	}))
	must(cat.Table("sessions").Upsert("root2", data.Record{
		"session_id": "root2", "title": "Root Two", "kind": "group_chat", "parent_id": "",
		"created_at": t0, "updated_at": t0,
	}))
	must(cat.Table("turns").Upsert("t1", data.Record{"turn_id": "t1", "session_id": "root1", "status": "completed", "created_at": t0}))
	must(cat.Table("turns").Upsert("t2", data.Record{"turn_id": "t2", "session_id": "child1", "status": "completed", "created_at": t0}))
	must(cat.Table("messages").Insert("m1", data.Record{"session_id": "root1", "turn_id": "t1", "role": "user", "content": "hi", "client_msg_id": "c1", "created_at": t0}))
	// 孤儿消息：session_id 缺失 → 迁移时按 turn_id 回填到 child1。
	must(cat.Table("messages").Insert("m2", data.Record{"session_id": "", "turn_id": "t2", "role": "assistant", "content": "yo", "created_at": t0}))
	must(cat.Table("messages").Insert("m3", data.Record{"session_id": "root2", "turn_id": "t9", "role": "user", "content": "g", "created_at": t0}))
	must(cat.Table("tasktree").Upsert("k1", data.Record{"task_id": "k1", "session_id": "root1", "top_session": "root1", "status": "done", "created_at": t0}))
	must(cat.Table("inbox_items").Upsert("i1", data.Record{"session_id": "root1", "turn_id": "t1", "status": "pending", "created_at": t0}))
}

// TestMigrateLegacyToDialogs 覆盖：分组、快照搬家、消息孤儿回填、非破坏、幂等。
func TestMigrateLegacyToDialogs(t *testing.T) {
	cat := openCatalog(t)
	seedLegacy(t, cat)

	res, err := Migrate(cat)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if res.Dialogs != 2 || res.Sessions != 3 || res.Snapshots != 1 ||
		res.Messages != 3 || res.Turns != 2 || res.Tasktree != 1 || res.Inbox != 1 {
		t.Fatalf("搬运统计不符: %+v", res)
	}

	// 目录行：title/kind 取根 session；dialog_id ≠ session_id。
	row, ok, err := GetDialog(cat, MigratedDialogID("root1"))
	if err != nil || !ok {
		t.Fatalf("dialogs 行缺失: ok=%v err=%v", ok, err)
	}
	if row.Title != "Root One" || row.Kind != "main" || row.File != DialogFileName(row.DialogID) {
		t.Fatalf("目录行字段不符: %+v", row)
	}
	if row.DialogID == "root1" {
		t.Fatal("dialog_id 不应等于 session_id")
	}

	// 库内：一树两行、快照搬家、session 行不再带 history/snapshot_turn。
	sdb, release, err := OpenDialog(cat, row.DialogID)
	if err != nil {
		t.Fatalf("OpenDialog: %v", err)
	}
	defer release()
	var rootRec data.Record
	if ok, err := sdb.Table("sessions").Get("root1", &rootRec); err != nil || !ok {
		t.Fatalf("库内缺 root1: ok=%v err=%v", ok, err)
	}
	if _, has := rootRec["history"]; has {
		t.Fatal("新库 sessions 行不应再带 history")
	}
	if _, has := rootRec["snapshot_turn"]; has {
		t.Fatal("新库 sessions 行不应再带 snapshot_turn")
	}
	var snap data.Record
	if ok, err := sdb.Table("snapshot").Get("root1", &snap); err != nil || !ok {
		t.Fatalf("snapshot 未搬入: ok=%v err=%v", ok, err)
	}
	var childRec data.Record
	if ok, err := sdb.Table("sessions").Get("child1", &childRec); err != nil || !ok {
		t.Fatalf("库内缺子会话 child1: ok=%v err=%v", ok, err)
	}
	var orphan data.Record
	if ok, err := sdb.Table("messages").Get("m2", &orphan); err != nil || !ok {
		t.Fatalf("孤儿消息未搬入: ok=%v err=%v", ok, err)
	}
	if got := sval(orphan["session_id"]); got != "child1" {
		t.Fatalf("孤儿消息 session_id 应回填为 child1，实际 %q", got)
	}
	if ok, _ := sdb.Table("messages").Get("m3", &orphan); ok {
		t.Fatal("另一棵树的 m3 不应搬入本库")
	}

	// 非破坏：旧行仍在（回退可读）。
	var legacy data.Record
	if ok, err := cat.Table("sessions").Get("root1", &legacy); err != nil || !ok {
		t.Fatalf("旧 sessions 行应保留: ok=%v err=%v", ok, err)
	}

	// 幂等：改库内数据后重跑，不得覆盖。
	if err := sdb.Table("messages").UpdateIn("m1", func(rec data.Record) data.Record {
		rec["content"] = "edited-after-migrate"
		return rec
	}); err != nil {
		t.Fatalf("改动库内消息: %v", err)
	}
	res2, err := Migrate(cat)
	if err != nil {
		t.Fatalf("Migrate 重跑: %v", err)
	}
	if res2.Dialogs != 0 || res2.Skipped != 2 {
		t.Fatalf("重跑应全部跳过: %+v", res2)
	}
	var m1 data.Record
	if ok, _ := sdb.Table("messages").Get("m1", &m1); !ok || sval(m1["content"]) != "edited-after-migrate" {
		t.Fatalf("重跑覆盖了迁移后的新数据: %+v", m1)
	}
}

// TestReconcileDialogs 覆盖：文件在行缺失 → 补行；标题漂移 → 回填；文件缺失 → 删行。
func TestReconcileDialogs(t *testing.T) {
	cat := openCatalog(t)
	seedLegacy(t, cat)
	if _, err := Migrate(cat); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	d1 := MigratedDialogID("root1")
	d2 := MigratedDialogID("root2")

	// ① 删目录行 → 文件在 → 补行
	if err := cat.Table(DialogTable).Delete(d2); err != nil {
		t.Fatalf("删目录行: %v", err)
	}
	res, err := Reconcile(cat)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Added != 1 {
		t.Fatalf("应补 1 行，实际 %+v", res)
	}

	// ② 库内改名 → 对账回填 title
	sdb, release, err := OpenDialog(cat, d2)
	if err != nil {
		t.Fatalf("open d2: %v", err)
	}
	if err := sdb.Table("sessions").UpdateIn("root2", func(rec data.Record) data.Record {
		rec["title"] = "Renamed Two"
		rec["updated_at"] = "2026-10-24T11:00:00.000000001Z"
		return rec
	}); err != nil {
		t.Fatalf("update title: %v", err)
	}
	release()
	res, err = Reconcile(cat)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Updated != 1 {
		t.Fatalf("应回填 1 行，实际 %+v", res)
	}
	if row, _, _ := GetDialog(cat, d2); row.Title != "Renamed Two" {
		t.Fatalf("title 未回填: %+v", row)
	}

	// ③ 删文件 → 行失效 → 删行
	if err := data.DeleteDialog(cat.Path(), d1); err != nil {
		t.Fatalf("删库文件: %v", err)
	}
	res, err = Reconcile(cat)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("应删 1 行，实际 %+v", res)
	}
	if _, ok, _ := GetDialog(cat, d1); ok {
		t.Fatal("失效行应已删除")
	}
}

// TestDialogIDAndFileName 覆盖 id 生成与文件名反解。
func TestDialogIDAndFileName(t *testing.T) {
	id, err := NewDialogID()
	if err != nil {
		t.Fatalf("NewDialogID: %v", err)
	}
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("dialog_id 形状不符: %q", id)
	}
	name := DialogFileName(id)
	back, ok := DialogIDFromFile(name)
	if !ok || back != id {
		t.Fatalf("文件名反解失败: %q → %q ok=%v", name, back, ok)
	}
	if _, ok := DialogIDFromFile("dialog-x.db"); ok {
		t.Fatal("非法 hex 不应反解成功")
	}
	if got := MigratedDialogID("root1"); got != MigratedDialogID("root1") {
		t.Fatal("迁移 id 必须确定性")
	}
	if MigratedDialogID("root1") == MigratedDialogID("root2") {
		t.Fatal("不同根会话的迁移 id 不应相同")
	}
}
