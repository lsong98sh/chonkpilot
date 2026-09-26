// 出网共用件白盒：`Exchange.PostJSON` 的响应体形态判定。
//
// 关注点（LR-11 行为等价性）：旧 llm 侧按 SSE 行解析、**不作 Content-Type 判定**；
// 故本件在未声明 `text/event-stream` 时按**体首行嗅探**（`data:` / `event:`）继续走 SSE 泵，
// 否则才走「兼容端点忽略 stream=true 回单发 JSON」路径。
package adaptor

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPostJSONSSEWithoutContentType：端点不回 `text/event-stream` 头、体仍是 SSE → 逐条回调。
func TestPostJSONSSEWithoutContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// 刻意不设置 Content-Type（Go 会按内容嗅探为 text/plain）。
		_, _ = w.Write([]byte("data: {\"a\":1}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	ex := NewExchange(nil, 0, 0)
	var got []string
	err := ex.PostJSON(context.Background(), srv.URL, nil, map[string]any{"stream": true}, func(payload []byte) error {
		got = append(got, string(payload))
		return nil
	})
	if err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if len(got) != 2 || got[0] != `{"a":1}` || got[1] != "[DONE]" {
		t.Fatalf("SSE 载荷=%q want [\"{\\\"a\\\":1}\" \"[DONE]\"]（终止标记由适配器过滤，见 IsDone）", got)
	}
}

// TestPostJSONSSEWithEventLines：仅 `event:` 开头（无 `data:` 前缀的语义化 SSE）也走 SSE 泵。
func TestPostJSONSSEWithEventLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("event: ping\ndata: {\"b\":2}\n\n"))
	}))
	defer srv.Close()

	ex := NewExchange(nil, 0, 0)
	var got []string
	if err := ex.PostJSON(context.Background(), srv.URL, nil, map[string]any{}, func(payload []byte) error {
		got = append(got, string(payload))
		return nil
	}); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if len(got) != 1 || got[0] != `{"b":2}` {
		t.Fatalf("载荷=%q want [\"{\\\"b\\\":2}\"]", got)
	}
}

// TestPostJSONOneShotJSON：非 SSE（体以 `{` 开头）→ 整体单发回调一次（忽略 stream=true 的端点）。
func TestPostJSONOneShotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(` {"ok":true} `))
	}))
	defer srv.Close()

	ex := NewExchange(nil, 0, 0)
	var got []string
	if err := ex.PostJSON(context.Background(), srv.URL, nil, map[string]any{}, func(payload []byte) error {
		got = append(got, string(payload))
		return nil
	}); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if len(got) != 1 || got[0] != `{"ok":true}` {
		t.Fatalf("单发载荷=%q want [\"{\\\"ok\\\":true}\"]（去首尾空白）", got)
	}
}

// TestLooksLikeSSE：嗅探判定的边界（前导空白跳过；JSON 不误判）。
func TestLooksLikeSSE(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{"data: {}", true},
		{"\n\n  data: {}", true},
		{"event: x\ndata: {}\n\n", true},
		{"{\"a\":1}", false},
		{"[1,2]", false},
		{"", false},
	} {
		if got := looksLikeSSE(bufio.NewReader(strings.NewReader(tc.body))); got != tc.want {
			t.Fatalf("looksLikeSSE(%q)=%v want %v", tc.body, got, tc.want)
		}
	}
}
