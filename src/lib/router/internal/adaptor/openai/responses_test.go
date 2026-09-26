// D-29 白盒：OpenAI Responses API 适配器（协议名 responses；DeepSeek /responses 亦走此协议）——
// 请求编码（instructions / input items / 扁平 tools / max_output_tokens / reasoning.effort）·
// 语义化 SSE 事件映射（output_text / reasoning_text / function_call_arguments / completed|incomplete|failed）·
// usage 归一（LR-9）· 断链判定。
package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// baseResponsesSpec 是给假端点用的 responses provider 配置（base 含版本段 → 端点 /v1/responses）。
func baseResponsesSpec(srv *httptest.Server) canon.Spec {
	return canon.Spec{Name: "ds", Protocol: canon.ProtocolResponses, BaseURL: srv.URL + "/v1/", APIKey: "sk-r", DefaultModel: "ds-default"}
}

// playChunks 把原始 SSE 分片写入响应（逐片 flush；写完关闭 → 客户端见 EOF）。
func playChunks(t *testing.T, w http.ResponseWriter, chunks ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("响应不支持 flush")
	}
	for _, c := range chunks {
		if _, err := io.WriteString(w, c); err != nil {
			t.Fatalf("写分片: %v", err)
		}
		flusher.Flush()
	}
}

// sse 把一条 JSON 载荷包成 SSE 事件（`data:` 行 + 终结空行，与上游同形）。
func sse(payload string) string {
	return "data: " + payload + "\n\n"
}

// runResponsesStream 跑一次 Responses 适配器的 Stream 并收集事件。
func runResponsesStream(t *testing.T, a *ResponsesAdapter, spec canon.Spec, req canon.Request) ([]canon.Event, error) {
	t.Helper()
	out := make(chan canon.Event, 64)
	done := make(chan error, 1)
	go func() {
		err := a.Stream(context.Background(), spec, req, out)
		close(out)
		done <- err
	}()
	var evs []canon.Event
	for ev := range out {
		evs = append(evs, ev)
	}
	return evs, <-done
}

// TestResponsesProtocolAndCaps：协议名 = responses；与 OpenAI 家族（chat completions）能力等价。
func TestResponsesProtocolAndCaps(t *testing.T) {
	a := NewResponsesAdapter(Options{})
	if got := a.Protocol(); got != canon.ProtocolResponses {
		t.Fatalf("Protocol=%q want %q", got, canon.ProtocolResponses)
	}
	if a.Caps() != New(Options{}).Caps() {
		t.Fatalf("Caps=%+v want 与 chat completions 等价 %+v", a.Caps(), New(Options{}).Caps())
	}
}

