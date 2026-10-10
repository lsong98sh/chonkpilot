// flash_singleton_test.go — flash 单例锁（**prjusr 文件锁**）验证（🆕 2026-10-10；19 §8.11 / 62 §3A）。
//
// 口径：flash 的项目单例锁 = `<work_dir>/.chonkpilot/chonkpilot.lock`（prjusr 数据根下的
// `chonkpilot.lock`），与**桌面单体同名同目录** → 同一项目在本机互斥。窗口类（wndclass）单例
// 已废弃移除。互斥的运行时行为由 `flash_test.go` 的 TestAcquireProjectBusy 覆盖，此处只验路径约定。
package flash_test

import (
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-gui/flash"
	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// TestPrjUsrLockPath：flash 的项目单例锁落 `<work_dir>/.chonkpilot/chonkpilot.lock`
// （文件名 = chonkpilot.lock；目录 = flash 的 prjusr 数据根）。
func TestPrjUsrLockPath(t *testing.T) {
	wd := filepath.Join(t.TempDir(), "proj")
	want := filepath.Join(wd, ".chonkpilot", "chonkpilot.lock")
	if got := lockfile.PrjUsrLockPath(flash.DataDirFor(wd)); got != want {
		t.Fatalf("prjusr 锁路径 = %q, want %q", got, want)
	}
	// 文件名常量约定（与桌面单体共用）——防误改成 per-user / 其它名字。
	if lockfile.PrjUsrLockName != "chonkpilot.lock" {
		t.Fatalf("锁文件名 = %q, want chonkpilot.lock", lockfile.PrjUsrLockName)
	}
}
