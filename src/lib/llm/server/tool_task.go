// task 型域工具（tool_stop / tool_result）：
// server 自带工具叠加在 gateway 工具之上发给 LLM（对齐 21-llm-server 任务编排 +
// 域工具契约 server/contracts/tools/{tool_stop,tool_result}.tool.md）。
//
// 识别与执行：turn 内工具分发命中 isTaskTool → 经 gateway 唯一执行入口（域工具回调
// domain-tool-call）→ execTaskTool 同步执行（tool_stop 级联取消 / tool_result 返回
// 转后台结果）→ 结果同步喂回本 turn；任务节点由 domainNode 建/广播、调用方收尾。
package server

import (
	"context"
	"fmt"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// turnInstance 取轮次归属 instance（tc 为空 → ""）：旧调用方（无 turn 上下文的直接调用）不限定
// 实例归属，行为与引入 instance 维度前一致（缺口 3/5 的过滤口径之一）。
func turnInstance(tc *turnCtx) string {
	if tc == nil {
		return ""
	}
	return tc.req.InstanceID
}

// ─── 识别 ─────────────────────────────────────────────

// isTaskTool 判断工具是否为 task 型（server 编排，不转发 gateway 普通路径）。
func isTaskTool(name string) bool {
	return name == "tool_stop" || name == "tool_result"
}

// execTaskTool 执行 task 型工具并返回 (结果文本, isErr)（不含 tasks.done / FeedToolResult——
// 由调用方收尾：域工具 handler 经 gateway 返回文本，turn 内同步喂回本轮次）。
// isErr 为**显式失败位**（B-17：调用方据此落任务终态，不再按结果文本前缀猜测失败）。
// execCtx = 工具执行 ctx（I-83 收尾，18 §3.4：父轮 ctx 派生 + 登记取消柄；gateway 取消回报
// → onGatewayTaskDone → cancelTaskExec 真停长跑执行体）。
func (s *Server) execTaskTool(parent *turnCtx, toolCallID string, node *TaskNode, tool string, args map[string]any, execCtx context.Context) (string, bool) {
	switch tool {
	case "tool_stop":
		return s.runToolStop(parent, node, args) // 级联取消为毫秒级快速操作，无 ctx 感知点
	case "tool_result":
		return s.runToolResult(parent, node, args, execCtx)
	default:
		return "错误: 未知 server 工具 " + tool, true
	}
}

// ─── tool_stop：级联取消 ─────────────────────────────

// runToolStop 停止任务（对齐 tool_stop.md）：task_id → taskManager 级联定位子树
// 标 cancelled + 广播 tasks.done；P2 起**状态判定以任务层为权威**、执行侧取消经层回调
// （`cancelSubtree` → 层 `CancelSubtree` → `OnCancelExec` → 进程内 sink `CancelExec(gw_task_id)`
// → gateway `tm.cancel` → provider.Invalidate，**真实打断在飞调用**，21 §9.2 交付物 ⑤）。
//
// 返回 (结果文本, isErr)；isErr=true 表示工具级失败（B-17：调用方据此落任务终态 error）。
//
// process_id 参数（B-01 安全）：**一律拒绝**。本 server 不持有任何子进程 PID 登记面——执行体
// （executor / 引擎 / dsl-executor 等）均由 gateway 派生，其 OS PID 不经任何消息面回报到本包
// （见 tasks.go `taskExecSink` / `tasklayer.Exec` 只带 gateway 侧任务 id GWTaskID，无 OS PID；
// 本包自身不 spawn 长跑子进程）→ 无法验证 PID 归属，直接拒绝，杜绝 LLM 经任意 PID 击杀本机
// 无关进程。终止任务请改用 task_id（级联取消，含转后台执行体）。
//
// 错误语义（2026-09-18）：已不可逆终态 / 执行池未注入（确有在飞执行）/ 任务不存在 → 明确文案。
func (s *Server) runToolStop(parent *turnCtx, node *TaskNode, args map[string]any) (string, bool) {
	taskID, _ := args["task_id"].(string)
	processID, _ := args["process_id"].(float64)
	if processID > 0 {
		return fmt.Sprintf("错误: 不支持按 process_id(%d) 终止进程（本服务不登记子进程 PID，无法校验归属）；请改用 task_id 级联取消", int(processID)), true
	}
	if taskID == "" {
		return "错误: task_id 必填（process_id 已不支持）", true
	}
	// 层为权威（RB-5 L4：只经 `taskStateReader` 窄接口读层状态，不直调层的具体方法面）：
	// 已不可逆终态 → 明确文案（不重复下发执行侧取消）。
	layer := s.taskState()
	if layer != nil {
		if st, ok := layer.State(taskID); ok && terminalTaskState(st) {
			return fmt.Sprintf("任务 %s 已结束（%s），无需停止", taskID, st), false
		}
	}
	// 可打断性门控：层持 exec 句柄但执行池未注入 → 明确文案（不静默）。
	if s.execSink == nil && layer != nil {
		if exec, ok := layer.ExecOf(taskID); ok && exec.GWTaskID != "" {
			return fmt.Sprintf("错误: 执行池不可用（未注入 sink），无法打断任务 %s", taskID), true
		}
	}
	cancelled := s.tasks.cancelSubtree(turnInstance(parent), taskID)
	if cancelled == 0 {
		return fmt.Sprintf("任务 %s 不存在或已结束", taskID), false
	}
	return fmt.Sprintf("🛑 任务 %s 已级联停止（取消 %d 个节点）", taskID, cancelled), false
}

// ─── tool_result：返回转后台结果 ─────────────────────────────

// runToolResult 获取转后台任务结果（对齐 tool_result.md）：id 支持 server
// 任务节点 / gateway 异步任务 / 子会话 turn id；timeout 秒内轮询至终态，超时返回
// 「任务尚未结束」（不取消任务，可加大 timeout 再次调用）。
// 返回 (结果文本, isErr)；isErr=true = 工具级失败（参数缺失 / 未找到任务；B-17）。
// execCtx = 工具执行 ctx（I-83 收尾）：gateway 取消回报 → cancel → 轮询及时退出
// （不空耗到 timeout；被查询任务本身未受影响）。
func (s *Server) runToolResult(parent *turnCtx, node *TaskNode, args map[string]any, execCtx context.Context) (string, bool) {
	id, _ := args["id"].(string)
	if id == "" {
		return "错误: id 必填", true
	}
	timeout := 30.0
	if v, ok := args["timeout"].(float64); ok {
		timeout = v
	}
	if timeout <= 0 {
		timeout = 0 // 0/负 → 立即查询一次
	}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		if execCtx.Err() != nil {
			return fmt.Sprintf("已取消: 任务 %s 查询中止（gateway 执行侧取消；被查询任务本身未受影响，可重新调用 tool_result）", id), false
		}
		if text, state, found := s.taskResult(parent, id); found {
			if state == "running" {
				if time.Now().After(deadline) {
					return fmt.Sprintf("任务尚未结束：%s 仍在运行（已等待 %.0fs）。可再次调用 tool_result(id=%q, timeout=更长秒数) 继续等待", id, timeout, id), false
				}
				sleepCtx(execCtx, 500*time.Millisecond)
				continue
			}
			return text, false
		}
		return "错误: 未找到任务 " + id, true
	}
}

