// 任务结果读取能力（`plugin/v1/task.result` / `plugin/v1/task.wait` 的宿主实现）：
// 任务层读（状态 / 部分输出）、message 表读与终态事件等待。
//
// 2026-11-08：`tool_result` 工具已删除（结果读取统一走 task 插件 `task_status`）；本文件只保留
// 宿主侧能力，不再有工具识别 / 执行 / 取消表。
//
// SESSION-1 阶段二·任务树：**入参校验与结果文案的语义映射**已下沉到纯 lib 组件
// `chonkpilot-session`（`task_tools.go`）。
//
// 2026-11-04：`tool_stop` 已删除，`task_stop` 为唯一停止入口（`handler_task.go` onTaskStop）。
package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-agent/session"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
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
// 2026-11-08：`tool_result` 已删除（结果读取统一走 task 插件 `task_status`）。本文件只保留
// 宿主侧**任务结果读取能力**（`plugin/v1/task.result` / `plugin/v1/task.wait` 的实现）。

// closeDelegatedInSubtree 关闭被取消子树内的委派子轮次（task-stop 链路共用；
// `handler_task.go` onTaskStop 调用）。
// 仅处理 kind=llm 且持有子 turn 的节点；幂等（closeDelegatedTurn 未命中即 no-op）。
func (s *Server) closeDelegatedInSubtree(instanceID, rootID string) {
	for _, n := range s.tasks.subtree(instanceID, rootID) {
		if n.Kind == TaskKindLLM && n.TurnID != "" {
			s.closeDelegatedTurn(n.InstanceID, n.TurnID)
		}
	}
}

// ─── 任务结果读取 / 等待 ─────────────────────────────

// taskTerminalWaiter 是一次结果等待的**终态事件**唤醒信号（一次性；多事件合并）。
type taskTerminalWaiter struct {
	ch   chan struct{}
	once sync.Once
}

func newTaskTerminalWaiter() *taskTerminalWaiter {
	return &taskTerminalWaiter{ch: make(chan struct{})}
}

// signal 唤醒等待方（幂等；事件可能来自多个主题 / 重复回报）。
func (w *taskTerminalWaiter) signal() {
	w.once.Do(func() { close(w.ch) })
}

// watchTaskTerminal 订阅**既有**任务终态事件面（零新增主题），返回唤醒通道与退订函数：
//   - `task-done`：任务层终态广播（tasks.done，61 §3.4；server 自身 publish，含 task_id/tool_call_id/
//     turn_id/instance_id）；
//   - `mcp-tasks-report`：gateway 异步任务完成回报（61 §5，含 task_id/tool_call_id/instance_id）；
//   - `session-complete`：子会话 turn 终态（结果 id 支持子 turn id）。
//
// 命中判据 = 同 instance（双方均非空时）+ id ∈ {task_id, tool_call_id, turn, id}；载荷无任何
// 标识字段 → **保守唤醒**（等待方醒来后重查权威状态，不会误判终态）。订阅回调只 close 本地信号，
// 不触碰 turn 状态（单写者约束）。
func (s *Server) watchTaskTerminal(instanceID, id string) (*taskTerminalWaiter, func()) {
	w := newTaskTerminalWaiter()
	if s == nil || s.bus == nil {
		return w, func() {}
	}
	handler := func(_ context.Context, _ string, v *mq.Value) error {
		var ev struct {
			InstanceID string `json:"instance_id"`
			TaskID     string `json:"task_id"`
			ToolCallID string `json:"tool_call_id"`
			Turn       string `json:"turn"`
			ID         string `json:"id"`
		}
		_ = json.Unmarshal(v.Payload, &ev)
		if instanceID != "" && ev.InstanceID != "" && ev.InstanceID != instanceID {
			return nil
		}
		if ev.TaskID == "" && ev.ToolCallID == "" && ev.Turn == "" && ev.ID == "" {
			w.signal() // 无标识字段：无法过滤，保守唤醒
			return nil
		}
		if id != "" && (id == ev.TaskID || id == ev.ToolCallID || id == ev.Turn || id == ev.ID) {
			w.signal()
		}
		return nil
	}
	subs := make([]mq.Sub, 0, 3)
	for _, topic := range []string{
		msgkeys.TopicTaskDone,
		msgkeys.TopicMcpTasksReport,
		msgkeys.TopicSessionComplete,
	} {
		sub, err := s.bus.On(topic, 0, handler)
		if err != nil {
			logf("[chonkpilot-server] task.wait: 订阅 %s 失败（退化为仅超时收口）: %v\n", topic, err)
			continue
		}
		subs = append(subs, sub)
	}
	return w, func() {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
	}
}

// stopTaskWaitTimer 停止等待 timer 并丢弃已入队的触发（避免伪唤醒）。
func stopTaskWaitTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// maxTaskWait 是单次 `plugin/v1/task.wait` 的等待上限（防止插件一次占住桥会话并发槽；
// 调用方按剩余 timeout 循环调用，语义不变）。
const maxTaskWait = 60 * time.Second

// waitTaskResult 在宿主侧**事件驱动**地等待任务终态（`plugin/v1/task.wait` 的实现）：
// 先订阅既有终态事件（task-done / mcp-tasks-report / session-complete），再查一次权威状态；
// running → 等事件或剩余超时（单 timer），醒来重查后返回。wait<=0 = 只查询一次。
//
// 与 taskResult 共用同一套状态判定与事件面；区别只是调用方（插件桥）跨进程。
func (s *Server) waitTaskResult(ctx context.Context, parent *turnCtx, id string, wait time.Duration) (string, string, string, bool) {
	waiter, stopWaiter := s.watchTaskTerminal(turnInstance(parent), id)
	defer stopWaiter()
	text, state, partial, found := s.taskResultDetail(parent, id)
	if !found || state != "running" || wait <= 0 {
		return text, state, partial, found
	}
	if wait > maxTaskWait {
		wait = maxTaskWait
	}
	timer := time.NewTimer(wait)
	defer stopTaskWaitTimer(timer)
	select {
	case <-ctx.Done():
	case <-waiter.ch:
	case <-timer.C:
	}
	return s.taskResultDetail(parent, id)
}

