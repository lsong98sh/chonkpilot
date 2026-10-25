// 进程内执行（GATEWAY-2 / L3）：契约 runtime == "builtin" 的工具由内置 mcp-server
// **进程内**执行，不再 spawn executor exe。
//
// 分派判据（同口径、无魔法）：
//   - 按契约 category：meta → toolmeta.Run(GroupMeta)；
//     server（domain 组）→ toolmeta.Run(GroupDomain)；
//   - 其余 category 的 builtin 契约 = 契约错误（明确失败，不静默 spawn）。
//
// 注：文件域 11 个工具（category `file`）已随 file 插件迁出主仓（PLUGIN-SPLIT-1），
// 由插件自身的 MCP server 执行；主仓 builtin 不再有 file 分派。
//
// 故障域补偿（设计 §3.3）：每个 handler 外层 recover() → panic 转 isError 结果，绝不冒泡
// 到 server 主循环；超时/取消由 callBuiltin 派生的 ctx 控制，只影响本次调用。
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-mcp-server/toolmeta"
)

// builtinRuntime 是「进程内执行」的契约 runtime 取值。
const builtinRuntime = "builtin"

// builtinResult 是进程内执行结果（Text 原样文本；IsError 对齐 executor 非 0 退出码）。
type builtinResult struct {
	Text    string
	IsError bool
}

// isBuiltinRuntime 判定契约 runtime 是否为进程内执行。
func isBuiltinRuntime(rt string) bool {
	return strings.EqualFold(strings.TrimSpace(rt), builtinRuntime)
}

// dispatchBuiltin 进程内执行一次工具调用（永不 panic、永不返回 nil）。
// args 已合并默认参数；cx = 协议 _meta 调用上下文。
func dispatchBuiltin(ctx context.Context, cfg *Config, td *ToolDoc, args map[string]any, cx CallContext) (res *builtinResult) {
	defer func() {
		if r := recover(); r != nil {
			res = &builtinResult{Text: fmt.Sprintf("tool %s panic: %v", td.Name, r), IsError: true}
		}
	}()

	// 文件域（file_*）已随插件迁出主仓（PLUGIN-SPLIT-1，2026-10-25）：
	// 由 file 插件的 MCP server 执行，主仓 builtin 不再持有其实现。

	var group toolmeta.Group
	switch strings.ToLower(strings.TrimSpace(td.Category)) {
	case "meta":
		group = toolmeta.GroupMeta
	case "server":
		group = toolmeta.GroupDomain
	default:
		return &builtinResult{
			Text:    fmt.Sprintf("tool %s: runtime builtin 但 category %q 无进程内实现", td.Name, td.Category),
			IsError: true,
		}
	}

	addr, token := cfg.bridge()
	r := toolmeta.Run(ctx, group, td.Name, args, toolmeta.Context{
		InstanceID: cx.InstanceID,
		Session:    cx.SessionID,
		Turn:       cx.TurnID,
		ToolCallID: cx.ToolCallID,
		Purpose:    cx.Purpose,
	}, toolmeta.Bridge{Addr: addr, Token: token})
	return &builtinResult{Text: r.Text, IsError: r.IsError}
}
