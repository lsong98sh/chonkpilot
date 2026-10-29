package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
)

// setupBootstrapEnv 建隔离实例（无预置对话）。
func setupBootstrapEnv(t *testing.T) (*Service, string, facade.Scope) {
	t.Helper()
	dir := t.TempDir()
	work, dataDir := filepath.Join(dir, "ws"), filepath.Join(dir, "dd")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	instID := "inst-bootstrap-" + filepath.Base(dir)
	scope := facade.Scope{WorkDir: work, DataDir: dataDir}
	data.Register(instID, work, dataDir)
	t.Cleanup(func() { data.Unregister(instID) })
	return New(kernel.NewBase(nil, kernel.Options{})), instID, scope
}

// TestEnsureBootstrapCreatesDialog：根会话缺 dialog_id → 确定性建对话并回带 id；
// 幂等；后续调用带该 id 落在对话库。
func TestEnsureBootstrapCreatesDialog(t *testing.T) {
	svc, instID, scope := setupBootstrapEnv(t)
	resp, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "root-1",
	})
	if err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	if resp.DialogID == "" {
		t.Fatal("应回带 dialog_id")
	}
	// 确定性 id：同一根会话恒同一 id。
	if resp.DialogID != migratedDialogID("root-1") {
		t.Fatalf("dialog_id 非确定性: %s", resp.DialogID)
	}
	// 幂等：二次 Ensure 回同一 id。
	again, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "root-1",
	})
	if err != nil || again.DialogID != resp.DialogID {
		t.Fatalf("二次 Ensure 应幂等: %+v err=%v", again, err)
	}
	// 对话库内应有主会话行 + 目录行带 main_session_id。
	prjUsr, err := data.PrjUsr(instID)
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	sdb, release, err := data.OpenExistingDialog(prjUsr.Path(), resp.DialogID)
	if err != nil {
		t.Fatalf("open dialog: %v", err)
	}
	var rec data.Record
	if ok, _ := sdb.Table("sessions").Get("root-1", &rec); !ok {
		release()
		t.Fatal("对话库缺主会话行")
	}
	release()
	var drow data.Record
	if ok, _ := prjUsr.Table("dialogs").Get(resp.DialogID, &drow); !ok {
		t.Fatal("缺 dialogs 目录行")
	}
	if drow["main_session_id"] != "root-1" {
		t.Fatalf("目录行 main_session_id 不符: %+v", drow)
	}
	// prjusr 不写会话正文（一树一库）。
	var old data.Record
	if ok, _ := prjUsr.Table("sessions").Get("root-1", &old); ok {
		t.Fatal("自举模式不应写 prjusr.sessions")
	}
}

// TestSessionListAndLatestDeriveFromDialogs：列表/最新由 dialogs 目录派生（带 dialog_id）。
func TestSessionListAndLatestDeriveFromDialogs(t *testing.T) {
	svc, instID, scope := setupBootstrapEnv(t)
	a, err := svc.SessionEnsure(facade.SessionEnsureRequest{InstanceID: instID, Scope: scope, SessionID: "s-a"})
	if err != nil {
		t.Fatalf("ensure a: %v", err)
	}
	b, err := svc.SessionEnsure(facade.SessionEnsureRequest{InstanceID: instID, Scope: scope, SessionID: "s-b"})
	if err != nil {
		t.Fatalf("ensure b: %v", err)
	}
	list, err := svc.SessionList(facade.SessionListRequest{InstanceID: instID, Scope: scope})
	if err != nil {
		t.Fatalf("SessionList: %v", err)
	}
	got := map[string]string{}
	for _, s := range list.List {
		got[s.ID] = s.DialogID
	}
	if got["s-a"] != a.DialogID || got["s-b"] != b.DialogID {
		t.Fatalf("列表未带 dialog_id: %+v", list.List)
	}
	latest, err := svc.SessionLatest(facade.SessionLatestRequest{InstanceID: instID, Scope: scope})
	if err != nil {
		t.Fatalf("SessionLatest: %v", err)
	}
	if latest.SessionID == "" || latest.DialogID == "" {
		t.Fatalf("最新会话应带 session_id + dialog_id: %+v", latest)
	}
}

// migratedDialogID 复算自举算法（测试断言用；算法本体在 sessionstore）。
func migratedDialogID(sessionID string) string {
	return sessionstore.MigratedDialogID(sessionID)
}
