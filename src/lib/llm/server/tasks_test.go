// 任务编排单测（对齐 21-llm-server）：任务树/级联/状态事件
// （tasks.started/updated/done，完整 task 结构含 tool_call_id/parent_id/top_session/kind/session_id/
// simplified/purpose —— purpose 为任务事件字段，其值来自 LLM 填的 tool_call_display_name）；
// 同步工具短生命周期；llm 型工具（llm_run）开子会话并广播 tasks.done；
// ask-user 补 multi/recommended。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// taskEvents 收集 tasks.started / tasks.updated / tasks.done（按 task_id 分组）。
type taskEvents struct {
	mu      sync.Mutex
	started map[string]map[string]any
	updated map[string][]map[string]any
	done    map[string]map[string]any
}

func watchTasks(t *testing.T, bus mq.Bus) *taskEvents {
	t.Helper()
	te := &taskEvents{
		started: map[string]map[string]any{},
		updated: map[string][]map[string]any{},
		done:    map[string]map[string]any{},
	}
	// 主题为相对 kebab（handler 收到去前缀相对主题），订阅 ">" 后按 task- 前缀过滤：
	// server 任务节点广播（TaskNode 全量含 kind）；gateway 完成回报
	// （task-done，payload 无 kind）不属任务树事件，跳过。
	sub, err := bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		p := v.Payload
		if !strings.HasPrefix(subject, "task-") {
			return nil
		}
		var m map[string]any
		if json.Unmarshal(p, &m) != nil {
			return nil
		}
		if _, ok := m["kind"].(string); !ok {
			return nil
		}
		id, _ := m["task_id"].(string)
		if id == "" {
			return nil
		}
		te.mu.Lock()
		defer te.mu.Unlock()
		switch {
		case strings.HasSuffix(subject, "task-started"):
			te.started[id] = m
		case strings.HasSuffix(subject, "task-updated"):
			te.updated[id] = append(te.updated[id], m)
		case strings.HasSuffix(subject, "task-done"):
			te.done[id] = m
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return te
}

func (te *taskEvents) wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertTaskIdentity 断言 tasks.started/done 载荷的公共身份/展示字段（§3.3 完整 task 结构）。
func assertTaskIdentity(t *testing.T, m map[string]any, tool, kind, turn, session string) {
	t.Helper()
	if m["tool"] != tool {
		t.Fatalf("tool=%v want %s (%+v)", m["tool"], tool, m)
	}
	if m["kind"] != kind {
		t.Fatalf("kind=%v want %s (%+v)", m["kind"], kind, m)
	}
	if m["turn_id"] != turn {
		t.Fatalf("turn_id=%v want %s", m["turn_id"], turn)
	}
	if m["session_id"] != session {
		t.Fatalf("session_id=%v want %s", m["session_id"], session)
	}
	if m["instance_id"] != "ins-test" {
		t.Fatalf("instance_id=%v want ins-test", m["instance_id"])
	}
	if m["tool_call_id"] == "" || m["task_id"] == "" || m["name"] == "" || m["simplified"] == "" {
		t.Fatalf("missing identity/display fields: %+v", m)
	}
	if m["started_at"] == "" {
		t.Fatalf("missing started_at: %+v", m)
	}
}

// TestTaskSyncToolLifecycle：同步工具短生命周期事件（tasks.started → tasks.done，无 pending 中间态）。
func TestTaskSyncToolLifecycle(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-tk-sync", "t-tk-sync")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-tk-sync", "turn": "t-tk-sync",
		"type": "text-user", "content": "sync tool please",
	}))
	evs := collectTurn(t, s.bus, "t-tk-sync", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("no complete: %+v", evs)
	}
	te.wait(t, "sync task started+done", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1 && len(te.done) == 1
	})
	te.mu.Lock()
	defer te.mu.Unlock()
	var st, dn map[string]any
	for id := range te.started {
		st = te.started[id]
		dn = te.done[id]
	}
	if st["state"] != TaskStateRunning {
		t.Fatalf("started state=%v want running: %+v", st["state"], st)
	}
	assertTaskIdentity(t, st, "core_file_read", TaskKindTool, "t-tk-sync", "s-tk-sync")
	// display：tool_call_display_name=sync → name/simplified=sync（事件字段 purpose 同值）
	if st["name"] != "sync" || st["purpose"] != "sync" || st["simplified"] != "sync" {
		t.Fatalf("display fields wrong: name=%v purpose=%v simplified=%v", st["name"], st["purpose"], st["simplified"])
	}
	// 终态：done + success + 结果回填
	if dn["state"] != TaskStateDone || dn["success"] != true {
		t.Fatalf("done wrong: %+v", dn)
	}
	if rs, _ := dn["result_summary"].(string); !strings.Contains(rs, "file content") {
		t.Fatalf("result_summary=%q want file content", rs)
	}
	if dn["finished_at"] == "" || dn["elapsed"] == nil {
		t.Fatalf("done missing finished_at/elapsed: %+v", dn)
	}
}

