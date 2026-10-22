// chat 宿主接线（MOD-1 A4-P1）：Inbox FIFO / 生产策略 tool invoker / session-interrupted 映射。
//
// 本文件只验证**新增适配器**；生产 `turnCtx` 的工具循环与 `server.go` 的启动订阅未改。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lsong98sh/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestChatInboxAdapter：FIFO / 空队列非阻塞 / Len / 并发 Push。
func TestChatInboxAdapter(t *testing.T) {
	q := newChatInbox()
	if q.Len() != 0 {
		t.Fatalf("初始 Len=%d want 0", q.Len())
	}
	if _, ok, err := q.Poll(context.Background()); ok || err != nil {
		t.Fatalf("空队列 Poll 应非阻塞返回 false/nil：%v %v", ok, err)
	}

	q.Push(chat.InboxItem{ID: "a"})
	q.Push(chat.InboxItem{ID: "b"})
	if q.Len() != 2 {
		t.Fatalf("Len=%d want 2", q.Len())
	}
	for _, want := range []string{"a", "b"} {
		item, ok, err := q.Poll(context.Background())
		if err != nil || !ok || item.ID != want {
			t.Fatalf("FIFO 出队错：item=%+v ok=%v err=%v want=%s", item, ok, err, want)
		}
	}
	if _, ok, _ := q.Poll(context.Background()); ok {
		t.Fatal("耗尽后 Poll 应返回 false")
	}

	const n = 32
	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			q.Push(chat.InboxItem{ID: fmt.Sprintf("i-%d", k)})
		}(k)
	}
	wg.Wait()
	if q.Len() != n {
		t.Fatalf("并发 Push 后 Len=%d want %d", q.Len(), n)
	}
	seen := make(map[string]bool, n)
	for k := 0; k < n; k++ {
		item, ok, err := q.Poll(context.Background())
		if err != nil || !ok {
			t.Fatalf("第 %d 次 Poll：ok=%v err=%v", k, ok, err)
		}
		seen[item.ID] = true
	}
	if len(seen) != n {
		t.Fatalf("并发 Push 丢项：len=%d want %d", len(seen), n)
	}
}

// TestChatInboxItemFromFacade：facade.InboxItem → chat.InboxItem 字段逐项一致（Think/Effort 原样 JSON）。
func TestChatInboxItemFromFacade(t *testing.T) {
	src := facade.InboxItem{
		ID: "i-1", SessionID: "s-1", TurnID: "t-1", Type: chat.InboxTypeInsert,
		LLM: "m-1", Content: "插话", Think: map[string]any{"type": "enabled"},
		Effort: "high", ScenarioID: "sc-1", Status: "pending",
		IdempotencyKey: "ik-1", CreatedAt: "2026-10-20T00:00:00Z",
	}
	got := ChatInboxFromFacade(src)
	want := chat.InboxItem{
		ID: src.ID, SessionID: src.SessionID, TurnID: src.TurnID, Type: src.Type,
		LLM: src.LLM, Content: src.Content, ScenarioID: src.ScenarioID,
		Status: src.Status, IdempotencyKey: src.IdempotencyKey, CreatedAt: src.CreatedAt,
	}
	if got.ID != want.ID || got.SessionID != want.SessionID || got.TurnID != want.TurnID ||
		got.Type != want.Type || got.LLM != want.LLM || got.Content != want.Content ||
		got.ScenarioID != want.ScenarioID || got.Status != want.Status ||
		got.IdempotencyKey != want.IdempotencyKey || got.CreatedAt != want.CreatedAt {
		t.Fatalf("字段不一致：\n got  = %+v\n want = %+v", got, want)
	}
	var think map[string]any
	if err := json.Unmarshal(got.Think, &think); err != nil || think["type"] != "enabled" {
		t.Fatalf("Think 未原样保留：%s %v", got.Think, err)
	}
	var effort string
	if err := json.Unmarshal(got.Effort, &effort); err != nil || effort != "high" {
		t.Fatalf("Effort 未原样保留：%s %v", got.Effort, err)
	}
}

