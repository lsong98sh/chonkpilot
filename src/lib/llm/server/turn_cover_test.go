// 白盒覆盖补充（2026-09-08）：重点 1/2/6 缺口——
// 一轮含 reasoning+tool-call+text；LLM retryable 自动重试；session-cancel 中断主会话。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestTurnReasoningSyncToolText：一轮完整会话 = reasoning 增量 → async tool-call →
// 工具完成回填 → 次轮（含 tool 结果）reasoning + 最终 text → complete。
func TestTurnReasoningSyncToolText(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = jsonDecode(r, &body)
		hasToolResult := false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasToolResult = true
			}
		}
		if !hasToolResult {
			// 首轮：reasoning + async 工具请求
			args := jb(map[string]any{"_async": true, "path": "a.txt"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"reasoning_content": "think about file"}, ""),
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-r1", "type": "function",
					"function": map[string]any{"name": "core_file_read", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		// 次轮：reasoning + 最终文本
		llmSSE(w, []string{
			sseChunk(map[string]any{"reasoning_content": "final think"}, ""),
			sseChunk(map[string]any{"content": "final-answer"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()

	s := newTestServer(t, llm)
	startTurn(t, s, "s-cov1", "t-cov1")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cov1", "turn": "t-cov1",
		"type": "text-user", "content": "reason round",
	}))
	evs := collectTurn(t, s.bus, "t-cov1", 15*time.Second)

	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete: %+v", evs)
	}
	if !strings.Contains(str(c["text"]), "final-answer") {
		t.Fatalf("final text miss: %q", c["text"])
	}
	reason := 0
	toolCall := 0
	for _, ev := range evs {
		switch ev["type"] {
		case NotifyTypeReason:
			reason++
		case NotifyTypeToolCall:
			toolCall++
		}
	}
	if reason < 2 {
		t.Fatalf("reasoning deltas <2 (got %d): %+v", reason, evs)
	}
	if toolCall < 1 {
		t.Fatalf("no tool-call notify: %+v", evs)
	}
}

// TestTurnLLMRetryableAutoRetry：retryable 请求错误（5xx）自动重试后 complete（请求计数=2）。
func TestTurnLLMRetryableAutoRetry(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // probe（连通性探测）一律 200
			w.WriteHeader(http.StatusOK)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "after-retry"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()

	s := newTestServer(t, llm)
	startTurn(t, s, "s-cov2", "t-cov2")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cov2", "turn": "t-cov2",
		"type": "text-user", "content": "retry me",
	}))
	evs := collectTurn(t, s.bus, "t-cov2", 15*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "complete" {
		t.Fatalf("no complete after retry: %+v", evs)
	}
	if !strings.Contains(str(c["text"]), "after-retry") {
		t.Fatalf("text miss: %q", c["text"])
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("llm calls = %d, want 2（retryable 自动重试）", n)
	}
}

// TestTurnCancelInterrupted：运行中 cancel（session-cancel）→ 终态 interrupted（非 error）。
func TestTurnCancelInterrupted(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		// 挂起连接（不发任何事件）：turn 保持 running，直到 cancel 断连 → Chat EOF。
		// 兜底超时退出，避免测试收尾时 handler 悬挂。
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer llm.Close()

	s := newTestServer(t, llm)
	startTurn(t, s, "s-cov3", "t-cov3")
	log := watchSessionEvents(t, s.bus, "t-cov3")
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cov3", "turn": "t-cov3",
		"type": "text-user", "content": "slow stream",
	}))
	time.Sleep(300 * time.Millisecond) // turn 已进入挂起的 LLM 请求（running）
	s.bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-test", "session": "s-cov3", "turn": "t-cov3",
	}))
	evs := waitComplete(t, log, 5*time.Second)
	c := lastComplete(evs)
	if c == nil || c["status"] != "interrupted" {
		t.Fatalf("want interrupted complete, got: %+v", evs)
	}
	// busy 释放：同 session 可再次 start
	startTurn(t, s, "s-cov3", "t-cov3b")
}

// eventLog 并发安全的事件收集（session-* 域，按 turn 过滤）。
type eventLog struct {
	mu   sync.Mutex
	evs  []map[string]any
	done chan struct{} // 收到 session-complete 后关闭
}

// watchSessionEvents 订阅全通配并收集 session-*（按 turn 过滤）直到 complete。
func watchSessionEvents(t *testing.T, bus mq.Bus, turn string) *eventLog {
	t.Helper()
	log := &eventLog{done: make(chan struct{})}
	_, err := bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		if !strings.HasPrefix(subject, "session-") {
			return nil
		}
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil || m["turn"] != turn {
			return nil
		}
		log.mu.Lock()
		log.evs = append(log.evs, m)
		if subject == "session-complete" {
			select {
			case <-log.done:
			default:
				close(log.done)
			}
		}
		log.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return log
}

// snapshot 返回已收集事件副本。
func (l *eventLog) snapshot() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]map[string]any, len(l.evs))
	copy(out, l.evs)
	return out
}

