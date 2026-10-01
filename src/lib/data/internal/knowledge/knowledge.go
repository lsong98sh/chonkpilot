// Package knowledge 是 knowledge 域门面实现（`chonkpilot-data/facade` 的 KnowledgeAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：知识库 = capability 原语文件树（根解析 / 目录列举 / 契约文档读写 / 增删改名 / 建删目录）。
// 把「领域字段 + 条目名」翻译成**四级** capability 根（系统/用户/项目/项目私有）下的**文件路径**
// （路径解析 / 归属判定留在本侧；门面不交路径规则）；契约分区文本与门面文档领域形态的翻译收在
// capfs（契约解析/组装）+ `facade/wire`（载荷形状）。每级下 6 个扁平子目录（prompts/tools/
// resources/skills/agents/scenarios，旧 `knowledge/**` 归并层已删除）。
//
// ⚠️ 作用域校验（G-26，**不得绕过**）：`KnowledgeRename` / `KnowledgeRenameDir` 的目标解析一律
// 走 `kbResolveMoveTarget`（逐段净化 + 严格落在源根内 + `kbRootOf` 归属复检同根 + 拒"移入自身"）
// 与 `kbGuardRenameTarget`（目标已存在即拒，避免 `os.Rename` 静默替换）。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）——gui 桥 /
//     browser 入口用；
//   - mq  绑定：persist 的 `data-knowledge-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package knowledge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// Service 是 knowledge 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 knowledge 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 knowledge 域门面（缺方法即编译不过）。
var _ facade.KnowledgeAPI = (*Service)(nil)

// kbDocToFacade 契约文档（分区解析结果）→ 门面文档领域形态。
func kbDocToFacade(d capfs.Doc) facade.KnowledgeDoc {
	return facade.KnowledgeDoc{
		Title:         d.Title,
		Meta:          d.Meta,
		Description:   d.Description,
		Parameters:    d.Parameters,
		Content:       d.Content,
		ParamsSection: d.ParamsSection,
	}
}

// kbDocFromFacade 门面文档领域形态 → 契约文档（供 BuildDoc 序列化回写）。
func kbDocFromFacade(d facade.KnowledgeDoc) capfs.Doc {
	meta := d.Meta
	if meta == nil {
		meta = map[string]string{}
	}
	return capfs.Doc{
		Title:         d.Title,
		Meta:          meta,
		Description:   d.Description,
		Parameters:    d.Parameters,
		Content:       d.Content,
		ParamsSection: d.ParamsSection,
	}
}

// KnowledgeRoot 解析知识库根（kind = app / user / project / prjusr；空 = app）。
func (s *Service) KnowledgeRoot(req facade.KnowledgeRootRequest) (facade.KnowledgeRootResponse, error) {
	kind := req.Kind
	if kind == "" {
		kind = capfs.KindApp
	}
	var root string
	switch kind {
	case capfs.KindPrjUsr:
		root = s.kbPrjUsrCapRoot(req.InstanceID, req.Scope)
		if root == "" {
			return facade.KnowledgeRootResponse{}, fmt.Errorf("kb root: 项目私有级（prjusr）capability 根解析失败")
		}
	case capfs.KindProject:
		workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
		if err != nil {
			return facade.KnowledgeRootResponse{}, err
		}
		root = kbProjectRoot(workDir)
	case capfs.KindUser:
		root = s.kbUserRoot()
	default:
		kind = capfs.KindApp
		r, err := s.kbAppRoot()
		if err != nil {
			return facade.KnowledgeRootResponse{}, err
		}
		root = r
	}
	return facade.KnowledgeRootResponse{Root: filepath.ToSlash(root), Kind: kind}, nil
}

