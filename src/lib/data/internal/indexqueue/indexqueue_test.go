// indexqueue 门面单测：append 去重 / pending 计数 / work_dir 隔离 / take 排序与上限 /
// ack 成功与失败退避 / compact 清理。
//
// 隔离方式同 internal/session/inbox_facade_test.go：临时 usr 根 + 临时 work_dir/data_dir，
// 每个用例 data.Reset() 前后清空连接缓存。
package indexqueue

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

func newEnv(t *testing.T) (*Store, facade.Scope) {
	t.Helper()
	usrDir, wd, dd := t.TempDir(), t.TempDir(), t.TempDir()
	data.Reset()
	t.Cleanup(data.Reset)
	s := New(kernel.NewBase(nil, kernel.Options{UsrPath: filepath.Join(usrDir, "usr.db"), AppDir: usrDir}))
	return s, facade.Scope{WorkDir: wd, DataDir: dd}
}

func tableFor(t *testing.T, s *Store, scope facade.Scope) *data.Table {
	t.Helper()
	prj, err := s.prjUsr(scope)
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	return prj.Table(tableName)
}

func getRow(t *testing.T, s *Store, scope facade.Scope, id string) (data.Record, bool) {
	t.Helper()
	var rec data.Record
	ok, err := tableFor(t, s, scope).Get(id, &rec)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return rec, ok
}

// TestWorkDirOnlyScopeReusesRegisteredDataDir：调用方只带 work_dir（filesys 变更入队 /
// 插件桥队列控制帧）时，复用同 work_dir 已登记实例的非空 data_dir —— 不得在 work_dir 下
// 另建 prj 库（`--data-dir` 分离形态的 workdir 零污染；run_data_dir 一.5 回归）。
func TestWorkDirOnlyScopeReusesRegisteredDataDir(t *testing.T) {
	s, scope := newEnv(t)
	// 模拟 instance-register：同 work_dir 已有带 data_dir 的实例登记
	if err := data.Register("inst-1", scope.WorkDir, scope.DataDir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.txt", Operation: "write",
		Scope: facade.Scope{WorkDir: scope.WorkDir},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.WorkDir, ".chonkpilot", "chonkpilot.db")); !os.IsNotExist(err) {
		t.Fatalf("work_dir 下不应创建 prj 库（--data-dir 已分离）：err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(scope.DataDir, "chonkpilot.db")); err != nil {
		t.Fatalf("数据根应生成 prj 库：%v", err)
	}
}

// TestAppendPending：入队一条 → pending=1；路径归一为 '/'；未改状态前 take 仍可读到。
func TestAppendPending(t *testing.T) {
	s, scope := newEnv(t)
	resp, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: `src\a\b.go`, Operation: "write", Scope: scope,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !resp.OK || resp.ID == "" {
		t.Fatalf("Append 应答异常：%+v", resp)
	}
	pending, err := s.IndexQueuePending(facade.IndexQueuePendingRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending.Pending != 1 {
		t.Fatalf("pending 应为 1，实际 %d", pending.Pending)
	}
	take, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(take.Items) != 1 {
		t.Fatalf("take 应返回 1 行，实际 %d", len(take.Items))
	}
	it := take.Items[0]
	if it.ID != resp.ID || it.Path != "src/a/b.go" || it.State != statePending || it.Attempts != 0 {
		t.Fatalf("行内容异常：%+v", it)
	}
}

// TestAppendDedup：同 (work_dir, path) 连续 append → 仍 1 行，operation 更新且 created_at 保留。
func TestAppendDedup(t *testing.T) {
	s, scope := newEnv(t)
	first, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.txt", Operation: "create", Scope: scope,
	})
	if err != nil {
		t.Fatalf("Append(create): %v", err)
	}
	take, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil || len(take.Items) != 1 {
		t.Fatalf("Take: err=%v items=%d", err, len(take.Items))
	}
	created := take.Items[0].CreatedAt

	second, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.txt", Operation: "write", Scope: scope,
	})
	if err != nil {
		t.Fatalf("Append(write): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("去重应复用既有行：first=%s second=%s", first.ID, second.ID)
	}
	pending, err := s.IndexQueuePending(facade.IndexQueuePendingRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending.Pending != 1 {
		t.Fatalf("去重后 pending 应为 1，实际 %d", pending.Pending)
	}
	take, err = s.IndexQueueTake(facade.IndexQueueTakeRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil || len(take.Items) != 1 {
		t.Fatalf("Take: err=%v items=%d", err, len(take.Items))
	}
	it := take.Items[0]
	if it.Operation != "write" {
		t.Fatalf("operation 应更新为 write，实际 %q", it.Operation)
	}
	if it.CreatedAt != created {
		t.Fatalf("created_at 应保留原值：before=%q after=%q", created, it.CreatedAt)
	}
}

// TestWorkDirIsolation：不同 work_dir 的行互不影响（同一 prjusr 库内按 work_dir 过滤）。
func TestWorkDirIsolation(t *testing.T) {
	s, scope := newEnv(t)
	scope2 := facade.Scope{WorkDir: t.TempDir(), DataDir: scope.DataDir}
	for _, sc := range []facade.Scope{scope, scope2} {
		if _, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
			WorkDir: sc.WorkDir, Path: "x.txt", Operation: "create", Scope: sc,
		}); err != nil {
			t.Fatalf("Append(%s): %v", sc.WorkDir, err)
		}
	}
	for _, sc := range []facade.Scope{scope, scope2} {
		pending, err := s.IndexQueuePending(facade.IndexQueuePendingRequest{WorkDir: sc.WorkDir, Scope: sc})
		if err != nil {
			t.Fatalf("Pending(%s): %v", sc.WorkDir, err)
		}
		if pending.Pending != 1 {
			t.Fatalf("%s pending 应为 1，实际 %d", sc.WorkDir, pending.Pending)
		}
	}
	take, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil || len(take.Items) != 1 {
		t.Fatalf("Take: err=%v items=%d", err, len(take.Items))
	}
	if take.Items[0].WorkDir != scope.WorkDir {
		t.Fatalf("take 不应串到其它 work_dir：%q", take.Items[0].WorkDir)
	}
}

