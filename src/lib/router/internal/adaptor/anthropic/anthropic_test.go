// LR-4 白盒：Anthropic 适配器（anthropic）——请求编码（system 顶层 / content blocks / tool_use /
// tool_result / input_schema / max_tokens / thinking）与流式映射（content_block_delta / input_json_delta /
// message_delta.usage / message_stop）。流式用例由 LR-5 金样本 fixture 驱动。
package anthropic

import (
	"context"
	"encoding/json"
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

// recordedRequest 是假端点记录到的一次请求。
type recordedRequest struct {
	path     string
	apiKey   string
	version  string
	sseGiven bool
	body     map[string]any
}

// newEndpoint 起假 Anthropic 端点：记录请求并按 respond 回放响应。
func newEndpoint(t *testing.T, req *recordedRequest, respond func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req.path = r.URL.Path
		req.apiKey = r.Header.Get("x-api-key")
		req.version = r.Header.Get("anthropic-version")
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		req.body = decoded
		respond(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// playFixture 把素材分片原样写入响应（SSE；逐片 flush）。
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

// runStream 跑一次 Stream 并收集事件。
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

// eventTypes 取事件类型序列。
func eventTypes(evs []canon.Event) []canon.EventType {
	out := make([]canon.EventType, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
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

// baseSpec 是给假端点用的 provider 配置（base 含版本段）。
func baseSpec(srv *httptest.Server) canon.Spec {
	return canon.Spec{Name: "ant", Protocol: canon.ProtocolAnthropic, BaseURL: srv.URL + "/v1", APIKey: "ak-1", DefaultModel: "claude-default"}
}

// userReq 构造一个最小请求（一条 user 文本消息）。
func userReq(text string) canon.Request {
	return canon.Request{Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: text}}}}}
}

// TestCaps：Anthropic 协议的能力声明（max_tokens 必填 / thinking / 流内 usage）。
func TestCaps(t *testing.T) {
	caps := New(Options{}).Caps()
	if !caps.Stream || !caps.Tools || !caps.Reasoning || !caps.ReasoningEffort || !caps.Images || !caps.MaxTokensRequired || !caps.UsageInStream || !caps.SystemRole {
		t.Fatalf("Caps=%+v want 流式/工具/思考/思考档位/图片/max_tokens 必填/流内 usage/独立 system", caps)
	}
	if got := New(Options{}).Protocol(); got != canon.ProtocolAnthropic {
		t.Fatalf("Protocol=%q", got)
	}
}

// TestRequestEncoding：system 顶层 + content blocks（text/image/tool_use/tool_result）+ input_schema +
// max_tokens + thinking + 头 + 端点路径（base 含版本段 → 只补 /messages）。
func TestRequestEncoding(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "anthropic_text_tool_thinking")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	temp, topP := 0.3, 0.9
	maxTokens := 2048
	req := canon.Request{
		Messages: []canon.Message{
			{Role: canon.RoleSystem, Content: []canon.Part{{Type: canon.PartText, Text: "系统一"}}},
			{Role: canon.RoleSystem, Content: []canon.Part{{Type: canon.PartText, Text: "系统二"}}},
			{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "看图"}, {Type: canon.PartImage, Image: &canon.ImagePart{DataURL: "data:image/png;base64,AA"}}}},
			{Role: canon.RoleAssistant, Reasoning: "不该回传", Content: []canon.Part{{Type: canon.PartText, Text: "我读一下"}}, ToolCalls: []canon.ToolCall{{ID: "toolu_1", Name: "core_file_read", Arguments: `{"path":"a"}`}}},
			{Role: canon.RoleTool, ToolCallID: "toolu_1", Content: []canon.Part{{Type: canon.PartText, Text: "文件内容"}}},
			{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "继续"}}},
		},
		Tools:   []canon.ToolDef{{Name: "core_file_read", Description: "读文件", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		Options: canon.CallOptions{Model: "claude-x", Temperature: &temp, TopP: &topP, MaxTokens: &maxTokens},
	}
	if _, err := runStream(t, New(Options{}), baseSpec(srv), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if got.path != "/v1/messages" {
		t.Fatalf("请求路径=%q want /v1/messages", got.path)
	}
	if got.apiKey != "ak-1" || got.version != apiVersion {
		t.Fatalf("头：x-api-key=%q anthropic-version=%q", got.apiKey, got.version)
	}
	if got.body["model"] != "claude-x" || got.body["stream"] != true || got.body["max_tokens"] != float64(2048) {
		t.Fatalf("model/stream/max_tokens=%v/%v/%v", got.body["model"], got.body["stream"], got.body["max_tokens"])
	}
	if got.body["system"] != "系统一\n\n系统二" {
		t.Fatalf("system=%q want 多段拼接", got.body["system"])
	}
	if got.body["temperature"] != temp || got.body["top_p"] != topP {
		t.Fatalf("temperature/top_p=%v/%v", got.body["temperature"], got.body["top_p"])
	}
	tc, _ := got.body["tool_choice"].(map[string]any)
	if tc["type"] != "auto" {
		t.Fatalf("tool_choice=%v", got.body["tool_choice"])
	}
	tools, _ := got.body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v", got.body["tools"])
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["name"] != "core_file_read" || tool0["description"] != "读文件" {
		t.Fatalf("tool[0]=%v", tool0)
	}
	if schema, _ := tool0["input_schema"].(map[string]any); schema["type"] != "object" {
		t.Fatalf("input_schema=%v", tool0["input_schema"])
	}

	msgs, _ := got.body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages 条数=%d want 3（user+assistant+tool_result 合并后的 user）", len(msgs))
	}
	m0, _ := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Fatalf("首条角色=%v want user", m0["role"])
	}
	b0, _ := m0["content"].([]any)
	if len(b0) != 2 {
		t.Fatalf("首条内容块=%v want text+image", m0["content"])
	}
	if blk, _ := b0[0].(map[string]any); blk["type"] != "text" || blk["text"] != "看图" {
		t.Fatalf("文本块=%v", b0[0])
	}
	img, _ := b0[1].(map[string]any)
	if img["type"] != "image" {
		t.Fatalf("图片块=%v", b0[1])
	}
	src, _ := img["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "AA" {
		t.Fatalf("image.source=%v", img["source"])
	}

	m1, _ := msgs[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Fatalf("第二条角色=%v", m1["role"])
	}
	b1, _ := m1["content"].([]any)
	if len(b1) != 2 {
		t.Fatalf("assistant 内容块=%v want text+tool_use", m1["content"])
	}
	tu, _ := b1[1].(map[string]any)
	if tu["type"] != "tool_use" || tu["id"] != "toolu_1" || tu["name"] != "core_file_read" {
		t.Fatalf("tool_use 块=%v", b1[1])
	}
	if input, _ := tu["input"].(map[string]any); input["path"] != "a" {
		t.Fatalf("tool_use.input=%v want 解析后的对象", tu["input"])
	}
	if _, ok := m1["reasoning"]; ok {
		t.Fatalf("思考链不应回传（缺 signature）：%v", m1)
	}

	m2, _ := msgs[2].(map[string]any)
	if m2["role"] != "user" {
		t.Fatalf("tool_result 必须落在 user 消息内，实得角色=%v", m2["role"])
	}
	b2, _ := m2["content"].([]any)
	if len(b2) != 2 {
		t.Fatalf("user 内容块=%v want tool_result+后续 user 文本（相邻同角色合并）", m2["content"])
	}
	tr, _ := b2[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "toolu_1" || tr["content"] != "文件内容" {
		t.Fatalf("tool_result 块=%v", b2[0])
	}
	if tail, _ := b2[1].(map[string]any); tail["text"] != "继续" {
		t.Fatalf("合并后的后续文本=%v", b2[1])
	}
}

