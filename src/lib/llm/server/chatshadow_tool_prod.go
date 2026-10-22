// chat 工具端口的**生产策略**装配（MOD-1 A4-P1）：把当前 turn 的白名单 / 旁路加固 / ask 通道
// 注入 `chatToolInvoker`。
//
// **不接管生产**：本文件只提供构造器，`turnCtx` 的工具循环保持原样（P3 切换时由开关调用）；
// 任务节点仍由生产循环创建 —— P1 不复制（ask 的 `taskID` 传空，见下）。
package server

import (
	"fmt"
	"strings"

	"github.com/lsong98sh/chonkpilot-chat"
)

// chatToolInvokerFromTurn 用当前 turn 的生产策略装配 invoker（**不接管**：调用方仍是 P3 的开关）。
//
// 装配口径（与 `turn.go` 工具循环逐字一致）：
//   - `args` = `tc.gatewayArgs`（缺省 `async=always` 等例外规则原样复用）；
//   - `policy` = ① `!tc.toolAllowed(name)` 硬拒 → ② `mcp_invoke` 目标工具的旁路加固硬拒；
//   - `ask` = `tc.server.askWithID(tc.req.Turn, call.ID, args, "", "ask-"+newID())`。
//
// 与生产的已知差异（P3 收口）：
//   - 任务节点（`tasks.start` / `node.TaskID`）由生产循环创建；本构造器不建节点，故 ask 的
//     `taskID` 传空。P3 需要把节点 id 传入（或由宿主在 Dispatch 前建节点）。
//   - `topSession` / `parent` 同属任务树归属，P1 留空；P3 装配时按 `taskNodeBase` 传入。
func chatToolInvokerFromTurn(tc *turnCtx) *chatToolInvoker {
	inv := newChatToolInvoker(tc.server, tc.req.InstanceID, tc.req.Session, tc.req.Turn, tc.req.WorkDir, tc.req.DataDir)
	inv.args = tc.gatewayArgs

	// 下发前硬拒（顺序与文案逐字对齐 turn.go 1840-1859）：① 白名单；② mcp_invoke 旁路加固。
	inv.policy = func(name string, args map[string]any) string {
		if !tc.toolAllowed(name) {
			return fmt.Sprintf("错误: 工具 %q 不在本轮 agent 的工具白名单内，已拒绝执行（可用工具见本轮 tools 参数；白名单 = 场景 agent 的「工具过滤」配置）", name)
		}
		if isMetaInvokeTool(strings.TrimPrefix(name, "self_")) && tc.allowedTools != nil {
			if target := strings.TrimSpace(str(args["name"])); target != "" && !tc.toolAllowed(target) {
				return fmt.Sprintf("错误: mcp_invoke 的目标工具 %q 不在本轮 agent 的工具白名单内，已拒绝执行（白名单 = 场景 agent 的「工具过滤」配置；白名单内工具可直接调用）", target)
			}
		}
		return ""
	}

	// ask_user → 宿主 ask 通道（等待 / 超时 / 失效回执语义归宿主）。
	// taskID 空 = P1 不建任务节点；P3 切换须把生产循环建好的节点 id 传入。
	inv.ask = func(call chat.ToolCall, args map[string]any) string {
		return tc.server.askWithID(tc.req.Turn, call.ID, args, "", "ask-"+newID())
	}
	return inv
}
