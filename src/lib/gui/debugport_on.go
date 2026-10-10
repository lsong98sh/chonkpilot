//go:build debugport

package gui

// DebugPortEnabled 指示本次构建是否启用 `--test-port` 测试通道（`-tags debugport`）。
//
// debug 构建（-tags debugport）：宿主注册 `--test-port` 并编入 `testserver.go` 全功能。
// release 构建（默认，无 tag）：不注册 `--test-port`、不打开任何监听端口（见 debugport_off.go）。
const DebugPortEnabled = true
