// 单聊（子聊天）域：域工具 handler + `subchat-*` 业务消息的驱动/断言（SUBCHAT-2 阶段 B-2）。
//
// 测试准则（项目规则 / 50-测试体系 §1）：**发送 mq 消息驱动、监听 mq 消息断言** —— 驱动面 =
// `domain-tool-call`（域工具真实回调主题，§5.1.1）/ `subchat-list` / `subchat-finish`
// （client→server 方法面）/ `session-start`（llm-start）；断言面 = 监听 `subchat-opened` /
// `subchat-state` 事件 + `data-session-*`（会话行）+ 首轮消息。消息面形状只依据
// `docs/spec/60-reference/61-消息一览.md`（唯一准则）。
//
// 单聊工具的两条路径都覆盖：① 经真实内嵌 gateway（`newTestServerMCP`，`subchat_open`
// 暴露名 → gateway `route.Original` → 域回调）；② 经域工具回调主题直驱（`domain-tool-call`）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/lsong98sh/chonkpilot-framework/host/core/mq"
	"github.com/lsong98sh/chonkpilot-framework/host/core/msgkeys"
)

// busRecorder 订阅全通配（相对主题）并按主题归集事件（mq 监听断言）。
type busRecorder struct {
	mu  sync.Mutex
	evs map[string][]map[string]any
}

func newBusRecorder(t *testing.T, bus mq.Bus) *busRecorder {
	t.Helper()
	r := &busRecorder{evs: map[string][]map[string]any{}}
	_, err := bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		r.mu.Lock()
		r.evs[subject] = append(r.evs[subject], m)
		r.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("busRecorder subscribe: %v", err)
	}
	return r
}

// wait 轮询等待某主题的首条事件（超时 = 红）；pred 非 nil 时须同时满足谓词。
func (r *busRecorder) wait(t *testing.T, subject string, pred func(map[string]any) bool, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, m := range r.evs[subject] {
			if pred == nil || pred(m) {
				r.mu.Unlock()
				return m
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	got := append([]map[string]any{}, r.evs[subject]...)
	r.mu.Unlock()
	t.Fatalf("超时未观测到事件 %s（pred 未满足）；已收 %d 条: %+v", subject, len(got), got)
	return nil
}

// snapshot 返回某主题下已观测事件的副本（并发安全；供逐条断言用）。
func (r *busRecorder) snapshot(subject string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any{}, r.evs[subject]...)
}

// sessionOf 读会话行（断言 kind / lifecycle / 固定参数 / purpose）。
func sessionOf(t *testing.T, s *Server, id string) facade.Session {
	t.Helper()
	resp, err := s.cfg.SessionGet(facade.SessionGetRequest{
		InstanceID: "ins-test", SessionID: id, Scope: s.cfgScope("ins-test"),
		DialogID: s.dialogOf("ins-test", id),
	})
	if err != nil || !resp.Found {
		t.Fatalf("SessionGet(%s): found=%v err=%v", id, resp.Found, err)
	}
	return resp.Session
}

// emitDomainTool 驱动一次**已迁 task 插件**的会话编排工具（2026-11-08：subchat_* /
// ask_user / next_run 不再走 core 的域工具回调；测试按宿主能力语义直接执行并建节点，
// 与迁移前 domainExecute 的副作用同形）。
func emitDomainTool(t *testing.T, s *Server, tool string, args map[string]any, turn, session, toolCallID, purpose string) map[string]any {
	t.Helper()
	tc := s.lookupTurnIn("ins-test", turn)
	if tc == nil {
		t.Fatalf("domain-tool-call(%s): turn 不存在", tool)
	}
	node, derr := s.domainNode(tc, toolCallID, tool, args, purpose)
	if derr != nil {
		t.Fatalf("domain-tool-call(%s): 建节点失败: %v", tool, derr)
	}
	text, isErr := execMovedDomainToolForTest(s, tc, toolCallID, tool, args, purpose)
	if !isErr && tool != "ask_user" {
		s.tasks.done(node.TaskID, TaskStateDone, text, "")
	}
	return map[string]any{
		"resultType": "complete",
		"content":    []any{map[string]any{"type": "text", "text": text}},
		"isError":    false, // 迁移前：next_run / subchat_* 的 isErr 不向 gateway 传递
	}
}

// domainResultText 取域工具回调结果文本（content[0].text）。
func domainResultText(t *testing.T, res map[string]any) string {
	t.Helper()
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("域工具结果无 content：%+v", res)
	}
	m, _ := content[0].(map[string]any)
	text, _ := m["text"].(string)
	return text
}

