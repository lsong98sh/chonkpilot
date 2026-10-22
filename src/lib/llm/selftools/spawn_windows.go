//go:build windows

package selftools

import "syscall"

// sysProcAttr 隐藏子进程控制台窗口：宿主是 windowsgui 子系统，创建 console 子系统子进程时
// 不加 CREATE_NO_WINDOW 会闪出黑框。
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
