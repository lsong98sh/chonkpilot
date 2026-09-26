// LR-11 接线白盒：llm ↔ router 的**折算层**（`routercall.go`）——请求折算（图片块 / 工具 /
// 选项）、事件折算（含 `tool_call` 聚成终态 `ToolCalls`）、错误折算与取消语义。
//
// 本文件同时承载原 Responses 协议白盒迁移后**仍属 llm 侧**的素材构造件（`responsesSSE` 等，
// 供 `llm_images_test.go` 的 responses 用例复用）；协议本身的解析断言已归 router 白盒
// （`src/lib/router/internal/adaptor/openai/responses_test.go`）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-router"
)

// newSSEServer 起一个受测端点（测试结束自动关闭）。
func newSSEServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// chatTest 走**生产同一条路径**（`Server.chat`）发一次来回（`step`）：只用 `rt` + 测试用 Spec，
// 不启动整个服务（MQ / persist / gateway 均无关本折算层）。
func chatTest(t *testing.T, spec router.Spec, msgs []ChatMsg, tools []ToolDef, opts ChatOptions) (<-chan StreamEvent, error) {
	t.Helper()
	s := &Server{rt: router.New(router.Config{})}
	return s.chat(context.Background(), spec, msgs, tools, opts)
}

// openAITestSpec 构造指向测试端点的 openai 协议 Spec。
func openAITestSpec(base, model string) router.Spec {
	return router.Spec{Name: "test", Protocol: ProtocolOpenAI, BaseURL: base, DefaultModel: model}
}

// responsesSSE 写 Responses 语义化 SSE（event: + data: 成对；无 data: [DONE]）。
// 每个事件的 data 里带 type（与 event: 同名），与 DeepSeek/OpenAI 文档一致。
func responsesSSE(w http.ResponseWriter, events []map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	for _, ev := range events {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], string(b))
		if fl != nil {
			fl.Flush()
		}
	}
}

// responsesTextDelta / responsesCompleted 是常用事件构造（增量文本 / 终态）。
func responsesTextDelta(text string) map[string]any {
	return map[string]any{"type": "response.output_text.delta", "delta": text}
}

func responsesCompleted() map[string]any {
	return map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}}
}

// collectStreamEvents 收完流事件并返回 (文本, 思维链, 工具调用, finish, err)。
func collectStreamEvents(evs <-chan StreamEvent) (string, string, []ToolCall, string, error) {
	var text, think strings.Builder
	var calls []ToolCall
	finish := ""
	var streamErr error
	for ev := range evs {
		if ev.Err != nil {
			streamErr = ev.Err
		}
		text.WriteString(ev.Content)
		think.WriteString(ev.Reasoning)
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
		if ev.Done {
			finish = ev.FinishReason
		}
	}
	return text.String(), think.String(), calls, finish, streamErr
}

