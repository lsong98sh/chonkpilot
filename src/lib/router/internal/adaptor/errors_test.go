// LR-5 / LR-8 白盒：错误体识别（errors.go）+ LR-8 的状态码 / 传输错误分类与 Retry-After 解析。
package adaptor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-router/internal/canon"
)

// TestErrorMessage：JSON（error 对象 / error 串 / message / detail）与非 JSON 兜底。
func TestErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai error.message", `{"error":{"message":"invalid api key","type":"invalid_request_error"}}`, "invalid api key"},
		{"error 字符串", `{"error":"boom"}`, "boom"},
		{"顶层 message", `{"message":"rate limited"}`, "rate limited"},
		{"顶层 detail", `{"detail":"not found"}`, "not found"},
		{"HTML 兜底", "\n<html><body>502 Bad Gateway</body></html>\n", "<html><body>502 Bad Gateway</body></html>"},
		{"纯文本兜底", "  upstream timeout  ", "upstream timeout"},
		{"空体", "   ", ""},
		{"error 对象无 message", `{"error":{"type":"x"}}`, `{"error":{"type":"x"}}`},
	}
	for _, c := range cases {
		if got := ErrorMessage([]byte(c.body)); got != c.want {
			t.Fatalf("%s: ErrorMessage=%q want %q", c.name, got, c.want)
		}
	}
}

// TestStatusErrorKindAndRetryable：HTTP 状态 → Kind/Retryable/Status（401·403 auth；429
// rate_limit；5xx server；**其余（含 4xx）protocol**，见 I-132），体非 JSON 亦能取到信息串。
func TestStatusErrorKindAndRetryable(t *testing.T) {
	cases := []struct {
		status    int
		wantKind  canon.ErrorKind
		retryable bool
	}{
		{401, canon.ErrorAuth, false},
		{403, canon.ErrorAuth, false},
		{429, canon.ErrorRateLimit, true},
		{500, canon.ErrorServer, true},
		{503, canon.ErrorServer, true},
		{400, canon.ErrorProtocol, false},
		{404, canon.ErrorProtocol, false},
	}
	for _, c := range cases {
		e := StatusError(c.status, http.Header{}, []byte(`{"error":{"message":"boom"}}`))
		if e.Kind != c.wantKind || e.Retryable != c.retryable || e.Status != c.status {
			t.Fatalf("status=%d → %+v want {kind:%s retryable:%v status:%d}", c.status, e, c.wantKind, c.retryable, c.status)
		}
		if e.Message == "" || e.Message == "llm http "+fmt.Sprint(c.status)+": " {
			t.Fatalf("status=%d 信息串为空：%q", c.status, e.Message)
		}
	}
	// 非 JSON 体（网关 HTML 兜底页）：原样取文本，分类不依赖 JSON。
	e := StatusError(502, http.Header{}, []byte("<html>502 Bad Gateway</html>"))
	if e.Kind != canon.ErrorServer || e.Message != "llm http 502: <html>502 Bad Gateway</html>" || e.RetryAfter != 0 {
		t.Fatalf("非 JSON 体 → %+v", e)
	}
	// 空体：回落状态码文本。
	if e := StatusError(504, nil, nil); e.Message != "llm http 504: Gateway Timeout" {
		t.Fatalf("空体 → %q", e.Message)
	}
}

// TestStatusErrorRetryAfter：`Retry-After`（秒数 / HTTP-date）落到 Error.RetryAfter；非法 / 过去 → 0。
func TestStatusErrorRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "3")
	if e := StatusError(429, h, nil); e.RetryAfter != 3*time.Second {
		t.Fatalf("Retry-After: 3 → %v want 3s", e.RetryAfter)
	}
	h.Set("Retry-After", "0")
	if e := StatusError(429, h, nil); e.RetryAfter != 0 {
		t.Fatalf("Retry-After: 0 → %v want 0", e.RetryAfter)
	}
	h.Set("Retry-After", "Mon, 02 Jan 2006 15:04:05 GMT")
	if e := StatusError(429, h, nil); e.RetryAfter != 0 {
		t.Fatalf("过去的 HTTP-date → %v want 0", e.RetryAfter)
	}
}

// TestParseRetryAfter：秒数 / HTTP-date（未来）/ 非法 / 负数 / 空。
func TestParseRetryAfter(t *testing.T) {
	if got := ParseRetryAfter(" 12 "); got != 12*time.Second {
		t.Fatalf("秒数=%v want 12s", got)
	}
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	got := ParseRetryAfter(future)
	if got <= 0 || got > 30*time.Second {
		t.Fatalf("未来 HTTP-date=%v want (0,30s]", got)
	}
	for _, v := range []string{"", "abc", "-1", "0", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)} {
		if got := ParseRetryAfter(v); got != 0 {
			t.Fatalf("ParseRetryAfter(%q)=%v want 0", v, got)
		}
	}
}

// TestTransportError：超时 → timeout；连接类 / 断链（含 SSE 半截事件）→ network（均可重试）；
// nil → nil。
func TestTransportError(t *testing.T) {
	if TransportError(nil) != nil {
		t.Fatal("nil → nil")
	}
	if e := TransportError(context.DeadlineExceeded); e.Kind != canon.ErrorTimeout || !e.Retryable {
		t.Fatalf("DeadlineExceeded → %+v want timeout/可重试", e)
	}
	if e := TransportError(&net.DNSError{IsTimeout: true}); e.Kind != canon.ErrorTimeout {
		t.Fatalf("net 超时 → %+v want timeout", e)
	}
	if e := TransportError(ErrTruncated); e.Kind != canon.ErrorNetwork || !e.Retryable {
		t.Fatalf("SSE 半截事件 → %+v want network/可重试", e)
	}
	if e := TransportError(errors.New("connection reset by peer")); e.Kind != canon.ErrorNetwork {
		t.Fatalf("连接重置 → %+v want network", e)
	}
}

// TestErrorTypeKind：流内错误对象 `error.type`（两套词表并集）→ Kind；词表外 → protocol。
func TestErrorTypeKind(t *testing.T) {
	cases := map[string]canon.ErrorKind{
		"rate_limit_error":      canon.ErrorRateLimit,
		"rate_limit_exceeded":   canon.ErrorRateLimit,
		"insufficient_quota":    canon.ErrorRateLimit,
		"overloaded_error":      canon.ErrorServer,
		"api_error":             canon.ErrorServer,
		"server_error":          canon.ErrorServer,
		"authentication_error":  canon.ErrorAuth,
		"permission_error":      canon.ErrorAuth,
		"invalid_request_error": canon.ErrorInvalid,
		"not_found_error":       canon.ErrorInvalid,
		"request_too_large":     canon.ErrorInvalid,
		"":                      canon.ErrorProtocol,
		"weird_new_error":       canon.ErrorProtocol,
		"Rate_Limit_Error":      canon.ErrorRateLimit, // 大小写容忍
	}
	for typ, want := range cases {
		if got := ErrorTypeKind(typ); got != want {
			t.Fatalf("ErrorTypeKind(%q)=%s want %s", typ, got, want)
		}
	}
}
