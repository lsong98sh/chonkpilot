// adapter_test.go —— `Store.DialogPurger.PurgeDialog` 的 L1（P4-2 批次二，2026-10-28）：
// 删目录行 + 删库文件 + 幂等；以及组件 `DeleteSession(主)` 经本适配器**不再**返回
// `E_UNSUPPORTED`（活动位不清 = 宿主调用点补偿，见 PurgeDialog 注释）。
package storeadapter

import (
	"os"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
	"github.com/lsong98sh/chonkpilot-agent/session/store"
)

// newTestStore 建隔离的 prjusr 库路径 + 适配器（`data.Reset` 清共享句柄缓存）。
func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	prjPath := t.TempDir() + "/chonkpilot.db"
	data.Reset()
	t.Cleanup(data.Reset)
	return New(prjPath), prjPath
}

// countDialogRows 统计 prjusr `dialogs` 表行数（目录行是否存在）。
func countDialogRows(t *testing.T, st *Store, dialogID string) bool {
	t.Helper()
	cat, release, err := st.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	defer release()
	var rec store.Record
	ok, err := cat.Table("dialogs").Get(dialogID, &rec)
	if err != nil {
		t.Fatalf("读 dialogs 行: %v", err)
	}
	return ok
}

// TestPurgeDialog 用例：删目录行 + 删库文件；目录行 / 文件已缺失时幂等成功。
func TestPurgeDialog(t *testing.T) {
	st, prjPath := newTestStore(t)
	dialogID, err := data.NewDialogID()
	if err != nil {
		t.Fatalf("NewDialogID: %v", err)
	}
	sdb, release, err := data.OpenDialog(prjPath, dialogID)
	if err != nil {
		t.Fatalf("OpenDialog: %v", err)
	}
	if err := sdb.Table("sessions").Insert("m1", data.Record{"session_id": "m1", "parent_id": ""}); err != nil {
		t.Fatalf("seed sessions: %v", err)
	}
	release() // 删文件前必须先释放句柄

	cat, relCat, err := st.Catalog()
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if err := cat.Table("dialogs").Upsert(dialogID, store.Record{
		"dialog_id": dialogID, "file": data.DialogFileName(dialogID), "title": "整对话", "kind": "main",
	}); err != nil {
		t.Fatalf("seed dialogs: %v", err)
	}
	relCat()

	if err := st.PurgeDialog(dialogID); err != nil {
		t.Fatalf("PurgeDialog: %v", err)
	}
	if countDialogRows(t, st, dialogID) {
		t.Fatal("目录行应已删")
	}
	if _, err := os.Stat(data.DialogDBPath(prjPath, dialogID)); !os.IsNotExist(err) {
		t.Fatalf("对话库文件应已删: %v", err)
	}

	// 幂等：行 / 文件都已缺失 → 再删仍成功。
	if err := st.PurgeDialog(dialogID); err != nil {
		t.Fatalf("重复 PurgeDialog 应幂等成功: %v", err)
	}

	// 入参校验：空 dialog_id 明确报错（不删任何东西）。
	if err := st.PurgeDialog(""); err == nil {
		t.Fatal("空 dialog_id 应报错")
	}
}

// TestComponentDeleteSessionPurgesDialog 用例：组件 `DeleteSession(主 session)` 经本适配器
// 删整对话（目录行 + 库文件），**不再**返回 `E_UNSUPPORTED`；活动位**不由适配器清**（宿主补偿）。
func TestComponentDeleteSessionPurgesDialog(t *testing.T) {
	st, prjPath := newTestStore(t)
	c, err := component.New(component.Options{Store: st})
	if err != nil {
		t.Fatalf("component.New: %v", err)
	}
	d, err := c.CreateDialog(component.CreateDialogCmd{Title: "删整对话"})
	if err != nil {
		t.Fatalf("CreateDialog: %v", err)
	}

	// 活动位指向该主会话（宿主库 prj 配置）；适配器不清它 → 删除后仍保留，由宿主调用点补偿。
	// 走共享层并**立即释放**（refs 归零即关句柄，避免与适配器的短开互斥）。
	prj, releasePrj, err := data.OpenSharedLayer(prjPath, data.LayerPrjUsr)
	if err != nil {
		t.Fatalf("Open prjusr: %v", err)
	}
	if err := kernel.PrjConfigSetVal(prj, "active_session_id", d.SessionID); err != nil {
		t.Fatalf("写活动位: %v", err)
	}
	releasePrj()

	if err := c.DeleteSession(component.DeleteSessionCmd{DialogID: d.DialogID, SessionID: d.SessionID}); err != nil {
		if component.CodeOf(err) == component.CodeUnsupported {
			t.Fatalf("适配器已实现 DialogPurger，不应再 E_UNSUPPORTED: %v", err)
		}
		t.Fatalf("DeleteSession(主): %v", err)
	}
	if countDialogRows(t, st, d.DialogID) {
		t.Fatal("主 session 删除应删目录行")
	}
	if _, err := os.Stat(data.DialogDBPath(prjPath, d.DialogID)); !os.IsNotExist(err) {
		t.Fatalf("主 session 删除应删库文件: %v", err)
	}

	prj2, releasePrj2, err := data.OpenSharedLayer(prjPath, data.LayerPrjUsr)
	if err != nil {
		t.Fatalf("Open prjusr（复核）: %v", err)
	}
	defer releasePrj2()
	if v, err := kernel.PrjConfigVal(prj2, "active_session_id"); err != nil || v != d.SessionID {
		t.Fatalf("活动位应保持原值（清理由宿主补偿）: v=%q err=%v", v, err)
	}

	// 幂等：对话已不存在 → 再删仍成功。
	if err := c.DeleteSession(component.DeleteSessionCmd{DialogID: d.DialogID, SessionID: d.SessionID}); err != nil {
		t.Fatalf("重复删主 session 应幂等成功: %v", err)
	}
}
