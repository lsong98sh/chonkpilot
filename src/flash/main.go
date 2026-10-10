//go:build windows

// chonkpilot-flash（desktop 分体常驻版**薄壳客户端**，产物 `chonkpilot-flash.exe`）：
// webview2 宿主外壳，复用 `chonkpilot-lib/gui` 的窗口/桥/前端 embed（方案 B：零重复）。
//
// 与 `src/gui` / `src/desktop` 的唯一区别 = **远程总线模式**（`gui.RunRemote`）：
// 不启动内嵌 server/persist/filesys，数据与业务消息经 **UDS** 转发到常驻 `chonkpilot-server.exe`
// （见 19 §8.11 / 62 §3A）。本壳只做两件事 —— 嵌入前端 dist + 调用远程宿主入口。
//
// 形态：复用 `desktop` 语义（免鉴权 + 前端不心跳；三态取值不含 flash），理由见
// `src/lib/gui/remote.go` 文件头与 62 §3A.2。
package main

import (
	"embed"
	"io/fs"

	"github.com/chonkpilot/chonkpilot-gui"
)

//go:embed all:frontend/dist
var distFS embed.FS

func main() {
	gui.RunRemote(distFS, gui.RemoteOptions{})
}

// 编译期断言：embed 产物满足宿主入口要求的文件系统形态（fs.FS）。
var _ fs.FS = distFS
