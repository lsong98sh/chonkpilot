//go:build !debugport

package gui

// DebugPortEnabled 指示本次构建是否启用 `--test-port` 测试通道（`-tags debugport`）。
//
// release 构建（默认，无 tag）：false —— 宿主**不注册** `--test-port`（传入即 unknown flag），
// 且 `testserver.go` 不参与编译（改由 testserver_off.go 提供空实现兜底编译）。
const DebugPortEnabled = false
