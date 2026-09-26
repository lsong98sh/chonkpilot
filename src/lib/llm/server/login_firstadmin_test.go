// 首启一次性初始化（admin）——**消息驱动**：Emit 发 `login-register` / `login-in` →
// 断言 v.Result（`{ok, user}` / `{ok:false, error}`）+ 角色（user.role）+ auth 库读回。
//
// 缺口（本文件修的对象）：`auth.allowRegister=false` + **空用户库** = 无任何登录路径（锁死）。
// 新口径（61 §4.6 待登记；实现见 chonkpilot-data/auth/local.go RegisterWithPolicy）：
//   - 用户库为空 → `login-register` **放行（无视 allowRegister）**，首个用户记 `role=admin`；
//   - 用户库非空 → 恢复按 `allowRegister` 判定（true 放行 role=user / false 拒 `login-failed`）；
//   - 并发两个注册 → **只有一个成功**（空库判定与写入首个用户在同一 bbolt 写事务内，原子）。
package server

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/auth"
)

// userRoleOf 取 login-* 应答里 user 对象的 role（缺失 → ""）。
func userRoleOf(res map[string]any) string {
	user, _ := res["user"].(map[string]any)
	role, _ := user["role"].(string)
	return role
}

// disableRegister 关闭 `auth.allowRegister`（对外暴露口径）。
func disableRegister(t *testing.T, s *Server) {
	t.Helper()
	off := false
	s.opts.AllowRegister = &off
	if s.allowRegister() {
		t.Fatal("allowRegister 应为 false")
	}
}

// TestLoginRegisterFirstUserAdminDespiteDisabled（用例 1）：**空库 + allowRegister=false** →
// 首个注册**成功**且 `role=admin`（首启一次性初始化；否则锁死无登录路径）。
func TestLoginRegisterFirstUserAdminDespiteDisabled(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	disableRegister(t, s)

	res := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": "admin0", "password": "p@ss1", "token_kind": "session",
	})
	if res["ok"] != true {
		t.Fatalf("空库 + 关闭注册：首个注册应放行，got %v", res)
	}
	if role := userRoleOf(res); role != auth.RoleAdmin {
		t.Fatalf("首个用户角色应为 %s，got %q（%v）", auth.RoleAdmin, role, res)
	}
	// 数据层读回同口径（令牌 → 身份：role=admin）
	token, _ := res["token"].(string)
	u, err := s.auth.ByToken(token, auth.TokenSession)
	if err != nil || u.Role != auth.RoleAdmin {
		t.Fatalf("令牌解析身份角色应为 %s，got %+v err=%v", auth.RoleAdmin, u, err)
	}
	// 首启用户可直接登录（自动登录之外的独立路径）
	if in := loginResult(t, s, SubjectLoginIn, map[string]any{
		"username": "admin0", "password": "p@ss1", "token_kind": "session",
	}); in["ok"] != true || userRoleOf(in) != auth.RoleAdmin {
		t.Fatalf("首启 admin 登录应成功且 role=%s，got %v", auth.RoleAdmin, in)
	}
}

// TestLoginRegisterRejectedWhenDisabledAndNotEmpty（用例 2）：**非空库 + allowRegister=false**
// → 注册**被拒**，错误码 = 既有 `login-failed`（不新造码）。
func TestLoginRegisterRejectedWhenDisabledAndNotEmpty(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)

	// 空库 + 关闭注册 → 首个注册放行（造成「库非空」的前置）
	disableRegister(t, s)
	if first := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": "admin0", "password": "p@ss1", "token_kind": "session",
	}); first["ok"] != true || userRoleOf(first) != auth.RoleAdmin {
		t.Fatalf("首个注册应放行并记 admin，got %v", first)
	}

	// 非空 + 关闭 → 拒（既有 login-failed）
	res := loginResult(t, s, SubjectLoginRegister, map[string]any{"username": "bob", "password": "p@ss1"})
	if res["ok"] != false || res["error"] != loginErrFailed {
		t.Fatalf("非空库 + 关闭注册应 {ok:false,error:%s}，got %v", loginErrFailed, res)
	}
	// 被拒者未落库 → 无法登录
	if in := loginResult(t, s, SubjectLoginIn, map[string]any{"username": "bob", "password": "p@ss1"}); in["error"] != loginErrFailed {
		t.Fatalf("被拒用户登录应 %s，got %v", loginErrFailed, in)
	}
}

