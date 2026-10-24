//go:build windows

package gui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// pluginAssetFixture 是测试用资源表（id → path → 内容）。
func assetFixture() map[string][]byte {
	return map[string][]byte{
		"config.html": []byte("<html>config</html>"),
		"status.html": []byte("<html>status</html>"),
		"style.css":   []byte("body{}"),
	}
}

// doAsset 直调处理器（不经网卡）。
func doAsset(h http.Handler, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestPluginAssetHit：命中 → 200 + 宿主推断的 MIME + no-store / nosniff + 原样字节。
func TestPluginAssetHit(t *testing.T) {
	files := assetFixture()
	calls := 0
	h := newPluginAssetHandler(func(_ context.Context, id, p string) ([]byte, bool, error) {
		calls++
		if id != "codebase" {
			return nil, false, nil
		}
		b, ok := files[p]
		return b, ok, nil
	}, func(string) string { return "1.0.0#1" })

	cases := []struct{ path, ctype string }{
		{"/plugins/codebase/config.html", "text/html; charset=utf-8"},
		{"/plugins/codebase/style.css", "text/css; charset=utf-8"},
	}
	for _, c := range cases {
		rec := doAsset(h, http.MethodGet, c.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d want 200", c.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != c.ctype {
			t.Fatalf("%s Content-Type=%q want %q", c.path, got, c.ctype)
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Fatalf("%s Cache-Control=%q 缺 no-store", c.path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s X-Content-Type-Options=%q", c.path, got)
		}
		want := files[strings.TrimPrefix(c.path, "/plugins/codebase/")]
		if rec.Body.String() != string(want) {
			t.Fatalf("%s body=%q want %q", c.path, rec.Body.String(), string(want))
		}
	}
	if calls != len(cases) {
		t.Fatalf("fetch 调用 %d 次，期望 %d", calls, len(cases))
	}
}

// TestPluginAssetCacheHit：同 (id, path, version) 重复请求只拉取一次。
func TestPluginAssetCacheHit(t *testing.T) {
	calls := 0
	h := newPluginAssetHandler(func(_ context.Context, _, _ string) ([]byte, bool, error) {
		calls++
		return []byte("x"), true, nil
	}, func(string) string { return "1.0.0#1" })
	for i := 0; i < 3; i++ {
		if rec := doAsset(h, http.MethodGet, "/plugins/codebase/a.html"); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次 status=%d", i+1, rec.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("fetch 调用 %d 次，期望 1（应命中缓存）", calls)
	}
}

// TestPluginAssetCacheVersionBump：版本分量变化（插件重启 / 升级）→ 缓存失效并重新拉取。
func TestPluginAssetCacheVersionBump(t *testing.T) {
	version := "1.0.0#1"
	body := "old"
	calls := 0
	h := newPluginAssetHandler(func(_ context.Context, _, _ string) ([]byte, bool, error) {
		calls++
		return []byte(body), true, nil
	}, func(string) string { return version })

	if rec := doAsset(h, http.MethodGet, "/plugins/codebase/a.html"); rec.Body.String() != "old" {
		t.Fatalf("首取 body=%q", rec.Body.String())
	}
	version, body = "1.0.0#2", "new"
	rec := doAsset(h, http.MethodGet, "/plugins/codebase/a.html")
	if rec.Body.String() != "new" {
		t.Fatalf("版本变化后 body=%q want new", rec.Body.String())
	}
	if calls != 2 {
		t.Fatalf("fetch 调用 %d 次，期望 2", calls)
	}
}

// TestPluginAssetNotFound：资源未命中 / 插件未就绪（fetch 报错）→ 404（本段不区分）。
func TestPluginAssetNotFound(t *testing.T) {
	h := newPluginAssetHandler(func(_ context.Context, _, p string) ([]byte, bool, error) {
		if p == "boom.html" {
			return nil, false, errors.New("插件未就绪: codebase(starting)")
		}
		return nil, false, nil
	}, func(string) string { return "v#1" })
	for _, p := range []string{"/plugins/codebase/nope.html", "/plugins/codebase/boom.html"} {
		if rec := doAsset(h, http.MethodGet, p); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d want 404", p, rec.Code)
		}
	}
	// 缺 id / 缺 path → 404。
	for _, p := range []string{"/plugins/", "/plugins/codebase", "/plugins/codebase/"} {
		if rec := doAsset(h, http.MethodGet, p); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d want 404", p, rec.Code)
		}
	}
}

