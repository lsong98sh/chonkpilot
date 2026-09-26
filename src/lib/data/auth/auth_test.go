package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

// openTest 建临时 auth 库（bbolt 单文件）。
func openTest(t *testing.T) *Local {
	t.Helper()
	l, err := OpenLocal(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// TestRegisterVerify：注册生成 UUID；重名 → login-username-taken；密码错 → login-failed；
// 空用户名/密码 → 参数错误（登录域归 login-failed）。
func TestRegisterVerify(t *testing.T) {
	l := openTest(t)

	u, err := l.Register("alice", "p@ss1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(u.UID) != 36 || strings.Count(u.UID, "-") != 4 {
		t.Fatalf("uid 应为 uuid v4，got %q", u.UID)
	}
	if u.Username != "alice" || u.CreatedAt == 0 {
		t.Fatalf("注册返回异常: %+v", u)
	}

	got, err := l.Verify("alice", "p@ss1")
	if err != nil || got.UID != u.UID {
		t.Fatalf("Verify 成功路径异常: %+v err=%v", got, err)
	}
	if _, err := l.Verify("alice", "wrong"); !errors.Is(err, ErrLoginFailed) {
		t.Fatalf("密码错应 ErrLoginFailed，got %v", err)
	}
	if _, err := l.Verify("bob", "p@ss1"); !errors.Is(err, ErrLoginFailed) {
		t.Fatalf("用户名不存在应 ErrLoginFailed，got %v", err)
	}
	if _, err := l.Register("alice", "other"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("重名应 ErrUsernameTaken，got %v", err)
	}
	if _, err := l.Register("  ", "x"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("空用户名应 ErrInvalidCredential，got %v", err)
	}
}

// TestSessionTokenIssueParseRevoke：会话令牌签发 → 解析；**只落哈希**（库内无明文）；
// 吊销（删行）即失效；类别不符 → instance-unauthorized。
func TestSessionTokenIssueParseRevoke(t *testing.T) {
	l := openTest(t)
	u, err := l.Register("alice", "p@ss1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	token, err := l.IssueToken(u, TokenSession)
	if err != nil {
		t.Fatalf("IssueToken(session): %v", err)
	}
	if token == "" || strings.ContainsAny(token, "+/=") {
		t.Fatalf("会话令牌应为 base64url 随机串，got %q", token)
	}
	got, err := l.ByToken(token, TokenSession)
	if err != nil || got.UID != u.UID {
		t.Fatalf("ByToken(session) 异常: %+v err=%v", got, err)
	}
	// 类别不符（拿会话令牌当免登录令牌用）→ 无效
	if _, err := l.ByToken(token, TokenRemember); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("类别不符应 ErrUnauthorized，got %v", err)
	}
	// **不落明文**：tokens 桶的键 = 令牌哈希，值里也不含明文令牌
	if err := l.st.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTokens)
		return b.ForEach(func(k, v []byte) error {
			if strings.Contains(string(k), token) || strings.Contains(string(v), token) {
				t.Fatalf("auth 库出现令牌明文: key=%s val=%s", k, v)
			}
			return nil
		})
	}); err != nil {
		t.Fatalf("遍历 tokens 桶: %v", err)
	}
	// 吊销（逐令牌可吊销 = 删行）
	if err := l.RevokeToken(token, TokenSession); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if _, err := l.ByToken(token, TokenSession); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("已吊销令牌应 ErrUnauthorized，got %v", err)
	}
	// 吊销幂等
	if err := l.RevokeToken(token, TokenSession); err != nil {
		t.Fatalf("RevokeToken 应幂等: %v", err)
	}
	if _, err := l.ByToken("", TokenSession); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("空令牌应 ErrUnauthorized，got %v", err)
	}
}