// TestSubChatOpenTool（B2-2 · 单聊 §3.4.1 / §10 Q2 / D30）：`subchat_open` 经域工具回调驱动，
// 断言 ① 建会话（kind=sub_chat + parent_id=主会话 + 固定参数 agent/scenario_id/llm +
// lifecycle=chatting + purpose=注入通道值）；② 交接内容以 **user 角色**写为首条消息（只落一次）；
// ③ 发 `subchat-opened`（instance_id=父窗口实例 + title + purpose）；④ 异步首轮（子 agent 自动
// 先开口，产出 assistant）；⑤ 返回 `{ok, session_id}`（终态）。
func TestSubChatOpenTool(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-open", "t-open")
	rec := newBusRecorder(t, s.bus)

	const purpose = "需求讨论"
	handoff := "我想要一份落地方案讨论"
	res := emitDomainTool(t, s, "subchat_open", map[string]any{
		"agent": "ag-1", "scenario_id": "scn-1", "llm": "llm-1", "prompt": handoff,
	}, "t-open", "s-main-open", "tc-so", purpose)

	// ⑤ 结果 = {ok, session_id}
	text := domainResultText(t, res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("subchat_open 结果非 JSON：%q err=%v", text, err)
	}
	if out["ok"] != true {
		t.Fatalf("subchat_open 应 ok=true：%q", text)
	}
	if out["mode"] != "single" {
		t.Fatalf("subchat_open 单聊应返回 mode=single：%q", text)
	}
	sid, _ := out["session_id"].(string)
	if sid == "" {
		t.Fatalf("subchat_open 应返回 session_id：%q", text)
	}

	// ① 会话行（固定参数 + lifecycle + purpose）
	sess := sessionOf(t, s, sid)
	if sess.Kind != facade.SessionKindSubChat || sess.ParentID != "s-main-open" ||
		sess.Agent != "ag-1" || sess.ScenarioID != "scn-1" || sess.LLM != "llm-1" ||
		sess.Lifecycle != facade.SessionLifecycleChatting {
		t.Fatalf("子聊天会话行不符：%+v", sess)
	}
	if sess.Purpose != purpose {
		t.Fatalf("sessions.purpose 应为注入通道值 %q：%q", purpose, sess.Purpose)
	}

	// ② 交接内容 = 首轮首条 **user** 消息
	turnID := newSessionStore(s.bus, "ins-test").LatestTurnID(sid)
	if turnID == "" {
		t.Fatal("子聊天应有首轮")
	}
	msgs := newSessionStore(s.bus, "ins-test").LoadMessages(turnID)
	if len(msgs) == 0 || msgs[0].Role != "user" || msgs[0].Content != handoff {
		t.Fatalf("首条消息应为 user 且等于交接内容：%+v", msgs)
	}
	userCount := 0
	for _, m := range msgs {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("交接内容应只落一条 user 消息（不重复落库）：%+v", msgs)
	}

	// ③ subchat-opened（instance_id = 父窗口实例；title/purpose 齐）
	opened := rec.wait(t, "subchat-opened", func(m map[string]any) bool { return str(m["session_id"]) == sid }, 3*time.Second)
	if str(opened["instance_id"]) != "ins-test" || str(opened["parent_session_id"]) != "s-main-open" {
		t.Fatalf("subchat-opened 归属字段不符：%+v", opened)
	}
	if str(opened["title"]) != purpose || str(opened["purpose"]) != purpose {
		t.Fatalf("subchat-opened title/purpose 应 = 子聊天名字：%+v", opened)
	}

	// ④ 异步首轮：子 agent 自动先开口（新轮次完成 → session-complete）
	rec.wait(t, "session-complete", func(m map[string]any) bool { return str(m["session"]) == sid }, 10*time.Second)
	done := newSessionStore(s.bus, "ins-test").LoadMessages(turnID)
	hasAssistant := false
	for _, m := range done {
		if m.Role == "assistant" && m.Content != "" {
			hasAssistant = true
		}
	}
	if !hasAssistant {
		t.Fatalf("子聊天首轮应有子 agent 产出（自动先开口）：%+v", done)
	}
}

