// P1-1 白盒：usr 配置 llms（provider）在主对话真正生效——
// baseUrl → 请求端点、apiKey → Authorization 头、model → 请求体 model（不再用 name）、
// temperature/maxOutputToken 允许 0 生效（存在性判定）、thinking/reasoningEffort → reasoning_effort
// （thinking=false → 不发送；修正「前端恒发 effort 导致 think=off 被短路」）。
// 未命中 provider → 回落 exe flags 现状（body.model = 传入值、无 Authorization、用 -llm-base）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// registerProviderInstance 注册测试实例并登记数据层连接清理
// （LIFO：data.Reset 先于 TempDir 移除执行，否则 usr 库文件仍被占用）。
func registerProviderInstance(t *testing.T, s *Server) {
	t.Helper()
	registerTestInstance(t, s)
	t.Cleanup(data.Reset)
}

// providerRec 记录 mock LLM 端点收到的请求（body / Authorization / path）。
type providerRec struct {
	mu     sync.Mutex
	bodies []map[string]any
	auths  []string
	paths  []string
}

// newProviderServer 起一个记录请求的 mock LLM 端点（固定文本回复，保证轮次 complete）。
func newProviderServer(t *testing.T) (*providerRec, *httptest.Server) {
	t.Helper()
	rec := &providerRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.auths = append(rec.auths, r.Header.Get("Authorization"))
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		llmSSE(w, []string{sseChunk(map[string]any{"content": "ok"}, ""), sseChunk(map[string]any{}, "stop")})
	}))
	t.Cleanup(srv.Close)
	return rec, srv
}

// first 取第 i 次请求的 (body, Authorization, path)。
func (r *providerRec) first(t *testing.T, i int) (map[string]any, string, string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) <= i {
		t.Fatalf("llm 请求数=%d，want >%d", len(r.bodies), i)
	}
	return r.bodies[i], r.auths[i], r.paths[i]
}

func (r *providerRec) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// saveTestLLMProvider 写一条 usr llms 记录（data-user-config-save，集合整体替换）。
func saveTestLLMProvider(t *testing.T, s *Server, item map[string]any) {
	t.Helper()
	saveTestUserConfig(t, s, map[string]any{"llms": []any{item}})
}

// startTurnLLM 发 session-start（对齐前端 llm-start 拆分载荷：llm=provider name + think/effort）。
func startTurnLLM(t *testing.T, s *Server, session, turn, llmName, think, effort string) {
	t.Helper()
	f := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-" + turn, "instance_id": "ins-test", "session": session, "turn": turn,
		"llm": llmName, "think": think, "effort": effort,
	}))
	v := f.Wait()
	if v.Err() != nil {
		t.Fatalf("session-start error: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("session-start rejected: %+v", res)
	}
}

// sendAndWait 发 text-user 并等到该轮 complete（一次来回（`step`））。
func sendAndWait(t *testing.T, s *Server, session, turn, text string) {
	t.Helper()
	wt := watchTurn(t, s.bus, turn) // 先订阅再触发
	s.bus.Emit(context.Background(), "session-send", jb(map[string]any{
		"instance_id": "ins-test", "session": session, "turn": turn, "type": "text-user", "content": text,
	}))
	evs := wt.waitComplete(10 * time.Second)
	if c := lastComplete(evs); c == nil || c["status"] != "complete" {
		t.Fatalf("轮次未 complete: %+v", evs)
	}
}

// TestLLMProviderConfigApplied（断言 ①②③-on）：配置 provider → 请求打到该 baseUrl、
// 带 Authorization、body.model=provider.model（非 name）、temperature/maxOutputToken 的 0 生效、
// thinking=true → reasoning_effort=provider.reasoningEffort。
func TestLLMProviderConfigApplied(t *testing.T) {
	defRec, defSrv := newProviderServer(t) // exe flag -llm-base（命中 provider 后不应再被请求）
	provRec, provSrv := newProviderServer(t)
	s := newTestServer(t, defSrv)
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{
		"name": "prov1", "protocol": "openai", "baseUrl": provSrv.URL, "apiKey": "sk-test",
		"model": "m1", "temperature": 0, "maxOutputToken": 0, "thinking": true, "reasoningEffort": "high",
	})
	startTurnLLM(t, s, "s-prov1", "t-prov1", "prov1", "on", "high")
	sendAndWait(t, s, "s-prov1", "t-prov1", "hello provider")

	body, auth, path := provRec.first(t, 0)
	if got, _ := body["model"].(string); got != "m1" {
		t.Fatalf("body.model=%v want m1（provider.model，非 name）", body["model"])
	}
	if auth != "Bearer sk-test" {
		t.Fatalf("Authorization=%q want Bearer sk-test", auth)
	}
	if path != "/chat/completions" {
		t.Fatalf("path=%q want /chat/completions", path)
	}
	// temperature/maxTokens：0 亦生效（存在性判定，缺键才不发送）
	if v, ok := body["temperature"]; !ok || v != float64(0) {
		t.Fatalf("temperature=%v ok=%v want 0 已发送", v, ok)
	}
	if v, ok := body["max_tokens"]; !ok || v != float64(0) {
		t.Fatalf("max_tokens=%v ok=%v want 0 已发送", v, ok)
	}
	if got, _ := body["reasoning_effort"].(string); got != "high" {
		t.Fatalf("reasoning_effort=%v want high（provider.reasoningEffort）", body["reasoning_effort"])
	}
	if n := defRec.count(); n != 0 {
		t.Fatalf("-llm-base 端点被请求 %d 次（应只打 provider.baseUrl）", n)
	}
}

