// INBOX（中途插话 / 等下一轮）L2 用例：**发送 mq 驱动 + 监听 mq 断言**（50-测试体系 §1）。
//
// 设计：docs/agent-chat/中途插话设计方案.md §3（S1/S3/S4/S4.5/S5）、§7.1（INS-xxx）。
// 时序控制：mock gateway 对 `_inbox_manual` 工具返回 pending 且**不自动回执**，
// 用例在需要时手动发 `mcp-tasks-report`，从而稳定复现"回执先/后于插话结束"两种顺序。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// ── 事件录制（session-* + mcp-tools-call）────────────────────────────────

type inboxEv struct {
	subject string
	m       map[string]any
}

type inboxRec struct {
	mu  sync.Mutex
	evs []inboxEv
}

func newInboxRec(t *testing.T, bus mq.Bus) *inboxRec {
	t.Helper()
	r := &inboxRec{}
	_, err := bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		if !strings.HasPrefix(subject, "session-") && subject != "mcp-tools-call" {
			return nil
		}
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		r.mu.Lock()
		r.evs = append(r.evs, inboxEv{subject: subject, m: m})
		r.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("recorder subscribe: %v", err)
	}
	return r
}

func (r *inboxRec) matches(subject string, pred func(map[string]any) bool) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, e := range r.evs {
		if e.subject != subject {
			continue
		}
		if pred == nil || pred(e.m) {
			out = append(out, e.m)
		}
	}
	return out
}

func (r *inboxRec) count(subject string, pred func(map[string]any) bool) int {
	return len(r.matches(subject, pred))
}

func (r *inboxRec) wait(t *testing.T, subject string, pred func(map[string]any) bool, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := r.matches(subject, pred); len(got) > 0 {
			return got[len(got)-1]
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("等待事件超时：%s（timeout=%s）", subject, timeout)
	return nil
}

// ── mock LLM（按请求消息角色分支）────────────────────────────────────────

type llmReqRec struct {
	mu   sync.Mutex
	reqs [][]ChatMsg
}

func (r *llmReqRec) snapshot() [][]ChatMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]ChatMsg, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// llmSlowSSE 逐块写出并在块间 sleep（制造真实的 streaming 窗口；测试专用）。
func llmSlowSSE(w http.ResponseWriter, chunks []string, gap time.Duration) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(gap)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

func newInboxLLM(t *testing.T) (*httptest.Server, *llmReqRec) {
	t.Helper()
	rec := &llmReqRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, body.Messages)
		rec.mu.Unlock()

		var last *ChatMsg
		for i := len(body.Messages) - 1; i >= 0; i-- {
			if body.Messages[i].Role != "system" {
				last = &body.Messages[i]
				break
			}
		}
		if last == nil {
			llmSSE(w, []string{sseChunk(map[string]any{"content": "好"}, "stop")})
			return
		}
		switch last.Role {
		case "tool":
			text := "主轮续轮完成"
			if strings.Contains(last.Content, "C2结果") {
				text = "插入轮完成"
			}
			llmSSE(w, []string{sseChunk(map[string]any{"content": text}, "stop")})
		case "assistant":
			llmSSE(w, []string{sseChunk(map[string]any{"content": "主轮续轮完成"}, "stop")})
		case "user":
			content := last.Content
			switch {
			case strings.Contains(content, "慢输出"):
				// 慢速流式输出：给用例留出"流式期插入"的窗口。
				var chunks []string
				for _, part := range []string{"慢", "输出", "中"} {
					chunks = append(chunks, sseChunk(map[string]any{"content": part}, ""))
				}
				chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
				llmSlowSSE(w, chunks, 180*time.Millisecond)
			case strings.Contains(content, "插话慢"):
				// 插入轮慢速输出：给用例留出"插入仍在流式、回执已到"的 S5 窗口。
				llmSlowSSE(w, []string{
					sseChunk(map[string]any{"content": "插话"}, ""),
					sseChunk(map[string]any{"content": "慢完成"}, ""),
					sseChunk(map[string]any{}, "stop"),
				}, 250*time.Millisecond)
			case strings.Contains(content, "慢工具"):
				args := jb(map[string]any{"_inbox_manual": true, "tool_call_display_name": "慢工具"})
				llmSSE(w, []string{sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-main", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls")})
			case strings.Contains(content, "带工具"):
				args := jb(map[string]any{"_inbox_manual": true, "tool_call_display_name": "插话工具"})
				llmSSE(w, []string{sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-ins", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls")})
			default:
				llmSSE(w, []string{sseChunk(map[string]any{"content": "插入回答:" + content}, "stop")})
			}
		default:
			llmSSE(w, []string{sseChunk(map[string]any{"content": "好"}, "stop")})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// ── 驱动助手 ────────────────────────────────────────────────────────────