// TestSubChatOpenToolArgsMissing：入参缺失 → `{ok:false, error}`（**必须明确回给 LLM**）。
func TestSubChatOpenToolArgsMissing(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-bad", "t-bad")

	res := emitDomainTool(t, s, "subchat_open", map[string]any{
		"agent": "ag-1", "scenario_id": "scn-1", "llm": "llm-1", // 缺 prompt
	}, "t-bad", "s-main-bad", "tc-bad", "目的")
	text := domainResultText(t, res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil || out["ok"] != false || str(out["error"]) == "" {
		t.Fatalf("缺参应返回 {ok:false, error}（明确回给 LLM）：%q", text)
	}
	if n := s.tasks.findByToolCall("ins-test", "tc-bad"); n == nil || n.State != TaskStateError {
		t.Fatalf("缺参应落任务节点 error：%+v", n)
	}
}

// TestSubChatFinishTool（B2-3 · §3.4.2）：子聊天会话内调 `subchat_finish` → lifecycle=ready
// （**建议态**）+ 发 `subchat-state`；返回 {ok,session_id,lifecycle}；不关窗、不回流。
func TestSubChatFinishTool(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	ensureTestSession(t, s, "s-top-fin", facade.SessionEnsureRequest{})
	ensureTestSession(t, s, "sub-fin", facade.SessionEnsureRequest{
		ParentSessionID: "s-top-fin", Kind: facade.SessionKindSubChat,
		Lifecycle: facade.SessionLifecycleChatting, Agent: "a", ScenarioID: "sc", LLM: "l",
	})
	startTurn(t, s, "sub-fin", "t-fin")
	rec := newBusRecorder(t, s.bus)

	res := emitDomainTool(t, s, "subchat_finish", map[string]any{}, "t-fin", "sub-fin", "tc-fin", "目的")
	if boolOf(res["isError"]) {
		t.Fatalf("subchat_finish 不应失败：%+v", res)
	}
	text := domainResultText(t, res)
	if !strings.Contains(text, `"ok":true`) {
		t.Fatalf("subchat_finish 应返回 ok:true：%q", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil ||
		str(out["session_id"]) != "sub-fin" || str(out["lifecycle"]) != facade.SessionLifecycleReady {
		t.Fatalf("subchat_finish 应返回 session_id/lifecycle=ready：%q", text)
	}
	if got := sessionOf(t, s, "sub-fin").Lifecycle; got != facade.SessionLifecycleReady {
		t.Fatalf("subchat_finish 应置 ready，got %q", got)
	}
	ev := rec.wait(t, "subchat-state", func(m map[string]any) bool { return str(m["session_id"]) == "sub-fin" }, 3*time.Second)
	if str(ev["lifecycle"]) != facade.SessionLifecycleReady || str(ev["parent_session_id"]) != "s-top-fin" {
		t.Fatalf("subchat-state 不符：%+v", ev)
	}
	if _, has := ev["instance_id"]; has {
		t.Fatalf("subchat-state 应为广播（不带 instance_id）：%+v", ev)
	}
}

// TestSubChatList（B2-4 · §3.4.4/§3.4.6/§3.4.7）：带 parent_id → 该父会话的**全部**子聊天（含
// finished）+ parent_session_title；缺省 → **全局未结束**（lifecycle ∈ {chatting, ready}）。
func TestSubChatList(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	ensureTestSession(t, s, "s-top-a", facade.SessionEnsureRequest{})
	ensureTestSession(t, s, "s-top-b", facade.SessionEnsureRequest{})
	ensureTestSession(t, s, "sub-a1", facade.SessionEnsureRequest{
		ParentSessionID: "s-top-a", Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleChatting, Purpose: "A1"})
	ensureTestSession(t, s, "sub-a2", facade.SessionEnsureRequest{
		ParentSessionID: "s-top-a", Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleFinished, Purpose: "A2"})
	ensureTestSession(t, s, "sub-b1", facade.SessionEnsureRequest{
		ParentSessionID: "s-top-b", Kind: facade.SessionKindGroupChat, Lifecycle: facade.SessionLifecycleReady, Purpose: "B1"})

	list := func(payload map[string]any) []any {
		t.Helper()
		v := s.bus.Emit(context.Background(), "subchat-list", jb(payload)).Wait()
		if v.Err() != nil {
			t.Fatalf("subchat-list: %v", v.Err())
		}
		res, _ := v.Result.(map[string]any)
		if err, has := res["error"]; has {
			t.Fatalf("subchat-list 失败：%v", err)
		}
		out, _ := res[msgkeys.SubchatListResultList].([]any)
		return out
	}
	bySession := func(items []any) map[string]map[string]any {
		m := map[string]map[string]any{}
		for _, it := range items {
			e, _ := it.(map[string]any)
			m[str(e["session_id"])] = e
		}
		return m
	}

	// 带 parent_id → 全部子聊天（含 finished）
	got := bySession(list(map[string]any{"instance_id": "ins-test", "parent_id": "s-top-a"}))
	if len(got) != 2 || got["sub-a1"] == nil || got["sub-a2"] == nil {
		t.Fatalf("带 parent_id 应返回该父全部子聊天（含 finished）：%+v", got)
	}
	if str(got["sub-a1"]["lifecycle"]) != facade.SessionLifecycleChatting || str(got["sub-a1"]["purpose"]) != "A1" {
		t.Fatalf("子聊天项字段不符：%+v", got["sub-a1"])
	}
	if str(got["sub-a1"]["parent_session_id"]) != "s-top-a" || str(got["sub-a1"]["parent_session_title"]) != "s-top-a" {
		t.Fatalf("补 parent_session_title 不符：%+v", got["sub-a1"])
	}

	// 缺省 → 全局未结束（finished 不入列）
	all := bySession(list(map[string]any{"instance_id": "ins-test"}))
	if all["sub-a1"] == nil || all["sub-b1"] == nil {
		t.Fatalf("全局应含未结束子聊天（chatting/ready）：%+v", all)
	}
	if all["sub-a2"] != nil {
		t.Fatalf("全局不应含 finished 子聊天：%+v", all)
	}
}

// TestSubChatFinishMessage（B2-4 · §3.4.5/§3.8 F5 · D25）：`subchat-finish`（client→server）→
// lifecycle=finished + 发 `subchat-state` + 返回 `{ok:true}`（不区分正常完成 / 强制中止）。
func TestSubChatFinishMessage(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	ensureTestSession(t, s, "s-top-fin2", facade.SessionEnsureRequest{})
	ensureTestSession(t, s, "sub-fin2", facade.SessionEnsureRequest{
		ParentSessionID: "s-top-fin2", Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleReady})
	rec := newBusRecorder(t, s.bus)

	v := s.bus.Emit(context.Background(), "subchat-finish", jb(map[string]any{
		"instance_id": "ins-test", "session_id": "sub-fin2",
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("subchat-finish: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res[msgkeys.SubchatFinishResultOk] != true {
		t.Fatalf("subchat-finish 应返回 {ok:true}：%+v", res)
	}
	if got := sessionOf(t, s, "sub-fin2").Lifecycle; got != facade.SessionLifecycleFinished {
		t.Fatalf("subchat-finish 应置 finished，got %q", got)
	}
	ev := rec.wait(t, "subchat-state", func(m map[string]any) bool { return str(m["session_id"]) == "sub-fin2" }, 3*time.Second)
	if str(ev["lifecycle"]) != facade.SessionLifecycleFinished {
		t.Fatalf("subchat-state 应为 finished：%+v", ev)
	}
}

// TestSubChatReadyToChattingOnNewTurn（B2-5 · §3.7/§3.5）：子聊天新一轮开始处（llm-start
// → newTurnCtx）若 lifecycle == ready → 置回 chatting + 发 `subchat-state`（撤销点亮）。
func TestSubChatReadyToChattingOnNewTurn(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	ensureTestSession(t, s, "s-top-rw", facade.SessionEnsureRequest{})
	ensureTestSession(t, s, "sub-rw", facade.SessionEnsureRequest{
		ParentSessionID: "s-top-rw", Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleReady})
	rec := newBusRecorder(t, s.bus)

	// llm-start（新一轮）
	v := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-rw", "instance_id": "ins-test", "session": "sub-rw", "turn": "t-rw",
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("session-start: %v", v.Err())
	}
	if res, _ := v.Result.(map[string]any); res["accepted"] != true {
		t.Fatalf("session-start 未受理：%+v", res)
	}
	if got := sessionOf(t, s, "sub-rw").Lifecycle; got != facade.SessionLifecycleChatting {
		t.Fatalf("新一轮开始应把 ready 回写 chatting，got %q", got)
	}
	ev := rec.wait(t, "subchat-state", func(m map[string]any) bool { return str(m["session_id"]) == "sub-rw" }, 3*time.Second)
	if str(ev["lifecycle"]) != facade.SessionLifecycleChatting {
		t.Fatalf("回写应发 subchat-state{chatting}：%+v", ev)
	}
}

// TestSubChatOpenViaGateway（B2-2 端到端 · 真实内嵌 gateway）：LLM 调 `subchat_open`
// （P0a 2026-10-29 起暴露名 = 契约名）→ gateway `route.Original` → 域回调 → handler 建会话 + 发 `subchat-opened`；
// 主轮**不挂起**（工具结果同步回喂、本轮照常 complete）。
func TestSubChatOpenViaGateway(t *testing.T) {
	// 仅对主会话的首条提问发起一次 subchat_open 工具调用；其余（子聊天首轮 / 回填后的收尾）
	// 一律回文本 —— 避免子聊天首轮再次落到本分支而递归开子聊天。
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		hasToolResult, wantOpen := false, false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasToolResult = true
			}
			if m.Role == "user" && strings.Contains(m.Content, "请开个子聊天") {
				wantOpen = true
			}
		}
		if wantOpen && !hasToolResult {
			args := jb(map[string]any{
				"agent": "ag-1", "scenario_id": "scn-1", "llm": "llm-1",
				"prompt": "我想要一份方案", "tool_call_display_name": "需求讨论",
			})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-so-gw", "type": "function",
					"function": map[string]any{"name": "subchat_open", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "已开启子聊天"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()
	s := newTestServerMCP(t, llm)
	startTurn(t, s, "s-main-gw", "t-gw")
	rec := newBusRecorder(t, s.bus)

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-main-gw", "turn": "t-gw",
		"type": "text-user", "content": "请开个子聊天",
	}))
	evs := collectTurn(t, s.bus, "t-gw", 15*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("主轮应照常 complete（不挂起）：%+v", evs)
	}
	if text, _ := c["text"].(string); !strings.Contains(text, "已开启子聊天") {
		t.Fatalf("主轮终态文本不符：%q", text)
	}
	// 工具结果（role=tool 落库）= {ok, session_id}
	toolText := toolResultContent(t, s, sessionOf(t, s, "s-main-gw").ID, "tc-so-gw")
	var out map[string]any
	if err := json.Unmarshal([]byte(toolText), &out); err != nil || out["ok"] != true {
		t.Fatalf("subchat_open 工具结果应 ok=true：%q err=%v", toolText, err)
	}
	sid, _ := out["session_id"].(string)
	if sid == "" {
		t.Fatalf("应返回 session_id：%q", toolText)
	}
	if sess := sessionOf(t, s, sid); sess.Kind != facade.SessionKindSubChat || sess.Purpose != "需求讨论" {
		t.Fatalf("经 gateway 建的子聊天会话不符：%+v", sess)
	}
	rec.wait(t, "subchat-opened", func(m map[string]any) bool { return str(m["session_id"]) == sid }, 3*time.Second)
	// 等子聊天首轮收尾（否则异步 goroutine 仍持有 prj 库句柄，TempDir 清理失败）。
	rec.wait(t, "session-complete", func(m map[string]any) bool { return str(m["session"]) == sid }, 10*time.Second)
}

// toolResultContent 读某会话某 tool_call_id 的 role=tool 结果文本（persist message 表）。
func toolResultContent(t *testing.T, s *Server, sessionID, toolCallID string) string {
	t.Helper()
	raw, ok := newSessionStore(s.bus, "ins-test").LoadToolContent(sessionID, toolCallID)
	if !ok {
		t.Fatalf("未找到 tool_call_id=%s 的结果行", toolCallID)
	}
	tc, ok := persist.ParseToolContent(raw)
	if !ok || tc.Result == nil {
		t.Fatalf("tool 结果解析失败：%s", raw)
	}
	return tc.Result.Content
}

// TestSubChatSessionsPurposeKeySubset（B2 · sessions.purpose 顶层键对账）：普通顶层会话行
// **不得**出现 purpose 键（仅非空才写键）；子聊天写入后读回一致（wire 双向）。
func TestSubChatSessionsPurposeKeySubset(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	ensureTestSession(t, s, "s-plain-p", facade.SessionEnsureRequest{})
	if p := sessionOf(t, s, "s-plain-p").Purpose; p != "" {
		t.Fatalf("普通会话 purpose 应为空：%q", p)
	}
	ensureTestSession(t, s, "sub-p", facade.SessionEnsureRequest{
		ParentSessionID: "s-plain-p", Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleChatting, Purpose: "写方案"})
	if p := sessionOf(t, s, "sub-p").Purpose; p != "写方案" {
		t.Fatalf("子聊天 purpose 读回不符：%q", p)
	}
}

// TestAllSessionsToolCallNoAsyncInjection：主会话 / 子聊天 / 子会话里的工具调用经 mq 均**不得**
// 注入调用级 `async`（异步/超时策略由 gateway 按界面 > md > 默认 auto/30s/background 裁决）；
// 但 `tool_call_display_name` 必须保留。
// 驱动 = `session-send`（text-user → mock LLM 回工具调用）；断言 = 监听 `mcp-tools-call`
// （MQ 驱动 + MQ 监听，50-测试体系 §1）。
func TestAllSessionsToolCallNoAsyncInjection(t *testing.T) {
	llm := mockLLMServer() // "sync tool" 分支 → core_file_read 工具调用（含 tool_call_display_name）
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	ensureTestSession(t, s, "s-fs-main", facade.SessionEnsureRequest{})
	ensureTestSession(t, s, "s-fs-chat", facade.SessionEnsureRequest{
		ParentSessionID: "s-fs-main", Kind: facade.SessionKindSubChat, Lifecycle: facade.SessionLifecycleChatting,
	})
	// 历史/子会话：parent_id 非空但无 kind → 读侧回退 sub_session（非主会话）
	ensureTestSession(t, s, "s-fs-sess", facade.SessionEnsureRequest{ParentSessionID: "s-fs-main"})

	rec := newBusRecorder(t, s.bus)
	for _, tc := range []struct {
		session, turn string
	}{
		{"s-fs-main", "t-fs-main"},
		{"s-fs-chat", "t-fs-chat"},
		{"s-fs-sess", "t-fs-sess"},
	} {
		startTurn(t, s, tc.session, tc.turn)
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": tc.session, "turn": tc.turn,
			"type": "text-user", "content": "sync tool please",
		}))
		collectTurn(t, s.bus, tc.turn, 10*time.Second)
		call := rec.wait(t, "mcp-tools-call", func(m map[string]any) bool {
			return str(m["session"]) == tc.session
		}, 3*time.Second)
		args, _ := call["arguments"].(map[string]any)
		if _, has := args["async"]; has {
			t.Fatalf("%s 的派发参数不得注入 async（策略由 gateway 裁决）：%+v", tc.session, args)
		}
		if got := args["tool_call_display_name"]; got == nil || got == "" {
			t.Fatalf("%s 的工具调用仍必须带 tool_call_display_name：%+v", tc.session, args)
		}
	}
}

