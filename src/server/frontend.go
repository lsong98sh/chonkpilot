// 静态页内嵌面（browser 形态前端 go:embed 进 chonkpilot-server.exe）。
//
// 现状（2026-09-21 用户拍板）：browser 形态静态页 = `src/frontend` 的 browser 产物，
// 由 `build-browser.ps1` 投放到本包目录下 `frontend/dist`（vite outDir 与该 embed 落点
// 对齐，见 src/frontend/vite.config.js），再由本文件 go:embed 进服务端 exe——
// 故 `chonkpilot-server.exe` **自带静态面**，不再依赖外部 `--web-root` 目录。
//
// `--web-root` **保留为覆盖**：显式指定（非空）时才读外部目录（见 httpapi.Options.WebRoot
// 与 readStatic 的优先级）；未指定（空）= 用本内嵌面。
//
// 约定：`frontend/dist` 必须**至少有占位文件**（`all:` 前缀的 embed 模式要求目录非空，
// 否则编译期报 "no matching files found"）——占了 .keep，vite 构建时会被 emptyOutDir 清掉
// 并写入真实产物，两者皆满足该约束。
package main

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var embeddedWeb embed.FS

// webFS 返回内嵌静态面根（= frontend/dist 子树；路径相对该子树，如 index.html）。
// 子树不可用（不应发生）→ nil，调用方按"静态面未配置"处理（503）。
func webFS() fs.FS {
	sub, err := fs.Sub(embeddedWeb, "frontend/dist")
	if err != nil {
		return nil
	}
	return sub
}
