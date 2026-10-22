module github.com/chonkpilot/chonkpilot-mcp-tools

go 1.26.0

require (
	github.com/chonkpilot/chonkpilot-lib v0.0.0
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3
	golang.org/x/sys v0.47.0
	golang.org/x/text v0.22.0
)

require github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect

require (
	github.com/chromedp/cdproto v0.0.0-20260804232424-e85f50dbfd32
	github.com/chromedp/chromedp v0.16.0
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/jchv/go-webview2 v0.0.0
)

replace github.com/chonkpilot/chonkpilot-lib => ../core

replace github.com/jchv/go-webview2 => ../go-webview2
