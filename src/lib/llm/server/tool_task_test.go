// task 型域工具（tool_stop / tool_result）单测
// （对齐 21-llm-server 任务编排 + 域工具契约 server/contracts/tools/{tool_stop,tool_result}.tool.md）：
// tool_stop 级联取消、tool_result 返回转后台结果。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestTaskToolStopCascade：tool_stop（LLM 工具）→ 级联定位任务子树 → 节点标 cancelled
// 广播 tasks.done → 异步子任务经**进程内 sink** 下发执行取消 → 回填父轮次 complete。
func TestTaskToolStopCascade(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	resetExecControl()
	startTurn(t, s, "s-tk-stp", "t-tk-stp")

	// 预建任务树：父（异步 gw-1）→ 子（异步 gw-2）
	parent, _ := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-tk-stp", TurnID: "t-tk-stp",
		InstanceID: "ins-test", TopSession: "s-tk-stp", Name: "parent", Purpose: "p",
	})
	s.tasks.setGwTask(parent.TaskID, "gw-1")
	child, _ := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-tk-stp", TurnID: "t-tk-stp",
		InstanceID: "ins-test", TopSession: "s-tk-stp", ParentID: parent.TaskID, Name: "child",
	})
	s.tasks.setGwTask(child.TaskID, "gw-2")
	fakeStopTaskID = parent.TaskID
	te.wait(t, "2 started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 2
	})

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-tk-stp", "turn": "t-tk-stp",
		"type": "text-user", "content": "stop please",
	}))
	evs := collectTurn(t, s.bus, "t-tk-stp", 10*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	// 子树 2 节点全部 cancelled（tasks.done 终态；tool_stop 自身节点 done 不算）
	te.wait(t, "2 cancelled", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		cancelled := 0
		for id, d := range te.done {
			if (id == parent.TaskID || id == child.TaskID) && d["state"] == TaskStateCancelled && d["success"] == false {
				cancelled++
			}
		}
		return cancelled == 2
	})
	// 执行侧取消级联下发（含 gateway 任务的父/子）
	got := execCancelledRefs()
	if len(got) != 2 || !containsStr(got, "gw-1") || !containsStr(got, "gw-2") {
		t.Fatalf("exec cancels=%v want [gw-1 gw-2]", got)
	}
}

// TestTaskToolResultFromMessage：tool_result → **读 message 表**（按 tool_call_id 定位 role=tool
// 消息 → 解析 {call,result,async} 的 result 段）→ 返回异步结果回填父轮次 complete。
// 2026-09-18（用户决定）：不再经 gateway `tasks/status|result` 方法面。
func TestTaskToolResultFromMessage(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	fakeGetResultID = "gw-async"
	startTurn(t, s, "s-tk-gr", "t-tk-gr")
	// 预置该调用的异步结果记录（role=tool，content = {call,result,async}；交付 1 的读取源）。
	persistAsyncToolRow(t, s, "t-tk-gr", "gw-async", "async file content", "completed")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-tk-gr", "turn": "t-tk-gr",
		"type": "text-user", "content": "get result please",
	}))
	evs := collectTurn(t, s.bus, "t-tk-gr", 10*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	text, _ := c["text"].(string)
	if !strings.Contains(text, "async file content") {
		t.Fatalf("tool_result missing async result: %q", text)
	}
}

// TestTaskToolResultNodeTerminalUnavailable：任务节点已终态但 message 表无结果行 →
// 明确文案「异步结果已不可用」（**不得静默返回空**）。
func TestTaskToolResultNodeTerminalUnavailable(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-tk-grv", "t-tk-grv")

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
	fakeGetResultID = node.TaskID

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-tk-grv", "turn": "t-tk-grv",
		"type": "text-user", "content": "get result please",
	}))
	evs := collectTurn(t, s.bus, "t-tk-grv", 10*time.Second)
	text := ""
	if c := lastComplete(evs); c != nil {
		text, _ = c["text"].(string)
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
	if err := newSessionStore(s.bus, "ins-test").AppendMsgMap(turnID, map[string]any{
		"role": "tool", "content": string(b), "tool_call_id": toolCallID, "tool_call_status": status,
	}); err != nil {
		t.Fatalf("persist async tool row: %v", err)
	}
}

// TestTaskToolResultUnknown：tool_result 查询不存在的任务 → 错误文本回填（不崩溃）。
func TestTaskToolResultUnknown(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	fakeGetResultID = "no-such-task"
	startTurn(t, s, "s-tk-gru", "t-tk-gru")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-tk-gru", "turn": "t-tk-gru",
		"type": "text-user", "content": "get result please",
	}))
	evs := collectTurn(t, s.bus, "t-tk-gru", 10*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	text, _ := c["text"].(string)
	if !strings.Contains(text, "未找到任务") {
		t.Fatalf("unknown id should report not found: %q", text)
	}
}
