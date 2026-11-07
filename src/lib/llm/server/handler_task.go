package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
)

// onTaskStop 用户手动结束任务（§6.3 场景 7 / §6.6 取消）：级联定位任务子树 →
// 节点标 cancelled + 广播 tasks.done（cascade 沿 parent_id）。P2：**状态判定以任务层为权威**
// （层判定可取消集合）+ **层 → 执行侧**取消（`cancelSubtree` → 层 `CancelSubtree` 对持 exec
// 句柄的节点回调 `OnCancelExec` → `onTaskLayerCancelExec` → 进程内 sink `CancelExec(gw_task_id)`
// → gateway `tm.cancel` → onTaskCancel → provider.Invalidate，**真实打断在飞调用**，
// 21 §9.2 交付物 ⑤）。2026-09-18：执行侧下发不再经 `mcp-tasks-cancel` 方法面。
//
// id 兼容（2026-09-18）：`task_id` = server 任务节点 id（任务树 ▍ / useTaskView 传入）；
// 前端工具行「停止」在裁决态只有 LLM tool_call_id（气泡上无 server 节点 id，其
// `message.task_id` / `arbitration.task_id` 均为 gateway 执行 id）→ 载荷**可选扩参**
// `tool_call_id`，先经内存任务节点反查归一到 `task_id`（**不新增 MQ 主题**，61 §4.2）。
// 错误语义：id 缺失/未命中、已不可逆终态、执行池未注入（确有在飞执行）→ 明确报错（不静默）。
func (s *Server) onTaskStop(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		TaskID     string `json:"task_id"`
		ToolCallID string `json:"tool_call_id"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &req)

	// ① id 归一：优先 task_id（任务节点 id）；缺省按 tool_call_id 反查已登记任务节点。
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		if callID := strings.TrimSpace(req.ToolCallID); callID != "" {
			if n := s.tasks.findByToolCall(req.InstanceID, callID); n != nil {
				taskID = n.TaskID
			}
		}
	}
	if taskID == "" {
		return methodErr("task-stop: task_id 或 tool_call_id 必填，且需命中已登记任务")
	}

	// ② 层为权威：已不可逆终态（done/error/cancelled）→ 明确拒绝，不重复下发执行侧取消。
	if s.taskLayer != nil {
		if st, ok := s.taskLayer.State(taskID); ok && terminalTaskState(st) {
			return methodErr("任务已结束（" + st + "），无需停止")
		}
	}

	// ③ 可打断性门控：层持 exec 句柄（`ExecOf` → gw_task_id）时，取消经**层回调**下发执行侧；
	// 执行池未注入（sink nil）且确有在飞执行 → 明确报错（不静默，避免"点了没反应"）。
	if s.execSink == nil && s.taskLayer != nil {
		if exec, ok := s.taskLayer.ExecOf(taskID); ok && exec.GWTaskID != "" {
			return methodErr("执行池不可用（未注入 sink），无法打断任务 " + taskID)
		}
	}

	// ④ 层先标终态 + 广播 tasks.done（cascade 沿 parent_id；层内 exec_json 保留）。
	// 2026-09-19：带 instance 归属（缺口 3）→ 只取消该 instance 的节点。
	cancelled := s.tasks.cancelSubtree(req.InstanceID, taskID)
	if cancelled == 0 {
		return methodErr("任务不存在或已结束: " + taskID)
	}
	// 委派子代理：任务态已级联取消 → 关闭对应子轮次（递归下一级）。
	s.closeDelegatedInSubtree(req.InstanceID, taskID)
	v.Result = map[string]any{"cancelled": true, "task_id": taskID}
	return nil
}

// onTaskBackground 前端「转后台」（61-消息一览 §4）：收到 {tool_call_id} → 经**层 + 进程内 sink**
// 解绑命中的在飞任务（manual）→ 返回 {task_id}。解绑后原同步调用返回 pending（turn.go 登记
// gwTaskID + turn 挂起），终态经 mcp-tasks-report 回报。
// 2026-09-18（用户决定）：不再经 gateway `tools/background` 方法面——层持有 exec 句柄
// （`ExecOf` → gw_task_id），经 sink `DetachExec` 同进程直调执行池；层未登记该执行的句柄 →
// 以 tool_call_id 交 gateway 定位（既有宽容语义）。
// 未命中 / 已终态 / 已解绑 → 执行侧回错误 → 本方法经 errors 语义上抛。
func (s *Server) onTaskBackground(_ context.Context, _ string, v *mq.Value) error {
	var req struct {
		ToolCallID string `json:"tool_call_id"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(v.Payload, &req)
	if strings.TrimSpace(req.ToolCallID) == "" {
		return methodErr("tool_call_id is required")
	}
	ref := req.ToolCallID
	// 归属 instance（缺口 2：执行池按 instance 分桶）：载荷未带而节点命中 → 取节点归属，
	// 保证带 gw_task_id 直调执行池时能落到正确桶。
	inst := req.InstanceID
	// 硬门控（G-17）：仅 manual（同步运行但任务化、可解绑）允许转后台；
	// auto/always/never 无在飞可解绑任务（auto 超阈值自动转），直接 errors、不下发执行侧。
	// 工具名由任务节点反查；节点缺失或工具不在 tools/list 缓存（无法判定）→ 放行交执行侧判定。
	if node := s.tasks.findByToolCall(req.InstanceID, req.ToolCallID); node != nil {
		inst = node.InstanceID
		// P2：状态判定以任务层为权威（21 §9.2 交付物 ⑤）——层已判为不可逆终态 → 无需转后台。
		if s.taskLayer != nil {
			if st, ok := s.taskLayer.State(node.TaskID); ok {
				switch st {
				case TaskStateDone, TaskStateError, TaskStateCancelled:
					return methodErr("任务已结束（" + st + "），无需转后台")
				}
			}
			// 层持执行句柄（P3）→ 以 gw_task_id 直调执行池（与取消回调同源）。
			if exec, ok := s.taskLayer.ExecOf(node.TaskID); ok && exec.GWTaskID != "" {
				ref = exec.GWTaskID
			}
		}
		if mode := s.gc.AsyncMode(node.Tool); mode != "" && mode != "manual" {
			return methodErr("该工具非 manual 模式（" + mode + "），无需转后台")
		}
	}
	if s.execSink == nil {
		return methodErr("执行池不可用（未注入 sink），无法转后台")
	}
	taskID, err := s.execSink.DetachExec(inst, ref)
	if err != nil {
		return methodErr(err.Error())
	}
	v.Result = map[string]any{"task_id": taskID}
	return nil
}