// KnowledgeList 列举目录下内容（根目录首次打开自动预置 **6 个扁平子目录**：
// `prompts/` `tools/` `resources/` `skills/` `agents/` `scenarios/`）。
func (s *Service) KnowledgeList(req facade.KnowledgeListRequest) (facade.KnowledgeListResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeListResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Dir)
	if err != nil {
		return facade.KnowledgeListResponse{}, err
	}
	dir := capfs.SafeJoin(root, req.Dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return facade.KnowledgeListResponse{}, err
	}
	if r := capfs.RelOf(root, dir); r == "" || r == "." {
		for _, t := range capfs.Types {
			_ = os.MkdirAll(filepath.Join(dir, filepath.FromSlash(t.Rel)), 0755)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return facade.KnowledgeListResponse{}, fmt.Errorf("list: %v", err)
	}
	resp := facade.KnowledgeListResponse{Dir: filepath.ToSlash(capfs.RelOf(root, dir))}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(dir, name)
		if e.IsDir() {
			resp.Dirs = append(resp.Dirs, facade.KnowledgeDir{Name: name, Path: filepath.ToSlash(p)})
			continue
		}
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		info, _ := e.Info()
		modified := ""
		if info != nil {
			modified = info.ModTime().Format(time.RFC3339)
		}
		desc, _ := capfs.PreviewDescription(p)
		resp.Files = append(resp.Files, facade.KnowledgeFile{
			Name: name, Path: filepath.ToSlash(p),
			Type: capfs.TypeOfFile(name, filepath.Dir(p)), Description: desc, Modified: modified,
		})
	}
	sort.Slice(resp.Files, func(i, j int) bool { return resp.Files[i].Name < resp.Files[j].Name })
	return resp, nil
}

// KnowledgeRead 读取原语文档并解析契约。
func (s *Service) KnowledgeRead(req facade.KnowledgeReadRequest) (facade.KnowledgeReadResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeReadResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Path)
	if err != nil {
		return facade.KnowledgeReadResponse{}, err
	}
	raw, err := os.ReadFile(capfs.SafeJoin(root, req.Path))
	if err != nil {
		return facade.KnowledgeReadResponse{}, fmt.Errorf("read: %v", err)
	}
	return facade.KnowledgeReadResponse{Source: string(raw), Doc: kbDocToFacade(capfs.ParseDoc(string(raw)))}, nil
}

// KnowledgeSave 保存原语文档（按契约序列化回写）。
func (s *Service) KnowledgeSave(req facade.KnowledgeSaveRequest) (facade.KnowledgeSaveResponse, error) {
	if req.Path == "" {
		return facade.KnowledgeSaveResponse{}, errors.New("path required")
	}
	raw := capfs.BuildDoc(kbDocFromFacade(req.Doc))
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeSaveResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Path)
	if err != nil {
		return facade.KnowledgeSaveResponse{}, err
	}
	p := capfs.SafeJoin(root, req.Path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return facade.KnowledgeSaveResponse{}, err
	}
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		return facade.KnowledgeSaveResponse{}, fmt.Errorf("save: %v", err)
	}
	return facade.KnowledgeSaveResponse{OK: true}, nil
}

// KnowledgeCreate 新建原语文档（按类型生成契约模板 + 规范文件名）。
func (s *Service) KnowledgeCreate(req facade.KnowledgeCreateRequest) (facade.KnowledgeCreateResponse, error) {
	if req.Name == "" {
		return facade.KnowledgeCreateResponse{}, errors.New("name required")
	}
	t := capfs.TypeOfArg(req.Type)
	if t == nil {
		return facade.KnowledgeCreateResponse{}, fmt.Errorf("unknown type: %s", req.Type)
	}
	fileName := capfs.FileName(req.Name, t.Token)
	rel := filepath.ToSlash(filepath.Join(req.Dir, fileName))
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeCreateResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Dir)
	if err != nil {
		return facade.KnowledgeCreateResponse{}, err
	}
	p := capfs.SafeJoin(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return facade.KnowledgeCreateResponse{}, err
	}
	if _, err := os.Stat(p); err == nil {
		return facade.KnowledgeCreateResponse{}, fmt.Errorf("%s exists", req.Name)
	}
	doc := capfs.Template(t.Token, capfs.SanitizeName(req.Name))
	if err := os.WriteFile(p, []byte(capfs.BuildDoc(doc)), 0o644); err != nil {
		return facade.KnowledgeCreateResponse{}, fmt.Errorf("create: %v", err)
	}
	return facade.KnowledgeCreateResponse{OK: true, Path: rel}, nil
}

