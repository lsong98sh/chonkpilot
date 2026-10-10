//go:build !windows

package flash

import "os/exec"

// launchDetached 是非 Windows 平台的默认拉起实现（无控制台窗口问题）。
func launchDetached(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// 不托管子进程（server 独立常驻）：释放句柄，免 Wait/finalizer 延迟回收。
	return cmd.Process.Release()
}
