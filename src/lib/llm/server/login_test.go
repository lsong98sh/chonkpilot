// login 域（认证，61-消息一览 §4.6；阶段 2b-1）——**消息驱动**：Emit 发 `login-register` /
// `login-in` / `login-out` → 断言 v.Result（请求-响应应答）+ auth 库落库效果 + 数据目录。
//
// 覆盖：注册（UUID / {ok,user} / 令牌下发 / 建数据目录 / 自动登录）+ 重名 + 密码错 +
// 登出（清令牌行）+ `auth.allowRegister=false` + 令牌类别（session / remember 自证型）。
//
// 注：服务端应答里的 `token` / `token_kind` 是**给入口**的内部字段（入口取走后剥除，
// 前端只看到 `{ok, user}`）—— 本用例直连总线，故可见；入口侧剥除见 httpapi / bridge。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/auth"
)

// loginResult 发一条 login-* 并取应答 v.Result（无应答 → 失败）。
func loginResult(t *testing.T, s *Server, subject string, payload map[string]any) map[string]any {
	t.Helper()
	v := s.bus.Emit(context.Background(), subject, jb(payload)).Wait()
	res, _ := v.Result.(map[string]any)
	if res == nil {
		t.Fatalf("%s 无应答（v.Result 为空）", subject)
	}
	return res
}

// TestLoginRegisterInOutOverMQ：注册 → 自动登录（{ok,user} + 令牌）+ 建数据目录；
// 登录（正确 / 密码错）；重名注册 → login-username-taken；登出 → 清令牌行。
func TestLoginRegisterInOutOverMQ(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)

	// ① login-register（61 §4.6 payload {username, password}；token_kind 为入口注入的内部字段）
	res := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": "alice", "password": "p@ss1", "token_kind": "session",
	})
	if res["ok"] != true {
		t.Fatalf("注册应答应为 {ok:true,...}，got %v", res)
	}
	user, _ := res["user"].(map[string]any)
	uid, _ := user["uid"].(string)
	if user["username"] != "alice" || len(uid) != 36 {
		t.Fatalf("注册应答 user 异常: %v", res["user"])
	}
	token, _ := res["token"].(string)
	if token == "" || res["token_kind"] != "session" {
		t.Fatalf("注册应下发会话令牌，got token=%q kind=%v", token, res["token_kind"])
	}
	// 建数据目录 `<用户数据根>/<uid>/`（注册流程，61 §4.6）
	if fi, err := os.Stat(filepath.Join(s.userDataRoot, uid)); err != nil || !fi.IsDir() {
		t.Fatalf("未建用户数据目录 %s: err=%v", filepath.Join(s.userDataRoot, uid), err)
	}
	// 注册成功即自动登录 → 令牌可用
	if u, err := s.auth.ByToken(token, auth.TokenSession); err != nil || u.UID != uid {
		t.Fatalf("注册下发的令牌应可解析: %+v err=%v", u, err)
	}

	// ② login-in 密码错 → {ok:false, error:"login-failed"}
	bad := loginResult(t, s, SubjectLoginIn, map[string]any{
		"username": "alice", "password": "bad", "token_kind": "session",
	})
	if bad["ok"] != false || bad["error"] != loginErrFailed {
		t.Fatalf("密码错应 {ok:false,error:%s}，got %v", loginErrFailed, bad)
	}
	// 用户名不存在（不区分 → 同码）
	miss := loginResult(t, s, SubjectLoginIn, map[string]any{"username": "bob", "password": "p"})
	if miss["error"] != loginErrFailed {
		t.Fatalf("用户名不存在应 %s，got %v", loginErrFailed, miss)
	}

	// ③ login-in 正确 → {ok,user} + 令牌下发
	ok := loginResult(t, s, SubjectLoginIn, map[string]any{
		"username": "alice", "password": "p@ss1", "token_kind": "session",
	})
	if ok["ok"] != true || ok["token"] == "" {
		t.Fatalf("登录应成功并下发令牌，got %v", ok)
	}
	sessTok, _ := ok["token"].(string)

	// ④ 重名注册 → login-username-taken
	dup := loginResult(t, s, SubjectLoginRegister, map[string]any{"username": "alice", "password": "other"})
	if dup["ok"] != false || dup["error"] != loginErrUsernameTaken {
		t.Fatalf("重名应 {ok:false,error:%s}，got %v", loginErrUsernameTaken, dup)
	}

	// ⑤ login-out（入口注入 token）→ {ok:true} + 令牌行清除（该令牌不可再用）
	out := loginResult(t, s, SubjectLoginOut, map[string]any{"token": sessTok, "token_kind": "session"})
	if out["ok"] != true {
		t.Fatalf("登出应答应为 {ok:true}，got %v", out)
	}
	if _, err := s.auth.ByToken(sessTok, auth.TokenSession); err == nil {
		t.Fatal("登出后令牌应失效")
	}
	// 幂等：无令牌登出恒成功
	if out2 := loginResult(t, s, SubjectLoginOut, map[string]any{}); out2["ok"] != true {
		t.Fatalf("无令牌登出应 {ok:true}，got %v", out2)
	}
}

