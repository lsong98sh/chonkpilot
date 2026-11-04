//go:build !windows

// D-66 / ERR-002：非 Windows 平台不做系统网络状态检测（恒 true，保持既有重试行为）。
package server

// systemNetworkConnected 在非 Windows 平台恒返回 true（不阻断正常重试）。
var systemNetworkConnected = func() bool { return true }