// TestResponsesRequestEncoding：system → `instructions`；input items（message / reasoning /
// function_call / function_call_output）；扁平 tools；`max_output_tokens` / `reasoning.effort`；
// 端点 `/responses`；鉴权头；`stream:true`。
func TestResponsesRequestEncoding(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		playChunks(t, w, sse(`{"type":"response.completed","response":{"status":"completed"}}`))
	})

	temp, topP := 0.3, 0.9
	maxTokens := 256
	req := canon.Request{
		Messages: []canon.Message{
			{Role: canon.RoleSystem, Content: []canon.Part{{Type: canon.PartText, Text: "系统一"}}},
			{Role: canon.RoleSystem, Content: []canon.Part{{Type: canon.PartText, Text: "系统二"}}},
			{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "看图"}, {Type: canon.PartImage, Image: &canon.ImagePart{DataURL: "data:image/png;base64,AA"}}}},
			{Role: canon.RoleAssistant, Reasoning: "想过", Content: []canon.Part{{Type: canon.PartText, Text: "我读一下"}}, ToolCalls: []canon.ToolCall{{ID: "call_1", Name: "core_file_read", Arguments: `{"path":"a"}`}}},
			{Role: canon.RoleTool, ToolCallID: "call_1", Content: []canon.Part{{Type: canon.PartText, Text: "文件内容"}}},
		},
		Tools:   []canon.ToolDef{{Name: "core_file_read", Description: "读文件", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		Options: canon.CallOptions{Model: "ds-x", Temperature: &temp, TopP: &topP, MaxTokens: &maxTokens, Reasoning: &canon.Reasoning{Effort: "high"}},
	}
	if _, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if got.path != "/v1/responses" {
		t.Fatalf("请求路径=%q want /v1/responses（base 尾斜杠已容忍）", got.path)
	}
	if got.auth != "Bearer sk-r" {
		t.Fatalf("Authorization=%q", got.auth)
	}
	if got.body["model"] != "ds-x" || got.body["stream"] != true {
		t.Fatalf("model/stream=%v/%v", got.body["model"], got.body["stream"])
	}
	if got.body["instructions"] != "系统一\n\n系统二" {
		t.Fatalf("instructions=%q want 多段拼接", got.body["instructions"])
	}
	if got.body["max_output_tokens"] != float64(256) {
		t.Fatalf("max_output_tokens=%v want 256（Responses 口径）", got.body["max_output_tokens"])
	}
	if _, ok := got.body["max_tokens"]; ok {
		t.Fatalf("不应下发 chat 口径 max_tokens：%v", got.body)
	}
	reasoning, _ := got.body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning=%v want {effort:high}", got.body["reasoning"])
	}
	if got.body["temperature"] != temp || got.body["top_p"] != topP {
		t.Fatalf("temperature/top_p=%v/%v", got.body["temperature"], got.body["top_p"])
	}
	if got.body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice=%v", got.body["tool_choice"])
	}

	tools, _ := got.body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v", got.body["tools"])
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["type"] != "function" || tool0["name"] != "core_file_read" || tool0["description"] != "读文件" {
		t.Fatalf("扁平工具定义=%v", tool0)
	}
	if _, nested := tool0["function"]; nested {
		t.Fatalf("Responses 工具定义不应有 function 嵌套层：%v", tool0)
	}
	if params, _ := tool0["parameters"].(map[string]any); params["type"] != "object" {
		t.Fatalf("parameters=%v", tool0["parameters"])
	}

	input, _ := got.body["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input 条数=%d want 5（user / reasoning / function_call / assistant 正文 / function_call_output）", len(input))
	}
	user0, _ := input[0].(map[string]any)
	if user0["type"] != "message" || user0["role"] != "user" {
		t.Fatalf("user item=%v", input[0])
	}
	parts, _ := user0["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("user 内容块=%v", user0["content"])
	}
	if p0, _ := parts[0].(map[string]any); p0["type"] != "input_text" || p0["text"] != "看图" {
		t.Fatalf("文本块=%v", parts[0])
	}
	if p1, _ := parts[1].(map[string]any); p1["type"] != "input_image" || p1["image_url"] != "data:image/png;base64,AA" {
		t.Fatalf("图片块=%v", parts[1])
	}
	reasoningItem, _ := input[1].(map[string]any)
	if reasoningItem["type"] != "reasoning" {
		t.Fatalf("reasoning item=%v", input[1])
	}
	callItem, _ := input[2].(map[string]any)
	if callItem["type"] != "function_call" || callItem["call_id"] != "call_1" || callItem["name"] != "core_file_read" || callItem["arguments"] != `{"path":"a"}` {
		t.Fatalf("function_call item=%v", input[2])
	}
	assistantItem, _ := input[3].(map[string]any)
	if assistantItem["type"] != "message" || assistantItem["role"] != "assistant" {
		t.Fatalf("assistant 正文 item=%v", input[3])
	}
	if ab, _ := assistantItem["content"].([]any); len(ab) != 1 {
		t.Fatalf("assistant 正文块=%v", assistantItem["content"])
	} else if blk, _ := ab[0].(map[string]any); blk["type"] != "output_text" || blk["text"] != "我读一下" {
		t.Fatalf("assistant 正文块=%v want output_text", ab[0])
	}
	outItem, _ := input[4].(map[string]any)
	if outItem["type"] != "function_call_output" || outItem["call_id"] != "call_1" || outItem["output"] != "文件内容" {
		t.Fatalf("function_call_output item=%v", input[4])
	}
}

