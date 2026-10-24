// chat 引擎执行一整轮 turn（MOD-1 A4-P4b：引擎为唯一执行路径）：行为用例。
//
// 全部用例 mq 驱动（session-send / session-cancel / inbox-add / session-ask-reply /
// mcp-tasks-report）+ 监听断言（session-receive / session-complete / task-*）。
// 断言口径：
//   - 落库消息序列（role / kind / content / tool_call_id / tool_calls 逐项）；
//   - role=tool 行数与终态（「running + result 同一行」）；
//   - 任务树事件（tasks.started / tasks.done 的 tool + kind + 终态，按序）；
//   - `llm-receive` 文本拼接；`llm-complete` 的 status / finish_reason / text / code / message。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
)

// engineReceiveText 拼接一次轮次内 `llm-receive` 的文本增量（type=text；忽略 complete / retry）。
func engineReceiveText(evs []map[string]any) string {
	var b strings.Builder
	for _, e := range evs {
		if _, isComplete := e["status"]; isComplete {
			continue
		}
		if e["type"] != NotifyTypeText {
			continue
		}
		payload, _ := e["payload"].(map[string]any)
		if payload == nil {
			continue
		}
		b.WriteString(str(payload["text"]))
	}
	return b.String()
}

// engineMessageDigest 取落库消息的比对口径（复用影子重放的投影：忽略 id / created_at / kind）。
func engineMessageDigest(msgs []ChatMsg) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, shadowMessageJSON(m))
	}
	return out
}

// engineCompleteView 取 `llm-complete` 的比对口径（status / finish_reason / text / code / message）。
func engineCompleteView(c map[string]any) string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("status=%v finish=%v text=%q code=%v message=%v retryable=%v",
		c["status"], c["finish_reason"], str(c["text"]), c["code"], c["message"], c["retryable"])
}

// engineTurnRecord 是一次「一轮 turn」的等价口径快照。
type engineTurnRecord struct {
	msgs     []string
	toolRows []string
	tasks    []string
	recv     string
	complete string
	events   []map[string]any
}

// engineTurnMsgDigest 取落库消息序列的比对口径（读侧归一后的 role / kind / content / 工具调用）。
func engineTurnMsgDigest(t *testing.T, s *Server, turn string) []string {
	t.Helper()
	msgs := newSessionStore(s.bus, "ins-test").LoadMessages(turn)
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		calls := make([]string, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			calls = append(calls, c.ID+"|"+c.Name+"|"+c.Arguments)
		}
		out = append(out, fmt.Sprintf("role=%s kind=%s content=%q tool_call_id=%s calls=[%s]",
			m.Role, m.Kind, m.Content, m.ToolCallID, strings.Join(calls, ";")))
	}
	return out
}

// engineTurnToolRows 取 role=tool 行的「主键形状 + 终态」（同一行断言：行数 + tool_call_status）。
func engineTurnToolRows(t *testing.T, turn string) []string {
	t.Helper()
	out := make([]string, 0, 2)
	for _, r := range toolRowsOf(t, turn) {
		out = append(out, persist.Sval(r["tool_call_id"])+"|"+persist.Sval(r["tool_call_status"]))
	}
	return out
}

// engineTurnTaskDigest 取任务树事件的比对口径（started / done 的 tool + kind + 终态，排序后）。
// 任务 id / started_at 随运行变化，不进比对。
func engineTurnTaskDigest(te *taskEvents) []string {
	te.mu.Lock()
	defer te.mu.Unlock()
	out := make([]string, 0, len(te.started)+len(te.done))
	for _, m := range te.started {
		out = append(out, fmt.Sprintf("started|%v|%v", m["tool"], m["kind"]))
	}
	for _, m := range te.done {
		out = append(out, fmt.Sprintf("done|%v|%v", m["tool"], m["state"]))
	}
	sort.Strings(out)
	return out
}

// resetTaskManagerForEngineTest 清空内存任务节点（用例内两次运行之间隔离）：
// fake gateway 的异步任务 id 固定（"tk-test"），跨运行会命中上一轮已终态的节点 →
// `onGatewayTaskDone` 的节点同步被跳过（生产 task_id 唯一，不存在该情形）。
func resetTaskManagerForEngineTest(t *testing.T, s *Server) {
	t.Helper()
	s.tasks.tree.Clear()
}

