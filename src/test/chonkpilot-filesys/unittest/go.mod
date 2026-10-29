module github.com/chonkpilot/chonkpilot-test/chonkpilot-filesys/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-filesys v0.0.0
	github.com/chonkpilot/chonkpilot-workspace v0.0.0
)

require (
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3 // indirect
	github.com/lsong98sh/chonkpilot-util v0.0.0 // indirect
	github.com/sgtdi/fswatcher v1.3.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-filesys => ../../../lib/filesys


replace github.com/chonkpilot/chonkpilot-workspace => ../../../lib/workspace

// 开发期依赖策略（2026-10-29）：同机跨仓依赖一律本地 replace；正式发版时统一切回版本号并删除本节。
replace github.com/lsong98sh/chonkpilot-plugin-framework => ../../../../../chonkpilot-framework/plugin/src

replace github.com/lsong98sh/chonkpilot-util => ../../../../../chonkpilot-util

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../../../chonkpilot-framework/host/src

require github.com/lsong98sh/chonkpilot-framework/host v0.0.0

replace github.com/lsong98sh/chonkpilot-agent => ../../../../../chonkpilot-agent

require github.com/lsong98sh/chonkpilot-agent v0.0.0