// TestTakeOrderAndLimit：按 created_at 升序 + Limit 截断；Take 不改状态（再取仍能取到）。
func TestTakeOrderAndLimit(t *testing.T) {
	s, scope := newEnv(t)
	tb := tableFor(t, s, scope)
	rows := []struct {
		key, path, created string
	}{
		{"k2", "b.txt", "2026-01-01T00:00:02.000000000Z"},
		{"k1", "a.txt", "2026-01-01T00:00:01.000000000Z"},
		{"k3", "c.txt", "2026-01-01T00:00:03.000000000Z"},
	}
	for _, r := range rows {
		if err := tb.Insert(r.key, data.Record{
			"work_dir": scope.WorkDir, "path": r.path, "operation": "write",
			"state": statePending, "attempts": 0, "created_at": r.created, "updated_at": r.created,
		}); err != nil {
			t.Fatalf("insert %s: %v", r.key, err)
		}
	}
	take, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{WorkDir: scope.WorkDir, Limit: 2, Scope: scope})
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(take.Items) != 2 {
		t.Fatalf("Limit=2 应返回 2 行，实际 %d", len(take.Items))
	}
	if take.Items[0].Path != "a.txt" || take.Items[1].Path != "b.txt" {
		t.Fatalf("应按 created_at 升序返回：%+v", take.Items)
	}
	again, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil {
		t.Fatalf("Take(again): %v", err)
	}
	if len(again.Items) != 3 {
		t.Fatalf("Take 不应修改状态（应仍取到 3 行），实际 %d", len(again.Items))
	}
}

// TestAckSuccess：成功回执 → state=done、pending 归零。
func TestAckSuccess(t *testing.T) {
	s, scope := newEnv(t)
	for _, p := range []string{"a.txt", "b.txt"} {
		if _, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
			WorkDir: scope.WorkDir, Path: p, Operation: "write", Scope: scope,
		}); err != nil {
			t.Fatalf("Append(%s): %v", p, err)
		}
	}
	take, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil || len(take.Items) != 2 {
		t.Fatalf("Take: err=%v items=%d", err, len(take.Items))
	}
	ids := []string{take.Items[0].ID, take.Items[1].ID}
	ack, err := s.IndexQueueAck(facade.IndexQueueAckRequest{IDs: ids, Scope: scope})
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if !ack.OK || ack.Updated != 2 {
		t.Fatalf("Ack 应答异常：%+v", ack)
	}
	for _, id := range ids {
		rec, ok := getRow(t, s, scope, id)
		if !ok || kernel.Sval(rec["state"]) != stateDone {
			t.Fatalf("%s 应为 done：ok=%v rec=%v", id, ok, rec)
		}
	}
	pending, err := s.IndexQueuePending(facade.IndexQueuePendingRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending.Pending != 0 {
		t.Fatalf("done 后 pending 应为 0，实际 %d", pending.Pending)
	}
	// 不存在的 id 跳过，不报整体失败。
	ack, err = s.IndexQueueAck(facade.IndexQueueAckRequest{IDs: []string{"nope"}, Scope: scope})
	if err != nil || ack.Updated != 0 {
		t.Fatalf("不存在的 id 应跳过：err=%v ack=%+v", err, ack)
	}
}

