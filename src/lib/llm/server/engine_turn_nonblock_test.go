// 2026-11-04 会话能力改造回归：
//   - 工具派发去阻塞：同步工具执行期间 turn 协调器仍可处理插话（此前 `gc.Call` 在协调线程上，
//     插话 / 取消只能等工具返回）；
//   - task_status / tool_result 的部分产出（running 时返回已产出内容）；
//   - task_llm 全能力委派子代理（子会话 / 子轮次 / kind=llm 任务节点 / detach）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pluginbridge "github.com/lsong98sh/chonkpilot-plugin-framework/bridge"
)

// nonBlockLLM 是「同步工具阻塞」用例的 LLM 桩。
func nonBlockLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		switch {
		case strings.Contains(text, "block tool please"):
			args := jb(map[string]any{"_block": true, "tool_call_display_name": "阻塞工具"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-block", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(text, "插话A"):
			llmSSE(w, []string{sseChunk(map[string]any{"content": "插话回答"}, "stop")})
		default:
			llmSSE(w, []string{sseChunk(map[string]any{"content": "主帧完成"}, "stop")})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestEngineTurnSyncToolDoesNotBlockCoordinator：同步工具执行中，插话仍能被引擎消费；
// 释放工具后主帧正常续轮收尾（同一 turn id）。
func TestEngineTurnSyncToolDoesNotBlockCoordinator(t *testing.T) {
	llm := nonBlockLLM(t)
	s := newTestServer(t, llm)
	release := make(chan struct{})
	setFakeGatewayBlock(release)
	t.Cleanup(func() { setFakeGatewayBlock(nil) })

	drive := func(session, turn string, log *eventLog) {
		engineTextTurn(s, session, turn, "block tool please")
		waitInsertable(t, s, session) // 工具已派发（inflight 登记）→ 可插入
		res := inboxAddInsertMQ(t, s, session, "【NB】插话A")
		if str(res["type"]) != "insert" {
			t.Fatalf("同步工具执行期应分类为 insert：%+v", res)
		}
		// 关键断言：工具仍在阻塞（release 未关）时，插入帧已经跑完并广播回答。
		engineWaitEvent(t, log, "插入帧回答（同步工具阻塞期间）", func(ev map[string]any) bool {
			if str(ev["type"]) != NotifyTypeText {
				return false
			}
			p, _ := ev["payload"].(map[string]any)
			return strings.Contains(str(p["text"]), "插话回答")
		})
		close(release)
	}
	rec := runEngineTurnCase(t, s, "s-nb", "t-nb", drive)

	if len(rec.toolRows) != 1 || rec.toolRows[0] != "tc-block|completed" {
		t.Fatalf("工具行应恰一条且 completed：%v", rec.toolRows)
	}
	joined := strings.Join(rec.msgs, "\n")
	if !strings.Contains(joined, `content="【NB】插话A"`) || !strings.Contains(joined, `content="插话回答"`) {
		t.Fatalf("插话内容应落库且被回答：%v", rec.msgs)
	}
	if !strings.Contains(rec.complete, "status=complete") || !strings.Contains(rec.complete, `text="主帧完成"`) {
		t.Fatalf("释放工具后主帧应正常收尾：%s", rec.complete)
	}
}

// TestTaskResultDetailPartialOutput：running 节点返回已产出的 Output；委派子轮次读当前
// assistant 文本；终态仍以摘要为准。
func TestTaskResultDetailPartialOutput(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	n, err := s.tasks.start(&TaskNode{
		Tool: "core_file_read", Kind: TaskKindTool, SessionID: "s-part", TurnID: "t-part",
		InstanceID: "ins-test", Name: "慢读",
	})
	if err != nil {
		t.Fatalf("tasks.start: %v", err)
	}
	s.tasks.tree.Progress(n.TaskID, 0.4, "partial-42")

	text, state, partial, found := s.taskResultDetail(nil, n.TaskID)
	if !found || state != "running" {
		t.Fatalf("running 节点应 found 且 state=running：found=%v state=%q text=%q", found, state, text)
	}
	if partial != "partial-42" {
		t.Fatalf("running 应返回部分产出，实际 %q", partial)
	}

	// 委派子轮次：kind=llm 节点头优先读子 turn 的当前 assistant（流式增量）。
	if err := newSessionStore(s.bus, "ins-test").EnsureSession("s-child"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := newSessionStore(s.bus, "ins-test").EnsureTurn("t-child", "s-child"); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	if _, err := newSessionStore(s.bus, "ins-test").AppendFullKeyed("t-child", ChatMsg{
		Role: "assistant", Content: "子代理已写一半",
	}, ""); err != nil {
		t.Fatalf("AppendFullKeyed: %v", err)
	}
	ln, err := s.tasks.start(&TaskNode{
		Tool: "task_llm", Kind: TaskKindLLM, SessionID: "s-child", TurnID: "t-child",
		InstanceID: "ins-test", Name: "子代理",
	})
	if err != nil {
		t.Fatalf("tasks.start(llm): %v", err)
	}
	_, state, partial, found = s.taskResultDetail(nil, ln.TaskID)
	if !found || state != "running" || partial != "子代理已写一半" {
		t.Fatalf("委派子轮次应返回当前 assistant：found=%v state=%q partial=%q", found, state, partial)
	}

	// 终态：摘要优先（部分产出不覆盖结果）。
	s.tasks.done(n.TaskID, TaskStateDone, "final-result", "")
	text, state, _, found = s.taskResultDetail(nil, n.TaskID)
	if !found || state != "done" || text != "final-result" {
		t.Fatalf("终态应以摘要为准：found=%v state=%q text=%q", found, state, text)
	}
}

// TestPluginAgentDelegate：`plugin/v1/agent.delegate` 起完整子轮次，返回结果与 kind=llm
// 任务节点；detach 立即返回 running，随后节点推进为 done。
func TestPluginAgentDelegate(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	store := newSessionStore(s.bus, "ins-test")
	if err := store.EnsureSession("s-del"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := store.EnsureTurn("t-del", "s-del"); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	// 父轮次（只用于能力定位；首个输入未投递 → loop 等待，不产生 LLM 调用）。
	parent := newTurnCtx(context.Background(), s, StartReq{
		InstanceID: "ins-test", Session: "s-del", Turn: "t-del", WorkDir: testWorkDir,
	})
	t.Cleanup(parent.Close)
	s.mu.Lock()
	s.registerTurnLocked(instKey("ins-test", "t-del"), parent)
	s.mu.Unlock()

	reg := pluginbridge.NewRegistry()
	s.SetPluginRegistry(reg)
	h, ok := reg.Lookup("plugin/v1/agent.delegate")
	if !ok {
		t.Fatal("plugin/v1/agent.delegate 未注册")
	}

	// ① 等待收尾（默认 wait_sec=0 = 一直等到子轮次结束）。
	raw, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-del", "session": "s-del",
		"tool_call_id": "tc-del", "prompt": "委派任务", "title": "子代理",
	})
	out, err := h(context.Background(), nil, raw)
	if err != nil {
		t.Fatalf("agent.delegate: %v", err)
	}
	res, _ := out.(map[string]any)
	if str(res["state"]) != "done" || str(res["text"]) != "委派任务" {
		t.Fatalf("委派应返回子代理最终回复：%+v", res)
	}
	subSession, subTurn, taskID := str(res["session_id"]), str(res["turn_id"]), str(res["task_id"])
	if subSession == "" || subTurn == "" || taskID == "" {
		t.Fatalf("委派应返回 session_id / turn_id / task_id：%+v", res)
	}
	// 子会话消息（user=prompt，assistant=回复）已落库。
	joined := ""
	for _, m := range store.LoadMessages(subTurn) {
		joined += m.Role + ":" + m.Content + "\n"
	}
	if !strings.Contains(joined, "user:委派任务") || !strings.Contains(joined, "assistant:委派任务") {
		t.Fatalf("子轮次消息未落库：%q", joined)
	}
	// kind=llm 任务节点：终态 done（后台收尾 goroutine 可能略晚于 handler 返回），
	// SessionID/TurnID 指向子轮次。
	var node *TaskNode
	deadline := time.Now().Add(5 * time.Second)
	for {
		node = s.tasks.get(taskID)
		if node != nil && node.State == TaskStateDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("委派任务节点未收尾：%+v", node)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if node.Kind != TaskKindLLM || node.SessionID != subSession || node.TurnID != subTurn {
		t.Fatalf("委派任务节点异常：%+v", node)
	}

	// ② detach：立即返回 running，后台继续；轮询至终态。
	raw2, _ := json.Marshal(map[string]any{
		"instance_id": "ins-test", "turn": "t-del", "session": "s-del",
		"tool_call_id": "tc-del2", "prompt": "后台任务", "detach": true,
	})
	out2, err := h(context.Background(), nil, raw2)
	if err != nil {
		t.Fatalf("agent.delegate(detach): %v", err)
	}
	res2, _ := out2.(map[string]any)
	if str(res2["state"]) != "running" {
		t.Fatalf("detach 应返回 running：%+v", res2)
	}
	id2 := str(res2["task_id"])
	deadline = time.Now().Add(10 * time.Second)
	for {
		_, state, _, found := s.taskResultDetail(parent, id2)
		if found && state != "running" {
			if state != "done" {
				t.Fatalf("后台委派终态应为 done，实际 %q", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("后台委派未在期限内收尾（state=%q found=%v）", state, found)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestDelegatedTurnLifecycle：委派子轮次的生命周期独立于父轮次（父轮次收尾不取消它），
// 取消经 `task_stop` 的 `closeDelegatedTurn` 显式下发。
func TestDelegatedTurnLifecycle(t *testing.T) {
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		llmSSE(w, []string{sseChunk(map[string]any{"content": "慢完成"}, "stop")})
	}))
	t.Cleanup(srv.Close)
	s := newTestServer(t, srv)
	registerTestInstance(t, s)

	if err := newSessionStore(s.bus, "ins-test").EnsureSession("s-life"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := newSessionStore(s.bus, "ins-test").EnsureTurn("t-life", "s-life"); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	parent := newTurnCtx(context.Background(), s, StartReq{
		InstanceID: "ins-test", Session: "s-life", Turn: "t-life", WorkDir: testWorkDir,
	})

	h, err := s.startChildTurn(context.Background(), parent.req, "s-life-sub", "慢慢来", "")
	if err != nil {
		t.Fatalf("startChildTurn: %v", err)
	}
	// 父轮次收尾（正常完成）→ 子轮次**不受影响**（独立 ctx）。
	parent.Close()
	select {
	case <-h.tc.Done():
		t.Fatal("父轮次收尾不应取消委派子轮次")
	case <-time.After(150 * time.Millisecond):
	}
	// 显式取消（task_stop 链路）→ 子轮次收尾。
	if !s.closeDelegatedTurn("ins-test", h.Turn) {
		t.Fatalf("closeDelegatedTurn 未命中子轮次 %s", h.Turn)
	}
	select {
	case <-h.tc.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("显式取消后子轮次未收尾")
	}
	close(gate)
}
