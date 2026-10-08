// llm.test-connection（设置 → LLM「测试连接」）单测：真实 HTTP stub 探活成功 / 鉴权失败 /
// 连接失败 / 超时 / 入参不合法，并锁死两条硬约束：
//   - **API Key 不泄露**：应答信封与日志（logf sink）都不得出现明文（上游回显亦须脱敏）；
//   - **只读探测**：不打当前生效 provider（exe flags）、不改 s.llm / 客户端缓存、不落库
//     （探活期间总线上不得出现 data-* / session-* 消息）。
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// probeRec 记录探活 stub 收到的请求（body / Authorization / path）。
type probeRec struct {
	mu     sync.Mutex
	bodies []map[string]any
	auths  []string
	paths  []string
}

func (r *probeRec) first(t *testing.T) (map[string]any, string, string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 || len(r.auths) == 0 || len(r.paths) == 0 {
		t.Fatal("探活 stub 未收到完整请求记录")
	}
	return r.bodies[0], r.auths[0], r.paths[0]
}

func (r *probeRec) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

// probeChatServer 起 chat 协议 stub：流式回一个带 model 字段的 chunk（探活据此回显模型名）。
func probeChatServer(t *testing.T) (*probeRec, *httptest.Server) {
	t.Helper()
	rec := &probeRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.auths = append(rec.auths, r.Header.Get("Authorization"))
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{
			"model":   "mock-model-9b",
			"choices": []any{map[string]any{"delta": map[string]any{"content": "hi"}, "finish_reason": "stop"}},
		})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return rec, srv
}

// testConn 发 llm.test-connection（真实总线方法面）并取结果信封。
func testConn(t *testing.T, s *Server, payload map[string]any) map[string]any {
	t.Helper()
	v := s.bus.Emit(context.Background(), SubjectLLMTestConnection, jb(payload)).Wait()
	if v.Err() != nil {
		t.Fatalf("llm.test-connection 总线错误: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("llm.test-connection 无结果信封（v.Result=%#v）", v.Result)
	}
	return res
}

// connError 取失败信封的 (kind, message)。
func connError(t *testing.T, res map[string]any) (string, string) {
	t.Helper()
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("预期失败，实得 ok=true: %+v", res)
	}
	e, _ := res["error"].(map[string]any)
	if e == nil {
		t.Fatalf("失败信封缺 error: %+v", res)
	}
	kind, _ := e["kind"].(string)
	msg, _ := e["message"].(string)
	return kind, msg
}

