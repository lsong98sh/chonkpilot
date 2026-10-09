// Package project 是 project 域门面实现（`chonkpilot-data/facade` 的 ProjectAPI）的落地包。
//
// 覆盖：项目初始化 = **工程规格文件**（`<workDir>/.chonkpilot/project_spec.md`，存在即「已初始化」
// 标记）+ 工作目录**只读探测** + **项目级 capability agent 文件写入**（供场景向导把合成提示词
// 落文件后引用）。路径规则（落点）与探测忽略规则留在本侧（门面不交路径规则）。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// specRelPath 是工程规格文件相对工作目录的落点。
const specRelPath = ".chonkpilot/project_spec.md"

// Service 是 project 域门面实现（inline 绑定；persist 装配注入共享内核）。
type Service struct {
	*kernel.Base
}

// New 构造 project 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 project 域门面（缺方法即编译不过）。
var _ facade.ProjectAPI = (*Service)(nil)

// SpecPath 返回工作目录下工程规格文件的绝对路径（斜杠形态，供应答定位串）。
func SpecPath(workDir string) string {
	return filepath.ToSlash(filepath.Join(workDir, filepath.FromSlash(specRelPath)))
}

// projectWriteTrustRoot 返回写越界复验的**信任锚**（A-32）：解析（可信）workDir 的符号链接后拼
// 字面的 `.chonkpilot[<sub...>]` —— 该段位于工作区内、可被恶意仓库预置 symlink（指向根外），故
// 保持字面不解析；目录被链接到根外即被拦下。workDir 不可解析 → 回落字面。
func projectWriteTrustRoot(workDir string, sub ...string) string {
	wd := workDir
	if r, ok := capfs.RealPathOrAncestor(workDir); ok {
		wd = r
	}
	parts := append([]string{wd, ".chonkpilot"}, sub...)
	return filepath.Join(parts...)
}

// ProjectSpecExists 判断工程规格文件是否存在。
func (s *Service) ProjectSpecExists(req facade.ProjectSpecExistsRequest) (facade.ProjectSpecExistsResponse, error) {
	wd, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.ProjectSpecExistsResponse{}, err
	}
	path := SpecPath(wd)
	_, statErr := os.Stat(filepath.Join(wd, filepath.FromSlash(specRelPath)))
	return facade.ProjectSpecExistsResponse{Exists: statErr == nil, Path: path}, nil
}

// ProjectSpecRead 读工程规格文件（不存在 → Exists=false，不是错误）。
func (s *Service) ProjectSpecRead(req facade.ProjectSpecReadRequest) (facade.ProjectSpecReadResponse, error) {
	wd, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.ProjectSpecReadResponse{}, err
	}
	path := SpecPath(wd)
	data, readErr := os.ReadFile(filepath.Join(wd, filepath.FromSlash(specRelPath)))
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return facade.ProjectSpecReadResponse{Exists: false, Path: path}, nil
		}
		return facade.ProjectSpecReadResponse{}, readErr
	}
	return facade.ProjectSpecReadResponse{Exists: true, Path: path, Content: string(data)}, nil
}

// ProjectSpecWrite 写工程规格文件（整份覆盖；父目录不存在则创建）。
func (s *Service) ProjectSpecWrite(req facade.ProjectSpecWriteRequest) (facade.ProjectSpecWriteResponse, error) {
	wd, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.ProjectSpecWriteResponse{}, err
	}
	full := filepath.Join(wd, filepath.FromSlash(specRelPath))
	// 写前越界复验（A-32）：`.chonkpilot` 被预置为指向根外的 symlink → 拒绝，不跟随写入。
	if !capfs.RealPathWithin(projectWriteTrustRoot(wd), full) {
		return facade.ProjectSpecWriteResponse{}, errors.New("project spec path escapes work dir")
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return facade.ProjectSpecWriteResponse{}, err
	}
	if err := os.WriteFile(full, []byte(req.Content), 0o644); err != nil {
		return facade.ProjectSpecWriteResponse{}, err
	}
	return facade.ProjectSpecWriteResponse{OK: true, Path: SpecPath(wd)}, nil
}

// ProjectProbe 只读探测工作目录。
func (s *Service) ProjectProbe(req facade.ProjectProbeRequest) (facade.ProjectProbeResponse, error) {
	wd, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.ProjectProbeResponse{}, err
	}
	return Probe(wd), nil
}

// ProjectAgentWrite 写项目级 capability agent 文件
// （`<workDir>/.chonkpilot/capability/agents/<净化名>.agent.md`，契约分区文本），
// 返回场景可直接引用的引用串（`${workDir}/.chonkpilot/capability/agents/<名>.agent.md`）。
// 名称缺省 / 工作目录不可解析 → error（不静默落空文件）。
func (s *Service) ProjectAgentWrite(req facade.ProjectAgentWriteRequest) (facade.ProjectAgentWriteResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return facade.ProjectAgentWriteResponse{}, errors.New("name required")
	}
	wd, err := s.WorkDirFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.ProjectAgentWriteResponse{}, err
	}
	capRoot := capfs.ProjectRoot(wd)
	abs := filepath.Join(capRoot, capfs.DirAgents, capfs.FileName(name, "agent"))
	// 写前越界复验（A-32）：`.chonkpilot` 被预置为指向根外的 symlink → 拒绝，不跟随写入。
	if !capfs.RealPathWithin(projectWriteTrustRoot(wd, "capability"), abs) {
		return facade.ProjectAgentWriteResponse{}, errors.New("agent path escapes work dir")
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return facade.ProjectAgentWriteResponse{}, err
	}
	doc := capfs.Doc{Title: name, Meta: map[string]string{}, Description: req.Description, Content: req.Prompt}
	if rt := strings.TrimSpace(req.RoleTag); rt != "" {
		doc.Meta["roletag"] = rt
	}
	if err := os.WriteFile(abs, []byte(capfs.BuildDoc(doc)), 0o644); err != nil {
		return facade.ProjectAgentWriteResponse{}, err
	}
	ref, ok := capfs.AgentRefOf(abs, capfs.RefRoots{Project: capRoot})
	if !ok {
		return facade.ProjectAgentWriteResponse{}, errors.New("agent ref 解析失败: " + abs)
	}
	return facade.ProjectAgentWriteResponse{OK: true, Ref: ref, Path: filepath.ToSlash(abs)}, nil
}
