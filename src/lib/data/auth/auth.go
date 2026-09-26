// Package auth 是认证数据层的**本地实现**（61-消息一览 §4.6 认证域 · 22-实例与会话标识 §6）。
//
// 定位：用户 · 项目 · 用户-项目关联 · 令牌四类元数据的**独立 auth 库**（本期 = bbolt 单文件，
// 默认 `auth.db`，位于 app-server 的 exe 旁）。它**属数据层**，但**与业务三级库
// （usr/prj/prjusr）分离** —— 将来可整体替换为远程数据库而业务库不动（22 §6.4）。
//
// 业务数据（每用户一套库）放**实例侧** `<用户数据根>/<uid>/`，不由本包管理（无 user 字段）。
//
// 密码哈希一律 **bcrypt**（`golang.org/x/crypto/bcrypt`）—— 61 §4.6 明确**不用 MD5**；
// 令牌只落**哈希**（可整行删除 = 吊销），不落明文。
package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
)

// TokenKind 是令牌类别（61 §4.6：会话令牌 = browser；免登录令牌 = desktop）。
type TokenKind string

const (
	// TokenSession 会话令牌（browser）：随机不透明串（32B base64url）→ 由**入口**下发
	// `Set-Cookie: chonkpilot-token=<v>`（HttpOnly、**不设 Max-Age** = 关浏览器即失效）。
	TokenSession TokenKind = "session"
	// TokenRemember 免登录令牌（desktop）：**自证型** AES-GCM 密文（含 用户名/UID/密码哈希）
	// → 由 GUI 入口落 `~/chonkpilot` 下文件并注入；含密码哈希使其天然支持「改密即踢下线」。
	TokenRemember TokenKind = "remember"
)

// 用户角色（🆕 首启一次性初始化）：`admin` = **用户库为空时的首个注册者**（首启初始化入口，
// 无视 `auth.allowRegister`）；`user` = 其余自助注册用户。缺字段视为 `user`（向后兼容旧记录）。
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// 错误码（61 §4.6；复用既有 `{ok, error}` 信封，**不新增字段**）。
var (
	// ErrLoginFailed 用户名或密码错（61 §4.6 `login-failed`）。
	ErrLoginFailed = errors.New("login-failed")
	// ErrUsernameTaken 注册重名（61 §4.6 `login-username-taken`）。
	ErrUsernameTaken = errors.New("login-username-taken")
	// ErrUnauthorized 未登录 / 令牌无效或已吊销（61 §4.6 `instance-unauthorized`）。
	ErrUnauthorized = errors.New("instance-unauthorized")
	// ErrInvalidCredential 用户名或密码为空（非消息面错误码：内部参数校验，登录域归 `login-failed`）。
	ErrInvalidCredential = errors.New("auth: 用户名或密码不能为空")
)

// User 是认证域的用户视图（uid = 服务端生成的 UUID；**不含**密码哈希）。
type User struct {
	UID       string `json:"uid"`
	Username  string `json:"username"`
	Role      string `json:"role,omitempty"` // admin / user（缺省 = user；🆕 首启初始化见 RoleAdmin）
	CreatedAt int64  `json:"created_at"`
}

// Authenticator 是认证**门面**（可替换实现：本期本地 auth 库 / 将来 OS 账号 · LDAP · OIDC）。
// 本期只装配**本地库实现**（Local）；其余实现只需实现本接口即可替换（22 §6.1）。
type Authenticator interface {
	// Register 自助注册（生成 uid=UUID；重名 → ErrUsernameTaken）。
	Register(username, password string) (User, error)
	// Verify 登录校验（用户名或密码错 → ErrLoginFailed）。
	Verify(username, password string) (User, error)
	// ByToken 令牌解析（session / remember 两种 kind；无效 / 已吊销 → ErrUnauthorized）。
	ByToken(token string, kind TokenKind) (User, error)
}

// RegisterPolicy 是注册**策略面**——`Authenticator` 的**可选扩展**（接口本身保持不变，
// 不破坏既有实现与调用方：调用方按 §实现 *可选* 断言取用；未实现 = 回落旧行为）。
//
// 为何需要：`auth.allowRegister=false` + 空用户库 = 无任何登录路径（锁死）。本面把
// 「用户库是否为空」的判定与「写入首个用户」放进**同一写事务**，使
// 「首个用户 = admin（无视 allowRegister）」成为**原子**决策 → 并发两个注册只成功一个。
type RegisterPolicy interface {
	// RegisterWithPolicy 自助注册（生成 uid=UUID；重名 → ErrUsernameTaken）：
	//   - 用户库为空（首启一次性初始化）→ **放行**，role=admin（**无视 allowRegister**）；
	//   - 用户库非空 且 allowRegister=false → ErrLoginFailed（= 61 §4.6 `login-failed`；
	//     **不新造错误码**）；
	//   - 用户库非空 且 allowRegister=true → 放行，role=user。
	RegisterWithPolicy(username, password string, allowRegister bool) (User, error)
}

// TokenIssuer 是令牌**签发/吊销**面（与门面分开的装配位：远程/OIDC 实现可另选签发策略）。
// 本期 = 本地库实现（Local 同时实现 Authenticator 与 TokenIssuer）。
type TokenIssuer interface {
	// IssueToken 为已认证用户签发指定类别的令牌（返回**明文令牌**，仅此刻可见；库内只落哈希）。
	IssueToken(u User, kind TokenKind) (string, error)
	// RevokeToken 吊销令牌（删 tokens 行；已不存在视为成功，幂等）。
	RevokeToken(token string, kind TokenKind) error
}

// newUUID 生成 RFC 4122 v4 风格 UUID（不引入额外依赖；同 GUI 桥 / httpapi 实现）。
func newUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("usr-%d", nowUnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
