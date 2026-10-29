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

// seedDialog 建一个对话库并写入主 session 行（parent_id=""）。
func seedDialog(t *testing.T, cat *data.DB, dialogID, mainID, title, kind string) {
	t.Helper()
	sdb, release, err := OpenDialog(cat, dialogID)
	if err != nil {
		t.Fatalf("open dialog %s: %v", dialogID, err)
	}
	defer release()
	rec := data.Record{
		"session_id": mainID, "title": title, "parent_id": "",
		"created_at": t0, "updated_at": t0,
	}
	if kind != "" {
		rec["kind"] = kind
	}
	if err := sdb.Table("sessions").Insert(mainID, rec); err != nil {
		t.Fatalf("seed session %s: %v", mainID, err)
	}
}

// TestReconcileDialogs 覆盖：文件在行缺失 → 补行；标题漂移 → 回填；文件缺失 → 删行。
func TestReconcileDialogs(t *testing.T) {
	cat := openCatalog(t)
	d1, _ := data.NewDialogID()
	d2, _ := data.NewDialogID()
	seedDialog(t, cat, d1, "root1", "Root One", "main")
	seedDialog(t, cat, d2, "root2", "Root Two", "group_chat")
	for _, row := range []DialogRow{
		{DialogID: d1, File: DialogFileName(d1), Title: "Root One", Kind: "main", MainSessionID: "root1", UpdatedAt: t0},
		{DialogID: d2, File: DialogFileName(d2), Title: "Root Two", Kind: "group_chat", MainSessionID: "root2", UpdatedAt: t0},
	} {
		if err := PutDialog(cat, row); err != nil {
			t.Fatalf("put dialog: %v", err)
		}
	}

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
	id, err := data.NewDialogID()
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
		t.Fatal("自举 id 必须确定性")
	}
	if MigratedDialogID("root1") == MigratedDialogID("root2") {
		t.Fatal("不同根会话的自举 id 不应相同")
	}
}
