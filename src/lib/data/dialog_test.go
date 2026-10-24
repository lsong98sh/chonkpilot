package data

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDialogDBPath 校验一树一库的路径规则（`<prjusr>/dialogs/dialog-<hex(id)>.db`）。
func TestDialogDBPath(t *testing.T) {
	prjUsr := filepath.Join(t.TempDir(), "users", "u1", "chonkpilot.db")
	got := DialogDBPath(prjUsr, "dlg-1")
	want := filepath.Join(filepath.Dir(prjUsr), "dialogs", "dialog-"+hex.EncodeToString([]byte("dlg-1"))+".db")
	if got != want {
		t.Fatalf("DialogDBPath = %q, want %q", got, want)
	}
	if !strings.Contains(got, string(filepath.Separator)+"dialogs"+string(filepath.Separator)) {
		t.Fatalf("路径应位于 dialogs/ 下: %q", got)
	}
}

// TestOpenDeleteDialog 校验新布局的建库 / 删库与目录守卫。
func TestOpenDeleteDialog(t *testing.T) {
	prjUsr := filepath.Join(t.TempDir(), "prjusr.db")
	cat, err := OpenLayer(prjUsr, LayerPrjUsr)
	if err != nil {
		t.Fatalf("open prjusr: %v", err)
	}
	defer cat.Close()
	path := DialogDBPath(prjUsr, "d1")
	db, release, err := OpenDialog(prjUsr, "d1")
	if err != nil {
		t.Fatalf("OpenDialog: %v", err)
	}
	if db.Layer() != LayerDialog {
		t.Fatalf("layer = %q, want %q", db.Layer(), LayerDialog)
	}
	// 新库应已建出全部表（同构迁移）：sessions / messages / snapshot / meta。
	if err := db.Table("sessions").Insert("s1", Record{"session_id": "s1", "parent_id": ""}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("库文件应存在: %v", err)
	}
	if err := DeleteDialog(prjUsr, "d1"); err != nil {
		t.Fatalf("DeleteDialog: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("库文件应已删除，stat err = %v", err)
	}
	// 幂等：再删一次不报错。
	if err := DeleteDialog(prjUsr, "d1"); err != nil {
		t.Fatalf("DeleteDialog 幂等失败: %v", err)
	}
}

// TestMessagesClientMsgIndex 校验 `(session_id, client_msg_id)` 幂等键索引：
// 命中单条；仅 session_id 的查询仍走原索引并返回全部（优先级不变）。
func TestMessagesClientMsgIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.db")
	db, err := OpenLayer(path, LayerPrjUsr)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	tb := db.Table("messages")
	rows := map[string]Record{
		"m1": {"session_id": "s1", "turn_id": "t1", "client_msg_id": "c1", "created_at": "2026-10-24T10:00:00.000000001Z"},
		"m2": {"session_id": "s1", "turn_id": "t2", "client_msg_id": "c2", "created_at": "2026-10-24T10:00:00.000000002Z"},
		"m3": {"session_id": "s2", "turn_id": "t3", "client_msg_id": "c1", "created_at": "2026-10-24T10:00:00.000000003Z"},
	}
	for k, rec := range rows {
		if err := tb.Insert(k, rec); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}
	recs, _, err := tb.Query(Query{Where: Record{"session_id": "s1", "client_msg_id": "c1"}})
	if err != nil {
		t.Fatalf("query by client id: %v", err)
	}
	if len(recs) != 1 || recs[0][KeyField] != "m1" {
		t.Fatalf("幂等键查询应命中 m1，实际 %v", recs)
	}
	recs, _, err = tb.Query(Query{Where: Record{"session_id": "s1"}, OrderBy: "created_at"})
	if err != nil {
		t.Fatalf("query by session: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("按 session 查询应返回 2 条，实际 %d", len(recs))
	}
}
