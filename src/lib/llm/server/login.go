// login 域（认证，61-消息一览 §4.6；阶段 2b-1 后端核心）。
//
// 三个**请求-响应**方法面（前端 type = 相对主题同名；结果经 publish promise 回发起者）：
//
//	login-register {username, password} → {ok, user} + 令牌下发（注册成功即自动登录）
//	login-in       {username, password} → {ok, user} + 令牌下发
//	login-out      {}                   → {ok}（清令牌行 + 清 cookie）
//
// **令牌不进前端 payload**：应答里的 `token` 字段是**给入口**的内部字段，入口（browser =
// httpapi `Set-Cookie` / GUI = 桥持有并落文件）取走后必须从返回前端的 result 中剥除
// （见 chonkpilot-llm/httpapi/login.go、chonkpilot-gui/bridge/login.go）。本包只负责
// 生成与写回，**不**决定承载方式。
//
// 身份一律**不采信前端 payload 的身份字段**：当前令牌由入口从连接层取（cookie / 桥持有）
// 后以内部字段 `token` 注入（同 instance_id 由入口注入的既有口径）。
//
// 存储：auth 库（与业务三级库分离的单文件，见 chonkpilot-data/auth）；密码 bcrypt；
// 令牌只落哈希。业务数据根 = `<用户数据根>/<uid>/`（注册时建目录）。
//
// 阶段 2b-2（本文件同时承载**认证域共用助手**）：
//   - 形态判定 `Form()` / `RequireAuth()`（61 §4.6：desktop=false；gui/browser=true）；
//   - 连接层令牌 → 身份 `userByToken()` 与 `Authenticated()`（入口首屏 `authed` 判定用）；
//   - 用户数据根 `userDataDir()`（业务数据每用户一套库，61 §4.6）。
//
// `instance-claim` 的 `work_dir` 归属校验（权限判定）在 claim.go。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/auth"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

// login 域主题（相对主题；chonk. 前缀由总线注入，61 §4.6）。
const (
	SubjectLoginRegister = msgkeys.TopicLoginRegister
	SubjectLoginIn       = msgkeys.TopicLoginIn
	SubjectLoginOut      = msgkeys.TopicLoginOut
)

// 形态取值（61 §4.6：三形态 = desktop / gui / browser；desktop = 桌面单体、gui = GUI 客户端
// + 独立 server）。
const (
	FormDesktop = "desktop"
	FormGui     = "gui"
	FormBrowser = "browser"
)

// Form 返回本实例的**运行形态**（Options.Form，由入口/宿主注入；空 / 未知 = desktop，
// 即桌面单体默认构建口径）。**不读 UA**（UA 客户端可伪造，仅可作日志诊断，22 §1）。
func (s *Server) Form() string {
	switch strings.ToLower(strings.TrimSpace(s.opts.Form)) {
	case FormGui:
		return FormGui
	case FormBrowser:
		return FormBrowser
	default:
		return FormDesktop
	}
}

// RequireAuth 是否要求认证（61 §4.6：**形态决定** —— desktop=false 本机单用户；
// gui / browser=true）。入口据此注入首屏 `window.__ck.requireAuth`。
func (s *Server) RequireAuth() bool { return s.Form() != FormDesktop }

// userByToken 解析**入口从连接层取**的令牌（browser = cookie / GUI = 桥持有）→ 身份。
//
// 空令牌 / 无效 / 已吊销 → `auth.ErrUnauthorized`（61 §4.6 `instance-unauthorized`）。
// kind 空 = 依次尝试会话令牌（browser）与免登录令牌（desktop）—— 令牌行内已记类别，
// 逐类解析不降低强度（同一令牌不可能两类都命中）。**不接受 payload 自报身份**（22 §1）。
func (s *Server) userByToken(token, kind string) (auth.User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return auth.User{}, auth.ErrUnauthorized
	}
	if err := s.ensureAuth(); err != nil {
		return auth.User{}, err
	}
	kinds := []auth.TokenKind{auth.TokenKind(strings.TrimSpace(kind))}
	if kinds[0] == "" {
		kinds = []auth.TokenKind{auth.TokenSession, auth.TokenRemember}
	}
	for _, k := range kinds {
		if u, err := s.auth.ByToken(token, k); err == nil && u.UID != "" {
			return u, nil
		}
	}
	return auth.User{}, auth.ErrUnauthorized
}

// Authenticated 判定入口持有的令牌是否有效（browser = cookie / GUI = 桥持有）——
// 供入口计算首屏 `window.__ck.authed`（61 §4.6：**服务端读凭证判定**，不读 UA、
// 不接受前端自报）。空令牌 → false（且**不打开 auth 库**：未登录的首屏不产生 auth 文件）。
func (s *Server) Authenticated(token string) bool {
	u, err := s.userByToken(token, "")
	return err == nil && u.UID != ""
}

// userDataRootPath 读用户数据根（ensureAuth 打开时确定；空 = 未打开 / 未配置）。
func (s *Server) userDataRootPath() string {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	return s.userDataRoot
}

