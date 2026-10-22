// chat 宿主适配器（MOD-1 M5b-①）：LLMClient / TurnStore / EventSink 契约验证。
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

	"github.com/chonkpilot/chonkpilot-chat"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-router"
)

// TestChatLLMClientStep：chat.LLMClient → router → StepEvent 流（文本 + Done）。
func TestChatLLMClientStep(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	cl := newChatLLMClient(s, router.Spec{
		Name: "chat-shadow", Protocol: ProtocolOpenAI, BaseURL: llm.URL, DefaultModel: "m1",
	}, ChatOptions{Model: "m1"})

	ch, err := cl.Step(context.Background(), chat.StepRequest{
		InstanceID: "ins-test", SessionID: "s-chat", TurnID: "t-chat",
		Messages: []chat.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
	}, chat.StepOptions{})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	var text string
	var done chat.StepEvent
	for ev := range ch {
		switch ev.Type {
		case chat.StepEventDelta:
			text += ev.Text
		case chat.StepEventDone:
			done = ev
		}
	}
	if text != "retry-ok" {
		t.Fatalf("文本增量=%q", text)
	}
	if done.Err != nil || done.Finish != "stop" {
		t.Fatalf("done=%+v", done)
	}
	if reqs := rec.requests(); len(reqs) != 1 || len(reqs[0]) != 2 || reqs[0][1].Content != "hi" {
		t.Fatalf("router 收到的消息错：%+v", reqs)
	}
}

