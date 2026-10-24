package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

const ts0 = "2026-10-24T10:00:00.000000001Z"

// setup 建隔离实例 + 一个对话（主 session m1 + 子会话 c1），返回服务、实例 id、prjusr 路径、dialog id。
func setup(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	work, dataDir := filepath.Join(dir, "ws"), filepath.Join(dir, "dd")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	instID := "inst-" + filepath.Base(dir)
	data.Register(instID, work, dataDir)
	t.Cleanup(func() { data.Unregister(instID) })
	prj, err := data.PrjUsr(instID)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	dlgID, _ := data.NewDialogID()
	sdb, release, err := data.OpenDialog(prj.Path(), dlgID)
	if err != nil {
		t.Fatalf("OpenDialog: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	must(sdb.Table("sessions").Insert("m1", data.Record{
		"session_id": "m1", "title": "主", "kind": "main", "parent_id": "",
		"created_at": ts0, "updated_at": ts0,
	}))
	must(sdb.Table("sessions").Insert("c1", data.Record{
		"session_id": "c1", "title": "子", "kind": "sub_chat", "parent_id": "m1",
		"created_at": ts0, "updated_at": ts0,
	}))
	must(sdb.Table("turns").Insert("t1", data.Record{
		"turn_id": "t1", "session_id": "c1", "status": "completed", "created_at": ts0,
	}))
	must(sdb.Table("messages").Insert("msg1", data.Record{
		"session_id": "c1", "turn_id": "t1", "role": "user", "content": "x", "created_at": ts0,
	}))
	must(sdb.Table("snapshot").Insert("c1", data.Record{
		"session_id": "c1", "history": `[{"role":"user","content":"x"}]`, "snapshot_turn": "t1", "updated_at": ts0,
	}))
	release()
	must(prj.Table("dialogs").Upsert(dlgID, data.Record{
		"dialog_id": dlgID, "file": data.DialogFileName(dlgID), "title": "主", "kind": "main", "updated_at": ts0,
	}))
	return New(kernel.NewBase(nil, kernel.Options{})), instID, prj.Path(), dlgID
}

// TestEnsureGetInDialog：对话内建会话 / 幂等 / 详情读取 / 不写 prjusr。
func TestEnsureGetInDialog(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t)
	if _, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c2", ParentSessionID: "m1", Kind: "sub_chat",
	}); err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	if _, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c2", ParentSessionID: "m1",
	}); err != nil {
		t.Fatalf("SessionEnsure 幂等: %v", err)
	}
	got, err := svc.SessionGet(facade.SessionGetRequest{InstanceID: instID, DialogID: dlgID, SessionID: "c2"})
	if err != nil || !got.Found || got.Session.ID != "c2" {
		t.Fatalf("SessionGet: %+v err=%v", got, err)
	}
	if nf, _ := svc.SessionGet(facade.SessionGetRequest{InstanceID: instID, DialogID: dlgID, SessionID: "ghost"}); nf.Found {
		t.Fatal("未知会话应 Found=false")
	}
	prj, _ := data.PrjUsr(instID)
	var rec data.Record
	if ok, _ := prj.Table("sessions").Get("c2", &rec); ok {
		t.Fatalf("对话模式不应写 prjusr.sessions: %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(prjUsr), "sessions")); !os.IsNotExist(err) {
		t.Fatalf("不应创建旧布局目录: %v", err)
	}
}

// TestTitleInDialog：主 session 改名同步 dialogs；子会话不同步；未知会话报错。
func TestTitleInDialog(t *testing.T) {
	svc, instID, _, dlgID := setup(t)
	if _, err := svc.SessionTitle(facade.SessionTitleRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1", Title: "新主标题",
	}); err != nil {
		t.Fatalf("SessionTitle(main): %v", err)
	}
	prj, _ := data.PrjUsr(instID)
	var row data.Record
	if ok, _ := prj.Table("dialogs").Get(dlgID, &row); !ok || kernel.Sval(row["title"]) != "新主标题" {
		t.Fatalf("dialogs.title 未同步: %+v", row)
	}
	if _, err := svc.SessionTitle(facade.SessionTitleRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c1", Title: "新子标题",
	}); err != nil {
		t.Fatalf("SessionTitle(child): %v", err)
	}
	if ok, _ := prj.Table("dialogs").Get(dlgID, &row); !ok || kernel.Sval(row["title"]) != "新主标题" {
		t.Fatalf("子会话改名不应动 dialogs.title: %+v", row)
	}
	if _, err := svc.SessionTitle(facade.SessionTitleRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "ghost", Title: "x",
	}); err == nil {
		t.Fatal("未知会话应报错")
	}
}

