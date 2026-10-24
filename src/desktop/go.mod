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
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.etcd.io/bbolt v1.4.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/chonkpilot/chonkpilot-appwire => ../lib/appwire
	github.com/chonkpilot/chonkpilot-data => ../lib/data
	github.com/chonkpilot/chonkpilot-filesys => ../lib/filesys
	github.com/chonkpilot/chonkpilot-gui => ../lib/gui
	github.com/chonkpilot/chonkpilot-gwadapter => ../lib/gwadapter
	github.com/chonkpilot/chonkpilot-ignore => ../lib/ignore
	github.com/chonkpilot/chonkpilot-lib => ../lib/core
	github.com/chonkpilot/chonkpilot-llm => ../lib/llm
	github.com/chonkpilot/chonkpilot-mcp-server => ../lib/mcp-server
	github.com/chonkpilot/chonkpilot-plugin => ../plugins/plugin
	github.com/chonkpilot/chonkpilot-plugin-codegraph => ../plugins/plugin-codegraph
	github.com/chonkpilot/chonkpilot-plugin-compress => ../plugins/plugin-compress
	github.com/chonkpilot/chonkpilot-plugin-history => ../plugins/plugin-history
	github.com/chonkpilot/chonkpilot-plugin-memory => ../plugins/plugin-memory
	github.com/chonkpilot/chonkpilot-plugin-vfts => ../plugins/plugin-vfts
	github.com/chonkpilot/chonkpilot-task => ../lib/task
	github.com/jchv/go-webview2 => ../lib/go-webview2
)

require (
	github.com/chonkpilot/chonkpilot-appwire v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-assembly v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-data v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-filesys v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-gwadapter v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-ignore v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-lib v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-llm v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-mcp-tools v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-plugin v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-plugin-codegraph v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-plugin-vfts v0.0.0 // indirect
	github.com/jchv/go-webview2 v0.0.0 // indirect
	github.com/lsong98sh/chonkpilot-chat v0.1.0 // indirect
	github.com/lsong98sh/chonkpilot-mcp-gateway v0.1.0 // indirect
	github.com/lsong98sh/chonkpilot-router v0.1.0 // indirect
	github.com/lsong98sh/chonkpilot-session v0.1.0 // indirect
	github.com/sgtdi/fswatcher v1.3.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-assembly => ../lib/assembly

replace github.com/chonkpilot/chonkpilot-mcp-tools => ../lib/mcp-tools
