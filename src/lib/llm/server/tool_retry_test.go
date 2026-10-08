// tool-retry 单测（61-消息一览 §4.2 tool-retry 🔶 → 完整）：消息区「重试」最后一条
// interrupted 工具 pair → 定位 → 重跑（gateway 二次调用）→ 终态广播 → 结果喂回该 turn
// 续轮（tool 结果进入下一条 LLM 消息）；已完成/无 interrupted pair → 明确错误；
// **进程被 kill 重启恢复**（2026-09-05 用户裁决）：内存无活跃 turn 且节点随进程丢失时，
// 从库重建上下文（该轮消息 assistant.tool_calls + 落库 interrupted 节点）续跑该工具结果，
// 不再报 "turn not running"。payload 多形态兼容：msg-ref 契约 {session, turn} 与前端现状
// {session_id, task_id=tool_call_id} 两种形态均覆盖。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// llmRecorder 记录 /chat/completions 每次请求收到的 messages，并以固定文本回复
// （断言"tool 结果进入下一条 LLM 消息"：录到的请求里应有 role=tool 且 tool_call_id 匹配）。
type llmRecorder struct {
	mu   sync.Mutex
	reqs [][]ChatMsg
}

func (r *llmRecorder) handler(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Messages []ChatMsg `json:"messages"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	r.mu.Lock()
	r.reqs = append(r.reqs, body.Messages)
	r.mu.Unlock()
	llmSSE(w, []string{
		sseChunk(map[string]any{"content": "retry-ok"}, ""),
		sseChunk(map[string]any{}, "stop"),
	})
}

func (r *llmRecorder) requests() [][]ChatMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]ChatMsg, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// toolCallWatch 记录 bus 上 gateway tools/call 请求（断言重试引发工具二次调用）。
type toolCallWatch struct {
	mu    sync.Mutex
	calls []mcpgateway.CallReq
}

func watchToolCalls(t *testing.T, bus mq.Bus) *toolCallWatch {
	w := &toolCallWatch{}
	sub, err := subRaw(bus, "mcp-tools-call", func(_ string, p []byte) {
		var req mcpgateway.CallReq
		if json.Unmarshal(p, &req) != nil {
			return
		}
		w.mu.Lock()
		w.calls = append(w.calls, req)
		w.mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return w
}

func (w *toolCallWatch) callsOf(tool string) []mcpgateway.CallReq {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []mcpgateway.CallReq
	for _, c := range w.calls {
		if c.Name == tool {
			out = append(out, c)
		}
	}
	return out
}

// watchTurn 先订阅再触发重试的轮次事件收集器（collectTurn 会阻塞等到终态，无法在
// 订阅后、触发前返回，故拆分：watchTurn 建立订阅即返回，waitComplete 再等终态）。
type watchTurnCollector struct {
	mu     sync.Mutex
	events []map[string]any
	done   chan struct{}
}

func watchTurn(t *testing.T, bus mq.Bus, turn string) *watchTurnCollector {
	t.Helper()
	c := &watchTurnCollector{done: make(chan struct{})}
	sub, err := subRaw(bus, ">", func(subject string, p []byte) {
		if !strings.HasPrefix(subject, "session-") {
			return
		}
		var m map[string]any
		if json.Unmarshal(p, &m) != nil {
			return
		}
		if m["turn"] != turn {
			return
		}
		c.mu.Lock()
		c.events = append(c.events, m)
		c.mu.Unlock()
		if subject == "session-complete" {
			select {
			case <-c.done:
			default:
				close(c.done)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return c
}

// waitComplete 等待该轮 session-complete（超时返回已收集事件，调用方自行判终态）。
func (c *watchTurnCollector) waitComplete(timeout time.Duration) []map[string]any {
	select {
	case <-c.done:
	case <-time.After(timeout):
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.events))
	copy(out, c.events)
	return out
}

// makeInterruptedNode 造一个 {session, turn} 上、状态 interrupted 的工具任务节点
// （复现进程中断遗留现场：pair 已落库 + 节点标 interrupted，可重试）。
func makeInterruptedNode(t *testing.T, s *Server, session, turn, toolCallID, tool string, args map[string]any) *TaskNode {
	t.Helper()
	node := &TaskNode{
		Tool:       tool,
		ToolCallID: toolCallID,
		TopSession: session,
		Kind:       taskKindOf(tool),
		SessionID:  session,
		TurnID:     turn,
		InstanceID: "ins-test",
		args:       args,
	}
	node.Name, node.Purpose, node.Simplified = taskDisplay(tool, args)
	node.Simplified = node.Name
	n, err := s.tasks.start(node)
	if err != nil {
		t.Fatal(err)
	}
	s.tasks.done(n.TaskID, TaskStateInterrupted, "", "进程中断遗留")
	return n
}

// persistInterruptedTurn 落库该 turn 的 user + assistant(tool_calls) 消息（历史冻结：
// 重试不改这些消息，只在其后回填同 tool-call-id 的 tool 结果）。
func persistInterruptedTurn(t *testing.T, s *Server, turn, userText, toolCallID, tool, argsJSON string) {
	t.Helper()
	st := newSessionStore(s.bus, "ins-test")
	if _, err := st.AppendFullKeyed(turn, ChatMsg{Role: "user", Kind: "text", Content: userText}, ""); err != nil {
		t.Fatal(err)
	}
	var assistant ChatMsg
	if err := json.Unmarshal(jb(map[string]any{
		"role": "assistant",
		"tool_calls": []any{map[string]any{
			"id": toolCallID, "type": "function",
			"function": map[string]any{"name": tool, "arguments": argsJSON},
		}},
	}), &assistant); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendFullKeyed(turn, assistant, ""); err != nil {
		t.Fatal(err)
	}
}

// retryReply 发 tool-retry 并返回 reply 载荷。
func retryReply(t *testing.T, s *Server, payload map[string]any) map[string]any {
	t.Helper()
	f := s.bus.Emit(context.Background(), "tool-retry", jb(payload))
	v := f.Wait()
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("no tool-retry reply for %+v", payload)
	}
	return res
}

// TestToolRetryLocateByFrontendPayload：前端现状形态 {session_id, task_id=LLM tool_call_id}
// → 定位命中 interrupted 节点 → 工具被二次调用（fakeGateway 同步返回 file content）→
// tasks.done 终态广播 → 结果喂回续轮 → tool 结果进入下一条 LLM 消息 → turn complete。
func TestToolRetryLocateByFrontendPayload(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	tcalls := watchToolCalls(t, s.bus)
	startTurn(t, s, "s-retry1", "t-retry1")

	persistInterruptedTurn(t, s, "t-retry1", "读取 a.txt", "call-r1", "core_file_read", `{"path":"a.txt"}`)
	node := makeInterruptedNode(t, s, "s-retry1", "t-retry1", "call-r1", "core_file_read", map[string]any{"path": "a.txt"})

	wt := watchTurn(t, s.bus, "t-retry1") // 先订阅，再触发重试，等续轮 complete
	res := retryReply(t, s, map[string]any{"session_id": "s-retry1", "task_id": "call-r1"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("tool-retry rejected: %+v", res)
	}
	if tid, _ := res["task_id"].(string); tid != node.TaskID {
		t.Fatalf("task_id=%v want %s", res["task_id"], node.TaskID)
	}

	// 续轮完成（complete 终态）
	evs := wt.waitComplete(10 * time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete after retry: evs=%+v", evs)
	}

	// 工具被二次调用（本次重试重跑，参数一致）
	calls := tcalls.callsOf("core_file_read")
	if len(calls) != 1 {
		t.Fatalf("core_file_read calls=%d want 1（重跑一次）: %+v", len(calls), calls)
	}
	if p, _ := calls[0].Arguments["path"].(string); p != "a.txt" {
		t.Fatalf("retry args=%+v want path=a.txt", calls[0].Arguments)
	}
	if calls[0].Session != "s-retry1" || calls[0].Turn != "t-retry1" {
		t.Fatalf("retry context session/turn=%q/%q want s-retry1/t-retry1（沿用原 pair）",
			calls[0].Session, calls[0].Turn)
	}

	// tool 结果进入下一条 LLM 消息（role=tool + 原 tool-call-id + 内容）
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("llm requests=%d want 1（重试续轮一次）", len(reqs))
	}
	var toolMsg *ChatMsg
	for i := range reqs[0] {
		if reqs[0][i].Role == "tool" && reqs[0][i].ToolCallID == "call-r1" {
			toolMsg = &reqs[0][i]
		}
	}
	if toolMsg == nil {
		t.Fatalf("no tool result in next llm request: %+v", reqs[0])
	}
	if !strings.Contains(toolMsg.Content, "file content") {
		t.Fatalf("tool result content=%q want file content", toolMsg.Content)
	}

	// 任务节点终态广播（tasks.done → GUI 桥 tool-pair 收尾）
	te.mu.Lock()
	dn := te.done[node.TaskID]
	te.mu.Unlock()
	if dn == nil || dn["state"] != TaskStateDone || dn["success"] != true {
		t.Fatalf("task done wrong: %+v", dn)
	}
	if rs, _ := dn["result_summary"].(string); !strings.Contains(rs, "file content") {
		t.Fatalf("result_summary=%q want file content", rs)
	}
}

// TestToolRetryLocateByContractPayload：msg-ref §4.2 记录契约形态 {session, turn}
// → 同一命中-重跑-续轮链路（无 tool_call_id/task_id 时按该轮最后一条 interrupted 定位）。
func TestToolRetryLocateByContractPayload(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	startTurn(t, s, "s-retry2", "t-retry2")

	persistInterruptedTurn(t, s, "t-retry2", "读取 b.txt", "call-r2", "core_file_read", `{"path":"b.txt"}`)
	node := makeInterruptedNode(t, s, "s-retry2", "t-retry2", "call-r2", "core_file_read", map[string]any{"path": "b.txt"})

	wt := watchTurn(t, s.bus, "t-retry2")
	res := retryReply(t, s, map[string]any{"session": "s-retry2", "turn": "t-retry2"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("tool-retry rejected (contract payload): %+v", res)
	}
	if tid, _ := res["task_id"].(string); tid != node.TaskID {
		t.Fatalf("task_id=%v want %s", res["task_id"], node.TaskID)
	}

	// 续轮完成（complete 终态）
	evs := wt.waitComplete(10 * time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete after retry: evs=%+v", evs)
	}
	te.mu.Lock()
	dn := te.done[node.TaskID]
	te.mu.Unlock()
	if dn == nil || dn["state"] != TaskStateDone {
		t.Fatalf("task done wrong: %+v", dn)
	}
}

// TestToolRetryRejectsDoneOrMissing：重试对已完成 pair / 无 interrupted pair 返回明确错误
// （{ok:false}，不触发工具调用、不改节点终态）。
func TestToolRetryRejectsDoneOrMissing(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	tcalls := watchToolCalls(t, s.bus)
	startTurn(t, s, "s-retry3", "t-retry3")

	// 已完成 pair（node 终态 done）：不可重试
	node := makeInterruptedNode(t, s, "s-retry3", "t-retry3", "call-done", "core_file_read", map[string]any{"path": "x"})
	s.tasks.done(node.TaskID, TaskStateDone, "已有结果", "")
	res := retryReply(t, s, map[string]any{"session_id": "s-retry3", "task_id": "call-done"})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("done pair retry should fail: %+v", res)
	}
	if errMsg, _ := res["error"].(string); !strings.Contains(errMsg, "tool not found") {
		t.Fatalf("done pair error=%q want tool not found", errMsg)
	}

	// 无任何 interrupted pair（未知 tool_call_id）
	res = retryReply(t, s, map[string]any{"session_id": "s-retry3", "task_id": "call-nope"})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("missing pair retry should fail: %+v", res)
	}
	if errMsg, _ := res["error"].(string); !strings.Contains(errMsg, "tool not found") {
		t.Fatalf("missing pair error=%q want tool not found", errMsg)
	}
	// 全程未触发任何 gateway 工具调用
	if n := len(tcalls.callsOf("core_file_read")); n != 0 {
		t.Fatalf("unexpected tool calls=%d want 0", n)
	}
	// done 节点保持 done（未被重试重置）
	if n := s.tasks.get(node.TaskID); n == nil || n.State != TaskStateDone {
		t.Fatalf("done node state mutated: %+v", n)
	}
}

// dropTurn 模拟进程重启/超时回收：内存活跃 turn 状态机移除（Close 释放 session 锁 + 注销 maps）。
func dropTurn(t *testing.T, s *Server, session, turn string) {
	t.Helper()
	s.mu.Lock()
	key := instKey("ins-test", turn)
	tc := s.turns[key]
	delete(s.turns, key)
	delete(s.busy, instKey("ins-test", session))
	s.mu.Unlock()
	if tc != nil {
		tc.Close()
	}
}

// dropNode 模拟进程重启：内存任务节点移除（库记录保留；恢复路径应从库重新登记同 task_id）。
func dropNode(t *testing.T, s *Server, taskID string) {
	t.Helper()
	s.tasks.mu.Lock()
	delete(s.tasks.nodes, taskID)
	delete(s.tasks.lastEmit, taskID)
	for i, id := range s.tasks.order {
		if id == taskID {
			s.tasks.order = append(s.tasks.order[:i], s.tasks.order[i+1:]...)
			break
		}
	}
	s.tasks.mu.Unlock()
}

// tasktreeRec 直连读 tasktree 库记录（A3 总线化：落库经 persist；断言同库直读）。
func tasktreeRec(t *testing.T, taskID string) data.Record {
	t.Helper()
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	var rec data.Record
	ok, err := prj.Table("tasktree").Get(taskID, &rec)
	if err != nil || !ok {
		t.Fatalf("tasktree get %s: ok=%v err=%v", taskID, ok, err)
	}
	return rec
}

// turnRec 直连读 turns 库记录。
func turnRec(t *testing.T, turnID string) data.Record {
	t.Helper()
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	var rec data.Record
	ok, err := prj.Table("turns").Get(turnID, &rec)
	if err != nil || !ok {
		t.Fatalf("turns get %s: ok=%v err=%v", turnID, ok, err)
	}
	return rec
}

// simulateRestartField 构造"进程被 kill → 重启清扫后"的库现场：
// startTurn 建轮次（内存 running）→ 落库该轮消息（user + 最后一条 assistant.tool_calls，
// 无 tool 结果）→ 节点落库 interrupted → 内存 turn/节点全部移除（模拟新进程空内存）。
// 返回 (session, turn, 中断节点)。
func simulateRestartField(t *testing.T, s *Server, session, turn, userText, toolCallID, tool, argsJSON string, args map[string]any) *TaskNode {
	t.Helper()
	startTurn(t, s, session, turn)
	persistInterruptedTurn(t, s, turn, userText, toolCallID, tool, argsJSON)
	node := makeInterruptedNode(t, s, session, turn, toolCallID, tool, args)
	// 重启清扫把该轮遗留 running turn 标 interrupted（对齐 data-session-cleanup-stale）
	st := newSessionStore(s.bus, "ins-test")
	if err := st.CompleteTurnTokens(turn, "interrupted", "interrupted", nil, nil); err != nil {
		t.Fatal(err)
	}
	dropNode(t, s, node.TaskID)
	dropTurn(t, s, session, turn)
	return node
}

// TestToolRetryRestartRecovery：进程被 kill → 重启 → tool-retry（进程重启恢复路径）——
// 内存无 turn 无节点，但库里有该轮消息（最后一条 assistant.tool_calls 无 tool 结果）+
// 节点落库 interrupted → 恢复：重建 turnCtx（沿用原 {session, turn}）→ gateway 二次调用
// 重跑该工具 → 原 tool-call-id 回填结果续轮（下一条 LLM 消息含 role=tool）→ tasks.done
// 广播 → 续轮 complete；节点/轮次落库终态一致。
func TestToolRetryRestartRecovery(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)
	tcalls := watchToolCalls(t, s.bus)

	session, turn := "s-recover", "t-recover"
	node := simulateRestartField(t, s, session, turn, "读取 z.txt", "call-rc", "core_file_read",
		`{"path":"z.txt"}`, map[string]any{"path": "z.txt"})
	if s.lookupTurn(turn) != nil || s.tasks.get(node.TaskID) != nil {
		t.Fatal("restart field should have empty memory turn/node")
	}

	wt := watchTurn(t, s.bus, turn) // 先订阅，再触发重试，等恢复续轮 complete
	res := retryReply(t, s, map[string]any{"session": session, "turn": turn})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("restart tool-retry rejected: %+v", res)
	}
	if tid, _ := res["task_id"].(string); tid != node.TaskID {
		t.Fatalf("task_id=%v want %s（沿用原节点）", res["task_id"], node.TaskID)
	}

	// 恢复续轮完成（complete 终态，session/turn 沿用原轮）
	evs := wt.waitComplete(10 * time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete after restart recovery: evs=%+v", evs)
	}

	// 工具被二次调用（恢复重跑，参数/上下文一致）
	calls := tcalls.callsOf("core_file_read")
	if len(calls) != 1 {
		t.Fatalf("core_file_read calls=%d want 1（恢复重跑一次）: %+v", len(calls), calls)
	}
	if p, _ := calls[0].Arguments["path"].(string); p != "z.txt" {
		t.Fatalf("retry args=%+v want path=z.txt", calls[0].Arguments)
	}
	if calls[0].Session != "s-recover" || calls[0].Turn != "t-recover" {
		t.Fatalf("retry context session/turn=%q/%q want s-recover/t-recover（沿用原轮）",
			calls[0].Session, calls[0].Turn)
	}

	// 续轮 LLM 收到 tool 结果：上下文 = 库消息（user + assistant.tool_calls）+ role=tool 结果
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("llm requests=%d want 1（恢复续轮一次）", len(reqs))
	}
	var toolMsg *ChatMsg
	var pairSeen bool
	for i := range reqs[0] {
		if reqs[0][i].Role == "tool" && reqs[0][i].ToolCallID == "call-rc" {
			toolMsg = &reqs[0][i]
		}
		if reqs[0][i].Role == "assistant" {
			for _, tc := range reqs[0][i].ToolCalls {
				if tc.ID == "call-rc" {
					pairSeen = true
				}
			}
		}
	}
	if !pairSeen {
		t.Fatalf("no assistant.tool_calls pair in continuation context: %+v", reqs[0])
	}
	if toolMsg == nil {
		t.Fatalf("no tool result in next llm request: %+v", reqs[0])
	}
	if !strings.Contains(toolMsg.Content, "file content") {
		t.Fatalf("tool result content=%q want file content", toolMsg.Content)
	}

	// 前端 tasks.done 广播（沿用原 task_id）
	te.mu.Lock()
	dn := te.done[node.TaskID]
	te.mu.Unlock()
	if dn == nil || dn["state"] != TaskStateDone || dn["success"] != true {
		t.Fatalf("task done wrong: %+v", dn)
	}

	// 节点/轮次落库终态一致：节点 done + 树/工具字段不丢；轮次 complete
	recDB := tasktreeRec(t, node.TaskID)
	if recDB["status"] != TaskStateDone {
		t.Fatalf("db node status=%v want done", recDB["status"])
	}
	if recDB["tool_call_id"] != "call-rc" || recDB["top_session"] != session {
		t.Fatalf("db node tree fields lost: %+v", recDB)
	}
	if recDB["finished_at"] == "" {
		t.Fatalf("db node finished_at missing: %+v", recDB)
	}
	tr := turnRec(t, turn)
	if tr["status"] != "complete" {
		t.Fatalf("db turn status=%v want complete", tr["status"])
	}
}

// TestToolRetryRestartErrors：进程重启恢复路径的明确报错（{ok:false}，不假死、不重跑）：
// ① 内存节点在但该轮库消息无 assistant.tool_calls pair（无续轮上下文）→ 明确错误；
// ② 库目标已 done（重启现场无 interrupted 节点）→ tool not found；③ 未知 pair → tool not found。
func TestToolRetryRestartErrors(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc((&llmRecorder{}).handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	tcalls := watchToolCalls(t, s.bus)

	// ① 死 turn：内存节点在（turn 被回收）但该轮消息未落 assistant.tool_calls → 无法恢复续轮
	startTurn(t, s, "s-err1", "t-err1")
	node1 := makeInterruptedNode(t, s, "s-err1", "t-err1", "call-e1", "core_file_read", map[string]any{"path": "e"})
	dropTurn(t, s, "s-err1", "t-err1")
	res := retryReply(t, s, map[string]any{"session": "s-err1", "turn": "t-err1"})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("no-context retry should fail: %+v", res)
	}
	if errMsg, _ := res["error"].(string); !strings.Contains(errMsg, "tool not found") ||
		!strings.Contains(errMsg, "消息记录不完整") {
		t.Fatalf("no-context error=%q want tool not found + 消息记录不完整", errMsg)
	}
	if n := s.tasks.get(node1.TaskID); n == nil || n.State != TaskStateInterrupted {
		t.Fatalf("node mutated after failed retry: %+v", n)
	}

	// ② 全重启：库目标已 done（无 interrupted 节点）→ tool not found
	session, turn := "s-err2", "t-err2"
	startTurn(t, s, session, turn)
	persistInterruptedTurn(t, s, turn, "读取 done.txt", "call-e2", "core_file_read", `{"path":"done.txt"}`)
	node2 := makeInterruptedNode(t, s, session, turn, "call-e2", "core_file_read", map[string]any{"path": "done.txt"})
	s.tasks.done(node2.TaskID, TaskStateDone, "已有结果", "")
	st := newSessionStore(s.bus, "ins-test")
	_ = st.CompleteTurnTokens(turn, "interrupted", "interrupted", nil, nil)
	dropNode(t, s, node2.TaskID)
	dropTurn(t, s, session, turn)
	res = retryReply(t, s, map[string]any{"session": session, "turn": turn})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("done-in-db retry should fail: %+v", res)
	}
	if errMsg, _ := res["error"].(string); !strings.Contains(errMsg, "tool not found") {
		t.Fatalf("done-in-db error=%q want tool not found", errMsg)
	}
	if recDB := tasktreeRec(t, node2.TaskID); recDB["status"] != TaskStateDone {
		t.Fatalf("db done node mutated after failed retry: %+v", recDB)
	}

	// ③ 未知 session/pair（库无任何节点）→ tool not found
	res = retryReply(t, s, map[string]any{"session_id": "s-unknown", "task_id": "call-nope"})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("unknown pair retry should fail: %+v", res)
	}
	if errMsg, _ := res["error"].(string); !strings.Contains(errMsg, "tool not found") {
		t.Fatalf("unknown error=%q want tool not found", errMsg)
	}
	// 全程未触发任何 gateway 工具调用
	if n := len(tcalls.callsOf("core_file_read")); n != 0 {
		t.Fatalf("unexpected tool calls=%d want 0", n)
	}
}
