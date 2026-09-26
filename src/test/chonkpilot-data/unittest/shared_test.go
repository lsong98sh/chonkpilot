package data_test

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
)

// setupShared 清理包级单例（防跨测试污染）。
func setupShared(t *testing.T) {
	t.Helper()
	data.Reset()
	t.Cleanup(data.Reset)
}

// TestOpenSharedReuse：同路径复用同一连接；Release 归零后关闭。
func TestOpenSharedReuse(t *testing.T) {
	dir := t.TempDir()
	setupShared(t)
	p := filepath.Join(dir, "chonkpilot.db")

	db1, r1, err := data.OpenShared(p)
	if err != nil {
		t.Fatalf("OpenShared#1: %v", err)
	}
	db2, r2, err := data.OpenShared(p)
	if err != nil {
		t.Fatalf("OpenShared#2: %v", err)
	}
	if db1 != db2 {
		t.Fatal("same path should share the same connection")
	}
	r1()
	r2()
	// 引用归零 → 缓存移除；再开是新的打开
	db3, r3, err := data.OpenShared(p)
	if err != nil {
		t.Fatalf("OpenShared#3: %v", err)
	}
	r3()
	if db3 == db1 {
		t.Fatal("after release-to-zero, re-open should be a fresh connection")
	}
}

// TestPrjByDataDir：不同 data_dir 不同连接；同 data_dir 复用同一连接。
func TestPrjByDataDir(t *testing.T) {
	base := t.TempDir()
	setupShared(t)
	data.Register("i-a", base, filepath.Join(base, "da"))
	data.Register("i-b", base, filepath.Join(base, "db"))
	data.Register("i-a2", base, filepath.Join(base, "da")) // 同 data_dir 第二个 instance

	dba, err := data.Prj("i-a")
	if err != nil {
		t.Fatalf("Prj(i-a): %v", err)
	}
	dbb, err := data.Prj("i-b")
	if err != nil {
		t.Fatalf("Prj(i-b): %v", err)
	}
	dba2, err := data.Prj("i-a2")
	if err != nil {
		t.Fatalf("Prj(i-a2): %v", err)
	}
	if dba == dbb {
		t.Fatal("different data_dir must be different connections")
	}
	if dba != dba2 {
		t.Fatal("same data_dir must share one connection")
	}
}

// TestPrjPathByDataDir v6：data_dir 非空 → 一律 <data_dir>/chonkpilot.db（不再回落 workdir）；
// data_dir 为空 → <workdir>/.chonkpilot/chonkpilot.db。
func TestPrjPathByDataDir(t *testing.T) {
	base := t.TempDir()
	setupShared(t)
	wd := filepath.Join(base, "proj")
	dd := filepath.Join(base, "tmp-data")

	data.Register("i1", wd, dd)
	db, err := data.Prj("i1")
	if err != nil {
		t.Fatalf("Prj: %v", err)
	}
	if want := filepath.Join(dd, "chonkpilot.db"); db.Path() != want {
		t.Fatalf("Prj path=%q, want %q", db.Path(), want)
	}

	data.Register("i2", wd, "")
	db2, err := data.Prj("i2")
	if err != nil {
		t.Fatalf("Prj: %v", err)
	}
	if want := filepath.Join(wd, ".chonkpilot", "chonkpilot.db"); db2.Path() != want {
		t.Fatalf("Prj path=%q, want %q", db2.Path(), want)
	}
}

// TestPrjUsrBinding v6：data_dir 为空 → prjusr 落 <dataRoot>/<project-id>/chonkpilot.db；
// project-id 首次生成、二次复用（同一连接）。
func TestPrjUsrBinding(t *testing.T) {
	root := t.TempDir()
	setupShared(t)
	data.SetDataHome(filepath.Join(root, "usr", "chonkpilot.db"), filepath.Join(root, "data"))
	t.Cleanup(func() { data.SetDataHome("", "") })

	wd := t.TempDir()
	data.Register("i1", wd, "")

	pu, err := data.PrjUsr("i1")
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	prj, err := data.Prj("i1")
	if err != nil {
		t.Fatalf("Prj: %v", err)
	}
	id, ok := data.ReadProjectID(prj)
	if !ok || id == "" {
		t.Fatal("project-id should be generated on first PrjUsr")
	}
	if want := data.PrjUsrDBPath(id); pu.Path() != want {
		t.Fatalf("PrjUsr path=%q, want %q", pu.Path(), want)
	}
	pu2, err := data.PrjUsr("i1")
	if err != nil {
		t.Fatalf("PrjUsr#2: %v", err)
	}
	if pu2 != pu {
		t.Fatal("same instance should reuse one prjusr connection")
	}
}