// TestAckFailureBackoff：失败回执 → attempts 递增；3 次后转 failed、pending 归零。
func TestAckFailureBackoff(t *testing.T) {
	s, scope := newEnv(t)
	app, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.txt", Operation: "write", Scope: scope,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	for i := 1; i <= maxAttempts; i++ {
		ack, err := s.IndexQueueAck(facade.IndexQueueAckRequest{
			IDs: []string{app.ID}, Failed: true, Error: "boom", Scope: scope,
		})
		if err != nil || ack.Updated != 1 {
			t.Fatalf("Ack(%d): err=%v ack=%+v", i, err, ack)
		}
		rec, ok := getRow(t, s, scope, app.ID)
		if !ok {
			t.Fatalf("第 %d 次失败回执后行应保留", i)
		}
		if got := intVal(rec["attempts"]); got != i {
			t.Fatalf("attempts 应为 %d，实际 %d", i, got)
		}
		if kernel.Sval(rec["last_error"]) != "boom" {
			t.Fatalf("last_error 应为 boom，实际 %q", kernel.Sval(rec["last_error"]))
		}
		wantState := statePending
		if i >= maxAttempts {
			wantState = stateFailed
		}
		if got := kernel.Sval(rec["state"]); got != wantState {
			t.Fatalf("第 %d 次失败后 state 应为 %s，实际 %s", i, wantState, got)
		}
	}
	pending, err := s.IndexQueuePending(facade.IndexQueuePendingRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending.Pending != 0 {
		t.Fatalf("failed 后 pending 应为 0，实际 %d", pending.Pending)
	}
}

// TestCompact：只删过期 done；未过期 done / failed / pending / 时间不可解析 → 保留。
func TestCompact(t *testing.T) {
	s, scope := newEnv(t)
	tb := tableFor(t, s, scope)
	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(d).Format(data.RFC3339FixedNano) }
	rows := map[string]data.Record{
		"done-old":    {"work_dir": scope.WorkDir, "path": "old.txt", "state": stateDone, "created_at": ts(-3 * time.Hour), "updated_at": ts(-2 * time.Hour)},
		"done-new":    {"work_dir": scope.WorkDir, "path": "new.txt", "state": stateDone, "created_at": ts(-time.Hour), "updated_at": ts(-time.Minute)},
		"done-bad":    {"work_dir": scope.WorkDir, "path": "bad.txt", "state": stateDone, "created_at": "not-a-time", "updated_at": "not-a-time"},
		"failed-old":  {"work_dir": scope.WorkDir, "path": "failed.txt", "state": stateFailed, "created_at": ts(-3 * time.Hour), "updated_at": ts(-2 * time.Hour)},
		"pending-old": {"work_dir": scope.WorkDir, "path": "pending.txt", "state": statePending, "created_at": ts(-3 * time.Hour), "updated_at": ts(-2 * time.Hour)},
	}
	for k, r := range rows {
		if err := tb.Insert(k, r); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}
	compact, err := s.IndexQueueCompact(facade.IndexQueueCompactRequest{KeepDoneSeconds: 3600, Scope: scope})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !compact.OK || compact.Removed != 1 {
		t.Fatalf("Compact 应只删 1 行，实际 %+v", compact)
	}
	if _, ok := getRow(t, s, scope, "done-old"); ok {
		t.Fatalf("过期 done 行应被删除")
	}
	for _, k := range []string{"done-new", "done-bad", "failed-old", "pending-old"} {
		if _, ok := getRow(t, s, scope, k); !ok {
			t.Fatalf("%s 不应被 Compact 删除", k)
		}
	}
}

