package desktop

import "github.com/chonkpilot/chonkpilot-mcp-tools/internal/cli"

// ToolResult 与统一契约 Result 同构（类型别名），桌面工具直接返回 *cli.Result。
type ToolResult = cli.Result