// TestPrjUsrTempDataDir v6：CLI 形态（data_dir 非空）→ prjusr 与 prj 同库（临时目录，会话隔离）。
func TestPrjUsrTempDataDir(t *testing.T) {
	base := t.TempDir()
	setupShared(t)
	wd := filepath.Join(base, "proj")
	dd := filepath.Join(base, "tmp-data")
	data.Register("i1", wd, dd)

	prj, err := data.Prj("i1")
	if err != nil {
		t.Fatalf("Prj: %v", err)
	}
	pu, err := data.PrjUsr("i1")
	if err != nil {
		t.Fatalf("PrjUsr: %v", err)
	}
	if pu.Path() != prj.Path() {
		t.Fatalf("temp mode: prjusr path=%q, want same as prj %q", pu.Path(), prj.Path())
	}
}

// TestPrjUsrDir v6：prjusr 数据根解析（MW-7/MW-8 的路径口径）——
// data_dir 空 → ~/.chonkpilot/data/<project-id>（project-id 从 prj 库取，首次生成）；
// data_dir 非空 → 该目录（prj 与 prjusr 同根）。prj 库恒在 <workDir>/.chonkpilot。
func TestPrjUsrDir(t *testing.T) {
	root := t.TempDir()
	setupShared(t)
	data.SetDataHome(filepath.Join(root, "usr", "chonkpilot.db"), filepath.Join(root, "data"))
	t.Cleanup(func() { data.SetDataHome("", "") })

	wd := t.TempDir()
	got, err := data.PrjUsrDir(wd, "")
	if err != nil {
		t.Fatalf("PrjUsrDir: %v", err)
	}
	// prj 库（含 project-id）恒留项目内
	prj, release, err := data.OpenShared(data.ProjectPath(wd, ""))
	if err != nil {
		t.Fatalf("open prj: %v", err)
	}
	id, ok := data.ReadProjectID(prj)
	release()
	if !ok || id == "" {
		t.Fatal("project-id should be generated in prj db")
	}
	if want := data.PrjUsrPath(id); got != want {
		t.Fatalf("PrjUsrDir(no dataDir)=%q, want %q", got, want)
	}
	// 幂等：再次解析 → 同一 project-id → 同一数据根
	again, err := data.PrjUsrDir(wd, "")
	if err != nil {
		t.Fatalf("PrjUsrDir#2: %v", err)
	}
	if again != got {
		t.Fatalf("PrjUsrDir should be stable: %q vs %q", again, got)
	}
	// 显式 data_dir → 与 prj 同根（不入 ~/.chonkpilot/data）
	dd := t.TempDir()
	explicit, err := data.PrjUsrDir(wd, dd)
	if err != nil {
		t.Fatalf("PrjUsrDir(dataDir): %v", err)
	}
	if explicit != dd {
		t.Fatalf("PrjUsrDir(dataDir)=%q, want %q", explicit, dd)
	}
}

// TestPrjUnregistered：未登记 instance → 报错。
func TestPrjUnregistered(t *testing.T) {
	setupShared(t)
	if _, err := data.Prj("nope"); err == nil {
		t.Fatal("want error for unregistered instance")
	}
}

// TestReset：Reset 清空绑定与缓存。
func TestReset(t *testing.T) {
	setupShared(t)
	base := t.TempDir()
	data.Register("i1", base, filepath.Join(base, "d"))
	if _, err := data.Prj("i1"); err != nil {
		t.Fatalf("Prj: %v", err)
	}
	data.Reset()
	if _, err := data.Prj("i1"); err == nil {
		t.Fatal("after Reset, instance should be unregistered")
	}
}