// TestNormalizeLLMProtocol：协议归一——responses / echo 生效；openai/空/未知 → openai。
func TestNormalizeLLMProtocol(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ProtocolOpenAI}, {"openai", ProtocolOpenAI}, {"responses", ProtocolResponses},
		{"echo", ProtocolEcho}, {"unknown", ProtocolOpenAI}, {" responses ", ProtocolResponses},
	} {
		if got := NormalizeLLMProtocol(tc.in); got != tc.want {
			t.Fatalf("NormalizeLLMProtocol(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// TestChatPlainStream：普通流 → content 增量 + 终态 Done(stop)；请求体与旧直连路径同形
// （model 回落 spec.DefaultModel、无 tools 即不下发 tools、无思考即无 reasoning_effort）。
func TestChatPlainStream(t *testing.T) {
	var got map[string]any
	srv := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		llmSSE(w, []string{
			sseChunk(map[string]any{"content": "h"}, ""),
			sseChunk(map[string]any{"content": "i"}, ""),
			sseChunk(map[string]any{}, "stop"),
		})
	})

	evs, err := chatTest(t, openAITestSpec(srv.URL, "mock"), []ChatMsg{{Role: "user", Content: "q"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	text, _, calls, finish, serr := collectStreamEvents(evs)
	if serr != nil || text != "hi" || finish != "stop" || len(calls) != 0 {
		t.Fatalf("text=%q finish=%q calls=%v err=%v", text, finish, calls, serr)
	}
	if model, _ := got["model"].(string); model != "mock" {
		t.Fatalf("body.model=%v want mock（Options.Model 空 → spec.DefaultModel）", got["model"])
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("无工具时不应下发 tools: %+v", got["tools"])
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("无思考配置时不应下发 reasoning_effort: %+v", got["reasoning_effort"])
	}
}

// TestChatToolCallsMergedIntoDone：分片 tool_calls → 拼装完成的 `tool_call`（第 7 类事件，D-32）
// 在 llm 侧**聚成终态 Done 附带的 ToolCalls**（复刻旧 chat 路径「终态一次带全部调用」的形状），
// finish 归一为 `tool_calls`。
func TestChatToolCallsMergedIntoDone(t *testing.T) {
	srv := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		llmSSE(w, []string{
			sseChunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call-1", "type": "function",
				"function": map[string]any{"name": "core_file_read", "arguments": `{"fil`},
			}}}, ""),
			sseChunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call-1", "type": "function",
				"function": map[string]any{"name": "core_file_read", "arguments": `es":[]}`},
			}}}, "tool_calls"),
		})
	})

	evs, err := chatTest(t, openAITestSpec(srv.URL, "mock"), []ChatMsg{{Role: "user", Content: "t"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	text, _, calls, finish, serr := collectStreamEvents(evs)
	if serr != nil || text != "" || finish != "tool_calls" {
		t.Fatalf("text=%q finish=%q err=%v", text, finish, serr)
	}
	if len(calls) != 1 || calls[0].ID != "call-1" || calls[0].Function.Name != "core_file_read" {
		t.Fatalf("calls=%+v", calls)
	}
	if calls[0].Function.Arguments != `{"files":[]}` {
		t.Fatalf("arguments 聚合错: %q", calls[0].Function.Arguments)
	}
	if calls[0].Type != toolCallTypeFunction {
		t.Fatalf("ToolCall.Type=%q want function", calls[0].Type)
	}
}

// TestChatRequestOptionsMapping：思考 / 温度 / 上限 / 工具定义按旧口径落到请求体
// （Effort 优先于 Think；tools 为 `{type:function,function:{…}}`）。
func TestChatRequestOptionsMapping(t *testing.T) {
	var got map[string]any
	srv := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
	})

	temp, maxTok := 0.0, 0
	evs, err := chatTest(t, openAITestSpec(srv.URL, "m1"),
		[]ChatMsg{{Role: "assistant", Content: "a", Reasoning: "思考"}},
		[]ToolDef{{Name: "f", Description: "d", Parameters: map[string]any{"type": "object"}}},
		ChatOptions{Model: "m-override", Think: "low", Effort: "high", Temperature: &temp, MaxTokens: &maxTok})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if _, _, _, _, serr := collectStreamEvents(evs); serr != nil {
		t.Fatalf("stream err: %v", serr)
	}
	if got["model"] != "m-override" {
		t.Fatalf("body.model=%v want m-override（Options.Model 优先）", got["model"])
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%v want high（Effort 优先于 Think）", got["reasoning_effort"])
	}
	if v, ok := got["temperature"]; !ok || v != float64(0) {
		t.Fatalf("temperature=%v ok=%v want 0 已下发", v, ok)
	}
	if v, ok := got["max_tokens"]; !ok || v != float64(0) {
		t.Fatalf("max_tokens=%v ok=%v want 0 已下发", v, ok)
	}
	msgs, _ := got["messages"].([]any)
	m0, _ := msgs[0].(map[string]any)
	if m0["reasoning_content"] != "思考" {
		t.Fatalf("reasoning_content=%v want 思考", m0["reasoning_content"])
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%+v want 1", got["tools"])
	}
	t0, _ := tools[0].(map[string]any)
	fn, _ := t0["function"].(map[string]any)
	if t0["type"] != "function" || fn["name"] != "f" {
		t.Fatalf("tools[0]=%+v", t0)
	}
}