// TestTaskAsyncPendingLifecycle：工具转后台（pending）→ 节点状态变更（tasks.updated）
// → gateway 完成回报 → tasks.done（终态 done）+ 续轮 complete。
func TestTaskAsyncPendingLifecycle(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-tk-async", "t-tk-async")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-tk-async", "turn": "t-tk-async",
		"type": "text-user", "content": "async tool please",
	}))
	evs := collectTurn(t, s.bus, "t-tk-async", 10*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	te.wait(t, "async task started+done", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1 && len(te.done) == 1
	})
	te.mu.Lock()
	defer te.mu.Unlock()
	var st, dn map[string]any
	for id := range te.started {
		st = te.started[id]
		dn = te.done[id]
	}
	assertTaskIdentity(t, st, "core_file_read", TaskKindTool, "t-tk-async", "s-tk-async")
	// 中间态：转后台 → tasks.updated{state=pending}
	pendingSeen := false
	for _, ups := range te.updated {
		for _, u := range ups {
			if u["state"] == TaskStatePending {
				pendingSeen = true
			}
		}
	}
	if !pendingSeen {
		t.Fatalf("no pending transition: %+v", te.updated)
	}
	// 终态 done（gateway 完成回报同步任务节点）
	if dn["state"] != TaskStateDone || dn["success"] != true {
		t.Fatalf("done wrong: %+v", dn)
	}
	if rs, _ := dn["result_summary"].(string); rs == "" {
		t.Fatalf("result_summary empty: %+v", dn)
	}
	// 续轮结果包含异步结果文本（父轮次 final text）
	finalText, _ := c["text"].(string)
	if !strings.Contains(finalText, "async file content") {
		t.Fatalf("final text missing async result: %q", finalText)
	}
}

// TestTaskAskUserMultiRecommended：ask_user → tasks.started（kind=ask_user）
// → ask-user 广播含 options/multi/recommended/expires_at → ask-user-reply →
// tasks.done（result_summary=answer）+ turn 续轮 complete。
func TestTaskAskUserMultiRecommended(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-tk-ask", "t-tk-ask")

	var askEvData map[string]any
	var askMu sync.Mutex
	sub, err := s.bus.On("session-ask", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		askMu.Lock()
		askEvData = m
		askMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-tk-ask", "turn": "t-tk-ask",
		"type": "text-user", "content": "ask please",
	}))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		askMu.Lock()
		got := askEvData != nil
		askMu.Unlock()
		if got {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	askMu.Lock()
	askEv := askEvData
	askMu.Unlock()
	if askEv == nil {
		t.Fatal("no ask-user broadcast")
	}
	if askEv["question"] != "确认继续？" {
		t.Fatalf("question=%v", askEv["question"])
	}
	opts, ok := askEv["options"].([]any)
	if !ok || len(opts) != 2 {
		t.Fatalf("options=%v want 2 options", askEv["options"])
	}
	if multi, ok := askEv["multi"].(bool); !ok || multi {
		t.Fatalf("multi=%v want false", askEv["multi"])
	}
	// recommended 统一为数组（单值字符串 "y" → 单元素数组 ["y"]，I-41）
	if rec, ok := askEv["recommended"].([]any); !ok || len(rec) != 1 || rec[0] != "y" {
		t.Fatalf("recommended=%v want [y]", askEv["recommended"])
	}
	if askEv["expires_at"] == nil || askEv["ask_id"] == nil {
		t.Fatalf("missing expires_at/ask_id: %+v", askEv)
	}
	// 业务 payload 一律必带 instance_id（61 §0.1）：从 turn 归属取（本轮实例 ins-test）
	if askEv["instance_id"] != "ins-test" {
		t.Fatalf("session-ask 缺 instance_id: %+v", askEv)
	}
	// 应答 → 回填 LLM + ask 任务节点 done
	s.bus.Emit(context.Background(), "session-ask-reply", jb(map[string]any{
		"ask_id": askEv["ask_id"], "answer": "y",
	}))
	evs := collectTurn(t, s.bus, "t-tk-ask", 10*time.Second)
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	te.wait(t, "ask task done", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		for id, m := range te.started {
			if m["kind"] == TaskKindAskUser && te.done[id] != nil && te.done[id]["state"] == TaskStateDone {
				return true
			}
		}
		return false
	})
	te.mu.Lock()
	defer te.mu.Unlock()
	foundAsk := false
	for id, m := range te.started {
		if m["kind"] != TaskKindAskUser {
			continue
		}
		foundAsk = true
		if m["tool"] != "ask_user" || m["simplified"] != "确认继续？" {
			t.Fatalf("ask node wrong: %+v", m)
		}
		// I-50：ask 追问内容/选项随节点 args 广播（前端任务详情渲染问题内容）
		aa, _ := m["args"].(map[string]any)
		if aa == nil || aa["question"] != "确认继续？" {
			t.Fatalf("ask 节点缺 args.question（I-50）: %+v", m)
		}
		dn := te.done[id]
		if rs, _ := dn["result_summary"].(string); rs != "y" {
			t.Fatalf("ask result_summary=%q want y", rs)
		}
	}
	if !foundAsk {
		t.Fatal("no ask_user task node")
	}
}