// TestResponsesStreamMapping：语义化 SSE → canonical 事件（reasoning / text / 按 item_id 聚合的
// tool_call / usage / done）；`response.completed` 收尾，有工具调用 → finish=tool_calls。
func TestResponsesStreamMapping(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		playChunks(t, w,
			sse(`{"type":"response.created","response":{"model":"ds-x"}}`),
			sse(`{"type":"response.reasoning_text.delta","delta":"想一下"}`),
			sse(`{"type":"response.output_text.delta","delta":"你"}`),
			sse(`{"type":"response.output_text.delta","delta":"好"}`),
			sse(`{"type":"response.output_item.added","item":{"type":"function_call","id":"item_a","call_id":"call_a","name":"core_file_read","arguments":""}}`),
			sse(`{"type":"response.function_call_arguments.delta","item_id":"item_a","delta":"{\"path\":"}`),
			sse(`{"type":"response.function_call_arguments.delta","item_id":"item_a","delta":"\"a.txt\"}"}`),
			sse(`{"type":"response.function_call_arguments.done","item_id":"item_a","arguments":"{\"path\":\"a.txt\"}"}`),
			sse(`{"type":"response.output_item.done","item":{"type":"function_call","id":"item_a","call_id":"call_a","name":"core_file_read","arguments":"{\"path\":\"a.txt\"}"}}`),
			sse(`{"type":"response.completed","response":{"status":"completed","model":"ds-x","usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":3}}}}`),
		)
	})

	evs, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvReasoningDelta, canon.EvTextDelta, canon.EvTextDelta, canon.EvToolCall, canon.EvUsage, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	if evs[0].Text != "想一下" || evs[0].Model != "ds-x" {
		t.Fatalf("reasoning 事件=%+v", evs[0])
	}
	if evs[1].Text != "你" || evs[2].Text != "好" {
		t.Fatalf("文本事件=%+v", evs[1:3])
	}
	tc := evs[3].ToolCallFull
	if tc == nil || tc.Index != 0 || tc.ID != "call_a" || tc.Name != "core_file_read" || tc.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("tool_call 事件=%+v（item_id → Index 派生 + 完整参数）", tc)
	}
	if u := evs[4].Usage; u == nil || u.PromptTokens != 9 || u.CompletionTokens != 4 || u.TotalTokens != 13 || u.ReasoningTokens != 3 || u.CachedTokens != 2 {
		t.Fatalf("usage=%+v want {9,4,13,reasoning 3,cached 2}", evs[4].Usage)
	}
	if evs[5].FinishReason != "tool_calls" {
		t.Fatalf("done.FinishReason=%q want tool_calls", evs[5].FinishReason)
	}
}

// TestResponsesUsageMissingFields：usage **部分字段缺失**（LR-9）：无 total → 求和；无 details → 0。
func TestResponsesUsageMissingFields(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		playChunks(t, w, sse(`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":2}}}`))
	})
	evs, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvUsage, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	if u := evs[0].Usage; u == nil || u.PromptTokens != 7 || u.CompletionTokens != 2 || u.TotalTokens != 9 || u.ReasoningTokens != 0 || u.CachedTokens != 0 {
		t.Fatalf("usage=%+v want {7,2,9,0,0}（total 求和、缺失即 0）", evs[0].Usage)
	}
}

// TestResponsesIncompleteIsLength：`response.incomplete{max_output_tokens}` → done{length}
// （触发 llm 侧既有自动续写）。
func TestResponsesIncompleteIsLength(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		playChunks(t, w, sse(`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`))
	})
	evs, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(evs) != 1 || evs[0].Type != canon.EvDone || evs[0].FinishReason != "length" {
		t.Fatalf("事件=%+v want done{length}", evs)
	}
}

// TestResponsesFailedIsClassified：`response.failed`（HTTP 200 + 流内失败）→ 按 error.code 分类，
// 且不投 done。
func TestResponsesFailedIsClassified(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		playChunks(t, w,
			sse(`{"type":"response.output_text.delta","delta":"半"}`),
			sse(`{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"too many"}}}`),
		)
	})
	evs, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorRateLimit || !e.Retryable {
		t.Fatalf("err=%+v want rate_limit/可重试", err)
	}
	if !strings.Contains(e.Message, "too many") {
		t.Fatalf("信息串=%q", e.Message)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta}) {
		t.Fatalf("事件=%v want 已投的 text_delta（无 done）", eventTypes(evs))
	}
}

// TestResponsesEOFWithoutTerminalEvent：EOF 落在事件边界但**未收到终态事件** → network（可重试）——
// 不静默当成功（Responses 无 `[DONE]`，终态只能靠 completed / incomplete / failed 判定）。
func TestResponsesEOFWithoutTerminalEvent(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		playChunks(t, w, sse(`{"type":"response.output_text.delta","delta":"半"}`))
	})
	evs, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorNetwork || !e.Retryable {
		t.Fatalf("err=%+v want network/可重试", err)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta}) {
		t.Fatalf("事件=%v want 仅 text_delta（无 done）", eventTypes(evs))
	}
}

// TestResponsesDoneCompatAndNoise：兼容实现仍发 `[DONE]` → 视作流结束；非 JSON 噪声载荷跳过不断流。
func TestResponsesDoneCompatAndNoise(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		playChunks(t, w,
			"data: keep-alive\n\n",
			sse(`{"type":"response.output_text.delta","delta":"hi"}`),
			"data: [DONE]\n\n",
		)
	})
	evs, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta, canon.EvDone}) {
		t.Fatalf("事件序列=%v want [text_delta, done]", eventTypes(evs))
	}
	if evs[1].FinishReason != "stop" {
		t.Fatalf("done.FinishReason=%q want stop", evs[1].FinishReason)
	}
}

// TestResponsesStopsAtCompletedWithoutEOF：`response.completed` 即终止 —— 上游**不关闭** keep-alive
// 连接时也必须立即收尾（不等 EOF/`streamTimeout`），见 `adaptor.ErrStreamDone`。
func TestResponsesStopsAtCompletedWithoutEOF(t *testing.T) {
	release := make(chan struct{})
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, sse(`{"type":"response.output_text.delta","delta":"hi"}`))
		flusher.Flush()
		_, _ = io.WriteString(w, sse(`{"type":"response.completed","response":{"status":"completed"}}`))
		flusher.Flush()
		<-release // 保持连接打开
	})
	t.Cleanup(func() { close(release) }) // LIFO：先解阻塞再关服务

	req := userReq("q")
	req.Options.StreamTimeout = time.Second
	evs, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), req)
	if err != nil {
		t.Fatalf("Stream err=%v want nil（completed 应立即收尾）", err)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta, canon.EvDone}) {
		t.Fatalf("事件序列=%v want [text_delta, done]", eventTypes(evs))
	}
}

// TestResponsesValidation：BaseURL 为空 / 无任何可发送内容 → invalid（不发请求）。
func TestResponsesValidation(t *testing.T) {
	a := NewResponsesAdapter(Options{})
	_, err := runResponsesStream(t, a, canon.Spec{Name: "ds", Protocol: canon.ProtocolResponses}, userReq("q"))
	if e, ok := err.(*canon.Error); !ok || e.Kind != canon.ErrorInvalid {
		t.Fatalf("空 BaseURL err=%+v want invalid", err)
	}
	_, err = runResponsesStream(t, a, canon.Spec{Name: "ds", Protocol: canon.ProtocolResponses, BaseURL: "http://127.0.0.1:1/v1"}, canon.Request{})
	if e, ok := err.(*canon.Error); !ok || e.Kind != canon.ErrorInvalid {
		t.Fatalf("空消息 err=%+v want invalid", err)
	}
}

// TestResponsesHTTPErrorClassification：非 2xx → 分类错误（含 Retry-After）。
func TestResponsesHTTPErrorClassification(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
	})
	_, err := runResponsesStream(t, NewResponsesAdapter(Options{}), baseResponsesSpec(srv), userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorRateLimit || e.Status != 429 || e.RetryAfter != 2*time.Second {
		t.Fatalf("err=%+v want rate_limit/429/2s", err)
	}
}
