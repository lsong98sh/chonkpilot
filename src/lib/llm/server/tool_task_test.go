// 任务结果读取能力单测（`plugin/v1/task.result` / `plugin/v1/task.wait` 的宿主实现）。
//
// 2026-11-08：`tool_result` 工具已删除（结果读取统一走 task 插件 `task_status`）；本文件保留
// 宿主侧读取/等待能力（`taskResult` / `waitTaskResult`）的语义覆盖，不再有工具执行/取消表。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestTaskResultFromMessage：结果读取优先读 message 表（按 tool_call_id 定位 role=tool
// 消息 → 解析 {call,result,async} 的 result 段）。
// 2026-09-18（用户决定）：不再经 gateway `tasks/status|result` 方法面。
func TestTaskResultFromMessage(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	store := s.newSessionStore("ins-test")
	if err := store.EnsureSession("s-tk-gr"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := store.EnsureTurn("t-tk-gr", "s-tk-gr"); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	tc := &turnCtx{server: s, req: StartReq{InstanceID: "ins-test", Session: "s-tk-gr", Turn: "t-tk-gr"},
		ctx: context.Background()}
	// 预置该调用的异步结果记录（role=tool，content = {call,result,async}；交付 1 的读取源）。
	persistAsyncToolRow(t, s, "t-tk-gr", "gw-async", "async file content", "completed")

	text, state, found := s.taskResult(tc, "gw-async")
	if !found || state != "done" {
		t.Fatalf("taskResult: found=%v state=%q", found, state)
	}
	if !strings.Contains(text, "async file content") {
		t.Fatalf("结果缺少 message 表内容: %q", text)
	}
}

// TestTaskResultNodeTerminalUnavailable：任务节点已终态但 message 表无结果行 →
// 明确文案「异步结果已不可用」（**不得静默返回空**）。
func TestTaskResultNodeTerminalUnavailable(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	if err := newSessionStore(s.bus, "ins-test").EnsureSession("s-tk-grv"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	// 建一个非终态节点（层判终态而 message 表无结果行 → 命中「不可用」分支）
	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-tk-grv", TurnID: "t-tk-grv",
		InstanceID: "ins-test", TopSession: "s-tk-grv", Name: "inflight", ToolCallID: "tc-unavail",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	s.tasks.setState(node.TaskID, TaskStatePending)
	waitTaskRow(t, s, "s-tk-grv", node.TaskID, func(map[string]any) bool { return true })
	// 层推进为终态（llm 内存节点仍非终态）→ 结果行缺失即「不可用」
	if err := s.taskLayer.Apply("task-done", jb(map[string]any{
		"task_id": node.TaskID, "state": TaskStateCancelled,
		"session_id": "s-tk-grv", "top_session": "s-tk-grv",
	})); err != nil {
		t.Fatalf("层推进终态: %v", err)
	}
	tc := &turnCtx{server: s, req: StartReq{InstanceID: "ins-test", Session: "s-tk-grv", Turn: "t-tk-grv"},
		ctx: context.Background()}

	text, state, found := s.taskResult(tc, node.TaskID)
	if !found || state != "error" {
		t.Fatalf("层终态应给 error 状态: found=%v state=%q text=%q", found, state, text)
	}
	if !strings.Contains(text, "异步结果已不可用") {
		t.Fatalf("层终态但结果行缺失应给明确文案: %q", text)
	}
}

// persistAsyncToolRow 预置一条 role=tool 异步结果记录（content = {call,result,async}）。
func persistAsyncToolRow(t *testing.T, s *Server, turnID, toolCallID, result, status string) {
	t.Helper()
	content := map[string]any{
		"call":   map[string]any{"tool_call_id": toolCallID, "name": "core_file_read"},
		"result": map[string]any{"content": result, "status": status},
		"async":  map[string]any{"pending": false, "task_id": "tk-async", "moved_at": "2026-09-18T00:00:00Z"},
	}
	b, _ := json.Marshal(content)
	if _, err := newSessionStore(s.bus, "ins-test").AppendMsgMap(turnID, map[string]any{
		"role": "tool", "content": string(b), "tool_call_id": toolCallID, "tool_call_status": status,
	}, ""); err != nil {
		t.Fatalf("persist async tool row: %v", err)
	}
}

// TestTaskResultUnknown：查询不存在的任务 → found=false（插件据此给「未找到任务」文案，不崩溃）。
func TestTaskResultUnknown(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	if err := newSessionStore(s.bus, "ins-test").EnsureSession("s-tk-gru"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	tc := &turnCtx{server: s, req: StartReq{InstanceID: "ins-test", Session: "s-tk-gru", Turn: "t-tk-gru"},
		ctx: context.Background()}

	if text, state, found := s.taskResult(tc, "no-such-task"); found || text != "" || state != "" {
		t.Fatalf("未知 id 应 found=false，got text=%q state=%q found=%v", text, state, found)
	}
}

// TestTaskResultWakesOnTaskDone（异步改造 §10.1）：结果等待不再固定轮询，而是订阅既有
// `task-done` 终态广播；任务 done 后立即返回（timeout 30s 不触发）。
func TestTaskResultWakesOnTaskDone(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	if err := newSessionStore(s.bus, "ins-test").EnsureSession("s-wake"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-wake", TurnID: "t-wake",
		InstanceID: "ins-test", TopSession: "s-wake", Name: "inflight",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	tc := &turnCtx{server: s, req: StartReq{InstanceID: "ins-test", Session: "s-wake", Turn: "t-wake"},
		ctx: context.Background()}

	go func() {
		time.Sleep(100 * time.Millisecond)
		s.tasks.done(node.TaskID, TaskStateDone, "async result done", "")
	}()

	start := time.Now()
	text, state, _, found := s.waitTaskResult(context.Background(), tc, node.TaskID, 30*time.Second)
	elapsed := time.Since(start)
	if !found || state != "done" {
		t.Fatalf("waitTaskResult: found=%v state=%q text=%q", found, state, text)
	}
	if text != "async result done" {
		t.Fatalf("结果 = %q, want async result done", text)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("应由 task-done 事件唤醒（非轮询超时），耗时 %v", elapsed)
	}
}