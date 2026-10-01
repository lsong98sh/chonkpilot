// Package scenario 是 scenario 域门面实现（`chonkpilot-data/facade` 的 ScenarioAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：场景 = **capability 根下的 `scenarios/` 子目录**（`<级别根>/capability/scenarios/`；四级同构
// app / user / project / prjusr）/<场景目录>/ 的场景元素（列举 / 定位 / 保存 / 删除）。
// 四级根 app / user / project / prjusr **均可编辑**；app 级（`<exeDir>/capability/scenarios/`）出厂内容 = **磁盘目录**
// （源 `src/initdata/capability/scenarios/`，由构建脚本投放）——**不再 embed、不再自动物化**：根缺失即为缺装
// 状态，读取前给出明确提示（checkFactoryScenarios）。
// 把「场景领域对象 + 级别」翻译成四级场景根下的目录读写（`scenario.json` +
// `main.agent.md` + `*.agent.md`）；目录/文件规则与 agent 契约文本留在 capfs（门面不交路径规则）。
//
// ⚠️ 写入级别：app / user / project / prjusr 四级均可写（app 级不再只读）。
// ⚠️ **场景 id 全局唯一（跨级亦然）**：save 时若 id 已存在于**其它**级别 → 拒绝并报错
// （四级"覆盖"语义整体不存在，25-MCP与场景分层模型 §6）；同级别同名 = 更新自己那份。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）；
//   - mq  绑定：persist 的 `data-scenario-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 变更广播（订阅面；23 §7）：Save / Delete 成功后由本实现广播既有
// `data-scenario-refresh`（{instance_id, id, op, list}）——任何绑定下订阅方照旧收到。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/facade/wire"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// Service 是 scenario 域门面实现（inline 绑定与 MQ 信封层共用）。
type Service struct {
	*kernel.Base
}

// New 构造 scenario 域服务（共享内核由装配侧注入）。
func New(base *kernel.Base) *Service { return &Service{Base: base} }

// 编译期断言：实现完整 scenario 域门面（缺方法即编译不过）。
var _ facade.ScenarioAPI = (*Service)(nil)

// checkFactoryScenarios 校验 **app 级场景根**（`<exeDir>/capability/scenarios/`）是否就位：
// 出厂场景现为**磁盘目录**（源 `src/initdata/capability/scenarios/`，由构建脚本投放）——**不再 embed、
// 不再自动物化**。根缺失（缺装）→ 输出明确提示（重新安装或用 `initial.zip` 恢复），
// 读取侧照旧工作（仅能读到 user/project/prjusr 级）。幂等、不产生任何写入。
func (s *Service) checkFactoryScenarios() {
	appRoot, err := capfs.ScenarioSystemRoot(s.AppDir)
	if err != nil {
		return
	}
	if _, err := os.Stat(appRoot); err != nil {
		if s.Warnf != nil {
			s.Warnf("scenario: 缺少出厂场景目录 %s，请重新安装或用 initial.zip 恢复", appRoot)
		}
	}
}

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

// scenarioRootForWrite 解析写入目标级（app / user / project / prjusr 四级均可写）。
// 缺省 user；project 需 workDir 非空（否则回落 user）；prjusr 需 prjUsrCapRoot 非空（否则回落 user）。
func (s *Service) scenarioRootForWrite(level, workDir, prjUsrCapRoot string) (string, string) {
	switch level {
	case capfs.KindApp:
		if root, err := capfs.ScenarioSystemRoot(s.AppDir); err == nil {
			return capfs.KindApp, root
		}
	case capfs.KindProject:
		if workDir != "" {
			return capfs.KindProject, capfs.ScenarioProjectRoot(workDir)
		}
	case capfs.KindPrjUsr:
		if prjUsrCapRoot != "" {
			return capfs.KindPrjUsr, capfs.ScenariosRoot(prjUsrCapRoot)
		}
	}
	return capfs.KindUser, capfs.ScenarioUserRoot(s.UsrPath)
}

