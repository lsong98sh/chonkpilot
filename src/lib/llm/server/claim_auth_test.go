// instance-claim 的**鉴权与权限校验**（阶段 2b-2；61-消息一览 §4.1 ① · §4.6）——**消息驱动**：
// Emit 发 `instance-claim`（凭据由入口注入的内部字段 `token` 承载）→ 断言 v.Result 的
// `{ok:false, error}` / 成功应答 + `instance-register` 广播 + auth 库的项目/关联落库。
//
// 覆盖（对齐 61 §4.6 两码语义）：
//   - 无令牌 / 令牌无效 → `instance-unauthorized`（不广播、不登记）；
//   - 有令牌但 work_dir 无权 → `instance-forbidden`（**不跳登录**）；
//   - 允许根下自建项目 → 通过（建 projects + access；他人再认领同目录 → forbidden）；
//   - `desktop` 形态 → **跳过鉴权与权限校验**（本机单用户，2a 口径保留）；
//   - `user` 提议与连接层身份不一致 → `instance-unauthorized`（客户端不自称，22 §1）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/auth"
)

// claimUser 经 MQ 注册一个用户，返回 (uid, 令牌)。
func claimUser(t *testing.T, s *Server, name, kind string) (string, string) {
	t.Helper()
	res := loginResult(t, s, SubjectLoginRegister, map[string]any{
		"username": name, "password": "p@ss1", "token_kind": kind,
	})
	user, _ := res["user"].(map[string]any)
	uid, _ := user["uid"].(string)
	token, _ := res["token"].(string)
	if res["ok"] != true || uid == "" || token == "" {
		t.Fatalf("注册应成功并下发令牌，got %v", res)
	}
	return uid, token
}

// claimOnce 发一次 instance-claim，返回 (应答 result, v.Err())。
func claimOnce(t *testing.T, s *Server, payload map[string]any) (map[string]any, error) {
	t.Helper()
	v := s.bus.Emit(context.Background(), SubjectInstanceClaim, jb(payload)).Wait()
	res, _ := v.Result.(map[string]any)
	return res, v.Err()
}

// TestClaimRequiresConnectionToken：gui / browser 形态下**无令牌 / 令牌无效** →
// `instance-unauthorized`（61 §4.6：未登录 → 前端跳登录）；不广播、不登记内存实例表。
func TestClaimRequiresConnectionToken(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	s.opts.Form = FormBrowser
	root := t.TempDir()
	s.opts.ProjectRoots = []string{root} // 目录本身在允许根下，只有令牌缺失 → 仍是 unauthorized
	got := claimBroadcasts(t, s)

	// ① 无令牌（入口未注入 / 未登录）
	res, err := claimOnce(t, s, map[string]any{
		"instance_id": "ins-noauth", "work_dir": root,
	})
	if res == nil || res["ok"] != false || res["error"] != claimErrUnauthorized {
		t.Fatalf("无令牌应 {ok:false,error:%s}，got %v", claimErrUnauthorized, res)
	}
	if err == nil || err.Error() != claimErrUnauthorized {
		t.Fatalf("错误码应同时进 errors，got %v", err)
	}

	// ② 令牌无效（伪造 / 已吊销）
	res2, _ := claimOnce(t, s, map[string]any{
		"instance_id": "ins-badtoken", "work_dir": root, "token": "not-a-token",
	})
	if res2 == nil || res2["error"] != claimErrUnauthorized {
		t.Fatalf("无效令牌应 %s，got %v", claimErrUnauthorized, res2)
	}

	assertNoBroadcast(t, got)
	if _, ok := s.im.Lookup("ins-noauth"); ok {
		t.Fatal("未认证不应写内存实例表")
	}
	if _, ok := s.im.Lookup("ins-badtoken"); ok {
		t.Fatal("令牌无效不应写内存实例表")
	}
}

