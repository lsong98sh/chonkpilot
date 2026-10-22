// chonkpilot-vfts-query-executor：vfts_query 单工具执行器。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-vfts-mcp-server/execx"
)

func main() { os.Exit(execx.Run()) }