// TestThinkingSignatureRoundTrip：D-31 —— 多轮含 thinking 的**往返**：
// 请求侧 `Message.ReasoningSignature` 非空 → 回传 `thinking` 块（`thinking` + `signature`，且位于
// 正文 / tool_use 块**之前**）；签名缺失 → **不回传**（宁可少回传，不让整轮请求被上游 400 拒）。
// 响应侧（`signature_delta` → `done.Signature`）见 `TestStreamFixtureMapping`。
func TestThinkingSignatureRoundTrip(t *testing.T) {
	fx := loadFixture(t, "anthropic_text_tool_thinking")

	assistant := func(sig string) canon.Message {
		return canon.Message{
			Role:               canon.RoleAssistant,
			Reasoning:          "先想",
			ReasoningSignature: sig,
			Content:            []canon.Part{{Type: canon.PartText, Text: "答"}},
			ToolCalls:          []canon.ToolCall{{ID: "toolu_9", Name: "core_file_read", Arguments: `{}`}},
		}
	}
	reqFor := func(sig string) canon.Request {
		return canon.Request{Messages: []canon.Message{
			{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "问"}}},
			assistant(sig),
			{Role: canon.RoleTool, ToolCallID: "toolu_9", Content: []canon.Part{{Type: canon.PartText, Text: "结果"}}},
		}}
	}

	t.Run("带签名回传 thinking 块", func(t *testing.T) {
		var got recordedRequest
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
		evs, err := runStream(t, New(Options{}), baseSpec(srv), reqFor("sig-1"))
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if len(evs) == 0 || evs[len(evs)-1].Type != canon.EvDone {
			t.Fatalf("事件=%v want 以 done 收尾", eventTypes(evs))
		}
		msgs, _ := got.body["messages"].([]any)
		if len(msgs) != 3 {
			t.Fatalf("messages 条数=%d want 3", len(msgs))
		}
		b1, _ := msgs[1].(map[string]any)["content"].([]any)
		if len(b1) != 3 {
			t.Fatalf("assistant 内容块=%v want thinking+text+tool_use", msgs[1])
		}
		th, _ := b1[0].(map[string]any)
		if th["type"] != "thinking" || th["thinking"] != "先想" || th["signature"] != "sig-1" {
			t.Fatalf("thinking 块=%v want {thinking:先想, signature:sig-1}", b1[0])
		}
		if blk, _ := b1[1].(map[string]any); blk["type"] != "text" || blk["text"] != "答" {
			t.Fatalf("正文块=%v want 紧随 thinking 之后", b1[1])
		}
		if blk, _ := b1[2].(map[string]any); blk["type"] != "tool_use" || blk["id"] != "toolu_9" {
			t.Fatalf("tool_use 块=%v", b1[2])
		}
	})

	t.Run("缺签名不回传", func(t *testing.T) {
		var got recordedRequest
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
		if _, err := runStream(t, New(Options{}), baseSpec(srv), reqFor("")); err != nil {
			t.Fatalf("Stream: %v", err)
		}
		msgs, _ := got.body["messages"].([]any)
		b1, _ := msgs[1].(map[string]any)["content"].([]any)
		if len(b1) != 2 {
			t.Fatalf("assistant 内容块=%v want text+tool_use（缺签名 → 不回传 thinking）", msgs[1])
		}
		for _, b := range b1 {
			if blk, _ := b.(map[string]any); blk["type"] == "thinking" {
				t.Fatalf("缺签名不应回传 thinking 块：%v", b1)
			}
		}
	})
}

