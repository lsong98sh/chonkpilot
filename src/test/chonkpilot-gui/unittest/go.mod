module github.com/chonkpilot/chonkpilot-test/chonkpilot-gui/unittest

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-gui v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/chonkpilot/chonkpilot-plugin v0.0.0
)

require (
	github.com/chonkpilot/chonkpilot-ignore v0.0.0 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-chat => ../../../../../chonkpilot-chat

replace github.com/chonkpilot/chonkpilot-data => ../../../lib/data

replace github.com/chonkpilot/chonkpilot-filesys => ../../../lib/filesys

replace github.com/chonkpilot/chonkpilot-gui => ../../../lib/gui

replace github.com/chonkpilot/chonkpilot-ignore => ../../../lib/ignore

replace github.com/chonkpilot/chonkpilot-lib => ../../../lib/core

replace github.com/chonkpilot/chonkpilot-llm => ../../../lib/llm

replace github.com/chonkpilot/chonkpilot-gwadapter => ../../../lib/gwadapter

replace github.com/chonkpilot/chonkpilot-mcp-gateway => ../../../../../chonkpilot-mcp-gateway

replace github.com/chonkpilot/chonkpilot-mcp-server => ../../../lib/mcp-server

replace github.com/chonkpilot/chonkpilot-plugin => ../../../plugins/plugin

replace github.com/chonkpilot/chonkpilot-plugin-compress => ../../../plugins/plugin-compress

replace github.com/chonkpilot/chonkpilot-plugin-history => ../../../plugins/plugin-history

replace github.com/chonkpilot/chonkpilot-task => ../../../lib/task

replace github.com/jchv/go-webview2 => ../../../lib/go-webview2

replace github.com/chonkpilot/chonkpilot-assembly => ../../../lib/assembly

replace github.com/chonkpilot/chonkpilot-mcp-tools => ../../../lib/mcp-tools