// TestRememberTokenSelfCertifying：免登录令牌 = AES-GCM 自证型（密文含 用户名/UID/密码哈希）；
// 解析可用；**改密即失效**（有意特性，61 §4.6）。
func TestRememberTokenSelfCertifying(t *testing.T) {
	l := openTest(t)
	u, err := l.Register("alice", "p@ss1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	token, err := l.IssueToken(u, TokenRemember)
	if err != nil {
		t.Fatalf("IssueToken(remember): %v", err)
	}
	if !strings.HasPrefix(token, rememberPrefix) {
		t.Fatalf("免登录令牌应带 %q 前缀，got %q", rememberPrefix, token)
	}
	// 自证：可解密（密钥由服务端 held 于 meta 桶）
	p, err := l.openRemember(token)
	if err != nil || p.UID != u.UID || p.Username != "alice" || p.PwdHash == "" {
		t.Fatalf("解密自证令牌异常: %+v err=%v", p, err)
	}
	if got, err := l.ByToken(token, TokenRemember); err != nil || got.UID != u.UID {
		t.Fatalf("ByToken(remember) 异常: %+v err=%v", got, err)
	}
	// 篡改密文 → 认证失败（GCM 认证标签）
	if _, err := l.ByToken(token[:len(token)-2]+"zz", TokenRemember); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("篡改令牌应 ErrUnauthorized，got %v", err)
	}

	// **改密即失效**：直接改写 users 表的 pwd_hash（模拟改密）
	newHash, err := bcrypt.GenerateFromPassword([]byte("new-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := l.st.db.Update(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketUsers).Get([]byte(u.UID))
		var rec userRec
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		rec.PwdHash = string(newHash)
		out, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketUsers).Put([]byte(u.UID), out)
	}); err != nil {
		t.Fatalf("改密: %v", err)
	}
	if _, err := l.ByToken(token, TokenRemember); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("改密后免登录令牌应失效，got %v", err)
	}
}

// TestStoreBuckets：auth 库桶语义齐备（users / users_by_name / projects / projects_by_path /
// access / tokens）。
func TestStoreBuckets(t *testing.T) {
	l := openTest(t)
	want := map[string]bool{}
	for _, b := range [][]byte{bucketUsers, bucketUsersByName, bucketProjects, bucketProjectsPath, bucketAccess, bucketTokens} {
		want[string(b)] = false
	}
	if err := l.st.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			if _, ok := want[string(name)]; ok {
				want[string(name)] = true
			}
			return nil
		})
	}); err != nil {
		t.Fatalf("遍历桶: %v", err)
	}
	for name, ok := range want {
		if !ok {
			t.Fatalf("auth 库缺桶 %s", name)
		}
	}
	// access 桶键编码：uid + "\x00" + project_id
	if got := AccessKey("u1", "p1"); got != "u1\x00p1" {
		t.Fatalf("AccessKey 编码异常: %q", got)
	}
}

