package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	"github.com/chonkpilot/chonkpilot-router"
	tasklayer "github.com/chonkpilot/chonkpilot-task"
)

// testBusPrefix 测试总线命名空间前缀（与生产 server/gui main.go 一致：业务只写相对主题）。
const testBusPrefix = "chonk."

// lastText 取最后一条非 system 非空文本（对齐 mock_llm_srv 语义）。
func lastText(msgs []ChatMsg) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "system" {
			continue
		}
		if msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return "Hello"
}

// execSinkRecorder 记录**执行侧控制面**（进程内 sink）收到的取消/转后台调用：
// 2026-09-18 起 `mcp-tasks-cancel` / `mcp-tools-background` 方法面已移除 → 取消/转后台经
// `ExecSink.CancelExec` / `DetachExec` 直调执行池，测试据此断言（取代原 fake gateway 的
// MQ 方法面记录）。
type execSinkRecorder struct {
	mu         sync.Mutex
	cancelled  []string // CancelExec 收到的引用（gw_task_id）
	cancelInst []string // CancelExec 收到的 instance 归属（缺口 2：执行池按 instance 分桶）
	detached   []string // DetachExec 收到的引用（gw_task_id 或 tool_call_id）
	detachInst []string // DetachExec 收到的 instance 归属
	states     map[string]string
	execEvents []mcpgateway.ExecState
}

var testExecSink = &execSinkRecorder{}

func (r *execSinkRecorder) OnExecState(ev mcpgateway.ExecState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execEvents = append(r.execEvents, ev)
}

func (r *execSinkRecorder) ExecStateOf(gwTaskID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.states[gwTaskID]
	return st, ok
}

// CancelExec 记录取消与 instance 归属（返回成功，模拟执行池已取消）。
func (r *execSinkRecorder) CancelExec(instanceID, gwTaskID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelled = append(r.cancelled, gwTaskID)
	r.cancelInst = append(r.cancelInst, instanceID)
	return true, nil
}

// DetachExec 记录转后台与 instance 归属（返回固定 task_id，模拟执行池已解绑）。
func (r *execSinkRecorder) DetachExec(instanceID, idOrCall string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detached = append(r.detached, idOrCall)
	r.detachInst = append(r.detachInst, instanceID)
	return "tk-bg", nil
}

func (r *execSinkRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelled, r.cancelInst, r.detached, r.detachInst, r.execEvents = nil, nil, nil, nil, nil
}

func (r *execSinkRecorder) cancelledRefs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.cancelled...)
}

func (r *execSinkRecorder) detachedRefs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.detached...)
}

// cancelInstances / detachInstances 取收到的 instance 归属快照（缺口 2）。
func (r *execSinkRecorder) cancelInstances() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.cancelInst...)
}

func (r *execSinkRecorder) detachInstances() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.detachInst...)
}

// resetExecControl 清空记录（用例前置）。
func resetExecControl() { testExecSink.reset() }

// execCancelledRefs / execDetachedRefs 取记录快照（用例断言）。
func execCancelledRefs() []string { return testExecSink.cancelledRefs() }
func execDetachedRefs() []string  { return testExecSink.detachedRefs() }

// execCancelledInstances / execDetachedInstances 取收到的 instance 归属快照（缺口 2 断言）。
func execCancelledInstances() []string { return testExecSink.cancelInstances() }
func execDetachedInstances() []string  { return testExecSink.detachInstances() }

var (
	// fakeStopTaskID / fakeGetResultID：mock LLM 分支注入（tool_stop / tool_result 的 id）。
	fakeStopTaskID  string
	fakeGetResultID string
	// fakeGWCallCtx 记录 fake gateway 收到的 tools/call 上下文（键 = tool_call_id → {top_session, parent}）：
	// I-90 断言「llm 确实把任务树归属随调用上下文下发」（gateway 随 mcp-tasks-report 回传 → 层建节点）。
	fakeGWCallCtxMu sync.Mutex
	fakeGWCallCtx   = map[string][2]string{}
)

// fakeGateway 模拟 gateway（v2 对称 promise 面，61-消息一览 §5）：mcp-tools-list /
// mcp-tools-call；`_async` → pending + 延迟 mcp-tasks-report 完成回报（fire-and-forget）。
// 2026-09-18：`mcp-tasks-*` / `mcp-tools-background` 方法面已移除 → 不再有对应 stub；
// 取消 / 转后台经进程内 sink（testExecSink，见 newTestServer 注入）。
func fakeGateway(bus mq.Bus, s *Server, taskDoneDelay time.Duration) {
	_, _ = bus.On("mcp-tools-list", 0, func(_ context.Context, _ string, v *mq.Value) error {
		v.Result = map[string]any{"resultType": "complete", "tools": []any{}, "ttlMs": 0}
		return nil
	})
	_, _ = bus.On("mcp-tools-call", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req mcpgateway.CallReq
		if json.Unmarshal(v.Payload, &req) != nil {
			return nil
		}
		fakeGWCallCtxMu.Lock()
		fakeGWCallCtx[req.ToolCallID] = [2]string{req.TopSession, req.Parent}
		fakeGWCallCtxMu.Unlock()
		// 域工具（task 型）：模拟 gateway 注册回调（21-llm-server）——
		// server 执行并返回文本，节点由 handler 建/广播；普通工具走下方固定回复。
		if s != nil && isTaskTool(req.Name) {
			tc := s.lookupTurn(req.Turn)
			if tc != nil {
				node, _ := s.domainNode(tc, req.ToolCallID, req.Name, req.Arguments)
				text := s.execTaskTool(tc, req.ToolCallID, node, req.Name, req.Arguments, context.WithoutCancel(tc.ctx))
				if strings.HasPrefix(text, "错误") {
					s.tasks.done(node.TaskID, TaskStateError, "", text)
				} else {
					s.tasks.done(node.TaskID, TaskStateDone, text, "")
				}
				v.Result = map[string]any{
					"resultType": "complete",
					"content":    []any{map[string]any{"type": "text", "text": text}},
				}
				return nil
			}
		}
		if cancel, _ := req.Arguments["_cancel"].(bool); cancel {
			// 模拟 gateway 对「超时裁决取消」的同步返回：结构化取消标记（task 节点应落 cancelled）
			v.Result = map[string]any{
				"resultType":        "complete",
				"content":           []any{map[string]any{"type": "text", "text": "任务结束(cancelled)"}},
				"isError":           true,
				"structuredContent": map[string]any{"status": "cancelled", "task_id": "tk-cancel"},
			}
			return nil
		}
		if async, _ := req.Arguments["_async"].(bool); async {
			v.Result = map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": "任务已转后台执行"}},
				"structuredContent": map[string]any{"status": "pending", "task_id": "tk-test"},
			}
			// 模拟异步完成：延迟发 mcp-tasks-report（**result_summary = 终态结果全文**，
			// 2026-09-18 起该通知面是结果唯一交付通道；server 据此落 message 表并回填续轮）。
			go func() {
				time.Sleep(taskDoneDelay)
				bus.Emit(context.Background(), "mcp-tasks-report", jb(map[string]any{
					"task_id": "tk-test", "tool": req.Name, "state": "done",
					"result_summary": "async file content", "session": req.Session, "turn": req.Turn,
					"instance_id": req.InstanceID,
				}))
			}()
			return nil
		}
		v.Result = map[string]any{
			"resultType": "complete",
			"content":    []any{map[string]any{"type": "text", "text": "file content"}},
		}
		return nil
	})
}

