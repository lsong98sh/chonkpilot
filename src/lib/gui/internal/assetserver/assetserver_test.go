package assetserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// newTestServer 构造测试用服务（未监听）：dist 只含公共资产，`/themes.css` 走 embed 兜底
// （ExeDir 留空 → themefs.Load 直接返回内置副本）。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dist := fstest.MapFS{
		"shared/ck-theme.js":  &fstest.MapFile{Data: []byte("console.log('ck-theme');")},
		"shared/ck-theme.css": &fstest.MapFile{Data: []byte("[data-theme]{--ck-bg:#fff}")},
		"index.html":          &fstest.MapFile{Data: []byte("<html></html>")},
	}
	s, err := New(Options{Addr: "127.0.0.1:0", DistFS: dist})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("New 返回 nil（127.0.0.1:0 应可构造）")
	}
	return s
}

// do 直调 handler（不经网卡）；host 为空 → 用缺省合法 Host（`127.0.0.1:5670`，
// 与真实访问同源形态；httptest 缺省的 `example.com` 会被 Host 校验拒掉）。
func do(s *Server, method, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if host == "" {
		host = "127.0.0.1:5670"
	}
	req.Host = host
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// TestWhitelistHit：白名单命中 → 200 + 正确 Content-Type + no-store/nosniff + 非空体。
func TestWhitelistHit(t *testing.T) {
	s := newTestServer(t)
	cases := []struct{ path, contentType string }{
		{"/themes.css", "text/css; charset=utf-8"},
		{"/shared/ck-theme.js", "application/javascript; charset=utf-8"},
		{"/shared/ck-theme.css", "text/css; charset=utf-8"},
	}
	for _, c := range cases {
		rec := do(s, http.MethodGet, c.path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d want 200", c.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, c.contentType) {
			t.Fatalf("%s Content-Type=%q want %q", c.path, got, c.contentType)
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Fatalf("%s Cache-Control=%q 缺 no-store", c.path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s X-Content-Type-Options=%q want nosniff", c.path, got)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s 响应体为空", c.path)
		}
	}
}

// TestNotFound：白名单外 / 目录 / 以 `/` 结尾 → 404（无目录服务）。
func TestNotFound(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{
		"/",
		"/index.html",
		"/shared/",
		"/shared/nope.js",
		"/shared/ck-theme.js/",
		"/shared/plugins/x.js",
		"/plugin-ui/x/index.html",
	} {
		if rec := do(s, http.MethodGet, p, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d want 404", p, rec.Code)
		}
	}
}

// TestTraversalForbidden：含 `..` / 编码后的 `%2e` / 反斜杠 → 403。
func TestTraversalForbidden(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{
		"/shared/../themes.css",
		"/shared/..%2fck-theme.js",
		"/shared/%2e%2e/ck-theme.js",
		"/shared/%2E%2E/ck-theme.js",
		"/shared/..%5Cck-theme.js",
		"/../themes.css",
	} {
		if rec := do(s, http.MethodGet, p, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d want 403", p, rec.Code)
		}
	}
}

// TestMethodNotAllowed：非 GET/HEAD → 405 + `Allow: GET, HEAD`。
func TestMethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := do(s, m, "/themes.css", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d want 405", m, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Fatalf("%s Allow=%q want %q", m, got, "GET, HEAD")
		}
	}
}

// TestHostValidation：仅 127.0.0.1 / localhost / [::1]（含端口）；其余 403（防 DNS rebinding）。
func TestHostValidation(t *testing.T) {
	s := newTestServer(t)
	allowed := []string{"127.0.0.1:5670", "localhost:5670", "[::1]:5670", "127.0.0.1", "localhost", "[::1]"}
	for _, h := range allowed {
		if rec := do(s, http.MethodGet, "/themes.css", h); rec.Code != http.StatusOK {
			t.Fatalf("Host=%s status=%d want 200", h, rec.Code)
		}
	}
	denied := []string{"evil.com", "evil.com:5670", "127.0.0.1.evil.com", "10.0.0.1:5670", "[::2]:5670"}
	for _, h := range denied {
		if rec := do(s, http.MethodGet, "/themes.css", h); rec.Code != http.StatusForbidden {
			t.Fatalf("Host=%q status=%d want 403", h, rec.Code)
		}
	}
	// 空 Host（HTTP/1.0 或畸形请求）→ 拒。
	req := httptest.NewRequest(http.MethodGet, "/themes.css", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("空 Host status=%d want 403", rec.Code)
	}
}

// TestNonLoopbackRefused：非 loopback 监听地址 → 拒启（返回明确错误，不静默降级）。
func TestNonLoopbackRefused(t *testing.T) {
	dist := fstest.MapFS{"shared/ck-theme.js": &fstest.MapFile{Data: []byte("x")}}
	for _, addr := range []string{"0.0.0.0:5670", ":5670", "192.168.1.9:5670", "[::]:5670"} {
		s, err := New(Options{Addr: addr, DistFS: dist})
		if err == nil || s != nil {
			t.Fatalf("addr=%s 非 loopback 应拒启，却得到 s=%v err=%v", addr, s, err)
		}
	}
}

// TestDisabled：`off` / 空 / `-` → 不启动（New 返回 nil, nil）。
func TestDisabled(t *testing.T) {
	dist := fstest.MapFS{"shared/ck-theme.js": &fstest.MapFile{Data: []byte("x")}}
	for _, addr := range []string{"", "  ", "off", " off ", "-"} {
		if !Disabled(addr) {
			t.Fatalf("Disabled(%q) 应为 true", addr)
		}
		s, err := New(Options{Addr: addr, DistFS: dist})
		if err != nil {
			t.Fatalf("addr=%q 关闭态不应报错：%v", addr, err)
		}
		if s != nil {
			t.Fatalf("addr=%q 关闭态应返回 nil 服务", addr)
		}
	}
	if Disabled("127.0.0.1:5670") {
		t.Fatal("正常地址不应判为关闭")
	}
}

// TestHeadNoBody：HEAD → 200 且无 body（头齐备，含 Content-Length）。
func TestHeadNoBody(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{"/themes.css", "/shared/ck-theme.js"} {
		rec := do(s, http.MethodHead, p, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("HEAD %s status=%d want 200", p, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("HEAD %s body=%q want 空", p, rec.Body.String())
		}
		if rec.Header().Get("Content-Type") == "" || rec.Header().Get("Content-Length") == "" {
			t.Fatalf("HEAD %s 缺 Content-Type / Content-Length", p)
		}
	}
}

// TestStartAndServe：真实 loopback 监听 → 可服务（Start/Addr/Shutdown 闭环）。
func TestStartAndServe(t *testing.T) {
	s := newTestServer(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()
	resp, err := http.Get("http://" + s.Addr() + "/shared/ck-theme.css")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("status=%d body=%q", resp.StatusCode, string(body))
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("Cache-Control=%q", resp.Header.Get("Cache-Control"))
	}
}

// TestNormalizeAddr：裸端口 / localhost 归一为确定性 loopback。
func TestNormalizeAddr(t *testing.T) {
	cases := map[string]string{
		"5670":              "127.0.0.1:5670",
		"localhost:5670":    "127.0.0.1:5670",
		"127.0.0.1:5670":    "127.0.0.1:5670",
		"[::1]:5670":        "[::1]:5670",
		" 127.0.0.1:5670  ": "127.0.0.1:5670",
	}
	for in, want := range cases {
		if got := normalizeAddr(in); got != want {
			t.Fatalf("normalizeAddr(%q)=%q want %q", in, got, want)
		}
	}
}
