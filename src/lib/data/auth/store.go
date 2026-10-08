package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	data "github.com/chonkpilot/chonkpilot-data"

	bolt "go.etcd.io/bbolt"
)

// 桶名（61 §4.6 · 22 §6.4；命名可微调，语义齐备）。
var (
	bucketUsers        = []byte("users")            // key = uid → userRec
	bucketUsersByName  = []byte("users_by_name")    // key = username → uid（唯一性索引）
	bucketProjects     = []byte("projects")         // key = project_id → Project
	bucketProjectsPath = []byte("projects_by_path") // key = work_dir（规范化）→ project_id
	bucketAccess       = []byte("access")           // key = uid + "\x00" + project_id → Access
	bucketTokens       = []byte("tokens")           // key = 令牌哈希 → tokenRec（删行 = 吊销）
	// bucketMeta 是本地实现自有元数据（自证令牌的 AES-GCM 密钥）；语义上不属"用户/项目/关联/
	// 令牌"四类，故单列一桶，不污染上述四桶。
	bucketMeta = []byte("meta")
)

// metaKeyTokenKey 是自证令牌加密密钥（32B，服务端持有）在 meta 桶中的键；首次打开生成。
var metaKeyTokenKey = []byte("token-key")

// userRec 是 users 桶记录（密码**只存 bcrypt 哈希**，永不落明文）。
type userRec struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
	PwdHash  string `json:"pwd_hash"`
	// Role 是用户角色（admin / user；🆕 首启一次性初始化）。**可选字段**：旧记录缺该字段
	// → 读侧归一为 `user`（见 role()），写入与既有记录**向后兼容**。
	Role      string `json:"role,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// role 归一角色：缺字段（旧记录）/ 未知值 → `user`（`admin` 仅由首启初始化写入）。
func (r userRec) role() string {
	if r.Role == RoleAdmin {
		return RoleAdmin
	}
	return RoleUser
}

// user 转对外的用户视图（去掉 pwd_hash）。
func (r userRec) user() User {
	return User{UID: r.UID, Username: r.Username, Role: r.role(), CreatedAt: r.CreatedAt}
}

// tokenRec 是 tokens 桶记录（**哈希不落明文**：key = 令牌哈希）。
type tokenRec struct {
	UID      string `json:"uid"`
	Kind     string `json:"kind"`
	IssuedAt int64  `json:"issued_at"`
	LastUsed int64  `json:"last_used"`
	Note     string `json:"note"`
}

// Project 是项目登记（projects 桶语义）。读写原语见 projects.go（2b-2：`instance-claim` 的
// `work_dir` 归属校验用）；**不提供管理消息面**（管理员授权界面不在 2b-2 范围，61 零变更）。
type Project struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	WorkDir   string `json:"work_dir"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
}

// Access 是用户-项目关联（access 桶语义）。读写原语见 projects.go（同上；自建项目 = owner）。
type Access struct {
	UID       string `json:"uid"`
	ProjectID string `json:"project_id"`
	Role      string `json:"role"`
}

// AccessKey 是 access 桶键（uid + "\x00" + project_id）。
func AccessKey(uid, projectID string) string { return uid + "\x00" + projectID }

// Store 是 auth 库（bbolt 单文件；与业务三级库**分离**，见 22 §6.4）。
type Store struct {
	path string
	db   *bolt.DB
}

// Open 打开/创建 auth 库（建全部桶；目录 0700、文件 0600）。
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: data.BoltOpenTimeout})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{
			bucketUsers, bucketUsersByName, bucketProjects, bucketProjectsPath,
			bucketAccess, bucketTokens, bucketMeta,
		} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{path: path, db: db}, nil
}

// Close 关闭 auth 库。
func (st *Store) Close() error { return st.db.Close() }

// Path 返回 auth 库文件路径。
func (st *Store) Path() string { return st.path }

// ── 内部读写（桶级）────────────────────────────

// userByUID 按 uid 读用户。
func (st *Store) userByUID(uid string) (userRec, bool, error) {
	var rec userRec
	ok := false
	err := st.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketUsers).Get([]byte(uid))
		if raw == nil {
			return nil
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		ok = true
		return nil
	})
	return rec, ok, err
}

// userByName 按 username（唯一性索引 users_by_name）读用户。
func (st *Store) userByName(username string) (userRec, bool, error) {
	var rec userRec
	ok := false
	err := st.db.View(func(tx *bolt.Tx) error {
		uid := tx.Bucket(bucketUsersByName).Get([]byte(username))
		if uid == nil {
			return nil
		}
		raw := tx.Bucket(bucketUsers).Get(uid)
		if raw == nil {
			return nil
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		ok = true
		return nil
	})
	return rec, ok, err
}

// putToken / token / delToken / touchToken 是 tokens 桶读写（key = 令牌哈希）。
func (st *Store) putToken(hash string, rec tokenRec) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return st.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTokens).Put([]byte(hash), raw)
	})
}

func (st *Store) token(hash string) (tokenRec, bool, error) {
	var rec tokenRec
	ok := false
	err := st.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketTokens).Get([]byte(hash))
		if raw == nil {
			return nil
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		ok = true
		return nil
	})
	return rec, ok, err
}

// delToken 删令牌行（不存在亦成功 = 幂等吊销）。
func (st *Store) delToken(hash string) error {
	return st.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTokens)
		if b.Get([]byte(hash)) == nil {
			return nil
		}
		return b.Delete([]byte(hash))
	})
}

// touchToken 刷新 last_used（尽力而为；失败不影响解析结果）。
func (st *Store) touchToken(hash string) error {
	return st.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTokens)
		raw := b.Get([]byte(hash))
		if raw == nil {
			return nil
		}
		var rec tokenRec
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil
		}
		rec.LastUsed = time.Now().Unix()
		out, err := json.Marshal(rec)
		if err != nil {
			return nil
		}
		return b.Put([]byte(hash), out)
	})
}

// tokenKey 取（或首次生成）自证令牌的 AES-GCM 密钥（32B，服务端持有；持久化于 meta 桶）。
func (st *Store) tokenKey() ([]byte, error) {
	var key []byte
	err := st.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMeta)
		if v := b.Get(metaKeyTokenKey); len(v) == 32 {
			key = append([]byte(nil), v...)
			return nil
		}
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		if err := b.Put(metaKeyTokenKey, buf); err != nil {
			return err
		}
		key = buf
		return nil
	})
	return key, err
}

// tokenHash 是令牌在库内的键：sha256(明文) 十六进制（不落明文）。
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// nowUnixNano 兜底时间源（UUID 生成失败时的降级标识用）。
func nowUnixNano() int64 { return time.Now().UnixNano() }