func inboxAddMQ(t *testing.T, s *Server, session, content string) map[string]any {
	t.Helper()
	v := s.bus.Emit(context.Background(), "inbox-add", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "llm": "mock", "content": content,
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("llm-inbox-add error: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("llm-inbox-add 无应答")
	}
	return res
}

func sendMQ(t *testing.T, s *Server, session, turn, content string) {
	t.Helper()
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn,
		"type": "text-user", "content": content,
	}))
}

func reportTask(t *testing.T, s *Server, taskID, result, session, turn string) {
	t.Helper()
	s.bus.Emit(context.Background(), "mcp-tasks-report", jb(map[string]any{
		"task_id": taskID, "tool": "core_file_read", "state": "done", "result_summary": result,
		"session": session, "turn": turn, "instance_id": "ins-test",
	}))
}

func byTurn(turn string) func(map[string]any) bool {
	return func(m map[string]any) bool { return str(m["turn"]) == turn }
}

// waitInsertable 等当前帧回到可插入相位（工具等待期）。插入帧出栈 / 主帧 pending 均在
// loop goroutine 内完成，事件到达时可能尚未切换——白盒轮询仅作**时序同步**（L1 补充），
// 主链路断言仍走 mq。
func waitInsertable(t *testing.T, s *Server, session string) {
	t.Helper()
	for i := 0; i < 150; i++ {
		if tc := s.turnBySessionForAdd("ins-test", session); tc != nil && tc.insertable() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待可插入相位超时（session=%s）", session)
}

func textContains(sub string) func(map[string]any) bool {
	return func(m map[string]any) bool {
		if str(m["type"]) != "text" {
			return false
		}
		p, _ := m["payload"].(map[string]any)
		return strings.Contains(str(p["text"]), sub)
	}
}

// TestInboxInsertDuringToolPending（INS-001 / S1）：工具执行中可插入，且插入轮上下文里的
// 未完成工具投影为"执行中"占位（非空串）。
func TestInboxInsertDuringToolPending(t *testing.T) {
	llm, reqs := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins1", "t-ins1")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins1", "t-ins1", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)

	res := inboxAddMQ(t, s, "s-ins1", "【INS-001】插话A")
	if ok, _ := res["ok"].(bool); !ok || str(res["type"]) != "insert" {
		t.Fatalf("工具等待期应分类为 insert：%+v", res)
	}
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)
	if n := rec.count("session-complete", byTurn("t-ins1")); n != 0 {
		t.Fatalf("插入期间不得发 session-complete（got %d）", n)
	}
	// 插入轮请求里 C1 的 tool 内容必须是"执行中"占位（§3.8）。
	seenPlaceholder := false
	for _, msgs := range reqs.snapshot() {
		for _, m := range msgs {
			if m.Role == "tool" && strings.Contains(m.Content, "工具执行中") {
				seenPlaceholder = true
			}
		}
	}
	if !seenPlaceholder {
		t.Fatalf("插入轮上下文缺少运行中工具的占位投影")
	}

	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins1", "t-ins1")
	rec.wait(t, "session-receive", textContains("主轮续轮完成"), 5*time.Second)
	rec.wait(t, "session-complete", byTurn("t-ins1"), 5*time.Second)
	if n := rec.count("session-complete", byTurn("t-ins1")); n != 1 {
		t.Fatalf("session-complete 应恰一次（got %d）", n)
	}
}