// userDataDir 返回该用户的数据根 `<用户数据根>/<uid>/`（61 §4.6：业务数据**每用户一套库**，
// 放实例侧 → 天然隔离，无需在任何表加 user 字段）。未配置 → ""（调用方自行回落）。
func (s *Server) userDataDir(uid string) string {
	root := s.userDataRootPath()
	if strings.TrimSpace(root) == "" || strings.TrimSpace(uid) == "" {
		return ""
	}
	return filepath.Join(root, uid)
}

// 错误码（61 §4.6；复用既有 {ok,error} 信封，不新增字段）。
const (
	loginErrFailed        = "login-failed"         // 用户名或密码错（亦用作注册关闭/库不可用）
	loginErrUsernameTaken = "login-username-taken" // 注册重名
)

// loginReq 是 login-* 请求载荷。
//
// 消息面字段（61 §4.6）= `{username, password}`（login-out 为 `{}`）；
// `token_kind` / `token` 是**入口注入**的内部字段（非消息面，同 instance_id 既有口径）：
//   - token_kind：入口形态决定令牌类别（browser → session（缺省）；GUI 桥 → remember）；
//   - token：入口从**连接层**取到的当前令牌（cookie / 桥持有），供登出吊销与后续身份解析
//     —— 服务端**不采信**前端 payload 里的身份字段。
type loginReq struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	TokenKind string `json:"token_kind"`
	Token     string `json:"token"`
}

// onLoginRegister 自助注册（61 §4.6）：生成 UUID(UID) → 写用户表 → 建数据目录 → 自动登录。
//
// 注册**放行判定**交给数据层的注册策略面（`auth.RegisterPolicy`，见下方 register）：
// `allowRegister=false` 时**空用户库仍放行**（**首启一次性初始化** —— 否则「关闭注册 + 空库」
// = 无任何登录路径，锁死），且该首个用户记为 `admin`；用户库非空则恢复按 allowRegister 判定。
func (s *Server) onLoginRegister(_ context.Context, _ string, v *mq.Value) error {
	var req loginReq
	_ = json.Unmarshal(v.Payload, &req)
	if err := s.ensureAuth(); err != nil {
		return s.loginFail(v, loginErrFailed)
	}
	u, err := s.register(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrUsernameTaken) {
			return s.loginFail(v, loginErrUsernameTaken)
		}
		// 关闭注册且用户库非空（或库不可用）→ **不新造字段**，复用既有 login-failed
		// （61 §4.6 只定义 login-failed / login-username-taken 两码）。
		logf("[chonkpilot-server] login-register 被拒/失败（allowRegister=%v）: %v\n", s.allowRegister(), err)
		return s.loginFail(v, loginErrFailed)
	}
	// 建数据目录 `<用户数据根>/<uid>/`（业务数据每用户一套库，放实例侧；61 §4.6）。
	if err := s.ensureUserDataDir(u.UID); err != nil {
		logf("[chonkpilot-server] login-register uid=%s 建数据目录失败: %v\n", u.UID, err)
		return s.loginFail(v, loginErrFailed)
	}
	return s.loginOK(v, u, req.TokenKind)
}

// register 走认证门面的**注册策略面**（`auth.RegisterPolicy`）：把「用户库是否为空（首启）」
// 与 `auth.allowRegister` 一并交**数据层在同一写事务内**判定 —— 保证「空库判定 + 写入首个
// 用户」**原子**（并发两个注册请求只成功一个，见 auth.Local.RegisterWithPolicy）。
//
// 门面未实现策略面（可替换实现：OS 账号 / LDAP / OIDC）→ 回落**旧行为**（关闭即拒）。
func (s *Server) register(username, password string) (auth.User, error) {
	allow := s.allowRegister()
	if rp, ok := s.auth.(auth.RegisterPolicy); ok {
		return rp.RegisterWithPolicy(username, password, allow)
	}
	if !allow {
		return auth.User{}, auth.ErrLoginFailed
	}
	return s.auth.Register(username, password)
}

// onLoginIn 登录（61 §4.6）：bcrypt 校验 → 令牌下发。
func (s *Server) onLoginIn(_ context.Context, _ string, v *mq.Value) error {
	var req loginReq
	_ = json.Unmarshal(v.Payload, &req)
	if err := s.ensureAuth(); err != nil {
		return s.loginFail(v, loginErrFailed)
	}
	u, err := s.auth.Verify(req.Username, req.Password)
	if err != nil {
		// 用户名错 / 密码错**不区分**（避免用户名枚举）。
		return s.loginFail(v, loginErrFailed)
	}
	return s.loginOK(v, u, req.TokenKind)
}