// onGatewayTaskDone gateway 异步任务完成回报（mcp-tasks-report，B1 独立主题）：
// ① 任务节点状态同步：命中未终态节点 → 广播 tasks.done（已终态如 cancelled 不重复广播）；
// ② 命中等待该 task 的 turn → 回填续轮（结果被本轮取走）；
// ③ 未命中等待 turn（调用方已离开）→ 广播 tool-notify（notice=completion）→ 前端追加 🔔 通知（I-34）。
func (s *Server) onGatewayTaskDone(_ string, payload []byte) {
	var ev struct {
		TaskID        string `json:"task_id"`
		ToolCallID    string `json:"tool_call_id"`
		Tool          string `json:"tool"`
		State         string `json:"state"`
		ResultSummary string `json:"result_summary"`
		Session       string `json:"session"`
		Turn          string `json:"turn"`
		InstanceID    string `json:"instance_id"`
	}
	_ = json.Unmarshal(payload, &ev)
	// ⓪ I-83 收尾（18 §3.4）：取消回报 → 按工具执行取消表**真停执行体**（task 型域工具
	// tool_result 轮询等；gateway 侧 Emit 同步派发不中断 handler，故经回报通道反向通知）。
	// 未命中 = 执行体已结束 / 未登记（无 tool_call_id 或非 task 型）→ 无操作（幂等）。
	if ev.State == "cancelled" {
		s.cancelTaskExec(ev.ToolCallID)
	}
	node := s.tasks.findByGwTask(ev.InstanceID, ev.TaskID)
	if node == nil && ev.ToolCallID != "" {
		// 非 detached 任务未登记 gwTaskID → 按 tool_call_id 兜底定位（I-62：超时裁决取消回报）
		node = s.tasks.findByToolCall(ev.InstanceID, ev.ToolCallID)
	}
	// ① 任务节点终态同步（findByGwTask / findByToolCall 定位 server 编排节点；done 幂等：
	// 已终态不重复广播、不覆盖）
	if node != nil && !terminalTaskState(node.State) {
		switch ev.State {
		case "done":
			s.tasks.done(node.TaskID, TaskStateDone, ev.ResultSummary, "")
		case "cancelled":
			s.tasks.done(node.TaskID, TaskStateCancelled, "", ev.ResultSummary)
		default: // error
			s.tasks.done(node.TaskID, TaskStateError, "", ev.ResultSummary)
		}
	}
	// ② 命中等待 turn → 续轮（缺口 5：按回报载荷的 instance 归属限定，不跨 instance 命中）。
	// 经 task_id → *turnCtx 运行态索引定位（O(1)）；未命中回退全表扫描（保守，索引与 pending
	// 短暂不一致时仍能命中，避免漏续轮）。
	if tc := s.taskTurnFor(ev.InstanceID, ev.Turn, ev.TaskID); tc != nil {
		m := asyncDoneMsg{taskID: ev.TaskID, state: ev.State, result: ev.ResultSummary}
		// 非阻塞投递（P3）：tc.asyncDone 缓冲有限（8），loop 忙时直接阻塞投递会卡住**同步派发的
		// 总线 goroutine**（Emit 同步回调，宿主 UI 线程）→ 满则转后台 goroutine 投递，不阻塞。
		select {
		case tc.asyncDone <- m:
		case <-tc.Done():
		default:
			go func() {
				select {
				case tc.asyncDone <- m:
				case <-tc.Done():
				}
			}()
		}
		return
	}
	// ③ 调用方已离开 → 完成通知（任务节点已由 ① 更新广播）
	s.notifyTaskCompletion(node, ev.Session, ev.Turn, ev.InstanceID, ev.Tool, ev.TaskID, ev.State)
}

