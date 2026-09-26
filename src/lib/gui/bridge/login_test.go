// login 域入口承载（61-消息一览 §4.6；阶段 2b-1）——GUI 桥侧黑盒。
//
// 断言要点：**令牌不进前端 payload**（入口取走并剥除）；令牌由桥**持有并落文件**；
// 后续上行请求由入口从连接层取令牌注入；登出清内存 + 删文件。
package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// TestLoginCarriedByBridgeGUI：login-in 应答里的内部 token 由桥取走（前端 result 无 token）
// → 持有（内存 + 文件）→ 后续上行请求注入；login-out → 清内存 + 删文件。
func TestLoginCarriedByBridgeGUI(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	// 令牌文件重定向到临时目录（不污染 ~/chonkpilot）。
	rememberPathOverride = filepath.Join(t.TempDir(), rememberTokenFile)
	t.Cleanup(func() { rememberPathOverride = "" })

	// 服务端桩：login-in → {ok,user,token}；login-out / session-send 采集入口注入的 token。
	seen := make(chan map[string]any, 4)
	if _, err := bus.On("login-in", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		seen <- req
		v.Result = map[string]any{
			"ok": true, "user": map[string]any{"uid": "u-1", "username": "alice"},
			"token": "ckr1.fake-token", "token_kind": "remember",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.On("login-out", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		seen <- req
		v.Result = map[string]any{"ok": true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.On("session-send", 0, func(_ context.Context, _ string, v *mq.Value) error {
		var req map[string]any
		_ = json.Unmarshal(v.Payload, &req)
		seen <- req
		v.Result = map[string]any{"accepted": true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	b := New("ins-1", "wd", "dd", func(string) {}, bus)

	// ① login-in：桥注入 token_kind=remember + 空 token（尚未持有）
	result, errs := b.PublishEvent("login-in", `{"username":"alice","password":"p@ss1"}`)
	if len(errs) != 0 {
		t.Fatalf("login-in 不应有错误: %v", errs)
	}
	req := <-seen
	if req["token_kind"] != tokenKindRemember {
		t.Fatalf("桥应注入 token_kind=remember，got %v", req["token_kind"])
	}
	if _, has := req["token"]; !has {
		t.Fatal("桥应注入 token 字段（连接层；未持有 = 空）")
	}
	// **令牌不进前端 payload**
	res, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("login-in 应答异常: %v", result)
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
	// 持有（内存 + 文件）
	if got := b.authTokenValue(); got != "ckr1.fake-token" {
		t.Fatalf("桥应持有令牌，got %q", got)
	}
	if raw, err := os.ReadFile(rememberPathOverride); err != nil || string(raw) != "ckr1.fake-token" {
		t.Fatalf("免登录令牌应落文件：err=%v content=%q", err, string(raw))
	}

	// ② 后续上行请求携带令牌（入口从连接层取）
	if _, errs := b.PublishEvent("llm-send", `{"session":"s1","turn":"t1"}`); len(errs) != 0 {
		t.Fatalf("llm-send 不应有错误: %v", errs)
	}
	sent := <-seen
	if sent["token"] != "ckr1.fake-token" {
		t.Fatalf("后续请求应注入桥持有的令牌，got %v", sent["token"])
	}

	// ③ login-out：把持有的令牌交服务端吊销 + 清内存/文件
	if _, errs := b.PublishEvent("login-out", `{}`); len(errs) != 0 {
		t.Fatalf("login-out 不应有错误: %v", errs)
	}
	out := <-seen
	if out["token"] != "ckr1.fake-token" {
		t.Fatalf("登出应带上持有的令牌供吊销，got %v", out["token"])
	}
	if b.authTokenValue() != "" {
		t.Fatal("登出后桥不应再持有令牌")
	}
	if _, err := os.Stat(rememberPathOverride); !os.IsNotExist(err) {
		t.Fatalf("登出后应删除令牌文件，stat err=%v", err)
	}
}

// TestAuthedReadsHeldToken：首屏 `authed` 的判定源（`bridge.Authed`）——
// 读桥**持有**的令牌 → 校验有效性（宿主注入 llm server 的 Authenticated）；
// 未注入判定回调 / 未持有 / 令牌无效 → false（61 §4.6：服务端读凭证判定，不读 UA）。
func TestAuthedReadsHeldToken(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("mq.New: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })
	rememberPathOverride = filepath.Join(t.TempDir(), rememberTokenFile)
	t.Cleanup(func() { rememberPathOverride = "" })

	b := New("ins-auth", "wd", "dd", func(string) {}, bus)
	// 未接线（宿主未注入判定）→ 未认证（**不静默放行**）
	if b.Authed() {
		t.Fatal("未注入判定回调应判未认证")
	}
	b.SetAuthCheck(func(token string) bool { return token == "tok-ok" })
	// 未持有令牌 → 未认证
	if b.Authed() {
		t.Fatal("未持有令牌应判未认证")
	}
	// 持有有效令牌 → 已认证
	b.holdAuthToken("tok-ok")
	if !b.Authed() {
		t.Fatal("持有有效令牌应判已认证")
	}
	// 持有无效令牌（如已吊销）→ 未认证
	b.holdAuthToken("tok-bad")
	if b.Authed() {
		t.Fatal("无效令牌应判未认证")
	}
	// 登出清令牌 → 未认证
	b.clearAuthToken()
	if b.Authed() {
		t.Fatal("登出后应判未认证")
	}
}
