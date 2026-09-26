//go:build !windows

package bridge

// lookupAppPath 非 Windows 平台无 App Paths 注册表，直接返回空。
func lookupAppPath(exe string) string { return "" }