// TestTaskStopCascadeCancel：task-stop → 级联定位任务子树（parent_id 遍历）→
// 全部节点标 cancelled 广播 tasks.done → 异步子任务经**进程内 sink**（层 → 执行池 CancelExec）
// 下发执行取消（2026-09-18 前经 gateway tasks/cancel 方法面）。
func TestTaskStopCascadeCancel(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	resetExecControl()

	// 直接建任务树：父（异步 gw-1）→ 子（异步 gw-2）→ 孙（无 gateway 任务）
	parent, _ := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-tk-c", TurnID: "t-tk-c",
		InstanceID: "ins-test", TopSession: "s-tk-c", Name: "parent", Purpose: "p",
	})
	s.tasks.setGwTask(parent.TaskID, "gw-1")
	child, _ := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-tk-c", TurnID: "t-tk-c",
		InstanceID: "ins-test", TopSession: "s-tk-c", ParentID: parent.TaskID, Name: "child",
	})
	s.tasks.setGwTask(child.TaskID, "gw-2")
	grandchild, _ := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-tk-c", TurnID: "t-tk-c",
		InstanceID: "ins-test", TopSession: "s-tk-c", ParentID: child.TaskID, Name: "gc",
	})
	te.wait(t, "3 started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 3
	})

	f := s.bus.Emit(context.Background(), "task-stop", jb(map[string]any{
		"req_id": "r-stop", "task_id": parent.TaskID,
	}))
	v := f.Wait()
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["cancelled"].(bool); !ok {
		t.Fatalf("task-stop ack wrong: %+v", res)
	}

	// 子树 3 节点全部 cancelled（tasks.done 终态）
	te.wait(t, "3 cancelled", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		if len(te.done) != 3 {
			return false
		}
		for _, d := range te.done {
			if d["state"] != TaskStateCancelled || d["success"] != false {
				return false
			}
		}
		return true
	})
	te.mu.Lock()
	childDone := te.done[child.TaskID]
	gcDone := te.done[grandchild.TaskID]
	te.mu.Unlock()
	if childDone["parent_id"] != parent.TaskID || childDone["top_session"] != "s-tk-c" {
		t.Fatalf("cancelled child lost tree fields: %+v", childDone)
	}
	if gcDone["parent_id"] != child.TaskID {
		t.Fatalf("grandchild parent_id lost: %+v", gcDone)
	}
	// 执行侧取消级联下发（含 gateway 任务的父/子；孙无 gateway 任务不下发）
	got := execCancelledRefs()
	if len(got) != 2 || !containsStr(got, "gw-1") || !containsStr(got, "gw-2") {
		t.Fatalf("exec cancels=%v want [gw-1 gw-2]", got)
	}
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestLLMCancelCascadeCancel（I-35）：turn 中有在飞工具（异步 pending）时发 llm-cancel →
// 该工具任务节点终态 cancelled，且经**进程内 sink** 下发执行取消（不再只终止轮次）。
func TestLLMCancelCascadeCancel(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	resetExecControl()
	startTurn(t, s, "s-cancel-cas", "t-cancel-cas")

	// turn 内在飞工具：登记 gateway 异步任务 id + 节点置 pending（非终态）
	node, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-cancel-cas", TurnID: "t-cancel-cas",
		InstanceID: "ins-test", TopSession: "s-cancel-cas", Name: "inflight",
	})
	if err != nil {
		t.Fatalf("start node: %v", err)
	}
	s.tasks.setGwTask(node.TaskID, "gw-inflight")
	s.tasks.setState(node.TaskID, TaskStatePending)
	te.wait(t, "inflight started", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.started) == 1
	})

	f := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cancel-cas", "turn": "t-cancel-cas",
	}))
	v := f.Wait()
	if v.Err() != nil {
		t.Fatalf("session-cancel: %v", v.Err())
	}

	te.wait(t, "inflight cancelled", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		d := te.done[node.TaskID]
		return d != nil && d["state"] == TaskStateCancelled
	})
	got := execCancelledRefs()
	if len(got) != 1 || got[0] != "gw-inflight" {
		t.Fatalf("exec cancels=%v want [gw-inflight]", got)
	}
}

