// cron_session_test.go —— 定时任务对话 / 会话归属（SANDBOX-WRITE-1 P4 / 方案 §5.5.3–§5.5.4）。
//
// 覆盖：
//   - C11：`kind = crontab` 的顶层对话 + 根会话 / 子会话（parent = 根会话），`origin = scheduler`；
//   - C12：顶层会话列表 / 最近活动**排除** `kind = crontab`；
//   - 启动对账：`dialogs/` 目录里的 crontab 库被补行时**保留 kind**，不因此进入可见列表。
//
// 外部模块黑盒（package persist_test）：全部经总线 data-* 主题驱动与断言。
package persist_test

import (
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// dialogRow 读 prjusr `dialogs` 目录行（不存在 → ok=false）。
func dialogRow(t *testing.T, inst, dialogID string) (data.Record, bool) {
	t.Helper()
	pudb, err := data.PrjUsr(inst)
	if err != nil {
		t.Fatalf("PrjUsr(%s): %v", inst, err)
	}
	var rec data.Record
	ok, err := pudb.Table("dialogs").Get(dialogID, &rec)
	if err != nil {
		t.Fatalf("读 dialogs 行: %v", err)
	}
	return rec, ok
}

// countSessionsInDialog 数对话库内会话行数（重复建立幂等断言用）。
func countSessionsInDialog(t *testing.T, inst, dialogID string) int {
	t.Helper()
	pudb, err := data.PrjUsr(inst)
	if err != nil {
		t.Fatalf("PrjUsr(%s): %v", inst, err)
	}
	db, release, err := data.OpenExistingDialog(pudb.Path(), dialogID)
	if err != nil {
		t.Fatalf("OpenExistingDialog(%s): %v", dialogID, err)
	}
	defer release()
	n := 0
	if err := db.Table("sessions").ForEach(func(string, data.Record) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("列举 sessions: %v", err)
	}
	return n
}

// countDialogsOfKind 数 prjusr `dialogs` 目录中指定 kind 的行数。
func countDialogsOfKind(t *testing.T, inst, kind string) int {
	t.Helper()
	pudb, err := data.PrjUsr(inst)
	if err != nil {
		t.Fatalf("PrjUsr(%s): %v", inst, err)
	}
	n := 0
	if err := pudb.Table("dialogs").ForEach(func(_ string, rec data.Record) error {
		if s, _ := rec["kind"].(string); s == kind {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("列举 dialogs: %v", err)
	}
	return n
}

// listSessionIDs 取 `data-session-list` 的返回项（session_id 集合）。
func listSessionIDs(t *testing.T, bus mq.Bus) []string {
	t.Helper()
	r := dataCall(t, bus, "data-session-list", map[string]any{"req_id": "ls", "instance_id": "ins-test"})
	list := dataList(dataResult(t, r))
	out := make([]string, 0, len(list))
	for _, e := range list {
		row, _ := e.(map[string]any)
		if row == nil {
			continue
		}
		if sid, _ := row["session_id"].(string); sid != "" {
			out = append(out, sid)
		}
	}
	return out
}

// TestCronDialogKindOriginAndListExclusion：根会话自举建 `kind=crontab` 对话 + 子会话归属，
// 顶层列表 / 最近活动**不含**它们（C11 / C12）。
func TestCronDialogKindOriginAndListExclusion(t *testing.T) {
	bus, _, _ := newTestPersist(t)
	regInstance(t, bus)

	root := "crontab-0a1b2c3d"
	// ① 根会话：缺 dialog_id → 自举建对话（kind=crontab，标题「定时任务」）。
	r := dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "c1", "instance_id": "ins-test",
		"data": map[string]any{
			"session_id": root, "kind": "crontab", "title": "定时任务", "origin": "scheduler",
		},
	})
	res := dataResult(t, r)
	dialogID, _ := res["dialog_id"].(string)
	if !res["ok"].(bool) || dialogID == "" {
		t.Fatalf("根会话自举应回带 dialog_id: %+v", r)
	}
	row, ok := dialogRow(t, "ins-test", dialogID)
	if !ok {
		t.Fatalf("dialogs 目录行缺失: %s", dialogID)
	}
	if row["kind"] != "crontab" || row["title"] != "定时任务" {
		t.Fatalf("目录行 kind/title 不符: %+v", row)
	}

	// ② 子会话：显式带 dialog_id + parent_session_id（每任务 1 个）。
	sub := "crontab-ct_deadbeef"
	r = dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "c2", "instance_id": "ins-test",
		"data": map[string]any{
			"session_id": sub, "dialog_id": dialogID, "parent_session_id": root,
			"kind": "crontab_task", "title": "执行：echo hi", "origin": "scheduler",
		},
	})
	if !dataResult(t, r)["ok"].(bool) {
		t.Fatalf("子会话建立失败: %+v", r)
	}
	// ②b 幂等：重复 ensure 根 / 子会话 → 同一 dialog_id、不新增会话行 / 目录行。
	r = dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "c3", "instance_id": "ins-test",
		"data": map[string]any{
			"session_id": root, "kind": "crontab", "title": "定时任务", "origin": "scheduler",
		},
	})
	if got, _ := dataResult(t, r)["dialog_id"].(string); got != dialogID {
		t.Fatalf("重复 ensure 根会话应回带同一 dialog_id: got=%q want=%q", got, dialogID)
	}
	r = dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "c4", "instance_id": "ins-test",
		"data": map[string]any{
			"session_id": sub, "dialog_id": dialogID, "parent_session_id": root,
			"kind": "crontab_task", "title": "执行：echo hi", "origin": "scheduler",
		},
	})
	if !dataResult(t, r)["ok"].(bool) {
		t.Fatalf("重复 ensure 子会话失败: %+v", r)
	}
	if n := countSessionsInDialog(t, "ins-test", dialogID); n != 2 {
		t.Fatalf("重复 ensure 不应新增会话行（应恒为 2）: %d", n)
	}
	if n := countDialogsOfKind(t, "ins-test", "crontab"); n != 1 {
		t.Fatalf("每项目应有且仅有 1 个 crontab 对话: %d", n)
	}

	// ③ 归属字段落库（kind / origin / parent_id）——经会话详情读回。
	r = dataCall(t, bus, "data-session-get", map[string]any{
		"req_id": "g1", "instance_id": "ins-test", "id": sub, "dialog_id": dialogID,
	})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d == nil || d["kind"] != "crontab_task" || d["origin"] != "scheduler" ||
		d["parent_id"] != root || d["dialog_id"] != dialogID {
		t.Fatalf("子会话归属字段不符: %+v", d)
	}

	// ④ 顶层列表 / 最近活动都不含 crontab 对话（C12）。
	if ids := listSessionIDs(t, bus); len(ids) != 0 {
		t.Fatalf("crontab 对话不应出现在顶层列表: %v", ids)
	}
	r = dataCall(t, bus, "data-session-latest", map[string]any{"req_id": "l1", "instance_id": "ins-test"})
	if sid, _ := dataResult(t, r)["session_id"].(string); sid != "" {
		t.Fatalf("最近活动不应回落到 crontab 会话: %q", sid)
	}

	// ⑤ 普通用户对话仍在列表（排除只针对 crontab）。
	userDialog, db := newDialog(t, "ins-test", "s-user")
	_ = db.Table("sessions").Upsert("s-user", data.Record{
		"session_id": "s-user", "title": "用户会话", "parent_id": "", "kind": "main",
		"created_at": time.Now().UTC().Format(data.RFC3339FixedNano),
	})
	putDialogRow(t, "ins-test", userDialog, "s-user", "用户会话",
		time.Now().UTC().Format(data.RFC3339FixedNano))
	if ids := listSessionIDs(t, bus); len(ids) != 1 || ids[0] != "s-user" {
		t.Fatalf("用户会话应可见、crontab 仍不可见: %v", ids)
	}
}