// TestDeleteInDialog：子会话删除只级联自身；主 session 删除 = 整对话删除；活动态清理。
func TestDeleteInDialog(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t)
	if _, err := svc.SessionActiveSet(facade.SessionActiveSetRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c1",
	}); err != nil {
		t.Fatalf("ActiveSet: %v", err)
	}
	if _, err := svc.SessionDelete(facade.SessionDeleteRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c1",
	}); err != nil {
		t.Fatalf("Delete(child): %v", err)
	}
	sdb, release, err := data.OpenExistingDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	var rec data.Record
	if ok, _ := sdb.Table("sessions").Get("c1", &rec); ok {
		t.Fatal("子会话行应删除")
	}
	if ok, _ := sdb.Table("messages").Get("msg1", &rec); ok {
		t.Fatal("子会话消息应级联删除")
	}
	if ok, _ := sdb.Table("turns").Get("t1", &rec); ok {
		t.Fatal("子会话轮次应级联删除")
	}
	if ok, _ := sdb.Table("snapshot").Get("c1", &rec); ok {
		t.Fatal("子会话快照应级联删除")
	}
	if ok, _ := sdb.Table("sessions").Get("m1", &rec); !ok {
		t.Fatal("主 session 不应受影响")
	}
	release()
	prj, _ := data.PrjUsr(instID)
	if v, _ := kernel.PrjConfigVal(prj, "active_session_id"); v != "" {
		t.Fatalf("活动态应清空: %q", v)
	}

	// 删主 session = 删整个对话（行 + 文件）
	if _, err := svc.SessionActiveSet(facade.SessionActiveSetRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1",
	}); err != nil {
		t.Fatalf("ActiveSet: %v", err)
	}
	if _, err := svc.SessionDelete(facade.SessionDeleteRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1",
	}); err != nil {
		t.Fatalf("Delete(main): %v", err)
	}
	if _, err := os.Stat(data.DialogDBPath(prjUsr, dlgID)); !os.IsNotExist(err) {
		t.Fatalf("对话库应删除: %v", err)
	}
	if ok, _ := prj.Table("dialogs").Get(dlgID, &rec); ok {
		t.Fatal("dialogs 行应删除")
	}
	if v, _ := kernel.PrjConfigVal(prj, "active_session_id"); v != "" {
		t.Fatalf("活动态应清空: %q", v)
	}
}

// TestActiveGetFallsBackToDialog：旧布局空 → 回落最近对话主 session。
func TestActiveGetFallsBackToDialog(t *testing.T) {
	svc, instID, _, _ := setup(t)
	got, err := svc.SessionActiveGet(facade.SessionActiveGetRequest{InstanceID: instID})
	if err != nil {
		t.Fatalf("SessionActiveGet: %v", err)
	}
	if got.SessionID != "m1" {
		t.Fatalf("应回落对话主 session m1，实际 %q", got.SessionID)
	}
}

// TestMissingDialogErrors：库缺失 → 全部报错（不静默建库）。
func TestMissingDialogErrors(t *testing.T) {
	svc, instID, _, _ := setup(t)
	missing, _ := data.NewDialogID()
	if _, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, DialogID: missing, SessionID: "x",
	}); err == nil {
		t.Fatal("Ensure 到缺失库应报错")
	}
	if _, err := svc.SessionGet(facade.SessionGetRequest{
		InstanceID: instID, DialogID: missing, SessionID: "x",
	}); err == nil {
		t.Fatal("Get 到缺失库应报错")
	}
}