// runEngineTurnCase 跑一轮并返回口径快照。drive 负责发送首条输入与后续回执。
func runEngineTurnCase(t *testing.T, s *Server, session, turn string,
	drive func(session, turn string, log *eventLog)) engineTurnRecord {
	t.Helper()
	resetTaskManagerForEngineTest(t, s)
	te := watchTasks(t, s.bus)
	startTurn(t, s, session, turn)
	log := watchSessionEvents(t, s.bus, turn)
	drive(session, turn, log)
	evs := waitComplete(t, log, 25*time.Second)
	// 任务层广播（tasks.done）与终态同批同步派发 → 等其落定（最多 5s）。
	deadline := time.Now().Add(5 * time.Second)
	for {
		te.mu.Lock()
		nStart, nDone := len(te.started), len(te.done)
		detail := fmt.Sprintf("started=%v done=%v", te.started, te.done)
		te.mu.Unlock()
		if nStart == nDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务树事件未落定：%s", detail)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return engineTurnRecord{
		msgs:     engineTurnMsgDigest(t, s, turn),
		toolRows: engineTurnToolRows(t, turn),
		tasks:    engineTurnTaskDigest(te),
		recv:     engineReceiveText(evs),
		complete: engineCompleteView(lastComplete(evs)),
		events:   evs,
	}
}

// engineTextTurn 发送一条用户消息（普通文本轮 / 工具轮的首条输入）。
func engineTextTurn(s *Server, session, turn, content string) {
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": content,
	}))
}

