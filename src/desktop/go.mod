module github.com/chonkpilot/chonkpilot-desktop

go 1.26.0

require github.com/chonkpilot/chonkpilot-gui v0.0.0

require (
	github.com/chonkpilot/chonkpilot-mcp-server v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-task v0.0.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/modelcontextprotocol/go-sdk v1.7.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/chonkpilot/chonkpilot-appwire => ../lib/appwire
	github.com/chonkpilot/chonkpilot-data => ../lib/data
	github.com/chonkpilot/chonkpilot-filesys => ../lib/filesys
	github.com/chonkpilot/chonkpilot-gui => ../lib/gui
	github.com/chonkpilot/chonkpilot-gwadapter => ../lib/gwadapter
	github.com/chonkpilot/chonkpilot-llm => ../lib/llm
	github.com/chonkpilot/chonkpilot-mcp-server => ../lib/mcp-server
	github.com/chonkpilot/chonkpilot-task => ../lib/task
	github.com/jchv/go-webview2 => ../../../chonkpilot-webview2
)

require (
	github.com/chonkpilot/chonkpilot-appwire v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-assembly v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-data v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-filesys v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-gwadapter v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-llm v0.0.0 // indirect
	github.com/jchv/go-webview2 v0.0.0 // indirect
	github.com/sgtdi/fswatcher v1.3.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-assembly => ../lib/assembly


require (
	github.com/chonkpilot/chonkpilot-workspace v0.0.0 // indirect
	github.com/lsong98sh/chonkpilot-agent/chat v0.0.0-00010101000000-000000000000 // indirect
	github.com/lsong98sh/chonkpilot-agent/gateway v0.0.0-00010101000000-000000000000 // indirect
	github.com/lsong98sh/chonkpilot-agent/router v0.0.0-00010101000000-000000000000 // indirect
	github.com/lsong98sh/chonkpilot-agent/session v0.0.0-00010101000000-000000000000 // indirect
	github.com/lsong98sh/chonkpilot-agent/types v0.0.0 // indirect
	github.com/lsong98sh/chonkpilot-plugin-framework v0.1.3 // indirect
	github.com/lsong98sh/chonkpilot-util v0.0.0 // indirect
)

// TURNRUN-1 批次 2：契约类型仓 + 两个独立仓（本地克隆与主仓同级，E:\BizWorks\chonkpilot-*）
// —— 开发期本地 replace，与 go-webview2 同口径；三仓发布新版本后删除本节。
// L6 动态池（2026-10-28）：gateway 内核同款本地 replace（发布新版本后删除该行）。

// 开发期依赖策略（2026-10-29）：同机跨仓依赖一律本地 replace；正式发版时统一切回版本号并删除本节。
replace (
	github.com/lsong98sh/chonkpilot-plugin-framework => ../../../chonkpilot-framework/plugin/src
	github.com/lsong98sh/chonkpilot-util => ../../../chonkpilot-util
)

// TOPO-1锛?026-10-29锛夛細骞冲彴鍩哄骇锛坈ore + 绐楀彛灞傦級宸茶縼鑷?framework/host銆俙r
replace github.com/lsong98sh/chonkpilot-framework/host => ../../../chonkpilot-framework/host/src

replace github.com/chonkpilot/chonkpilot-workspace => ../lib/workspace

require github.com/lsong98sh/chonkpilot-framework/host v0.0.0

replace github.com/lsong98sh/chonkpilot-agent => ../../../chonkpilot-agent

replace github.com/lsong98sh/chonkpilot-agent/chat => ../../../chonkpilot-agent/chat/src

replace github.com/lsong98sh/chonkpilot-agent/gateway => ../../../chonkpilot-agent/gateway/src

replace github.com/lsong98sh/chonkpilot-agent/router => ../../../chonkpilot-agent/router/src

replace github.com/lsong98sh/chonkpilot-agent/session => ../../../chonkpilot-agent/session/src

replace github.com/lsong98sh/chonkpilot-agent/types => ../../../chonkpilot-agent/types/src

require github.com/lsong98sh/chonkpilot-agent v0.0.0 // indirect