// TestCronSessionIdempotentAcrossRestart：服务重启（新总线 + 新 persist 实例、同一 usr/workdir）
// 后再次 ensure → 同一 dialog_id、不重复建会话 / 目录行，子会话的 dialog_id 关联不丢。
func TestCronSessionIdempotentAcrossRestart(t *testing.T) {
	data.Reset()
	wd := t.TempDir()
	usrPath := t.TempDir() + "/usr.db"

	start := func() (mq.Bus, *persist.Service) {
		bus, err := mq.New(mq.Options{Prefix: "chonk."})
		if err != nil {
			t.Fatalf("mq.New: %v", err)
		}
		svc := persist.New(bus, persist.Options{UsrPath: usrPath})
		if err := svc.Start(); err != nil {
			t.Fatalf("persist.Start: %v", err)
		}
		pubFire(bus, "instance-register", jb(map[string]any{
			"instance_id": "ins-test", "client_type": "unittest", "work_dir": wd,
		}))
		time.Sleep(30 * time.Millisecond)
		return bus, svc
	}

	root := "crontab-0a1b2c3d"
	sub := "crontab-ct_restart"
	ensure := func(bus mq.Bus, reqID string) string {
		r := dataCall(t, bus, "data-session-ensure-session", map[string]any{
			"req_id": reqID, "instance_id": "ins-test",
			"data": map[string]any{
				"session_id": root, "kind": "crontab", "title": "定时任务", "origin": "scheduler",
			},
		})
		dialogID, _ := dataResult(t, r)["dialog_id"].(string)
		if dialogID == "" {
			t.Fatalf("根会话应回带 dialog_id: %+v", r)
		}
		r = dataCall(t, bus, "data-session-ensure-session", map[string]any{
			"req_id": reqID + "-sub", "instance_id": "ins-test",
			"data": map[string]any{
				"session_id": sub, "dialog_id": dialogID, "parent_session_id": root,
				"kind": "crontab_task", "title": "执行：echo hi", "origin": "scheduler",
			},
		})
		if !dataResult(t, r)["ok"].(bool) {
			t.Fatalf("子会话建立失败: %+v", r)
		}
		return dialogID
	}

	bus1, svc1 := start()
	dialogID := ensure(bus1, "r1")
	if n := countSessionsInDialog(t, "ins-test", dialogID); n != 2 {
		t.Fatalf("首次应建根 + 子会话: %d", n)
	}
	svc1.Stop()
	_ = bus1.Close()
	data.Reset()

	bus2, svc2 := start()
	defer func() { svc2.Stop(); _ = bus2.Close(); data.Reset() }()
	if got := ensure(bus2, "r2"); got != dialogID {
		t.Fatalf("重启后 dialog_id 应稳定: got=%q want=%q", got, dialogID)
	}
	if n := countSessionsInDialog(t, "ins-test", dialogID); n != 2 {
		t.Fatalf("重启后重复 ensure 不应新增会话行: %d", n)
	}
	if n := countDialogsOfKind(t, "ins-test", "crontab"); n != 1 {
		t.Fatalf("重启后 crontab 对话应仍为 1: %d", n)
	}

	// 关联不丢：子会话仍带 dialog_id / parent / origin；顶层列表仍排除 crontab。
	r := dataCall(t, bus2, "data-session-get", map[string]any{
		"req_id": "g", "instance_id": "ins-test", "id": sub, "dialog_id": dialogID,
	})
	d, _ := dataResult(t, r)["data"].(map[string]any)
	if d == nil || d["dialog_id"] != dialogID || d["parent_id"] != root || d["origin"] != "scheduler" {
		t.Fatalf("重启后子会话归属丢失: %+v", d)
	}
	if ids := listSessionIDs(t, bus2); len(ids) != 0 {
		t.Fatalf("重启后 crontab 对话仍不应进入顶层列表: %v", ids)
	}
}

