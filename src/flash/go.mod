module github.com/chonkpilot/chonkpilot-flash

go 1.26.0

require github.com/chonkpilot/chonkpilot-gui v0.0.0

require (
	github.com/chonkpilot/chonkpilot-ignore v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-llm v0.0.0 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-lib v0.0.0 // indirect
	github.com/jchv/go-webview2 v0.0.0 // indirect
)

replace (
	github.com/chonkpilot/chonkpilot-assembly => ../lib/assembly
	github.com/chonkpilot/chonkpilot-chat => ../lib/chat
	github.com/chonkpilot/chonkpilot-data => ../lib/data
	github.com/chonkpilot/chonkpilot-filesys => ../lib/filesys
	github.com/chonkpilot/chonkpilot-gui => ../lib/gui
	github.com/chonkpilot/chonkpilot-ignore => ../lib/ignore
	github.com/chonkpilot/chonkpilot-lib => ../lib/core
	github.com/chonkpilot/chonkpilot-llm => ../lib/llm
	github.com/chonkpilot/chonkpilot-mcp-gateway => ../lib/gateway
	github.com/chonkpilot/chonkpilot-mcp-server => ../lib/mcp-server
	github.com/chonkpilot/chonkpilot-plugin => ../plugins/plugin
	github.com/chonkpilot/chonkpilot-plugin-codegraph => ../plugins/plugin-codegraph
	github.com/chonkpilot/chonkpilot-plugin-compress => ../plugins/plugin-compress
	github.com/chonkpilot/chonkpilot-plugin-history => ../plugins/plugin-history
	github.com/chonkpilot/chonkpilot-plugin-memory => ../plugins/plugin-memory
	github.com/chonkpilot/chonkpilot-plugin-vfts => ../plugins/plugin-vfts
	github.com/chonkpilot/chonkpilot-router => ../lib/router
	github.com/chonkpilot/chonkpilot-task => ../lib/task
	github.com/jchv/go-webview2 => ../lib/go-webview2
)

replace github.com/chonkpilot/chonkpilot-mcp-tools => ../lib/mcp-tools