// TestChatTurnStoreWrites：消息落库 + 事件日志幂等（同 seq 不同 type → 冲突）。
func TestChatTurnStoreWrites(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-chat-store"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn("t-chat-store", "s-chat-store"); err != nil {
		t.Fatal(err)
	}
	store := newChatTurnStore(s, "ins-test", "s-chat-store", "t-chat-store")
	if err := store.SaveMessage(context.Background(), chat.Message{Role: "assistant", Content: "hello"}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if msgs := st.LoadMessages("t-chat-store"); len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Fatalf("落库消息错：%+v", msgs)
	}

	ev := chat.TurnEvent{
		Seq: 1, Type: chat.TurnEventStart, At: time.Now().UTC(),
		Payload: []byte(`{"session_id":"s-chat-store","turn_id":"t-chat-store"}`),
	}
	if err := store.AppendEvent(context.Background(), ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	// 同 turn+seq 幂等（同 type）→ 成功；不同 type → 冲突。
	conflict, err := dataRequest(s.bus, msgkeys.TopicDataTurnEventAppend, map[string]any{
		"instance_id": "ins-test",
		"data": map[string]any{
			"session_id": "s-chat-store", "turn_id": "t-chat-store",
			"seq": 1, "type": "step_start", "at": time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
	if err == nil || conflict["ok"] == true {
		t.Fatalf("同 seq 不同 type 应失败：%+v %v", conflict, err)
	}
}

// TestChatShadowParity：引擎经宿主三个适配器跑两遍（第二遍挂影子记录），轨迹逐项相等。
// 等价比对 = StepRequest 序列 + 落库写入序列（消息 / 事件日志）；时钟固定，排除时间噪声。
func TestChatShadowParity(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-shadow"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn("t-shadow", "s-shadow"); err != nil {
		t.Fatal(err)
	}

	cl := newChatLLMClient(s, router.Spec{
		Name: "chat-shadow", Protocol: ProtocolOpenAI, BaseURL: llm.URL, DefaultModel: "m1",
	}, ChatOptions{Model: "m1"})
	store := newChatTurnStore(s, "ins-test", "s-shadow", "t-shadow")
	sink := newChatEventSink(s, "ins-test", "s-shadow", "t-shadow", nil)

	run := func() chat.Trace {
		shadow := chat.NewRecorder()
		eng := chat.New(chat.Deps{
			InstanceID: "ins-test", SessionID: "s-shadow",
			Context: shadowContext{},
			Store:   &chat.ShadowTurnStore{Inner: store, Rec: shadow},
			Sink:    sink, LLM: cl, Catalog: shadowCatalog{}, Shadow: shadow,
			Clock:  shadowClock{at: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)},
			Config: chat.RuntimeConfig{MaxSteps: 4},
		})
		res, err := eng.RunTurn(context.Background(), "t-shadow")
		if err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		if res.FinalText != "retry-ok" || res.Finish != "stop" {
			t.Fatalf("终态错：%+v", res)
		}
		return shadow.Trace()
	}

	want := run()
	got := run()
	if err := chat.Verify(want, got); err != nil {
		t.Fatalf("影子轨迹不等价：%v", err)
	}
	if len(want.Steps) != 1 || len(want.Writes) == 0 {
		t.Fatalf("影子轨迹异常：steps=%d writes=%d", len(want.Steps), len(want.Writes))
	}
	// 写入顺序：turn_start/step_start 事件 → assistant 消息 → step_done/turn_end 事件。
	var kinds []string
	for _, w := range want.Writes {
		kinds = append(kinds, w.Kind)
	}
	wantKinds := []string{"event", "event", "message", "event", "event"}
	if strings.Join(kinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("写入顺序错：%v", kinds)
	}
}

// TestChatEnginePublishesRetry（M6e）：引擎驱动时 OnRetry → EventLLMRetry → `llm-retry`
// （相对主题 `session-retry`）。桩：第 1 次 HTTP 500（router 报文级重试），第 2 次正常 SSE。
func TestChatEnginePublishesRetry(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // probe 一律 200
			w.WriteHeader(http.StatusOK)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
			return
		}
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "retry-ok"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	}))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	const session, turn = "s-engine-retry", "t-engine-retry"
	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession(session); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn(turn, session); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var retries []map[string]any
	if _, err := s.bus.On(domainSubject("llm-retry"), 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		if json.Unmarshal(v.Payload, &m) != nil {
			return nil
		}
		if m["turn"] != turn {
			return nil
		}
		mu.Lock()
		retries = append(retries, m)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	retryCount := 1
	cl := newChatLLMClient(s, router.Spec{
		Name: "chat-retry", Protocol: ProtocolOpenAI, BaseURL: llm.URL, DefaultModel: "m1",
	}, ChatOptions{Model: "m1", MaxRetries: &retryCount})
	eng := chat.New(chat.Deps{
		InstanceID: "ins-test", SessionID: session,
		Context: shadowContext{},
		Store:   newChatTurnStore(s, "ins-test", session, turn),
		Sink:    newChatEventSink(s, "ins-test", session, turn, nil),
		LLM:     cl,
		Clock:   shadowClock{at: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)},
		Config:  chat.RuntimeConfig{MaxSteps: 4, RetryCount: &retryCount, RetryBudget: 2 * time.Second},
	})

	res, err := eng.RunTurn(context.Background(), turn)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.FinalText != "retry-ok" || res.Finish != "stop" {
		t.Fatalf("终态错：%+v", res)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("LLM 请求数=%d want 2（1 次失败 + 1 次重试）", n)
	}

	var got []map[string]any
	for i := 0; i < 20; i++ {
		mu.Lock()
		got = append([]map[string]any(nil), retries...)
		mu.Unlock()
		if len(got) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) == 0 {
		t.Fatalf("未收到 llm-retry 事件")
	}
	attempt, _ := got[0]["retry_attempt"].(float64)
	count, _ := got[0]["retry_count"].(float64)
	if int(attempt) < 1 || int(count) != 1 {
		t.Fatalf("llm-retry 载荷错：%+v", got[0])
	}
	if got[0]["session"] != session || got[0]["instance_id"] != "ins-test" {
		t.Fatalf("llm-retry 归属错：%+v", got[0])
	}
}

// shadowContext / shadowCatalog / shadowClock：影子等价测试的最小端口实现。
type shadowContext struct{}

func (shadowContext) Build(context.Context, chat.ContextRequest) (chat.Context, error) {
	return chat.Context{
		SystemPrompt: "sys",
		Messages:     []chat.Message{{Role: "user", Content: "hi"}},
	}, nil
}

