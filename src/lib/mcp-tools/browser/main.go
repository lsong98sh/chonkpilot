// chonkpilot-browser-executor：browser 分类工具执行器（浏览器自动化，chromedp）。
// 统一契约：<exe> <tool> --input=<参数 JSON>，结果统一 JSON 输出到 stdout。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/browser"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
)

func main() {
	os.Exit(cli.Run(dispatch, cli.Help{Dir: "tools/browser"}))
}

func dispatch(workDir, tool string, args map[string]interface{}) *cli.Result {
	switch tool {
	case "browser_run":
		return browser.HandleBrowserRun(args)
	default:
		return nil
	}
}