// engineWaitEvent 轮询事件日志直到 cond 命中（兜底超时失败）。
func engineWaitEvent(t *testing.T, log *eventLog, what string, cond func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range log.snapshot() {
			if cond(ev) {
				return ev
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待事件超时：%s（events=%+v）", what, log.snapshot())
	return nil
}

// TestEngineTurnText：纯文本轮（user → assistant）由引擎执行：落库两条、终态 complete。
func TestEngineTurnText(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	before := engineTurnCalls.Load()
	rec := runEngineTurnCase(t, s, "s-et-text", "t-et-text",
		func(session, turn string, _ *eventLog) { engineTextTurn(s, session, turn, "hello") })
	if got := engineTurnCalls.Load() - before; got != 1 {
		t.Fatalf("应恰好由引擎执行 1 轮（calls=%d）", got)
	}

	if len(rec.msgs) != 2 {
		t.Fatalf("纯文本轮落库消息数=%d want 2：%v", len(rec.msgs), rec.msgs)
	}
	if !strings.Contains(rec.complete, "status=complete") || !strings.Contains(rec.complete, "finish=stop") {
		t.Fatalf("终态错：%s", rec.complete)
	}
}

// TestEngineTurnSyncTool：同步工具轮（tool_call → 同步结果 → 文本）。
// 断言：任务树 started/completed、running+result 同一行、聚合回喂、终态。
func TestEngineTurnSyncTool(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	drive := func(session, turn string, _ *eventLog) { engineTextTurn(s, session, turn, "sync tool please") }
	rec := runEngineTurnCase(t, s, "s-et-sync", "t-et-sync", drive)

	if len(rec.toolRows) != 1 || rec.toolRows[0] != "tc-sync|completed" {
		t.Fatalf("同步工具应恰一行且终态 completed：%v", rec.toolRows)
	}
	if len(rec.tasks) != 2 ||
		rec.tasks[0] != "done|core_file_read|done" || rec.tasks[1] != "started|core_file_read|tool" {
		t.Fatalf("同步工具任务树事件不符：%v", rec.tasks)
	}
	if !strings.Contains(strings.Join(rec.msgs, "\n"), `content="file content"`) {
		t.Fatalf("同步结果应聚合回喂：%v", rec.msgs)
	}
	if !strings.Contains(rec.complete, "status=complete") {
		t.Fatalf("同步工具轮终态应 complete：%s", rec.complete)
	}
}

// TestEngineTurnAsyncTool：异步工具轮（gateway 返回 pending → 轮内等待 → 生产回执路径投递 → 续轮）。
func TestEngineTurnAsyncTool(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	drive := func(session, turn string, _ *eventLog) { engineTextTurn(s, session, turn, "async tool please") }
	rec := runEngineTurnCase(t, s, "s-et-async", "t-et-async", drive)

	if len(rec.toolRows) != 1 || rec.toolRows[0] != "tc-1|completed" {
		t.Fatalf("异步工具应恰一行且终态 completed：%v", rec.toolRows)
	}
	if !strings.Contains(strings.Join(rec.msgs, "\n"), `content="async file content"`) {
		t.Fatalf("异步结果应回填续轮：%v", rec.msgs)
	}
	if !strings.Contains(rec.complete, "status=complete") {
		t.Fatalf("异步轮终态应 complete：%s", rec.complete)
	}
}

// TestEngineTurnAsk：ask 轮（ask_user → ask-user 广播 → ask-user-reply → 续轮）。
func TestEngineTurnAsk(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	drive := func(session, turn string, log *eventLog) {
		engineTextTurn(s, session, turn, "ask please")
		ask := engineWaitEvent(t, log, "ask-user", func(ev map[string]any) bool {
			return str(ev["ask_id"]) != ""
		})
		s.bus.Emit(context.Background(), "session-ask-reply", jb(map[string]any{
			"ask_id": ask["ask_id"], "answer": "y",
		}))
	}
	rec := runEngineTurnCase(t, s, "s-et-ask", "t-et-ask", drive)

	if len(rec.toolRows) != 1 || rec.toolRows[0] != "tc-ask|completed" {
		t.Fatalf("ask 应恰一行且终态 completed：%v", rec.toolRows)
	}
	if len(rec.tasks) != 2 || rec.tasks[0] != "done|ask_user|done" || rec.tasks[1] != "started|ask_user|ask_user" {
		t.Fatalf("ask 任务树事件不符：%v", rec.tasks)
	}
	if !strings.Contains(strings.Join(rec.msgs, "\n"), `content="y"`) {
		t.Fatalf("ask 回答应回填续轮：%v", rec.msgs)
	}
}

// engineInsertLLM 是插话用例的确定性 LLM 桩：慢工具（手动 pending）/ 插话回答 / 收尾回答。
func engineInsertLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		switch {
		case strings.Contains(text, "慢工具"):
			args := jb(map[string]any{"_inbox_manual": true, "tool_call_display_name": "慢工具", "path": "a.txt"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-main", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(text, "插话A"):
			llmSSE(w, []string{sseChunk(map[string]any{"content": "插话回答"}, "stop")})
		default:
			llmSSE(w, []string{sseChunk(map[string]any{"content": "完成"}, "stop")})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestEngineTurnInsert：工具等待期间插话（insert → 插入帧执行 → 恢复等回执 → 续轮）。
func TestEngineTurnInsert(t *testing.T) {
	llm := engineInsertLLM(t)
	s := newTestServer(t, llm)

	drive := func(session, turn string, log *eventLog) {
		engineTextTurn(s, session, turn, "请用慢工具")
		waitInsertable(t, s, session) // 工具等待期（pending 已登记）
		res := inboxAddMQ(t, s, session, "【ET】插话A")
		if str(res["type"]) != "insert" {
			t.Fatalf("工具等待期应分类为 insert：%+v", res)
		}
		// 插入帧跑完（广播插话回答）后再投递工具回执。
		engineWaitEvent(t, log, "insert answer", func(ev map[string]any) bool {
			if str(ev["type"]) != NotifyTypeText {
				return false
			}
			p, _ := ev["payload"].(map[string]any)
			return strings.Contains(str(p["text"]), "插话回答")
		})
		reportTask(t, s, "tk-tc-main", "manual result", session, turn)
	}
	rec := runEngineTurnCase(t, s, "s-et-ins", "t-et-ins", drive)

	if len(rec.toolRows) != 1 || rec.toolRows[0] != "tc-main|completed" {
		t.Fatalf("插话轮工具应恰一行且终态 completed：%v", rec.toolRows)
	}
	joined := strings.Join(rec.msgs, "\n")
	if !strings.Contains(joined, `content="【ET】插话A"`) || !strings.Contains(joined, `content="插话回答"`) {
		t.Fatalf("插话内容与插入帧回答应落库：%v", rec.msgs)
	}
	// 终态文本 = 主帧累积（插入帧正文不进 llm-complete.text）。
	if !strings.Contains(rec.complete, `text="完成"`) {
		t.Fatalf("终态文本应为主帧累积：%s", rec.complete)
	}
}

// TestEngineTurnLength：length 截断（关闭自动续写）→ incomplete + 中断提示；半截正文落库。
func TestEngineTurnLength(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	if res := dataCall(t, s, "data-user-config-save", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"autoContinue": false},
	}); res["ok"] != true {
		t.Fatalf("save user-config failed: %+v", res)
	}
	cfg, err := s.loadUserConfig("ins-test")
	if err != nil {
		t.Fatalf("loadUserConfig: %v", err)
	}
	if enabled, _, _ := llmContinueConfigFrom(cfg); enabled {
		t.Fatalf("前置条件不成立：usr autoContinue 未关闭（%v）", cfg["autoContinue"])
	}

	drive := func(session, turn string, _ *eventLog) { engineTextTurn(s, session, turn, "length please") }
	rec := runEngineTurnCase(t, s, "s-et-len", "t-et-len", drive)

	if !strings.Contains(rec.complete, "status=incomplete") || !strings.Contains(rec.complete, "finish=length") {
		t.Fatalf("length 应收敛到 incomplete：%s", rec.complete)
	}
	if !strings.Contains(rec.complete, `text="part"`) {
		t.Fatalf("incomplete 应携带已产出的半截正文：%s", rec.complete)
	}
	if !strings.Contains(strings.Join(rec.msgs, "\n"), `role=assistant kind= content="part"`) {
		t.Fatalf("截断前已产出的正文应落库：%v", rec.msgs)
	}
	// 中断提示：`llm-interrupted`（code=LLM_INCOMPLETE / retryable=true / delay=0）。
	var got map[string]any
	for _, e := range rec.events {
		if str(e["code"]) == "LLM_INCOMPLETE" {
			got = e
		}
	}
	if got == nil {
		t.Fatalf("缺 llm-interrupted(code=LLM_INCOMPLETE)：%+v", rec.events)
	}
	if got["retryable"] != true {
		t.Fatalf("中断提示 retryable 应为 true：%+v", got)
	}
	if delay, _ := got["auto_continue_delay_ms"].(float64); delay != 0 {
		t.Fatalf("未开启续写时 auto_continue_delay_ms 应为 0：%+v", got)
	}
	if _, has := got["message"]; has {
		t.Fatalf("length 中断提示不应带 message：%+v", got)
	}
}

// TestEngineTurnCancel：llm-cancel → interrupted。
func TestEngineTurnCancel(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer llm.Close()
	s := newTestServer(t, llm)

	before := engineTurnCalls.Load()
	startTurn(t, s, "s-et-cancel", "t-et-cancel")
	log := watchSessionEvents(t, s.bus, "t-et-cancel")
	engineTextTurn(s, "s-et-cancel", "t-et-cancel", "slow stream")
	time.Sleep(300 * time.Millisecond) // 进入挂起的 LLM 请求
	s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-et-cancel", "turn": "t-et-cancel",
	}))
	evs := waitComplete(t, log, 8*time.Second)
	complete := engineCompleteView(lastComplete(evs))
	if got := engineTurnCalls.Load() - before; got != 1 {
		t.Fatalf("应恰好由引擎执行 1 轮（calls=%d）", got)
	}
	if !strings.Contains(complete, "status=interrupted") {
		t.Fatalf("取消应收敛到 interrupted：%s", complete)
	}
}

