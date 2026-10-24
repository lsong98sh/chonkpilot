// LLM 桥接入口（BRIDGE-1）白盒：路由（路径拼接 + `default` 保留段）/ 鉴权覆盖 /
// 流式直通 / loopback 硬拒 / 失败语义（404 · 400 · 502）。
//
// 上游 = `httptest.NewServer` 记录 path/query/headers/body；下游 = 本入口 `Handler()` 直调
// （`httptest.NewServer(s.Handler())`），不经真实网卡（loopback 拒启单独用 Start 验证）。
package llmbridge

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recorder 记录上游收到的请求。
type recorder struct {
	path    string
	query   string
	auth    string
	apiKey  string
	body    string
	headers http.Header
}

// newUpstream 起一个记录请求并回固定文本的上游端点。
func newUpstream(t *testing.T, rec *recorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.auth = r.Header.Get("Authorization")
		rec.apiKey = r.Header.Get("X-Api-Key")
		rec.body = string(b)
		rec.headers = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newBridge 起一个桥入口（httptest 包装 Handler；不经 socket）。
func newBridge(t *testing.T, resolve Resolver) *httptest.Server {
	t.Helper()
	s := New(Options{Resolve: resolve})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// TestBridgeOpenAIPathJoinAndAuthOverride：openai 型 → 剩余路径原样拼到 base_url（含查询串）、
// body 字节透传、**客户端 Authorization 被覆盖**为服务端 key。
func TestBridgeOpenAIPathJoinAndAuthOverride(t *testing.T) {
	rec := &recorder{}
	up := newUpstream(t, rec)
	bridge := newBridge(t, func(name string) (Target, bool) {
		if name != "gpt4" {
			return Target{}, false
		}
		return Target{BaseURL: up.URL + "/v1", APIKey: "sk-server-key", Protocol: "openai"}, true
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, bridge.URL+"/llm/gpt4/chat/completions?foo=bar", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer client-key") // 客户端自报 key → 必须被覆盖
	req.Header.Set("X-Trace", "keep-me")                 // 其余请求头原样透传
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if rec.path != "/v1/chat/completions" {
		t.Fatalf("上游 path=%q want /v1/chat/completions（base_url 路径 + 剩余路径原样拼接）", rec.path)
	}
	if rec.query != "foo=bar" {
		t.Fatalf("上游 query=%q want foo=bar（查询串原样透传）", rec.query)
	}
	if rec.auth != "Bearer sk-server-key" {
		t.Fatalf("上游 Authorization=%q want Bearer sk-server-key（覆盖客户端自报）", rec.auth)
	}
	if rec.apiKey != "" {
		t.Fatalf("openai 型不应注入 X-Api-Key，实得 %q", rec.apiKey)
	}
	if rec.body != body {
		t.Fatalf("上游 body=%q want %q（字节透传）", rec.body, body)
	}
	if got := rec.headers.Get("X-Trace"); got != "keep-me" {
		t.Fatalf("其余请求头应原样透传，X-Trace=%q", got)
	}
}

// TestBridgeAnthropicAuthOverride：anthropic 型 → `x-api-key` 注入服务端 key，
// **Authorization 被删除**（客户端自带的 key 头一律被覆盖）。
func TestBridgeAnthropicAuthOverride(t *testing.T) {
	rec := &recorder{}
	up := newUpstream(t, rec)
	bridge := newBridge(t, func(name string) (Target, bool) {
		if name != "claude" {
			return Target{}, false
		}
		return Target{BaseURL: up.URL, APIKey: "sk-ant-key", Protocol: "anthropic"}, true
	})

	req, _ := http.NewRequest(http.MethodPost, bridge.URL+"/llm/claude/v1/messages", strings.NewReader(`{"model":"claude"}`))
	req.Header.Set("X-Api-Key", "unused")             // 客户端自报 → 必须被覆盖
	req.Header.Set("Authorization", "Bearer client")  // 异族头 → 必须被删除
	req.Header.Set("Anthropic-Version", "2023-06-01") // 协议头 → 原样透传
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if rec.path != "/v1/messages" {
		t.Fatalf("上游 path=%q want /v1/messages", rec.path)
	}
	if rec.apiKey != "sk-ant-key" {
		t.Fatalf("上游 X-Api-Key=%q want sk-ant-key", rec.apiKey)
	}
	if rec.auth != "" {
		t.Fatalf("anthropic 型不应带 Authorization，实得 %q", rec.auth)
	}
	if got := rec.headers.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Fatalf("Anthropic-Version 应原样透传，实得 %q", got)
	}
}

// TestBridgeStreamingPassthrough：SSE **流式直通**——客户端在**上游尚未结束**时即收到首块
// （若桥缓冲则本用例会死等首块 → 超时失败）。
func TestBridgeStreamingPassthrough(t *testing.T) {
	firstSent := make(chan struct{})
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		fl.Flush()
		close(firstSent)
		<-release // 保持连接打开，直到客户端确认已收到首块
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer up.Close()
	bridge := newBridge(t, func(string) (Target, bool) {
		return Target{BaseURL: up.URL, APIKey: "sk-x", Protocol: "openai"}, true
	})

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(bridge.URL + "/llm/p/chat/completions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	firstLine := make(chan string, 1)
	go func() {
		br := bufio.NewReader(resp.Body)
		line, _ := br.ReadString('\n')
		firstLine <- line
	}()
	select {
	case line := <-firstLine:
		if !strings.Contains(line, `"content":"a"`) {
			t.Fatalf("首块=%q want 含 content:a", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未在上游结束前收到首块（桥疑似缓冲，未流式直通）")
	}
	close(release) // 放行上游结束
}

// TestBridgeDefaultSegment：保留段 `default` 作为原样 `<name>` 传给解析回调（系统默认 LLM）。
func TestBridgeDefaultSegment(t *testing.T) {
	rec := &recorder{}
	up := newUpstream(t, rec)
	var gotName string
	bridge := newBridge(t, func(name string) (Target, bool) {
		gotName = name
		if name != "default" {
			return Target{}, false
		}
		return Target{BaseURL: up.URL, Protocol: "openai"}, true
	})
	resp, err := http.Get(bridge.URL + "/llm/default/chat/completions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || gotName != "default" {
		t.Fatalf("status=%d name=%q want 200/default", resp.StatusCode, gotName)
	}
	if rec.auth != "" {
		t.Fatalf("无 apiKey 时不应注入 Authorization，实得 %q", rec.auth)
	}
}

// TestBridgeProviderNotFound404：解析未命中 → 404（不静默回落）。
func TestBridgeProviderNotFound404(t *testing.T) {
	bridge := newBridge(t, func(string) (Target, bool) { return Target{}, false })
	resp, err := http.Get(bridge.URL + "/llm/nope/chat/completions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}

// TestBridgeUnsupportedProtocol400：未识别协议 → 400（扩展点，不静默猜）。
func TestBridgeUnsupportedProtocol400(t *testing.T) {
	bridge := newBridge(t, func(string) (Target, bool) {
		return Target{BaseURL: "http://127.0.0.1:1", Protocol: "gemini"}, true
	})
	resp, err := http.Get(bridge.URL + "/llm/x/chat/completions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", resp.StatusCode)
	}
}

// TestBridgeMissingProviderSegment：`/llm/`（无 provider 段）→ 404。
func TestBridgeMissingProviderSegment(t *testing.T) {
	bridge := newBridge(t, func(string) (Target, bool) { return Target{}, true })
	resp, err := http.Get(bridge.URL + "/llm/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}

// TestBridgeDefaultPath：空 Path → DefaultPath()（文件名恒为 llm.sock）；显式路径原样保留。
func TestBridgeDefaultPath(t *testing.T) {
	if got := New(Options{}).Path(); filepath.Base(got) != DefaultSocketName {
		t.Fatalf("默认 Path=%q 应以 %q 结尾", got, DefaultSocketName)
	}
	p := filepath.Join(t.TempDir(), "custom.sock")
	if got := New(Options{Path: p}).Path(); got != p {
		t.Fatalf("Path()=%q want %q", got, p)
	}
}

// TestBridgeUDSStartOK：UDS 启动后可服务（socket 文件落到指定路径）。
func TestBridgeUDSStartOK(t *testing.T) {
	rec := &recorder{}
	up := newUpstream(t, rec)
	sock := filepath.Join(t.TempDir(), "llm.sock")
	s := New(Options{
		Path:    sock,
		Resolve: func(string) (Target, bool) { return Target{BaseURL: up.URL, Protocol: "responses"}, true },
	})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Shutdown(context.Background()) }()

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := client.Post("http://unix/llm/any/responses", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rec.path != "/responses" {
		t.Fatalf("status=%d path=%q want 200//responses", resp.StatusCode, rec.path)
	}
}

// TestBridgeUDSDuplicateRefused：同路径已有活实例 → 第二个拒启（按路径独立的单例判定）。
func TestBridgeUDSDuplicateRefused(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "llm.sock")
	first := New(Options{Path: sock, Resolve: func(string) (Target, bool) { return Target{}, false }})
	if err := first.Start(); err != nil {
		t.Fatalf("Start(first): %v", err)
	}
	defer func() { _ = first.Shutdown(context.Background()) }()

	second := New(Options{Path: sock, Resolve: func(string) (Target, bool) { return Target{}, false }})
	if err := second.Start(); err == nil {
		_ = second.Shutdown(context.Background())
		t.Fatal("同路径已有活实例，第二个应拒启")
	}
}
