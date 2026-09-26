//go:build windows

// chonkpilot-desktop（桌面单体，产物 `chonkpilot.exe`）：webview2 宿主外壳。
//
// 本壳只做两件事 —— **嵌入前端 dist**（embed 落点 = `src/desktop/frontend/dist`，由
// build-desktop.ps1 投放）与**声明形态** `gui.FormDesktop`；宿主实现（窗口/桥/内嵌
// server→persist→filesys/gateway/插件装配）**唯一一份**在 `chonkpilot-gui`
// （`src/lib/gui`，方案 B：零重复）。CLI 单体见 `cli/`。
//
// 形态不再由编译开关（`-tags split`）决定宿主分支：桌面单体 = 本壳（无 tag 构建），
// GUI 客户端 = `src/gui`（`-tags split` 构建）。`-tags split` 对其它组件（instance 心跳 /
// llm·persist·plugin-* 的 `sweep_split.go`）的作用**保留不变**。
package main

import (
	"embed"
	"io/fs"

	"github.com/chonkpilot/chonkpilot-gui"
)

//go:embed all:frontend/dist
var distFS embed.FS

func main() {
	gui.Main(distFS, gui.Options{Form: gui.FormDesktop})
}

// 编译期断言：embed 产物满足宿主入口要求的文件系统形态（fs.FS）。
var _ fs.FS = distFS