// KnowledgeDelete 删除原语文档。
func (s *Service) KnowledgeDelete(req facade.KnowledgeDeleteRequest) (facade.KnowledgeDeleteResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeDeleteResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Path)
	if err != nil {
		return facade.KnowledgeDeleteResponse{}, err
	}
	if err := os.Remove(capfs.SafeJoin(root, req.Path)); err != nil {
		return facade.KnowledgeDeleteResponse{}, fmt.Errorf("delete: %v", err)
	}
	return facade.KnowledgeDeleteResponse{OK: true}, nil
}

// KnowledgeRename 改名原语文档（保留 *.type.md 后缀规范），或**跨目录移动**该文件
// （NewName 含路径分隔符 = 移动；目标为同一知识库根内，G-26）。
func (s *Service) KnowledgeRename(req facade.KnowledgeRenameRequest) (facade.KnowledgeRenameResponse, error) {
	if req.NewName == "" {
		return facade.KnowledgeRenameResponse{}, errors.New("new_name required")
	}
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeRenameResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Path)
	if err != nil {
		return facade.KnowledgeRenameResponse{}, err
	}
	oldP := capfs.SafeJoin(root, req.Path)
	newP, err := s.kbResolveMoveTarget(req.InstanceID, req.Scope, workDir, root, oldP, req.NewName, false)
	if err != nil {
		return facade.KnowledgeRenameResponse{}, err
	}
	if err := kbGuardRenameTarget(oldP, newP); err != nil {
		return facade.KnowledgeRenameResponse{}, fmt.Errorf("rename: %v", err)
	}
	if err := os.Rename(oldP, newP); err != nil {
		return facade.KnowledgeRenameResponse{}, fmt.Errorf("rename: %v", err)
	}
	return facade.KnowledgeRenameResponse{OK: true}, nil
}

// KnowledgeMkdir 创建知识库目录。
func (s *Service) KnowledgeMkdir(req facade.KnowledgeMkdirRequest) (facade.KnowledgeMkdirResponse, error) {
	if req.Name == "" {
		return facade.KnowledgeMkdirResponse{}, errors.New("name required")
	}
	rel := filepath.ToSlash(filepath.Join(req.Parent, capfs.SanitizeName(req.Name)))
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeMkdirResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Parent)
	if err != nil {
		return facade.KnowledgeMkdirResponse{}, err
	}
	if err := os.MkdirAll(capfs.SafeJoin(root, rel), 0o755); err != nil {
		return facade.KnowledgeMkdirResponse{}, fmt.Errorf("mkdir: %v", err)
	}
	return facade.KnowledgeMkdirResponse{OK: true, Path: rel}, nil
}

// KnowledgeRmdir 递归删除知识库目录。
func (s *Service) KnowledgeRmdir(req facade.KnowledgeRmdirRequest) (facade.KnowledgeRmdirResponse, error) {
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeRmdirResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Path)
	if err != nil {
		return facade.KnowledgeRmdirResponse{}, err
	}
	if err := os.RemoveAll(capfs.SafeJoin(root, req.Path)); err != nil {
		return facade.KnowledgeRmdirResponse{}, fmt.Errorf("rmdir: %v", err)
	}
	return facade.KnowledgeRmdirResponse{OK: true}, nil
}

// KnowledgeRenameDir 目录改名，或**跨目录移动**该目录（NewName 含分隔符 = 移动；G-26）。
func (s *Service) KnowledgeRenameDir(req facade.KnowledgeRenameDirRequest) (facade.KnowledgeRenameDirResponse, error) {
	if req.NewName == "" {
		return facade.KnowledgeRenameDirResponse{}, errors.New("new_name required")
	}
	workDir, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.KnowledgeRenameDirResponse{}, err
	}
	root, err := s.kbRootOf(req.InstanceID, req.Scope, workDir, req.Path)
	if err != nil {
		return facade.KnowledgeRenameDirResponse{}, err
	}
	oldP := capfs.SafeJoin(root, req.Path)
	newP, err := s.kbResolveMoveTarget(req.InstanceID, req.Scope, workDir, root, oldP, req.NewName, true)
	if err != nil {
		return facade.KnowledgeRenameDirResponse{}, err
	}
	if err := kbGuardRenameTarget(oldP, newP); err != nil {
		return facade.KnowledgeRenameDirResponse{}, fmt.Errorf("rename-dir: %v", err)
	}
	if err := os.Rename(oldP, newP); err != nil {
		return facade.KnowledgeRenameDirResponse{}, fmt.Errorf("rename-dir: %v", err)
	}
	return facade.KnowledgeRenameDirResponse{OK: true}, nil
}

