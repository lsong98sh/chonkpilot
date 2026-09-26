//go:build windows

// chonkpilot-gui-client（GUI 客户端，产物 `chonkpilot-gui-client.exe`）：webview2 宿主外壳。
//
// 本壳只做两件事 —— **嵌入前端 dist**（embed 落点 = `src/gui/frontend/dist`，由
// build-gui.ps1 投放）与**声明形态** `gui.FormGui`；宿主实现（窗口/桥/装配）**唯一一份**在
// `chonkpilot-gui`（`src/lib/gui`，方案 B：零重复）。服务端 = 独立 `chonkpilot-server.exe`
// （`src/server`，gui 与 browser 共用）。
//
// 形态不再由编译开关（`-tags split`）决定宿主分支：客户端形态 = 本壳（`-tags split` 构建），
// 桌面单体 = `src/desktop`（无 tag 构建）。本模块仍以 `-tags split` 构建（`-tags split` 对
// instance 心跳 / llm·persist·plugin-* 的 `sweep_split.go` 的作用**保留不变**）。
package main

import (
	"embed"
	"io/fs"

	"github.com/chonkpilot/chonkpilot-gui"
)

//go:embed all:frontend/dist
var distFS embed.FS

func main() {
	gui.Main(distFS, gui.Options{Form: gui.FormGui})
}

// 编译期断言：embed 产物满足宿主入口要求的文件系统形态（fs.FS）。
var _ fs.FS = distFS
