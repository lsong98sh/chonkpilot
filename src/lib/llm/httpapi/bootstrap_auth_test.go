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
	"time"

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
	s, bus, base := newTestServer(t, "")
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
		// 前端自报 token=fake → 入口以连接层（cookie）令牌**覆盖**。
		// B-30：普通方法面须带 instance_id（缺失即拒绝）；instance-claim 缺失 id 属豁免。
		payload := `{"work_dir":"C:\\w","token":"fake"}`
		if tc.typ != "instance-claim" {
			payload = `{"work_dir":"C:\\w","token":"fake","instance_id":` + strconvQuote(s.InstanceID()) + `}`
		}
		_, _ = postPublish(t, base, tc.typ, payload, "conn-token")
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

// TestPublishInstanceIDEnforced B-30：单字/点分方法面的 instance_id **入口强制绑定**（与
// bindData / bindFilesys / token 同口径）——① 自报他实例 id → 覆盖为本实例；② 缺失 → 报错
// 拒绝（errors 含 instance_id required，不进总线）；③ 唯一豁免 instance-claim 缺失 → 放行。
func TestPublishInstanceIDEnforced(t *testing.T) {
	s, bus, base := newTestServer(t, "")

	seen := make(chan map[string]any, 4)
	for _, subject := range []string{"session-send", "instance-claim"} {
		if _, err := bus.On(subject, 0, func(_ context.Context, _ string, v *mq.Value) error {
			var req map[string]any
			if json.Unmarshal(v.Payload, &req) == nil && req != nil {
				select {
				case seen <- req:
				default:
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// ① 自报他实例 id → 覆盖为本实例（而非拒绝）
	_, env1 := postPublish(t, base, "llm-send", `{"session":"s1","instance_id":"evil-instance"}`, "")
	if env1["ok"] != true {
		t.Fatalf("伪造 id 应被覆盖而非拒绝：%v", env1)
	}
	select {
	case req := <-seen:
		if req["instance_id"] != s.InstanceID() {
			t.Fatalf("自报他实例 id 应被覆盖：got=%v want=%v", req["instance_id"], s.InstanceID())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("伪造 id 请求未到达总线")
	}

	// ② 缺失 instance_id → 报错拒绝（errors 含 instance_id required；不进总线）
	_, env2 := postPublish(t, base, "llm-send", `{"session":"s1"}`, "")
	if env2["ok"] != false {
		t.Fatalf("缺失 instance_id 应拒绝：%v", env2)
	}
	errs, _ := env2["errors"].([]any)
	if len(errs) == 0 || !strings.Contains(errs[0].(string), "instance_id required") {
		t.Fatalf("缺失错误文案应含 instance_id required：%v", env2)
	}
	select {
	case req := <-seen:
		t.Fatalf("缺失 instance_id 的请求不得进总线：%v", req)
	case <-time.After(300 * time.Millisecond):
	}

	// ③ instance-claim 缺失 instance_id → 豁免放行
	_, env3 := postPublish(t, base, "instance-claim", `{"work_dir":"C:\\w"}`, "")
	if env3["ok"] != true {
		t.Fatalf("instance-claim 缺失 instance_id 应放行：%v", env3)
	}
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("instance-claim 未到达总线")
	}
}

// TestAuthMiddlewareProtectedFaces：browser 入口（AuthCheck 已接线）**服务端鉴权中间件** ——
// 未认证时对受保护面（写操作 `/publish`、下行 `/events`、`/dirs`）返回 401 且**不进总线**；
// 静态面与 `login-*` 上行**豁免**（登录视图须可加载 / 登录本身即入口）；有效令牌放行。
func TestAuthMiddlewareProtectedFaces(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><head></head></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, bus, base := newTestServerOpts(t, Options{
		WorkDir: t.TempDir(), DataDir: t.TempDir(), WebRoot: root,
		AuthCheck: func(token string) bool { return token == "good-token" },
	})

	get := func(path, cookie string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("new request %s: %v", path, err)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: authCookieName, Value: cookie})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// ① 受保护 GET 面：未认证 → 401；有效令牌 → 放行
	if code := get(eventsPath, ""); code != http.StatusUnauthorized {
		t.Fatalf("/events 未认证应 401，got %d", code)
	}
	if code := get(dirsPath, ""); code != http.StatusUnauthorized {
		t.Fatalf("/dirs 未认证应 401，got %d", code)
	}
	if code := get(dirsPath, "good-token"); code != http.StatusOK {
		t.Fatalf("/dirs 有效令牌应 200，got %d", code)
	}
	// ② 静态面 + shim 豁免（登录视图须可加载，否则把自己锁死）
	if code := get("/", ""); code != http.StatusOK {
		t.Fatalf("静态面未认证应仍可取（登录视图），got %d", code)
	}
	if code := get(shimPath, ""); code != http.StatusOK {
		t.Fatalf("shim 未认证应仍可取，got %d", code)
	}

	// ③ 写操作：未认证 → 401 且**不进总线**；有效令牌 → 放行
	reached := make(chan struct{}, 1)
	if _, err := bus.On("session-start", 0, func(_ context.Context, _ string, _ *mq.Value) error {
		select {
		case reached <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := postPublish(t, base, "llm-start", `{"session":"s1","q":"hi"}`, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证写操作应 401，got %d", resp.StatusCode)
	}
	select {
	case <-reached:
		t.Fatal("未认证写操作不得进总线")
	default:
	}

	// ④ login-* 上行豁免（未认证可达；无订阅方 → 恒 200 信封）
	if resp, _ := postPublish(t, base, "login-in", `{"username":"u","password":"p"}`, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login-in 未认证应可达（200），got %d", resp.StatusCode)
	}
	// ⑤ 有效令牌写操作放行
	if resp, _ := postPublish(t, base, "llm-start", `{"session":"s2","q":"hi"}`, "good-token"); resp.StatusCode != http.StatusOK {
		t.Fatalf("有效令牌写操作应 200，got %d", resp.StatusCode)
	}
}

// TestAuthMiddlewareDisabledWhenUnwired：未接线（AuthCheck=nil：desktop 形态不经本入口 /
// 未配置认证域）→ 中间件**不启用**（行为同改前，不因"无法校验"而锁死）。
func TestAuthMiddlewareDisabledWhenUnwired(t *testing.T) {
	_, _, base := newTestServer(t, t.TempDir())
	for _, path := range []string{eventsPath, dirsPath} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("未接线 %s 不应 401（desktop/未配置不受影响）", path)
		}
	}
}
