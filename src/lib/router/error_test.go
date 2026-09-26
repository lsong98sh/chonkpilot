// LR-1 白盒：错误分类（Kind → Retryable、HTTP 状态码映射）与退避等待值 RetryWait。
package router

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestErrorKindRetryableMatrix：可重试 = network / timeout / rate_limit / server；
// 不可重试 = auth / protocol / invalid。
func TestErrorKindRetryableMatrix(t *testing.T) {
	cases := []struct {
		kind ErrorKind
		want bool
	}{
		{ErrorNetwork, true},
		{ErrorTimeout, true},
		{ErrorRateLimit, true},
		{ErrorServer, true},
		{ErrorAuth, false},
		{ErrorProtocol, false},
		{ErrorInvalid, false},
	}
	for _, c := range cases {
		e := newError(c.kind, "x")
		if e.Kind != c.kind || e.Retryable != c.want {
			t.Fatalf("newError(%s).Retryable=%v want %v", c.kind, e.Retryable, c.want)
		}
		if got := e.Error(); got != "["+string(c.kind)+"] x" {
			t.Fatalf("Error()=%q", got)
		}
	}
}

// TestKindFromStatus：401 / 403 → auth；429 → rate_limit；5xx → server；其余（**含 4xx**）→
// protocol（I-132：2026-09-22 统一到 llm 侧既有口径——`invalid` 不再由状态码产生）。
func TestKindFromStatus(t *testing.T) {
	cases := map[int]ErrorKind{
		401: ErrorAuth, 403: ErrorAuth, 429: ErrorRateLimit,
		500: ErrorServer, 503: ErrorServer,
		400: ErrorProtocol, 404: ErrorProtocol, 409: ErrorProtocol,
		200: ErrorProtocol, 302: ErrorProtocol,
	}
	for status, want := range cases {
		if got := kindFromStatus(status); got != want {
			t.Fatalf("kindFromStatus(%d)=%s want %s", status, got, want)
		}
		e := newStatusError(status, "x")
		if e.Status != status || e.Kind != want || e.Retryable != retryableFor(want) {
			t.Fatalf("newStatusError(%d)=%+v want {kind:%s, retryable:%v}", status, e, want, retryableFor(want))
		}
	}
}

// TestToError：*Error 原样保留；context 超时 → timeout（可重试）；其它 → protocol（不可重试）。
func TestToError(t *testing.T) {
	orig := newError(ErrorServer, "5xx")
	if got := toError(orig); got != orig {
		t.Fatalf("已分类错误应原样返回，实得 %+v", got)
	}
	if got := toError(context.DeadlineExceeded); got.Kind != ErrorTimeout || !got.Retryable {
		t.Fatalf("DeadlineExceeded → %+v want timeout/可重试", got)
	}
	if got := toError(errors.New("boom")); got.Kind != ErrorProtocol || got.Retryable {
		t.Fatalf("未分类 → %+v want protocol/不可重试", got)
	}
	// 包装错误（%w）：应仍识别为 *Error。
	wrapped := toError(errors.Join(errors.New("outer"), orig))
	if wrapped != orig {
		t.Fatalf("errors.As 识别失败：%+v", wrapped)
	}
}

// TestRetryWait：Retry-After 优先；否则指数退避（1s/2s/4s…）且封顶；不可重试 / nil → 0。
func TestRetryWait(t *testing.T) {
	if got := RetryWait(nil, 1); got != 0 {
		t.Fatalf("nil → %v want 0", got)
	}
	if got := RetryWait(newError(ErrorAuth, "401"), 1); got != 0 {
		t.Fatalf("不可重试 → %v want 0", got)
	}
	rate := newError(ErrorRateLimit, "429")
	if got := RetryWait(rate, 1); got != time.Second {
		t.Fatalf("attempt=1 → %v want 1s", got)
	}
	if got := RetryWait(rate, 2); got != 2*time.Second {
		t.Fatalf("attempt=2 → %v want 2s", got)
	}
	if got := RetryWait(rate, 3); got != 4*time.Second {
		t.Fatalf("attempt=3 → %v want 4s", got)
	}
	if got := RetryWait(rate, 0); got != time.Second {
		t.Fatalf("attempt<1 → %v want 1s（按第 1 次算）", got)
	}
	if got := RetryWait(rate, 40); got != retryMaxWait {
		t.Fatalf("attempt=40 → %v want 封顶 %v", got, retryMaxWait)
	}
	// Retry-After 优先于指数退避（含超过封顶的显式值）。
	after := newError(ErrorRateLimit, "429")
	after.RetryAfter = 3 * time.Second
	if got := RetryWait(after, 5); got != 3*time.Second {
		t.Fatalf("Retry-After 优先 → %v want 3s", got)
	}
}