// TestMaxTokensRequiredDefaults：`max_tokens` 必填 —— 缺值补缺省、非正值同样回落缺省。
func TestMaxTokensRequiredDefaults(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "anthropic_text_tool_thinking")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
	if _, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q")); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.body["max_tokens"] != float64(defaultMaxTokens) {
		t.Fatalf("max_tokens=%v want %d", got.body["max_tokens"], defaultMaxTokens)
	}

	zero := 0
	req := userReq("q")
	req.Options.MaxTokens = &zero
	if _, err := runStream(t, New(Options{}), baseSpec(srv), req); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.body["max_tokens"] != float64(defaultMaxTokens) {
		t.Fatalf("非正 max_tokens 应回落缺省，实得 %v", got.body["max_tokens"])
	}
}

// TestThinkingBlockAndSamplingDrop：Reasoning 非空 → `thinking.budget_tokens`（按档位），且
// **不下发** temperature / top_p（Anthropic 不接受与扩展思考同用）；max_tokens 过小 → 省略 thinking。
func TestThinkingBlockAndSamplingDrop(t *testing.T) {
	fx := loadFixture(t, "anthropic_text_tool_thinking")
	cases := []struct {
		name         string
		effort       string
		maxTokens    int
		wantThinking bool
		wantBudget   int
	}{
		{"high", "high", 16384, true, thinkingBudgetHigh},
		{"low", "low", 16384, true, thinkingBudgetLow},
		{"空档按默认", "", 16384, true, defaultThinkingBudget},
		{"夹到 max_tokens-1", "high", 2048, true, 2047},
		{"max_tokens 过小 → 省略", "low", 1024, false, 0},
	}
	for _, c := range cases {
		var got recordedRequest
		srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
		temp, topP := 0.5, 0.5
		mt := c.maxTokens
		req := userReq("q")
		req.Options = canon.CallOptions{Model: "claude-x", Temperature: &temp, TopP: &topP, MaxTokens: &mt, Reasoning: &canon.Reasoning{Effort: c.effort}}
		if _, err := runStream(t, New(Options{}), baseSpec(srv), req); err != nil {
			t.Fatalf("%s: Stream: %v", c.name, err)
		}
		th, hasThinking := got.body["thinking"].(map[string]any)
		if hasThinking != c.wantThinking {
			t.Fatalf("%s: thinking=%v want %v", c.name, got.body["thinking"], c.wantThinking)
		}
		if c.wantThinking {
			if th["type"] != thinkingEnabled || th["budget_tokens"] != float64(c.wantBudget) {
				t.Fatalf("%s: thinking=%v want {enabled,%d}", c.name, th, c.wantBudget)
			}
			if _, ok := got.body["temperature"]; ok {
				t.Fatalf("%s: 启用 thinking 时不应下发 temperature", c.name)
			}
			if _, ok := got.body["top_p"]; ok {
				t.Fatalf("%s: 启用 thinking 时不应下发 top_p", c.name)
			}
		} else if got.body["temperature"] != temp {
			t.Fatalf("%s: 未启用 thinking 时应照常下发 temperature", c.name)
		}
	}
}

