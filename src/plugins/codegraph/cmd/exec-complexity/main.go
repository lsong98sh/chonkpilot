// chonkpilot-codegraph-complexity-executor：codegraph_analyze_complexity 单工具执行器。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-codegraph-mcp-server/execx"
)

func main() { os.Exit(execx.Run("codegraph_analyze_complexity")) }