// TestClaimForbiddenWithoutWorkDirAccess：**有有效令牌但 work_dir 无权** →
// `instance-forbidden`（**不跳登录**，提示换目录）；目录不在允许根下 → 不登记为项目。
func TestClaimForbiddenWithoutWorkDirAccess(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	s.opts.Form = FormBrowser
	allowed := t.TempDir()
	s.opts.ProjectRoots = []string{allowed}
	outside := t.TempDir() // 允许根之外 → 不得自建项目
	got := claimBroadcasts(t, s)

	_, token := claimUser(t, s, "alice", string(auth.TokenSession))

	// ① 不在允许根下 → forbidden
	res, err := claimOnce(t, s, map[string]any{
		"instance_id": "ins-outside", "token": token, "user": "alice", "work_dir": outside,
	})
	if res == nil || res["ok"] != false || res["error"] != claimErrForbidden {
		t.Fatalf("允许根外应 {ok:false,error:%s}，got %v（err=%v）", claimErrForbidden, res, err)
	}
	if err == nil || err.Error() != claimErrForbidden {
		t.Fatalf("错误码应同时进 errors，got %v", err)
	}
	// ② work_dir 为空（gui/browser 必填）→ forbidden（不臆造目录）
	res2, _ := claimOnce(t, s, map[string]any{"instance_id": "ins-nodir", "token": token})
	if res2 == nil || res2["error"] != claimErrForbidden {
		t.Fatalf("缺 work_dir 应 %s，got %v", claimErrForbidden, res2)
	}
	assertNoBroadcast(t, got)
	// 不得登记为项目（越权自登记被拦）
	if _, ok, err := s.authz.ProjectByPath(outside); err != nil || ok {
		t.Fatalf("允许根外目录不应登记为项目：ok=%v err=%v", ok, err)
	}
}

// TestClaimSelfCreatedProjectUnderAllowedRoot：**允许根下自建项目** → 通过：
// 建 projects 记录（name = 目录名、created_by = uid）+ access 关联；应答 work_dir 规范化、
// data_dir = `<用户数据根>/<uid>/`（61 §4.6：业务数据每用户一套库）；广播 instance-register；
// **他人再认领同目录**（项目已存在但无 access）→ forbidden。
func TestClaimSelfCreatedProjectUnderAllowedRoot(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	s.opts.Form = FormGui // gui = GUI 客户端 + 独立 server：与 browser 同等要求认证
	root := t.TempDir()
	proj := filepath.Join(root, "proj-a")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s.opts.ProjectRoots = []string{root}
	got := claimBroadcasts(t, s)

	uid, token := claimUser(t, s, "alice", string(auth.TokenRemember))

	res, err := claimOnce(t, s, map[string]any{
		"instance_id": "ins-a", "token": token, "user": "alice", "work_dir": proj,
	})
	if err != nil {
		t.Fatalf("允根下自建项目应通过，got err=%v res=%v", err, res)
	}
	if res["instance_id"] != "ins-a" || auth.NormalizeDir(res["work_dir"].(string)) != auth.NormalizeDir(proj) {
		t.Fatalf("应答应绑定该目录：%v", res)
	}
	wantData := filepath.Join(s.userDataRootPath(), uid)
	if res["data_dir"] != wantData {
		t.Fatalf("gui/browser 的 data_dir 应为每用户数据根 %s，got %v", wantData, res["data_dir"])
	}
	// 广播 instance-register（主题/payload 一字不改；data/filesys 据此绑定，G-21）
	m := drainClaimBroadcast(t, got)
	if m["instance_id"] != "ins-a" || auth.NormalizeDir(m["work_dir"].(string)) != auth.NormalizeDir(proj) {
		t.Fatalf("instance-register 广播不符：%v", m)
	}
	// 项目登记 + 关联落库（方案 A：自建即 owner）
	rec, ok, err := s.authz.ProjectByPath(proj)
	if err != nil || !ok {
		t.Fatalf("应登记 projects 记录：ok=%v err=%v", ok, err)
	}
	if rec.CreatedBy != uid || rec.Name != "proj-a" {
		t.Fatalf("projects 记录不符：%+v（uid=%s）", rec, uid)
	}
	if has, err := s.authz.HasAccess(uid, rec.ProjectID); err != nil || !has {
		t.Fatalf("应建 access 关联：has=%v err=%v", has, err)
	}
	// 重复认领幂等（不重复登记）
	if _, err := claimOnce(t, s, map[string]any{
		"instance_id": "ins-a", "token": token, "user": "alice", "work_dir": proj,
	}); err != nil {
		t.Fatalf("重复认领应成功，got %v", err)
	}
	if rec2, _, _ := s.authz.ProjectByPath(proj); rec2.ProjectID != rec.ProjectID {
		t.Fatalf("重复认领不应另建项目：%s vs %s", rec2.ProjectID, rec.ProjectID)
	}

	// 他人（bob）认领**同目录**：项目已存在但无 access → forbidden
	_, bobToken := claimUser(t, s, "bob", string(auth.TokenSession))
	resBob, _ := claimOnce(t, s, map[string]any{
		"instance_id": "ins-b", "token": bobToken, "user": "bob", "work_dir": proj,
	})
	if resBob == nil || resBob["error"] != claimErrForbidden {
		t.Fatalf("无 access 的用户应 %s，got %v", claimErrForbidden, resBob)
	}
}

