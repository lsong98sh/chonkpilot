// Package llmtest — chonkpilot-llm 黑盒回归测试（工程目录 chonkpilot-test/chonkpilot-llm/unittest）。
//
// 只经公开总线与公开相对主题（chonk. 前缀由总线注入）驱动 server，对 payload/输出字段断言：
//   - 一轮纯文本会话：session-start ack → session-receive(type=text) 增量 → session-complete(status/text)
//   - 无上下文单轮 LLM 方法面：llm-simple 请求 {prompt} → {text}
//   - 运行中取消：session-cancel → session-complete{status:interrupted}
//   - 域工具源目录：mcp-tools-list 含 self_ask_user 等（name/description/inputSchema/scope）、
//     mcp-prompts-list **不含** agent（25 §5/T2：agent 已撤出资产面）
package llmtest

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
	"github.com/chonkpilot/chonkpilot-lib/mq"

	llmserver "github.com/chonkpilot/chonkpilot-llm/server"
)

// writeSSE 以 OpenAI 流式 chunk 写响应（delta/finish_reason），以 [DONE] 结束。
func writeSSE(w http.ResponseWriter, chunks []map[string]any, finish string) {
	fl, _ := w.(http.Flusher)
	for _, d := range chunks {
		ev, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": d, "finish_reason": ""}}})
		fmt.Fprintf(w, "data: %s\n\n", ev)
		if fl != nil {
			fl.Flush()
		}
	}
	if finish != "" {
		ev, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", ev)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

// textLLMServer 固定文本 responder（忽略入参，直接回 text）。
func textLLMServer(text string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeSSE(w, []map[string]any{{"content": text}}, "stop")
	}))
}

// jb 序列化 payload（Publish 需要 []byte）。
func jb(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// newBus 建带前缀总线。
func newBus(t *testing.T) mq.Bus {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	t.Cleanup(data.Reset) // 关闭数据层库连接缓存（TempDir 清理前，对齐白盒测试）
	return bus
}

// registerInstance 注册测试实例（work_dir=临时目录）。
func registerInstance(t *testing.T, bus mq.Bus, instanceID string) {
	t.Helper()
	t.Cleanup(data.Reset) // 晚于本函数 t.TempDir 注册 → LIFO 先关数据层连接，再删临时目录
	bus.Emit(context.Background(), "instance-register", jb(map[string]any{
		"instance_id": instanceID, "client_type": "unittest", "work_dir": t.TempDir(),
	}))
}

// startLLM 建 server（文本 LLM；DisableMCP 由调用方决定）。
func startLLM(t *testing.T, bus mq.Bus, llmBase string, disableMCP bool) *llmserver.Server {
	t.Helper()
	s := llmserver.New(bus, llmserver.Options{
		LLMBase: llmBase, LLMModel: "mock",
		DisableMCP:    disableMCP,
		MCPServerRoot: t.TempDir(),
		UsrPath:       t.TempDir() + "/usr.db",
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

// startTurn 发 session-start 并等 ack。
func startTurn(t *testing.T, bus mq.Bus, session, turn, instanceID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := bus.Emit(ctx, "session-start", map[string]any{
		"instance_id": instanceID, "session": session, "turn": turn,
	}).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("session-start: %v", err)
	}
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("session-start rejected: %+v", res)
	}
}

// watcher 收集 session-* 事件（按 turn 过滤）。
type watcher struct {
	mu   sync.Mutex
	evs  []map[string]any
	done chan struct{}
}

// watch 订阅 session-* 事件并收集（complete 到达关闭 done）。
func watch(t *testing.T, bus mq.Bus, turn string) *watcher {
	t.Helper()
	w := &watcher{done: make(chan struct{})}
	_, err := bus.On(">", 0, func(_ context.Context, subject string, v *mq.Value) error {
		if !strings.HasPrefix(subject, "session-") {
			return nil
		}
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil || m["turn"] != turn {
			return nil
		}
		w.mu.Lock()
		w.evs = append(w.evs, m)
		if subject == "session-complete" {
			select {
			case <-w.done:
			default:
				close(w.done)
			}
		}
		w.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return w
}

func (w *watcher) snapshot() []map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]map[string]any, len(w.evs))
	copy(out, w.evs)
	return out
}

// waitComplete 等 complete 事件并返回快照（终态 status 取自最后一条 session-complete）。
func waitComplete(t *testing.T, w *watcher, timeout time.Duration) ([]map[string]any, string) {
	t.Helper()
	select {
	case <-w.done:
	case <-time.After(timeout):
		t.Fatalf("no complete within %v (events=%+v)", timeout, w.snapshot())
	}
	var status string
	for _, ev := range w.snapshot() {
		if st, ok := ev["status"].(string); ok {
			status = st
		}
	}
	return w.snapshot(), status
}

// str 取 map 字符串字段。
func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// ── 用例 ──────────────────────────────────────────────

// TestSessionPlainTextTurn：一轮纯文本会话 payload：start ack → text 增量 → complete(status/text)。
func TestSessionPlainTextTurn(t *testing.T) {
	llm := textLLMServer("你好，我是助手")
	defer llm.Close()
	bus := newBus(t)
	startLLM(t, bus, llm.URL, false)
	registerInstance(t, bus, "ins-llm")
	time.Sleep(500 * time.Millisecond) // 实例注册（im 订阅）异步生效

	const session, turn = "s-llm-1", "t-llm-1"
	startTurn(t, bus, session, turn, "ins-llm")

	w := watch(t, bus, turn)
	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-llm", "session": session, "turn": turn,
		"type": "text-user", "content": "hello",
	}))
	evs, status := waitComplete(t, w, 10*time.Second)
	if status != "complete" {
		t.Fatalf("status = %q, want complete (events=%+v)", status, evs)
	}
	gotText := ""
	for _, ev := range evs {
		if ev["type"] == "text" {
			if p, ok := ev["payload"].(map[string]any); ok {
				gotText += str(p, "text")
			}
		}
	}
	if !strings.Contains(gotText, "你好") {
		types := make([]string, 0, len(evs))
		for _, ev := range evs {
			types = append(types, fmt.Sprintf("%v", ev["type"]))
		}
		t.Fatalf("received text miss: %q (types=%v)", gotText, types)
	}
	// session-complete payload 字段
	for _, ev := range evs {
		if st, ok := ev["status"].(string); ok && st == "complete" {
			if ev["session"] != session || ev["turn"] != turn {
				t.Fatalf("complete payload session/turn miss: %+v", ev)
			}
			if str(ev, "text") == "" {
				t.Fatalf("complete payload text empty: %+v", ev)
			}
			return
		}
	}
	t.Fatalf("no complete payload found: %+v", evs)
}