// TestTaskBackgroundForwards：task-background {tool_call_id} → 经**进程内 sink**
// （层 exec 句柄 / tool_call_id）转执行池转后台 → 返回 {task_id}；缺 tool_call_id → 受理层直接拒绝
// （不下发执行侧）。2026-09-18：`tools/background` 方法面已移除。
func TestTaskBackgroundForwards(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	resetExecControl()

	// ① 命中：tool_call_id → 执行池返回 {task_id} 并原样回传前端
	v := s.bus.Emit(context.Background(), "task-background", jb(map[string]any{
		"instance_id": "ins-test", "tool_call_id": "tc-bg",
	})).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("task-background: %v", err)
	}
	res, _ := v.Result.(map[string]any)
	if tid, _ := res["task_id"].(string); tid != "tk-bg" {
		t.Fatalf("task-background ack wrong: %+v", res)
	}
	got := execDetachedRefs()
	if len(got) != 1 || got[0] != "tc-bg" {
		t.Fatalf("exec detached=%v want [tc-bg]", got)
	}

	// ② 缺 tool_call_id → errors 语义（v.Err 非空），且不下发执行侧
	bad := s.bus.Emit(context.Background(), "task-background", jb(map[string]any{
		"instance_id": "ins-test",
	})).Wait()
	if bad.Err() == nil {
		t.Fatalf("task-background without tool_call_id should error")
	}
	got = execDetachedRefs()
	if len(got) != 1 {
		t.Fatalf("exec pool must not be called without tool_call_id: %v", got)
	}

	// ③ 硬门控（G-17）：在飞节点工具非 manual（tools/list 缓存 = auto）→ errors，不下发执行侧
	s.gc.mu.Lock()
	s.gc.tools = []ToolDef{{Name: "core_script_run", Async: "auto"}}
	s.gc.mu.Unlock()
	if _, err := s.tasks.start(&TaskNode{
		Tool: "core_script_run", ToolCallID: "tc-bg-auto", Kind: TaskKindTool,
		TopSession: "s-bg", SessionID: "s-bg", TurnID: "t-bg", InstanceID: "ins-test",
	}); err != nil {
		t.Fatalf("tasks.start: %v", err)
	}
	rej := s.bus.Emit(context.Background(), "task-background", jb(map[string]any{
		"instance_id": "ins-test", "tool_call_id": "tc-bg-auto",
	})).Wait()
	if rej.Err() == nil {
		t.Fatalf("non-manual task-background should error")
	}
	got = execDetachedRefs()
	if len(got) != 1 {
		t.Fatalf("exec pool must not be called for non-manual: %v", got)
	}

	// ④ 在飞节点工具 manual（缓存 = manual）→ 放行转发
	s.gc.mu.Lock()
	s.gc.tools = []ToolDef{{Name: "core_script_run", Async: "manual"}}
	s.gc.mu.Unlock()
	okV := s.bus.Emit(context.Background(), "task-background", jb(map[string]any{
		"instance_id": "ins-test", "tool_call_id": "tc-bg-auto",
	})).Wait()
	if err := okV.Err(); err != nil {
		t.Fatalf("manual task-background: %v", err)
	}
	got = execDetachedRefs()
	if len(got) != 2 || got[1] != "tc-bg-auto" {
		t.Fatalf("exec detached=%v want [tc-bg tc-bg-auto]", got)
	}
}

