// L2 黑盒测试：chonkpilot-cli 启动前置编排（D-45 · I-158，src/lib/cli）。
//
// 覆盖：`--data-dir` 三态解析（方案 B）、临时隔离的配置复制与退出清理、
// 真实根的 work-dir 占用校验与锁生命周期、prjusr 冲突试开报错、
// 自定数据根两分支、CopyConfigTables 源缺失跳过 / 源损坏报错。
package unittest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cli "github.com/chonkpilot/chonkpilot-cli"
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// isolateDataHome 把数据主目录重定位到测试目录（usr 主库 = <dir>/usr.db、
// prjusr 数据根 = <dir>/data），避免读写真实 ~/.chonkpilot。
// 返回隔离根目录与恢复函数（测试结束调用）。
func isolateDataHome(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	data.SetDataHome(filepath.Join(dir, "usr.db"), filepath.Join(dir, "data"))
	return dir, func() {
		data.Reset()
		data.SetDataHome("", "")
	}
}

// seedPrj 在 <wd>/.chonkpilot 预置 prj 库：生成 project-id + 写一个 config 测试键。
// 返回 project-id（对应 prjusr 库不预置 → 复制/试开按「不存在」分支走）。
func seedPrj(t *testing.T, wd string) string {
	t.Helper()
	db, err := data.OpenLayer(filepath.Join(wd, ".chonkpilot", "chonkpilot.db"), data.LayerPrj)
	if err != nil {
		t.Fatalf("seed prj: %v", err)
	}
	defer db.Close()
	id, err := data.EnsureProjectID(db)
	if err != nil {
		t.Fatalf("seed project-id: %v", err)
	}
	if err := db.Table("config").Upsert("ck-l2-prj", data.Record{"v": "p1"}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return id
}

// getCfg 打开 db 读 config 键值（校验复制结果用）。
func getCfg(t *testing.T, path string, layer data.Layer, key string) string {
	t.Helper()
	db, err := data.OpenLayer(path, layer)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	var rec data.Record
	if ok, err := db.Table("config").Get(key, &rec); err != nil || !ok {
		t.Fatalf("config 键 %q 未复制到 %s：ok=%v err=%v", key, path, ok, err)
	}
	v, _ := rec["v"].(string)
	return v
}

// TestResolveDataModeThreeState 三态判定（方案 B，2026-10-01 反转）：
// 未传 → 临时隔离；显式留空 → 真实根；给路径 → 自定数据根。
func TestResolveDataModeThreeState(t *testing.T) {
	if got := cli.ResolveDataMode("", false); got != cli.DataModeTemp {
		t.Fatalf("未传 --data-dir 应 DataModeTemp，得 %v", got)
	}
	if got := cli.ResolveDataMode("", true); got != cli.DataModeReal {
		t.Fatalf("`--data-dir=` 应 DataModeReal，得 %v", got)
	}
	if got := cli.ResolveDataMode("C:/ck-root", true); got != cli.DataModeCustom {
		t.Fatalf("`--data-dir=C:/ck-root` 应 DataModeCustom，得 %v", got)
	}
}

// TestPrepareTempIsolation ① 未传 → 临时隔离：tmp 空根 + 复制 usr/prj 三级配置
// （prjusr 源不存在 → 跳过），Cleanup 删 tmp 根并恢复数据主目录默认。
func TestPrepareTempIsolation(t *testing.T) {
	dir, restore := isolateDataHome(t)
	defer restore()
	wd := t.TempDir()

	// 预置 usr 源（重定位后的主库）。
	udb, err := data.OpenLayer(filepath.Join(dir, "usr.db"), data.LayerUsr)
	if err != nil {
		t.Fatalf("seed usr: %v", err)
	}
	if err := udb.Table("config").Upsert("ck-l2-usr", data.Record{"v": "u1"}); err != nil {
		t.Fatalf("seed usr config: %v", err)
	}
	if err := udb.Close(); err != nil {
		t.Fatal(err)
	}
	seedPrj(t, wd) // prj 源 + 新 project-id（prjusr 源不存在 → 跳过）

	p, err := cli.PrepareDataDir(wd, "", false)
	if err != nil {
		t.Fatalf("PrepareDataDir: %v", err)
	}
	if p.Mode != cli.DataModeTemp {
		t.Fatalf("Mode 应 DataModeTemp，得 %v", p.Mode)
	}
	if p.DataDir == "" || p.DataDir == wd {
		t.Fatalf("临时根应非空且 ≠ workDir，得 %q", p.DataDir)
	}
	if got := getCfg(t, filepath.Join(p.DataDir, "usr.db"), data.LayerUsr, "ck-l2-usr"); got != "u1" {
		t.Fatalf("usr 配置未复制：v=%q", got)
	}
	if got := getCfg(t, filepath.Join(p.DataDir, "chonkpilot.db"), data.LayerPrj, "ck-l2-prj"); got != "p1" {
		t.Fatalf("prj 配置未复制：v=%q", got)
	}

	p.Cleanup()
	if _, err := os.Stat(p.DataDir); !os.IsNotExist(err) {
		t.Fatalf("Cleanup 后临时根应删除：err=%v", err)
	}
	if data.UserPath() == filepath.Join(dir, "usr.db") {
		t.Fatal("Cleanup 后数据主目录应恢复默认")
	}
}

// TestPrepareRealLockLifecycle ② 留空 → 真实根：Mode/DataDir 正确、持有 work-dir 锁；
// Cleanup 释放锁后可被再次获取。
func TestPrepareRealLockLifecycle(t *testing.T) {
	_, restore := isolateDataHome(t)
	defer restore()
	wd := t.TempDir()

	p, err := cli.PrepareDataDir(wd, "", true)
	if err != nil {
		t.Fatalf("PrepareDataDir: %v", err)
	}
	if p.Mode != cli.DataModeReal || p.DataDir != "" {
		t.Fatalf("应 (DataModeReal, DataDir=\"\")，得 (%v, %q)", p.Mode, p.DataDir)
	}
	p.Cleanup()

	l, err := lockfile.AcquireWorkDir(wd)
	if err != nil {
		t.Fatalf("Cleanup 后 work-dir 锁应可再次获取：%v", err)
	}
	l.Release()
}

// TestPrepareRealBusyError ② 真实根 + work-dir 已被另一实例占用 → 明确报错
// （提示关闭该实例或改用临时模式），不静默放行。
func TestPrepareRealBusyError(t *testing.T) {
	_, restore := isolateDataHome(t)
	defer restore()
	wd := t.TempDir()

	l, err := lockfile.AcquireWorkDir(wd)
	if err != nil {
		t.Fatalf("预置 work-dir 占用: %v", err)
	}
	defer l.Release()

	_, err = cli.PrepareDataDir(wd, "", true)
	if err == nil || !strings.Contains(err.Error(), "work-dir 已被另一实例打开") {
		t.Fatalf("busy 应明确报错，得：%v", err)
	}
}

// TestPrepareRealPrjUsrOccupiedError ② 真实根 + prj 库已有 project-id 且 prjusr 库
// 被另一进程常开（模拟 GUI）→ 启动准备期试开即明确报错（提示改用临时模式或关闭 GUI）。
func TestPrepareRealPrjUsrOccupiedError(t *testing.T) {
	_, restore := isolateDataHome(t)
	defer restore()
	wd := t.TempDir()
	id := seedPrj(t, wd)

	// 预置 prjusr 库并持有句柄（排他锁 → 模拟 GUI 常开占用）。
	puPath := data.PrjUsrDBPath(id)
	pu, err := data.OpenLayer(puPath, data.LayerPrjUsr)
	if err != nil {
		t.Fatalf("预置 prjusr: %v", err)
	}
	defer pu.Close()

	_, err = cli.PrepareDataDir(wd, "", true)
	if err == nil || !strings.Contains(err.Error(), "prjusr 库被另一实例占用") {
		t.Fatalf("prjusr 被占应明确报错，得：%v", err)
	}
}

// TestPrepareCustomWithChonkpilot ③ 给路径 + workdir 已有 .chonkpilot →
// 自定数据根重定位 prjusr、prj 仍取 workdir（data_dir="" 下发），试开通过。
func TestPrepareCustomWithChonkpilot(t *testing.T) {
	_, restore := isolateDataHome(t)
	defer restore()
	wd := t.TempDir()
	seedPrj(t, wd) // prjusr 未被占 → 试开通过

	p, err := cli.PrepareDataDir(wd, t.TempDir(), true)
	if err != nil {
		t.Fatalf("PrepareDataDir: %v", err)
	}
	if p.Mode != cli.DataModeCustom || p.DataDir != "" {
		t.Fatalf("应 (DataModeCustom, DataDir=\"\")，得 (%v, %q)", p.Mode, p.DataDir)
	}
	p.Cleanup()
}

// TestPrepareCustomNoChonkpilot ③ 给路径 + workdir 无 .chonkpilot → 回落 tmp 空根
// （prj/prjusr 同库于临时区，不污染 cwd），Cleanup 删除。
func TestPrepareCustomNoChonkpilot(t *testing.T) {
	_, restore := isolateDataHome(t)
	defer restore()
	wd := t.TempDir()

	p, err := cli.PrepareDataDir(wd, t.TempDir(), true)
	if err != nil {
		t.Fatalf("PrepareDataDir: %v", err)
	}
	if p.Mode != cli.DataModeCustom || p.DataDir == "" {
		t.Fatalf("无 .chonkpilot 应回落 tmp 根（非空 DataDir），得 (%v, %q)", p.Mode, p.DataDir)
	}
	p.Cleanup()
	if _, err := os.Stat(p.DataDir); !os.IsNotExist(err) {
		t.Fatalf("Cleanup 后 tmp 根应删除：err=%v", err)
	}
}

// TestCopyConfigTablesMissingSourceSkips 源库不存在 → 跳过（nil），不建目标库。
func TestCopyConfigTablesMissingSourceSkips(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.db")
	if err := data.CopyConfigTables(dst, data.LayerUsr, filepath.Join(dir, "nope.db")); err != nil {
		t.Fatalf("源缺失应跳过（nil），得：%v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("源缺失不应创建目标库：err=%v", err)
	}
}

// TestCopyConfigTablesBrokenSourceErrors 源存在但损坏（非 bbolt 库）→ 明确报错，
// 不静默跳过（D-45 ④：静默降级会让 CLI 拿着缺配置的库跑）。
func TestCopyConfigTablesBrokenSourceErrors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "broken.db")
	if err := os.WriteFile(src, []byte("not-a-bolt-db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := data.CopyConfigTables(filepath.Join(dir, "dst.db"), data.LayerUsr, src); err == nil {
		t.Fatal("源损坏应报错（D-45 ④），得 nil")
	}
}