// TestEngineTurnSubChat：子会话（kind=sub_chat）由引擎执行：落库消息 / 终态正常。
func TestEngineTurnSubChat(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	ensure := func(session string) {
		t.Helper()
		if _, err := s.cfg.SessionEnsure(facade.SessionEnsureRequest{
			InstanceID: "ins-test", Scope: s.cfgScope("ins-test"), SessionID: session,
			ParentSessionID: "s-et-top", Kind: facade.SessionKindSubChat,
			Lifecycle: facade.SessionLifecycleChatting, Agent: "sub",
		}); err != nil {
			t.Fatalf("SessionEnsure(%s): %v", session, err)
		}
	}
	ensure("s-et-sub")
	drive := func(session, turn string, _ *eventLog) { engineTextTurn(s, session, turn, "hello") }

	before := engineTurnCalls.Load()
	rec := runEngineTurnCase(t, s, "s-et-sub", "t-et-sub", drive)
	if got := engineTurnCalls.Load() - before; got != 1 {
		t.Fatalf("子聊天应由引擎执行（calls=%d）", got)
	}
	if !strings.Contains(rec.complete, "status=complete") {
		t.Fatalf("子聊天轮应正常完成：%s", rec.complete)
	}
}

// TestEngineTurnRecoveryAlwaysEngine：恢复轮（recoverTurnCtx）**始终**由引擎执行 ——
// ① 只有 user 消息 → 引擎；② 已存在 assistant（半途中断）→ **同样**引擎（不按消息形态分流）。
func TestEngineTurnRecoveryAlwaysEngine(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	// ① 只有 user 消息 → 引擎（等首输入，不产生终态；Close 收尾）。
	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureTurn("t-rec-user", "s-rec-user"); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	if _, err := st.AppendFullKeyed("t-rec-user", ChatMsg{Role: "user", Kind: "text", Content: "hi"}, ""); err != nil {
		t.Fatalf("AppendFullKeyed: %v", err)
	}
	before := engineTurnCalls.Load()
	tc := s.recoverTurnCtx("ins-test", "s-rec-user", "t-rec-user")
	if !tc.isRecovery {
		tc.Close()
		t.Fatalf("恢复轮应标记 isRecovery（isRecovery=%v）", tc.isRecovery)
	}
	// 引擎已进入（runEngineLoop 计数 +1，等待首输入）。
	deadline := time.Now().Add(2 * time.Second)
	for engineTurnCalls.Load()-before == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := engineTurnCalls.Load() - before; got != 1 {
		tc.Close()
		t.Fatalf("恢复轮应进入引擎（calls=%d）", got)
	}
	tc.Close()

	// ② 已有 assistant（半途中断）→ 恢复轮**仍由引擎执行**。
	if err := st.EnsureTurn("t-rec-asst", "s-rec-asst"); err != nil {
		t.Fatalf("EnsureTurn: %v", err)
	}
	for _, m := range []ChatMsg{
		{Role: "user", Kind: "text", Content: "hi"},
		{Role: "assistant", Content: "partial"},
	} {
		if _, err := st.AppendFullKeyed("t-rec-asst", m, ""); err != nil {
			t.Fatalf("AppendFullKeyed: %v", err)
		}
	}
	before = engineTurnCalls.Load()
	tc2 := s.recoverTurnCtx("ins-test", "s-rec-asst", "t-rec-asst")
	if !tc2.isRecovery {
		tc2.Close()
		t.Fatalf("含 assistant 的恢复轮也应标记 isRecovery（isRecovery=%v）", tc2.isRecovery)
	}
	deadline = time.Now().Add(2 * time.Second)
	for engineTurnCalls.Load()-before == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := engineTurnCalls.Load() - before; got != 1 {
		tc2.Close()
		t.Fatalf("半途恢复轮应由引擎执行（calls=%d）", got)
	}
	tc2.Close()
}