// toolNodeStarted 从 tasks.started 快照取指定工具名的任务节点（未命中也 nil）。
func toolNodeStarted(te *taskEvents, tool string) map[string]any {
	te.mu.Lock()
	defer te.mu.Unlock()
	var out map[string]any
	for _, st := range te.started {
		if st["tool"] == tool {
			out = st
		}
	}
	return out
}

// TestTaskTreeSubSessionParent：子会话（llm_run 的 LLM 步骤经 runChildTurn 派生的子 turn，
// parents 非空）内产生的工具节点应挂到该子会话对应的 kind=llm 节点之下
// （parent_id = llm 节点 task_id、top_session = 顶层主会话），而非恒为顶层。
func TestTaskTreeSubSessionParent(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	// 注册 instance（子会话 llm-start 需 instance 已注册）；主会话 turn 仅用于确权，不喂消息
	startTurn(t, s, "s-topo", "t-topo")

	// 模拟 jobdsl.llmAction 时序：先建子会话对应的 llm 节点（SessionID=子会话、TopSession=主会话）
	sub := "job-topo-1"
	llmNode, err := s.tasks.start(&TaskNode{
		Tool: "llm_run", Kind: TaskKindLLM, SessionID: sub,
		TopSession: "s-topo", InstanceID: "ins-test", Name: "子步骤", Purpose: "子步骤",
	})
	if err != nil {
		t.Fatalf("start llm node: %v", err)
	}

	// 子会话内开轮（parents 非空 = 子 turn）→ 工具调用落节点
	v := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-sub", "instance_id": "ins-test", "session": sub, "turn": "t-sub",
		"parents": []string{"s-topo"},
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("sub session-start: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("sub session-start rejected: %+v", res)
	}
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": sub, "turn": "t-sub",
		"type": "text-user", "content": "sync tool please",
	}))
	evs := collectTurn(t, s.bus, "t-sub", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("sub turn no complete: %+v", evs)
	}
	te.wait(t, "sub tool started", func() bool { return toolNodeStarted(te, "core_file_read") != nil })

	tn := toolNodeStarted(te, "core_file_read")
	if tn["parent_id"] != llmNode.TaskID {
		t.Fatalf("子会话工具节点 parent_id=%v want %s（%+v）", tn["parent_id"], llmNode.TaskID, tn)
	}
	if tn["top_session"] != "s-topo" {
		t.Fatalf("子会话工具节点 top_session=%v want s-topo", tn["top_session"])
	}
}

// TestTaskTreeMainSessionTopLevel：主会话（parents 空）内产生的工具节点仍为顶层
// （parent_id 空、top_session = 本会话），原行为不变。
func TestTaskTreeMainSessionTopLevel(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-top", "t-top")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-top", "turn": "t-top",
		"type": "text-user", "content": "sync tool please",
	}))
	evs := collectTurn(t, s.bus, "t-top", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("main turn no complete: %+v", evs)
	}
	te.wait(t, "main tool started", func() bool { return toolNodeStarted(te, "core_file_read") != nil })

	tn := toolNodeStarted(te, "core_file_read")
	if pid, _ := tn["parent_id"].(string); pid != "" {
		t.Fatalf("主会话工具节点 parent_id 应空（顶层），实际=%v（%+v）", tn["parent_id"], tn)
	}
	if tn["top_session"] != "s-top" {
		t.Fatalf("主会话工具节点 top_session=%v want s-top", tn["top_session"])
	}
}