func jb(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// mockLLMServer 按最后文本分支返回 SSE（length/empty/async/auth/默认回显）。
func mockLLMServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "auth"):
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"bad key"}`)
			return
		case strings.Contains(lower, "length"):
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": "part"}, ""),
				sseChunk(map[string]any{}, "length"),
			})
		case strings.Contains(lower, "empty"):
			llmSSE(w, []string{sseChunk(map[string]any{}, "stop")})
		case strings.Contains(lower, "async tool"):
			args := jb(map[string]any{"_async": true})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-1", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(lower, "sync tool"):
			args := jb(map[string]any{"tool_call_display_name": "sync", "path": "a.txt"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-sync", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(lower, "cancel tool please"):
			// 同步工具返回结构化取消标记（模拟超时裁决取消；I-62）
			args := jb(map[string]any{"tool_call_display_name": "取消探针", "_cancel": true})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-cancel", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(lower, "ask please"):
			args := jb(map[string]any{
				"question": "确认继续？",
				"options": []any{
					map[string]any{"value": "y", "label": "是"},
					map[string]any{"value": "n", "label": "否"},
				},
				"multi":       false,
				"recommended": "y",
			})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-ask", "type": "function",
					"function": map[string]any{"name": "ask_user", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(lower, "stop please"):
			args := jb(map[string]any{"task_id": fakeStopTaskID, "tool_call_display_name": "停止任务"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-stop", "type": "function",
					"function": map[string]any{"name": "tool_stop", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		case strings.Contains(lower, "get result please"):
			args := jb(map[string]any{"id": fakeGetResultID, "timeout": 5, "tool_call_display_name": "获取结果"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-gr", "type": "function",
					"function": map[string]any{"name": "tool_result", "arguments": string(args)},
				}}}, "tool_calls"),
			})
		default:
			chunks := []string{}
			for _, ch := range text {
				chunks = append(chunks, sseChunk(map[string]any{"content": string(ch)}, ""))
			}
			chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
			llmSSE(w, chunks)
		}
	}))
}

// testWorkDir 单测共享 workdir：同测试内多次 startTurn 复用同一绑定（instance ↔ workdir 恒定）。
var testWorkDir string

// newTestServer 建完整测试环境（内部 MQ + 内嵌 persist 数据服务 + mock LLM + fake gateway）。
func newTestServer(t *testing.T, llmSrv *httptest.Server) *Server {
	t.Helper()
	data.Reset()
	testWorkDir = t.TempDir()
	t.Cleanup(func() { testWorkDir = "" })
	bus, err := mq.New(mq.Options{Prefix: testBusPrefix})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	// DisableMCP：测试用 fakeGateway 模拟内嵌 gateway（fake 对域工具转发 server 执行）；
	// 真实内嵌 gateway（inprocess://）链路由专门的 mcp 集成测试覆盖。
	// UsrPath：注入临时 usr 库（避免污染/占用 ~/.chonkpilot 用户库；persist 数据服务同路径）。
	s := New(bus, Options{LLMBase: llmSrv.URL, LLMModel: "mock", DisableMCP: true,
		UsrPath: t.TempDir() + "/usr.db",
		// 认证域（61 §4.6；阶段 2b-1）：auth 库 / 用户数据根各用临时目录（默认在 exe 旁 →
		// 会跨用例共享文件与用户表 → 必须隔离）。
		AuthDBPath:   t.TempDir() + "/auth.db",
		UserDataRoot: t.TempDir()})
	// 执行侧控制面注入（与生产装配同形：DisableMCP 时 gateway 不存在 → 由测试用记录器模拟执行池）。
	// 取消 / 转后台经此 sink 直调（2026-09-18：mcp-tasks-cancel / mcp-tools-background 方法面已移除）。
	s.execSink = testExecSink
	fakeGateway(bus, s, 150*time.Millisecond)
	s.locksDir = t.TempDir() // session 锁根注入临时目录（不污染 ~/.chonkpilot/locks）
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// startTurn 注册 instance + 发 llm-start 并等 ack（使用 Emit + Wait，2026-09-04 移除 reply 过渡期机制）。
func startTurn(t *testing.T, s *Server, session, turn string) {
	t.Helper()
	// instance-register（客户端自生成 uuid，绑定共享 workdir——instance ↔ workdir 恒定）
	s.bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": testWorkDir,
	}))
	// 数据层连接缓存关闭（data.Prj 打开 workdir 库）需先于 TempDir 清理（LIFO）
	t.Cleanup(data.Reset)
	time.Sleep(50 * time.Millisecond)
	f := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-" + turn, "instance_id": "ins-test", "session": session, "turn": turn,
	}))
	v := f.Wait()
	if v.Err() != nil {
		t.Fatalf("llm-start error: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("llm-start rejected: %+v", res)
	}
}

// collectTurn 订阅 ">"（相对全通配）并按 session- 域前缀过滤，收集轮次事件直到
// llm-complete（相对主题 session-complete）。2026-09-03 域化后 llm-* 事件在 session-* 域
// （receive/complete/compress），主题为 kebab 一体（handler 收到去 chonk. 前缀的相对主题）。
func collectTurn(t *testing.T, bus mq.Bus, turn string, timeout time.Duration) []map[string]any {
	t.Helper()
	var mu sync.Mutex
	var events []map[string]any
	done := make(chan struct{})
	_, _ = bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		p := v.Payload
		if !strings.HasPrefix(subject, "session-") {
			return nil
		}
		var m map[string]any
		if json.Unmarshal(p, &m) != nil {
			return nil
		}
		if m["turn"] != turn {
			return nil
		}
		mu.Lock()
		events = append(events, m)
		mu.Unlock()
		if subject == "session-complete" {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		return nil
	})
	select {
	case <-done:
	case <-time.After(timeout):
	}
	mu.Lock()
	defer mu.Unlock()
	return events
}

func lastComplete(events []map[string]any) map[string]any {
	for i := len(events) - 1; i >= 0; i-- {
		if _, ok := events[i]["status"]; ok { // llm-complete 载荷含 status
			return events[i]
		}
	}
	return nil
}

// TestTurnLengthAutoContinue：length 截断 → 自动续写（内部"继续"）→ complete。
func TestTurnLengthAutoContinue(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-len", "t-len")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-len", "turn": "t-len",
		"type": "text-user", "content": "length please",
	}))
	evs := collectTurn(t, s.bus, "t-len", 10*time.Second)
	c := lastComplete(evs)
	if c == nil {
		t.Fatalf("no complete: %+v", evs)
	}
	if c["status"] != "complete" {
		t.Fatalf("status=%v want complete (自动续写后): %+v", c["status"], evs)
	}
	// 自动续写注入的 user 消息应带 Kind=continue（供组装/压缩识别为同一轮，不当作新轮边界）
	msgs := newSessionStore(s.bus, "ins-test").LoadMessages("t-len")
	found := false
	for _, m := range msgs {
		if m.Role == "user" && m.Kind == assembleContinueKind {
			found = true
		}
	}
	if !found {
		t.Fatalf("自动续写注入消息应带 Kind=%q：%+v", assembleContinueKind, msgs)
	}
}

// TestTurnEmptyReply：stop 但空内容 → llm-complete{status:error, code:EMPTY_REPLY, retryable:false}。
// retryable=false（S21）：空回复不静默自动续写，前端应"提示 + 手动继续"。
func TestTurnEmptyReply(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-empty", "t-empty")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-empty", "turn": "t-empty",
		"type": "text-user", "content": "empty please",
	}))
	evs := collectTurn(t, s.bus, "t-empty", 10*time.Second)
	found := false
	for _, e := range evs {
		if code, _ := e["code"].(string); code == "EMPTY_REPLY" {
			found = true
			if e["retryable"] != false {
				t.Fatalf("EMPTY_REPLY 应 retryable=false，got %v（payload=%+v）", e["retryable"], e)
			}
		}
	}
	if !found {
		t.Fatalf("no EMPTY_REPLY: %+v", evs)
	}
}

// TestTurnAuthImmediate：401（不可重试）→ 立即 llm-complete error（不可重试），无重试等待。
func TestTurnAuthImmediate(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-auth", "t-auth")

	start := time.Now()
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-auth", "turn": "t-auth",
		"type": "text-user", "content": "auth please",
	}))
	evs := collectTurn(t, s.bus, "t-auth", 10*time.Second)
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("401 should fail immediately, took %v", el)
	}
	found := false
	for _, e := range evs {
		if e["code"] != nil {
			found = true
			// 401 → ErrAuth（不可重试）：retryable=false（S21 前端据此显示错误气泡 + 手动继续）
			if e["retryable"] != false {
				t.Fatalf("401 应 retryable=false，got %v（payload=%+v）", e["retryable"], e)
			}
		}
	}
	if !found {
		t.Fatalf("no llm-complete error: %+v", evs)
	}
}

// TestTurnAsyncPending：工具转异步（pending）→ turn 挂起 → tasks.done 续轮 → complete。
func TestTurnAsyncPending(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-async", "t-async")

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-async", "turn": "t-async",
		"type": "text-user", "content": "async tool please",
	}))
	evs := collectTurn(t, s.bus, "t-async", 10*time.Second)
	c := lastComplete(evs)
	if c == nil {
		t.Fatalf("no complete: %+v", evs)
	}
	if c["status"] != "complete" {
		t.Fatalf("status=%v: %+v", c["status"], evs)
	}
	// 续轮结果：llm-complete 的 text 是完整最终文本（流式字符事件经 MQ 异步派发会乱序，不可拼接断言）
	finalText, _ := c["text"].(string)
	if strings.Contains(finalText, "任务已转后台") {
		t.Fatalf("喂回的是转后台占位文本（S4 未生效）: %q", finalText)
	}
	if !strings.Contains(finalText, "file content") {
		t.Fatalf("未包含异步结果: %q", finalText)
	}
}

// TestLLMCompressEvent（C2 端到端）：唯一终态 llm-complete → 写快照（snapshot_turn=本轮）
// → 发 llm-compress（载荷含 instance_id/work_dir/data_dir/session/last_turn/snapshot_turn）。
func TestLLMCompressEvent(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-comp", "t1")

	var compressEv map[string]any
	var compressMu sync.Mutex
	compSub, err := s.bus.On("session-compress", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		compressMu.Lock()
		compressEv = m
		compressMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer compSub.Unsubscribe()

	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-comp", "turn": "t1",
		"type": "text-user", "content": "hello compress",
	}))
	evs := collectTurn(t, s.bus, "t1", 10*time.Second)
	if lastComplete(evs) == nil {
		t.Fatalf("no complete: %+v", evs)
	}
	// llm-compress 事件（MQ 异步派发，轮询）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		compressMu.Lock()
		got := compressEv != nil
		compressMu.Unlock()
		if got {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	compressMu.Lock()
	ev := compressEv
	compressMu.Unlock()
	if ev == nil {
		t.Fatal("no llm-compress event")
	}
	if ev["session"] != "s-comp" || ev["last_turn"] != "t1" || ev["snapshot_turn"] != "t1" {
		t.Fatalf("compress payload wrong: %+v", ev)
	}
	if ev["instance_id"] != "ins-test" || ev["work_dir"] != testWorkDir {
		t.Fatalf("compress payload missing instance/work_dir: %+v", ev)
	}
	// 未配置 provider（无 max_context_token / max_output_token）→ **不带**这两个可选键（与旧载荷逐字节等价）；
	// 载荷字段已统一 snake_case（口径 Z4）——旧名（camelCase `maxContextToken` / 更早 `window`）**不得再出现**。
	for _, k := range []string{
		"max_context_token", "max_output_token",
		"maxContextToken", "maxOutputToken", "window",
	} {
		if _, ok := ev[k]; ok {
			t.Fatalf("未配置 provider 不应带键 %q: %+v", k, ev)
		}
	}

	// 快照已落库（经 persist data-snapshot-get 断言）：snapshot_turn = t1，history 含本轮用户消息
	snap := snapshotOf(t, s, "s-comp")
	if snap == nil || str(snap["snapshot_turn"]) != "t1" {
		t.Fatalf("snapshot not written with turn: %+v", snap)
	}
	hist, _ := snap["history"].([]any)
	found := false
	for _, m := range hist {
		mm, _ := m.(map[string]any)
		if mm != nil && str(mm["content"]) == "hello compress" {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot missing user msg: %+v", snap)
	}
}

// TestLLMCompressEventCarriesContextTokens（口径 Z3/Z4，2026-09-25）：provider 配置
// maxContextToken / maxOutputToken → session-compress 载荷携带 **snake_case** 同义字段
// `max_context_token` / `max_output_token`（兜底归并的真窗口 + 输出预留）；
// 未配置则不带键（见 TestLLMCompressEvent 的缺省断言）。
func TestLLMCompressEventCarriesContextTokens(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{
		"name": "prov-ctx", "baseUrl": srv.URL, "model": "m1",
		"maxContextToken": 128000, "maxOutputToken": 4096,
	})

	var compressEv map[string]any
	var compressMu sync.Mutex
	compSub, err := s.bus.On("session-compress", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		compressMu.Lock()
		compressEv = m
		compressMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer compSub.Unsubscribe()

	startTurnLLM(t, s, "s-ctx", "t-ctx", "prov-ctx", "on", "high")
	sendAndWait(t, s, "s-ctx", "t-ctx", "hello ctx")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		compressMu.Lock()
		got := compressEv != nil
		compressMu.Unlock()
		if got {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	compressMu.Lock()
	ev := compressEv
	compressMu.Unlock()
	if ev == nil {
		t.Fatal("no llm-compress event")
	}
	if got, ok := ev["max_context_token"].(float64); !ok || int(got) != 128000 {
		t.Fatalf("max_context_token=%v want 128000（provider maxContextToken 透传）", ev["max_context_token"])
	}
	if got, ok := ev["max_output_token"].(float64); !ok || int(got) != 4096 {
		t.Fatalf("max_output_token=%v want 4096（provider maxOutputToken 透传）", ev["max_output_token"])
	}
	if n := rec.count(); n == 0 {
		t.Fatal("provider 端点未被请求")
	}
}

// TestTurnSyncToolPersisted：同步工具结果落一条 role=tool 记录（content={call,result}；
// tool_call_status=completed），且下一条 LLM 请求里同一 tool_call_id 只出现一次（不重复）。
func TestTurnSyncToolPersisted(t *testing.T) {
	var mu sync.Mutex
	var reqs [][]ChatMsg
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		reqs = append(reqs, body.Messages)
		mu.Unlock()
		hasTool := false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasTool = true
			}
		}
		if !hasTool {
			args := jb(map[string]any{"path": "a.txt"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-p1", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "done"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	startTurn(t, s, "s-pers", "t-pers")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-pers", "turn": "t-pers",
		"type": "text-user", "content": "read file",
	}))
	collectTurn(t, s.bus, "t-pers", 15*time.Second)

	// 库：恰好一条 role=tool；content 解析为 {call,result}，call 段带工具名/参数。
	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	recs, _, err := prj.Table("messages").Query(data.Query{
		Where: data.Record{"turn_id": "t-pers"}, OrderBy: "created_at",
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, rec := range recs {
		if persist.Sval(rec["role"]) != "tool" {
			continue
		}
		n++
		if persist.Sval(rec["tool_call_id"]) != "tc-p1" || persist.Sval(rec["tool_call_status"]) != "completed" {
			t.Fatalf("tool 顶层字段错: %+v", rec)
		}
		tc, ok := persist.ParseToolContent(persist.Sval(rec["content"]))
		if !ok || tc.Call == nil || tc.Call.Name != "core_file_read" || tc.Result == nil ||
			!strings.Contains(tc.Result.Content, "file content") || tc.Result.Status != "completed" {
			t.Fatalf("tool content 结构错: %+v", rec)
		}
		if tc.Async != nil {
			t.Fatalf("同步工具不应有 async 段: %+v", tc.Async)
		}
	}
	if n != 1 {
		t.Fatalf("role=tool 落库数=%d want 1", n)
	}

	// LLM 末次请求：tool 结果只出现一次（不因落库 + 内存回喂而重复）。
	mu.Lock()
	last := reqs[len(reqs)-1]
	mu.Unlock()
	tn := 0
	for _, m := range last {
		if m.Role == "tool" && m.ToolCallID == "tc-p1" {
			tn++
		}
	}
	if tn != 1 {
		t.Fatalf("LLM 请求中 tool 结果出现 %d 次 want 1: %+v", tn, last)
	}
}

// seedGWToolMeta 往 gwClient 工具缓存注入一条带 _meta 的工具定义（I-60 落库断言用；
// 默认 fakeGateway 的 tools/list 为空）。
func seedGWToolMeta(s *Server, name string, meta map[string]any) {
	s.gc.mu.Lock()
	s.gc.tools = append(s.gc.tools, ToolDef{Name: name, Hot: true, Async: str(meta["async"]), Meta: meta})
	s.gc.mu.Unlock()
}

// TestTurnToolMetaPersisted：处理 tool-call 时取网关工具 _meta 子集，随 role=tool 消息落库
// （_meta.async），供前端本地判断超时裁决项（I-60）。
func TestTurnToolMetaPersisted(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	// 注入工具 _meta：async=manual + timeout/category（原样透传）。
	seedGWToolMeta(s, "core_file_read", map[string]any{
		"async": "manual", "timeout": 30.0, "category": "file",
	})
	startTurn(t, s, "s-meta", "t-meta")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-meta", "turn": "t-meta",
		"type": "text-user", "content": "sync tool please",
	}))
	collectTurn(t, s.bus, "t-meta", 15*time.Second)

	prj, err := data.PrjUsr("ins-test")
	if err != nil {
		t.Fatal(err)
	}
	recs, _, err := prj.Table("messages").Query(data.Query{
		Where: data.Record{"turn_id": "t-meta"}, OrderBy: "created_at",
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, rec := range recs {
		if persist.Sval(rec["role"]) != "tool" {
			continue
		}
		n++
		meta, _ := rec["_meta"].(map[string]any)
		if meta == nil || persist.Sval(meta["async"]) != "manual" ||
			persist.Sval(meta["category"]) != "file" {
			t.Fatalf("role=tool 消息 _meta 未落库/裁剪: %+v", rec["_meta"])
		}
		if f, _ := meta["timeout"].(float64); f != 30 {
			t.Fatalf("_meta.timeout 未原样透传: %+v", meta["timeout"])
		}
	}
	if n != 1 {
		t.Fatalf("role=tool 落库数=%d want 1", n)
	}
}

// subRaw 订阅封装（等价 bus.On(order=0)；主题相对，前缀总线注入）。
func subRaw(bus mq.Bus, subject string, h func(subject string, p []byte)) (mq.Sub, error) {
	return bus.On(subject, 0, func(_ context.Context, subj string, v *mq.Value) error {
		h(subj, v.Payload)
		return nil
	})
}

// snapshotOf 经 persist data-snapshot-get 读会话快照（map{history, snapshot_turn}；无快照 → nil）。
func snapshotOf(t *testing.T, s *Server, session string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := dataRequest(s.bus, "data-snapshot-get", map[string]any{
			"instance_id": "ins-test", "data": map[string]any{"session_id": session},
		})
		if err == nil {
			snap, ok := res["snapshot"].(map[string]any)
			if ok && snap != nil {
				return snap
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}

// TestExecControlGoesThroughSink：取消 / 转后台**经进程内 sink 直调执行池**，
// **不再经 MQ 方法面**（`mcp-tasks-status|result|list|cancel`、`mcp-tools-background` 已于
// 2026-09-18 移除）：
//
//	① 层取消回调（onTaskLayerCancelExec）→ sink.CancelExec(gw_task_id)；
//	② 前端 task-background{tool_call_id} → onTaskBackground → sink.DetachExec；
//	③ 全程零流量打到已移除的方法面主题（无消费者 = 静默丢失 → 必须为 0）。
func TestExecControlGoesThroughSink(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	resetExecControl()

	removed := map[string]bool{
		"mcp-tasks-status": true, "mcp-tasks-result": true, "mcp-tasks-list": true,
		"mcp-tasks-cancel": true, "mcp-tools-background": true,
	}
	var mu sync.Mutex
	var leaked []string
	_, _ = s.bus.On(">", 0, func(_ context.Context, subject string, _ *mq.Value) error {
		if removed[subject] {
			mu.Lock()
			leaked = append(leaked, subject)
			mu.Unlock()
		}
		return nil
	})

	// ① 层 → 执行侧取消（层持 exec 句柄回调）—— instance 归属随 exec 透传（缺口 2）
	s.onTaskLayerCancelExec("tk-node", tasklayer.Exec{GWTaskID: "tk-gw", InstanceID: "ins-gw"})
	if got := execCancelledRefs(); len(got) != 1 || got[0] != "tk-gw" {
		t.Fatalf("sink.CancelExec=%v want [tk-gw]", got)
	}
	if got := execCancelledInstances(); len(got) != 1 || got[0] != "ins-gw" {
		t.Fatalf("sink.CancelExec instance=%v want [ins-gw]", got)
	}

	// ② 前端转后台 → sink.DetachExec（无任务节点 → 以 tool_call_id 交执行池定位）
	if v := s.bus.Emit(context.Background(), "task-background",
		jb(map[string]any{"tool_call_id": "tc-gw", "instance_id": "ins-gw"})).Wait(); v.Err() != nil {
		t.Fatalf("onTaskBackground: %v", v.Err())
	}
	if got := execDetachedRefs(); len(got) != 1 || got[0] != "tc-gw" {
		t.Fatalf("sink.DetachExec=%v want [tc-gw]", got)
	}
	if got := execDetachedInstances(); len(got) != 1 || got[0] != "ins-gw" {
		t.Fatalf("sink.DetachExec instance=%v want [ins-gw]", got)
	}

	// ③ 已移除的方法面零流量
	mu.Lock()
	defer mu.Unlock()
	if len(leaked) != 0 {
		t.Fatalf("已移除的方法面不应再收到流量: %v", leaked)
	}
}

// TestExecSinkNilExplicitError：执行池未接入（sink 为 nil，如 DisableMCP）→ 控制路径返回
// **明确错误、不动作**（主路径不受影响）。
func TestExecSinkNilExplicitError(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	s.execSink = nil // 模拟未装配执行池

	err := s.bus.Emit(context.Background(), "task-background",
		jb(map[string]any{"tool_call_id": "tc-none", "instance_id": "ins-test"})).Wait().Err()
	if err == nil || !strings.Contains(err.Error(), "执行池不可用") {
		t.Fatalf("sink 为 nil 应回明确错误，got %v", err)
	}
	// 层取消回调：无 sink → 只告警、不 panic
	s.onTaskLayerCancelExec("tk-node", tasklayer.Exec{GWTaskID: "tk-gw"})
	// 适配器 nil 行为（未注入 gateway）
	adapter := &taskExecSink{}
	if _, err := adapter.CancelExec("ins-x", "tk-gw"); err == nil {
		t.Fatal("adapter.CancelExec 未注入 gateway 应报错")
	}
	if _, err := adapter.DetachExec("ins-x", "tk-gw"); err == nil {
		t.Fatal("adapter.DetachExec 未注入 gateway 应报错")
	}
}

// startedNodeNamed 取 tasks.started 中 tool+name 命中的节点（存在即返回 true）。
func startedNodeNamed(te *taskEvents, tool, name string) bool {
	te.mu.Lock()
	defer te.mu.Unlock()
	for _, st := range te.started {
		if st["tool"] == tool && st["name"] == name {
			return true
		}
	}
	return false
}

// TestTaskNodeTitleFromInjectedDisplayName（I-38，端到端）：主会话工具节点（网关暴露名带 self_ 前缀）的
// title/name 取「调用参数注入的展示名 tool_call_display_name」（委派就是工具调用）；无注入时回退
// 「工具契约名」（剥 self_ 前缀，非暴露名）。
func TestTaskNodeTitleFromInjectedDisplayName(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := lastText(body.Messages)
		switch {
		case strings.Contains(text, "inject please"), strings.Contains(text, "plain please"):
			args := map[string]any{"files": []any{}}
			if strings.Contains(text, "inject please") {
				args["tool_call_display_name"] = "读取配置"
			}
			// 工具名用网关暴露名（self_ 前缀）：display 取值须按契约名归一。
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-title", "type": "function",
					"function": map[string]any{"name": "self_core_file_read", "arguments": string(jb(args))},
				}}}, "tool_calls"),
			})
		default:
			llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, ""), sseChunk(map[string]any{}, "stop")})
		}
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	te := watchTasks(t, s.bus)

	// ① 有注入 → 节点 title/name = 注入值
	startTurn(t, s, "s-title-inj", "t-title-inj")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-title-inj", "turn": "t-title-inj",
		"type": "text-user", "content": "inject please",
	}))
	if lastComplete(collectTurn(t, s.bus, "t-title-inj", 10*time.Second)) == nil {
		t.Fatal("注入轮次未完成")
	}
	te.wait(t, "注入展示名节点", func() bool { return startedNodeNamed(te, "self_core_file_read", "读取配置") })

	// ② 无注入（新会话）→ 回退工具契约名（剥 self_）
	startTurn(t, s, "s-title-plain", "t-title-plain")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-title-plain", "turn": "t-title-plain",
		"type": "text-user", "content": "plain please",
	}))
	if lastComplete(collectTurn(t, s.bus, "t-title-plain", 10*time.Second)) == nil {
		t.Fatal("无注入轮次未完成")
	}
	te.wait(t, "回退契约名节点", func() bool { return startedNodeNamed(te, "self_core_file_read", "core_file_read") })
}

// TestLLMRuntimeConfigWiring：usr 超时/重试配置 → newTurnCtx 实际生效（T-27 接线白盒）。
// 只断言"配置值 → 构造出的值"，不真实等待超时/重试。
func TestLLMRuntimeConfigWiring(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	res := dataCall(t, s, "data-user-config-save", map[string]any{
		"instance_id": "ins-test",
		"data": map[string]any{
			"responseTimeout": 7, "streamTimeout": 9, "retryCount": 4,
		},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("save user-config failed: %+v", res)
	}

	startTurn(t, s, "s-rt", "t-rt")
	s.mu.Lock()
	tc := s.turns[instKey("ins-test", "t-rt")]
	s.mu.Unlock()
	if tc == nil {
		t.Fatal("turnCtx not created")
	}
	if tc.responseTimeout != 7*time.Second || tc.streamTimeout != 9*time.Second {
		t.Fatalf("超时未取自配置: resp=%v stream=%v，want 7s/9s", tc.responseTimeout, tc.streamTimeout)
	}
	if tc.retryCount != 4 {
		t.Fatalf("重试次数未取自配置: count=%d，want 4", tc.retryCount)
	}
}

// TestLLMRuntimeConfigFallback：超时两项配置为非正值（0）→ 回落旧硬编码常量；retryCount 例外
// （P0-A 存在性判定）：显式 0 = **不重试**，不再回落默认 2。
func TestLLMRuntimeConfigFallback(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	dataCall(t, s, "data-user-config-save", map[string]any{
		"instance_id": "ins-test",
		"data": map[string]any{
			"responseTimeout": 0, "streamTimeout": 0, "retryCount": 0,
		},
	})

	respTO, streamTO, rc := s.loadLLMRuntimeConfig("ins-test")
	if respTO != ResponseTimeout || streamTO != StreamTimeout {
		t.Fatalf("0 值应回落常量: resp=%v stream=%v，want %v/%v",
			respTO, streamTO, ResponseTimeout, StreamTimeout)
	}
	if rc != 0 {
		t.Fatalf("retryCount=0 应=不重试（显式 0 生效），got %d want 0", rc)
	}
}

// llmCallCounter 并发安全的 LLM 请求计数（重试语义断言用）。
type llmCallCounter struct {
	mu sync.Mutex
	n  int
}

func (c *llmCallCounter) add() { c.mu.Lock(); c.n++; c.mu.Unlock() }

func (c *llmCallCounter) load() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// failingLLMServer 恒返回 5xx（retryable，见 LLMError.Retryable）并计数每次请求；
// probe（TCP 预检）由 httptest 监听天然成功 → 重试分支可达。
func failingLLMServer(c *llmCallCounter) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // 连通性探测
			w.WriteHeader(http.StatusOK)
			return
		}
		c.add()
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
}

// TestTurnRetryCountSemantics：retryCount 存在性判定（P0-A）端到端——以**实际 HTTP 请求数**为
// 原始证据（每次尝试 = 1 次请求）：显式 0 → 1 次（不重试）；显式 1 → 2 次；缺省 → 3 次（默认 2 次重试）。
// 退避经 router.RetryWait（1s/2s…）；不再有 retryDelay 配置。
func TestTurnRetryCountSemantics(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     map[string]any
		wantCall int
	}{
		{"显式0=不重试", map[string]any{"retryCount": 0}, 1},
		{"显式1=重试1次", map[string]any{"retryCount": 1}, 2},
		{"缺省=默认2次", map[string]any{}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &llmCallCounter{}
			llm := failingLLMServer(calls)
			defer llm.Close()
			s := newTestServer(t, llm)
			registerTestInstance(t, s)
			if res := dataCall(t, s, "data-user-config-save", map[string]any{
				"instance_id": "ins-test", "data": tc.data,
			}); res["ok"] != true {
				t.Fatalf("save user-config failed: %+v", res)
			}
			startTurn(t, s, "s-rc0", "t-rc0")
			s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
				"instance_id": "ins-test", "session": "s-rc0", "turn": "t-rc0",
				"type": "text-user", "content": "always fail please",
			}))
			evs := collectTurn(t, s.bus, "t-rc0", 15*time.Second)
			c := lastComplete(evs)
			if c == nil || c["status"] != "error" {
				t.Fatalf("want error complete: %+v", evs)
			}
			if n := calls.load(); n != tc.wantCall {
				t.Fatalf("LLM 请求数=%d want %d（retryCount 语义）", n, tc.wantCall)
			}
		})
	}
}

// TestRetryWaitUsesRouter：llm 可视重试的退避值经 `router.RetryWait` 计算（2026-10-05 定案，
// 不再自持 retryDelay）——断言指数退避（1s/2s/4s）+ `Retry-After` 优先，且源 router 错误的
// `Retry-After` 经 `mapRouterError` 透传（生产路径）。
func TestRetryWaitUsesRouter(t *testing.T) {
	// 无 Retry-After → 指数退避 1s·2s·4s（口径与 router.RetryWait 白盒一致）。
	rate := &LLMError{Kind: ErrRateLimit, Retryable: true}
	if got := retryWait(rate, 1); got != time.Second {
		t.Fatalf("attempt=1 → %v want 1s", got)
	}
	if got := retryWait(rate, 2); got != 2*time.Second {
		t.Fatalf("attempt=2 → %v want 2s", got)
	}
	if got := retryWait(rate, 3); got != 4*time.Second {
		t.Fatalf("attempt=3 → %v want 4s", got)
	}
	// Retry-After 优先于指数退避。
	after := &LLMError{Kind: ErrRateLimit, Retryable: true, RetryAfter: 3 * time.Second}
	if got := retryWait(after, 5); got != 3*time.Second {
		t.Fatalf("Retry-After 优先 → %v want 3s", got)
	}
	// 不可重试 / 非 *LLMError → 0（调用方已先行过滤）。
	if got := retryWait(&LLMError{Kind: ErrAuth}, 1); got != 0 {
		t.Fatalf("不可重试 → %v want 0", got)
	}
	if got := retryWait(context.Canceled, 1); got != 0 {
		t.Fatalf("非 *LLMError → %v want 0", got)
	}
	// 源 router 错误的 Retry-After 经 mapRouterError 透传（生产路径）。
	re := &router.Error{Kind: router.ErrorRateLimit, Retryable: true, RetryAfter: 4 * time.Second}
	mapped := mapRouterError(re)
	var le *LLMError
	if !errors.As(mapped, &le) || !le.Retryable || le.RetryAfter != 4*time.Second {
		t.Fatalf("mapRouterError 未透传 Retry-After: %+v", mapped)
	}
	if got := retryWait(mapped, 9); got != 4*time.Second {
		t.Fatalf("透传后 Retry-After 优先 → %v want 4s", got)
	}
}

// ── 全局层（25 §3 / 用户口径 2026-09-26）────────────────────────────────

// TestGlobalLayerPromptTemplate：全局层文案严格为模板式
// `你是 {agentName}，一个全能智能体。你运行在 {env} 中。`——
// ① agentName 取当前 agent 裸名；② 通用模式 = 肥猫；③ env = 运行形态（Form，61 §4.6）+ 平台（runtime.GOOS）。
func TestGlobalLayerPromptTemplate(t *testing.T) {
	s := &Server{} // Form 空 → desktop（桌面单体默认口径）
	for _, tc := range []struct {
		name, agent, form, want string
	}{
		{"通用模式（肥猫）", "肥猫", "", "你是 肥猫，一个全能智能体。你运行在 ChonkPilot 桌面客户端（" + platformName() + "） 中。"},
		{"有场景（当前 agent 裸名）", "scout", "", "你是 scout，一个全能智能体。你运行在 ChonkPilot 桌面客户端（" + platformName() + "） 中。"},
		{"GUI 形态", "main", FormGui, "你是 main，一个全能智能体。你运行在 ChonkPilot GUI 客户端（" + platformName() + "） 中。"},
		{"浏览器形态", "main", FormBrowser, "你是 main，一个全能智能体。你运行在 ChonkPilot 浏览器端（" + platformName() + "） 中。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.opts.Form = tc.form
			if got := s.globalLayerPrompt(tc.agent); got != tc.want {
				t.Fatalf("全局层 = %q，want %q", got, tc.want)
			}
		})
	}
}

// TestCurrentAgentName：当前 agent 裸名取值——instance 级（当前场景内 agent）优先，
// 未命中取 global 级（app 级场景内置 agent）名，皆空 → ""（调用方回落 defaultAgentName）。
func TestCurrentAgentName(t *testing.T) {
	if got := currentAgentName(nil, AgentDef{}); got != "" {
		t.Fatalf("无 agent → %q，want 空", got)
	}
	if got := currentAgentName(&scenarioAgent{Name: "  scout "}, AgentDef{Name: "内置"}); got != "scout" {
		t.Fatalf("instance 级应优先且去空白 → %q，want scout", got)
	}
	if got := currentAgentName(nil, AgentDef{Name: " 内置 "}); got != "内置" {
		t.Fatalf("global 级 → %q，want 内置", got)
	}
	if got := currentAgentName(&scenarioAgent{Name: "  "}, AgentDef{Name: "内置"}); got != "内置" {
		t.Fatalf("instance 级名为空白应回落 global 级 → %q，want 内置", got)
	}
}

// TestLLMRuntimeConfigSystemDefaults：完全未配置（用户未写任何超时/重试项）→ 有效值
// = 系统默认 120s/60s/2（与 persist userConfigSystemDefaults 及旧硬编码一致，防默认放宽回归）。
func TestLLMRuntimeConfigSystemDefaults(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	respTO, streamTO, rc := s.loadLLMRuntimeConfig("ins-test")
	if respTO != 120*time.Second || streamTO != 60*time.Second || rc != 2 {
		t.Fatalf("未配置默认 = %v/%v/%d，want 120s/60s/2", respTO, streamTO, rc)
	}
}

// TestSystemDefaultParamsMirror：「参数配置页」的「系统默认」栏是**前端镜像**（P0-D）——
// 本用例断言其后端权威源的真实取值（后端常量被静默改动 → 本用例先红，提醒同步前端
// SettingsParamsPage.vue 的 SYSTEM_DEFAULTS）：
//
//	① 用户级三项 = persist userConfigSystemDefaults（经既有 data-user-config-load 回读）
//	② 项目级三项 = mcpms.DefaultConfig()（300 / 16 / 12 项跳过目录）
//	③ keep_full_max_turns = defaultKeepFullTurns（10）；compress_token_threshold = 压缩插件
//	   DefaultOptions().TokenMax（20000）。**③ 已不在「参数配置页」镜像**（该项唯一编辑入口
//	   收敛至「上下文管理」页 ContextConfig.vue，去重 2026-09-15）；本断言保留为后端常量守护。
func TestSystemDefaultParamsMirror(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	// ① 用户级三项
	res := dataCall(t, s, "data-user-config-load", map[string]any{"instance_id": "ins-test"})
	d, _ := dataResult(t, res)["data"].(map[string]any)
	if d == nil {
		t.Fatalf("user-config-load 无 data: %+v", res)
	}
	for key, want := range map[string]int{
		"responseTimeout": 120, "streamTimeout": 60, "retryCount": 2,
	} {
		if got, ok := configInt(d, key); !ok || got != want {
			t.Fatalf("系统默认 %s=%v want %d（前端 SYSTEM_DEFAULTS 镜像须同步）", key, d[key], want)
		}
	}

	// ② 项目级三项
	def := mcpms.DefaultConfig()
	if def.TimeoutSec != 300 || def.MaxConcurrency != 16 {
		t.Fatalf("服务参数默认 timeout=%d concurrency=%d want 300/16", def.TimeoutSec, def.MaxConcurrency)
	}
	wantSkip := ".git,.svn,node_modules,.trae,.chonkpilot,__pycache__,.venv,venv,build,dist,.next,.nuxt"
	if got := strings.Join(def.Defaults.SkipDirs, ","); got != wantSkip {
		t.Fatalf("skip_dirs 默认=%s want %s", got, wantSkip)
	}

	// ③ 维持轮数默认（压缩阈值 20000 的权威在 compress 包，见其镜像用例）
	if defaultKeepFullTurns != 10 {
		t.Fatalf("keep_full_max_turns 默认=%d want 10", defaultKeepFullTurns)
	}
}

// TestFinishTerminalOnce（E2-2）：正常完成与取消并发到达同一轮次 → 终态（llm-complete 广播 +
// turn 行终态落库）**恰好一次**；已终态后取消命中不再重复广播。
func TestFinishTerminalOnce(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)

	tc := s.recoverTurnCtx("ins-t", "s-t", "t-t")
	t.Cleanup(tc.Close)

	var mu sync.Mutex
	completes := 0
	statuses := map[string]int{}
	_, _ = s.bus.On("session-complete", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil || m["turn"] != "t-t" {
			return nil
		}
		st, _ := m["status"].(string)
		mu.Lock()
		completes++
		statuses[st]++
		mu.Unlock()
		return nil
	})

	// 并发两个终态：正常完成 + 取消（模拟 race window）。
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.complete(tc, "interrupted", "interrupted", "") }()
	go func() { defer wg.Done(); s.complete(tc, "complete", "stop", "hello") }()
	wg.Wait()

	mu.Lock()
	n := completes
	mu.Unlock()
	if n != 1 {
		t.Fatalf("终态广播应恰好一次：got=%d statuses=%v", n, statuses)
	}

	// 取消命中已终态轮次（仍在 s.turns）→ 跳过，不重复落库/广播。
	if err := s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-t", "session": "s-t", "turn": "t-t",
	})).Wait().Err(); err != nil {
		t.Fatalf("session-cancel: %v", err)
	}
	mu.Lock()
	n = completes
	mu.Unlock()
	if n != 1 {
		t.Fatalf("取消命中已终态轮次不应重复广播：got=%d", n)
	}
}