// TestPluginAssetTraversalForbidden：`..` / `%2e` / 反斜杠 / 绝对路径形态 → 403。
func TestPluginAssetTraversalForbidden(t *testing.T) {
	h := newPluginAssetHandler(func(_ context.Context, _, _ string) ([]byte, bool, error) {
		return []byte("secret"), true, nil
	}, func(string) string { return "v#1" })
	for _, p := range []string{
		"/plugins/codebase/../plugin.json",
		"/plugins/codebase/..%2fplugin.json",
		"/plugins/codebase/%2e%2e/plugin.json",
		"/plugins/codebase/%2E%2E/plugin.json",
		"/plugins/codebase/a%5Cb.html",
		"/plugins/codebase//abs.html",
	} {
		if rec := doAsset(h, http.MethodGet, p); rec.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d want 403", p, rec.Code)
		}
	}
}

// TestPluginRoutesInAppHandler：`/plugins/<id>/<path>` 经 appHandler 路由可服务；
// 旧 `/plugin-ui/<id>/*`（宿主直读磁盘 webui）**已下线**（一律 404）；
// `/plugin-ui/_sdk/plugin.js`（共享桥）行为不变（200 + 逐字节一致）。
func TestPluginRoutesInAppHandler(t *testing.T) {
	h := &appHandler{
		distFS: fstest.MapFS{},
		pluginAssets: newPluginAssetHandler(func(_ context.Context, id, p string) ([]byte, bool, error) {
			// 与真实插件同口径：接受 manifest 的 `webui/` 前缀形态。
			p = strings.TrimPrefix(p, "webui/")
			if id == "codebase" && p == "config.html" {
				return []byte("<html>embedded</html>"), true, nil
			}
			return nil, false, nil
		}, func(string) string { return "1.0.0#1" }),
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugins/codebase/config.html", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>embedded</html>" {
		t.Fatalf("新路由 status=%d body=%q", rec.Code, rec.Body.String())
	}

	// 旧路由已下线：任何 `/plugin-ui/<id>/…`（含默认 index.html 形态）→ 404。
	for _, p := range []string{
		"/plugin-ui/codebase/config.html",
		"/plugin-ui/codebase/",
		"/plugin-ui/_sdk/other.js",
		"/plugin-ui/_sdk/",
	} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("旧路由 %s status=%d want 404", p, rec.Code)
		}
	}

	// 共享桥 SDK 保留：内容逐字节一致 + no-store（插件页面 <script src="/plugin-ui/_sdk/plugin.js">）。
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin-ui/_sdk/plugin.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("SDK status=%d want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/javascript; charset=utf-8" {
		t.Fatalf("SDK Content-Type=%q", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("SDK Cache-Control=%q 缺 no-store", got)
	}
	if rec.Body.String() != pluginUIBridgeJS {
		t.Fatalf("SDK 内容与 pluginUIBridgeJS 不一致（len=%d want %d）", rec.Body.Len(), len(pluginUIBridgeJS))
	}

	rec = httptest.NewRecorder()
	// 新路由同时接受 manifest 的 `webui/` 前缀形态（ui.views[].entry）。
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugins/codebase/webui/config.html", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>embedded</html>" {
		t.Fatalf("webui/ 前缀形态 status=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestPluginAssetDisabled / Method：未装配插件平台 → 404；非 GET/HEAD → 405 + Allow。
func TestPluginAssetDisabledAndMethod(t *testing.T) {
	h := newPluginAssetHandler(nil, nil)
	if rec := doAsset(h, http.MethodGet, "/plugins/codebase/a.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("未装配 status=%d want 404", rec.Code)
	}

	h2 := newPluginAssetHandler(func(_ context.Context, _, _ string) ([]byte, bool, error) {
		return []byte("x"), true, nil
	}, nil)
	rec := doAsset(h2, http.MethodPost, "/plugins/codebase/a.html")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow=%q", got)
	}
	// HEAD → 200 无 body。
	rec = doAsset(h2, http.MethodHead, "/plugins/codebase/a.html")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD status=%d body=%q", rec.Code, rec.Body.String())
	}
}