// TestProjectGrantLookupAccess：项目登记 + 用户-项目关联原语（阶段 2b-2；
// 61 §4.6「项目 = 被登记的对象」）——自建项目（name = 目录名、created_by = uid）+
// access 关联；按路径查（**规范化** + Windows 大小写不敏感）；未登记 / 无关联的判定；
// 幂等（同路径重复登记复用同一 project_id）；参数非法 → ErrInvalidProject。
func TestProjectGrantLookupAccess(t *testing.T) {
	l := openTest(t)
	alice, err := l.Register("alice", "p@ss1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	bob, err := l.Register("bob", "p@ss1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 路径规范化（键口径）：末尾分隔符 / 冗余段 / 相对分隔符 → 同一键
	dir := filepath.Join(t.TempDir(), "proj-a")
	if NormalizeDir(dir) != NormalizeDir(dir+string(filepath.Separator)) {
		t.Fatal("NormalizeDir 应归一末尾分隔符")
	}
	if NormalizeDir("") != "" {
		t.Fatal("空路径应归一为空串（= 未提供）")
	}

	// 未登记 → ok=false
	if _, ok, err := l.ProjectByPath(dir); err != nil || ok {
		t.Fatalf("未登记路径不应命中项目：ok=%v err=%v", ok, err)
	}
	if has, err := l.HasAccess(alice.UID, "no-such-proj"); err != nil || has {
		t.Fatalf("无关联应为 false：has=%v err=%v", has, err)
	}

	// 自建项目：projects 记录 + access 关联（同一事务）
	proj, err := l.GrantProject(alice.UID, dir)
	if err != nil {
		t.Fatalf("GrantProject: %v", err)
	}
	if proj.ProjectID == "" || proj.Name != "proj-a" || proj.CreatedBy != alice.UID || proj.CreatedAt == 0 {
		t.Fatalf("项目记录异常: %+v", proj)
	}
	if proj.WorkDir != NormalizeDir(dir) {
		t.Fatalf("项目路径应为规范化键：%q vs %q", proj.WorkDir, NormalizeDir(dir))
	}
	got, ok, err := l.ProjectByPath(dir)
	if err != nil || !ok || got.ProjectID != proj.ProjectID {
		t.Fatalf("按路径查项目异常: %+v ok=%v err=%v", got, ok, err)
	}
	if has, err := l.HasAccess(alice.UID, proj.ProjectID); err != nil || !has {
		t.Fatalf("建项目应同时建 access：has=%v err=%v", has, err)
	}
	// 他人无关联 → false
	if has, err := l.HasAccess(bob.UID, proj.ProjectID); err != nil || has {
		t.Fatalf("他人不应有关联：has=%v err=%v", has, err)
	}

	// 幂等：同路径重复登记（含大小写差异）→ 复用同一 project_id，且为第二人建独立关联
	same, err := l.GrantProject(bob.UID, strings.ToUpper(dir))
	if err != nil {
		t.Fatalf("重复 GrantProject: %v", err)
	}
	if same.ProjectID != proj.ProjectID {
		t.Fatalf("同路径应复用项目：%s vs %s", same.ProjectID, proj.ProjectID)
	}
	if has, err := l.HasAccess(bob.UID, proj.ProjectID); err != nil || !has {
		t.Fatalf("重复登记应为该用户建关联：has=%v err=%v", has, err)
	}

	// 参数非法
	if _, err := l.GrantProject("", dir); !errors.Is(err, ErrInvalidProject) {
		t.Fatalf("空 uid 应 ErrInvalidProject，got %v", err)
	}
	if _, err := l.GrantProject(alice.UID, "  "); !errors.Is(err, ErrInvalidProject) {
		t.Fatalf("空路径应 ErrInvalidProject，got %v", err)
	}
}

// TestRegisterRoleAndBackwardCompat：`role` 是**可选字段**（🆕 首启一次性初始化）——
// 空用户库的首个注册者 = `admin`、其余 = `user`；**旧记录缺该字段 → 读侧归一 `user`**
// （不改写既有记录，向后兼容）。
func TestRegisterRoleAndBackwardCompat(t *testing.T) {
	l := openTest(t)

	first, err := l.Register("alice", "p@ss1")
	if err != nil {
		t.Fatalf("Register(first): %v", err)
	}
	if first.Role != RoleAdmin {
		t.Fatalf("空库首个用户应为 %s，got %q", RoleAdmin, first.Role)
	}
	second, err := l.Register("bob", "p@ss1")
	if err != nil {
		t.Fatalf("Register(second): %v", err)
	}
	if second.Role != RoleUser {
		t.Fatalf("非首个用户应为 %s，got %q", RoleUser, second.Role)
	}
	// Verify / ByToken 同口径
	if got, err := l.Verify("alice", "p@ss1"); err != nil || got.Role != RoleAdmin {
		t.Fatalf("Verify 角色应为 %s：%+v err=%v", RoleAdmin, got, err)
	}

	// 落库形态：users 表内 role 字段随记录写入
	if err := l.st.db.View(func(tx *bolt.Tx) error {
		var rec userRec
		if err := json.Unmarshal(tx.Bucket(bucketUsers).Get([]byte(first.UID)), &rec); err != nil {
			return err
		}
		if rec.Role != RoleAdmin {
			t.Fatalf("库内首个用户 role 应为 %s，got %q", RoleAdmin, rec.Role)
		}
		return nil
	}); err != nil {
		t.Fatalf("读 users 桶: %v", err)
	}

	// **向后兼容**：手工写入「旧形态」记录（缺 role 字段）→ 读侧归一 user
	hash, err := bcrypt.GenerateFromPassword([]byte("p@ss1"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	legacyRaw := []byte(`{"uid":"legacy-uid","username":"carol","pwd_hash":"` + string(hash) + `","created_at":1}`)
	if strings.Contains(string(legacyRaw), `"role"`) {
		t.Fatal("构造的旧记录不应含 role 字段")
	}
	if err := l.st.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketUsers).Put([]byte("legacy-uid"), legacyRaw); err != nil {
			return err
		}
		return tx.Bucket(bucketUsersByName).Put([]byte("carol"), []byte("legacy-uid"))
	}); err != nil {
		t.Fatalf("写旧记录: %v", err)
	}
	got, err := l.Verify("carol", "p@ss1")
	if err != nil {
		t.Fatalf("旧记录应可登录: %v", err)
	}
	if got.Role != RoleUser {
		t.Fatalf("缺 role 字段的旧记录应归一为 %s，got %q", RoleUser, got.Role)
	}
}

