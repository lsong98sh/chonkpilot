//go:build windows

package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestReadRequestBodyLimit 请求体读取上限（D-26）：limit 内正常读入；超出 limit 判
// tooLarge（serveWebResource 据此回 413），limit 复用响应方向 maxRecordedBody 常量。
func TestReadRequestBodyLimit(t *testing.T) {
	// ① 恰等于 limit → 正常，不判超限
	body, tooLarge := readRequestBody(strings.NewReader("0123456789abcdef"), 16)
	if tooLarge || string(body) != "0123456789abcdef" {
		t.Fatalf("limit 内应完整读入且不超限：tooLarge=%v body=%q", tooLarge, body)
	}
	// ② limit+1 字节 → tooLarge（LimitReader(limit+1) 探测出越界）
	body, tooLarge = readRequestBody(strings.NewReader("0123456789abcdefg"), 16)
	if !tooLarge {
		t.Fatalf("超出 limit 应判 tooLarge：body=%q", body)
	}
	// ③ 空体 → 正常空读
	body, tooLarge = readRequestBody(strings.NewReader(""), 16)
	if tooLarge || len(body) != 0 {
		t.Fatalf("空体应正常返回：tooLarge=%v body=%q", tooLarge, body)
	}
	// ④ 生产口径：maxRecordedBody 常量本身可作 limit 传入（编译期校验类型口径）
	if _, tooLarge := readRequestBody(strings.NewReader("x"), maxRecordedBody); tooLarge {
		t.Fatal("maxRecordedBody limit 下单字节不应判超限")
	}
}

// TestSameOriginSharedAssets：宿主的**同源**资产路由仍直连可用（UIBRIDGE-1 验收口径）——
// `/themes.css`（themefs：磁盘优先 + embed 兜底）与 `/shared/*`（前端 dist 的 shared/）
// 由 appHandler 直接提供，**不经**新增的静态资产端口（端口启动失败也不影响这里）。
func TestSameOriginSharedAssets(t *testing.T) {
	h := &appHandler{distFS: fstest.MapFS{
		"shared/ck-theme.js":  &fstest.MapFile{Data: []byte("console.log('ck-theme');")},
		"shared/ck-theme.css": &fstest.MapFile{Data: []byte("[data-theme]{--ck-bg:#fff}")},
	}}
	cases := []struct{ path, wantCT string }{
		{"/themes.css", "text/css; charset=utf-8"},
		{"/shared/ck-theme.js", "application/javascript"},
		{"/shared/ck-theme.css", "text/css"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d want 200", c.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, c.wantCT) {
			t.Fatalf("%s Content-Type=%q want 前缀 %q", c.path, got, c.wantCT)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s 响应体为空", c.path)
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("%s 缺 no-store 缓存头", c.path)
		}
	}
}