// onLoginOut 登出（61 §4.6）：清令牌行（入口负责清 cookie / 桥持有的令牌）。
func (s *Server) onLoginOut(_ context.Context, _ string, v *mq.Value) error {
	var req loginReq
	_ = json.Unmarshal(v.Payload, &req)
	if strings.TrimSpace(req.Token) == "" {
		// 无令牌可吊销（入口未持有）→ 无需打开 auth 库，恒成功（幂等）。
		v.Result = map[string]any{"ok": true}
		return nil
	}
	if err := s.ensureAuth(); err != nil {
		return s.loginFail(v, loginErrFailed)
	}
	if err := s.authTokens.RevokeToken(req.Token, auth.TokenKind(req.TokenKind)); err != nil {
		logf("[chonkpilot-server] login-out 吊销令牌失败: %v\n", err)
		return s.loginFail(v, loginErrFailed)
	}
	v.Result = map[string]any{"ok": true}
	return nil
}

// loginOK 写回登录成功应答：前端可见 = `{ok, user}`（61 §4.6）；`token` / `token_kind`
// 是**给入口**的内部字段（入口取走后从返回前端的 result 中剥除）。
//
// `user` 对象**增补 `role`**（`admin` / `user`；🆕 首启一次性初始化）—— 纯**增补**字段
// （payload 与信封不变；入口只剥除 token / token_kind，role 透传前端）。
func (s *Server) loginOK(v *mq.Value, u auth.User, kindRaw string) error {
	kind := auth.TokenKind(kindRaw)
	if kind == "" {
		kind = auth.TokenSession // 缺省 = 会话令牌（browser 口径；GUI 桥显式注入 remember）
	}
	token, err := s.authTokens.IssueToken(u, kind)
	if err != nil {
		logf("[chonkpilot-server] login 签发令牌失败: %v\n", err)
		return s.loginFail(v, loginErrFailed)
	}
	v.Result = map[string]any{
		"ok":         true,
		"user":       map[string]any{"uid": u.UID, "username": u.Username, "role": u.Role},
		"token":      token,
		"token_kind": string(kind),
	}
	return nil
}

// loginFail 写回登录失败（61 §4.6：复用既有 {ok,error} 信封，不新增字段）：
// Result = {ok:false, error:<code>} + 同名错误进 v.Errors（HTTP 侧 ok=false）。
func (s *Server) loginFail(v *mq.Value, code string) error {
	v.Result = map[string]any{"ok": false, "error": code}
	return errors.New(code)
}

// allowRegister 读配置 `auth.allowRegister`（默认 true；61 §4.6）。
func (s *Server) allowRegister() bool {
	if s.opts.AllowRegister == nil {
		return true
	}
	return *s.opts.AllowRegister
}

// ensureUserDataDir 建该用户的数据根目录 `<用户数据根>/<uid>/`（业务数据每用户一套库）。
func (s *Server) ensureUserDataDir(uid string) error {
	if strings.TrimSpace(s.userDataRoot) == "" {
		return errors.New("auth: 用户数据根未配置")
	}
	return os.MkdirAll(filepath.Join(s.userDataRoot, uid), 0o700)
}

// ensureAuth **惰性**打开 auth 库（与业务三级库**分离**的单文件）并装配认证门面
// （61 §4.6 · 22 §6.4）—— 仅在**首个 login-* 请求**时打开（未被使用的宿主不产生 auth 文件）。
//
// 路径：库 = `Options.AuthDBPath`，缺省 `<exe 目录>/auth.db`（app-server 的 exe 旁）；
// 用户数据根 = `Options.UserDataRoot`，缺省 `<exe 目录>/data`。
//
// 打开失败 → 返回 error（调用方按 `login-failed` 应答），不影响主路径
// （与 capability watcher / 任务层同口径：附属能力失败不阻断服务）。
func (s *Server) ensureAuth() error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.auth != nil {
		return nil
	}
	if s.authErr != nil {
		return s.authErr
	}
	dbPath := strings.TrimSpace(s.opts.AuthDBPath)
	root := strings.TrimSpace(s.opts.UserDataRoot)
	exeDir, err := exedir.Dir()
	if err != nil {
		exeDir = "."
	}
	if dbPath == "" {
		dbPath = filepath.Join(exeDir, "auth.db")
	}
	if root == "" {
		root = filepath.Join(exeDir, "data")
	}
	s.userDataRoot = root
	lib, err := auth.OpenLocal(dbPath)
	if err != nil {
		logf("[chonkpilot-server] auth 库打开失败（认证不可用，login-* 一律 %s）: %s: %v\n",
			loginErrFailed, dbPath, err)
		s.authErr = err
		return err
	}
	s.auth = lib       // Authenticator 门面（本地库实现；将来可换 OS 账号 / LDAP / OIDC）
	s.authTokens = lib // 令牌签发/吊销面（同上装配位）
	s.authz = lib      // 项目登记 + 用户-项目关联面（claim 归属校验用；同上装配位，2b-2）
	s.authStop = func() { _ = lib.Close() }
	logf("[chonkpilot-server] auth 库就绪: %s（用户数据根 %s；allowRegister=%v）\n",
		lib.Path(), root, s.allowRegister())
	return nil
}
