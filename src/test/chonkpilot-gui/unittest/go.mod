module github.com/chonkpilot/chonkpilot-test/chonkpilot-gui/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-gui v0.0.0
)

require (
	github.com/chonkpilot/chonkpilot-workspace v0.0.0 // indirect
	github.com/jchv/go-webview2 v0.0.0 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/lsong98sh/chonkpilot-util v0.0.0 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-data => ../../../lib/data

replace github.com/chonkpilot/chonkpilot-filesys => ../../../lib/filesys

replace github.com/chonkpilot/chonkpilot-gui => ../../../lib/gui

replace github.com/chonkpilot/chonkpilot-llm => ../../../lib/llm

replace github.com/chonkpilot/chonkpilot-gwadapter => ../../../lib/gwadapter

replace github.com/chonkpilot/chonkpilot-mcp-server => ../../../lib/mcp-server

replace github.com/chonkpilot/chonkpilot-task => ../../../lib/task

replace github.com/jchv/go-webview2 => ../../../../../chonkpilot-webview2

replace github.com/chonkpilot/chonkpilot-assembly => ../../../lib/assembly

replace github.com/chonkpilot/chonkpilot-appwire => ../../../lib/appwire

replace github.com/chonkpilot/chonkpilot-mcp-tools => ../../../lib/mcp-tools

require github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3

// TURNRUN-1 批次 2：契约类型仓 + 两个独立仓（本地克隆与主仓同级）——开发期本地 replace。

// 开发期依赖策略（2026-10-29）：同机跨仓依赖一律本地 replace；正式发版时统一切回版本号并删除本节。
replace (
	github.com/lsong98sh/chonkpilot-plugin-framework => ../../../../../chonkpilot-framework/plugin/src
	github.com/lsong98sh/chonkpilot-util => ../../../../../chonkpilot-util
)

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../../../chonkpilot-framework/host/src

replace github.com/chonkpilot/chonkpilot-workspace => ../../../lib/workspace

require github.com/lsong98sh/chonkpilot-framework/host v0.0.0

replace github.com/lsong98sh/chonkpilot-agent => ../../../../../chonkpilot-agent

require github.com/lsong98sh/chonkpilot-agent v0.0.0 // indirect
