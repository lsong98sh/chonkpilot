// RB-5（2026-09-22，LLM 层干净化）**窄接口（端口）**声明处：本层对下层（内嵌 gateway /
// 任务层）的**依赖倒置入口**。接口在**消费方（本包）**声明、由下层实现，故本层不再直接
// 依赖下层的具体类型与宽方法面：
//
//   - L1：`Server.gw` 不再持具体类型 `*mcpgateway.Gateway`，改持 `gwRuntime`
//     （只列本层实际消费的方法面；构建方 = 入口装配器 `src/lib/assembly`）。
//   - L4：turn 内（`tool_task.go`）对任务层的**只读状态**访问只经 `taskStateReader`
//     （`Server.taskState()` 取视图），执行侧取消/转后台经 `execController`。
//
// **零行为变更**：接口实现 = 既有具体类型（`*mcpgateway.Gateway` / `*tasklayer.Layer`），
// 调用点逐字不变；未注入时（nil）与改前的 nil 判等语义等价。
package server

import (
	"context"

	mcpgateway "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
)

// gwRuntime 是 llm 对**内嵌 gateway 运行态**的窄接口（RB-5 L1）：启动/停止 + 工具级覆盖
// 注入 + 执行池控制面。实现方 = `chonkpilot-mcp-gateway/gateway.(*Gateway)`。
type gwRuntime interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	SetAsyncOverrides(m map[string]mcpgateway.ToolAsyncOverride)
	SetSandboxOverrides(m map[string]bool)
	CancelExec(instanceID, idOrCall string) (bool, error)
	DetachExec(instanceID, idOrCall string) (string, error)
}

// execController 是 llm 对**执行池控制面**的窄接口（RB-5 L4；层 → 执行侧取消/转后台）。
// 实现方 = `chonkpilot-mcp-gateway/gateway.(*Gateway)`。
type execController interface {
	CancelExec(instanceID, idOrCall string) (bool, error)
	DetachExec(instanceID, idOrCall string) (string, error)
}

// taskStateReader 是 llm 对任务层**只读状态**的窄接口（RB-5 L4；turn 内 `tool_task.go` 用）。
// 实现方 = `chonkpilot-task.(*Layer)`。
type taskStateReader interface {
	State(taskID string) (string, bool)
	ExecOf(taskID string) (tasklayer.Exec, bool)
}

// taskState 返回任务层只读视图（RB-5 L4）：未注入层 → nil，调用方按 nil 处置
// （与改前直接判 `s.taskLayer == nil` 等价）。
func (s *Server) taskState() taskStateReader {
	if s.taskLayer == nil {
		return nil
	}
	return s.taskLayer
}
