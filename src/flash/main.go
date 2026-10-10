//go:build windows

// chonkpilot-flash（desktop 分体常驻版**薄壳客户端**，产物 `chonkpilot-flash.exe`）：
// webview2 宿主外壳，复用 `chonkpilot-gui` 的窗口/桥能力（方案 B：零重复）。
//
// 与 `src/gui` / `src/desktop` 的唯一区别 = **远程总线模式**（`gui.RunRemote`）：
// 不启动内嵌 server/persist/filesys，数据与业务消息经 **UDS** 转发到常驻 `chonkpilot-server.exe`
// （见 19 §8.11 / 62 §3A）。
//
// **薄壳不 embed 前端**（R9）：前端 dist 由 build-gui.ps1 部署到 `<exeDir>/frontend/dist`，
// 本壳以 `os.DirFS(<exeDir>)` 交给 `RunRemote`（其内部 `fs.Sub("frontend/dist")`）——避免把
// 整个前端（mermaid/pdf/pptx 查看器等）编进薄壳 exe。
//
// 形态：复用 `desktop` 语义（免鉴权 + 前端不心跳；三态取值不含 flash），理由见
// `src/lib/gui/remote.go` 文件头与 62 §3A.2。
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-gui"
)

func main() {
	gui.RunRemote(frontendFS(), gui.RemoteOptions{})
}

// frontendFS 返回承载前端的**磁盘**文件系统根：
//   - `CHONK_FRONTEND_DIR` 环境变量（显式覆盖，供开发/测试）——其值 = **含 `frontend/dist`
//     子目录的根目录**（**非** dist 本身；RunRemote 内部再 `fs.Sub` 剥掉该前缀）；
//   - 否则 flash exe 同目录（约定其下有 `frontend/dist/`）；
//   - exe 路径不可解析时回落 cwd。
//
// RunRemote 内部再 `fs.Sub(fs, "frontend/dist")`；缺该子目录 → 启动报错并弹框（可诊断）。
func frontendFS() fs.FS {
	if d := strings.TrimSpace(os.Getenv("CHONK_FRONTEND_DIR")); d != "" {
		return os.DirFS(d)
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		return os.DirFS(filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		return os.DirFS(wd)
	}
	return os.DirFS(".")
}