func boolOf(v any) bool { b, _ := v.(bool); return b }

// TestSubChatOpenParticipantsInvalid（P0 2026-11-08）：participants 坏项 / 重复项不得
// 静默丢弃并降级单聊，必须明确返回参数错误。
func TestSubChatOpenParticipantsInvalid(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-pv", "t-pv")
	base := func(parts any) map[string]any {
		return map[string]any{
			"agent": "main-agent", "scenario_id": "scn-1", "llm": "llm-1", "prompt": "讨论",
			"participants": parts,
		}
	}
	cases := []struct {
		name  string
		parts any
		want  string
	}{
		{"坏项", []any{map[string]any{"agent": "a"}, map[string]any{"role": "x"}}, "缺少 agent"},
		{"重复", []any{map[string]any{"agent": "a"}, map[string]any{"agent": "a"}}, "重复 agent"},
		{"非数组", "a,b", "必须是数组"},
	}
	for i, c := range cases {
		res := emitDomainTool(t, s, "subchat_open", base(c.parts), "t-pv", "s-main-pv",
			"tc-pv-"+strconv.Itoa(i), "群聊")
		text := domainResultText(t, res)
		if !strings.Contains(text, `"ok":false`) || !strings.Contains(text, c.want) {
			t.Fatalf("%s：应明确失败并说明 %s，got %q", c.name, c.want, text)
		}
	}
}

