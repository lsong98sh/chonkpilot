// login 域入口承载（61-消息一览 §4.6；阶段 2b-1）——browser 入口（httpapi）侧。
//
// 断言要点：**令牌不进前端 payload**（入口取走并剥除）；会话令牌只经 `Set-Cookie`
// 承载（`HttpOnly`、**不设 Max-Age**）；后续请求由入口从连接层（cookie）取令牌注入；
// `login-out` 清 cookie。
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// postPublish 发一次上行（可带 cookie），返回响应与原始信封。
func postPublish(t *testing.T, base, typ, payload, cookie string) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"type": typ, "payload": payload})
	req, err := http.NewRequest(http.MethodPost, base+publishPath, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", authCookieName+"="+cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /publish: %v", err)
	}
	defer resp.Body.Close()
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode publish reply: %v", err)
	}
	return resp, env
}

// TestLoginCookieCarriedByHTTPEntry：login-in → Set-Cookie（HttpOnly、不设 Max-Age）+
// 前端 result 无 token；后续请求入口注入 cookie 令牌；login-out 清 cookie。
func TestLoginCookieCarriedByHTTPEntry(t *testing.T) {
	_, bus, base := newTestServer(t, "")

	seenLogin := make(chan map[string]any, 1)
	if _, err := bus.On("login-in", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		seenLogin <- req
		v.Result = map[string]any{
			"ok": true, "user": map[string]any{"uid": "u-1", "username": "alice"},
			"token": "sess-tok", "token_kind": "session",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seenLater := make(chan map[string]any, 1)
	if _, err := bus.On("session-send", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		seenLater <- req
		v.Result = map[string]any{"accepted": true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seenOut := make(chan map[string]any, 1)
	if _, err := bus.On("login-out", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		seenOut <- req
		v.Result = map[string]any{"ok": true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// ① login-in（无 cookie）：入口注入 token_kind=session + token=""（连接层取，尚未持有）
	resp, env := postPublish(t, base, "login-in", `{"username":"alice","password":"p@ss1"}`, "")
	req := <-seenLogin
	if req["token_kind"] != tokenKindSession {
		t.Fatalf("入口应注入 token_kind=session，got %v", req["token_kind"])
	}
	if _, has := req["token"]; !has {
		t.Fatal("入口应注入 token 字段（连接层；未持有 = 空）")
	}
	// 前端 result 里**不得**出现令牌（入口已取走并剥除）
	res, _ := env["result"].(map[string]any)
	if res == nil {
		t.Fatalf("login-in 应答异常: %v", env)
	}
	if _, leaked := res["token"]; leaked {
		t.Fatalf("令牌不得出现在给前端的 payload 中: %v", res)
	}
	if _, leaked := res["token_kind"]; leaked {
		t.Fatalf("内部 token_kind 不得下发前端: %v", res)
	}
	if res["ok"] != true {
		t.Fatalf("前端应看到 {ok:true,...}，got %v", res)
	}
	// 会话令牌只经 Set-Cookie（HttpOnly、不设 Max-Age = 关浏览器即失效）
	raw := strings.Join(resp.Header.Values("Set-Cookie"), "\n")
	if !strings.Contains(raw, authCookieName+"=sess-tok") || !strings.Contains(raw, "HttpOnly") {
		t.Fatalf("应下发 HttpOnly 会话 cookie，got %q", raw)
	}
	if strings.Contains(raw, "Max-Age") {
		t.Fatalf("会话 cookie **不得**设 Max-Age（关浏览器即失效），got %q", raw)
	}

	// ② 后续请求带 cookie → 入口从连接层取令牌注入
	_, _ = postPublish(t, base, "llm-send", `{"session":"s1","turn":"t1"}`, "sess-tok")
	if later := <-seenLater; later["token"] != "sess-tok" {
		t.Fatalf("后续请求应注入 cookie 令牌，got %v", later["token"])
	}

	// ③ login-out（带 cookie）→ 服务端可吊销该令牌 + 清 cookie
	resp3, env3 := postPublish(t, base, "login-out", `{}`, "sess-tok")
	if out := <-seenOut; out["token"] != "sess-tok" {
		t.Fatalf("登出应带上连接层令牌供吊销，got %v", out["token"])
	}
	if r3, _ := env3["result"].(map[string]any); r3 == nil || r3["ok"] != true {
		t.Fatalf("登出应答应为 {ok:true}，got %v", env3)
	}
	raw3 := strings.Join(resp3.Header.Values("Set-Cookie"), "\n")
	if !strings.Contains(raw3, authCookieName+"=") || !strings.Contains(raw3, "Max-Age=0") {
		t.Fatalf("登出应清 cookie（Max-Age=0），got %q", raw3)
	}
}
