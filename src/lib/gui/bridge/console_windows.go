//go:build windows

package bridge

import "syscall"

// createNewConsole 为 Windows CREATE_NEW_CONSOLE（0x00000010）：为子进程分配一个**新的
// 控制台窗口**。此处与 winproc 的 CREATE_NO_WINDOW 恰好相反——`gui.console.open` 需要
// 在系统控制台（cmd 新窗口）打开目标目录。
const createNewConsole = 0x00000010

// newConsoleProcAttr 返回「新建控制台窗口」的进程创建属性。
func newConsoleProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewConsole}
}
