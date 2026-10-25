module github.com/chonkpilot/chonkpilot-appwire

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-data v0.0.0
	github.com/chonkpilot/chonkpilot-filesys v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/chonkpilot/chonkpilot-llm v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-codegraph v0.0.0
	github.com/chonkpilot/chonkpilot-plugin-vfts v0.0.0
	github.com/lsong98sh/chonkpilot-mcp-gateway v0.1.0
	github.com/lsong98sh/chonkpilot-session v0.1.0
)

require (
	github.com/chonkpilot/chonkpilot-mcp-server v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-task v0.0.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/kr/text v0.2.0 // indirect
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
	github.com/chonkpilot/chonkpilot-data => ../data
	github.com/chonkpilot/chonkpilot-filesys => ../filesys
	github.com/chonkpilot/chonkpilot-gwadapter => ../gwadapter
	github.com/chonkpilot/chonkpilot-ignore => ../../lib/ignore
	github.com/chonkpilot/chonkpilot-lib => ../core
	github.com/chonkpilot/chonkpilot-llm => ../llm
	github.com/chonkpilot/chonkpilot-mcp-server => ../mcp-server
	github.com/chonkpilot/chonkpilot-plugin-codegraph => ../../plugins/plugin-codegraph
	github.com/chonkpilot/chonkpilot-plugin-compress => ../../plugins/plugin-compress
	github.com/chonkpilot/chonkpilot-plugin-vfts => ../../plugins/plugin-vfts
	github.com/chonkpilot/chonkpilot-task => ../task
	github.com/jchv/go-webview2 => ../go-webview2
)

require (
	github.com/chonkpilot/chonkpilot-assembly v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-gwadapter v0.0.0 // indirect
	github.com/chonkpilot/chonkpilot-ignore v0.0.0 // indirect
	github.com/lsong98sh/chonkpilot-chat v0.1.0 // indirect
	github.com/lsong98sh/chonkpilot-router v0.1.0 // indirect
	github.com/sgtdi/fswatcher v1.3.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/chonkpilot/chonkpilot-assembly => ../assembly

replace github.com/chonkpilot/chonkpilot-mcp-tools => ../mcp-tools

require github.com/lsong98sh/chonkpilot-plugin-framework v0.1.2