// TestInboxInsertNestedTool（INS-003 / S3）：插入轮自身可调用工具（嵌套），主轮保持挂起。
func TestInboxInsertNestedTool(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins3", "t-ins3")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins3", "t-ins3", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)

	res := inboxAddMQ(t, s, "s-ins3", "【INS-003】插话带工具")
	if str(res["type"]) != "insert" {
		t.Fatalf("应为 insert：%+v", res)
	}
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-ins" }, 5*time.Second)
	if n := rec.count("session-complete", byTurn("t-ins3")); n != 0 {
		t.Fatalf("嵌套工具期间不得 complete")
	}
	reportTask(t, s, "tk-tc-ins", "C2结果", "s-ins3", "t-ins3")
	rec.wait(t, "session-receive", textContains("插入轮完成"), 5*time.Second)
	if n := rec.count("session-complete", byTurn("t-ins3")); n != 0 {
		t.Fatalf("插入轮结束后主轮工具仍 pending → 不得 complete")
	}
	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins3", "t-ins3")
	rec.wait(t, "session-receive", textContains("主轮续轮完成"), 5*time.Second)
	rec.wait(t, "session-complete", byTurn("t-ins3"), 5*time.Second)
}

// TestInboxAwaitJoin（INS-004 / S4）：插入结束、原工具仍跑 → 不续轮；回执到达后续一次。
func TestInboxAwaitJoin(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins4", "t-ins4")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins4", "t-ins4", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)
	inboxAddMQ(t, s, "s-ins4", "【INS-004】插话A")
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)
	// 插入已结束，原工具未回执 → 不得续轮
	time.Sleep(150 * time.Millisecond)
	if n := rec.count("session-receive", textContains("主轮续轮完成")); n != 0 {
		t.Fatalf("awaitJoin 期不得续轮（got %d）", n)
	}
	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins4", "t-ins4")
	rec.wait(t, "session-receive", textContains("主轮续轮完成"), 5*time.Second)
	rec.wait(t, "session-complete", byTurn("t-ins4"), 5*time.Second)
}

// TestInboxJoinToolDoneFirst（INS-005 / S5）：回执先到、插入后结束 → 插入出栈后立即续轮（仅一次）。
func TestInboxJoinToolDoneFirst(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins5", "t-ins5")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins5", "t-ins5", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)
	// 插入轮慢速输出（约 750ms），给"回执夹在插入中间"留窗口
	inboxAddMQ(t, s, "s-ins5", "【INS-005】插话慢")
	rec.wait(t, "session-receive", textContains("插话"), 5*time.Second)
	// 插入仍在流式 → 外层回执到达（清 pending，但栈顶是插入帧 → 不得续轮）
	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins5", "t-ins5")
	time.Sleep(120 * time.Millisecond)
	if n := rec.count("session-receive", textContains("主轮续轮完成")); n != 0 {
		t.Fatalf("插入帧仍是栈顶 → 不得续主轮（got %d）", n)
	}
	// 插入结束后立即恢复主轮（只一次）
	rec.wait(t, "session-receive", textContains("主轮续轮完成"), 5*time.Second)
	rec.wait(t, "session-complete", byTurn("t-ins5"), 5*time.Second)
	if n := rec.count("session-receive", textContains("主轮续轮完成")); n != 1 {
		t.Fatalf("主轮只应续一次（got %d）", n)
	}
}

// TestInboxReinsertAwaitJoin（INS-006 / S4.5）：等待期可再次插入，主轮只续一次。
func TestInboxReinsertAwaitJoin(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins6", "t-ins6")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins6", "t-ins6", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)

	inboxAddMQ(t, s, "s-ins6", "【INS-006】插话A")
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)
	// 已回到 awaitJoin（主帧 pending 非空）→ 可再插入
	waitInsertable(t, s, "s-ins6")
	res := inboxAddMQ(t, s, "s-ins6", "【INS-006】插话C")
	if str(res["type"]) != "insert" {
		t.Fatalf("awaitJoin 应可再插入：%+v", res)
	}
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)
	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins6", "t-ins6")
	rec.wait(t, "session-receive", textContains("主轮续轮完成"), 5*time.Second)
	rec.wait(t, "session-complete", byTurn("t-ins6"), 5*time.Second)
	if n := rec.count("session-receive", textContains("主轮续轮完成")); n != 1 {
		t.Fatalf("多次插话后主轮只应续一次（got %d）", n)
	}
}

// TestInboxDuplicateReportIdempotent（INS-009）：重复回执不重复续轮。
func TestInboxDuplicateReportIdempotent(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins9", "t-ins9")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins9", "t-ins9", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)
	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins9", "t-ins9")
	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins9", "t-ins9") // 重复
	rec.wait(t, "session-complete", byTurn("t-ins9"), 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	if n := rec.count("session-receive", textContains("主轮续轮完成")); n != 1 {
		t.Fatalf("重复回执不得重复续轮（got %d）", n)
	}
	if n := rec.count("session-complete", byTurn("t-ins9")); n != 1 {
		t.Fatalf("session-complete 应恰一次（got %d）", n)
	}
}