// TestLLMProviderThinkingOffNoReasoningEffort（断言 ③-off）：thinking=false → 请求体无
// reasoning_effort（即使请求体带了 effort=high）。
func TestLLMProviderThinkingOffNoReasoningEffort(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{
		"name": "prov-off", "baseUrl": srv.URL, "model": "m1",
		"thinking": false, "reasoningEffort": "high",
	})
	startTurnLLM(t, s, "s-think-off", "t-think-off", "prov-off", "on", "high")
	sendAndWait(t, s, "s-think-off", "t-think-off", "no reasoning")

	body, _, _ := rec.first(t, 0)
	if v, ok := body["reasoning_effort"]; ok {
		t.Fatalf("thinking=false 不应发送 reasoning_effort，实得 %v", v)
	}
}

// TestLLMRequestThinkOffGatesEffort：请求体 think=off（前端当轮关闭思考）→ 不发送
// reasoning_effort（修正「恒发 effort 短路 think」；provider 无 thinking 键时按请求体判定）。
func TestLLMRequestThinkOffGatesEffort(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{
		"name": "prov2", "baseUrl": srv.URL, "model": "m1",
	})
	startTurnLLM(t, s, "s-req-off", "t-req-off", "prov2", "off", "high")
	sendAndWait(t, s, "s-req-off", "t-req-off", "think off")

	body, _, _ := rec.first(t, 0)
	if v, ok := body["reasoning_effort"]; ok {
		t.Fatalf("think=off 不应发送 reasoning_effort，实得 %v", v)
	}
	// 同 provider 另一轮 think=on + effort 空 → 缺省 high（配置无 thinking 键，按请求体）
	startTurnLLM(t, s, "s-req-on", "t-req-on", "prov2", "on", "")
	sendAndWait(t, s, "s-req-on", "t-req-on", "think on")
	body2, _, _ := rec.first(t, 1)
	if got, _ := body2["reasoning_effort"].(string); got != "high" {
		t.Fatalf("think=on + effort 空 → reasoning_effort=%v want high（缺省）", body2["reasoning_effort"])
	}
}

// TestLLMProviderEmptyFieldsFallBackToFlags：命中 provider 但 baseUrl/model 为空 →
// 分别回落 exe flag -llm-base / -llm-model（apiKey 仍生效）。
func TestLLMProviderEmptyFieldsFallBackToFlags(t *testing.T) {
	flagRec, flagSrv := newProviderServer(t) // exe flag -llm-base
	s := newTestServer(t, flagSrv)           // Options.LLMModel = "mock"（exe flag -llm-model）
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{"name": "prov-empty", "apiKey": "sk-e"})
	startTurnLLM(t, s, "s-empty", "t-empty", "prov-empty", "on", "high")
	sendAndWait(t, s, "s-empty", "t-empty", "fallback")

	body, auth, _ := flagRec.first(t, 0) // baseUrl 空 → 打到 -llm-base
	if got, _ := body["model"].(string); got != "mock" {
		t.Fatalf("body.model=%v want mock（provider.model 空 → 回落 -llm-model）", body["model"])
	}
	if auth != "Bearer sk-e" {
		t.Fatalf("Authorization=%q want Bearer sk-e（apiKey 独立生效）", auth)
	}
}

// TestLLMProviderUnmatchedFallsBackToFlags（断言 ④）：未命中的 provider name → 回落 exe flags
// 现状（请求打到 -llm-base、body.model = 传入值、无 Authorization）。
func TestLLMProviderUnmatchedFallsBackToFlags(t *testing.T) {
	flagRec, flagSrv := newProviderServer(t) // = exe flag -llm-base（Options.LLMBase）
	otherRec, otherSrv := newProviderServer(t)
	s := newTestServer(t, flagSrv)
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{
		"name": "prov-x", "baseUrl": otherSrv.URL, "apiKey": "sk-other", "model": "mx",
	})
	startTurnLLM(t, s, "s-nomatch", "t-nomatch", "unknown-prov", "on", "high")
	sendAndWait(t, s, "s-nomatch", "t-nomatch", "no provider")

	body, auth, _ := flagRec.first(t, 0)
	if got, _ := body["model"].(string); got != "unknown-prov" {
		t.Fatalf("body.model=%v want unknown-prov（未命中：传入值即 model，现状）", body["model"])
	}
	if auth != "" {
		t.Fatalf("未命中 provider 不应带 Authorization，实得 %q", auth)
	}
	if n := otherRec.count(); n != 0 {
		t.Fatalf("未命中 provider 时不应请求其 baseUrl（%d 次）", n)
	}
}