// TestRegisterWithPolicyAtomic：**并发安全**——「用户库是否为空」的判定与「写入首个用户」在
// **同一 bbolt 写事务**内（见 Local.RegisterWithPolicy）→
//   - 空库 + allowRegister=false：N 个并发请求**只成功一个**（该者 = admin），其余 ErrLoginFailed；
//   - 空库 + allowRegister=true：N 个并发请求**全部成功**，且**恰好一个 admin**，其余 user；
//   - 库内用户数 = N（无重复写入 / 无丢失）。
func TestRegisterWithPolicyAtomic(t *testing.T) {
	const n = 8

	run := func(t *testing.T, allowRegister bool) ([]User, []error) {
		t.Helper()
		l := openTest(t)
		users := make([]User, n)
		errs := make([]error, n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start // 同刻起跑（真并发进入注册路径）
				users[i], errs[i] = l.RegisterWithPolicy(fmt.Sprintf("u%d", i), "p@ss1", allowRegister)
			}(i)
		}
		close(start)
		wg.Wait()
		// 库内用户数：关闭注册 → 只有唯一成功者落库；开放注册 → 全部落库
		want := n
		if !allowRegister {
			want = 1
		}
		cnt := 0
		if err := l.st.db.View(func(tx *bolt.Tx) error {
			c := tx.Bucket(bucketUsers).Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				cnt++
			}
			return nil
		}); err != nil {
			t.Fatalf("遍历 users 桶: %v", err)
		}
		if cnt != want {
			t.Fatalf("库内用户数应为 %d，got %d", want, cnt)
		}
		return users, errs
	}

	// ① 空库 + 关闭注册 → 只成功一个（admin）
	users, errs := run(t, false)
	okCount, rejected := 0, 0
	for i := 0; i < n; i++ {
		switch {
		case errs[i] == nil:
			okCount++
			if users[i].Role != RoleAdmin {
				t.Fatalf("关闭注册下唯一成功者应为 %s，got %q", RoleAdmin, users[i].Role)
			}
		case errors.Is(errs[i], ErrLoginFailed):
			rejected++
		default:
			t.Fatalf("落败者应为 ErrLoginFailed，got %v", errs[i])
		}
	}
	if okCount != 1 || rejected != n-1 {
		t.Fatalf("关闭注册 + 空库并发应只成功一个：ok=%d rejected=%d", okCount, rejected)
	}

	// ② 空库 + 开放注册 → 全部成功，恰好一个 admin
	users, errs = run(t, true)
	admin, plain := 0, 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("开放注册应全部成功，got err=%v", errs[i])
		}
		switch users[i].Role {
		case RoleAdmin:
			admin++
		case RoleUser:
			plain++
		default:
			t.Fatalf("角色非法: %q", users[i].Role)
		}
	}
	if admin != 1 || plain != n-1 {
		t.Fatalf("开放注册并发应恰好一个 %s：admin=%d user=%d", RoleAdmin, admin, plain)
	}
}