// TestTaskDisplayTitlePriority（I-38）：任务节点标题（name/purpose/simplified）取值优先级 ——
// ① 调用参数注入的展示名 tool_call_display_name 最优先；② llm 型显式 title 更明确、覆盖注入值；
// ③ 无注入回退工具契约名（调用方须传契约名；网关暴露名 self_<契约名> 由 turn.go 剥前缀）。
func TestTaskDisplayTitlePriority(t *testing.T) {
	// ① 有注入（llm_run 委派工具节点）→ title = 注入值
	name, purpose, simp := taskDisplay("llm_run", map[string]any{
		"script": `LLM "worker" "p"`, "tool_call_display_name": "派活说明",
	})
	if name != "派活说明" || purpose != "派活说明" || simp != "派活说明" {
		t.Fatalf("①注入优先失败: name=%q purpose=%q simplified=%q", name, purpose, simp)
	}
	// ② llm 型显式 title（比通用展示名更明确）→ 覆盖 ①
	name, _, _ = taskDisplay("llm_run", map[string]any{
		"tool_call_display_name": "委派", "title": "整理日志要点",
	})
	if name != "整理日志要点" {
		t.Fatalf("②llm 显式 title 覆盖失败: name=%q", name)
	}
	// ③ 无注入 → 回退工具契约名（purpose 空）
	name, purpose, simp = taskDisplay("llm_run", map[string]any{"script": "x"})
	if name != "llm_run" || simp != "llm_run" || purpose != "" {
		t.Fatalf("③回退契约名失败: name=%q purpose=%q simplified=%q", name, purpose, simp)
	}
	// 普通工具同优先级：注入优先 / 缺省回退契约名
	if name, _, _ = taskDisplay("core_file_read", map[string]any{"tool_call_display_name": "读取配置"}); name != "读取配置" {
		t.Fatalf("普通工具注入优先失败: name=%q", name)
	}
	if name, _, _ = taskDisplay("core_file_read", map[string]any{}); name != "core_file_read" {
		t.Fatalf("普通工具回退契约名失败: name=%q", name)
	}
}

