module github.com/chonkpilot/chonkpilot-test/chonkpilot-plugin-memory/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-memory v0.0.0
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3
)

require (
	github.com/chonkpilot/chonkpilot-workspace v0.0.0-00010101000000-000000000000 // indirect
	github.com/lsong98sh/chonkpilot-util v0.0.0 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-data => ../../../lib/data

replace github.com/chonkpilot/chonkpilot-plugin-memory => ../../../../../chonkpilot-plugins/memory/src/lib

// TURNRUN-1 批次 2：契约类型仓 + 两个独立仓（本地克隆与主仓同级）——开发期本地 replace。

// 开发期依赖策略（2026-10-29）：同机跨仓依赖一律本地 replace；正式发版时统一切回版本号并删除本节。
replace github.com/lsong98sh/chonkpilot-plugin-framework => ../../../../../chonkpilot-framework/plugin/src

replace github.com/lsong98sh/chonkpilot-util => ../../../../../chonkpilot-util

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../../../chonkpilot-framework/host/src

replace github.com/chonkpilot/chonkpilot-workspace => ../../../lib/workspace

require github.com/lsong98sh/chonkpilot-framework/host v0.0.0

replace github.com/lsong98sh/chonkpilot-agent => ../../../../../chonkpilot-agent

replace github.com/lsong98sh/chonkpilot-agent/chat => ../../../../../chonkpilot-agent/chat/src

replace github.com/lsong98sh/chonkpilot-agent/gateway => ../../../../../chonkpilot-agent/gateway/src

replace github.com/lsong98sh/chonkpilot-agent/router => ../../../../../chonkpilot-agent/router/src

replace github.com/lsong98sh/chonkpilot-agent/session => ../../../../../chonkpilot-agent/session/src

replace github.com/lsong98sh/chonkpilot-agent/types => ../../../../../chonkpilot-agent/types/src

require (
	github.com/lsong98sh/chonkpilot-agent/session v0.0.0-00010101000000-000000000000 // indirect
	github.com/lsong98sh/chonkpilot-agent/types v0.0.0 // indirect
)
