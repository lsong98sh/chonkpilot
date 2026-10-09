// Package mcp 是 mcp 域门面实现（`chonkpilot-data/facade` 的 McpAPI）的落地包
// （阶段 4「internal 下沉」的延续；与 scenario 域同构）。
//
// 覆盖：MCP server 配置 = **四级 `<级别>/capability/mcps/<名>.json` 文件**
// （app / user / project / prjusr）/<名>.json 的领域元素（列举 / 定位 / 保存 / 删除）。
// 四级根与场景根**同构**（capfs.McpRoots）；四级均可编辑。
//
// 同名跨级语义（25-MCP与场景分层模型 §4）：**同名 = 最具体级优先、整条覆盖**
// （prjusr > project > user > app）——McpList 返回生效定义（Level = 命中的最具体级）。
//
// 存储来源：本域**只**承载文件化配置（新写入一律走文件）；旧 usr KV `mcpServers`（专用表
// `mcps`）已于 2026-10-01 彻底废弃（代码零兼容），不再有回落。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）；
//   - mq  绑定：persist 的 `data-mcp-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 变更广播（订阅面；23 §7）：Save / Delete 成功后由本实现广播既有
// `data-mcp-refresh`（{instance_id, id, op, list}）——任何绑定下订阅方（gateway 热生效）照旧收到。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package mcp

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// Service 是 mcp 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 mcp 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 mcp 域门面（缺方法即编译不过）。
var _ facade.McpAPI = (*Service)(nil)