// TestCronDialogReconcileKeepsKind：启动对账扫到 crontab 对话库时**补行但保留 kind**，
// 该对话不因此进入用户列表（“对账不误补”）。
func TestCronDialogReconcileKeepsKind(t *testing.T) {
	bus, _, _ := newTestPersist(t)

	// 先登记（不发布 instance-register）→ 直写两个对话库文件，模拟「建库后、写目录行前崩溃」。
	wd := t.TempDir()
	if err := data.Register("ins-test", wd, ""); err != nil {
		t.Fatalf("data.Register: %v", err)
	}
	pudb, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	now := time.Now().UTC().Format(data.RFC3339FixedNano)
	seedDialogFile := func(mainID, kind string) string {
		dialogID, err := data.NewDialogID()
		if err != nil {
			t.Fatalf("NewDialogID: %v", err)
		}
		db, release, err := data.OpenDialog(pudb.Path(), dialogID)
		if err != nil {
			t.Fatalf("OpenDialog: %v", err)
		}
		if err := db.Table("sessions").Upsert(mainID, data.Record{
			"session_id": mainID, "title": mainID, "parent_id": "", "kind": kind,
			"created_at": now, "updated_at": now,
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		release()
		return dialogID
	}
	cronDialog := seedDialogFile("crontab-feedface", "crontab")
	userDialog := seedDialogFile("s-reconciled", "main")
	if _, ok := dialogRow(t, "ins-test", cronDialog); ok {
		t.Fatal("前置：目录行此时不应存在")
	}

	// 发布 instance-register → persist 异步对账（补行）。
	pubFire(bus, "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": wd,
	}))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, okUser := dialogRow(t, "ins-test", userDialog)
		_, okCron := dialogRow(t, "ins-test", cronDialog)
		if okUser && okCron {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	row, ok := dialogRow(t, "ins-test", userDialog)
	if !ok {
		t.Fatal("对账应补普通对话的目录行")
	}
	if row["kind"] != "main" {
		t.Fatalf("普通对话 kind 应为 main: %+v", row)
	}
	crow, ok := dialogRow(t, "ins-test", cronDialog)
	if !ok {
		t.Fatal("对账应补 crontab 对话的目录行（保留文件与索引）")
	}
	if crow["kind"] != "crontab" {
		t.Fatalf("crontab 对话 kind 被对账改写: %+v", crow)
	}
	// 可见列表只含用户对话。
	ids := listSessionIDs(t, bus)
	if len(ids) != 1 || ids[0] != "s-reconciled" {
		t.Fatalf("对账后列表应只含用户对话: %v", ids)
	}
}

// setUsrKV 直写 usr 库 config 键（等价插件配置页经 `plugin/v1/config.user.set` 落库形态）。
func setUsrKV(t *testing.T, usrPath, key, value string) {
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

// TestCronShowInSessionListSwitch（G-99，L2）：`plugins.crontab.show-in-session-list`
// 切换后 `data-session-list` 的结果差异（缺省 false → 排除；true → 进入列表；false → 回归排除）。
func TestCronShowInSessionListSwitch(t *testing.T) {
	bus, _, usrPath := newTestPersist(t)
	regInstance(t, bus)

	// crontab 顶层对话（根会话自举建 dialog，kind=crontab）。
	root := "crontab-1234abcd"
	r := dataCall(t, bus, "data-session-ensure-session", map[string]any{
		"req_id": "c1", "instance_id": "ins-test",
		"data": map[string]any{
			"session_id": root, "kind": "crontab", "title": "定时任务", "origin": "scheduler",
		},
	})
	res := dataResult(t, r)
	if dlg, _ := res["dialog_id"].(string); dlg == "" || res["ok"] != true {
		t.Fatalf("建 crontab 对话失败: %+v", r)
	}
	// 普通用户对话（目录行 kind=main）。
	userDialog, _ := newDialog(t, "ins-test", "s-user")
	putDialogRow(t, "ins-test", userDialog, "s-user", "用户会话",
		time.Now().UTC().Format(data.RFC3339FixedNano))

	// ① 键缺失（缺省 false）→ 只含用户会话。
	if ids := listSessionIDs(t, bus); len(ids) != 1 || ids[0] != "s-user" {
		t.Fatalf("缺省应排除 crontab 对话: %v", ids)
	}

	// ② true → crontab 顶层对话进入列表（按最近活动排序，用户会话更新在后 → 先用户）。
	setUsrKV(t, usrPath, "plugins.crontab.show-in-session-list", "true")
	ids := listSessionIDs(t, bus)
	if len(ids) != 2 || ids[0] != "s-user" || ids[1] != root {
		t.Fatalf("开关 true 后 crontab 对话应进入列表: %v", ids)
	}

	// ③ false → 回归排除。
	setUsrKV(t, usrPath, "plugins.crontab.show-in-session-list", "false")
	if ids := listSessionIDs(t, bus); len(ids) != 1 || ids[0] != "s-user" {
		t.Fatalf("开关 false 后应重新排除 crontab 对话: %v", ids)
	}
}