// TestSubChatOpenRejectsUnknownRefs（2026-11-10）：能力注册表 / usr llms 可用时，固定参数
// 写错必须在父轮次明确失败，而不是建出“人格/模型不对”的子聊天。
func TestSubChatOpenRejectsUnknownRefs(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-main-ref", "t-ref")
	saveTestUserConfig(t, s, map[string]any{"llms": []any{
		map[string]any{"name": "known", "baseUrl": llm.URL, "model": "mock"},
	}})

	res := emitDomainTool(t, s, "subchat_open", map[string]any{
		"agent": "known-agent", "scenario_id": "scn-1", "llm": "typo", "prompt": "讨论",
	}, "t-ref", "s-main-ref", "tc-ref-llm", "x")
	if text := domainResultText(t, res); !strings.Contains(text, `"ok":false`) || !strings.Contains(text, "未配置") {
		t.Fatalf("未知 llm 应明确失败：%q", text)
	}

	// 注册表非空 → 场景/agent 进入严格校验（测试无 scenario 数据）。
	s.mu.Lock()
	if s.agents == nil {
		s.agents = map[string]AgentDef{}
	}
	s.agents["known-agent"] = AgentDef{}
	s.mu.Unlock()
	res = emitDomainTool(t, s, "subchat_open", map[string]any{
		"agent": "known-agent", "scenario_id": "no-such", "llm": "known", "prompt": "讨论",
	}, "t-ref", "s-main-ref", "tc-ref-scn", "x")
	if text := domainResultText(t, res); !strings.Contains(text, `"ok":false`) || !strings.Contains(text, "scenario_id") {
		t.Fatalf("未知 scenario_id 应明确失败：%q", text)
	}
}

