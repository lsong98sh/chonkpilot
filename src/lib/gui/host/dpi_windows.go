//go:build windows

package host

import (
	"golang.org/x/sys/windows"
)

// ── 进程 DPI 感知（2026-10-26）──────────────────────────────────────────────
// 背景：仓库虽有 `src/lib/gui/app.manifest`（dpiAware=true/pm + dpiAwareness=PerMonitorV2），
// 但构建链**没有把它嵌进 exe**（无 .syso / 无 windres 步骤）→ 进程实际是 DPI-unaware：
//   - Windows 对整个窗口做位图拉伸（150% 缩放下物理/逻辑坐标错位，真机拖拽 140px 实测变 225px）；
//   - WebView2 也要求宿主 Per-Monitor V2，否则 WebView 内容与窗口坐标换算会漂移。
//
// 这里在包 init（早于任何窗口创建）调用 Win32 API 显式声明：
//   ① SetProcessDpiAwarenessContext(PER_MONITOR_AWARE_V2)（Win10 1703+，首选）；
//   ② 回退 SetProcessDPIAware()（旧系统 / API 缺失）。
// 失败（已被清单或父进程设置过）一律静默忽略——重复设置会返回 ERROR_ACCESS_DENIED，不是错误。
//
// 说明：运行期 API 与清单声明等价（都改进程默认 DPI 感知）；清单仍保留在仓库，供后续打包
// 资源化（.syso）时使用。

var (
	user32DLL                         = windows.NewLazySystemDLL("user32.dll")
	procSetProcessDpiAwarenessContext = user32DLL.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32DLL.NewProc("SetProcessDPIAware")
)

// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (DPI_AWARENESS_CONTEXT)-4。
const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)

func init() {
	ensureDPIAwareness()
}

// ensureDPIAwareness 声明进程 DPI 感知（Per-Monitor V2 → 旧系统回退 Per-Monitor/System）。
func ensureDPIAwareness() {
	if r, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2); r != 0 {
		return
	}
	_, _, _ = procSetProcessDPIAware.Call()
}