// TestChatErrorMapping：router 分类错误 → `*LLMError`（Kind 同名同义、Retryable 透传）；
// `invalid`（调用方用法错误）→ `protocol`。
func TestChatErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status    int
		want      ErrKind
		retryable bool
	}{
		{401, ErrAuth, false}, {403, ErrAuth, false},
		{429, ErrRateLimit, true}, {500, ErrServer, true}, {503, ErrServer, true},
		{400, ErrProtocol, false}, {404, ErrProtocol, false},
	} {
		srv := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			fmt.Fprint(w, `{"error":{"message":"boom"}}`)
		})
		evs, err := chatTest(t, openAITestSpec(srv.URL, "mock"), []ChatMsg{{Role: "user", Content: "hi"}}, nil, ChatOptions{})
		if err != nil {
			t.Fatalf("status %d: chat 应把 HTTP 失败作为流内错误（err=%v）", tc.status, err)
		}
		_, _, _, _, serr := collectStreamEvents(evs)
		le, ok := serr.(*LLMError)
		if !ok {
			t.Fatalf("status %d: err=%v want *LLMError", tc.status, serr)
		}
		if le.Kind != tc.want || le.Retryable != tc.retryable {
			t.Fatalf("status %d: kind=%s retryable=%v want %s/%v", tc.status, le.Kind, le.Retryable, tc.want, tc.retryable)
		}
		if !strings.Contains(le.Message, "boom") {
			t.Fatalf("status %d: message=%q 应含上游错误信息", tc.status, le.Message)
		}
	}
}

// TestChatUnregisteredProviderInvalid：Spec 非法（未注册 / 协议无适配器）时 `chat` 同步报错。
func TestChatUnregisteredProviderInvalid(t *testing.T) {
	s := &Server{rt: router.New(router.Config{})}
	_, err := s.chat(context.Background(), router.Spec{Name: "", Protocol: ProtocolOpenAI}, nil, nil, ChatOptions{})
	if err == nil {
		t.Fatal("空 Name 应报错（invalid → protocol）")
	}
	le, ok := err.(*LLMError)
	if !ok || le.Kind != ErrProtocol {
		t.Fatalf("err=%v want *LLMError{protocol}", err)
	}
}

// TestChatCancelSilent：消费方取消 → 流内补投一条取消类错误（turn 据此静默返回，不误判空回复）。
func TestChatCancelSilent(t *testing.T) {
	srv := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: "+sseChunk(map[string]any{"content": "x"}, "")+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(2 * time.Second) // 保持流打开，等消费方取消
	})

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{rt: router.New(router.Config{})}
	evs, err := s.chat(ctx, openAITestSpec(srv.URL, "mock"), []ChatMsg{{Role: "user", Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	first := <-evs
	if first.Content != "x" {
		t.Fatalf("首个事件=%+v want content=x", first)
	}
	cancel()
	var got error
	for ev := range evs {
		if ev.Err != nil {
			got = ev.Err
		}
	}
	if got == nil {
		t.Fatal("取消后应补投取消类错误事件（turn 静默判据）")
	}
}

// TestRouterWireParityKnownDiffs 固化 llm→router 的**已知线格式差异**（事件语义不变）：
// `stream_options.include_usage` 恒开启；其余报文形态与旧直连路径一致。
func TestRouterWireParityKnownDiffs(t *testing.T) {
	var got map[string]any
	srv := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, "stop")})
	})
	evs, err := chatTest(t, openAITestSpec(srv.URL, "mock"), []ChatMsg{{Role: "user", Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if _, _, _, _, serr := collectStreamEvents(evs); serr != nil {
		t.Fatalf("stream err: %v", serr)
	}
	so, ok := got["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("已知差异：router 恒开启 stream_options.include_usage，实得 %+v", got["stream_options"])
	}
	if got["stream"] != true {
		t.Fatalf("stream=%v want true", got["stream"])
	}
}