// taskResult 查询任务结果：返回 (结果文本, 状态, 是否找到)（既有调用方口径）。
func (s *Server) taskResult(parent *turnCtx, id string) (string, string, bool) {
	text, state, _, found := s.taskResultDetail(parent, id)
	return text, state, found
}

// taskResultDetail 同 taskResult，额外返回**部分输出**（running 时已产出的内容）：
// 任务节点 Output（执行器上报的进度文本）/ 委派子轮次当前 assistant 文本。
// 查找顺序：server 任务节点 → gateway 执行 id 反查节点 → message 表（按 tool_call_id）→
// 子会话 turn（读该 turn 最后一条 assistant）。
//
// 2026-09-18（用户决定）：不再经 gateway `tasks/status|result` 方法面——**状态以任务层为权威、
// 异步结果读 message 表**（role=tool 消息 content 的 {call,result,async}）。
//
// 2026-11-08：`tool_result` 删除后，本函数是 `plugin/v1/task.result|task.wait` 的读取实现；
// 终态结果被取走时登记 consumed（§5.3 第 4 条：之后不再发完成通知）。
func (s *Server) taskResultDetail(parent *turnCtx, id string) (string, string, string, bool) {
	text, state, partial, found := s.taskResultDetailRaw(parent, id)
	if found && state != "running" {
		s.markResultConsumed(parent, id)
	}
	return text, state, partial, found
}

// markResultConsumed 登记「任务结果已被取走」（id 可能是任务节点 / gateway 执行 id / tool-call id）。
func (s *Server) markResultConsumed(parent *turnCtx, id string) {
	inst := turnInstance(parent)
	s.markTaskResultConsumed(inst, id)
	if n := s.tasks.nodeIn(inst, id); n != nil {
		s.markTaskResultConsumed(n.InstanceID, n.TaskID, n.ToolCallID)
	} else if n := s.tasks.findByGwTask(inst, id); n != nil {
		s.markTaskResultConsumed(n.InstanceID, n.TaskID, n.ToolCallID)
	} else if n := s.tasks.findByToolCall(inst, id); n != nil {
		s.markTaskResultConsumed(n.InstanceID, n.TaskID, n.ToolCallID)
	}
}

func (s *Server) taskResultDetailRaw(parent *turnCtx, id string) (string, string, string, bool) {
	// 任务节点直取（限本 instance 归属，缺口 3：不跨 instance 读他人任务结果）
	if n := s.tasks.nodeIn(turnInstance(parent), id); n != nil {
		return s.nodeTaskResultDetail(parent, n)
	}
	// gateway 执行 id（未挂 server 节点）→ 按已登记 gwTaskID 反查节点（限本 instance，缺口 3）
	if n := s.tasks.findByGwTask(turnInstance(parent), id); n != nil {
		return s.nodeTaskResultDetail(parent, n)
	}
	// gateway 原生异步任务（无 server 节点）：id 可能即 LLM tool-call id → 直接读 message 表
	if txt, state, ok := s.asyncResultFromMessage(parent, id); ok {
		return txt, state, "", true
	}
	// 子会话 turn id（委派子代理 / DSL 子轮次）
	if txt, ok := s.turnResultText(parent, id); ok {
		return txt, "done", "", true
	}
	return "", "", "", false
}

// nodeTaskResultDetail 由任务节点给出查询结果：
//   - 节点终态（done/error/cancelled）→ 节点摘要（既有行为：终态摘要随 mcp-tasks-report 回填；
//     文案映射在组件 `session.TaskNodeResultText`）；
//   - 非终态 → **层为权威**判定终态：层判终态但 message 表无结果行 → 明确文案
//     「异步结果已不可用」（不静默返回空）；否则读 message 表（已落 result = 完成；仅 async.pending = 在飞）。
//   - partial = 节点 Output 或委派子轮次的当前 assistant 文本（running 时返回已产出内容）。
func (s *Server) nodeTaskResultDetail(parent *turnCtx, n *TaskNode) (string, string, string, bool) {
	partial := s.nodePartial(parent, n)
	if text, state, ok := session.TaskNodeResultText(n); ok {
		return text, state, partial, true
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
			return txt, state, partial, true
		}
	}
	if layerTerminal {
		return session.AsyncResultUnavailableText, "error", partial, true
	}
	return "", "running", partial, true
}

// nodePartial 取任务节点已产出的部分输出（不改变终态判定）：
//   - 执行器经 `TaskTree.Progress` 上报的 Output 优先；
//   - 委派子轮次（kind=llm）→ 读子 turn 最后一条 assistant（流式增量回填同一主键 = 当前累计）。
func (s *Server) nodePartial(parent *turnCtx, n *TaskNode) string {
	if n == nil {
		return ""
	}
	if n.Output != "" {
		return n.Output
	}
	if n.Kind == TaskKindLLM && n.TurnID != "" {
		inst := n.InstanceID
		if inst == "" {
			inst = turnInstance(parent)
		}
		return s.partialTurnText(inst, n.TurnID)
	}
	return ""
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
	raw, ok := s.newSessionStore(parent.req.InstanceID).LoadToolContent(parent.req.Session, toolCallID)
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
	msgs := s.newSessionStore(parent.req.InstanceID).LoadMessages(turnID)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != "" {
			return msgs[i].Content, true
		}
	}
	return "", false
}