// TestInboxStreamingNextTurn（INS-002 / INS-010）：流式期插入 → next_turn；本轮结束后
// 自动起新一轮消费（新 session-turn-start + receive + complete），原轮终态恰一次。
func TestInboxStreamingNextTurn(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins2", "t-ins2")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins2", "t-ins2", "慢输出")
	// 等首片流输出（进入 streaming 相位）
	rec.wait(t, "session-receive", func(m map[string]any) bool { return str(m["type"]) == "text" }, 5*time.Second)
	res := inboxAddMQ(t, s, "s-ins2", "【INS-002】插话B")
	if str(res["type"]) != "next_turn" {
		t.Fatalf("流式期应分类为 next_turn：%+v", res)
	}
	rec.wait(t, "session-complete", func(m map[string]any) bool { return str(m["status"]) == "complete" }, 5*time.Second)
	// 新一轮（turn != t-ins2）由 next_turn 自动消费
	rec.wait(t, "session-turn-start", func(m map[string]any) bool { return str(m["session"]) == "s-ins2" && str(m["turn"]) != "t-ins2" }, 5*time.Second)
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)
	// 等新一轮终态（否则清理阶段仍有在飞轮次 → bus 关闭 / TempDir 占用）
	rec.wait(t, "session-complete", func(m map[string]any) bool {
		return str(m["session"]) == "s-ins2" && str(m["turn"]) != "t-ins2"
	}, 5*time.Second)
}

// TestInboxPromoteNextTurn（INS-018）：next_turn 显式提升为 insert 并立即消费。
func TestInboxPromoteNextTurn(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins18", "t-ins18")
	rec := newInboxRec(t, s.bus)

	// 先造一条 next_turn（无运行轮 → 服务端会立即消费；因此用 data 面直接落库模拟流式期写入）
	dres, err := dataRequest(s.bus, "data-inbox-append", map[string]any{
		"instance_id": "ins-test",
		"data": map[string]any{
			"session": "s-ins18", "type": "next_turn", "content": "【INS-018】插话M", "llm": "mock",
		},
	})
	if err != nil {
		t.Fatalf("data-inbox-append: %v", err)
	}
	itemID := str(dres["id"])
	// 让 llm-server 记住该 item（list 填充内存索引）
	lv := s.bus.Emit(context.Background(), "inbox-list", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-ins18",
	})).Wait()
	if lv.Err() != nil {
		t.Fatalf("llm-inbox-list: %v", lv.Err())
	}

	sendMQ(t, s, "s-ins18", "t-ins18", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)

	pv := s.bus.Emit(context.Background(), "inbox-promote", jb(map[string]any{
		"instance_id": "ins-test", "id": itemID,
	})).Wait()
	res, _ := pv.Result.(map[string]any)
	if ok, _ := res["ok"].(bool); !ok || str(res["type"]) != "insert" {
		t.Fatalf("提升应成功并变为 insert：%+v", res)
	}
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)
	if n := rec.count("session-complete", byTurn("t-ins18")); n != 0 {
		t.Fatalf("提升后主轮仍挂起，不得 complete")
	}
	reportTask(t, s, "tk-tc-main", "主轮完成", "s-ins18", "t-ins18")
	rec.wait(t, "session-complete", byTurn("t-ins18"), 5*time.Second)
}