// TestLLMSimpleLLMFieldSelectsProvider（SL-2 / SL-C7，2026-09-24）：llm-simple 带可选 `llm`
// （provider name）→ 该 provider 生效（baseUrl / model / Authorization）。
func TestLLMSimpleLLMFieldSelectsProvider(t *testing.T) {
	defRec, defSrv := newProviderServer(t) // exe flag -llm-base（带 llm 后不应再被请求）
	provRec, provSrv := newProviderServer(t)
	s := newTestServer(t, defSrv)
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{
		"name": "sub-llm", "protocol": "openai", "baseUrl": provSrv.URL,
		"apiKey": "sk-sub", "model": "msub",
	})

	v := &mq.Value{Payload: jb(map[string]any{
		"prompt": "summarize this", "instance_id": "ins-test", "llm": "sub-llm",
	})}
	if err := s.onLLMSimple(context.Background(), "llm-simple", v); err != nil {
		t.Fatalf("onLLMSimple: %v", err)
	}
	body, auth, path := provRec.first(t, 0)
	if got, _ := body["model"].(string); got != "msub" {
		t.Fatalf("body.model=%v want msub（llm 命中的 provider.model）", body["model"])
	}
	if auth != "Bearer sk-sub" {
		t.Fatalf("Authorization=%q want Bearer sk-sub", auth)
	}
	if path != "/chat/completions" {
		t.Fatalf("path=%q want /chat/completions", path)
	}
	if n := defRec.count(); n != 0 {
		t.Fatalf("带 llm 时不应请求 -llm-base 端点（%d 次）", n)
	}
	res, _ := v.Result.(map[string]any)
	if txt, _ := res["text"].(string); txt != "ok" {
		t.Fatalf("应答 text=%v want ok", res["text"])
	}
}

// TestLLMSimpleWithoutLLMUsesExeDefault（SL-2 兼容硬要求）：**不传 `llm`** 与**传空串**两条路径
// 等价 → 现状（exe flags 隐含默认：打到 -llm-base、body.model=-llm-model、无 Authorization），
// 即 provider 解析对缺省载荷零影响。
func TestLLMSimpleWithoutLLMUsesExeDefault(t *testing.T) {
	defRec, defSrv := newProviderServer(t)
	s := newTestServer(t, defSrv) // Options.LLMBase/LLMModel = "mock"
	registerProviderInstance(t, s)
	// 铺一个 provider：证明「不传 llm」不会去解析它。
	saveTestLLMProvider(t, s, map[string]any{"name": "sub-llm", "baseUrl": "http://127.0.0.1:1", "model": "m-x"})

	for _, payload := range []map[string]any{
		{"prompt": "hello", "instance_id": "ins-test"},            // 缺省不携带
		{"prompt": "hello", "instance_id": "ins-test", "llm": ""}, // 显式空串
	} {
		v := &mq.Value{Payload: jb(payload)}
		if err := s.onLLMSimple(context.Background(), "llm-simple", v); err != nil {
			t.Fatalf("onLLMSimple(%v): %v", payload, err)
		}
	}
	if n := defRec.count(); n != 2 {
		t.Fatalf("llm 请求数=%d want 2（两次均走 exe flags 默认）", n)
	}
	body0, auth0, path0 := defRec.first(t, 0)
	body1, auth1, path1 := defRec.first(t, 1)
	if got, _ := body0["model"].(string); got != "mock" {
		t.Fatalf("body.model=%v want mock（exe flag -llm-model，现状）", body0["model"])
	}
	if auth0 != "" || auth1 != "" {
		t.Fatalf("未指定 provider 不应带 Authorization，实得 %q / %q", auth0, auth1)
	}
	if path0 != path1 || !reflect.DeepEqual(body0, body1) {
		t.Fatalf("缺省不携带与显式空串应逐字段等价：\n%v %q\n%v %q", body0, path0, body1, path1)
	}
}

// TestLLMProviderLegacyMaxTokensCompat（口径 Z1，2026-09-25）：provider 仍带**旧键** `maxTokens`
// （改名前的历史配置，无 `maxOutputToken`）→ 读时兼容映射为 maxOutputToken → 请求体 max_tokens 生效。
func TestLLMProviderLegacyMaxTokensCompat(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerProviderInstance(t, s)
	saveTestLLMProvider(t, s, map[string]any{
		"name": "prov-legacy", "baseUrl": srv.URL, "model": "m1",
		"maxTokens": 1234, // 旧键（无 maxOutputToken）
	})
	startTurnLLM(t, s, "s-legacy", "t-legacy", "prov-legacy", "on", "high")
	sendAndWait(t, s, "s-legacy", "t-legacy", "legacy field")

	body, _, _ := rec.first(t, 0)
	if v, ok := body["max_tokens"]; !ok || v != float64(1234) {
		t.Fatalf("max_tokens=%v ok=%v want 1234（旧键 maxTokens 读时兼容为 maxOutputToken）", v, ok)
	}
}