// TestLLMTestConnectionOK：配置齐备 → 真实打到「给定 baseUrl」的 /chat/completions，带给定
// apiKey、max_tokens=1、stream=true、无工具；首包即成功 → ok + 延迟 + 回显模型名；
// 同时锁死：不打当前生效 provider（exe flags）、不改运行态客户端、不落库。
func TestLLMTestConnectionOK(t *testing.T) {
	defRec, defSrv := newProviderServer(t) // exe flag -llm-base（当前生效 provider，探活不得触碰）
	s := newTestServer(t, defSrv)
	registerProviderInstance(t, s)
	// 先落一条 provider 配置（探活后必须原样不动）
	saveTestLLMProvider(t, s, map[string]any{
		"name": "prov-keep", "baseUrl": defSrv.URL, "model": "m-keep", "apiKey": "sk-keep-0001",
	})
	before := mustJSON(t, dataResult(t, dataCall(t, s, "data-user-config-load", map[string]any{"instance_id": "ins-test"})))

	rec, probeSrv := probeChatServer(t)
	// 探活期间总线上不得出现会话/数据面写（不落库、不建会话）
	var mu sync.Mutex
	var subjects []string
	sub, err := s.bus.On(">", 0, func(_ context.Context, subject string, _ *mq.Value) error {
		mu.Lock()
		subjects = append(subjects, subject)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("订阅失败: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	const key = "sk-secret-probe-1234"
	res := testConn(t, s, map[string]any{
		"instance_id": "ins-test", "baseUrl": probeSrv.URL, "model": "cfg-model",
		"apiKey": key, "protocol": "openai",
	})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("探活应成功，实得 %+v", res)
	}
	if _, has := res["latency_ms"]; !has {
		t.Fatalf("成功信封缺 latency_ms: %+v", res)
	}
	if got, _ := res["model_echo"].(string); got != "mock-model-9b" {
		t.Fatalf("model_echo=%q want mock-model-9b（上游回显）", got)
	}

	// 请求形态：给定配置生效（模型 / Key / 端点），且是「最小请求」
	body, auth, path := rec.first(t)
	if path != "/chat/completions" {
		t.Fatalf("path=%q want /chat/completions", path)
	}
	if auth != "Bearer "+key {
		t.Fatalf("Authorization=%q want Bearer <给定 apiKey>", auth)
	}
	if got, _ := body["model"].(string); got != "cfg-model" {
		t.Fatalf("body.model=%v want cfg-model（给定 model，非当前生效 provider）", body["model"])
	}
	if got, _ := body["max_tokens"].(float64); got != 1 {
		t.Fatalf("body.max_tokens=%v want 1（最小请求）", body["max_tokens"])
	}
	if got, _ := body["stream"].(bool); !got {
		t.Fatalf("body.stream=%v want true（对齐既有调用栈）", body["stream"])
	}
	if _, has := body["tools"]; has {
		t.Fatal("探活不得下发工具")
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages=%v want 单条 user 消息（最小输入）", body["messages"])
	}
	// 当前生效 provider 与运行态不得被触碰（LR-11：注册集 = router；exe flags 隐含默认仍在册）
	if n := defRec.count(); n != 0 {
		t.Fatalf("探活不得请求当前生效 provider（exe flags），实得 %d 次", n)
	}
	if c := s.rt.Capabilities(exeDefaultSpecName); !c.Stream {
		t.Fatalf("exe flags 隐含默认 provider 被改动（Capabilities=%+v）", c)
	}
	// 不落库：探活期间无 data-* / session-* / task-* 消息（须在后续 data- 读快照之前判定）
	mu.Lock()
	probed := append([]string(nil), subjects...)
	mu.Unlock()
	for _, subj := range probed {
		if strings.HasPrefix(subj, "data-") || strings.HasPrefix(subj, "session-") || strings.HasPrefix(subj, "task-") {
			t.Fatalf("探活期间出现会话/数据面消息 %q（须为只读探测）", subj)
		}
	}
	// 不落库：usr 配置逐字节不变
	after := mustJSON(t, dataResult(t, dataCall(t, s, "data-user-config-load", map[string]any{"instance_id": "ins-test"})))
	if before != after {
		t.Fatalf("探活改动了 usr 配置:\n before=%s\n after=%s", before, after)
	}
	// 返回信封不得含明文 Key
	if raw, _ := json.Marshal(res); strings.Contains(string(raw), key) {
		t.Fatalf("应答泄露 API Key: %s", raw)
	}
}

// TestLLMTestConnectionAuthMaskedKey：401 → 分类 auth；上游把 Authorization 回显进错误体时
// 应答与日志都必须脱敏（不得出现明文 Key），且 message 保留主路径形态 `[kind] …`（前端分类映射依赖）。
func TestLLMTestConnectionAuthMaskedKey(t *testing.T) {
	const key = "sk-secret-probe-401"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	s := newTestServer(t, mockLLMServer())
	// 日志 sink 须在 New 之后注入（New 会按 Options.LogWriter 重置 sink）
	var logBuf bytes.Buffer
	setLogSink(&logBuf)
	t.Cleanup(func() { setLogSink(nil) })

	res := testConn(t, s, map[string]any{"baseUrl": srv.URL, "model": "m1", "apiKey": key, "protocol": "openai"})
	kind, msg := connError(t, res)
	if kind != string(ErrAuth) {
		t.Fatalf("kind=%q want auth（401）", kind)
	}
	if !strings.HasPrefix(msg, "[auth] ") {
		t.Fatalf("message=%q 须为主路径形态 `[auth] …`（前端分类映射依赖）", msg)
	}
	if strings.Contains(msg, key) {
		t.Fatalf("应答泄露明文 Key: %q", msg)
	}
	if !strings.Contains(msg, "sk-***") {
		t.Fatalf("应答未脱敏为 sk-***: %q", msg)
	}
	if raw, _ := json.Marshal(res); strings.Contains(string(raw), key) {
		t.Fatalf("应答信封泄露明文 Key: %s", raw)
	}
	if logBuf.Len() == 0 {
		t.Fatal("探活结果须落日志（诊断）")
	}
	if strings.Contains(logBuf.String(), key) {
		t.Fatalf("日志泄露明文 Key: %q", logBuf.String())
	}
}

// TestLLMTestConnectionNetwork：端口不可达 → 分类 network（不判成功）。
func TestLLMTestConnectionNetwork(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close() // 关掉端口 → 连接被拒

	s := newTestServer(t, mockLLMServer())
	res := testConn(t, s, map[string]any{"baseUrl": url, "model": "m1"})
	kind, msg := connError(t, res)
	if kind != string(ErrNetwork) {
		t.Fatalf("kind=%q want network（连接失败），message=%q", kind, msg)
	}
	if !strings.HasPrefix(msg, "[network] ") {
		t.Fatalf("message=%q 须为主路径形态 `[network] …`", msg)
	}
}

// TestLLMTestConnectionTimeout：上游不响应 → 超时分类（不长时间挂起）。
func TestLLMTestConnectionTimeout(t *testing.T) {
	old := llmTestTimeout
	llmTestTimeout = 120 * time.Millisecond
	t.Cleanup(func() { llmTestTimeout = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
	}))
	defer srv.Close()

	s := newTestServer(t, mockLLMServer())
	start := time.Now()
	res := testConn(t, s, map[string]any{"baseUrl": srv.URL, "model": "m1"})
	kind, msg := connError(t, res)
	if kind != string(ErrTimeout) {
		t.Fatalf("kind=%q want timeout，message=%q", kind, msg)
	}
	if !strings.Contains(msg, "超时") {
		t.Fatalf("超时文案须可读: %q", msg)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("探活超时上限未生效（耗时 %v）", elapsed)
	}
}

// TestLLMTestConnectionInvalidParams：baseUrl/model 缺失 → invalid（不发任何请求）。
func TestLLMTestConnectionInvalidParams(t *testing.T) {
	rec, srv := probeChatServer(t)
	s := newTestServer(t, mockLLMServer())
	res := testConn(t, s, map[string]any{"baseUrl": srv.URL}) // 缺 model
	kind, msg := connError(t, res)
	if kind != string(llmTestKindInvalid) {
		t.Fatalf("kind=%q want invalid，message=%q", kind, msg)
	}
	if n := rec.count(); n != 0 {
		t.Fatalf("入参不合法不得发请求（实得 %d 次）", n)
	}
}

// TestLLMTestConnectionResponsesProtocol：protocol=responses → 走 /responses，max_output_tokens=1。
func TestLLMTestConnectionResponsesProtocol(t *testing.T) {
	rec := &probeRec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.auths = append(rec.auths, r.Header.Get("Authorization"))
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		ev, _ := json.Marshal(map[string]any{
			"type": "response.output_text.delta", "delta": "hi",
			"response": map[string]any{"model": "deepseek-v3"},
		})
		fmt.Fprintf(w, "data: %s\n\n", ev)
		done, _ := json.Marshal(map[string]any{"type": "response.completed"})
		fmt.Fprintf(w, "data: %s\n\n", done)
	}))
	defer srv.Close()

	s := newTestServer(t, mockLLMServer())
	res := testConn(t, s, map[string]any{"baseUrl": srv.URL, "model": "cfg-model", "protocol": "responses"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("responses 协议探活应成功，实得 %+v", res)
	}
	if got, _ := res["model_echo"].(string); got != "deepseek-v3" {
		t.Fatalf("model_echo=%q want deepseek-v3", got)
	}
	body, _, path := rec.first(t)
	if path != "/responses" {
		t.Fatalf("path=%q want /responses（协议分派）", path)
	}
	if got, _ := body["max_output_tokens"].(float64); got != 1 {
		t.Fatalf("body.max_output_tokens=%v want 1（最小请求）", body["max_output_tokens"])
	}
}

