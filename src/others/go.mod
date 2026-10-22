module github.com/chonkpilot/chonkpilot-others

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-gwadapter v0.0.0
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/chonkpilot/chonkpilot-mcp-server v0.0.0
	github.com/lsong98sh/chonkpilot-mcp-gateway v0.1.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
)

require (
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	golang.org/x/text v0.22.0 // indirect
)

require (
	github.com/chonkpilot/chonkpilot-mcp-tools v0.0.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/chonkpilot/chonkpilot-gwadapter => ../lib/gwadapter
	github.com/chonkpilot/chonkpilot-lib => ../lib/core
	github.com/chonkpilot/chonkpilot-mcp-server => ../lib/mcp-server
)

replace github.com/chonkpilot/chonkpilot-mcp-tools => ../lib/mcp-tools
