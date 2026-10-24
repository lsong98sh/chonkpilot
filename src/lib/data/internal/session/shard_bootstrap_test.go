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

// setupShard 建隔离实例（无预置对话）；shard=true 时置 prj 开关 `session.db.shard=on`。
func setupShard(t *testing.T, shard bool) (*Service, string, facade.Scope) {
	t.Helper()
	dir := t.TempDir()
	work, dataDir := filepath.Join(dir, "ws"), filepath.Join(dir, "dd")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	instID := "inst-shard-" + filepath.Base(dir)
	scope := facade.Scope{WorkDir: work, DataDir: dataDir}
	data.Register(instID, work, dataDir)
	t.Cleanup(func() { data.Unregister(instID) })
	// P3-4c 起缺省 = on；显式 off 用于验证旧布局回退。
	prj, release, err := data.Prj(instID)
	if err != nil {
		t.Fatalf("Prj: %v", err)
	}
	val := "off"
	if shard {
		val = "on"
	}
	if err := kernel.PrjConfigSetVal(prj, kernel.ConfigSessionShard, val); err != nil {
		t.Fatalf("set shard=%s: %v", val, err)
	}
	release()
	return New(kernel.NewBase(nil, kernel.Options{})), instID, scope
}

// TestEnsureBootstrapCreatesDialog：shard=on 且缺 dialog_id → 确定性建对话并回带 id；
// 幂等；后续调用带该 id 落在对话库。
func TestEnsureBootstrapCreatesDialog(t *testing.T) {
	svc, instID, scope := setupShard(t, true)
	resp, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "root-1",
	})
	if err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	if resp.DialogID == "" {
		t.Fatal("应回带 dialog_id")
	}
	// 确定性 id：与迁移算法同源。
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
	// 旧布局不写。
	var old data.Record
	if ok, _ := prjUsr.Table("sessions").Get("root-1", &old); ok {
		t.Fatal("自举模式不应写 prjusr.sessions")
	}
}

// TestEnsureNoBootstrapWhenShardOff：shard 缺省 off → 旧布局写入、不回带 dialog_id。
func TestEnsureNoBootstrapWhenShardOff(t *testing.T) {
	svc, instID, scope := setupShard(t, false)
	resp, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "root-off",
	})
	if err != nil {
		t.Fatalf("SessionEnsure: %v", err)
	}
	if resp.DialogID != "" {
		t.Fatalf("off 不应回带 dialog_id: %s", resp.DialogID)
	}
	prjUsr, _ := data.PrjUsr(instID)
	var rec data.Record
	if ok, _ := prjUsr.Table("sessions").Get("root-off", &rec); !ok {
		t.Fatal("旧布局应写 prjusr.sessions")
	}
}

// TestSessionListAndLatestDeriveFromDialogs：shard=on 时列表/最新由 dialogs 派生。
func TestSessionListAndLatestDeriveFromDialogs(t *testing.T) {
	svc, instID, scope := setupShard(t, true)
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

// migratedDialogID 复算迁移算法（测试断言用；算法本体在 sessionstore）。
func migratedDialogID(sessionID string) string {
	return sessionstore.MigratedDialogID(sessionID)
}
