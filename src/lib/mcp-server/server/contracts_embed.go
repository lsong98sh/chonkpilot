// 内置契约（app 层）embed（GATEWAY-2 C，2026-10-21）。
//
// 构建期由 `build-mcp-server.ps1` / `build-desktop.ps1` / `build-gui.ps1` 从
// `src/initdata/capability/{tools,prompts,skills,resources}` 覆盖同步到本包 `contracts/` 目录
// （**该目录内容不入库**，仅 `.gitkeep` 占位，保证 clean clone 的 `//go:embed` 可编译）。
//
// 语义 = **磁盘缺失兜底**（见 contractsource.go）：部署产物的 capability 根存在 → 以磁盘为权威
// （不叠加 embed）；磁盘根缺失（exe 单独拷贝 / 源码直跑未同步）→ 用本 embed 提供 app 层契约，
// 避免工具面与资产面全空。executor 相对路径仍按部署根的同位目录解析。
package server

import (
	"embed"
	"io/fs"
)

//go:embed all:contracts
var builtinContracts embed.FS

// builtinContractFS 返回内置契约根（去掉 `contracts/` 前缀）；读取失败 → nil。
func builtinContractFS() fs.FS {
	sub, err := fs.Sub(builtinContracts, "contracts")
	if err != nil {
		return nil
	}
	return sub
}
