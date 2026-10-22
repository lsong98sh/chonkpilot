// chonkpilot-tools-executor：内置工具统一执行器（GATEWAY-2 / L2）。
//
// 与旧「一工具一 exe」的区别：同一个 exe 承载多个工具，调用时按工具名分发。
// 契约：<exe> <tool> --input=<params.json>；`--help` 列出 tools/file 下的全部契约。
//
// L3（2026-10-21）：文件域实现已抽到公开包 toolset，供内置 mcp-server 进程内调用；
// 本 exe 保留为调试/独立部署形态，分发逻辑复用同一 toolset。
package main

import (
	"context"
	"os"

	"github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"
	"github.com/chonkpilot/chonkpilot-mcp-tools/toolset"
)

func main() {
	os.Exit(cli.Run(dispatch, cli.Help{Dir: "tools/file"}))
}

// dispatch 按工具名分发；未登记 → nil（cli 报 unknown tool）。
func dispatch(_ string, tool string, args map[string]interface{}) *cli.Result {
	res, ok := toolset.Dispatch(context.Background(), tool, args)
	if !ok {
		return nil
	}
	return &cli.Result{Tool: tool, Success: !res.IsError, Output: res.Text}
}