// TestLLMSimpleOneShot：无上下文单轮 LLM 方法面（llm-simple）：{prompt} → {text}。
func TestLLMSimpleOneShot(t *testing.T) {
	llm := textLLMServer("one-shot-reply")
	defer llm.Close()
	bus := newBus(t)
	startLLM(t, bus, llm.URL, false)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := bus.Emit(ctx, "llm-simple", map[string]any{"prompt": "summarize this"}).Wait()
	if err := v.Err(); err != nil {
		t.Fatalf("llm-simple: %v", err)
	}
	res, _ := v.Result.(map[string]any)
	if str(res, "text") != "one-shot-reply" {
		t.Fatalf("llm-simple text = %q, want one-shot-reply", str(res, "text"))
	}
}

// TestSessionContinueSameTurn：同轮次继续（session-start{continue:true}）复用既有轮次
// （同一 turn，不新增 turn），注入的 user 消息由 server 标 Kind=continue（同一轮）。
func TestSessionContinueSameTurn(t *testing.T) {
	llm := textLLMServer("ok")
	defer llm.Close()
	bus := newBus(t)
	startLLM(t, bus, llm.URL, false)
	registerInstance(t, bus, "ins-llm")
	time.Sleep(300 * time.Millisecond)

	const session, turn = "s-llm-cont", "t-llm-cont"
	startTurn(t, bus, session, turn, "ins-llm")
	w := watch(t, bus, turn)
	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-llm", "session": session, "turn": turn,
		"type": "text-user", "content": "hello",
	}))
	if _, status := waitComplete(t, w, 10*time.Second); status != "complete" {
		t.Fatalf("first status = %q, want complete", status)
	}

	// 同轮次继续：session-start 带 continue:true + 同一 turn
	// （loop 清 busy 在 session-complete 广播之后，可能短暂 "session busy" → 有界重试）
	w2 := watch(t, bus, turn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var res map[string]any
	for i := 0; i < 40; i++ {
		v := bus.Emit(ctx, "session-start", map[string]any{
			"instance_id": "ins-llm", "session": session, "turn": turn, "continue": true,
		}).Wait()
		if err := v.Err(); err != nil {
			t.Fatalf("continue session-start: %v", err)
		}
		res, _ = v.Result.(map[string]any)
		if ok, _ := res["accepted"].(bool); ok {
			break
		}
		if !strings.Contains(str(res, "error"), "busy") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("continue session-start rejected: %+v", res)
	}
	if str(res, "turn") != turn {
		t.Fatalf("continue turn = %q, want reuse %q", str(res, "turn"), turn)
	}
	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-llm", "session": session, "turn": turn,
		"type": "text-user", "content": "继续",
	}))
	evs, status := waitComplete(t, w2, 10*time.Second)
	if status != "complete" {
		t.Fatalf("continue status = %q, want complete (events=%+v)", status, evs)
	}
	// 事件仍在同一 turn 上（复用，不新增轮次）
	for _, ev := range evs {
		if ev["turn"] != turn {
			t.Fatalf("continue event turn = %v, want %q", ev["turn"], turn)
		}
	}
}

