module github.com/chonkpilot/chonkpilot-mcp-tools

go 1.26.0

require (
	github.com/lsong98sh/chonkpilot-util v0.0.0
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3
	golang.org/x/sys v0.47.0
	golang.org/x/text v0.22.0
)


replace github.com/lsong98sh/chonkpilot-util => ../../../../chonkpilot-util

replace github.com/jchv/go-webview2 => ../../../../chonkpilot-webview2

// 开发期依赖策略（2026-10-29）：同机跨仓依赖一律本地 replace；正式发版时统一切回版本号并删除本节。
replace github.com/lsong98sh/chonkpilot-plugin-framework => ../../../../chonkpilot-framework/plugin/src

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../../chonkpilot-framework/host/src

replace github.com/chonkpilot/chonkpilot-workspace => ../workspace

require github.com/lsong98sh/chonkpilot-framework/host v0.0.0

replace github.com/lsong98sh/chonkpilot-agent => ../../../../chonkpilot-agent

require github.com/lsong98sh/chonkpilot-agent v0.0.0
