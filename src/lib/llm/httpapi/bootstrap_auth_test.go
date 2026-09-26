// 首屏注入的**认证标记**（阶段 2b-2；61 §4.6「首屏注入」）——browser 入口（httpapi）侧。
//
// 断言要点：`window.__ck` 的 `requireAuth` = 形态决定（browser → true）；`authed` =
// **服务端读连接层凭证判定**（cookie `chonkpilot-token` → AuthCheck），**不读 UA、
// 不接受前端自报**；未接线（AuthCheck=nil）→ 未认证。
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// bootstrapServer 起一个带 AuthCheck 的最小入口（静态面 = 临时 index.html）。
func bootstrapServer(t *testing.T, check func(token string) bool) http.Handler {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><head></head></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	s := New(bus, Options{Bus: bus, WorkDir: t.TempDir(), WebRoot: root, Addr: "127.0.0.1:0", AuthCheck: check})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s.Handler()
}

// getIndex 取首屏 HTML（可带会话 cookie）。
func getIndex(t *testing.T, h http.Handler, cookie string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: cookie})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / status=%d", rr.Code)
	}
	return rr.Body.String()
}

// TestBootstrapAuthFlags：requireAuth=true（browser 形态）；authed 由**服务端读 cookie
// 判定**（有效/token → true；无 cookie / 无效 token → false）；instanceId 仍注入入口绑定值。
func TestBootstrapAuthFlags(t *testing.T) {
	h := bootstrapServer(t, func(token string) bool { return token == "good-token" })

	// ① 未认证（无 cookie）→ requireAuth=true、authed=false、instanceId 仍在
	html := getIndex(t, h, "")
	for _, want := range []string{`"requireAuth":true`, `"authed":false`, `"form":"browser"`, `window.__chonkpilotInstanceId="`} {
		if !strings.Contains(html, want) {
			t.Fatalf("首屏缺少 %q；got=%s", want, html)
		}
	}
	// ② 无效令牌 → 仍未认证
	bad := getIndex(t, h, "bogus")
	if !strings.Contains(bad, `"authed":false`) {
		t.Fatalf("无效令牌应判未认证：%s", bad)
	}
	// ③ 有效令牌（cookie 由入口从连接层取）→ 已认证
	ok := getIndex(t, h, "good-token")
	if !strings.Contains(ok, `"authed":true`) {
		t.Fatalf("有效令牌应判已认证：%s", ok)
	}
}

// TestBootstrapUnwiredAuthCheckIsUnauthenticated：未注入 AuthCheck（宿主未接线）→
// `authed` 恒 false（**不静默放行**：未接线 = 未认证，前端进登录视图）。
func TestBootstrapUnwiredAuthCheckIsUnauthenticated(t *testing.T) {
	h := bootstrapServer(t, nil)
	html := getIndex(t, h, "any-token")
	if !strings.Contains(html, `"authed":false`) || !strings.Contains(html, `"requireAuth":true`) {
		t.Fatalf("未接线应判未认证且要求认证：%s", html)
	}
}

// TestPublishInjectsConnectionToken：上行载荷的 `token` **由入口从连接层注入并覆盖**
// 前端自报值（22 §1「客户端不自称」）——instance-claim 凭它解析身份。
func TestPublishInjectsConnectionToken(t *testing.T) {
	_, bus, base := newTestServer(t, "")
	for _, tc := range []struct{ typ, subject string }{
		{"instance-claim", "instance-claim"}, // 认证域相关的 claim：凭连接层令牌解析身份
		{"llm-send", "session-send"},         // 普通方法面：同一注入口径（连接层为准）
	} {
		seen := make(chan map[string]any, 1)
		sub, err := bus.On(tc.subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var req map[string]any
			if json.Unmarshal(v.Payload, &req) == nil && req != nil {
				select {
				case seen <- req:
				default:
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("subscribe %s: %v", tc.subject, err)
		}
		// 前端自报 token=fake → 入口以连接层（cookie）令牌**覆盖**
		_, _ = postPublish(t, base, tc.typ, `{"work_dir":"C:\\w","token":"fake"}`, "conn-token")
		select {
		case req := <-seen:
			if req["token"] != "conn-token" {
				t.Fatalf("%s：入口应以连接层令牌覆盖自报值，got %v", tc.typ, req["token"])
			}
		default:
			t.Fatalf("%s 未到达总线（subject=%s）", tc.typ, tc.subject)
		}
		_ = sub.Unsubscribe()
	}
}