// TestSessionCancelInterrupted：运行中 cancel → session-complete{status:interrupted}。
func TestSessionCancelInterrupted(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer llm.Close()
	bus := newBus(t)
	startLLM(t, bus, llm.URL, false)
	registerInstance(t, bus, "ins-llm")
	time.Sleep(100 * time.Millisecond)

	const session, turn = "s-llm-cx", "t-llm-cx"
	startTurn(t, bus, session, turn, "ins-llm")
	w := watch(t, bus, turn)
	bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-llm", "session": session, "turn": turn,
		"type": "text-user", "content": "hang",
	}))
	time.Sleep(300 * time.Millisecond)
	bus.Emit(context.Background(), "session-cancel", jb(map[string]any{
		"instance_id": "ins-llm", "session": session, "turn": turn,
	}))
	_, status := waitComplete(t, w, 8*time.Second)
	if status != "interrupted" {
		t.Fatalf("status = %q, want interrupted", status)
	}
}

// TestTaskBackgroundForwardsGateway：task-background 受理并经**进程内 sink** 下发执行池转后台
// （2026-09-18：`tools/background` 方法面已移除）：
// 未知 tool_call_id → 无在飞任务可解绑 → 执行池回错误（errors 语义，v.Err 非空）。
func TestTaskBackgroundForwardsGateway(t *testing.T) {
	llm := textLLMServer("ok")
	defer llm.Close()
	bus := newBus(t)
	registerInstance(t, bus, "ins-llm")
	startLLM(t, bus, llm.URL, false) // DisableMCP=false：内嵌真实 gateway
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := bus.Emit(ctx, "task-background", map[string]any{
		"instance_id": "ins-llm", "tool_call_id": "tc-none",
	}).Wait()
	if v.Err() == nil {
		t.Fatalf("task-background for unknown tool_call_id should error, got %+v", v.Result)
	}
	if !strings.Contains(v.Err().Error(), "not found") {
		t.Fatalf("err = %v, want contains not found", v.Err())
	}
}

// TestDomainToolsListFields：真实内嵌 gateway 下 mcp-tools-list / mcp-prompts-list 输出字段。
func TestDomainToolsListFields(t *testing.T) {
	llm := textLLMServer("ok")
	defer llm.Close()
	bus := newBus(t)
	registerInstance(t, bus, "ins-llm")
	startLLM(t, bus, llm.URL, false) // DisableMCP=false：内嵌 gateway + 域工具/agent 注册

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 轮询 mcp-tools-list 直到域工具（self_ask_user）注入完成
	var tools []any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		v := bus.Emit(ctx, "mcp-tools-list", map[string]any{}).Wait()
		if v.Err() == nil {
			if res, ok := v.Result.(map[string]any); ok {
				tools, _ = res["tools"].([]any)
				for _, it := range tools {
					if m, ok := it.(map[string]any); ok && str(m, "name") == "self_ask_user" {
						goto found
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("self_ask_user not in tools list within 10s (tools=%+v)", tools)
found:
	// 条目 payload 字段断言
	for _, name := range []string{"self_ask_user", "self_llm_run", "self_tool_result", "self_tool_stop"} {
		foundTool := false
		for _, it := range tools {
			m, _ := it.(map[string]any)
			if str(m, "name") == name {
				if str(m, "description") == "" {
					t.Fatalf("%s description empty: %+v", name, m)
				}
				if m["inputSchema"] == nil {
					t.Fatalf("%s inputSchema nil: %+v", name, m)
				}
				if _, ok := m["scope"]; !ok {
					t.Fatalf("%s scope missing: %+v", name, m)
				}
				foundTool = true
				break
			}
		}
		if !foundTool {
			t.Fatalf("tool %s missing", name)
		}
	}
	// prompts list：25 §5/T2（2026-09-25）—— agent 已撤出资产面 → **不得再列 type=agent**
	// （旧「域 agent 经 prompts/register 注册为资产」已撤；agent 只经系统提示词"注入"）。
	v := bus.Emit(ctx, "mcp-prompts-list", map[string]any{}).Wait()
	if v.Err() != nil {
		t.Fatalf("mcp-prompts-list: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	prompts, _ := res["prompts"].([]any)
	for _, it := range prompts {
		m, _ := it.(map[string]any)
		if str(m, "type") == "agent" {
			t.Fatalf("prompts/list 不应再列 agent（25 §5/T2）：%+v", m)
		}
	}
}