// TestLoginRegisterLaterUserRoleUser（用例 3）：**非空库 + allowRegister=true** → 注册成功
// 且 `role=user`（首启之后的用户一律 user）。
func TestLoginRegisterLaterUserRoleUser(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)

	first := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": "admin0", "password": "p@ss1", "token_kind": "session",
	})
	if first["ok"] != true || userRoleOf(first) != auth.RoleAdmin {
		t.Fatalf("首个注册应为 admin，got %v", first)
	}
	if !s.allowRegister() {
		t.Fatal("缺省 allowRegister 应为 true")
	}

	res := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": "bob", "password": "p@ss1", "token_kind": "session",
	})
	if res["ok"] != true {
		t.Fatalf("非空库 + 开放注册应放行，got %v", res)
	}
	if role := userRoleOf(res); role != auth.RoleUser {
		t.Fatalf("非首个用户角色应为 %s，got %q（%v）", auth.RoleUser, role, res)
	}
	// 数据层读回同口径
	token, _ := res["token"].(string)
	u, err := s.auth.ByToken(token, auth.TokenSession)
	if err != nil || u.Role != auth.RoleUser {
		t.Fatalf("令牌解析身份角色应为 %s，got %+v err=%v", auth.RoleUser, u, err)
	}
}

// TestLoginRegisterConcurrentFirstUserOnlyOneWins（用例 4）：**并发两个注册请求**同刻打
// （空库 + allowRegister=false）→ **只有一个成功**（该者 role=admin），另一个按
// `login-failed` 拒绝 —— 空库判定与写入首个用户在同一 bbolt 写事务内，原子。
func TestLoginRegisterConcurrentFirstUserOnlyOneWins(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	disableRegister(t, s)

	const n = 2
	type outcome struct {
		res map[string]any
		err error
	}
	out := make([]outcome, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 同刻起跑：两请求真并发走进注册路径（Emit 在调用方 goroutine 内派发）
			v := s.bus.Emit(context.Background(), SubjectLoginRegister, jb(map[string]any{
				"username": fmt.Sprintf("racer-%d", i), "password": "p@ss1", "token_kind": "session",
			})).Wait()
			out[i].res, _ = v.Result.(map[string]any)
			out[i].err = v.Err()
		}(i)
	}
	close(start)
	wg.Wait()

	okCount, rejected := 0, 0
	var winner map[string]any
	for i, o := range out {
		if o.res == nil {
			t.Fatalf("racer-%d 无应答（err=%v）", i, o.err)
		}
		if o.res["ok"] == true {
			okCount++
			winner = o.res
			continue
		}
		if o.res["error"] != loginErrFailed {
			t.Fatalf("落败者应为 {ok:false,error:%s}，got %v", loginErrFailed, o.res)
		}
		rejected++
	}
	if okCount != 1 || rejected != n-1 {
		t.Fatalf("并发注册应只有一个成功：ok=%d rejected=%d（%v）", okCount, rejected, out)
	}
	if role := userRoleOf(winner); role != auth.RoleAdmin {
		t.Fatalf("唯一成功者应为首启 admin，got role=%q（%v）", role, winner)
	}
	// 库已非空：再注册必拒（关闭状态），且胜者令牌可用
	if again := loginResult(t, s, SubjectLoginRegister, map[string]any{"username": "late", "password": "p@ss1"}); again["error"] != loginErrFailed {
		t.Fatalf("空位已被首启用户占用，再注册应 %s，got %v", loginErrFailed, again)
	}
	token, _ := winner["token"].(string)
	if u, err := s.auth.ByToken(token, auth.TokenSession); err != nil || u.Role != auth.RoleAdmin {
		t.Fatalf("胜者令牌应可解析且 role=%s，got %+v err=%v", auth.RoleAdmin, u, err)
	}
}
