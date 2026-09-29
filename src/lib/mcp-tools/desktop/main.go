// chonkpilot-desktop-executor：desktop 分类工具执行器（Win32 API，自持实现 internal/desktop）。
// 统一契约：<exe> <tool> --input=<参数 JSON>，结果统一 JSON 输出到 stdout。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/desktop"
)

func main() {
	os.Exit(cli.Run(dispatch, cli.Help{Dir: "tools/desktop"}))
}

func dispatch(workDir, tool string, args map[string]interface{}) *cli.Result {
	switch tool {
	case "desktop_run":
		return desktop.HandleDesktopRun(args)
	default:
		return nil
	}
}