// TestFanoutAppendAndTake：一次 Append 扇出 vfts / codegraph 两行，各自 take 互不遗漏。
func TestFanoutAppendAndTake(t *testing.T) {
	s, scope := newEnv(t)
	resp, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.go", Operation: "write",
		Consumers: []string{"vfts", "codegraph"}, Scope: scope,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !resp.OK || len(resp.IDs) != 2 || resp.ID != resp.IDs[0] {
		t.Fatalf("扇出应答异常：%+v", resp)
	}
	for _, consumer := range []string{"vfts", "codegraph"} {
		take, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
			WorkDir: scope.WorkDir, Consumer: consumer, Scope: scope,
		})
		if err != nil {
			t.Fatalf("Take(%s): %v", consumer, err)
		}
		if len(take.Items) != 1 {
			t.Fatalf("%s 应取到 1 行，实际 %d: %+v", consumer, len(take.Items), take.Items)
		}
		if take.Items[0].Consumer != consumer || take.Items[0].Path != "a.go" {
			t.Fatalf("%s 行内容异常：%+v", consumer, take.Items[0])
		}
	}
}

// TestFanoutAckIsolation：一方 ack done 不影响另一方；双方 ack 后各自 pending=0。
func TestFanoutAckIsolation(t *testing.T) {
	s, scope := newEnv(t)
	if _, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.go", Operation: "write",
		Consumers: []string{"vfts", "codegraph"}, Scope: scope,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	takeV, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir: scope.WorkDir, Consumer: "vfts", Scope: scope,
	})
	if err != nil || len(takeV.Items) != 1 {
		t.Fatalf("Take(vfts): err=%v items=%d", err, len(takeV.Items))
	}
	if _, err := s.IndexQueueAck(facade.IndexQueueAckRequest{
		Consumer: "vfts", IDs: []string{takeV.Items[0].ID}, Scope: scope,
	}); err != nil {
		t.Fatalf("Ack(vfts): %v", err)
	}
	takeC, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir: scope.WorkDir, Consumer: "codegraph", Scope: scope,
	})
	if err != nil || len(takeC.Items) != 1 {
		t.Fatalf("vfts ack 后 codegraph 仍应取到 1 行：err=%v items=%+v", err, takeC.Items)
	}
	if _, err := s.IndexQueueAck(facade.IndexQueueAckRequest{
		Consumer: "codegraph", IDs: []string{takeC.Items[0].ID}, Scope: scope,
	}); err != nil {
		t.Fatalf("Ack(codegraph): %v", err)
	}
	for _, consumer := range []string{"vfts", "codegraph"} {
		p, err := s.IndexQueuePending(facade.IndexQueuePendingRequest{
			WorkDir: scope.WorkDir, Consumer: consumer, Scope: scope,
		})
		if err != nil || p.Pending != 0 {
			t.Fatalf("%s pending 应为 0：err=%v p=%+v", consumer, err, p)
		}
	}
}

// TestFanoutAppendDedupPerConsumer：同 consumer 重复 Append 去重；不同 consumer 各 1 行。
func TestFanoutAppendDedupPerConsumer(t *testing.T) {
	s, scope := newEnv(t)
	first, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.go", Operation: "create",
		Consumers: []string{"vfts"}, Scope: scope,
	})
	if err != nil {
		t.Fatalf("Append(vfts): %v", err)
	}
	second, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.go", Operation: "write",
		Consumers: []string{"vfts", "codegraph"}, Scope: scope,
	})
	if err != nil {
		t.Fatalf("Append(vfts+codegraph): %v", err)
	}
	if len(second.IDs) != 2 || second.IDs[0] != first.ID {
		t.Fatalf("vfts 应复用既有行、codegraph 新增：first=%s resp=%+v", first.ID, second)
	}
	pending, err := s.IndexQueuePending(facade.IndexQueuePendingRequest{WorkDir: scope.WorkDir, Scope: scope})
	if err != nil || pending.Pending != 2 {
		t.Fatalf("按 consumer 去重后总 pending 应为 2：err=%v p=%+v", err, pending)
	}
	takeV, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir: scope.WorkDir, Consumer: "vfts", Scope: scope,
	})
	if err != nil || len(takeV.Items) != 1 || takeV.Items[0].Operation != "write" {
		t.Fatalf("vfts 行应更新为 write：err=%v items=%+v", err, takeV.Items)
	}
	takeC, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir: scope.WorkDir, Consumer: "codegraph", Scope: scope,
	})
	if err != nil || len(takeC.Items) != 1 || takeC.Items[0].Operation != "write" {
		t.Fatalf("codegraph 行应为 write：err=%v items=%+v", err, takeC.Items)
	}
}

