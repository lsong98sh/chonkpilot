//go:build windows

package gui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doAction 直调动作面处理器（不经网卡）；header 为来源插件 id（空 = 不带）。
func doAction(h http.Handler, method, target, pluginIDHdr, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if pluginIDHdr != "" {
		req.Header.Set(pluginIDHeader, pluginIDHdr)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestParsePluginActionPath：`/plugins/<id>/api/<action>` 拆分 + 非动作形态拒绝。
func TestParsePluginActionPath(t *testing.T) {
	cases := []struct {
		path, id, action string
		ok               bool
	}{
		{"/plugins/codebase/api/ui.ping", "codebase", "ui.ping", true},
		{"/plugins/codebase/api/status.get", "codebase", "status.get", true},
		{"/plugins/codebase/api/", "", "", false},
		{"/plugins//api/x", "", "", false},
		{"/plugins/codebase/api/a/b", "", "", false},
		{"/plugins/codebase/api/..%2fplugin.json", "", "", false},
		{"/plugins/codebase/config.html", "", "", false},
		{"/plug-ins/codebase/api/x", "", "", false},
	}
	for _, c := range cases {
		id, action, ok := parsePluginActionPath(c.path)
		if ok != c.ok || id != c.id || action != c.action {
			t.Fatalf("%s → (%q,%q,%v) want (%q,%q,%v)", c.path, id, action, ok, c.id, c.action, c.ok)
		}
	}
}

// TestPluginActionOK：命中 → 200 + `{ok:true, result}` + no-store/nosniff；空 body = `{}`。
func TestPluginActionOK(t *testing.T) {
	var gotID, gotAction string
	var gotPayload map[string]any
	h := newPluginActionHandler(func(_ context.Context, id, action string, payload map[string]any) (any, error) {
		gotID, gotAction, gotPayload = id, action, payload
		return map[string]any{"v": 1}, nil
	})

	rec := doAction(h, http.MethodPost, "/plugins/codebase/api/ui.ping", "codebase", `{"a":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotID != "codebase" || gotAction != "ui.ping" {
		t.Fatalf("id=%q action=%q", gotID, gotAction)
	}
	if v, _ := gotPayload["a"].(float64); v != 1 {
		t.Fatalf("payload=%v", gotPayload)
	}
	if !strings.Contains(rec.Body.String(), `"v":1`) {
		t.Fatalf("body=%s（须原样回吐插件结果）", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control=%q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q", got)
	}

	// 空 body → `{}`（不视为错误）
	rec = doAction(h, http.MethodPost, "/plugins/codebase/api/ui.ping", "codebase", "")
	if rec.Code != http.StatusOK || len(gotPayload) != 0 {
		t.Fatalf("空 body status=%d payload=%v", rec.Code, gotPayload)
	}
}

// TestPluginActionIDMismatchForbidden：来源视图 id（X-CK-Plugin-Id）≠ 路径 id → 403，且拒绝为 404/200。
func TestPluginActionIDMismatchForbidden(t *testing.T) {
	calls := 0
	h := newPluginActionHandler(func(context.Context, string, string, map[string]any) (any, error) {
		calls++
		return map[string]any{"ok": true}, nil
	})
	for _, hdr := range []string{"", "other"} {
		rec := doAction(h, http.MethodPost, "/plugins/codebase/api/ui.ping", hdr, "{}")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("header=%q status=%d want 403", hdr, rec.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("id 不匹配不得透传插件（calls=%d）", calls)
	}
}

// TestPluginActionUpstreamErrors：未就绪 → 404；超时 → 504；未实现 → 501；其它 → 502（统一错误信封）。
func TestPluginActionUpstreamErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"未就绪", errors.New("插件未就绪: codebase(starting)"), http.StatusNotFound},
		{"未登记", errors.New("插件未登记: ghost"), http.StatusNotFound},
		{"超时", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"未实现", errors.New("method_not_found: nope"), http.StatusNotImplemented},
		{"其它", errors.New("boom"), http.StatusBadGateway},
	}
	for _, c := range cases {
		h := newPluginActionHandler(func(context.Context, string, string, map[string]any) (any, error) {
			return nil, c.err
		})
		rec := doAction(h, http.MethodPost, "/plugins/codebase/api/x", "codebase", "{}")
		if rec.Code != c.want {
			t.Fatalf("%s status=%d want %d", c.name, rec.Code, c.want)
		}
		if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), `"errors"`) {
			t.Fatalf("%s 错误信封=%s", c.name, rec.Body.String())
		}
	}
}

// TestPluginActionRequestErrors：GET → 405 + Allow；非 JSON 对象 → 400。
func TestPluginActionRequestErrors(t *testing.T) {
	h := newPluginActionHandler(func(context.Context, string, string, map[string]any) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	rec := doAction(h, http.MethodGet, "/plugins/codebase/api/ui.ping", "codebase", "")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Fatalf("GET status=%d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	for _, body := range []string{`[1]`, `"s"`, `not-json`} {
		rec = doAction(h, http.MethodPost, "/plugins/codebase/api/ui.ping", "codebase", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d want 400", body, rec.Code)
		}
	}
}

// TestPluginActionDisabled：未装配动作面 → 404。
func TestPluginActionDisabled(t *testing.T) {
	h := newPluginActionHandler(nil)
	rec := doAction(h, http.MethodPost, "/plugins/codebase/api/ui.ping", "codebase", "{}")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

// TestPluginRoutesWithActionFace：appHandler 分流 —— `/api/*` 归动作面，其它 `/plugins/<id>/*` 仍归资源面。
func TestPluginRoutesWithActionFace(t *testing.T) {
	actionCalls := 0
	h := &appHandler{
		pluginAssets: newPluginAssetHandler(func(_ context.Context, id, p string) ([]byte, bool, error) {
			if id == "codebase" && p == "config.html" {
				return []byte("<html>embedded</html>"), true, nil
			}
			return nil, false, nil
		}, func(string) string { return "1.0.0#1" }),
		pluginActions: newPluginActionHandler(func(context.Context, string, string, map[string]any) (any, error) {
			actionCalls++
			return map[string]any{"ok": true}, nil
		}),
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/plugins/codebase/api/ui.ping", strings.NewReader("{}")))
	// 无 X-CK-Plugin-Id → 403（但仍应走动作面，不落资源面 405/404）。
	if rec.Code != http.StatusForbidden || actionCalls != 0 {
		t.Fatalf("动作面 status=%d calls=%d", rec.Code, actionCalls)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/plugins/codebase/api/ui.ping", strings.NewReader("{}"))
	req.Header.Set(pluginIDHeader, "codebase")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || actionCalls != 1 {
		t.Fatalf("动作面 status=%d calls=%d", rec.Code, actionCalls)
	}

	// 资源面回归：同一 appHandler 下 GET 静态资源仍可用。
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugins/codebase/config.html", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>embedded</html>" {
		t.Fatalf("资源面 status=%d body=%q", rec.Code, rec.Body.String())
	}

	// 未装配动作面时 `/api/*` → 404（不落资源面）。
	h.pluginActions = nil
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/plugins/codebase/api/ui.ping", strings.NewReader("{}"))
	req.Header.Set(pluginIDHeader, "codebase")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未装配动作面 status=%d want 404", rec.Code)
	}
}
