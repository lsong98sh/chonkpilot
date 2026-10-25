module github.com/chonkpilot/chonkpilot-mcp-tools

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3
	golang.org/x/sys v0.47.0
	golang.org/x/text v0.22.0
)

replace github.com/chonkpilot/chonkpilot-lib => ../core

replace github.com/jchv/go-webview2 => ../go-webview2