// TestInboxMergeNextTurn（INS-013 / Q2）：流式期连发两条 → 均 next_turn；本轮结束后
// **合并为一次**新一轮请求（同一请求体含两条 user），messages 逐条保留。
func TestInboxMergeNextTurn(t *testing.T) {
	llm, reqs := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins13", "t-ins13")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins13", "t-ins13", "慢输出")
	rec.wait(t, "session-receive", func(m map[string]any) bool { return str(m["type"]) == "text" }, 5*time.Second)
	if res := inboxAddMQ(t, s, "s-ins13", "【INS-013】M1"); str(res["type"]) != "next_turn" {
		t.Fatalf("M1 应为 next_turn：%+v", res)
	}
	if res := inboxAddMQ(t, s, "s-ins13", "【INS-013】M2"); str(res["type"]) != "next_turn" {
		t.Fatalf("M2 应为 next_turn：%+v", res)
	}
	rec.wait(t, "session-complete", func(m map[string]any) bool { return str(m["turn"]) == "t-ins13" }, 5*time.Second)
	rec.wait(t, "session-turn-start", func(m map[string]any) bool {
		return str(m["session"]) == "s-ins13" && str(m["turn"]) != "t-ins13"
	}, 5*time.Second)
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)
	rec.wait(t, "session-complete", func(m map[string]any) bool {
		return str(m["session"]) == "s-ins13" && str(m["turn"]) != "t-ins13"
	}, 5*time.Second)

	merged := 0
	for _, msgs := range reqs.snapshot() {
		has1, has2 := false, false
		for _, m := range msgs {
			if m.Role != "user" {
				continue
			}
			if strings.Contains(m.Content, "M1") {
				has1 = true
			}
			if strings.Contains(m.Content, "M2") {
				has2 = true
			}
		}
		if has1 && has2 {
			merged++
		}
	}
	if merged != 1 {
		t.Fatalf("两条 next_turn 应合并为一次请求（got %d）", merged)
	}
}

// TestInboxCancelGlobal（INS-008/015 / Q3-a）：等待期 llm-cancel = 全局终止：
// session-complete{interrupted} 恰一次，之后不再续轮（不回到原任务）。
func TestInboxCancelGlobal(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins8", "t-ins8")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-ins8", "t-ins8", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)
	inboxAddMQ(t, s, "s-ins8", "【INS-008】插话A")
	rec.wait(t, "session-receive", textContains("插入回答"), 5*time.Second)

	v := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-ins8", "turn": "t-ins8",
	})).Wait()
	if v.Err() != nil {
		t.Fatalf("llm-cancel: %v", v.Err())
	}
	done := rec.wait(t, "session-complete", byTurn("t-ins8"), 5*time.Second)
	if str(done["status"]) != "interrupted" {
		t.Fatalf("取消终态应为 interrupted：%+v", done)
	}
	// 回执后不得再续轮
	reportTask(t, s, "tk-tc-main", "C1结果", "s-ins8", "t-ins8")
	time.Sleep(150 * time.Millisecond)
	if n := rec.count("session-receive", textContains("主轮续轮完成")); n != 0 {
		t.Fatalf("取消后不得回到原任务（got %d）", n)
	}
	if n := rec.count("session-complete", byTurn("t-ins8")); n != 1 {
		t.Fatalf("session-complete 应恰一次（got %d）", n)
	}
}

// TestInboxLimit（INS-024）：每会话 pending 上限 50，第 51 条拒绝且不入库。
func TestInboxLimit(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-ins24", "t-ins24")

	append1 := func(i int) map[string]any {
		res, err := dataRequest(s.bus, "data-inbox-append", map[string]any{
			"instance_id": "ins-test",
			"data": map[string]any{
				"session": "s-ins24", "type": "next_turn",
				"content": fmt.Sprintf("【INS-024】M%d", i),
			},
		})
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return res
	}
	for i := 0; i < 50; i++ {
		if res := append1(i); str(res["id"]) == "" {
			t.Fatalf("第 %d 条应写入成功：%+v", i+1, res)
		}
	}
	// 第 51 条：persist 以 ok:false 失败应答（dataRequest 侧表现为错误 → inbox-add ok:false）
	add := inboxAddMQ(t, s, "s-ins24", "【INS-024】M51")
	if ok, _ := add["ok"].(bool); ok {
		t.Fatalf("第 51 条应被拒绝：%+v", add)
	}
}

// ── Q6 顺序/删除语义（先入 inbox 再落 message；撤回后不得再落）────────────────