// TestSubChatFinishRejectsNonSubChat（P0 2026-11-08）：subchat_finish 只允许 kind=sub_chat；
// 主会话 / 群聊调用明确失败且不改写 lifecycle。
func TestSubChatFinishRejectsNonSubChat(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	ensureTestSession(t, s, "s-main-fi", facade.SessionEnsureRequest{})
	ensureTestSession(t, s, "g-fi", facade.SessionEnsureRequest{
		ParentSessionID: "s-main-fi", Kind: facade.SessionKindGroupChat,
		Lifecycle: facade.SessionLifecycleChatting, Agent: "a", ScenarioID: "sc", LLM: "l",
	})
	startTurn(t, s, "s-main-fi", "t-main-fi")
	res := emitDomainTool(t, s, "subchat_finish", map[string]any{}, "t-main-fi", "s-main-fi", "tc-fi-main", "x")
	if text := domainResultText(t, res); !strings.Contains(text, `"ok":false`) || !strings.Contains(text, "仅可在子聊天") {
		t.Fatalf("主会话调用 subchat_finish 应明确失败：%q", text)
	}
	if got := sessionOf(t, s, "s-main-fi").Lifecycle; got != "" {
		t.Fatalf("主会话 lifecycle 不应被改写：%q", got)
	}

	startTurn(t, s, "g-fi", "t-g-fi")
	res = emitDomainTool(t, s, "subchat_finish", map[string]any{}, "t-g-fi", "g-fi", "tc-fi-group", "x")
	if text := domainResultText(t, res); !strings.Contains(text, `"ok":false`) {
		t.Fatalf("群聊调用 subchat_finish 应失败：%q", text)
	}
	if got := sessionOf(t, s, "g-fi").Lifecycle; got != facade.SessionLifecycleChatting {
		t.Fatalf("群聊 lifecycle 不应被改写：%q", got)
	}
}