// ── 根解析 ──────────────────────────────────────────

// kbAppRoot 系统级知识库根（AppDir 注入 / 宿主可执行目录/capability）。
func (s *Service) kbAppRoot() (string, error) {
	return capfs.SystemRoot(s.AppDir)
}

// kbUserRoot 用户级知识库根（~/.chonkpilot/capability）。
func (s *Service) kbUserRoot() string {
	return capfs.UserRoot(s.UsrPath)
}

// kbProjectRoot 项目级知识库根（<work_dir>/.chonkpilot/capability；12-数据层）。
func kbProjectRoot(workDir string) string {
	return capfs.ProjectRoot(workDir)
}

// kbPrjUsrRoot 项目私有级知识库根（<prjusr 数据根>/capability = ~/.chonkpilot/data/<project-id>/capability）：
// 先打开 prj 库 → EnsureProjectID → 解析根（4 级恒可用，不把"未初始化"当前提）。
// 解析不出（实例未登记 / 打开 prj 失败 / project-id 缺失）→ 返回 ""（不阻断 app/user/project 三级）。
func (s *Service) kbPrjUsrCapRoot(instanceID string, scope facade.Scope) string {
	workDir, dataDir, err := s.CfgInstBind(instanceID, scope)
	if err != nil {
		return ""
	}
	prj, release, err := kernel.OpenPrjLayer(workDir, dataDir)
	if err != nil {
		return ""
	}
	defer release()
	id, err := data.EnsureProjectID(prj)
	if err != nil || id == "" {
		return ""
	}
	return capfs.PrjUsrRoot(id)
}