// taskResult 查询任务结果：返回 (结果文本, 状态, 是否找到)。
// 查找顺序：server 任务节点 → gateway 执行 id 反查节点 → message 表（按 tool_call_id）→
// 子会话 turn（读该 turn 最后一条 assistant）。
//
// 2026-09-18（用户决定）：不再经 gateway `tasks/status|result` 方法面——**状态以任务层为权威、
// 异步结果读 message 表**（role=tool 消息 content 的 {call,result,async}）。
func (s *Server) taskResult(parent *turnCtx, id string) (string, string, bool) {
	// 任务节点直取（限本 instance 归属，缺口 3：不跨 instance 读他人任务结果）
	if n := s.tasks.nodeIn(turnInstance(parent), id); n != nil {
		return s.nodeTaskResult(parent, n)
	}
	// gateway 执行 id（未挂 server 节点）→ 按已登记 gwTaskID 反查节点（限本 instance，缺口 3）
	if n := s.tasks.findByGwTask(turnInstance(parent), id); n != nil {
		return s.nodeTaskResult(parent, n)
	}
	// gateway 原生异步任务（无 server 节点）：id 可能即 LLM tool-call id → 直接读 message 表
	if txt, state, ok := s.asyncResultFromMessage(parent, id); ok {
		return txt, state, true
	}
	// 子会话 turn id（llm_run 委派/DSL 子轮次）
	if txt, ok := s.turnResultText(parent, id); ok {
		return txt, "done", true
	}
	return "", "", false
}