// appendRecoveryToolRow 落一条「工具行」（`{call,result,async}` 存储形态 + `tool_call_status`），
// 与生产 `persistToolResult` 落库形状一致 —— 供恢复轮用例构造半途历史。
func appendRecoveryToolRow(t *testing.T, st *sessionStore, turn, toolCallID, tool string,
	args map[string]any, result, status string) {
	t.Helper()
	content, err := json.Marshal(persist.ToolContent{
		Call:   &persist.ToolCallContent{ToolCallID: toolCallID, Name: tool, Arguments: args},
		Result: &persist.ToolResultContent{Content: result, Status: status},
	})
	if err != nil {
		t.Fatalf("marshal tool content: %v", err)
	}
	if _, err := st.AppendMsgMap(turn, map[string]any{
		"role": "tool", "content": string(content),
		"tool_call_id": toolCallID, "tool_call_status": status,
	}, ""); err != nil {
		t.Fatalf("AppendMsgMap(tool): %v", err)
	}
}

// appendRecoveryMidTurnHistory 落半途历史：user + assistant(tool_calls) [+ tool 行（giveResult=false 时省略）]。
func appendRecoveryMidTurnHistory(t *testing.T, st *sessionStore, turn, userText, toolCallID string,
	giveResult bool) {
	t.Helper()
	if err := st.EnsureTurn(turn, "s-"+turn); err != nil {
		t.Fatalf("EnsureTurn(%s): %v", turn, err)
	}
	if _, err := st.AppendFullKeyed(turn, ChatMsg{Role: "user", Kind: "text", Content: userText}, ""); err != nil {
		t.Fatalf("AppendFullKeyed(user): %v", err)
	}
	assistant := ChatMsg{Role: "assistant", ToolCalls: []ToolCall{{
		ID: toolCallID, Name: "core_file_read", Arguments: `{"path":"a.txt"}`,
	}}}
	if _, err := st.AppendFullKeyed(turn, assistant, ""); err != nil {
		t.Fatalf("AppendFullKeyed(assistant): %v", err)
	}
	if giveResult {
		appendRecoveryToolRow(t, st, turn, toolCallID, "core_file_read",
			map[string]any{"path": "a.txt"}, "file content", "completed")
	}
}

