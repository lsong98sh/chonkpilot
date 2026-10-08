//go:build !windows

package bridge

import "syscall"

// newConsoleProcAttr 非 Windows 无「新建控制台窗口」概念，返回 nil（不设进程属性）。
func newConsoleProcAttr() *syscall.SysProcAttr { return nil }