// TestEndpointJoin：base 含版本段 → 只补 /messages；不含 → 补 /v1/messages（尾斜杠容忍）。
func TestEndpointJoin(t *testing.T) {
	cases := map[string]string{
		"https://api.anthropic.com":     "https://api.anthropic.com/v1/messages",
		"https://api.anthropic.com/":    "https://api.anthropic.com/v1/messages",
		"https://api.anthropic.com/v1":  "https://api.anthropic.com/v1/messages",
		"https://api.anthropic.com/v1/": "https://api.anthropic.com/v1/messages",
		"http://127.0.0.1:8080/proxy":   "http://127.0.0.1:8080/proxy/v1/messages",
	}
	for base, want := range cases {
		if got := endpoint(base); got != want {
			t.Fatalf("endpoint(%q)=%q want %q", base, got, want)
		}
	}
}

// TestRequestValidation：无消息 / 首条非 user / 图片 DataURL 非法 / 工具参数非 JSON → 明确报错。
func TestRequestValidation(t *testing.T) {
	a := New(Options{})
	spec := canon.Spec{Name: "ant", Protocol: canon.ProtocolAnthropic, BaseURL: "http://127.0.0.1:1/v1"}

	// 无消息（仅 system）→ invalid。
	_, err := runStream(t, a, spec, canon.Request{Messages: []canon.Message{{Role: canon.RoleSystem, Content: []canon.Part{{Type: canon.PartText, Text: "s"}}}}})
	if e, ok := err.(*canon.Error); !ok || e.Kind != canon.ErrorInvalid {
		t.Fatalf("无消息 err=%+v want invalid", err)
	}

	// 首条非 user → invalid。
	_, err = runStream(t, a, spec, canon.Request{Messages: []canon.Message{{Role: canon.RoleAssistant, Content: []canon.Part{{Type: canon.PartText, Text: "a"}}}}})
	if e, ok := err.(*canon.Error); !ok || e.Kind != canon.ErrorInvalid || !strings.Contains(e.Message, "user") {
		t.Fatalf("首条非 user err=%+v want invalid", err)
	}

	// 图片 DataURL 非法 → invalid。
	_, err = runStream(t, a, spec, canon.Request{Messages: []canon.Message{{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartImage, Image: &canon.ImagePart{DataURL: "http://x/a.png"}}}}}})
	if e, ok := err.(*canon.Error); !ok || e.Kind != canon.ErrorInvalid {
		t.Fatalf("非法 DataURL err=%+v want invalid", err)
	}

	// assistant 工具参数非 JSON → protocol。
	_, err = runStream(t, a, spec, canon.Request{Messages: []canon.Message{
		{Role: canon.RoleUser, Content: []canon.Part{{Type: canon.PartText, Text: "q"}}},
		{Role: canon.RoleAssistant, ToolCalls: []canon.ToolCall{{ID: "t1", Name: "f", Arguments: `{"broken"`}}},
	}})
	if e, ok := err.(*canon.Error); !ok || e.Kind != canon.ErrorProtocol {
		t.Fatalf("非法工具参数 err=%+v want protocol", err)
	}

	// BaseURL 为空 → invalid。
	_, err = runStream(t, a, canon.Spec{Name: "ant", Protocol: canon.ProtocolAnthropic}, userReq("q"))
	if e, ok := err.(*canon.Error); !ok || e.Kind != canon.ErrorInvalid {
		t.Fatalf("空 BaseURL err=%+v want invalid", err)
	}
}