// llmRequestRoles 返回一次 LLM 请求的消息角色序列（含 tool_call_id），供恢复轮上下文顺序断言。
func llmRequestRoles(msgs []ChatMsg) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		s := m.Role
		if m.Role == "tool" {
			s += "(" + m.ToolCallID + ")"
		}
		out = append(out, s)
	}
	return out
}

// llmRequestTail 返回一次 LLM 请求**末尾** n 条消息的角色序列（system 层由场景 / 指引注入，
// 不在恢复轮断言口径内）。
func llmRequestTail(msgs []ChatMsg, n int) string {
	roles := llmRequestRoles(msgs)
	if len(roles) > n {
		roles = roles[len(roles)-n:]
	}
	return strings.Join(roles, "|")
}

// llmRequestToolMsg 按 tool_call_id 取一次 LLM 请求里的 tool 消息（未命中 → nil）。
func llmRequestToolMsg(msgs []ChatMsg, callID string) *ChatMsg {
	for i := range msgs {
		if msgs[i].Role == "tool" && msgs[i].ToolCallID == callID {
			return &msgs[i]
		}
	}
	return nil
}

// TestEngineTurnRecoveryMidTurn：半途恢复（已落库 user + assistant(tool_calls) + tool(completed)）——
// 恢复轮由引擎执行：上下文种子含**这三条** → 只从历史继续下一次 LLM 调用，**不重复派发**已完成工具
// → 终态 / 落库与「同一历史 + 同一『继续』输入」的新建轮逐项一致。
func TestEngineTurnRecoveryMidTurn(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	tcalls := watchToolCalls(t, s.bus)

	st := newSessionStore(s.bus, "ins-test")
	appendRecoveryMidTurnHistory(t, st, "t-rec-mid", "读取 a.txt", "tc-rec-mid", true)

	// ① 恢复轮：落库历史 → recoverTurnCtx → 「继续」 → 终态。
	before := engineTurnCalls.Load()
	recLog := watchSessionEvents(t, s.bus, "t-rec-mid")
	tc := s.recoverTurnCtx("ins-test", "s-rec-mid", "t-rec-mid")
	t.Cleanup(tc.Close)
	if !tc.isRecovery {
		t.Fatalf("半途恢复轮应标记 isRecovery（isRecovery=%v）", tc.isRecovery)
	}
	tc.FeedContinue("继续")
	recEvs := waitComplete(t, recLog, 15*time.Second)
	if got := engineTurnCalls.Load() - before; got != 1 {
		t.Fatalf("恢复轮应恰好由引擎执行 1 次（calls=%d）", got)
	}
	if c := lastComplete(recEvs); c == nil || c["status"] != "complete" {
		t.Fatalf("恢复轮应正常完成：%+v", recEvs)
	}

	// 下一步 LLM 收到的 Messages 含 user / assistant(tool_calls) / tool(completed) 三条（按序）+ 「继续」。
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("恢复轮 LLM 请求数=%d want 1（只续跑一次）", len(reqs))
	}
	if got := llmRequestTail(reqs[0], 4); got != "user|assistant|tool(tc-rec-mid)|user" {
		t.Fatalf("恢复轮上下文顺序错：got=%s msgs=%+v", got, reqs[0])
	}
	if tm := llmRequestToolMsg(reqs[0], "tc-rec-mid"); tm == nil || tm.Content != "file content" {
		t.Fatalf("tool 结果应原样进入上下文：%+v", tm)
	}
	// 不重复派发已完成工具（引擎只从历史继续；派发会经 gateway 的 mcp-tools-call）。
	if n := len(tcalls.callsOf("core_file_read")); n != 0 {
		t.Fatalf("恢复轮不得重复派发已完成工具（gateway calls=%d）", n)
	}

	// ② 新建轮的「继续」（同一历史 + 同一输入）→ 逐项比对落库 / 终态。
	appendRecoveryMidTurnHistory(t, st, "t-rec-manual", "读取 a.txt", "tc-rec-mid", true)
	manualLog := watchSessionEvents(t, s.bus, "t-rec-manual")
	tcManual := newTurnCtx(context.Background(), s, StartReq{
		InstanceID: "ins-test", Session: "s-t-rec-manual", Turn: "t-rec-manual",
	})
	t.Cleanup(tcManual.Close)
	tcManual.FeedContinue("继续")
	manualEvs := waitComplete(t, manualLog, 15*time.Second)

	if got, want := strings.Join(engineTurnMsgDigest(t, s, "t-rec-mid"), "\n"),
		strings.Join(engineTurnMsgDigest(t, s, "t-rec-manual"), "\n"); got != want {
		t.Fatalf("恢复轮落库应与新建轮「继续」一致\n恢复=%v\n新建=%v", got, want)
	}
	if got, want := engineCompleteView(lastComplete(recEvs)), engineCompleteView(lastComplete(manualEvs)); got != want {
		t.Fatalf("恢复轮终态应与新建轮「继续」一致\n恢复=%s\n新建=%s", got, want)
	}
	if rows := engineTurnToolRows(t, "t-rec-mid"); len(rows) != 1 || rows[0] != "tc-rec-mid|completed" {
		t.Fatalf("恢复轮 tool 行应保持既有 completed 单行：%v", rows)
	}
}

