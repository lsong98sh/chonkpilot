// Package contracts 承载工具契约（*.tool.md）的嵌入资源。
//
// 单一数据源：md 即工具定义（mcp-server 部署到 dist/capability 后运行时扫描），
// executor exe 通过 --help 展示同一份内容（go:embed 编译期嵌入，零外部依赖）。
// 目录结构对齐部署布局：tools/{core,desktop}/*.tool.md。
package contracts

import "embed"

// FS 是嵌入的契约文件系统（路径形如 tools/core/file_read.tool.md）。
//
//go:embed tools
var FS embed.FS
