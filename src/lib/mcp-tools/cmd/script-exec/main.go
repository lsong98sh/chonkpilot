// chonkpilot-script-executor：script_run 专用执行器（一工具一 exe，2026-10-19）。
// 契约：<exe> script_run --input=<params.json>。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/scriptrun"
)

func main() {
	os.Exit(cli.Run(dispatch, cli.Help{Dir: "tools/core"}))
}

func dispatch(_ string, tool string, args map[string]interface{}) *cli.Result {
	if tool != "script_run" {
		return nil
	}
	return scriptrun.HandleScriptRun("", args)
}
