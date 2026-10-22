package data

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// dbUsable 判断连接是否仍可用（已 Close 的 bbolt 句柄返回错误）。
func dbUsable(db *DB) bool {
	if db == nil || db.b == nil {
		return false
	}
	return db.b.View(func(tx *bolt.Tx) error { return nil }) == nil
}

// 同 work_dir 的两个 instance 共享同一 prjusr 连接：一个退出不得关闭另一个仍在用的连接；
// 全部退出后引用归零才真正关闭（PRODSIM-1 L1 缺陷①）。
func TestPrjUsrSharedAcrossInstances(t *testing.T) {
	Reset()
	defer Reset()
	work := t.TempDir()
	dd := t.TempDir() // dataDir 非空 → prj 与 prjusr 同文件（多窗口高危场景）
	if err := Register("i1", work, dd); err != nil {
		t.Fatalf("register i1: %v", err)
	}
	if err := Register("i2", work, dd); err != nil {
		t.Fatalf("register i2: %v", err)
	}
	db1, err := PrjUsr("i1")
	if err != nil {
		t.Fatalf("prjusr i1: %v", err)
	}
	db2, err := PrjUsr("i2")
	if err != nil {
		t.Fatalf("prjusr i2: %v", err)
	}
	if db1 != db2 {
		t.Fatalf("同 work_dir 应共享同一 prjusr 连接")
	}
	path := db1.Path()

	Unregister("i1")
	if !dbUsable(db2) {
		t.Fatalf("i1 退出后 i2 仍在用的连接被误关闭")
	}

	Unregister("i2")
	storeMu.Lock()
	_, cached := shared[path]
	storeMu.Unlock()
	if cached {
		t.Fatalf("全部实例退出后 prjusr 连接未释放")
	}

	// 释放后重新登记应能重新打开（不撞同进程 bbolt 互斥）。
	if err := Register("i3", work, dd); err != nil {
		t.Fatalf("register i3: %v", err)
	}
	if _, err := PrjUsr("i3"); err != nil {
		t.Fatalf("重新打开 prjusr: %v", err)
	}
}

// 重复 Register（bridge + claim 双注册）不得清空 prjusr 绑定，否则 instance-exit 不释放连接
// （PRODSIM-1 L1 缺陷③）。
func TestReRegisterKeepsPrjUsrBinding(t *testing.T) {
	Reset()
	defer Reset()
	work := t.TempDir()
	dd := t.TempDir()
	if err := Register("i1", work, dd); err != nil {
		t.Fatalf("register: %v", err)
	}
	db, err := PrjUsr("i1")
	if err != nil {
		t.Fatalf("prjusr: %v", err)
	}
	path := db.Path()

	// 同参数第二次登记（真实链路：bridge.Start 与 instance-claim 各发一次 instance-register）。
	if err := Register("i1", work, ""); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	Unregister("i1")

	storeMu.Lock()
	_, cached := shared[path]
	storeMu.Unlock()
	if cached {
		t.Fatalf("重复登记后 instance-exit 未释放 prjusr 连接")
	}
}

// 迟到的旧 release 不得删除同路径的「新一代」缓存条目（PRODSIM-1 L1 缺陷②）。
func TestReleaseOnceKeepsNewGeneration(t *testing.T) {
	Reset()
	defer Reset()
	path := filepath.Join(t.TempDir(), "gen.db")

	_, oldRelease, err := OpenSharedLayer(path, LayerPrj)
	if err != nil {
		t.Fatalf("open gen1: %v", err)
	}
	// 模拟外部关闭并移除条目（等价于原 closeSharedPath 的效果）。
	storeMu.Lock()
	if s := shared[path]; s != nil {
		_ = s.db.Close()
		delete(shared, path)
	}
	storeMu.Unlock()

	newDB, newRelease, err := OpenSharedLayer(path, LayerPrj)
	if err != nil {
		t.Fatalf("open gen2: %v", err)
	}
	defer newRelease()

	oldRelease() // 迟到的旧 release

	storeMu.Lock()
	s := shared[path]
	storeMu.Unlock()
	if s == nil || s.db != newDB {
		t.Fatalf("旧 release 误删了新一代缓存条目")
	}
	if !dbUsable(newDB) {
		t.Fatalf("新一代连接被旧 release 关闭")
	}
}
