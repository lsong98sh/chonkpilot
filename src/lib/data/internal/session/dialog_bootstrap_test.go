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

// setupBootstrapEnv 建隔离实例（无预置对话），返回服务 / 实例 id / Scope / usr 库路径。
// usr 库注入临时路径（避免测试读写真实用户配置；G-99 开关即落该库）。
func setupBootstrapEnv(t *testing.T) (*Service, string, facade.Scope, string) {
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
	usrPath := filepath.Join(t.TempDir(), "usr.db")
	return New(kernel.NewBase(nil, kernel.Options{UsrPath: usrPath})), instID, scope, usrPath
}

// TestEnsureBootstrapCreatesDialog：根会话缺 dialog_id → 确定性建对话并回带 id；
// 幂等；后续调用带该 id 落在对话库。
func TestEnsureBootstrapCreatesDialog(t *testing.T) {
	svc, instID, scope, _ := setupBootstrapEnv(t)
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
	svc, instID, scope, _ := setupBootstrapEnv(t)
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

// setUsrConfig 直写 usr 库 config 表（落库形态 `{v: 字符串}`，与插件配置页写入一致）。
func setUsrConfig(t *testing.T, usrPath, key, value string) {
	t.Helper()
	db, release, err := data.OpenSharedLayer(usrPath, data.LayerUsr)
	if err != nil {
		t.Fatalf("打开 usr 库: %v", err)
	}
	defer release()
	if err := data.SetConfig(db, key, value); err != nil {
		t.Fatalf("写 usr 配置 %s: %v", key, err)
	}
}

// listIDs 取当前用户会话列表的 session_id 集合（保持顺序）。
func listIDs(t *testing.T, svc *Service, instID string, scope facade.Scope) []string {
	t.Helper()
	list, err := svc.SessionList(facade.SessionListRequest{InstanceID: instID, Scope: scope})
	if err != nil {
		t.Fatalf("SessionList: %v", err)
	}
	out := make([]string, 0, len(list.List))
	for _, s := range list.List {
		out = append(out, s.ID)
	}
	return out
}

// assertListIDs 断言列表 id 集合逐项相等。
func assertListIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("会话列表不符：got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("会话列表不符：got=%v want=%v", got, want)
		}
	}
}

// TestSessionListCrontabVisibilitySwitch（G-99）：开关缺省 / false / true / 非法值 四种口径。
//
// 语义：`plugins.crontab.show-in-session-list=true` → crontab 顶层对话进入用户会话列表；
// 缺省 / false / 非 "true" → 维持排除（fail-closed）。「当前活动会话」回落**恒排除**。
func TestSessionListCrontabVisibilitySwitch(t *testing.T) {
	svc, instID, scope, usrPath := setupBootstrapEnv(t)
	if _, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "crontab-deadbeef",
		Kind: sessionstore.DialogKindCrontab, Title: "定时任务",
	}); err != nil {
		t.Fatalf("建 crontab 对话: %v", err)
	}
	if _, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "s-user", Title: "用户会话",
	}); err != nil {
		t.Fatalf("建用户对话: %v", err)
	}

	// ① 键缺失（缺省）→ 只显示用户会话。
	assertListIDs(t, listIDs(t, svc, instID, scope), []string{"s-user"})

	// ② 显式 false → 维持排除。
	setUsrConfig(t, usrPath, crontabShowInSessionListKey, "false")
	assertListIDs(t, listIDs(t, svc, instID, scope), []string{"s-user"})

	// ③ true → crontab 对话一并返回（排在最近活动的用户会话之后）。
	setUsrConfig(t, usrPath, crontabShowInSessionListKey, "true")
	assertListIDs(t, listIDs(t, svc, instID, scope), []string{"s-user", "crontab-deadbeef"})

	// ④ 非布尔值 → fail-closed（按不显示）。
	setUsrConfig(t, usrPath, crontabShowInSessionListKey, "yes-please")
	assertListIDs(t, listIDs(t, svc, instID, scope), []string{"s-user"})

	// ⑤ 开关只作用于「列表」：活动位回落恒排除 crontab。
	setUsrConfig(t, usrPath, crontabShowInSessionListKey, "true")
	latest, err := svc.SessionLatest(facade.SessionLatestRequest{InstanceID: instID, Scope: scope})
	if err != nil {
		t.Fatalf("SessionLatest: %v", err)
	}
	if latest.SessionID != "s-user" {
		t.Fatalf("活动位回落必须排除 crontab：%+v", latest)
	}
}

// TestSessionListCrontabSwitchReadFailureClosed（G-99）：usr 库打不开 → 按 false 处理
// （fail-closed 到「不显示」），列表调用本身不报错。
func TestSessionListCrontabSwitchReadFailureClosed(t *testing.T) {
	work, dataDir := t.TempDir(), t.TempDir()
	instID := "ins-usr-unreadable"
	scope := facade.Scope{WorkDir: work, DataDir: dataDir}
	data.Register(instID, work, dataDir)
	t.Cleanup(func() { data.Unregister(instID) })
	// UsrPath 指向**目录** → bbolt 打开失败 → 开关读取失败。
	svc := New(kernel.NewBase(nil, kernel.Options{UsrPath: t.TempDir()}))

	if _, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "crontab-0a1b2c3d",
		Kind: sessionstore.DialogKindCrontab, Title: "定时任务",
	}); err != nil {
		t.Fatalf("建 crontab 对话: %v", err)
	}
	if _, err := svc.SessionEnsure(facade.SessionEnsureRequest{
		InstanceID: instID, Scope: scope, SessionID: "s-user", Title: "用户会话",
	}); err != nil {
		t.Fatalf("建用户对话: %v", err)
	}
	assertListIDs(t, listIDs(t, svc, instID, scope), []string{"s-user"})
}
