// chonkpilot-core-executor：core 分类工具执行器（自持实现，见 internal/fileops / scriptrun / fetch）。
// 统一契约：<exe> <tool> --input=<参数 JSON>，结果统一 JSON 输出到 stdout。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fetch"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/fileops"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/scriptrun"
)

func main() {
	os.Exit(cli.Run(dispatch, cli.Help{Dir: "tools/core"}))
}

func dispatch(workDir, tool string, args map[string]interface{}) *cli.Result {
	switch tool {
	case "file_find":
		return fileops.HandleFind(workDir, args)
	case "filesys_run":
		return fileops.HandleFileManager(workDir, args)
	case "file_read":
		return fileops.HandleReadFile(workDir, args)
	case "file_diff":
		return fileops.HandleDiff(workDir, args)
	case "script_run":
		return scriptrun.HandleScriptRun(workDir, args)
	case "web_fetch":
		return fetch.HandleFetch(workDir, args)
	default:
		return nil
	}
}