// kbRootOf 由路径推导知识库根：绝对路径按前缀归属 **项目私有 > 项目 > 用户 > 系统**（具体级优先），
// 相对路径默认项目级根（与旧行为一致：前端多回传 list 给出的绝对路径）。
func (s *Service) kbRootOf(instanceID string, scope facade.Scope, workDir, rel string) (string, error) {
	roots := []string{}
	if prjUsr := s.kbPrjUsrCapRoot(instanceID, scope); prjUsr != "" {
		roots = append(roots, prjUsr)
	}
	if workDir != "" {
		roots = append(roots, kbProjectRoot(workDir))
	}
	roots = append(roots, s.kbUserRoot())
	if app, err := s.kbAppRoot(); err == nil {
		roots = append(roots, app)
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if !filepath.IsAbs(rel) {
		// 相对路径默认归属项目级根（旧行为）；无项目级 → 首级。
		if workDir != "" {
			return kbProjectRoot(workDir), nil
		}
		if len(roots) > 0 {
			return roots[0], nil
		}
		return "", fmt.Errorf("kb root: no capability root available")
	}
	cleanAbs := filepath.ToSlash(filepath.Clean(rel))
	for _, root := range roots {
		rootSlash := strings.TrimSuffix(filepath.ToSlash(root), "/")
		if cleanAbs == rootSlash || strings.HasPrefix(cleanAbs, rootSlash+"/") {
			return root, nil
		}
	}
	return "", fmt.Errorf("kb root: path %q outside knowledge roots", rel)
}

// kbResolveMoveTarget 解析 data-knowledge-rename / rename-dir 的 new_name → 目标绝对路径。
//
// new_name 语义（G-26：知识库域承载拖拽移动，capability 路径不再经 filesys）：
//   - 无路径分隔符 = 同目录改名（旧行为：文件按 capfs.NormalizeFileName 补 .md，目录名原样）；
//   - 含路径分隔符 = **跨目录移动**，接受「知识库根相对路径」与「落在同一根内的绝对路径」两种写法。
//
// 作用域（安全底线，四条）：
//  1. 逐段校验：拒绝空段 / `.` / `..`，其余段经 capfs.SanitizeName 净化（分隔符与非法字符）；
//  2. 目标必须**严格**落在源根内（capfs.StrictlyWithin；Join 各段已无 `..`，此处再兜底）；
//  3. 目标再经 kbRootOf **归属复检** = 与源同一根 —— 跨级移动（prjusr↔project↔user↔app）一律拒绝；
//  4. 目标不得是源自身或其子路径（目录不能移入自己）。
func (s *Service) kbResolveMoveTarget(instanceID string, scope facade.Scope, workDir, root, oldP, newName string, isDir bool) (string, error) {
	name := strings.TrimSpace(newName)
	if name == "" {
		return "", fmt.Errorf("new_name required")
	}
	slashed := filepath.ToSlash(strings.ReplaceAll(name, "\\", "/"))
	var target string
	if !strings.Contains(slashed, "/") { // 纯名 = 同目录改名（旧语义不变）
		if slashed == "." || slashed == ".." {
			return "", fmt.Errorf("new_name: %q invalid", newName)
		}
		if isDir {
			target = filepath.Join(filepath.Dir(oldP), capfs.SanitizeName(name))
		} else {
			target = filepath.Join(filepath.Dir(oldP), capfs.NormalizeFileName(name))
		}
	} else { // 跨目录移动：逐段净化/校验后拼接在源根下
		rootSlash := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root)), "/")
		rel := slashed
		if strings.HasPrefix(slashed, rootSlash+"/") {
			rel = strings.TrimPrefix(slashed, rootSlash+"/") // 根内绝对路径 → 转根相对
		} else if filepath.IsAbs(filepath.FromSlash(slashed)) {
			return "", fmt.Errorf("new_name: target %q outside knowledge root", newName)
		}
		segs := strings.Split(rel, "/")
		out := make([]string, 0, len(segs))
		for _, seg := range segs {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				return "", fmt.Errorf("new_name: %q has an empty path segment", newName)
			}
			if seg == "." || seg == ".." {
				return "", fmt.Errorf("new_name: %q has an invalid path segment %q", newName, seg)
			}
			if seg = capfs.SanitizeName(seg); seg == "" {
				return "", fmt.Errorf("new_name: %q has an invalid path segment", newName)
			}
			out = append(out, seg)
		}
		last := out[len(out)-1]
		if isDir {
			out[len(out)-1] = capfs.SanitizeName(last)
		} else {
			out[len(out)-1] = capfs.NormalizeFileName(last)
		}
		target = filepath.Join(root, filepath.Join(out...))
	}
	if !capfs.StrictlyWithin(root, target) {
		return "", fmt.Errorf("new_name: target %q outside knowledge root", newName)
	}
	tRoot, err := s.kbRootOf(instanceID, scope, workDir, filepath.ToSlash(target))
	if err != nil {
		return "", fmt.Errorf("new_name: target %q: %v", newName, err)
	}
	if filepath.Clean(tRoot) != filepath.Clean(root) {
		return "", fmt.Errorf("new_name: target %q crosses knowledge root", newName)
	}
	if filepath.Clean(target) == filepath.Clean(oldP) {
		return target, nil // 同路径 = 无操作改名
	}
	if capfs.StrictlyWithin(oldP, target) {
		return "", fmt.Errorf("new_name: target %q inside source", newName)
	}
	return target, nil
}

// kbGuardRenameTarget 目标已存在 → 拒绝（不覆盖）。
// os.Rename 在 Windows/Unix 均会**静默替换**已存在的目标；而 data-knowledge-rename /
// rename-dir 无 `overwrite` 入参（61 §3.3 冻结面）→ 此处显式拦截，避免改名/拖拽静默丢数据。
// 目标与源为同一文件（Windows 仅大小写改名）→ 放行，仍交由 os.Rename 完成。
func kbGuardRenameTarget(oldP, newP string) error {
	if filepath.Clean(oldP) == filepath.Clean(newP) {
		return nil
	}
	oldFi, err := os.Stat(oldP)
	if err != nil {
		return err
	}
	if newFi, err := os.Stat(newP); err == nil {
		if os.SameFile(oldFi, newFi) {
			return nil
		}
		return fmt.Errorf("target %q exists", filepath.Base(newP))
	}
	return nil
}