// ── 异步任务运行态索引（task_id → *turnCtx）─────────────────────────────

// forgetPendingTaskIndex 摘除 task_id 的运行态索引（消费回执；**仅当仍指向 tc**，避免误删新轮次登记）。
func (s *Server) forgetPendingTaskIndex(instanceID, taskID string, tc *turnCtx) {
	if taskID == "" {
		return
	}
	s.mu.Lock()
	k := instKey(instanceID, taskID)
	if s.taskTurnIndex[k] == tc {
		delete(s.taskTurnIndex, k)
	}
	s.mu.Unlock()
}

// forgetPendingTaskIndexLocked 摘除某轮次的**全部** pending 运行态索引（**须持 s.mu**；轮次收尾）
// ——索引规模 = 在飞任务数，收尾时整体扫一次代价极小。
func (s *Server) forgetPendingTaskIndexLocked(tc *turnCtx) {
	for k, t := range s.taskTurnIndex {
		if t == tc {
			delete(s.taskTurnIndex, k)
		}
	}
}

// taskTurnFor 定位等待该网关任务的运行 turn。定位优先级：① 报带 turn → 按 {instance,turn}
// 直取该轮并校验其 pending（O(1)，避免全表扫描）；② task_id → *turnCtx 运行态索引（O(1)）；
// ③ 索引未命中回退全表扫描（保守，兼容索引与 pending 短暂不一致，避免漏续轮）。
// **须在 s.mu 内访问索引 / 轮次表**。
func (s *Server) taskTurnFor(instanceID, turn, taskID string) *turnCtx {
	if taskID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn != "" {
		if tc := s.turnCtxByKeyLocked(instKey(instanceID, turn)); tc != nil && tc.pendingHas(taskID) {
			return tc
		}
	}
	if tc := s.taskTurnIndex[instKey(instanceID, taskID)]; tc != nil &&
		(instanceID == "" || tc.req.InstanceID == instanceID) {
		return tc
	}
	for _, sess := range s.turns {
		t, ok := sess.(*turnCtx)
		if !ok {
			continue
		}
		if instanceID != "" && t.req.InstanceID != instanceID {
			continue
		}
		if t.pendingHas(taskID) {
			return t
		}
	}
	return nil
}

// pendingHas 报告本轮的挂起登记是否含该 task_id（**须在持有 s.mu 的前提下调用**：内部取 tc.mu，
// 与既有全表扫描同一锁定序 s.mu → tc.mu）。
func (tc *turnCtx) pendingHas(taskID string) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.frames.HasPending(taskID)
}

// notifyTaskCompletion 调用方已离开（无等待 turn）的异步任务完成通知：广播 tool-notify
// （notice=completion）→ 前端 MessageList.onCompletionNotice 追加 🔔 user-notify 气泡
// （结果尚未被本轮取走，可再经 tool_result 获取）。用户主动取消（cancelled）不通知。
// instance_id/session_id 由任务节点（session → server 编排映射，TaskNode.InstanceID/SessionID）
// 取，缺省回落 gateway 完成回报载荷；实例字段必带（61-消息一览 §0.1），缺失不广播。
func (s *Server) notifyTaskCompletion(node *TaskNode, session, turn, instanceID, tool, taskID, state string) {
	if node != nil {
		if session == "" {
			session = node.SessionID
		}
		if turn == "" {
			turn = node.TurnID
		}
		if instanceID == "" {
			instanceID = node.InstanceID
		}
		if node.TaskID != "" {
			taskID = node.TaskID // 通知/去重以 server 编排节点 id 为准（tool_result 可直接解析）
		}
		if node.Name != "" {
			tool = node.Name
		} else if tool == "" {
			tool = node.Tool
		}
		if node.State == TaskStateCancelled {
			state = "cancelled"
		}
	}
	if state == "cancelled" || instanceID == "" || taskID == "" {
		return
	}
	if tool == "" {
		tool = "任务"
	}
	msg := "🔔 异步任务已完成：" + tool + "（task_id=" + taskID + "）。该任务结果尚未获取，可调用 tool_result(id=\"" + taskID + "\") 获取完整结果。"
	if state != "done" {
		msg = "🔔 异步任务执行失败：" + tool + "（task_id=" + taskID + "）。"
	}
	s.enqueueCompletionNotice(completionNotice{
		instanceID: instanceID,
		sessionID:  session,
		turnID:     turn,
		taskID:     taskID,
		toolCallID: nodeToolCallID(node),
		tool:       tool,
		state:      state,
		message:    msg,
		messageID:  "msg-notify-" + taskID,
	})
}

// notify 发布 llm-receive（与 llm-send 对应：LLM 返回内容广播给监听者）。
func (s *Server) notify(tc *turnCtx, typ string, payload map[string]any) {
	ev := map[string]any{
		"type": typ, "payload": payload,
		"instance_id": tc.req.InstanceID, "session": tc.req.Session, "turn": tc.req.Turn,
	}
	s.publish("llm-receive", ev)
}
