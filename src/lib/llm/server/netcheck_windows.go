//go:build windows

// D-66 / ERR-002：LLM 重试前的**系统网络状态**检测（Windows）。
//
// 判据 = 本机是否已连接网络（wininet `InternetGetConnectedState`），**不探测目标端点**：
// 端点不可达但本机在线（服务 5xx / 端口未监听）仍按既有规则重试；本机断网 →
// router 在重试前直接判 `network` 终态，不再按退避预算空等（断网首错 <15s）。
package server

import (
	"syscall"
	"unsafe"
)

var (
	wininetDLL                    = syscall.NewLazyDLL("wininet.dll")
	procInternetGetConnectedState = wininetDLL.NewProc("InternetGetConnectedState")
)

// systemNetworkConnected 返回**系统网络连接状态**（var 便于单测替换为断网）。
// 调用失败 / 平台异常 → true（保守：不因检测失败阻断正常重试）。
var systemNetworkConnected = func() bool {
	var flags uint32
	r, _, _ := procInternetGetConnectedState.Call(uintptr(unsafe.Pointer(&flags)), 0)
	return r != 0
}