// TestStreamFixtureMapping：素材驱动 —— thinking_delta → reasoning_delta、text_delta、tool_use 的
// input_json_delta 按 content block index 拼装、signature_delta 累加到 done.Signature（D-31）、
// usage 跨事件归并、message_stop 收尾。
func TestStreamFixtureMapping(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "anthropic_text_tool_thinking")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })

	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	want := []canon.EventType{canon.EvReasoningDelta, canon.EvTextDelta, canon.EvToolCall, canon.EvUsage, canon.EvDone}
	if !equalTypes(eventTypes(evs), want) {
		t.Fatalf("事件序列=%v want %v", eventTypes(evs), want)
	}
	if evs[0].Text != "先想" || evs[1].Text != "你好" {
		t.Fatalf("思考/文本事件=%+v", evs[0:2])
	}
	if evs[0].Model != "claude-x" {
		t.Fatalf("model=%q want claude-x（message_start 回显）", evs[0].Model)
	}
	tc := evs[2].ToolCallFull
	if tc == nil || tc.Index != 2 || tc.ID != "toolu_1" || tc.Name != "core_file_read" || tc.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("tool_call 事件=%+v", tc)
	}
	if u := evs[3].Usage; u == nil || u.PromptTokens != 11 || u.CompletionTokens != 7 || u.TotalTokens != 18 || u.CachedTokens != 4 {
		t.Fatalf("usage=%+v want {11,7,18,cached 4}", evs[3].Usage)
	}
	if evs[4].FinishReason != "tool_calls" {
		t.Fatalf("done.FinishReason=%q want tool_calls（tool_use → 归一）", evs[4].FinishReason)
	}
	if evs[4].Signature != "sig-abc" {
		t.Fatalf("done.Signature=%q want sig-abc（signature_delta 累加，D-31）", evs[4].Signature)
	}
}

// TestStreamEOFWithoutMessageStop：EOF 落在事件边界但缺 `message_delta.stop_reason` / `message_stop`
// → 断链（network，可重试），不静默当成功。
func TestStreamEOFWithoutMessageStop(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-x\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"半\"}}\n\n")
	})
	evs, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorNetwork || !e.Retryable {
		t.Fatalf("err=%+v want network/可重试", err)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta}) {
		t.Fatalf("事件=%v want 仅 text_delta（无 done）", eventTypes(evs))
	}
}

// TestStreamErrorObjectInPayload：流内 `error` 事件 → 按 `error.type` 分类（overloaded_error → server）。
func TestStreamErrorObjectInPayload(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n")
	})
	_, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorServer || !e.Retryable {
		t.Fatalf("err=%+v want server/可重试", err)
	}
}

// TestHTTPErrorClassification：非 2xx → 分类错误（401 auth / 429 + Retry-After）。
func TestHTTPErrorClassification(t *testing.T) {
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`)
	})
	_, err := runStream(t, New(Options{}), baseSpec(srv), userReq("q"))
	e, ok := err.(*canon.Error)
	if !ok || e.Kind != canon.ErrorAuth || e.Status != 401 || e.Retryable || e.RetryAfter != 5*time.Second {
		t.Fatalf("err=%+v want auth/401/不可重试/5s", err)
	}
	if e.Message != "llm http 401: bad key" {
		t.Fatalf("信息串=%q", e.Message)
	}
}

// TestStreamStopsAtMessageStopWithoutEOF：`message_stop` 即终止 —— 上游**不关闭** keep-alive 连接时
// 也必须立即收尾（不等 EOF/`streamTimeout`），见 `adaptor.ErrStreamDone`。
func TestStreamStopsAtMessageStopWithoutEOF(t *testing.T) {
	release := make(chan struct{})
	var got recordedRequest
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
		<-release // 保持连接打开
	})
	t.Cleanup(func() { close(release) }) // LIFO：先解阻塞再关服务

	req := userReq("q")
	req.Options.StreamTimeout = time.Second
	evs, err := runStream(t, New(Options{}), baseSpec(srv), req)
	if err != nil {
		t.Fatalf("Stream err=%v want nil（message_stop 应立即收尾）", err)
	}
	if !equalTypes(eventTypes(evs), []canon.EventType{canon.EvTextDelta, canon.EvDone}) {
		t.Fatalf("事件序列=%v want [text_delta, done]", eventTypes(evs))
	}
}

// TestStreamNoApiKeyOmitsKey：apiKey 为空 → 不发 x-api-key（无鉴权兼容端点可连）。
func TestStreamNoApiKeyOmitsKey(t *testing.T) {
	var got recordedRequest
	fx := loadFixture(t, "anthropic_text_tool_thinking")
	srv := newEndpoint(t, &got, func(w http.ResponseWriter) { playFixture(t, w, fx) })
	spec := baseSpec(srv)
	spec.APIKey = ""
	if _, err := runStream(t, New(Options{}), spec, userReq("q")); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.apiKey != "" || got.version != apiVersion {
		t.Fatalf("x-api-key=%q anthtopic-version=%q", got.apiKey, got.version)
	}
}