// nodeTaskResult 由任务节点给出查询结果：
//   - 节点终态（done/error/cancelled）→ 节点摘要（既有行为：终态摘要随 mcp-tasks-report 回填）；
//   - 非终态 → **层为权威**判定终态：层判终态但 message 表无结果行 → 明确文案
//     「异步结果已不可用」（不静默返回空）；否则读 message 表（已落 result = 完成；仅 async.pending = 在飞）。
func (s *Server) nodeTaskResult(parent *turnCtx, n *TaskNode) (string, string, bool) {
	switch n.State {
	case TaskStateDone:
		return n.ResultSummary, "done", true
	case TaskStateError:
		return "任务失败: " + n.Error, "error", true
	case TaskStateCancelled:
		return "任务已取消: " + n.Error, "cancelled", true
	}
	layerTerminal := false
	if layer := s.taskState(); layer != nil { // RB-5 L4：经窄接口读层权威状态
		if st, ok := layer.State(n.TaskID); ok {
			switch st {
			case TaskStateDone, TaskStateError, TaskStateCancelled:
				layerTerminal = true
			}
		}
	}
	if n.ToolCallID != "" {
		if txt, state, ok := s.asyncResultFromMessage(parent, n.ToolCallID); ok {
			return txt, state, true
		}
	}
	if layerTerminal {
		return "异步结果已不可用（任务已终态，但 message 表无该调用的结果行）", "error", true
	}
	return "", "running", true
}

// asyncResultFromMessage 读 message 表取该 tool_call_id 的异步结果（交付 1：读路径 = 层 + message 表）：
// 按 tool_call_id 定位 role=tool 消息 → 解析 content 的 {call,result,async}：
//   - 最终结果 = result.content（+ result.status）；
//   - 中间态 = async.pending / async.task_id / async.moved_at（已转后台但尚无结果 → running）。
//
// 返回 (结果文本, 状态, 是否取到)；状态 ∈ done/error/cancelled/running。第二个返回 false =
// 该调用在 message 表中无落库行（仍在飞 / 已被清理）——调用方据此给明确文案。
func (s *Server) asyncResultFromMessage(parent *turnCtx, toolCallID string) (string, string, bool) {
	if toolCallID == "" {
		return "", "", false
	}
	raw, ok := newSessionStore(s.bus, parent.req.InstanceID).LoadToolContent(parent.req.Session, toolCallID)
	if !ok {
		return "", "", false
	}
	tc, ok := persist.ParseToolContent(raw)
	if !ok {
		return "", "", false
	}
	text, status := "", ""
	if tc.Result != nil {
		text, status = tc.Result.Content, tc.Result.Status
	}
	switch status {
	case persist.ToolStatusCancelled:
		return text, "cancelled", true
	case persist.ToolStatusFailed:
		return text, "error", true
	case persist.ToolStatusCompleted:
		return text, "done", true
	}
	// 已转后台挂起（async 段）且尚无结果 → 中间态；其余有结果按完成处理。
	if text == "" && tc.Async != nil && tc.Async.Pending {
		return "", "running", true
	}
	if text != "" {
		return text, "done", true
	}
	return "", "running", true
}

// turnResultText 按子会话 turn id 读结果（llm_run 子轮次最后一条 assistant 消息）。
func (s *Server) turnResultText(parent *turnCtx, turnID string) (string, bool) {
	msgs := newSessionStore(s.bus, parent.req.InstanceID).LoadMessages(turnID)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != "" {
			return msgs[i].Content, true
		}
	}
	return "", false
}