// TestEngineTurnRecoveryDanglingCall：断链恢复（assistant tool_calls **无** tool 行，模拟清理未覆盖）——
// 引擎在 RunTurn 前显式合成一条 `role=tool` / `tool_call_status=interrupted`（文案同生产 cleanup 占位）
// 并落库，随后正常续跑；不得 panic / 死循环、不得把断链直接发给 LLM。
func TestEngineTurnRecoveryDanglingCall(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)
	tcalls := watchToolCalls(t, s.bus)

	st := newSessionStore(s.bus, "ins-test")
	appendRecoveryMidTurnHistory(t, st, "t-rec-dang", "读取 b.txt", "tc-rec-dang", false)

	before := engineTurnCalls.Load()
	log := watchSessionEvents(t, s.bus, "t-rec-dang")
	tc := s.recoverTurnCtx("ins-test", "s-rec-dang", "t-rec-dang")
	t.Cleanup(tc.Close)
	tc.FeedContinue("继续")
	evs := waitComplete(t, log, 15*time.Second)

	if got := engineTurnCalls.Load() - before; got != 1 {
		t.Fatalf("断链恢复轮应由引擎执行（calls=%d）", got)
	}
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("断链恢复轮应正常完成（不 panic / 不死循环）：%+v", evs)
	}

	// 合成 1 条 interrupted tool 行（落库 + 状态列），内容 = 生产 cleanup 占位文案。
	rows := toolRowsOf(t, "t-rec-dang")
	if len(rows) != 1 {
		t.Fatalf("断链应恰合成 1 条 tool 行：got=%d", len(rows))
	}
	if got := persist.Sval(rows[0]["tool_call_status"]); got != persist.ToolStatusInterrupted {
		t.Fatalf("合成 tool 行状态=%q want %q", got, persist.ToolStatusInterrupted)
	}
	if !strings.Contains(persist.Sval(rows[0]["content"]), engineInterruptedToolText) {
		t.Fatalf("合成 tool 行内容应含生产占位文案：%q", persist.Sval(rows[0]["content"]))
	}

	// 下一条 LLM 请求：assistant(tool_calls) 后紧跟合成 tool 行（协议合法）。
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("断链恢复 LLM 请求数=%d want 1", len(reqs))
	}
	if got := llmRequestTail(reqs[0], 4); got != "user|assistant|tool(tc-rec-dang)|user" {
		t.Fatalf("断链恢复上下文顺序错：got=%s msgs=%+v", got, reqs[0])
	}
	if tm := llmRequestToolMsg(reqs[0], "tc-rec-dang"); tm == nil || !strings.Contains(tm.Content, "工具已中断") {
		t.Fatalf("合成结果应作为 tool 内容发给 LLM：%+v", tm)
	}
	if n := len(tcalls.callsOf("core_file_read")); n != 0 {
		t.Fatalf("断链恢复不得派发工具（gateway calls=%d）", n)
	}
}

