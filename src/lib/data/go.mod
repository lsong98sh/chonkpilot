module github.com/chonkpilot/chonkpilot-data

go 1.26.0

require (
	github.com/lsong98sh/chonkpilot-util v0.0.0
	go.etcd.io/bbolt v1.4.2
)

require (
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// 开发期依赖策略（2026-10-29）：同机跨仓依赖一律本地 replace；正式发版时统一切回版本号并删除本节。
replace github.com/lsong98sh/chonkpilot-plugin-framework => ../../../../chonkpilot-framework/plugin/src

replace github.com/lsong98sh/chonkpilot-util => ../../../../chonkpilot-util

// TURNRUN-1 批次 2：契约类型仓 + 两个独立仓（本地克隆与主仓同级）——开发期本地 replace；
// 三仓发布新版本后删除本节。

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../../chonkpilot-framework/host/src

replace github.com/chonkpilot/chonkpilot-workspace => ../workspace

require (
	github.com/chonkpilot/chonkpilot-workspace v0.0.0-00010101000000-000000000000
	github.com/lsong98sh/chonkpilot-framework/host v0.0.0
)

replace github.com/lsong98sh/chonkpilot-agent => ../../../../chonkpilot-agent

require github.com/lsong98sh/chonkpilot-agent v0.0.0