// TestClaimUserProposalCrossCheck：payload 的 `user` 提议与**连接层身份**不一致 →
// `instance-unauthorized`（22 §1「客户端不自称」：身份以令牌为准，提议只作交叉校验）。
func TestClaimUserProposalCrossCheck(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	s.opts.Form = FormBrowser
	root := t.TempDir()
	s.opts.ProjectRoots = []string{root}

	uid, token := claimUser(t, s, "alice", string(auth.TokenSession))

	// 自称他人 → unauthorized（即便目录本身在允许根下）
	res, _ := claimOnce(t, s, map[string]any{
		"instance_id": "ins-x", "token": token, "user": "bob", "work_dir": root,
	})
	if res == nil || res["error"] != claimErrUnauthorized {
		t.Fatalf("user 提议与身份不一致应 %s，got %v", claimErrUnauthorized, res)
	}
	// 一致（用户名 / uid 均可）→ 通过
	for _, who := range []string{"alice", uid} {
		ok, err := claimOnce(t, s, map[string]any{
			"token": token, "user": who, "work_dir": root,
		})
		if err != nil {
			t.Fatalf("user=%q 应通过，got %v", who, err)
		}
		if ok["instance_id"] == "" {
			t.Fatalf("user=%q 应得到实例应答，got %v", who, ok)
		}
	}
}

// TestClaimDesktopSkipsAuth：`desktop`（Options.Form 空 = 桌面单体默认构建）→
// **跳过鉴权与权限校验**（本机单用户；61 §4.1 ① desktop 省略 user/work_dir）：
// 无令牌亦可认领，work_dir 取服务端启动参数；2a 的「提议不得改写入口绑定」判定保留。
func TestClaimDesktopSkipsAuth(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	if s.Form() != FormDesktop {
		t.Fatalf("默认形态应为 desktop，got %s", s.Form())
	}
	s.opts.WorkDir = testWorkDir
	s.opts.DataDir = filepath.Join(testWorkDir, ".chonkpilot")
	s.opts.ProjectRoots = []string{t.TempDir()} // 即便配了允许根，desktop 也不做权限校验
	got := claimBroadcasts(t, s)

	// ① 无令牌、无 work_dir → 成功（回落启动参数）
	res, err := claimOnce(t, s, map[string]any{"instance_id": "ins-sa"})
	if err != nil {
		t.Fatalf("desktop 免鉴权应成功，got %v", err)
	}
	if res["work_dir"] != testWorkDir {
		t.Fatalf("work_dir 应回落启动参数 %s，got %v", testWorkDir, res["work_dir"])
	}
	if m := drainClaimBroadcast(t, got); m["instance_id"] != "ins-sa" {
		t.Fatalf("应照发 instance-register：%v", m)
	}
	// ② 提议与绑定不一致 → forbidden（**不静默改写入口绑定**；2a 口径保留）
	res2, _ := claimOnce(t, s, map[string]any{
		"instance_id": "ins-sa2", "work_dir": filepath.Join(testWorkDir, "elsewhere"),
	})
	if res2 == nil || res2["error"] != claimErrForbidden {
		t.Fatalf("desktop 提议不一致应 %s，got %v", claimErrForbidden, res2)
	}
}

// TestAuthenticatedReadsConnectionToken：首屏 `authed` 的判定源（`Server.Authenticated`）
// —— 空令牌 false（且不产生 auth 文件）、有效令牌 true、登出后 false。
func TestAuthenticatedReadsConnectionToken(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServer(t, llm)
	t.Cleanup(data.Reset)
	s.opts.Form = FormBrowser

	if s.Authenticated("") {
		t.Fatal("空令牌应判未认证")
	}
	_, token := claimUser(t, s, "alice", string(auth.TokenSession))
	if !s.Authenticated(token) {
		t.Fatal("有效令牌应判已认证")
	}
	if s.Authenticated("bogus") {
		t.Fatal("无效令牌应判未认证")
	}
	// 登出入口（携带同一令牌）→ 令牌行清除 → 不再认证
	if out := loginResult(t, s, SubjectLoginOut, map[string]any{"token": token, "token_kind": "session"}); out["ok"] != true {
		t.Fatalf("登出应成功，got %v", out)
	}
	if s.Authenticated(token) {
		t.Fatal("登出后不应再判已认证")
	}
}