// waitNotify 阻塞直到事件流出现指定 type 的 receive（或超时）。
func waitNotify(t *testing.T, log *eventLog, typ string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, ev := range log.snapshot() {
			if ev["type"] == typ {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s notify within %v (events=%+v)", typ, timeout, log.snapshot())
}

// waitComplete 等待 complete 事件，返回事件快照（或超时失败）。
func waitComplete(t *testing.T, log *eventLog, timeout time.Duration) []map[string]any {
	t.Helper()
	select {
	case <-log.done:
		return log.snapshot()
	case <-time.After(timeout):
		t.Fatalf("no complete within %v (events=%+v)", timeout, log.snapshot())
	}
	return nil
}

// jsonDecode 从请求读 JSON。
func jsonDecode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// saveRetryConfig 写 usr 配置 retryCount（次数）——退避经 router.RetryWait（1s/2s…），与
// TestTurnRetryCountSemantics 同一路径（P0-A 存在性判定：显式值生效）。
func saveRetryConfig(t *testing.T, s *Server, count int) {
	t.Helper()
	if res := dataCall(t, s, "data-user-config-save", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"retryCount": count},
	}); res["ok"] != true {
		t.Fatalf("save user-config failed: %+v", res)
	}
}

// streamErrCode 判断 llm-complete 错误码是否属"LLM 请求/流失败"两类
// （HTTP 层失败按上报路径收敛为 LLM_REQUEST_FAILED 或 LLM_STREAM_ERROR，两者同为
// 错误终态、retryable 取该错误 *LLMError.Retryable，故断言接受二者）。
func streamErrCode(code string) bool {
	return code == "LLM_REQUEST_FAILED" || code == "LLM_STREAM_ERROR"
}

// TestTurnCompleteRetryableField（S21）：llm-complete 的**可选** retryable 字段取值——
// ① 5xx 重试耗尽（retryable 错误，retryCount=1）→ retryable=true（前端可自动续写）；
// ② 400 协议错误（不可重试，不重试）→ retryable=false（前端错误气泡 + 手动继续）；
// ③ 正常完成 → 不带该字段（旧订阅方按缺省处理，向后兼容）。
func TestTurnCompleteRetryableField(t *testing.T) {
	t.Run("5xx重试耗尽→retryable=true", func(t *testing.T) {
		calls := &llmCallCounter{}
		llm := failingLLMServer(calls)
		defer llm.Close()
		s := newTestServer(t, llm)
		registerTestInstance(t, s)
		saveRetryConfig(t, s, 1) // 重试 1 次 → 2 次请求（真实"耗尽"）
		startTurn(t, s, "s-rt1", "t-rt1")
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-rt1", "turn": "t-rt1",
			"type": "text-user", "content": "always fail please",
		}))
		evs := collectTurn(t, s.bus, "t-rt1", 15*time.Second)
		c := lastComplete(evs)
		// code：HTTP 层失败经流错误路径收敛为 LLM_STREAM_ERROR（另一路径为 LLM_REQUEST_FAILED）
		if c == nil || c["status"] != "error" || !streamErrCode(str(c["code"])) {
			t.Fatalf("want error/LLM_*_ERROR: %+v", evs)
		}
		if c["retryable"] != true {
			t.Fatalf("5xx 耗尽应 retryable=true，got %v（payload=%+v）", c["retryable"], c)
		}
		if n := calls.load(); n != 2 {
			t.Fatalf("LLM 请求数=%d want 2（retryCount=1 重试耗尽）", n)
		}
	})

	t.Run("400协议错误→retryable=false", func(t *testing.T) {
		calls := &llmCallCounter{}
		llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet { // 连通性探测
				w.WriteHeader(http.StatusOK)
				return
			}
			calls.add()
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"bad request"}`)
		}))
		defer llm.Close()
		s := newTestServer(t, llm)
		registerTestInstance(t, s)
		saveRetryConfig(t, s, 2)
		startTurn(t, s, "s-rt2", "t-rt2")
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-rt2", "turn": "t-rt2",
			"type": "text-user", "content": "protocol error please",
		}))
		evs := collectTurn(t, s.bus, "t-rt2", 15*time.Second)
		c := lastComplete(evs)
		if c == nil || c["status"] != "error" || !streamErrCode(str(c["code"])) {
			t.Fatalf("want error/LLM_*_ERROR: %+v", evs)
		}
		if c["retryable"] != false {
			t.Fatalf("协议错误（400）应 retryable=false，got %v（payload=%+v）", c["retryable"], c)
		}
		if n := calls.load(); n != 1 {
			t.Fatalf("LLM 请求数=%d want 1（不可重试，不重发）", n)
		}
	})

	t.Run("正常完成→不带retryable字段", func(t *testing.T) {
		llm := mockLLMServer()
		defer llm.Close()
		s := newTestServer(t, llm)
		startTurn(t, s, "s-rt3", "t-rt3")
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-rt3", "turn": "t-rt3",
			"type": "text-user", "content": "hello",
		}))
		evs := collectTurn(t, s.bus, "t-rt3", 15*time.Second)
		c := lastComplete(evs)
		if c == nil || c["status"] != "complete" {
			t.Fatalf("want complete: %+v", evs)
		}
		if _, ok := c["retryable"]; ok {
			t.Fatalf("正常完成不应携带 retryable: %+v", c)
		}
	})
}