// TestMaskLLMSecrets：密钥脱敏（本次 Key 原文 + 通用形态兜底）。
func TestMaskLLMSecrets(t *testing.T) {
	for _, tc := range []struct{ in, key, want string }{
		{"Incorrect API key provided: sk-abc123456", "", "Incorrect API key provided: sk-***"},
		{"Bearer sk-abc123456", "", "Bearer ***"},
		{"key=secret-key-9876543210 x", "secret-key-9876543210", "key=sk-*** x"},
		{"no secret here", "sk-not-present", "no secret here"},
	} {
		if got := maskLLMSecrets(tc.in, tc.key); got != tc.want {
			t.Fatalf("maskLLMSecrets(%q, %q)=%q want %q", tc.in, tc.key, got, tc.want)
		}
	}
}

// mustJSON 序列化（比较 usr 配置前后快照用）。
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestValidateLLMTarget：SSRF 目标校验 —— 仅 http/https；requireAuth 形态（browser/gui）拒绝
// 内网 / 元数据目标（IP 字面量与域名解析两路）；desktop 形态只校验协议（本机 provider 零摩擦）。
func TestValidateLLMTarget(t *testing.T) {
	browser := New(nil, Options{Form: FormBrowser})
	desktop := New(nil, Options{Form: FormDesktop})

	// 协议：非 http/https（含缺 host）一律拒（两形态同）
	for _, bad := range []string{"ftp://example.com/x", "file:///etc/passwd", "http:///nohost", "not-a-url"} {
		if err := browser.validateLLMTarget(bad); err == nil {
			t.Fatalf("browser: %q 应拒绝（协议 / host）", bad)
		}
		if err := desktop.validateLLMTarget(bad); err == nil {
			t.Fatalf("desktop: %q 应拒绝（协议 / host）", bad)
		}
	}
	// requireAuth 形态：内网 / 元数据（IP 字面量）→ 拒
	for _, bad := range []string{
		"http://127.0.0.1:11434/v1", "http://10.0.0.5/v1", "http://192.168.1.1/v1",
		"http://172.16.0.1/v1", "http://169.254.169.254/latest/meta-data/",
		"https://[::1]:8443/v1", "http://0.0.0.0/v1",
	} {
		if err := browser.validateLLMTarget(bad); err == nil {
			t.Fatalf("browser: %q（内网/元数据）应拒绝", bad)
		}
	}
	// requireAuth 形态：公网 IP 字面量 → 放行（不实际出网，仅校验）
	if err := browser.validateLLMTarget("https://8.8.8.8/v1"); err != nil {
		t.Fatalf("browser: 公网目标应放行，got %v", err)
	}
	// desktop 形态：本机 provider 放行（零摩擦）
	for _, ok := range []string{"http://127.0.0.1:11434/v1", "https://localhost:8901/v1", "http://192.168.1.9/v1"} {
		if err := desktop.validateLLMTarget(ok); err != nil {
			t.Fatalf("desktop: %q 应放行，got %v", ok, err)
		}
	}
}