// prjUsrCapRoot 解析实例的**项目私有级 capability 根**（打开 prj 库 → EnsureProjectID →
// capfs.PrjUsrRoot）；解析不出（实例未登记 / prj 打不开 / project-id 缺失）→ 空串（不含该级）。
func (s *Service) prjUsrCapRoot(instanceID string, scope facade.Scope) string {
	workDir, dataDir, ok := s.InstBindingFor(instanceID, scope)
	if !ok {
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

// mcpLevelsFor 解析 MCP 可见的**四级根**（含实例 work_dir → project 级、prjusr → 项目私有级；
// 未登记则仅 app + user）。
func (s *Service) mcpLevelsFor(instanceID string, scope facade.Scope) []capfs.Level {
	return capfs.McpRoots(s.AppDir, s.UsrPath, s.WorkDirLoose(instanceID, scope), s.prjUsrCapRoot(instanceID, scope))
}

// mcpRootForWrite 解析写入目标级（app / user / project / prjusr 四级均可写）。
// 缺省 user；project 需 workDir 非空（否则回落 user）；prjusr 需 prjUsrCapRoot 非空（否则回落 user）；
// app 需系统级根可解析（否则回落 user）。
func (s *Service) mcpRootForWrite(level, workDir, prjUsrCapRoot string) (string, string) {
	switch level {
	case capfs.KindApp:
		if root, err := capfs.McpSystemRoot(s.AppDir); err == nil {
			return capfs.KindApp, root
		}
	case capfs.KindProject:
		if workDir != "" {
			return capfs.KindProject, capfs.McpProjectRoot(workDir)
		}
	case capfs.KindPrjUsr:
		if prjUsrCapRoot != "" {
			return capfs.KindPrjUsr, capfs.McpsRoot(prjUsrCapRoot)
		}
	}
	return capfs.KindUser, capfs.McpUserRoot(s.UsrPath)
}

// mcpFromMap 把文件字段 map（补 name/level）转门面 DTO。
func mcpFromMap(name, level string, obj map[string]any) facade.McpServer {
	m := make(map[string]any, len(obj)+2)
	for k, v := range obj {
		m[k] = v
	}
	m["name"] = name
	m["level"] = level
	b, _ := json.Marshal(m)
	var s facade.McpServer
	_ = json.Unmarshal(b, &s)
	if s.Name == "" {
		s.Name = name
	}
	s.Level = level
	return s
}

// mcpToMap 把门面 DTO 转文件字段 map（name/level 由文件名与所在级承载，capfs.WriteMcpFile 会剔除）。
func mcpToMap(s facade.McpServer) map[string]any {
	b, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// McpList 列举生效 MCP server（四级合并；同名最具体级优先、整条覆盖）。
// 遍历序 = app → user → project → prjusr，具体级在后 → 后者覆盖前者；结果按名排序（稳定可断言）。
func (s *Service) McpList(req facade.McpListRequest) (facade.McpListResponse, error) {
	levels := s.mcpLevelsFor(req.InstanceID, req.Scope)
	byName := map[string]facade.McpServer{}
	for _, lv := range levels {
		for _, name := range capfs.ListMcpNames(lv.Root) {
			obj, ok := capfs.ReadMcpFile(lv.Root, name)
			if !ok {
				continue
			}
			byName[name] = mcpFromMap(name, lv.Kind, obj) // 具体级覆盖（整条）
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]facade.McpServer, 0, len(names))
	for _, n := range names {
		list = append(list, byName[n])
	}
	return facade.McpListResponse{List: list}, nil
}

// McpGet 按名定位（Level 空 = 具体级优先 prjusr → project → user → app）。
func (s *Service) McpGet(req facade.McpGetRequest) (facade.McpGetResponse, error) {
	if req.Name == "" {
		return facade.McpGetResponse{}, errors.New("name required")
	}
	levels := s.mcpLevelsFor(req.InstanceID, req.Scope)
	order := capfs.PriorityOrder()
	if req.Level != "" {
		order = []string{req.Level}
	}
	for _, want := range order {
		for _, lv := range levels {
			if lv.Kind != want {
				continue
			}
			if obj, ok := capfs.ReadMcpFile(lv.Root, req.Name); ok {
				return facade.McpGetResponse{Server: mcpFromMap(req.Name, lv.Kind, obj)}, nil
			}
		}
	}
	return facade.McpGetResponse{}, errors.New("mcp server not found: " + req.Name)
}

// McpSave 保存 MCP server（按 Server.Level 落对应级 `<名>.json`；Level 空 = 用户级）。
// OldName 非空且与新名/级别不同 → 先删旧文件（改名/移级不残留）。名非法 → 拒绝。
func (s *Service) McpSave(req facade.McpSaveRequest) (facade.McpSaveResponse, error) {
	srv := req.Server
	if !capfs.ValidMcpName(srv.Name) {
		return facade.McpSaveResponse{}, errors.New("invalid mcp server name: " + srv.Name)
	}
	workDir := s.WorkDirLoose(req.InstanceID, req.Scope)
	prjUsrCap := s.prjUsrCapRoot(req.InstanceID, req.Scope)
	kind, root := s.mcpRootForWrite(srv.Level, workDir, prjUsrCap)
	srv.Level = kind
	// 改名/移级：先删旧文件（不同名或不同级别）
	if req.OldName != "" && (req.OldName != srv.Name || req.OldLevel != kind) {
		if oldKind, oldRoot := s.mcpRootForWrite(req.OldLevel, workDir, prjUsrCap); req.OldLevel == "" || oldKind == req.OldLevel {
			// 尽力清理旧定义；失败（越界 / IO）留痕，但不阻断新写入（best-effort 语义不变，A-42）。
			if err := capfs.DeleteMcpFile(oldRoot, req.OldName); err != nil && s.Warnf != nil {
				s.Warnf("mcp: 删除旧定义 %s/%s 失败：%v", oldRoot, req.OldName, err)
			}
		}
	}
	if err := capfs.WriteMcpFile(root, srv.Name, mcpToMap(srv)); err != nil {
		return facade.McpSaveResponse{}, err
	}
	s.RefreshScoped("mcp", req.InstanceID, srv.Name, "save", req.Scope)
	return facade.McpSaveResponse{OK: true, Name: srv.Name}, nil
}

// McpDelete 删除 MCP server（Level 空 = 具体级优先 prjusr → project → user → app 删命中的副本）。
func (s *Service) McpDelete(req facade.McpDeleteRequest) (facade.McpDeleteResponse, error) {
	if !capfs.ValidMcpName(req.Name) {
		return facade.McpDeleteResponse{}, errors.New("invalid mcp server name: " + req.Name)
	}
	workDir := s.WorkDirLoose(req.InstanceID, req.Scope)
	prjUsrCap := s.prjUsrCapRoot(req.InstanceID, req.Scope)
	levels := s.mcpLevelsFor(req.InstanceID, req.Scope)
	order := capfs.PriorityOrder()
	if req.Level != "" {
		order = []string{req.Level}
	}
	deleted := false
	for _, want := range order {
		for _, lv := range levels {
			if lv.Kind != want || !capfs.McpFileExists(lv.Root, req.Name) {
				continue
			}
			if err := capfs.DeleteMcpFile(lv.Root, req.Name); err != nil {
				return facade.McpDeleteResponse{}, err
			}
			deleted = true
		}
		if deleted {
			break
		}
	}
	// 指定级别删除（order = 单级）时，未命中也要尝试该级根（层级可能不在 levels 内，如 prjusr 未解析）
	if !deleted && req.Level != "" {
		if _, root := s.mcpRootForWrite(req.Level, workDir, prjUsrCap); capfs.McpFileExists(root, req.Name) {
			if err := capfs.DeleteMcpFile(root, req.Name); err != nil {
				return facade.McpDeleteResponse{}, err
			}
		}
	}
	s.RefreshScoped("mcp", req.InstanceID, req.Name, "delete", req.Scope)
	return facade.McpDeleteResponse{OK: true}, nil
}
