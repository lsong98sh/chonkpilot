// chonkpilot-gwadapter 是 gateway 的**宿主适配胶水层**（2026-10-21 从 gateway module 拆出）：
//   - mqadapter：gateway 端口（Transport / Hooks）的 mq 实现——主题名在此决定
//   - facadeapi / internal/facade：mq ↔ mcp 门面
//
// 为什么独立：gateway 内核（gateway/ + sandbox/ + winproc/）须零本仓依赖以便独立发布；
// 而胶水层必须依赖 core 的 mq / msgkeys，故与内核分属不同 module。
module github.com/chonkpilot/chonkpilot-gwadapter

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/lsong98sh/chonkpilot-mcp-gateway v0.1.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.1 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-lib => ../core
