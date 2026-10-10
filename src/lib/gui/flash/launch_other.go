//go:build !windows

package flash

import "os/exec"

// launchDetached 是非 Windows 平台的默认拉起实现（无控制台窗口问题）。
func launchDetached(exe string, args []string) error {
	return exec.Command(exe, args...).Start()
}
