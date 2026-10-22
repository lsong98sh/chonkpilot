//go:build !windows

package selftools

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }
