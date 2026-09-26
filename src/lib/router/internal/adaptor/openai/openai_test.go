// LR-3 白盒：OpenAI 兼容适配器（openai）——请求编码、流式事件映射、OpenAI 兼容端点的已知差异坑。
// 流式用例由 LR-5 金样本 fixture（`internal/adaptor/testdata/*.sse.json`）驱动，经 httptest 原样回放分片。
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-router/internal/adaptor"
	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// loadFixture 按名取 LR-5 金样本素材（缺 → 直接失败）。
func loadFixture(t *testing.T, name string) *adaptor.Fixture {
	t.Helper()
	fixtures, err := adaptor.LoadDir("../testdata")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	for _, f := range fixtures {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("缺素材 %s", name)
	return nil
}

// newEndpoint 起假 LLM 端点：记录请求（path / 头 / 体）并按 respond 回放响应。
func newEndpoint(t *testing.T, req *recordedRequest, respond func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req.path = r.URL.Path
		req.auth = r.Header.Get("Authorization")
		req.contentType = r.Header.Get("Content-Type")
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		req.body = decoded
		respond(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// recordedRequest 是假端点记录到的一次请求。
type recordedRequest struct {
	path        string
	auth        string
	contentType string
	body        map[string]any
}

// playFixture 把素材分片原样写入响应（SSE；逐片 flush，末片后连接关闭 → 客户端见 EOF）。
func playFixture(t *testing.T, w http.ResponseWriter, fx *adaptor.Fixture) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("响应不支持 flush")
	}
	for _, chunk := range fx.Chunks {
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Fatalf("写分片: %v", err)
		}
		flusher.Flush()
	}
}

// runStream 跑一次 Stream 并收集事件（事件按序、Stream 返回后通道关闭）。
func runStream(t *testing.T, a *Adapter, spec canon.Spec, req canon.Request) ([]canon.Event, error) {
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

// eventTypes 取事件类型序列（便于断言顺序）。
func eventTypes(evs []canon.Event) []canon.EventType {
	out := make([]canon.EventType, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

// baseSpec 是给假端点用的 provider 配置。
func baseSpec(srv *httptest.Server) canon.Spec {
	return canon.Spec{Name: "oai", Protocol: canon.ProtocolOpenAI, BaseURL: srv.URL + "/v1/", APIKey: "sk-1", DefaultModel: "gpt-default"}
}

// userReq 构造一个最小请求（一条 user 文本消息）。
func userReq(text string) canon.Request {
	return canon.Request{Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: text}}}}}
}

// TestCaps：OpenAI 兼容协议的能力声明（LR-7 据此降级）。
func TestCaps(t *testing.T) {
	caps := New(Options{}).Caps()
	if caps != (canon.Caps{
		Stream: true, Tools: true, ToolChoice: true, ParallelToolCalls: true,
		Reasoning: true, ReasoningEffort: true, Images: true, TopP: true,
		MaxTokensRequired: false, UsageInStream: true, SystemRole: true,
	}) {
		t.Fatalf("Caps=%+v", caps)
	}
	if got := New(Options{}).Protocol(); got != canon.ProtocolOpenAI {
		t.Fatalf("Protocol=%q want %q", got, canon.ProtocolOpenAI)
	}
}

// TestRequestEncoding：请求组装 —— messages / tools[].function.parameters / stream /
// stream_options.include_usage / tool_choice / temperature / reasoning_effort / 鉴权头 / 端点路径。
func TestRequestEncoding(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_no_done")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	temp := 0.3
	maxTokens := 64
	req := canon.Request{
		Messages: []canon.Message{
			{Role: canon.RoleSystem, Kind: "text", Content: []canon.Part{{Type: canon.PartText, Text: "sys"}}},
			{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "看图"}, {Type: canon.PartImage, Image: &canon.ImagePart{MIME: "image/png", DataURL: "data:image/png;base64,AA"}}}},
			{Role: canon.RoleAssistant, ToolCalls: []canon.ToolCall{{ID: "call_1", Name: "core_file_read", Arguments: `{"path":"a"}`}}, Reasoning: "想过"},
			{Role: canon.RoleTool, ToolCallID: "call_1", Content: []canon.Part{{Type: canon.PartText, Text: "内容"}}},
		},
		Tools:   []canon.ToolDef{{Name: "core_file_read", Description: "读文件", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}, {Name: "no_schema"}},
		Options: canon.CallOptions{Model: "gpt-x", Temperature: &temp, MaxTokens: &maxTokens, Reasoning: &canon.Reasoning{Effort: "high"}},
	}
	evs, err := runStream(t, New(Options{}), baseSpec(srv), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("事件为空")
	}

	if got.path != "/v1/chat/completions" {
		t.Fatalf("请求路径=%q want /v1/chat/completions（base 尾斜杠已容忍）", got.path)
	}
	if got.auth != "Bearer sk-1" {
		t.Fatalf("Authorization=%q want Bearer sk-1", got.auth)
	}
	if got.contentType != "application/json" {
		t.Fatalf("Content-Type=%q", got.contentType)
	}
	if got.body["model"] != "gpt-x" || got.body["stream"] != true {
		t.Fatalf("model/stream=%v/%v", got.body["model"], got.body["stream"])
	}
	so, ok := got.body["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options=%v want {include_usage:true}", got.body["stream_options"])
	}
	if got.body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice=%v want auto", got.body["tool_choice"])
	}
	if got.body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%v want high", got.body["reasoning_effort"])
	}
	if got.body["temperature"] != temp {
		t.Fatalf("temperature=%v want %v", got.body["temperature"], temp)
	}
	if _, ok := got.body["max_tokens"]; !ok {
		t.Fatal("gpt-x 应下发 max_tokens")
	}
	if _, ok := got.body["max_completion_tokens"]; ok {
		t.Fatal("gpt-x 不应下发 max_completion_tokens")
	}

	msgs, _ := got.body["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages 条数=%d want 4", len(msgs))
	}
	m0, _ := msgs[0].(map[string]any)
	if m0["role"] != "system" || m0["content"] != "sys" || m0["kind"] != "text" {
		t.Fatalf("system 消息=%v", m0)
	}
	m1, _ := msgs[1].(map[string]any)
	parts, _ := m1["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("多模态 content=%v want 两块", m1["content"])
	}
	if p0, _ := parts[0].(map[string]any); p0["type"] != "text" || p0["text"] != "看图" {
		t.Fatalf("文本块=%v", parts[0])
	}
	if p1, _ := parts[1].(map[string]any); p1["type"] != "image_url" {
		t.Fatalf("图片块=%v", parts[1])
	} else if iu, _ := p1["image_url"].(map[string]any); iu["url"] != "data:image/png;base64,AA" {
		t.Fatalf("图片 url=%v", p1["image_url"])
	}
	m2, _ := msgs[2].(map[string]any)
	if m2["role"] != "assistant" || m2["reasoning_content"] != "想过" {
		t.Fatalf("assistant 消息=%v（reasoning → reasoning_content）", m2)
	}
	calls, _ := m2["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls=%v", m2["tool_calls"])
	}
	if c0, _ := calls[0].(map[string]any); c0["id"] != "call_1" || c0["type"] != "function" {
		t.Fatalf("tool_call=%v", calls[0])
	}
	m3, _ := msgs[3].(map[string]any)
	if m3["role"] != "tool" || m3["tool_call_id"] != "call_1" || m3["content"] != "内容" {
		t.Fatalf("tool 消息=%v", m3)
	}

	tools, _ := got.body["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools 条数=%d want 2", len(tools))
	}
	fn0, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn0["name"] != "core_file_read" || fn0["description"] != "读文件" {
		t.Fatalf("tool[0]=%v", tools[0])
	}
	if params, _ := fn0["parameters"].(map[string]any); params["type"] != "object" {
		t.Fatalf("parameters=%v want 原样透传", fn0["parameters"])
	}
	fn1, _ := tools[1].(map[string]any)["function"].(map[string]any)
	if params, _ := fn1["parameters"].(map[string]any); params["type"] != "object" {
		t.Fatalf("缺省 schema=%v want 空对象 schema", fn1["parameters"])
	}
	if _, ok := fn1["description"]; ok {
		t.Fatalf("空 description 不应下发：%v", fn1)
	}
}

// TestMaxTokensParamSelection：`max_tokens` vs `max_completion_tokens` 按模型择一（含显式指定）。
func TestMaxTokensParamSelection(t *testing.T) {
	fx := loadFixture(t, "openai_no_done")
	maxTokens := 8
	cases := []struct {
		name     string
		model    string
		override string
		wantKey  string
	}{
		{"兼容模型", "gpt-4o", "", "max_tokens"},
		{"空模型", "", "", "max_tokens"},
		{"o 系", "o3-mini", "", "max_completion_tokens"},
		{"gpt-5 系", "gpt-5-codex", "", "max_completion_tokens"},
		{"显式老口径", "o3-mini", MaxTokensParamLegacy, "max_tokens"},
		{"显式新口径", "gpt-4o", MaxTokensParamCompletion, "max_completion_tokens"},
	}
	for _, c := range cases {
		var got recordedRequest
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
		req := userReq("hi")
		req.Options = canon.CallOptions{Model: c.model, MaxTokens: &maxTokens}
		if _, err := runStream(t, New(Options{MaxTokensParam: c.override}), baseSpec(srv), req); err != nil {
			t.Fatalf("%s: Stream: %v", c.name, err)
		}
		if _, ok := got.body[c.wantKey]; !ok {
			t.Fatalf("%s: 缺 %s（body=%v）", c.name, c.wantKey, got.body)
		}
		other := "max_tokens"
		if c.wantKey == "max_tokens" {
			other = "max_completion_tokens"
		}
		if _, ok := got.body[other]; ok {
			t.Fatalf("%s: 不应同时下发 %s（body=%v）", c.name, other, got.body)
		}
	}
}

// TestStreamTextReasoningUsageDone：文本 + reasoning + usage（末片）+ `[DONE]` 的事件序列。
func TestStreamTextReasoningUsageDone(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_text_reason_usage_done")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvReasoningDelta, canon.EvTextDelta, canon.EvTextDelta, canon.EvUsage, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	if evs[0].Text != "想一下" || evs[0].Model != "gpt-default" {
		t.Fatalf("reasoning 事件=%+v", evs[0])
	}
	if evs[1].Text != "你" || evs[2].Text != "好" {
		t.Fatalf("文本事件=%+v", evs[1:3])
	}
	if u := evs[3].Usage; u == nil || u.PromptTokens != 3 || u.CompletionTokens != 2 || u.TotalTokens != 5 {
		t.Fatalf("usage 事件=%+v", evs[3].Usage)
	}
	if evs[4].FinishReason != "stop" {
		t.Fatalf("done.FinishReason=%q want stop", evs[4].FinishReason)
	}
}

// TestStreamNoDoneEndsAtEOF：**无 `[DONE]` 的流**以 EOF 收尾 —— 事件照投（含 usage、done）。
func TestStreamNoDoneEndsAtEOF(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_no_done")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvTextDelta, canon.EvTextDelta, canon.EvUsage, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	if evs[3].FinishReason != "length" {
		t.Fatalf("done.FinishReason=%q want length", evs[3].FinishReason)
	}
}

// TestStreamToolCallsByIndex：LR-6 集成 —— 多 index 并行 tool_call 增量按 index 拼装，终态以
// `tool_call`（D-32：完整值）给出 `ToolCall{ID,Name,Arguments,Index}`；片段级事件不发。
func TestStreamToolCallsByIndex(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_toolcall_index")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvToolCall, canon.EvToolCall, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	c0, c1 := evs[0].ToolCallFull, evs[1].ToolCallFull
	if c0 == nil || c0.Index != 0 || c0.ID != "call_a" || c0.Name != "core_file_read" || c0.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("index=0 事件=%+v", c0)
	}
	if c1 == nil || c1.Index != 1 || c1.ID != "call_b" || c1.Name != "core_file_write" || c1.Arguments != `{"path":"b.txt"}` {
		t.Fatalf("index=1 事件=%+v", c1)
	}
	if evs[2].FinishReason != "tool_calls" {
		t.Fatalf("done.FinishReason=%q want tool_calls", evs[2].FinishReason)
	}
}

// TestStreamTruncatedIsNetwork：流在事件边界前中断（半截事件）→ network（可重试），且不投 done。
func TestStreamTruncatedIsNetwork(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_stream_truncated")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err == nil {
		t.Fatal("半截事件应报错")
	}
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorNetwork || !e.Retryable {
		t.Fatalf("err=%+v want network/可重试", err)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta}) {
		t.Fatalf("事件=%v want 仅 text_delta（无 done）", eventTypes(evs))
	}
}

// TestStreamNonEventStreamBody：兼容端点忽略 `stream=true` 回单发 JSON（Content-Type=application/json）
// → 整体解析（choices[].message）后照投事件。
func TestStreamNonEventStreamBody(t *testing.T) {
	var got recordedRequest
	body := `{"model":"gpt-x","choices":[{"message":{"content":"hi","tool_calls":[{"id":"call_9","type":"function","function":{"name":"core_file_read","arguments":"{\"path\":\"a\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})

	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvTextDelta, canon.EvToolCall, canon.EvUsage, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	if evs[0].Text != "hi" || evs[0].Model != "gpt-x" {
		t.Fatalf("文本事件=%+v", evs[0])
	}
	if tc := evs[1].ToolCallFull; tc == nil || tc.Index != 0 || tc.ID != "call_9" || tc.Arguments != `{"path":"a"}` {
		t.Fatalf("tool_call 事件=%+v", evs[1].ToolCallFull)
	}
	if evs[2].Usage == nil || evs[2].Usage.TotalTokens != 3 {
		t.Fatalf("usage 事件=%+v", evs[2].Usage)
	}
	if evs[3].FinishReason != "tool_calls" {
		t.Fatalf("done=%+v", evs[3])
	}
}

// TestHTTPErrorClassification：非 2xx → 分类错误（429 + Retry-After / 5xx + 非 JSON 错误体）。
func TestHTTPErrorClassification(t *testing.T) {
	var got recordedRequest
	t.Run("429 Retry-After", func(t *testing.T) {
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
		})
		_, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
		e, ok := err.(*canon.Error)
		if !ok || e.Kind != canon.ErrorRateLimit || e.Status != 429 || !e.Retryable || e.RetryAfter != 3*time.Second {
			t.Fatalf("err=%+v want rate_limit/429/可重试/3s", err)
		}
		if e.Message != "llm http 429: slow down" {
			t.Fatalf("信息串=%q", e.Message)
		}
	})
	t.Run("502 非 JSON 体", func(t *testing.T) {
		fx := loadFixture(t, "error_body_nonjson")
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, fx.Body)
		})
		_, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
		e, ok := err.(*canon.Error)
		if !ok || e.Kind != canon.ErrorServer || e.Status != 502 {
			t.Fatalf("err=%+v want server/502", err)
		}
		if !strings.Contains(e.Message, "502 Bad Gateway") {
			t.Fatalf("非 JSON 体信息串=%q", e.Message)
		}
	})
}

// TestStreamErrorObjectInPayload：流内错误对象（`{"error":{…}}`）→ 分类错误（不当作成功）。
func TestStreamErrorObjectInPayload(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"半\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"error\":{\"type\":\"rate_limit_exceeded\",\"message\":\"too many\"}}\n\n")
	})
	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorRateLimit {
		t.Fatalf("err=%+v want rate_limit", err)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta}) {
		t.Fatalf("事件=%v want 已投的 text_delta（无 done）", eventTypes(evs))
	}
}

// TestStreamSkipsNonJSONNoise：data 行里的非 JSON 噪声载荷跳过（不断流）。
func TestStreamSkipsNonJSONNoise(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_no_done")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: keep-alive\n\n")
		playFixture(t, w, fx)
	})
	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if evs[len(evs)-1].Type != canon.EvDone {
		t.Fatalf("事件=%v want 以 done 收尾", eventTypes(evs))
	}
}

// TestStreamMissingBaseURL：BaseURL 为空 → invalid（不发请求）。
func TestStreamMissingBaseURL(t *testing.T) {
	spec := canon.Spec{Name: "oai", Protocol: canon.ProtocolOpenAI, DefaultModel: "m"}
	_, err := runStream(t, New(Options{}), spec, userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorInvalid {
		t.Fatalf("err=%+v want invalid", err)
	}
}

// TestStreamContextCanceled：消费方取消 → 立即返回（不阻塞、不投 done）。
func TestStreamContextCanceled(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_no_done")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := make(chan canon.Event, 8)
	err := New(Options{}).Stream(ctx, baseSpec(srv), userReq("q"), out)
	if err == nil {
		t.Fatal("已取消的 ctx 应返回错误")
	}
	if len(out) != 0 {
		t.Fatalf("取消后不应投事件，实得 %d", len(out))
	}
}

// TestStreamReasoningFieldAliases：思考链字段名兼容 —— `reasoning` / `thinking` 别名同样映射为
// reasoning_delta（各写各的兼容端点）。
func TestStreamReasoningFieldAliases(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"甲乙\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"thinking\":\"丙丁\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"正文\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvReasoningDelta, canon.EvReasoningDelta, canon.EvTextDelta, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	if evs[0].Text != "甲乙" || evs[1].Text != "丙丁" || evs[2].Text != "正文" {
		t.Fatalf("事件内容=%+v", evs[:3])
	}
}

// TestStreamTimeouts：两级超时 —— 首字节超时（ResponseTimeout）/ 流 chunk 间隔超时（StreamTimeout）
// 均归 timeout（可重试），且不投 done。
func TestStreamTimeouts(t *testing.T) {
	var got recordedRequest
	t.Run("首字节超时", func(t *testing.T) {
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
			time.Sleep(400 * time.Millisecond)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		})
		req := userReq("q")
		req.Options.ResponseTimeout = 60 * time.Millisecond
		evs, err := runStream(t, New(Options{}), baseSpec(srv), req)
		e, ok := err.(*canon.Error)
		if !ok || e.Kind != canon.ErrorTimeout || !e.Retryable {
			t.Fatalf("err=%+v want timeout/可重试", err)
		}
		if !strings.Contains(e.Message, "首字节超时") {
			t.Fatalf("信息串=%q", e.Message)
		}
		if len(evs) != 0 {
			t.Fatalf("首字节超时不应投事件：%v", eventTypes(evs))
		}
	})
	t.Run("流间隔超时", func(t *testing.T) {
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"首片\"}}]}\n\n")
			flusher.Flush()
			time.Sleep(400 * time.Millisecond) // 超过 StreamTimeout → 触发间隔超时
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"后片\"}}]}\n\n")
			flusher.Flush()
		})
		req := userReq("q")
		req.Options.StreamTimeout = 60 * time.Millisecond
		evs, err := runStream(t, New(Options{}), baseSpec(srv), req)
		e, ok := err.(*canon.Error)
		if !ok || e.Kind != canon.ErrorTimeout || !e.Retryable {
			t.Fatalf("err=%+v want timeout/可重试", err)
		}
		if !strings.Contains(e.Message, "流 chunk 间隔超时") {
			t.Fatalf("信息串=%q", e.Message)
		}
		if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta}) {
			t.Fatalf("事件=%v want 仅首片 text_delta（无 done）", eventTypes(evs))
		}
	})
}

// TestStreamStopsAtDoneWithoutEOF：`[DONE]` 即终止 —— 上游**不关闭** keep-alive 连接（本仓 L4
// `mock_llm.py` 等测试桩形态）时也必须立即收尾，不得等到 `streamTimeout` 才报超时
// （旧 llm 侧按行解析、命中终止标记即收尾 → 属行为等价性要求，见 `adaptor.ErrStreamDone`）。
func TestStreamStopsAtDoneWithoutEOF(t *testing.T) {
	release := make(chan struct{})
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
		<-release // 保持连接打开：客户端收不到 EOF
	})
	t.Cleanup(func() { close(release) }) // 先于 srv.Close 执行（LIFO）：先解阻塞再关服务

	req := userReq("hi")
	req.Options.StreamTimeout = time.Second // 若仍等 EOF，此处 1s 后必报 timeout
	evs, err := runStream(t, New(Options{}), baseSpec(srv), req)
	if err != nil {
		t.Fatalf("Stream err=%v want nil（[DONE] 应立即收尾，不等 EOF）", err)
	}
	if got := eventTypes(evs); !equalTypes(got, []canon.EventType{canon.EvTextDelta, canon.EvDone}) {
		t.Fatalf("事件=%v want [text_delta done]", got)
	}
}

// TestStreamMissingDoneAndFinishIsNetwork：流在事件边界上结束（EOF）但**既无 `[DONE]` 也无
// `finish_reason`** → 判为断链（network，可重试）、不投 done（对齐 llm 侧既有口径：未收到
// finish_reason = 半截流 → 断链续写，见 35-错误处理与恢复 §S6/S8）。L4 的 `mock_llm.py`
// 「call break」（部分文本后 RST，客户端见 EOF）即此形态。
func TestStreamMissingDoneAndFinishIsNetwork(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"role":"assistant","content":"PARTIAL-TEXT-"},"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		// 直接返回 → 连接关闭（EOF）；无 [DONE]、无 finish_reason
	})

	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err == nil {
		t.Fatal("缺 [DONE] 与 finish_reason 的流应报断链错误")
	}
	var ce *canon.Error
	if !errors.As(err, &ce) || ce.Kind != canon.ErrorNetwork {
		t.Fatalf("err=%v want Kind=network", err)
	}
	if got := eventTypes(evs); !equalTypes(got, []canon.EventType{canon.EvTextDelta}) {
		t.Fatalf("事件=%v want 仅 text_delta（不投 done）", got)
	}
}

// TestStreamNoApiKeyOmitsAuth：apiKey 为空 → 不带 Authorization（本地 mock / 无鉴权端点可连）。
func TestStreamNoApiKeyOmitsAuth(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_no_done")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
	spec := baseSpec(srv)
	spec.APIKey = ""
	if _, err := runStream(t, New(Options{}), spec, userReq("q")); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.auth != "" {
		t.Fatalf("Authorization=%q want 空", got.auth)
	}
}

// TestDegradeToolsUnsupported：LR-7 集成 —— 适配器入口先按 caps 降级（本协议 caps.Tools=true，
// 故带工具可通过；硬不支持路径见 adaptor.Degrade 白盒）。
func TestDegradeToolsUnsupported(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "openai_no_done")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
	req := userReq("q")
	req.Tools = []canon.ToolDef{{Name: "t"}}
	if _, err := runStream(t, New(Options{}), baseSpec(srv), req); err != nil {
		t.Fatalf("支持工具的协议不应报错：%v", err)
	}
	if _, ok := got.body["tools"]; !ok {
		t.Fatalf("tools 应下发：%v", got.body)
	}
}

// equalTypes 比较事件类型序列。
func equalTypes(got, want []canon.EventType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
