package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// 自证型免登录令牌（61 §4.6 · 22 §6.3）：
//
//	token = "ckr1." + base64url( nonce || AES-GCM({username, uid, pwd_hash}) )
//
// 要点：
//   - 算法 = **AES-GCM**（含随机 nonce；61 §4.6 明确建议 AES-GCM 而非 DES）；
//   - **密钥由服务端持有**（meta 桶持久化，见 Store.tokenKey；不经消息面、不下发前端）；
//   - 解析时解密 → 与 users 表比对 **pwd_hash** → **改密即失效**（有意特性）；
//   - 明文令牌只存哈希行（tokens 桶）→ 仍可**逐令牌吊销**（删行即失效）。
const rememberPrefix = "ckr1."

// rememberPayload 是自证令牌的明文载荷（仅服务端可见，密文外流也无法伪造）。
type rememberPayload struct {
	Username string `json:"username"`
	UID      string `json:"uid"`
	PwdHash  string `json:"pwd_hash"`
}

// sealRemember 把用户记录加密成自证令牌串。
func (l *Local) sealRemember(rec userRec) (string, error) {
	gcm, err := l.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	raw, err := json.Marshal(rememberPayload{Username: rec.Username, UID: rec.UID, PwdHash: rec.PwdHash})
	if err != nil {
		return "", err
	}
	// Seal(dst=nonce, ...) → 结果 = nonce || ciphertext（nonce 前置，解析侧按 NonceSize 切分）。
	sealed := gcm.Seal(nonce, nonce, raw, nil)
	return rememberPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openRemember 解密自证令牌（前缀/编码/认证标签任一不符 → 错误）。
func (l *Local) openRemember(token string) (rememberPayload, error) {
	var p rememberPayload
	if !strings.HasPrefix(token, rememberPrefix) {
		return p, errNoToken
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, rememberPrefix))
	if err != nil {
		return p, errNoToken
	}
	gcm, err := l.gcm()
	if err != nil {
		return p, err
	}
	if len(sealed) < gcm.NonceSize() {
		return p, errNoToken
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	raw, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return p, errNoToken
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, errNoToken
	}
	return p, nil
}

// gcm 由服务端持有的密钥构造 AES-GCM AEAD。
func (l *Local) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(l.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