type shadowCatalog struct{}

func (shadowCatalog) Servers(context.Context, string) ([]chat.ServerInfo, error) { return nil, nil }

func (shadowCatalog) Tools(context.Context, string) ([]chat.ToolSpec, error) {
	return []chat.ToolSpec{{
		Name: "echo", Description: "echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}, nil
}

type shadowClock struct{ at time.Time }

func (c shadowClock) Now() time.Time                       { return c.at }
func (shadowClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// TestChatTurnStoreCommitInbox：`data-inbox-commit` 一步原子消费（重试幂等、消息只落一条）。
func TestChatTurnStoreCommitInbox(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	res, err := dataRequest(s.bus, msgkeys.TopicDataInboxAppend, map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"session": "s-inbox", "type": "insert", "content": "hi"},
	})
	if err != nil {
		t.Fatalf("inbox append: %v", err)
	}
	inboxID := str(res["id"])
	if inboxID == "" {
		t.Fatalf("inbox append 未返回 id：%+v", res)
	}

	st := newSessionStore(s.bus, "ins-test")
	if err := st.EnsureSession("s-inbox"); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureTurn("t-inbox", "s-inbox"); err != nil {
		t.Fatal(err)
	}
	store := newChatTurnStore(s, "ins-test", "s-inbox", "t-inbox")
	if err := store.CommitInbox(context.Background(), inboxID, chat.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("CommitInbox: %v", err)
	}
	if msgs := st.LoadMessages("t-inbox"); len(msgs) != 1 || msgs[0].Content != "hi" {
		t.Fatalf("落库消息错：%+v", msgs)
	}
	// 同 inbox 重试：不重复落库。
	if err := store.CommitInbox(context.Background(), inboxID, chat.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("CommitInbox 重试: %v", err)
	}
	if msgs := st.LoadMessages("t-inbox"); len(msgs) != 1 {
		t.Fatalf("重试后消息数应仍为 1：%d", len(msgs))
	}
	if _, err := dataRequest(s.bus, msgkeys.TopicDataInboxList, map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"session": "s-inbox", "status": "consumed"},
	}); err != nil {
		t.Fatalf("inbox list: %v", err)
	}
}

// TestChatEventSinkTopics：事件映射到既有主题（llm-receive / llm-complete）。
func TestChatEventSinkTopics(t *testing.T) {
	rec := &llmRecorder{}
	llm := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer llm.Close()
	s := newTestServer(t, llm)
	registerTestInstance(t, s)

	receive := make(chan map[string]any, 1)
	complete := make(chan map[string]any, 1)
	// 订阅相对域主题（llm-receive → session-receive，见 domainTopics）。
	if _, err := s.bus.On(domainSubject("llm-receive"), 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		receive <- m
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.bus.On(domainSubject("llm-complete"), 0, func(_ context.Context, _ string, v *mq.Value) error {
		var m map[string]any
		_ = json.Unmarshal(v.Payload, &m)
		complete <- m
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	sink := newChatEventSink(s, "ins-test", "s-evt", "t-evt", nil)
	sink.Emit(context.Background(), chat.Event{Type: chat.EventLLMDelta, Text: "hi"})
	sink.Emit(context.Background(), chat.Event{Type: chat.EventTurnComplete, Status: "complete", Text: "done"})

	select {
	case m := <-receive:
		if m["type"] != NotifyTypeText || m["session"] != "s-evt" || m["turn"] != "t-evt" {
			t.Fatalf("llm-receive 载荷错：%+v", m)
		}
		payload, _ := m["payload"].(map[string]any)
		if payload["text"] != "hi" {
			t.Fatalf("llm-receive payload 错：%+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 llm-receive")
	}
	select {
	case m := <-complete:
		if m["status"] != "complete" || m["text"] != "done" || m["session"] != "s-evt" {
			t.Fatalf("llm-complete 载荷错：%+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 llm-complete")
	}
}
