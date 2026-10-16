// LLM 桥接入口（BRIDGE-1）黑盒回归（工程目录 chonkpilot-test/chonkpilot-llm/unittest）。
//
// **HTTP 面豁免「MQ 驱动」总纲**（见 [50-测试体系 §1/§7]）：桥接入口是**纯 HTTP 面**——
// 不新增 / 修改 / 删除任何 MQ 主题与 payload，故按 HTTP 面既有先例，本文件以 **HTTP 请求驱动 +
// 上游 HTTP 断言**（不经 mq 驱动/监听）。
//
// 只经公开 API（`llmbridge.New/Start/Shutdown`）驱动；上游 = `httptest.NewServer` 记录请求。
package llmtest

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-llm/llmbridge"
)

// bridgeUpstreamRec 记录上游收到的请求。
type bridgeUpstreamRec struct {
	path   string
	query  string
	auth   string
	apiKey string
	body   string
}

// TestLLMBridgeTransparentProxy：openai / anthropic 两条转发路径（路径拼接 + 鉴权覆盖 +
// 透传）+ 流式（SSE）直通 + 非 loopback 拒启。
func TestLLMBridgeTransparentProxy(t *testing.T) {
	rec := &bridgeUpstreamRec{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.auth = r.Header.Get("Authorization")
		rec.apiKey = r.Header.Get("X-Api-Key")
		rec.body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()

	// 两条 provider 路由：gpt4（openai）/ claude（anthropic）。
	bridge := llmbridge.New(llmbridge.Options{
		Addr: "127.0.0.1:0",
		Resolve: func(name string) (llmbridge.Target, bool) {
			switch name {
			case "gpt4":
				return llmbridge.Target{BaseURL: up.URL + "/v1", APIKey: "sk-server-oa", Protocol: "openai"}, true
			case "claude":
				return llmbridge.Target{BaseURL: up.URL, APIKey: "sk-server-ant", Protocol: "anthropic"}, true
			}
			return llmbridge.Target{}, false
		},
	})
	if err := bridge.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = bridge.Shutdown(context.Background()) }()
	base := "http://" + bridge.Addr()

	// ① openai：剩余路径原样拼到 base_url（含查询串）；客户端 Authorization 被覆盖；body 透传。
	body := `{"model":"gpt-4o"}`
	req, _ := http.NewRequest(http.MethodPost, base+"/llm/gpt4/chat/completions?foo=bar", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer client-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("openai do: %v", err)
	}
	_ = resp.Body.Close()
	if rec.path != "/v1/chat/completions" || rec.query != "foo=bar" || rec.auth != "Bearer sk-server-oa" || rec.body != body {
		t.Fatalf("openai 转发异常：path=%q query=%q auth=%q body=%q", rec.path, rec.query, rec.auth, rec.body)
	}

	// ② anthropic：x-api-key 注入服务端 key；Authorization（异族）被删除。
	req, _ = http.NewRequest(http.MethodPost, base+"/llm/claude/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("X-Api-Key", "unused")
	req.Header.Set("Authorization", "Bearer client")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("anthropic do: %v", err)
	}
	_ = resp.Body.Close()
	if rec.path != "/v1/messages" || rec.apiKey != "sk-server-ant" || rec.auth != "" {
		t.Fatalf("anthropic 转发异常：path=%q apiKey=%q auth=%q", rec.path, rec.apiKey, rec.auth)
	}

	// ③ 未命中 provider → 404（不静默回落）。
	resp, err = http.Get(base + "/llm/nope/chat/completions")
	if err != nil {
		t.Fatalf("404 get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未命中 provider status=%d want 404", resp.StatusCode)
	}
}

// TestLLMBridgeStreamingPassthrough：SSE 流式直通（客户端在上游结束前收到首块）。
func TestLLMBridgeStreamingPassthrough(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		fl.Flush()
		<-release
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer up.Close()

	bridge := llmbridge.New(llmbridge.Options{
		Addr:    "127.0.0.1:0",
		Resolve: func(string) (llmbridge.Target, bool) { return llmbridge.Target{BaseURL: up.URL, Protocol: "openai"}, true },
	})
	if err := bridge.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = bridge.Shutdown(context.Background()) }()

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + bridge.Addr() + "/llm/p/chat/completions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	lineCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		lineCh <- line
	}()
	select {
	case line := <-lineCh:
		if !strings.Contains(line, `"content":"a"`) {
			t.Fatalf("首块=%q want 含 content:a", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未在上游结束前收到首块（桥疑似缓冲）")
	}
	close(release)
}

// TestLLMBridgeNonLoopbackRefused：非 loopback 绑定 → 拒启（fail-closed）。
func TestLLMBridgeNonLoopbackRefused(t *testing.T) {
	s := llmbridge.New(llmbridge.Options{
		Addr:    "0.0.0.0:5669",
		Resolve: func(string) (llmbridge.Target, bool) { return llmbridge.Target{}, false },
	})
	if err := s.Start(); err == nil {
		_ = s.Shutdown(context.Background())
		t.Fatal("非 loopback 应拒启，却启动成功")
	}
}
