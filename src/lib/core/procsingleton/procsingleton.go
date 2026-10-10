// Package procsingleton 提供「进程级单例」判定（🆕 2026-10-10；19 §8.11 / 62 §3A）。
//
// 用途：`chonkpilot-server.exe` **每个用户一份** —— 同一用户的第二份**立即退出**（exit code
// 非 0），**不得**触碰 / 覆盖既有 UDS socket，**不得**启动第二份服务。
//
// 判据 = **server exe 同目录的文件锁**（`<exe目录>/chonkpilot-server.<userHash>.lock`，复用
// `lockfile` 的文件锁原语）：非阻塞获取；已被其它进程持有 → duplicate；进程崩溃由 OS
// 自动释放（不依赖 sticky 锁文件，见 lockfile 包）。**按用户隔离**（userHash = 对
// `osuser.CurrentUser()` 取值取哈希）——与 per-user 的 UDS 路径（`%LOCALAPPDATA%`，见
// `uds.DefaultPath`）同口径，允许多用户 / 多会话在同机同安装目录各起一份 server
// （R9-1-2/R9-1-3；旧「固定文件名」在 RDS 场景全局互斥属回归）。
//
// **不再**用「UDS bind 失败」判定 —— `uds.Server.Start()` 先 `os.Remove` 旧 socket 再
// `net.Listen`，Windows 下同一路径**重复 bind 不失败**（见 `uds` 包注释），旧口径不成立；
// 亦**废弃**原「命名互斥体（CreateMutexW）」判据（改文件锁，便于观测与跨形态统一）。
package procsingleton

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/lockfile"
)

// lockNameFor 生成某用户的 server 单例锁文件名：`chonkpilot-server.<userHash>.lock`。
// userHash = sha1(user) 前 16 个十六进制字符（与 lockfile.WorkDirLockPath 同口径：短哈希做
// 文件名，规避用户名中的非法字符 / 路径长度问题）。
func lockNameFor(user string) string {
	sum := sha1.Sum([]byte(user))
	return "chonkpilot-server." + hex.EncodeToString(sum[:])[:16] + ".lock"
}

// LockPath 返回**指定用户** server 单例锁的**首选**路径 =
// `<server exe 目录>/chonkpilot-server.<userHash>.lock`（用户明确要求：锁落 exe 同目录）。
// os.Executable 失败（或返回空路径的异常态）→ 回落系统临时目录，返回**非 nil** warn
// （调用方告警但**不阻断**启动）。
func LockPath(user string) (path string, warn error) {
	name := lockNameFor(user)
	exe, err := os.Executable()
	if err != nil {
		return filepath.Join(os.TempDir(), name), err
	}
	if exe == "" {
		// R9-1-5：`exe=="" && err==nil` 的异常态亦须返回**非 nil** warn，否则调用方静默回落。
		return filepath.Join(os.TempDir(), name), errors.New("procsingleton: os.Executable 返回空路径")
	}
	return filepath.Join(filepath.Dir(exe), name), nil
}

// fallbackLockPath 返回 exe 目录不可写时的**回落**锁路径：
// `<用户缓存目录>/chonkpilot/<同名锁>`。用户缓存目录 = `%LOCALAPPDATA%`（与
// `uds.DefaultPath` 同口径：无 `%LOCALAPPDATA%` → 系统临时目录），保证目录 ACL 限当前用户。
func fallbackLockPath(user string) string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		base = os.TempDir() // 非 Windows / 无 LOCALAPPDATA 的兜底（不落系统共享目录）
	}
	return filepath.Join(base, "chonkpilot", lockNameFor(user))
}

// Acquire 尝试取得**指定用户**的 server 进程单例（文件锁）。返回：
//   - duplicate=true → **该用户已有 server 在运行**（本进程应退出；**未持有**锁）；
//   - 否则（且 err==nil）→ release 非 nil（进程退出时释放；调用方应 defer 调用）；
//   - 锁设施故障（**非** ErrBusy，如 exe 目录只读 → MkdirAll/OpenFile 失败）→ 先回落
//     用户缓存目录同名锁（fallbackLockPath）；回落亦失败 → **仅告警并按「非单例」继续**
//     （release = no-op，err = nil，**不致命**）——避免只读 exe 目录令**首个实例也无法启动**
//     （R9-1-1：fail-open；ErrBusy 仍判 duplicate，故「同一用户已有实例」的互斥不受影响）。
//
// 非 Windows 平台不提供文件锁 → 视为恒为首实例（server 仅发行于 Windows）。
// user = `osuser.CurrentUser()`（单例按用户隔离，见包注释）。
func Acquire(user string) (release func(), duplicate bool, err error) {
	path, warn := LockPath(user)
	if warn != nil {
		log.Printf("[procsingleton] 定位 exe 目录失败，单例锁回落临时目录 %s: %v", path, warn)
	}
	l, err := lockfile.Acquire(path)
	switch {
	case errors.Is(err, lockfile.ErrBusy):
		return nil, true, nil
	case errors.Is(err, lockfile.ErrUnsupported):
		return func() {}, false, nil
	case err != nil:
		// R9-1-1：非 ErrBusy 的设施故障（如 exe 目录只读）→ 回落用户缓存目录同名锁；
		// 回落成败均不致命（fail-open），避免首个实例被拒之门外。
		fb := fallbackLockPath(user)
		if fb == path {
			log.Printf("[procsingleton] 单例锁不可用（%v），按非单例继续", err)
			return func() {}, false, nil
		}
		l2, err2 := lockfile.Acquire(fb)
		switch {
		case errors.Is(err2, lockfile.ErrBusy):
			return nil, true, nil
		case err2 == nil:
			log.Printf("[procsingleton] 单例锁 %s 不可用（%v），回落 %s", path, err, fb)
			return l2.Release, false, nil
		default:
			log.Printf("[procsingleton] 单例锁回落 %s 亦失败（%v），按非单例继续", fb, err2)
			return func() {}, false, nil
		}
	}
	return l.Release, false, nil
}
