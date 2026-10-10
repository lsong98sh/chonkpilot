package flash

// 窗口单例的**具名窗口类名**（🆕 2026-10-10；19 §8.11 / 62 §3A）。
//
// 口径（用户明确要求「单例使用 wndclass 判断」）：flash 以 Windows **具名窗口类**承担
// **窗口单例** —— 同名类 = 同用户 + 同项目；同用户同项目重复启动 → 命中既有窗口 →
// 置顶既有主窗口、不新开。
//
// 职责边界（与 per-user work-dir 锁并存，两者互不替代）：
//   - **work-dir 锁**（`lockfile`，per-user）= 「**项目是否已被打开**」——防两个实例同时
//     打开同一项目（数据面独占）；
//   - **具名窗口类**（本文件 + `src/lib/gui/wndclass_windows.go`）= 「**窗口单例**」——
//     防同用户同项目出现两个主窗口（UI 面独占），且**不依赖 server 可达**。
//
// 类名由 (user, workDir) 稳定哈希得（Windows 路径大小写不敏感 → 统一小写 + Clean）。

import (
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
)

// windowClassPrefix 是 flash 窗口类名前缀（与 server 托盘类 `ChonkPilotTrayWnd` 区分）。
const windowClassPrefix = "ChonkPilotFlash_"

// WindowClassName 返回「同用户 + 同项目」的稳定窗口类名。
// 同一 (user, workDir) 在不同进程/不同次启动 → 同名（供 FindWindow 跨进程判重）。
func WindowClassName(user, workDir string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(user))))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.ToLower(filepath.Clean(workDir))))
	return windowClassPrefix + strconv.FormatUint(h.Sum64(), 16)
}