// TestFanoutFailureAttemptsIsolation：失败 ack 只影响对应 consumer（attempts 独立）。
func TestFanoutFailureAttemptsIsolation(t *testing.T) {
	s, scope := newEnv(t)
	if _, err := s.IndexQueueAppend(facade.IndexQueueAppendRequest{
		WorkDir: scope.WorkDir, Path: "a.go", Operation: "write",
		Consumers: []string{"vfts", "codegraph"}, Scope: scope,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	takeV, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir: scope.WorkDir, Consumer: "vfts", Scope: scope,
	})
	if err != nil || len(takeV.Items) != 1 {
		t.Fatalf("Take(vfts): err=%v items=%d", err, len(takeV.Items))
	}
	ack, err := s.IndexQueueAck(facade.IndexQueueAckRequest{
		Consumer: "vfts", IDs: []string{takeV.Items[0].ID}, Failed: true, Error: "boom", Scope: scope,
	})
	if err != nil || ack.Updated != 1 {
		t.Fatalf("Ack(vfts failed): err=%v ack=%+v", err, ack)
	}
	takeC, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir: scope.WorkDir, Consumer: "codegraph", Scope: scope,
	})
	if err != nil || len(takeC.Items) != 1 {
		t.Fatalf("Take(codegraph): err=%v items=%d", err, len(takeC.Items))
	}
	if takeC.Items[0].Attempts != 0 {
		t.Fatalf("codegraph attempts 不应受 vfts 失败影响：%+v", takeC.Items[0])
	}
	takeV, err = s.IndexQueueTake(facade.IndexQueueTakeRequest{
		WorkDir: scope.WorkDir, Consumer: "vfts", Scope: scope,
	})
	if err != nil || len(takeV.Items) != 1 || takeV.Items[0].Attempts != 1 || takeV.Items[0].LastError != "boom" {
		t.Fatalf("vfts 行应 attempts=1：err=%v items=%+v", err, takeV.Items)
	}
	if _, err := s.IndexQueueAck(facade.IndexQueueAckRequest{
		Consumer: "codegraph", IDs: []string{takeC.Items[0].ID}, Scope: scope,
	}); err != nil {
		t.Fatalf("Ack(codegraph): %v", err)
	}
	recC, ok := getRow(t, s, scope, takeC.Items[0].ID)
	if !ok || kernel.Sval(recC["state"]) != stateDone || intVal(recC["attempts"]) != 0 {
		t.Fatalf("codegraph 行应 done 且 attempts=0：%+v", recC)
	}
}

// TestLegacyRowVisibleToAllConsumers：无 consumer 的旧行对任意 consumer 可见、可回执。
func TestLegacyRowVisibleToAllConsumers(t *testing.T) {
	s, scope := newEnv(t)
	tb := tableFor(t, s, scope)
	created := "2026-01-01T00:00:01.000000000Z"
	if err := tb.Insert("legacy-1", data.Record{
		"work_dir": scope.WorkDir, "path": "old.go", "operation": "write",
		"state": statePending, "attempts": 0, "created_at": created, "updated_at": created,
	}); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	for _, consumer := range []string{"vfts", "codegraph"} {
		take, err := s.IndexQueueTake(facade.IndexQueueTakeRequest{
			WorkDir: scope.WorkDir, Consumer: consumer, Scope: scope,
		})
		if err != nil || len(take.Items) != 1 {
			t.Fatalf("legacy 行应对 %s 可见：err=%v items=%+v", consumer, err, take.Items)
		}
		if take.Items[0].Consumer != "" {
			t.Fatalf("legacy 行 consumer 应为空：%+v", take.Items[0])
		}
	}
	ack, err := s.IndexQueueAck(facade.IndexQueueAckRequest{
		Consumer: "vfts", IDs: []string{"legacy-1"}, Scope: scope,
	})
	if err != nil || ack.Updated != 1 {
		t.Fatalf("legacy 行应可由 vfts 回执：err=%v ack=%+v", err, ack)
	}
	if rec, ok := getRow(t, s, scope, "legacy-1"); !ok || kernel.Sval(rec["state"]) != stateDone {
		t.Fatalf("legacy 行应 done：ok=%v rec=%+v", ok, rec)
	}
}
