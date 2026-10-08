// login 域**入口承载**（61-消息一览 §4.6；阶段 2b-1）。
//
// 本入口（GUI 形态：desktop / gui）是**免登录令牌**的承载方：
//   - 上行：注入 `token_kind=remember` + 桥**持有**的令牌（连接层）—— **不采信前端 payload
//     里的身份字段**；
//   - 下行：服务端应答里的内部 `token` 字段在此**取走并剥除** → **令牌不进前端 payload**
//     （前端只看到 61 §4.6 的 `{ok, user}`）；
//   - 拿到令牌 → 桥持有（内存）+ 落 `~/chonkpilot/remember-token`（61 §4.6：免登录令牌存
//     `~/chonkpilot`；自证型 → 服务端重启后仍可解析）；
//   - `login-out` → 清内存 + 删文件（令牌行由服务端清）。
//
// 注：认证域三个主题**不经** `frontMethodSubjects` 分派（需持有/落盘令牌，故走专用分支）。
package bridge

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
)

const (
	// tokenKindRemember 是本入口注入的令牌类别（GUI = 免登录令牌，自证型）。
	tokenKindRemember = "remember"
	// rememberTokenFile 是免登录令牌文件名（61 §4.6：落 `~/chonkpilot` 下）。
	rememberTokenFile = "remember-token"
)

// loginSubjects 认证域上行 type → 相对主题（同名，61 §4.6）。
var loginSubjects = map[string]string{
	msgkeys.TopicLoginRegister: msgkeys.TopicLoginRegister,
	msgkeys.TopicLoginIn:       msgkeys.TopicLoginIn,
	msgkeys.TopicLoginOut:      msgkeys.TopicLoginOut,
}

// authTokenValue 取桥当前持有的令牌（空 = 未持有）。
func (b *Bridge) authTokenValue() string {
	b.authMu.Lock()
	defer b.authMu.Unlock()
	return b.authToken
}

// SetAuthCheck 注入「令牌有效性判定」回调（宿主 = GUI main 注入 llm server 的
// `Authenticated`）—— 供首屏注入 `authed`（61 §4.6：**服务端读凭证判定**，不读 UA、
// 不接受前端自报）。未注入（nil）→ `Authed()` 恒 false（未认证）。
func (b *Bridge) SetAuthCheck(f func(token string) bool) {
	b.authMu.Lock()
	b.authCheck = f
	b.authMu.Unlock()
}

// Authed 判定本连接（静默形态）是否已认证：读桥**持有**的令牌（连接层）→ 校验其有效性。
// 空令牌 / 未注入判定 / 令牌无效 → false（61 §4.6：`authed` 由服务端读凭证判定）。
func (b *Bridge) Authed() bool {
	b.authMu.Lock()
	f, token := b.authCheck, b.authToken
	b.authMu.Unlock()
	if f == nil || token == "" {
		return false
	}
	return f(token)
}

// loginEvent 处理认证域上行：注入内部字段（token_kind / 桥持有令牌）→ 发布 →
// 取走并剥除内部 token → 持有/落文件（登出则清除）。
func (b *Bridge) loginEvent(subject, typ, payloadJSON string) (any, []error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	m["token_kind"] = tokenKindRemember
	m["token"] = b.authTokenValue()
	raw, err := json.Marshal(m)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, []error{err}
	}
	result, errs := b.publishV(subject, string(raw)) // string = 原样字节（[]byte 会被 json 编码成 base64）
	issued := takeInternalToken(result)              // 取走内部 token（返回前端前必须剥除）
	switch {
	case typ == msgkeys.TopicLoginOut:
		b.clearAuthToken() // 登出：清内存 + 删文件（令牌行由服务端清）
	case issued != "":
		b.holdAuthToken(issued)
	}
	return result, errs
}

// injectAuth 给**上行**方法面载荷补入口持有的令牌（连接层；**无条件覆盖**前端自报的
// 同名字段 —— 连接层为准；未持有 = 置空）。
//
// ⚠️ **绝不用于下行/前端投递路径**（compat.go 的 `injectInstance` 是给前端 emit 用的，
// 那里注入令牌 = 泄漏）。见 61 §4.6「令牌由入口承载、不下发前端」。
func (b *Bridge) injectAuth(payloadJSON string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil {
		return payloadJSON
	}
	m["token"] = b.authTokenValue()
	raw, err := json.Marshal(m)
	if err != nil {
		return payloadJSON
	}
	return string(raw)
}

// takeInternalToken 从服务端应答里**取走**内部令牌字段（`token` / `token_kind`）。
func takeInternalToken(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	token, _ := m["token"].(string)
	delete(m, "token")
	delete(m, "token_kind")
	return token
}

// holdAuthToken 持有令牌：内存 + 落 `~/chonkpilot/remember-token`（0600）。
// 落盘失败只告警（本次会话仍可用内存值），不阻断登录。
func (b *Bridge) holdAuthToken(token string) {
	b.authMu.Lock()
	b.authToken = token
	b.authMu.Unlock()
	path, err := rememberTokenPath()
	if err != nil {
		slog.Warn("remember token path failed", "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Warn("remember token mkdir failed", "err", err)
		return
	}
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		slog.Warn("remember token write failed", "err", err)
	}
}

// clearAuthToken 清除令牌（内存 + 文件）。
func (b *Bridge) clearAuthToken() {
	b.authMu.Lock()
	b.authToken = ""
	b.authMu.Unlock()
	if path, err := rememberTokenPath(); err == nil {
		_ = os.Remove(path)
	}
}

// loadRememberToken 启动时恢复免登录令牌（自证型 → 服务端重启后仍可解析；本次不自动重登，
// 由后续请求带出）。无文件 → 不动。
func (b *Bridge) loadRememberToken() {
	path, err := rememberTokenPath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if token := strings.TrimSpace(string(raw)); token != "" {
		b.authMu.Lock()
		b.authToken = token
		b.authMu.Unlock()
	}
}

// rememberPathOverride 是免登录令牌文件路径的**测试重定向**（空 = 走 `~/chonkpilot/remember-token`）。
// 生产恒为空；仅本包测试用于避免污染真实用户主目录。
var rememberPathOverride string

// rememberTokenPath 返回免登录令牌文件路径（61 §4.6：`~/chonkpilot` 下）。
func rememberTokenPath() (string, error) {
	if rememberPathOverride != "" {
		return rememberPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "chonkpilot", rememberTokenFile), nil
}
