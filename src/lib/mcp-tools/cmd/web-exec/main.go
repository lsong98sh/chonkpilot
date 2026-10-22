// chonkpilot-web-executor：web_fetch 专用执行器（一工具一 exe，2026-10-19）。
// 契约：<exe> web_fetch --input=<params.json>。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fetch"
)

func main() {
	os.Exit(cli.Run(dispatch, cli.Help{Dir: "tools/core"}))
}

func dispatch(_ string, tool string, args map[string]interface{}) *cli.Result {
	if tool != "web_fetch" {
		return nil
	}
	return fetch.HandleFetch("", args)
}
