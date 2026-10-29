//go:build windows

package host

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestProcessDPIAware 验证进程已声明 Per-Monitor DPI 感知（PROCESS_PER_MONITOR_DPI_AWARE=2）。
// 依据：host 包 init 在测试二进制启动时已调用 SetProcessDpiAwarenessContext(V2)。
func TestProcessDPIAware(t *testing.T) {
	shcore := windows.NewLazySystemDLL("shcore.dll")
	procGetProcessDpiAwareness := shcore.NewProc("GetProcessDpiAwareness")
	var v uint32
	hr, _, err := procGetProcessDpiAwareness.Call(0, uintptr(unsafe.Pointer(&v)))
	if hr != 0 {
		t.Skipf("GetProcessDpiAwareness 不可用: %v", err)
	}
	if v != 2 { // 0=unaware, 1=system, 2=per-monitor
		t.Fatalf("进程 DPI 感知 = %d，期望 2（PER_MONITOR）", v)
	}
}
