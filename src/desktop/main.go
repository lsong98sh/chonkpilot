//go:build windows

// chonkpilot-desktop（桌面单体，产物 `chonkpilot.exe`）：webview2 宿主外壳。
//
// 本壳只做两件事 —— **嵌入前端 dist**（embed 落点 = `src/desktop/frontend/dist`，由
// build-desktop.ps1 投放）与**声明形态** `gui.FormDesktop`；单体装配宿主（窗口/桥/内嵌
// server→persist→filesys/gateway/插件装配）**唯一一份**在 `chonkpilot-gui/host`
// （`src/lib/gui/host`，方案 B：零重复）。
package main

import (
	"embed"
	"errors"
	"io/fs"
	"os"

	"github.com/chonkpilot/chonkpilot-gui/host"
	"github.com/lsong98sh/chonkpilot-framework/host/gui"
	"github.com/lsong98sh/chonkpilot-framework/host/singleinstance"
)

//go:embed all:frontend/dist
var distFS embed.FS

func main() {
	// TOPO-1 §2.22.6（2026-10-29）：启动第一件事 = **实例 UDS 独占 listen** 判单例
	// （替代已删除的命名互斥体 procsingleton）。作用域 = 数据根（`<work-dir>/.chonkpilot`）：
	// 同一数据根只允许一份进程（保护 bbolt 库不被双开），不同数据根可并存。
	// framework 只返回 typed error；**弹框与退出由本壳做**。
	root := host.DesktopDataRoot()
	inst, err := singleinstance.Acquire(root)
	switch {
	case errors.Is(err, singleinstance.ErrAlreadyRunning):
		gui.NativeAlert("ChonkPilot", "已有实例在运行（数据根 "+root+"）")
		os.Exit(0)
	case err != nil:
		gui.NativeAlert("ChonkPilot", err.Error())
		os.Exit(1)
	}
	defer func() { _ = inst.Close() }()

	host.Main(distFS, gui.Options{Form: gui.FormDesktop})
}

// 编译期断言：embed 产物满足宿主入口要求的文件系统形态（fs.FS）。
var _ fs.FS = distFS
