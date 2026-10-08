// Package lockfile 提供**跨进程文件锁**原语（复用 `LockFileEx` 模式抽象到共用处，
// 见 llm/server/sessionlock_windows.go 的 per-session 锁），当前用于「同 work-dir 只允许
// 单实例」的启动占用校验（I-74；CLI × GUI 共库冲突（D-45）复用同一原语）。
//
// 语义：非阻塞获取；已被其它进程持有 → `ErrBusy`；进程崩溃由 OS 自动释放（Windows
// LockFileEx 句柄随进程回收）——**不依赖 sticky 锁文件**（文件本身可残留，锁不残留）。
package lockfile

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// ErrBusy 表示目标锁已被其它进程持有（调用方据此给出「已被打开」的明确提示）。
var ErrBusy = errors.New("lockfile: busy")

// workDirLocksDir 返回占用锁根目录：`<home>/.chonkpilot/locks`（与 llm server 的
// per-session 锁同根，见 sessionlock_windows.go）；home 不可解析 → 回落系统临时目录。
func workDirLocksDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".chonkpilot", "locks")
}

// WorkDirLockPath 返回 work-dir 占用锁文件路径：
//
//	<home>/.chonkpilot/locks/workdir-<sha1(canonical(workDir))[:16]>.lock
//
// 放用户数据根（不污染项目目录）；用哈希做文件名避免路径长度 / 非法字符问题；
// canonicalPath 由平台文件提供（Windows 折叠大小写：同一目录的不同拼写映射到同一把锁）。
func WorkDirLockPath(workDir string) string {
	sum := sha1.Sum([]byte(canonicalPath(workDir)))
	name := "workdir-" + hex.EncodeToString(sum[:])[:16] + ".lock"
	return filepath.Join(workDirLocksDir(), name)
}

// AcquireWorkDir 非阻塞获取 work-dir 占用锁；已被占用 → ErrBusy。
// 返回的 Lock 须在进程存活期持有（`defer lock.Release()`）。
func AcquireWorkDir(workDir string) (*Lock, error) {
	return Acquire(WorkDirLockPath(workDir))
}

// WorkDirFree 试获取即释放：work-dir 当前是否空闲（供「选目录前置校验」用，不持有）。
// 非 ErrBusy 的 IO 错误（如锁目录不可写）→ **放行**（true）：占用校验是体验增强，
// 不应因锁设施故障阻断用户操作。
func WorkDirFree(workDir string) bool {
	l, err := Acquire(WorkDirLockPath(workDir))
	if err != nil {
		return !errors.Is(err, ErrBusy)
	}
	l.Release()
	return true
}