// TestLoginRegisterDisabled：配置 `auth.allowRegister=false` + **用户库非空** → 注册返回既有
// 错误码 `login-failed`（**不新造字段**，61 §4.6 只定义两码）；关闭注册**不影响既有用户登录**。
//
// （**空库首启例外**：`allowRegister=false` + 空用户库 = 无任何登录路径 → 首个注册放行并记
// `admin`，见 TestLoginRegisterFirstUserAdminDespiteDisabled / 数据层 RegisterWithPolicy。）
func TestLoginRegisterDisabled(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)

	// 先以默认配置（allowRegister 缺省 true）落一个用户 → 用户库非空
	seed := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": "alice", "password": "p@ss1", "token_kind": "session",
	})
	if seed["ok"] != true {
		t.Fatalf("种子注册应成功，got %v", seed)
	}

	off := false
	s.opts.AllowRegister = &off
	res := loginResult(t, s, SubjectLoginRegister, map[string]any{"username": "bob", "password": "p@ss1"})
	if res["ok"] != false || res["error"] != loginErrFailed {
		t.Fatalf("非空库关闭注册后应 {ok:false,error:%s}，got %v", loginErrFailed, res)
	}
	// 被拒用户未落库（登录亦失败 → 仍走既有错误码）
	in := loginResult(t, s, SubjectLoginIn, map[string]any{"username": "bob", "password": "p@ss1"})
	if in["ok"] != false || in["error"] != loginErrFailed {
		t.Fatalf("被拒用户登录应 {ok:false,error:%s}，got %v", loginErrFailed, in)
	}
	// 关闭注册只挡**新注册**：既有用户仍可登录
	relogin := loginResult(t, s, SubjectLoginIn, map[string]any{"username": "alice", "password": "p@ss1"})
	if relogin["ok"] != true {
		t.Fatalf("关闭注册不应影响既有用户登录，got %v", relogin)
	}
}

// TestLoginRememberTokenKind：`token_kind=remember`（desktop 口径）→ 自证型 AES-GCM 令牌
// （含密码哈希）；可解析；类别不符 → 无效。
func TestLoginRememberTokenKind(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)

	res := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": "carol", "password": "p@ss1", "token_kind": "remember",
	})
	token, _ := res["token"].(string)
	if !strings.HasPrefix(token, "ckr1.") || res["token_kind"] != "remember" {
		t.Fatalf("免登录令牌应为自证型（ckr1. 前缀），got %q kind=%v", token, res["token_kind"])
	}
	if u, err := s.auth.ByToken(token, auth.TokenRemember); err != nil || u.Username != "carol" {
		t.Fatalf("自证令牌应可解析: %+v err=%v", u, err)
	}
	if _, err := s.auth.ByToken(token, auth.TokenSession); err == nil {
		t.Fatal("类别不符（remember 当 session 用）应无效")
	}
	// 登出 → 吊销
	if out := loginResult(t, s, SubjectLoginOut, map[string]any{"token": token, "token_kind": "remember"}); out["ok"] != true {
		t.Fatalf("登出应答应为 {ok:true}，got %v", out)
	}
	if _, err := s.auth.ByToken(token, auth.TokenRemember); err == nil {
		t.Fatal("登出后免登录令牌应失效")
	}
}