// TestChatToolInvokerFromTurnPolicy：生产策略装配（白名单硬拒 / mcp_invoke 旁路加固 / 正常下发 / args 钩子）。
//
// turnCtx 直接用生产字段构造（`req` / `allowedTools` / `denyTools` / `server` 即 policy 与 args
// 钩子消费的全部输入）；生产由 `newTurnCtx` 建同形实例（本用例不跑 loop）。
func TestChatToolInvokerFromTurnPolicy(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)

	fakeGWCallCtxMu.Lock()
	fakeGWCallCtx = map[string][2]string{}
	fakeGWCallCtxMu.Unlock()

	tc := &turnCtx{
		server: s,
		req: StartReq{
			InstanceID: "ins-test", Session: "s-a4", Turn: "t-a4",
			WorkDir: "/work", DataDir: "/data",
		},
		allowedTools: map[string]struct{}{"core_file_read": {}, "self_mcp_invoke": {}},
	}
	inv := chatToolInvokerFromTurn(tc)
	if inv.args == nil || inv.policy == nil || inv.ask == nil {
		t.Fatalf("生产策略钩子未装配：args=%v policy=%v ask=%v", inv.args != nil, inv.policy != nil, inv.ask != nil)
	}
	ctx := context.Background()

	// ① 白名单外工具：失败回喂，不下发 gateway。
	out, err := inv.Dispatch(ctx, chat.ToolCall{ID: "tc-deny", Name: "shell_exec", Arguments: `{"cmd":"ls"}`})
	if err != nil {
		t.Fatal(err)
	}
	wantDeny := fmt.Sprintf("错误: 工具 %q 不在本轮 agent 的工具白名单内，已拒绝执行（可用工具见本轮 tools 参数；白名单 = 场景 agent 的「工具过滤」配置）", "shell_exec")
	if out.Pending || !out.Result.IsError || out.Result.Content != wantDeny {
		t.Fatalf("白名单拒绝结果错：%+v", out)
	}
	if _, called := gwCallCtxOf("tc-deny"); called {
		t.Fatal("白名单拒绝不应下发 gateway")
	}

	// ② mcp_invoke 目标不在白名单：旁路加固拒绝，不下发 gateway。
	out, err = inv.Dispatch(ctx, chat.ToolCall{ID: "tc-meta", Name: "self_mcp_invoke", Arguments: `{"name":"shell_exec"}`})
	if err != nil {
		t.Fatal(err)
	}
	wantMeta := fmt.Sprintf("错误: mcp_invoke 的目标工具 %q 不在本轮 agent 的工具白名单内，已拒绝执行（白名单 = 场景 agent 的「工具过滤」配置；白名单内工具可直接调用）", "shell_exec")
	if out.Pending || !out.Result.IsError || out.Result.Content != wantMeta {
		t.Fatalf("mcp_invoke 旁路拒绝结果错：%+v", out)
	}
	if _, called := gwCallCtxOf("tc-meta"); called {
		t.Fatal("mcp_invoke 旁路拒绝不应下发 gateway")
	}

	// ③ 白名单内工具：正常下发（fake gateway 固定回 "file content"）。
	out, err = inv.Dispatch(ctx, chat.ToolCall{ID: "tc-ok", Name: "core_file_read", Arguments: `{"path":"a.txt"}`})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pending || out.Result.IsError || out.Result.Content != "file content" {
		t.Fatalf("白名单内工具结果错：%+v", out)
	}
	if _, called := gwCallCtxOf("tc-ok"); !called {
		t.Fatal("白名单内工具应下发 gateway")
	}

	// ④ args 钩子：普通工具缺省补 async=always（不改原 map）；进程内工具不补。
	orig := map[string]any{"path": "a.txt"}
	got := inv.args("core_file_read", orig)
	if got["async"] != "always" {
		t.Fatalf("普通工具应补 async=always：%+v", got)
	}
	if _, mutated := orig["async"]; mutated {
		t.Fatalf("args 钩子不应改写入参 map：%+v", orig)
	}
	if _, has := inv.args("ask_user", map[string]any{"question": "q"})["async"]; has {
		t.Fatal("进程内工具（ask_user）不应补 async")
	}
}

// gwCallCtxOf 查询 fake gateway 是否收到某次 tools/call。
func gwCallCtxOf(toolCallID string) ([2]string, bool) {
	fakeGWCallCtxMu.Lock()
	defer fakeGWCallCtxMu.Unlock()
	v, ok := fakeGWCallCtx[toolCallID]
	return v, ok
}

// TestChatEventSinkInterrupted：session_interrupted → `llm-interrupted`（六字段 + 可选 message）。
func TestChatEventSinkInterrupted(t *testing.T) {
	s := newTestServer(t, httptest.NewServer(http.NotFoundHandler()))
	registerTestInstance(t, s)

	got := make(chan map[string]any, 2)
	if _, err := s.bus.On(domainSubject("llm-interrupted"), 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		got <- m
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	next := func() map[string]any {
		select {
		case m := <-got:
			return m
		case <-time.After(2 * time.Second):
			t.Fatal("未收到 llm-interrupted")
			return nil
		}
	}

	sink := newChatEventSink(s, "ins-test", "s-a4", "t-a4", nil)
	sink.Emit(context.Background(), chat.Event{
		Type: chat.EventSessionInterrupted, Text: "partial",
		Code: "length", Retryable: true, DelayMilli: 600000,
	})
	m := next()
	if m["instance_id"] != "ins-test" || m["session"] != "s-a4" || m["turn"] != "t-a4" {
		t.Fatalf("归属字段错：%+v", m)
	}
	if m["code"] != "length" || m["retryable"] != true {
		t.Fatalf("code/retryable 错：%+v", m)
	}
	if delay, _ := m["auto_continue_delay_ms"].(float64); int64(delay) != 600000 {
		t.Fatalf("auto_continue_delay_ms=%v want 600000", m["auto_continue_delay_ms"])
	}
	if m["message"] != "partial" {
		t.Fatalf("message 错：%+v", m)
	}

	// 空文本 → 省略 message（其余字段仍在）。
	sink.Emit(context.Background(), chat.Event{Type: chat.EventSessionInterrupted, Code: "stream", Retryable: true})
	m = next()
	if m["code"] != "stream" {
		t.Fatalf("code 错：%+v", m)
	}
	if _, has := m["message"]; has {
		t.Fatalf("空文本不应带 message：%+v", m)
	}
}
