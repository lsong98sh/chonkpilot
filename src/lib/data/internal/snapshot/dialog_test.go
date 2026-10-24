package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// setupInst 注册一个隔离实例（data-dir 非空 → prjusr = <data>/chonkpilot.db），返回实例 id 与 prjusr 路径。
func setupInst(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "ws")
	dataDir := filepath.Join(dir, "dd")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	id := "inst-" + filepath.Base(dir)
	data.Register(id, work, dataDir)
	t.Cleanup(func() { data.Unregister(id) }) // 关闭 prjusr 连接，允许 TempDir 清理
	db, err := data.PrjUsr(id)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	return id, db.Path()
}

// TestSnapshotDialogRouting 覆盖 P3-1：带 dialog_id → 读/写对话库，不污染 prjusr / 旧布局。
func TestSnapshotDialogRouting(t *testing.T) {
	instID, prjUsr := setupInst(t)
	dlgID, _ := data.NewDialogID()
	sdb, release, err := data.OpenDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("OpenDialog: %v", err)
	}
	if err := sdb.Table("sessions").Insert("s1", data.Record{
		"session_id": "s1", "title": "对话", "kind": "main", "parent_id": "",
		"created_at": "2026-10-24T10:00:00.000000001Z", "updated_at": "2026-10-24T10:00:00.000000001Z",
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	release()

	svc := New()
	snap := facade.Snapshot{SessionID: "s1", Turn: "t1", Messages: []facade.Message{
		{Role: "user", Content: "第一句"}, {Role: "assistant", Content: "回复"},
	}}
	if _, err := svc.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: instID, DialogID: dlgID, Snapshot: snap,
	}); err != nil {
		t.Fatalf("SnapshotSet(dialog): %v", err)
	}
	got, err := svc.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("SnapshotGet(dialog): %v", err)
	}
	if !got.Found || got.Snapshot.Turn != "t1" || len(got.Snapshot.Messages) != 2 ||
		got.Snapshot.Messages[1].Content != "回复" {
		t.Fatalf("对话库快照往返不符: %+v", got)
	}

	// 不污染旧布局：prjusr.sessions 无 s1；没有 sessions/<hex>/ 目录。
	cat, err := data.PrjUsr(instID)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	var rec data.Record
	if ok, _ := cat.Table("sessions").Get("s1", &rec); ok {
		t.Fatalf("对话模式下不应写 prjusr.sessions: %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(prjUsr), "sessions")); !os.IsNotExist(err) {
		t.Fatalf("不应创建旧布局目录: %v", err)
	}
}

// TestSnapshotDialogErrors 覆盖：未知会话 / 库缺失 → 报错（不回退旧布局）。
func TestSnapshotDialogErrors(t *testing.T) {
	instID, prjUsr := setupInst(t)
	dlgID, _ := data.NewDialogID()
	sdb, release, err := data.OpenDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("OpenDialog: %v", err)
	}
	release()
	svc := New()
	if _, err := svc.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: instID, DialogID: dlgID,
		Snapshot: facade.Snapshot{SessionID: "ghost", Messages: []facade.Message{{Role: "user", Content: "x"}}},
	}); err == nil {
		t.Fatal("未知会话应报错（不得补建第 2 个主 session）")
	}
	missing, _ := data.NewDialogID()
	if _, err := svc.SnapshotGet(facade.SnapshotGetRequest{
		InstanceID: instID, DialogID: missing, SessionID: "s1",
	}); err == nil {
		t.Fatal("对话库缺失应报错（不得静默新建空库）")
	}
	_ = sdb
}

// TestSnapshotLegacyStillWorks 兼容：不带 dialog_id → 旧布局照旧（含目录行补建）。
func TestSnapshotLegacyStillWorks(t *testing.T) {
	instID, prjUsr := setupInst(t)
	svc := New()
	if _, err := svc.SnapshotSet(facade.SnapshotSetRequest{
		InstanceID: instID,
		Snapshot:   facade.Snapshot{SessionID: "old-1", Turn: "t9", Messages: []facade.Message{{Role: "user", Content: "旧"}}},
	}); err != nil {
		t.Fatalf("SnapshotSet(legacy): %v", err)
	}
	got, err := svc.SnapshotGet(facade.SnapshotGetRequest{InstanceID: instID, SessionID: "old-1"})
	if err != nil || !got.Found || got.Snapshot.Turn != "t9" {
		t.Fatalf("旧布局快照往返不符: %+v err=%v", got, err)
	}
	cat, _ := data.PrjUsr(instID)
	var rec data.Record
	if ok, _ := cat.Table("sessions").Get("old-1", &rec); !ok {
		t.Fatal("旧布局应补建 prjusr 会话目录行")
	}
	if _, err := os.Stat(data.SessionDBPath(prjUsr, "old-1")); err != nil {
		t.Fatalf("旧布局会话库缺失: %v", err)
	}
}
