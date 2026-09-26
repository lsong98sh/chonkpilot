package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

// Local 是 Authenticator / TokenIssuer 的**本地 auth 库实现**（本期唯一实现；61 §4.6）。
type Local struct {
	st  *Store
	key []byte // 自证令牌的 AES-GCM 密钥（meta 桶持久化，服务端持有；不经消息面/前端下发）
}

// OpenLocal 打开本地实现（打开/建库 + 取（或首次生成）令牌加密密钥）。
func OpenLocal(path string) (*Local, error) {
	st, err := Open(path)
	if err != nil {
		return nil, err
	}
	key, err := st.tokenKey()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Local{st: st, key: key}, nil
}

// Close 关闭本地实现（关底层 auth 库）。
func (l *Local) Close() error { return l.st.Close() }

// Path 返回 auth 库文件路径（诊断用）。
func (l *Local) Path() string { return l.st.Path() }

// Register 自助注册（生成 uid=UUID；重名 → ErrUsernameTaken）。密码用 bcrypt 哈希后落库。
//
// 等价 `RegisterWithPolicy(username, password, true)`：**不校验**配置（旧调用方语义不变），
// 角色仍按「用户库为空 → admin / 否则 user」判定（见 RegisterWithPolicy）。
func (l *Local) Register(username, password string) (User, error) {
	return l.RegisterWithPolicy(username, password, true)
}

// RegisterWithPolicy 自助注册 + 注册策略（`RegisterPolicy` 面；61 §4.6）：
//
//   - 用户库为空（**首启一次性初始化**）→ 放行，role = admin（**无视 allowRegister**）；
//   - 用户库非空 且 allowRegister=false → ErrLoginFailed（= 消息面 `login-failed`；不新造码）；
//   - 用户库非空 且 allowRegister=true → 放行，role = user。
//
// **并发安全（关键）**：「用户库是否为空」的判定（cursor.First）与「写入首个用户」在
// **同一个 bbolt 写事务**内完成 —— bbolt 的写事务全局串行，故两个并发注册请求中只有一个
// 能看到空库（→ admin），另一个必然看到首个用户已落库 → 非空分支（allowRegister=false →
// ErrLoginFailed；=true → user）。重名判定（users_by_name）同样在该事务内，故只可能成功一个。
func (l *Local) RegisterWithPolicy(username, password string, allowRegister bool) (User, error) {
	name := strings.TrimSpace(username)
	if name == "" || password == "" {
		return User{}, ErrInvalidCredential
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	now := time.Now().Unix()
	rec := userRec{UID: newUUID(), Username: name, PwdHash: string(hash), CreatedAt: now}
	err = l.st.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketUsersByName).Get([]byte(name)) != nil {
			return ErrUsernameTaken
		}
		first, _ := tx.Bucket(bucketUsers).Cursor().First() // 空库判定（与写入同事务；见函数注释）
		if first == nil {
			rec.Role = RoleAdmin // 首个用户 = admin（首启初始化，无视 allowRegister）
		} else {
			if !allowRegister {
				return ErrLoginFailed // 非空且注册关闭 → 拒绝（复用 61 §4.6 login-failed）
			}
			rec.Role = RoleUser
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketUsers).Put([]byte(rec.UID), raw); err != nil {
			return err
		}
		return tx.Bucket(bucketUsersByName).Put([]byte(name), []byte(rec.UID))
	})
	if err != nil {
		return User{}, err
	}
	return rec.user(), nil
}

// Verify 登录校验（用户名或密码错 → ErrLoginFailed；不区分二者，避免用户名枚举）。
func (l *Local) Verify(username, password string) (User, error) {
	rec, ok, err := l.st.userByName(strings.TrimSpace(username))
	if err != nil || !ok {
		return User{}, ErrLoginFailed
	}
	if bcrypt.CompareHashAndPassword([]byte(rec.PwdHash), []byte(password)) != nil {
		return User{}, ErrLoginFailed
	}
	return rec.user(), nil
}

// ByToken 令牌解析（session / remember 两种 kind；无效 / 已吊销 → ErrUnauthorized）。
//
// 两类令牌都须在 tokens 桶有行（哈希键）—— 行被删 = 已吊销（逐令牌可吊销）。
// remember 额外做**自证校验**：解密 → 与 users 表比对 uid/username/pwd_hash
// → 改密即失效（有意特性，61 §4.6）。
func (l *Local) ByToken(token string, kind TokenKind) (User, error) {
	if strings.TrimSpace(token) == "" {
		return User{}, ErrUnauthorized
	}
	if kind == "" {
		kind = TokenSession
	}
	hash := tokenHash(token)
	rec, ok, err := l.st.token(hash)
	if err != nil || !ok || TokenKind(rec.Kind) != kind {
		return User{}, ErrUnauthorized
	}
	u, ok, err := l.st.userByUID(rec.UID)
	if err != nil || !ok {
		return User{}, ErrUnauthorized
	}
	if kind == TokenRemember {
		p, err := l.openRemember(token)
		if err != nil || p.UID != u.UID || p.Username != u.Username || p.PwdHash != u.PwdHash {
			return User{}, ErrUnauthorized
		}
	}
	if err := l.st.touchToken(hash); err != nil {
		return User{}, err
	}
	return u.user(), nil
}

// IssueToken 签发令牌（返回明文令牌；库内只落哈希）。
//
//   - TokenSession：32B 随机 → base64url（不透明串，交由入口下发 Set-Cookie）。
//   - TokenRemember：AES-GCM 自证密文（含 用户名/UID/密码哈希；交 GUI 入口落文件）。
func (l *Local) IssueToken(u User, kind TokenKind) (string, error) {
	if kind == "" {
		kind = TokenSession
	}
	var token string
	switch kind {
	case TokenSession:
		token = newOpaqueToken()
	case TokenRemember:
		rec, ok, err := l.st.userByUID(u.UID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrUnauthorized
		}
		if token, err = l.sealRemember(rec); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("auth: 未知令牌类别 %q", kind)
	}
	now := time.Now().Unix()
	if err := l.st.putToken(tokenHash(token), tokenRec{
		UID: u.UID, Kind: string(kind), IssuedAt: now, LastUsed: now,
	}); err != nil {
		return "", err
	}
	return token, nil
}

// RevokeToken 吊销令牌（删 tokens 行；幂等）。kind 仅作语义标注（键 = 令牌哈希，与类别无关）。
func (l *Local) RevokeToken(token string, kind TokenKind) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return l.st.delToken(tokenHash(token))
}

// errNoToken 是内部哨兵（空/坏令牌一律归 ErrUnauthorized）。
var errNoToken = errors.New("auth: 令牌格式非法")

// newOpaqueToken 生成会话令牌：32B 随机 → base64url（61 §4.6）。
func newOpaqueToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