// TestInboxDiscardBeforeConsume：insert 项入队后、loop 消费前被撤回 → 不得再落 message。
func TestInboxDiscardBeforeConsume(t *testing.T) {
	llm, reqs := newInboxLLM(t)
	s := newTestServer(t, llm)
	startTurn(t, s, "s-del1", "t-del1")
	rec := newInboxRec(t, s.bus)

	sendMQ(t, s, "s-del1", "t-del1", "请用慢工具")
	rec.wait(t, "mcp-tools-call", func(m map[string]any) bool { return str(m["tool_call_id"]) == "tc-main" }, 5*time.Second)
	tc := s.turnBySessionForAdd("ins-test", "s-del1")
	if tc == nil || !tc.insertable() {
		t.Fatalf("前置：主轮应处于可插入相位")
	}
	item := facade.InboxItem{
		ID: "i-del-1", SessionID: "s-del1", Type: "insert",
		Content: "【撤回】不得落库", Status: "pending",
	}
	s.inboxRemember(item)
	s.inboxSetStatus(item.ID, "discarded") // 用户已在消费前撤回

	tc.engineRouteInsert(item)
	time.Sleep(200 * time.Millisecond)
	for _, turnMsgs := range reqs.snapshot() {
		for _, m := range turnMsgs {
			if strings.Contains(m.Content, item.Content) {
				t.Fatalf("已撤回的 insert 项不得进入 LLM 上下文：%q", m.Content)
			}
		}
	}
	if cur, ok := s.inboxLookup(item.ID); !ok || cur.Status != "discarded" {
		t.Fatalf("撤回状态被改写：%+v ok=%v", cur, ok)
	}
}

// TestInboxDiscardConsumedRejected：已 consumed（消息已落库）的项不可再撤回（ok=false）。
func TestInboxDiscardConsumedRejected(t *testing.T) {
	llm, _ := newInboxLLM(t)
	s := newTestServer(t, llm)
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	t.Cleanup(data.Reset)
	time.Sleep(50 * time.Millisecond)
	rec := newInboxRec(t, s.bus)

	res := inboxAddMQ(t, s, "s-del2", "【已发送】")
	id := str(res["id"])
	rec.wait(t, "session-complete", nil, 5*time.Second)

	v := s.bus.Emit(context.Background(), "inbox-discard", jb(map[string]any{
		"instance_id": "ins-test", "id": id,
	})).Wait()
	r, _ := v.Result.(map[string]any)
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("已 consumed 的项不应允许撤回：%+v", r)
	}
	if cur, ok := s.inboxLookup(id); !ok || cur.Status != "consumed" {
		t.Fatalf("撤回不得改写已消费状态：%+v ok=%v", cur, ok)
	}
}

// TestInboxNextTurnBatchSkipsDiscarded：next_turn 批次消费时逐条复核 pending ——
// 批次读取后被撤回的项不得落 message（其余照常消费）。
func TestInboxNextTurnBatchSkipsDiscarded(t *testing.T) {
	llm, reqs := newInboxLLM(t)
	s := newTestServer(t, llm)
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	t.Cleanup(data.Reset)
	time.Sleep(50 * time.Millisecond)

	appendItem := func(content string) string {
		res, err := dataRequest(s.bus, "data-inbox-append", map[string]any{
			"instance_id": "ins-test",
			"data": map[string]any{
				"session": "s-batch", "type": "next_turn", "content": content, "llm": "mock",
			},
		})
		if err != nil {
			t.Fatalf("append %q: %v", content, err)
		}
		return str(res["id"])
	}
	idKeep := appendItem("【保留】A")
	idDrop := appendItem("【撤回】B")
	// 内存索引（consumeNextTurn 的 pending 门槛/复核依赖它）
	s.inboxRemember(facade.InboxItem{ID: idKeep, SessionID: "s-batch", Type: "next_turn", Content: "【保留】A", Status: "pending"})
	s.inboxRemember(facade.InboxItem{ID: idDrop, SessionID: "s-batch", Type: "next_turn", Content: "【撤回】B", Status: "pending"})
	if v := s.bus.Emit(context.Background(), "inbox-discard", jb(map[string]any{
		"instance_id": "ins-test", "id": idDrop,
	})).Wait(); v.Err() != nil {
		t.Fatalf("discard: %v", v.Err())
	}

	rec := newInboxRec(t, s.bus)
	s.consumeNextTurn("ins-test", "s-batch")
	rec.wait(t, "session-complete", nil, 5*time.Second)

	seenKeep := false
	for _, turnMsgs := range reqs.snapshot() {
		for _, m := range turnMsgs {
			if strings.Contains(m.Content, "【撤回】B") {
				t.Fatalf("已撤回的 next_turn 项不得落 message")
			}
			if strings.Contains(m.Content, "【保留】A") {
				seenKeep = true
			}
		}
	}
	if !seenKeep {
		t.Fatalf("未撤回项应正常消费")
	}
}