// TestLLMTestConnectionSSRFBlocked：requireAuth 形态（browser）下，内网 / 元数据目标在**出网前**
// 被拒（kind=invalid，且不发任何请求）；desktop 形态同目标仍放行（本机 provider 零摩擦）。
func TestLLMTestConnectionSSRFBlocked(t *testing.T) {
	rec, srv := probeChatServer(t) // 目标 = 127.0.0.1（内网）
	s := newTestServer(t, mockLLMServer())
	s.opts.Form = FormBrowser // 强制 requireAuth 形态 → 触发 SSRF 目标校验
	res := testConn(t, s, map[string]any{"baseUrl": srv.URL, "model": "m1"})
	if kind, _ := connError(t, res); kind != string(llmTestKindInvalid) {
		t.Fatalf("kind=%q want invalid（SSRF 目标被拒）", kind)
	}
	if n := rec.count(); n != 0 {
		t.Fatalf("被拒目标不得发请求，实得 %d 次", n)
	}

	// desktop 形态（newTestServer 默认）→ 同目标放行（可达 stub）
	s2 := newTestServer(t, mockLLMServer())
	res2 := testConn(t, s2, map[string]any{"baseUrl": srv.URL, "model": "m1"})
	if ok, _ := res2["ok"].(bool); !ok {
		t.Fatalf("desktop 形态本机 provider 应放行，实得 %+v", res2)
	}
}