// TestGatewayTaskDoneEmitsToolNotify（I-34）：调用方已离开（无等待 turn）的 gateway 异步任务
// 完成 → 广播 tool-notify（notice=completion）。payload 字段齐备且 instance_id 非空
// （由任务节点 session 归属取得；回落 gateway 回报载荷）。
func TestGatewayTaskDoneEmitsToolNotify(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	var mu sync.Mutex
	var got map[string]any
	sub, err := s.bus.On("tool-notify", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			mu.Lock()
			got = m
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tool-notify: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	// 任务节点（退后台场景：内存节点仍在，但无等待 turn）→ 完成回报按 gwTaskID 命中节点
	node, err := s.tasks.start(&TaskNode{
		Tool: "script_run", Kind: TaskKindTool, SessionID: "s-notify", TurnID: "t-notify",
		InstanceID: "ins-test", Name: "script_run", Simplified: "run script",
	})
	if err != nil {
		t.Fatalf("tasks.start: %v", err)
	}
	s.tasks.setGwTask(node.TaskID, "gw-notify-1")

	s.bus.Emit(context.Background(), "mcp-tasks-report", jb(map[string]any{
		"task_id": "gw-notify-1", "tool": "script_run", "state": "done",
		"result_summary": "ok", "session": "s-notify", "turn": "t-notify",
		"instance_id": "ins-test",
	}))

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		m := got
		mu.Unlock()
		if m != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting tool-notify")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	m := got
	mu.Unlock()

	if m["notice"] != "completion" {
		t.Fatalf("notice=%v want completion (%+v)", m["notice"], m)
	}
	if m["instance_id"] != "ins-test" {
		t.Fatalf("instance_id=%v want ins-test (%+v)", m["instance_id"], m)
	}
	if m["session_id"] != "s-notify" {
		t.Fatalf("session_id=%v want s-notify (%+v)", m["session_id"], m)
	}
	if m["task_id"] != node.TaskID {
		t.Fatalf("task_id=%v want %s (%+v)", m["task_id"], node.TaskID, m)
	}
	if msg, _ := m["message"].(string); msg == "" {
		t.Fatalf("message 必填: %+v", m)
	}
	if mid, _ := m["message_id"].(string); mid == "" {
		t.Fatalf("message_id 必填（前端去重）: %+v", m)
	}
}

// TestGatewayReportLocatesByToolCallIdempotent（I-62）：非 detached 在飞任务（未登记 gwTaskID）
// 的取消回报按 tool_call_id 兜底定位节点 → 落 cancelled 广播 task-done；重复回报（state=done）
// 不得二次广播、不得把 cancelled 覆盖成 done。
func TestGatewayReportLocatesByToolCallIdempotent(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	var mu sync.Mutex
	var states []string
	sub, err := s.bus.On("task-done", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		mu.Lock()
		states = append(states, str(m["state"]))
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe task-done: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	node, err := s.tasks.start(&TaskNode{
		Tool: "script_run", Kind: TaskKindTool, ToolCallID: "tc-loc",
		SessionID: "s-loc", TurnID: "t-loc", InstanceID: "ins-test", Name: "script_run",
	})
	if err != nil {
		t.Fatalf("tasks.start: %v", err)
	}
	// 首次回报：无 gwTaskID 登记 → 按 tool_call_id 兜底命中 → cancelled
	s.bus.Emit(context.Background(), "mcp-tasks-report", jb(map[string]any{
		"task_id": "tk-gw-loc", "tool_call_id": "tc-loc", "tool": "script_run",
		"state": "cancelled", "result_summary": "cancelled", "instance_id": "ins-test",
	}))
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(states)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting task-done")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 重复/迟到回报（state=done）→ 不二次广播、不覆盖 cancelled
	s.bus.Emit(context.Background(), "mcp-tasks-report", jb(map[string]any{
		"task_id": "tk-gw-loc", "tool_call_id": "tc-loc", "tool": "script_run",
		"state": "done", "result_summary": "late", "instance_id": "ins-test",
	}))
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	got := append([]string{}, states...)
	mu.Unlock()
	if len(got) != 1 || got[0] != TaskStateCancelled {
		t.Fatalf("task-done 广播 = %v, want 单次 cancelled", got)
	}
	if n := s.tasks.get(node.TaskID); n == nil || n.State != TaskStateCancelled {
		t.Fatalf("节点终态被覆盖: %+v", n)
	}
}

// TestTurnSyncCancelMarksTaskCancelled（I-62）：同步工具返回结构化取消标记
// （structuredContent.status=cancelled）→ 任务节点终态 = cancelled（非 done）、success=false；
// tool 记录状态落 cancelled。
func TestTurnSyncCancelMarksTaskCancelled(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-cancel", "t-cancel")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cancel", "turn": "t-cancel",
		"type": "text-user", "content": "cancel tool please",
	}))
	evs := collectTurn(t, s.bus, "t-cancel", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("no complete: %+v", evs)
	}
	var nodeID string
	te.wait(t, "cancel node done", func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		for id, m := range te.done {
			if m["tool_call_id"] == "tc-cancel" {
				nodeID = id
				return true
			}
		}
		return false
	})
	te.mu.Lock()
	d := te.done[nodeID]
	te.mu.Unlock()
	if d["state"] != TaskStateCancelled || d["success"] != false {
		t.Fatalf("取消后节点终态 = %v (success=%v), want cancelled/false: %+v", d["state"], d["success"], d)
	}
}

// TestAskRecommendedArrayNormalization：I-41 —— ask() 把 recommended 统一为**数组**
// （数组原样保留；单值字符串 → 单元素数组），对齐前端 AskUser 的 Array 渲染口径。
func TestAskRecommendedArrayNormalization(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	got := make(chan map[string]any, 4)
	// 相对主题 = session-ask（ask-user → session-ask，见 domainTopics）
	sub, err := s.bus.On("session-ask", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) == nil {
			got <- m
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	// ① 数组入参：原样透传为数组
	s.ask("t-rec", "tc-rec", map[string]any{
		"question":    "q",
		"recommended": []any{"a", "b"},
	}, "")
	// ② 单值字符串入参：兼容 → 单元素数组
	s.ask("t-rec", "tc-rec", map[string]any{
		"question":    "q",
		"recommended": "c",
	}, "")

	two := <-got
	if rec, _ := two["recommended"].([]any); len(rec) != 2 || rec[0] != "a" || rec[1] != "b" {
		t.Fatalf("array recommended=%v want [a b]", two["recommended"])
	}
	one := <-got
	if rec, _ := one["recommended"].([]any); len(rec) != 1 || rec[0] != "c" {
		t.Fatalf("string recommended=%v want [c]", one["recommended"])
	}
}
