module github.com/chonkpilot/chonkpilot-assembly

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-gwadapter v0.0.0
	github.com/chonkpilot/chonkpilot-mcp-server v0.0.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/kr/text v0.1.0 // indirect
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3 // indirect
	github.com/lsong98sh/chonkpilot-util v0.0.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)


replace github.com/chonkpilot/chonkpilot-gwadapter => ../gwadapter

replace github.com/chonkpilot/chonkpilot-mcp-server => ../mcp-server

replace github.com/chonkpilot/chonkpilot-mcp-tools => ../mcp-tools

// 开发期依赖策略（2026-10-29）：同机跨仓依赖一律本地 replace；正式发版时统一切回版本号并删除本节。
replace github.com/lsong98sh/chonkpilot-plugin-framework => ../../../../chonkpilot-framework/plugin/src

replace github.com/lsong98sh/chonkpilot-util => ../../../../chonkpilot-util

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../../chonkpilot-framework/host/src

replace github.com/chonkpilot/chonkpilot-workspace => ../workspace

require github.com/lsong98sh/chonkpilot-framework/host v0.0.0

replace github.com/lsong98sh/chonkpilot-agent => ../../../../chonkpilot-agent

require github.com/lsong98sh/chonkpilot-agent v0.0.0
