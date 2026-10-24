package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestTurnRoutingInDialog：turn 写/读走对话库，不落 prjusr。
func TestTurnRoutingInDialog(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t)
	if _, err := svc.TurnEnsure(facade.TurnEnsureRequest{
		InstanceID: instID, DialogID: dlgID, TurnID: "t2", SessionID: "m1",
	}); err != nil {
		t.Fatalf("TurnEnsure: %v", err)
	}
	if _, err := svc.TurnSetSummary(facade.TurnSetSummaryRequest{
		InstanceID: instID, DialogID: dlgID, TurnID: "t2", Summary: "摘要",
	}); err != nil {
		t.Fatalf("TurnSetSummary: %v", err)
	}
	if _, err := svc.TurnComplete(facade.TurnCompleteRequest{
		InstanceID: instID, DialogID: dlgID, TurnID: "t2", Status: "done", FinishReason: "stop",
	}); err != nil {
		t.Fatalf("TurnComplete: %v", err)
	}
	sdb, release, err := data.OpenExistingDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	defer release()
	var rec data.Record
	if ok, _ := sdb.Table("turns").Get("t2", &rec); !ok {
		t.Fatal("对话库缺 turn 行")
	}
	if rec["status"] != "done" || rec["summary"] != "摘要" || rec["finish_reason"] != "stop" {
		t.Fatalf("turn 字段不符: %+v", rec)
	}
	prj, _ := data.PrjUsr(instID)
	if ok, _ := prj.Table("turns").Get("t2", &rec); ok {
		t.Fatal("对话模式不应写 prjusr.turns")
	}
}

// TestMessageAppendLoadInDialog：消息写/读走对话库。
func TestMessageAppendLoadInDialog(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t)
	if _, err := svc.TurnEnsure(facade.TurnEnsureRequest{
		InstanceID: instID, DialogID: dlgID, TurnID: "t3", SessionID: "m1",
	}); err != nil {
		t.Fatalf("TurnEnsure: %v", err)
	}
	res, err := svc.MessageAppend(facade.MessageAppendRequest{
		InstanceID: instID, DialogID: dlgID, TurnID: "t3", SessionID: "m1",
		Message: facade.Message{Role: "user", Content: "你好"},
	})
	if err != nil {
		t.Fatalf("MessageAppend: %v", err)
	}
	if res.ID == "" {
		t.Fatal("应返回消息主键")
	}
	loaded, err := svc.MessageLoad(facade.MessageLoadRequest{
		InstanceID: instID, DialogID: dlgID, TurnID: "t3",
	})
	if err != nil {
		t.Fatalf("MessageLoad: %v", err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "你好" || loaded.Messages[0].Role != "user" {
		t.Fatalf("消息往返不符: %+v", loaded.Messages)
	}
	sdb, release, err := data.OpenExistingDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	defer release()
	var rec data.Record
	if ok, _ := sdb.Table("messages").Get(res.ID, &rec); !ok || rec["session_id"] != "m1" {
		t.Fatalf("对话库消息行不符: ok=%v %+v", ok, rec)
	}
	prj, _ := data.PrjUsr(instID)
	if ok, _ := prj.Table("messages").Get(res.ID, &rec); ok {
		t.Fatal("对话模式不应写 prjusr.messages")
	}
}

// TestMessageContextInDialog：上下文组装读对话库（快照同库，不触发旧布局迁移）。
func TestMessageContextInDialog(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t)
	// 直接给 m1 写一份快照（对话库快照表）。
	sdb, release, err := data.OpenExistingDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	if err := sdb.Table("snapshot").Insert("m1", data.Record{
		"session_id": "m1", "snapshot_turn": "",
		"history":    `[{"role":"system","content":"记忆"},{"role":"user","content":"上一句"}]`,
		"updated_at": ts0,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	release()

	got, err := svc.MessageContext(facade.MessageContextRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1", IncludeSnapshot: true,
	})
	if err != nil {
		t.Fatalf("MessageContext: %v", err)
	}
	if len(got.Messages) != 2 || got.Messages[1].Content != "上一句" {
		t.Fatalf("快照上下文不符: %+v", got.Messages)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(prjUsr), "sessions")); !os.IsNotExist(err) {
		t.Fatalf("对话模式不应建旧布局目录: %v", err)
	}
}

// TestTurnHistoryInDialog：历史分页读对话库（turns + messages）。
func TestTurnHistoryInDialog(t *testing.T) {
	svc, instID, _, dlgID := setup(t)
	got, err := svc.TurnHistory(facade.TurnHistoryRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "c1",
	})
	if err != nil {
		t.Fatalf("TurnHistory: %v", err)
	}
	if len(got.Turns) != 1 || got.Turns[0].ID != "t1" {
		t.Fatalf("轮次不符: %+v", got.Turns)
	}
	if len(got.Messages) != 1 || got.Messages[0].MessageID != "msg1" {
		t.Fatalf("消息视图不符: %+v", got.Messages)
	}
}

// TestContentMissingDialogErrors：库缺失 → 读写全部报错（不静默建库 / 不回退 prjusr）。
func TestContentMissingDialogErrors(t *testing.T) {
	svc, instID, _, _ := setup(t)
	missing, _ := data.NewDialogID()
	if _, err := svc.TurnEnsure(facade.TurnEnsureRequest{
		InstanceID: instID, DialogID: missing, TurnID: "x", SessionID: "m1",
	}); err == nil {
		t.Fatal("TurnEnsure 到缺失库应报错")
	}
	if _, err := svc.MessageLoad(facade.MessageLoadRequest{
		InstanceID: instID, DialogID: missing, TurnID: "x",
	}); err == nil {
		t.Fatal("MessageLoad 到缺失库应报错")
	}
	if _, err := svc.MessageAppend(facade.MessageAppendRequest{
		InstanceID: instID, DialogID: missing, TurnID: "x",
		Message: facade.Message{Role: "user", Content: "y"},
	}); err == nil {
		t.Fatal("MessageAppend 到缺失库应报错")
	}
}
