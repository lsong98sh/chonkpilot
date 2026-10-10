//go:build windows

package flash

import (
	"os/exec"
	"syscall"
)

// createNoWindow = CREATE_NO_WINDOW（0x08000000）：不为子进程创建控制台窗口。
const createNoWindow = 0x08000000

// launchDetached 在 Windows 上以「无控制台窗口、独立进程」方式拉起服务端：
// flash 为 windowsgui 宿主（无控制台），若按默认方式拉起 console 版 server 会弹出黑窗，
// 故显式 CREATE_NO_WINDOW + HideWindow。server 以 --autoexit 自行收尾，无需父进程托管。
func launchDetached(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd.Start()
}
