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

// retryEvents 取可见重试中间态（session-retry：有 retry_attempt、无 status）。
func retryEvents(evs []map[string]any) []map[string]any {
	var out []map[string]any
	for _, ev := range evs {
		if _, ok := ev["status"]; ok {
			continue
		}
		if _, ok := ev["retry_attempt"]; ok {
			out = append(out, ev)
		}
	}
	return out
}

// errorTerminalCount 统计错误终态条数（deliver 一次终态 = 唯一一次错误）。
func errorTerminalCount(evs []map[string]any) int {
	n := 0
	for _, ev := range evs {
		if ev["status"] == "error" {
			n++
		}
	}
	return n
}

// numEq 判断 JSON 解包后的数值键是否等于 want（collectTurn 经 json.Unmarshal → float64）。
func numEq(v any, want int) bool {
	f, ok := v.(float64)
	return ok && int(f) == want
}

// TestTurnVisibleRetryEvents（RETRY-1，2026-10-17）：可见重试中间态与真实计数。
// ① 每次失败发一条 session-retry（计数递增），终态错误只发一次且携带真实计数；
// ② 重试后成功 → 只有中间态 + complete，无错误终态；
// ③ retryCount=0 → 无中间态，直接终态（attempt=0）；
// ④ 等待值 = router.RetryWait（Retry-After 优先）。
func TestTurnVisibleRetryEvents(t *testing.T) {
	t.Run("每次失败发中间态且计数递增/终态仅一次", func(t *testing.T) {
		calls := &llmCallCounter{}
		llm := failingLLMServer(calls)
		defer llm.Close()
		s := newTestServer(t, llm)
		registerTestInstance(t, s)
		saveRetryConfig(t, s, 2)
		startTurn(t, s, "s-vr1", "t-vr1")
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-vr1", "turn": "t-vr1",
			"type": "text-user", "content": "always fail please",
		}))
		evs := collectTurn(t, s.bus, "t-vr1", 20*time.Second)
		retries := retryEvents(evs)
		if len(retries) != 2 {
			t.Fatalf("session-retry 次数=%d want 2（retryCount=2）：%+v", len(retries), retries)
		}
		for i, r := range retries {
			if !numEq(r["retry_attempt"], i+1) {
				t.Fatalf("第 %d 条 retry_attempt=%v want %d", i+1, r["retry_attempt"], i+1)
			}
			if !numEq(r["retry_count"], 2) {
				t.Fatalf("retry_count=%v want 2", r["retry_count"])
			}
			if ms, _ := r["retry_wait_ms"].(float64); ms <= 0 {
				t.Fatalf("中间态 retry_wait_ms=%v want >0", r["retry_wait_ms"])
			}
			if r["retryable"] != true {
				t.Fatalf("中间态应 retryable=true: %+v", r)
			}
		}
		if n := errorTerminalCount(evs); n != 1 {
			t.Fatalf("错误终态条数=%d want 1：%+v", n, evs)
		}
		c := lastComplete(evs)
		if c == nil || c["status"] != "error" || c["retryable"] != true {
			t.Fatalf("终态应为 error/retryable=true：%+v", c)
		}
		if !numEq(c["retry_attempt"], 2) || !numEq(c["retry_count"], 2) || !numEq(c["retry_wait_ms"], 0) {
			t.Fatalf("终态计数=%v/%v/%v want 2/2/0", c["retry_attempt"], c["retry_count"], c["retry_wait_ms"])
		}
		if n := calls.load(); n != 3 {
			t.Fatalf("LLM 请求数=%d want 3（1 次 + 2 次重试）", n)
		}
	})

	t.Run("重试后成功→仅中间态+complete无错误", func(t *testing.T) {
		var calls atomic.Int32
		llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				return
			}
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":"boom"}`)
				return
			}
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": "recovered"}, ""),
				sseChunk(map[string]any{}, "stop"),
			})
		}))
		defer llm.Close()
		s := newTestServer(t, llm)
		registerTestInstance(t, s)
		saveRetryConfig(t, s, 2)
		startTurn(t, s, "s-vr2", "t-vr2")
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-vr2", "turn": "t-vr2",
			"type": "text-user", "content": "fail once",
		}))
		evs := collectTurn(t, s.bus, "t-vr2", 20*time.Second)
		if n := len(retryEvents(evs)); n != 1 {
			t.Fatalf("session-retry 次数=%d want 1：%+v", n, retryEvents(evs))
		}
		if n := errorTerminalCount(evs); n != 0 {
			t.Fatalf("成功后不得出现错误终态（got %d）：%+v", n, evs)
		}
		c := lastComplete(evs)
		if c == nil || c["status"] != "complete" || !strings.Contains(str(c["text"]), "recovered") {
			t.Fatalf("want complete(recovered)：%+v", c)
		}
	})

	t.Run("retryCount=0→无中间态直接终态", func(t *testing.T) {
		calls := &llmCallCounter{}
		llm := failingLLMServer(calls)
		defer llm.Close()
		s := newTestServer(t, llm)
		registerTestInstance(t, s)
		saveRetryConfig(t, s, 0)
		startTurn(t, s, "s-vr3", "t-vr3")
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-vr3", "turn": "t-vr3",
			"type": "text-user", "content": "no retry please",
		}))
		evs := collectTurn(t, s.bus, "t-vr3", 20*time.Second)
		if n := len(retryEvents(evs)); n != 0 {
			t.Fatalf("retryCount=0 不应有 session-retry（got %d）", n)
		}
		c := lastComplete(evs)
		if c == nil || c["status"] != "error" {
			t.Fatalf("want error terminal：%+v", c)
		}
		if !numEq(c["retry_attempt"], 0) || !numEq(c["retry_count"], 0) || !numEq(c["retry_wait_ms"], 0) {
			t.Fatalf("retryCount=0 终态计数=%v/%v/%v want 0/0/0",
				c["retry_attempt"], c["retry_count"], c["retry_wait_ms"])
		}
		if n := calls.load(); n != 1 {
			t.Fatalf("LLM 请求数=%d want 1（不重试）", n)
		}
	})

	t.Run("等待值=Retry-After", func(t *testing.T) {
		var calls atomic.Int32
		llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				return
			}
			calls.Add(1)
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"rate limited"}`)
		}))
		defer llm.Close()
		s := newTestServer(t, llm)
		registerTestInstance(t, s)
		// 预算需覆盖 Retry-After（2s），否则预算门槛会直接终态（RETRY-2 语义）。
		if res := dataCall(t, s, "data-user-config-save", map[string]any{
			"instance_id": "ins-test", "data": map[string]any{"retryCount": 1, "retryBudgetSec": 5},
		}); res["ok"] != true {
			t.Fatalf("save user-config failed: %+v", res)
		}
		startTurn(t, s, "s-vr4", "t-vr4")
		s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
			"instance_id": "ins-test", "session": "s-vr4", "turn": "t-vr4",
			"type": "text-user", "content": "rate limited please",
		}))
		evs := collectTurn(t, s.bus, "t-vr4", 20*time.Second)
		retries := retryEvents(evs)
		if len(retries) != 1 {
			t.Fatalf("session-retry 次数=%d want 1：%+v", len(retries), retries)
		}
		if ms, _ := retries[0]["retry_wait_ms"].(float64); ms != 2000 {
			t.Fatalf("retry_wait_ms=%v want 2000（Retry-After: 2）", retries[0]["retry_wait_ms"])
		}
		c := lastComplete(evs)
		if c == nil || c["status"] != "error" || !numEq(c["retry_attempt"], 1) || !numEq(c["retry_count"], 1) {
			t.Fatalf("终态应为 error 且计数 1/1：%+v", c)
		}
	})
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

// saveRetryConfig 写 usr 配置 retryCount（次数）+ retryBudgetSec=1（小预算，测试快速）——
// 退避由 router 按预算归一化（RETRY-2），与 TestTurnRetryCountSemantics 同一路径。
func saveRetryConfig(t *testing.T, s *Server, count int) {
	t.Helper()
	if res := dataCall(t, s, "data-user-config-save", map[string]any{
		"instance_id": "ins-test", "data": map[string]any{"retryCount": count, "retryBudgetSec": 1},
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
