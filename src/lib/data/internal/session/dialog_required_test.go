// dialog_required_test.go —— P3-4d：一树一库生效时缺 `dialog_id` 的会话类调用**明确失败**
// （61 §0.6：`dialog_required`，不做全局反查、不静默落回 prjusr）。
package session

import (
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestDialogRequiredWhenShardOn 覆盖内容域（ContentDB）与会话 CRUD 四条必填入口。
func TestDialogRequiredWhenShardOn(t *testing.T) {
	svc, instID, _, _ := setup(t) // shard 缺省 on（P3-4c）

	step := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s：缺 dialog_id 应明确失败", name)
		}
		if !strings.Contains(err.Error(), "dialog_required") {
			t.Fatalf("%s：错误应带 dialog_required 码，got=%v", name, err)
		}
	}

	_, err := svc.SessionGet(facade.SessionGetRequest{InstanceID: instID, SessionID: "m1"})
	step("SessionGet", err)
	_, err = svc.SessionTitle(facade.SessionTitleRequest{InstanceID: instID, SessionID: "m1", Title: "x"})
	step("SessionTitle", err)
	_, err = svc.SessionDelete(facade.SessionDeleteRequest{InstanceID: instID, SessionID: "m1"})
	step("SessionDelete", err)
	_, err = svc.SessionEnsure(facade.SessionEnsureRequest{InstanceID: instID, SessionID: "c9", ParentSessionID: "m1"})
	step("SessionEnsure(子会话)", err)
	_, err = svc.TurnEnsure(facade.TurnEnsureRequest{InstanceID: instID, TurnID: "t9", SessionID: "m1"})
	step("TurnEnsure", err)
	_, err = svc.TurnHistory(facade.TurnHistoryRequest{InstanceID: instID, SessionID: "m1"})
	step("TurnHistory", err)
	_, err = svc.SessionNextRunCheckAndAdd(facade.SessionNextRunCheckAndAddRequest{
		InstanceID: instID, SessionID: "m1", Max: 3,
	})
	step("SessionNextRunCheckAndAdd", err)
	_, err = svc.SessionNextRunGet(facade.SessionNextRunGetRequest{InstanceID: instID, SessionID: "m1"})
	step("SessionNextRunGet", err)
	_, err = svc.SessionNextRunReset(facade.SessionNextRunResetRequest{InstanceID: instID, SessionID: "m1"})
	step("SessionNextRunReset", err)
	_, err = svc.SessionNextRunDec(facade.SessionNextRunDecRequest{InstanceID: instID, SessionID: "m1"})
	step("SessionNextRunDec", err)

	// 根会话自举仍是唯一允许缺 dialog_id 的写入口（不受 P3-4d 影响）。
	got, err := svc.SessionEnsure(facade.SessionEnsureRequest{InstanceID: instID, SessionID: "root-new"})
	if err != nil || got.DialogID == "" {
		t.Fatalf("根会话自举应成功并回带 dialog_id：%+v err=%v", got, err)
	}
}

// TestLegacyLayoutStillWorksWhenShardOff：显式 off → 旧布局行为不变（过渡期回退路径）。
func TestLegacyLayoutStillWorksWhenShardOff(t *testing.T) {
	svc, instID, _, _ := setup(t)
	disableShard(t, svc, instID, facade.Scope{})
	prj, err := data.PrjUsr(instID)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	if err := prj.Table("sessions").Insert("legacy1", data.Record{
		"session_id": "legacy1", "title": "旧", "parent_id": "", "created_at": ts0, "updated_at": ts0,
	}); err != nil {
		t.Fatalf("seed 旧布局: %v", err)
	}
	got, err := svc.SessionGet(facade.SessionGetRequest{InstanceID: instID, SessionID: "legacy1"})
	if err != nil || !got.Found {
		t.Fatalf("off：旧布局 SessionGet 应成功，found=%v err=%v", got.Found, err)
	}
}
