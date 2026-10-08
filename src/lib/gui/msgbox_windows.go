//go:build windows

package gui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	msgboxUser32    = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = msgboxUser32.NewProc("MessageBoxW")
)

// nativeAlert 弹出原生消息框（阻塞至用户确认）。
// 用途 = **启动期致命错误的明确提示**：`chonkpilot.exe` 以 `-H windowsgui` 构建 → stderr
// 不可见，且启动早期（prj 库打开前）尚未挂上文件日志 sink（见 Main 的调用点），
// 消息框是唯一可见通道。
func nativeAlert(title, text string) {
	const mbOKIconError = 0x00000010 // MB_OK | MB_ICONERROR
	t, errT := windows.UTF16PtrFromString(title)
	c, errC := windows.UTF16PtrFromString(text)
	if errT != nil || errC != nil {
		return
	}
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), mbOKIconError)
}
