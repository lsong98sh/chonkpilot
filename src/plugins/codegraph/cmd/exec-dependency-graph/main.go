// chonkpilot-codegraph-dependency-graph-executor：codegraph_get_dependency_graph 单工具执行器。
package main

import (
	"os"

	"github.com/chonkpilot/chonkpilot-codegraph-mcp-server/execx"
)

func main() { os.Exit(execx.Run("codegraph_get_dependency_graph")) }