// refRootsOf 由四级**场景根**推导四级 **capability 根**（= filepath.Dir(场景根)）→ 供场景
// agent 引用的展开/生成（capfs.RefRoots；P4）。缺失级别 → 空串（该级引用视为悬空）。
func refRootsOf(levels []capfs.Level) capfs.RefRoots {
	var r capfs.RefRoots
	for _, lv := range levels {
		capRoot := filepath.Dir(lv.Root)
		switch lv.Kind {
		case capfs.KindApp:
			r.App = capRoot
		case capfs.KindUser:
			r.User = capRoot
		case capfs.KindProject:
			r.Project = capRoot
		case capfs.KindPrjUsr:
			r.PrjUsr = capRoot
		}
	}
	return r
}

// scenarioListAll 合并四级场景根（app → user → project → prjusr）。场景 id 全局唯一（跨级亦然，
// 25 §6）→ 四级并集**不会重名**，无需去重（原"同名可在不同级并存"语义已废除）。
// app 级 = 出厂场景来源（出厂内容 = 磁盘 `<exeDir>/capability/scenarios/`，缺装时 checkFactoryScenarios 提示）。
func (s *Service) scenarioListAll(levels []capfs.Level) []map[string]any {
	out := []map[string]any{}
	roots := refRootsOf(levels)
	for _, lv := range levels {
		for _, dir := range capfs.ListScenarioDirs(lv.Root) {
			sc, err := capfs.ReadScenarioDir(lv.Kind, lv.Root, dir, roots)
			if err != nil {
				continue
			}
			out = append(out, sc)
		}
	}
	return out
}

// scenarioLoadOne 按 id 定位场景；level 非空时限定级别，否则按 具体级优先
// （prjusr → project → user → app）。
func (s *Service) scenarioLoadOne(levels []capfs.Level, id, level string) (map[string]any, error) {
	order := capfs.PriorityOrder() // 具体级优先：prjusr → project → user → app
	if level != "" {
		order = []string{level}
	}
	roots := refRootsOf(levels)
	for _, want := range order {
		for _, lv := range levels {
			if lv.Kind != want || !capfs.ScenarioDirExists(lv.Root, id) {
				continue
			}
			return capfs.ReadScenarioDir(lv.Kind, lv.Root, id, roots)
		}
	}
	return nil, errors.New("scenario not found: " + id)
}

// scenarioLevelsFor 解析场景可见的**四级根**（含实例 work_dir → project 级、prjusr → 项目私有级；
// 未登记则仅 app + user）。
func (s *Service) scenarioLevelsFor(instanceID string, scope facade.Scope) []capfs.Level {
	return capfs.ScenarioRoots(s.AppDir, s.UsrPath, s.WorkDirLoose(instanceID, scope), s.prjUsrCapRoot(instanceID, scope))
}

// ensureScenarioIDUnique 跨级重名校验（25-MCP与场景分层模型 §6）：场景 id **全局唯一（跨级亦然）**
// → 四级"覆盖"语义整体不存在。**同级别同名 = 更新自己那份，放行**；id 已存在于**其它**级别
// → 拒绝并指明级别（错误信息指导用户改用其它 id 或先删除该级同名场景）。
func (s *Service) ensureScenarioIDUnique(levels []capfs.Level, id, targetKind string) error {
	for _, lv := range levels {
		if lv.Kind == targetKind {
			continue
		}
		if capfs.ScenarioDirExists(lv.Root, id) {
			return fmt.Errorf("场景 id %q 已存在于%s：场景 id 全局唯一（不允许跨级同名），"+
				"请改用其它 id，或先删除该级同名场景", id, scenarioLevelLabel(lv.Kind))
		}
	}
	return nil
}

// scenarioLevelLabel 级别中文名（重名错误文案用）。
func scenarioLevelLabel(kind string) string {
	switch kind {
	case capfs.KindApp:
		return "应用级（app）"
	case capfs.KindUser:
		return "用户级（user）"
	case capfs.KindProject:
		return "项目级（project）"
	case capfs.KindPrjUsr:
		return "项目私有级（prjusr）"
	}
	return kind
}

