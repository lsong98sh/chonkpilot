package session

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestInboxRoutingInDialog：inbox 四动作（append/list/update/commit）走对话库，不落 prjusr。
func TestInboxRoutingInDialog(t *testing.T) {
	svc, instID, prjUsr, dlgID := setup(t)

	app, err := svc.InboxAppend(facade.InboxAppendRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1",
		Type: "insert", Content: "插话", IdempotencyKey: "ik-1",
	})
	if err != nil {
		t.Fatalf("InboxAppend: %v", err)
	}
	if app.ID == "" {
		t.Fatal("应返回 inbox 主键")
	}
	// 幂等：同幂等键重放 → 同一主键、不新增行。
	again, err := svc.InboxAppend(facade.InboxAppendRequest{
		InstanceID: instID, DialogID: dlgID, SessionID: "m1",
		Type: "insert", Content: "插话", IdempotencyKey: "ik-1",
	})
	if err != nil {
		t.Fatalf("InboxAppend 重放: %v", err)
	}
	if again.ID != app.ID {
		t.Fatalf("幂等命中应返回既有主键，got %s want %s", again.ID, app.ID)
	}

	list, err := svc.InboxList(facade.InboxListRequest{InstanceID: instID, DialogID: dlgID, SessionID: "m1"})
	if err != nil {
		t.Fatalf("InboxList: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != app.ID || list.Items[0].Status != "pending" {
		t.Fatalf("inbox 列表不符: %+v", list.Items)
	}

	// commit：同事务落 messages + 置 consumed（消息写对话库）。
	if _, err := svc.InboxCommit(facade.InboxCommitRequest{
		InstanceID: instID, DialogID: dlgID, InboxID: app.ID, TurnID: "m1t1",
		Message: facade.Message{Role: "user", Content: "插话"},
	}); err != nil {
		t.Fatalf("InboxCommit: %v", err)
	}
	if _, err := svc.InboxUpdate(facade.InboxUpdateRequest{
		InstanceID: instID, DialogID: dlgID, ID: app.ID, Status: "discarded",
	}); err != nil {
		t.Fatalf("InboxUpdate: %v", err)
	}

	sdb, release, err := data.OpenExistingDialog(prjUsr, dlgID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	defer release()
	var rec data.Record
	if ok, _ := sdb.Table("inbox_items").Get(app.ID, &rec); !ok {
		t.Fatal("对话库缺 inbox 行")
	}
	if rec["status"] != "discarded" {
		t.Fatalf("inbox 状态未更新: %+v", rec)
	}
	if recs, _, err := sdb.Table("messages").Query(data.Query{}); err != nil || len(recs) == 0 {
		t.Fatalf("对话库应落 commit 的消息: len=%d err=%v", len(recs), err)
	}
	// 不污染 prjusr。
	prj, _ := data.PrjUsr(instID)
	if ok, _ := prj.Table("inbox_items").Get(app.ID, &rec); ok {
		t.Fatal("对话模式不应写 prjusr.inbox_items")
	}
}

// TestInboxMissingDialogErrors：带未知 dialog_id → 明确报错（不回退 prjusr）。
func TestInboxMissingDialogErrors(t *testing.T) {
	svc, instID, _, _ := setup(t)
	if _, err := svc.InboxAppend(facade.InboxAppendRequest{
		InstanceID: instID, DialogID: "no-such-dialog", SessionID: "m1",
		Type: "insert", Content: "x",
	}); err == nil {
		t.Fatal("append 未知对话库应报错")
	}
	if _, err := svc.InboxList(facade.InboxListRequest{
		InstanceID: instID, DialogID: "no-such-dialog", SessionID: "m1",
	}); err == nil {
		t.Fatal("list 未知对话库应报错")
	}
	if _, err := svc.InboxUpdate(facade.InboxUpdateRequest{
		InstanceID: instID, DialogID: "no-such-dialog", ID: "i1", Status: "consumed",
	}); err == nil {
		t.Fatal("update 未知对话库应报错")
	}
	if _, err := svc.InboxCommit(facade.InboxCommitRequest{
		InstanceID: instID, DialogID: "no-such-dialog", InboxID: "i1", TurnID: "t1",
		Message: facade.Message{Role: "user", Content: "x"},
	}); err == nil {
		t.Fatal("commit 未知对话库应报错")
	}
}