// TestEngineTurnSameTurnContinue：同轮次继续（llm-start{continue:true} + text-user）——首输入以
// Kind=continue 落库、上下文 = 本轮既有消息 + 全部历史。
func TestEngineTurnSameTurnContinue(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	run := func(session, turn string) engineTurnRecord {
		t.Helper()
		resetTaskManagerForEngineTest(t, s)
		te := watchTasks(t, s.bus)
		startTurn(t, s, session, turn)
		log := watchSessionEvents(t, s.bus, turn)
		engineTextTurn(s, session, turn, "hello")
		waitComplete(t, log, 15*time.Second)
		waitTurnIdle(t, s, session)

		v := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
			"req_id": "r-cont-" + turn, "instance_id": "ins-test", "session": session,
			"turn": turn, "continue": true,
		})).Wait()
		if res, _ := v.Result.(map[string]any); res["accepted"] != true {
			t.Fatalf("continue llm-start rejected: %+v", res)
		}
		log2 := watchSessionEvents(t, s.bus, turn)
		engineTextTurn(s, session, turn, "again")
		evs := waitComplete(t, log2, 15*time.Second)
		return engineTurnRecord{
			msgs:     engineTurnMsgDigest(t, s, turn),
			toolRows: engineTurnToolRows(t, turn),
			tasks:    engineTurnTaskDigest(te),
			recv:     engineReceiveText(evs),
			complete: engineCompleteView(lastComplete(evs)),
		}
	}
	rec := run("s-et-cont", "t-et-cont")

	if !strings.Contains(rec.complete, "status=complete") {
		t.Fatalf("继续轮应正常完成：%s", rec.complete)
	}
	kinds := make([]string, 0, 2)
	for _, m := range newSessionStore(s.bus, "ins-test").LoadMessages("t-et-cont") {
		if m.Role == "user" {
			kinds = append(kinds, m.Kind)
		}
	}
	if len(kinds) != 2 || kinds[0] != "text" || kinds[1] != assembleContinueKind {
		t.Fatalf("user kinds=%v want [text %s]", kinds, assembleContinueKind)
	}
}

// TestEngineTurnImage：msgs 含图片引用 `![a](x.png)` → 图片展开在宿主 ChatOptions（与引擎无关），
// 整轮正常完成且不写 turn_events。
func TestEngineTurnImage(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	root := imageDataRoot("", testWorkDir)
	if root == "" {
		t.Fatal("数据根不可解析（无法准备图片夹具）")
	}
	dir := filepath.Join(root, "tmp", "up")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 仅需 PNG 魔数（loadImagePart 按魔数识别）。
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(filepath.Join(dir, "x.png"), png, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	drive := func(session, turn string, _ *eventLog) { engineTextTurn(s, session, turn, "look ![a](tmp/up/x.png)") }
	rec := runEngineTurnCase(t, s, "s-et-img", "t-et-img", drive)

	if !strings.Contains(rec.complete, "status=complete") {
		t.Fatalf("图片轮应正常完成：%s", rec.complete)
	}
	if len(rec.msgs) != 2 {
		t.Fatalf("图片轮落库消息数=%d want 2：%v", len(rec.msgs), rec.msgs)
	}
}

// TestEngineTurnEmptyReply：LLM 返回空内容 → 错误终态 EMPTY_REPLY，且不落 assistant。
func TestEngineTurnEmptyReply(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	drive := func(session, turn string, _ *eventLog) { engineTextTurn(s, session, turn, "empty please") }
	rec := runEngineTurnCase(t, s, "s-et-empty", "t-et-empty", drive)

	if !strings.Contains(rec.complete, "status=error") || !strings.Contains(rec.complete, "code=EMPTY_REPLY") ||
		!strings.Contains(rec.complete, "retryable=false") {
		t.Fatalf("EMPTY_REPLY 口径不符：%s", rec.complete)
	}
	for _, m := range rec.msgs {
		if strings.Contains(m, `role=assistant`) {
			t.Fatalf("空回复不应落 assistant：%v", rec.msgs)
		}
	}
}