// ScenarioList 列举场景（app → user → project → prjusr；app 级 = 出厂场景来源，缺装时提示）。
func (s *Service) ScenarioList(req facade.ScenarioListRequest) (facade.ScenarioListResponse, error) {
	s.checkFactoryScenarios()
	raw := s.scenarioListAll(s.scenarioLevelsFor(req.InstanceID, req.Scope))
	list := make([]facade.Scenario, 0, len(raw))
	for _, m := range raw {
		list = append(list, wire.ScenarioFromWire(m))
	}
	return facade.ScenarioListResponse{List: list}, nil
}

// ScenarioGet 按 id 定位场景（Level 空 = 具体级优先 prjusr → project → user → app）。
func (s *Service) ScenarioGet(req facade.ScenarioGetRequest) (facade.ScenarioGetResponse, error) {
	s.checkFactoryScenarios()
	m, err := s.scenarioLoadOne(s.scenarioLevelsFor(req.InstanceID, req.Scope), req.ScenarioID, req.Level)
	if err != nil {
		return facade.ScenarioGetResponse{}, err
	}
	return facade.ScenarioGetResponse{Scenario: wire.ScenarioFromWire(m)}, nil
}

// ScenarioSave 保存场景（级别可为 app / user / project / prjusr：四级均可编辑）。
// id **全局唯一（跨级亦然，25 §6）**：已存在于**其它**级别 → 拒绝（无覆盖语义）；
// 同级别同名 = 更新自己那份（放行）。
// 另：写盘前经 `capfs.WriteScenarioDir` 做**同场景 agent 重名校验** → 重名拒绝、不落盘（42 §2 (175)）。
func (s *Service) ScenarioSave(req facade.ScenarioSaveRequest) (facade.ScenarioSaveResponse, error) {
	sc := req.Scenario
	if sc.ID == "" {
		return facade.ScenarioSaveResponse{}, errors.New("id required")
	}
	workDir := s.WorkDirLoose(req.InstanceID, req.Scope)
	prjUsrCap := s.prjUsrCapRoot(req.InstanceID, req.Scope)
	kind, root := s.scenarioRootForWrite(sc.Level, workDir, prjUsrCap)
	levels := capfs.ScenarioRoots(s.AppDir, s.UsrPath, workDir, prjUsrCap)
	if err := s.ensureScenarioIDUnique(levels, sc.ID, kind); err != nil {
		return facade.ScenarioSaveResponse{}, err
	}
	if err := capfs.WriteScenarioDir(kind, root, sc.ID, capfs.NormalizeScenarioPayload(wire.ScenarioToWire(sc)), refRootsOf(levels)); err != nil {
		return facade.ScenarioSaveResponse{}, err
	}
	s.RefreshScoped("scenario", req.InstanceID, sc.ID, "save", req.Scope)
	return facade.ScenarioSaveResponse{OK: true, ID: sc.ID}, nil
}

// ScenarioDelete 删除场景（app / user / project / prjusr 四级均可删；Level 空 = 具体级优先
// prjusr → project → user → app）。
func (s *Service) ScenarioDelete(req facade.ScenarioDeleteRequest) (facade.ScenarioDeleteResponse, error) {
	if req.ScenarioID == "" {
		return facade.ScenarioDeleteResponse{}, errors.New("id required")
	}
	workDir := s.WorkDirLoose(req.InstanceID, req.Scope)
	prjUsrCap := s.prjUsrCapRoot(req.InstanceID, req.Scope)
	levels := capfs.ScenarioRoots(s.AppDir, s.UsrPath, workDir, prjUsrCap)
	level := req.Level
	if level == "" {
		// 未指定级别：按 具体级优先 找实际存在的副本（prjusr → project → user → app）
		for _, want := range capfs.PriorityOrder() {
			for _, lv := range levels {
				if lv.Kind == want && capfs.ScenarioDirExists(lv.Root, req.ScenarioID) {
					level = want
				}
			}
			if level != "" {
				break
			}
		}
	}
	if level == "" {
		level = capfs.KindUser
	}
	_, root := s.scenarioRootForWrite(level, workDir, prjUsrCap)
	if err := os.RemoveAll(filepath.Join(root, req.ScenarioID)); err != nil {
		return facade.ScenarioDeleteResponse{}, err
	}
	s.RefreshScoped("scenario", req.InstanceID, req.ScenarioID, "delete", req.Scope)
	return facade.ScenarioDeleteResponse{OK: true}, nil
}
