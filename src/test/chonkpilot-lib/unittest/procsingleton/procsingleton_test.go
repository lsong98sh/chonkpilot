// procsingleton_test.go — server 进程单例的**纯逻辑**验证（🆕 2026-10-10；19 §8.11 / 62 §3A）。
//
// 口径：单例判据 = **server exe 同目录的、按用户隔离的文件锁**
// （`<exe目录>/chonkpilot-server.<userHash>.lock`）；已占用 → 第二实例退出。文件锁互斥
// （`Acquire`）在 `lockfile` 包单测覆盖（含 ErrBusy 映射），此处只验 `LockPath` 的落点约定、
// **按用户隔离**（不同用户 → 不同锁名）与「exe 不可定位时回落临时目录」。
package procsingleton_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/procsingleton"
)

// TestLockPathExeDir：锁文件落 **server exe 同目录**，文件名 = chonkpilot-server.<userHash>.lock。
func TestLockPathExeDir(t *testing.T) {
	const user = "alice"
	got, warn := procsingleton.LockPath(user)
	exe, err := os.Executable()
	if err != nil || exe == "" {
		// os.Executable 罕见失败 → 应回落临时目录并返回 warn（不阻断）。
		if warn == nil {
			t.Fatalf("os.Executable 失败时应返回 warn，got path=%q", got)
		}
		if dir := filepath.Dir(got); dir != os.TempDir() {
			t.Fatalf("回落目录 = %q, want %q", dir, os.TempDir())
		}
		if base := filepath.Base(got); !isServerLockName(base) {
			t.Fatalf("回落锁文件名 = %q, want chonkpilot-server.<userHash>.lock", base)
		}
		return
	}
	if warn != nil {
		t.Fatalf("os.Executable 成功不应返回 warn: %v", warn)
	}
	base := filepath.Base(got)
	if !isServerLockName(base) {
		t.Fatalf("锁文件名 = %q, want chonkpilot-server.<userHash>.lock", base)
	}
	if dir, want := filepath.Dir(got), filepath.Dir(exe); dir != want {
		t.Fatalf("锁目录 = %q, want exe 目录 %q", dir, want)
	}
	// 按用户隔离（R9-1-2）：不同用户 → 不同锁文件名（同名则全局互斥，属回归）。
	if other, _ := procsingleton.LockPath("bob"); filepath.Base(other) == base {
		t.Fatalf("不同用户应映射到不同锁文件名，got %q", base)
	}
}

// isServerLockName 判定是否形如 `chonkpilot-server.<userHash>.lock`。
func isServerLockName(base string) bool {
	return strings.HasPrefix(base, "chonkpilot-server.") && strings.HasSuffix(base, ".lock")
}
