// 项目登记与用户-项目关联（61-消息一览 §4.6 · 22-实例与会话标识 §6；阶段 2b-2）。
//
// 定位：`instance-claim` 的 `work_dir` 归属校验所需的**数据面原语**——
//   - `ProjectByPath`：按规范化路径查项目（`projects_by_path` 桶）；
//   - `GrantProject`：用户**自建项目**（同一事务内写 `projects` + `access` 关联）；
//   - `HasAccess`：该用户是否在 `access` 中关联到该项目。
//
// **不在本文件做业务判定**（「落在允许根下才允许自建」「项目存在但无关联即拒绝」属
// 服务端 claim 判定，见 chonkpilot-llm/server/claim.go）；本文件只提供**读写原语**。
//
// 命名与装配位：与 `TokenIssuer` 同法，`ProjectAuthorizer` 是**可替换**面（远程 DB /
// LDAP / OIDC 实现可另选项目存储，见 auth.go 门面说明）；本期唯一实现 = `Local`。
package auth

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ErrInvalidProject 项目登记参数非法（uid 或 work_dir 为空）。
var ErrInvalidProject = errors.New("auth: 项目登记参数非法（uid / work_dir 不能为空）")

// accessRoleOwner 是**自建项目**的默认角色（方案 A：用户自建即 owner）。
const accessRoleOwner = "owner"

// NormalizeDir 规范化目录路径（`projects_by_path` 的**键口径**，与服务端判定同源）：
// 去空白 → `FromSlash`/`Clean` 统一分隔符与冗余段 → 尽量转绝对路径。
//
// **不改变大小写**（Windows 上路径大小写不敏感由查询侧 `EqualFold` 兜底，见 projectByPath）；
// 空输入 → 空串（调用方按「未提供」处理）。
func NormalizeDir(p string) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return ""
	}
	s = filepath.Clean(filepath.FromSlash(s))
	if abs, err := filepath.Abs(s); err == nil {
		s = abs
	}
	return s
}

// ProjectAuthorizer 是「项目登记 + 用户-项目关联」面（与 Authenticator 分开的装配位：
// 远程 / OIDC 实现可另选项目存储；本期 = 本地 auth 库，见 auth.go 文件头）。
type ProjectAuthorizer interface {
	// ProjectByPath 按规范化 work_dir 查项目（未登记 → ok=false）。
	ProjectByPath(workDir string) (Project, bool, error)
	// GrantProject 为 uid 登记 work_dir 项目并建 access 关联；路径已登记 → 复用该项目（幂等）。
	GrantProject(uid, workDir string) (Project, error)
	// HasAccess 判定 uid 是否在 access 中关联到 projectID。
	HasAccess(uid, projectID string) (bool, error)
}

// ProjectByPath 按规范化 work_dir 查项目（61 §4.6：项目 = **被登记的对象**）。
func (l *Local) ProjectByPath(workDir string) (Project, bool, error) {
	dir := NormalizeDir(workDir)
	if dir == "" {
		return Project{}, false, nil
	}
	return l.st.projectByPath(dir)
}

// GrantProject 为用户登记项目 + access 关联（**自建项目**：用户创建项目即建关联）：
// fields = name 取目录名、created_by = uid、created_at = now；路径已登记 → 复用项目 id（幂等）。
func (l *Local) GrantProject(uid, workDir string) (Project, error) {
	dir := NormalizeDir(workDir)
	if strings.TrimSpace(uid) == "" || dir == "" {
		return Project{}, ErrInvalidProject
	}
	var proj Project
	err := l.st.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketProjects)
		if id := projectIDByPathTx(tx, dir); len(id) > 0 {
			if raw := b.Get(id); len(raw) > 0 {
				if err := json.Unmarshal(raw, &proj); err == nil && proj.ProjectID != "" {
					return putAccess(tx, uid, proj.ProjectID, accessRoleOwner)
				}
			}
		}
		proj = Project{
			ProjectID: newUUID(),
			Name:      filepath.Base(dir),
			WorkDir:   dir,
			CreatedBy: uid,
			CreatedAt: time.Now().Unix(),
		}
		raw, err := json.Marshal(proj)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(proj.ProjectID), raw); err != nil {
			return err
		}
		if err := tx.Bucket(bucketProjectsPath).Put([]byte(dir), []byte(proj.ProjectID)); err != nil {
			return err
		}
		return putAccess(tx, uid, proj.ProjectID, accessRoleOwner)
	})
	if err != nil {
		return Project{}, err
	}
	return proj, nil
}

// HasAccess 判定 uid 是否关联到 projectID（access 桶；key = uid + "\x00" + project_id）。
func (l *Local) HasAccess(uid, projectID string) (bool, error) {
	if strings.TrimSpace(uid) == "" || strings.TrimSpace(projectID) == "" {
		return false, nil
	}
	ok := false
	err := l.st.db.View(func(tx *bolt.Tx) error {
		ok = tx.Bucket(bucketAccess).Get([]byte(AccessKey(uid, projectID))) != nil
		return nil
	})
	return ok, err
}

// projectByPath 按键查项目：先精确命中（规范化后键），未命中再**大小写不敏感**扫描
// （Windows 路径大小写不敏感；项目量小，扫描代价可忽略 —— 不引入第二套键编码）。
func (st *Store) projectByPath(dir string) (Project, bool, error) {
	var proj Project
	ok := false
	err := st.db.View(func(tx *bolt.Tx) error {
		id := projectIDByPathTx(tx, dir)
		if len(id) == 0 {
			return nil
		}
		raw := tx.Bucket(bucketProjects).Get(id)
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, &proj); err != nil {
			return err
		}
		ok = proj.ProjectID != ""
		return nil
	})
	if err != nil {
		return Project{}, false, err
	}
	return proj, ok, nil
}

// projectIDByPathTx 在给定事务内按规范化路径取 project_id（精确键优先，未命中 → EqualFold
// 扫描；均不含副作用）。查/写（GrantProject）两侧**共用同一查询**，保证「同路径不重复登记」。
func projectIDByPathTx(tx *bolt.Tx, dir string) []byte {
	pb := tx.Bucket(bucketProjectsPath)
	if id := pb.Get([]byte(dir)); len(id) > 0 {
		return id
	}
	var found []byte
	_ = pb.ForEach(func(k, v []byte) error {
		if found == nil && strings.EqualFold(string(k), dir) {
			found = append([]byte(nil), v...)
		}
		return nil
	})
	return found
}

// putAccess 写 access 关联（同事务内；幂等：同 uid×project 覆盖）。
func putAccess(tx *bolt.Tx, uid, projectID, role string) error {
	raw, err := json.Marshal(Access{UID: uid, ProjectID: projectID, Role: role})
	if err != nil {
		return err
	}
	return tx.Bucket(bucketAccess).Put([]byte(AccessKey(uid, projectID)), raw)
}
