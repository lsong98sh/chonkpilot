// Package scenario 是 scenario 域门面实现（`chonkpilot-data/facade` 的 ScenarioAPI）的落地包
// （阶段 4「internal 下沉」：由 `chonkpilot-data/persist` 下沉至此）。
//
// 覆盖：场景 = **独立根 `scenarios/`**（与 capability/ 平级）/<场景目录>/ 的场景元素
// （列举 / 定位 / 保存 / 删除 / 还原出厂）。
// 出厂场景 = **app 级只读**（随发布只读资源 `scenarios/<id>/`，25 §8.1 #8 / T6）；
// 本实现**不再**持有任何代码内嵌默认场景（原 `capfs.DefaultScenarioAgents` 已删、list 物化已撤）。
// 把「场景领域对象 + 级别」翻译成三级场景根下的目录读写（`scenario.json` +
// `main.agent.md` + `*.agent.md`）；目录/文件规则与 agent 契约文本留在 capfs（门面不交路径规则）。
//
// ⚠️ 写入级别：只允许 user / project（app = 随发布只读资源）；显式 app → 复制到 user 再改。
// ⚠️ **场景 id 全局唯一（跨级亦然）**：save 时若 id 已存在于**其它**级别 → 拒绝并报错
// （三级"覆盖"语义整体不存在，25-MCP与场景分层模型 §6）；同级别同名 = 更新自己那份。
//
// 单一实现两处绑定（同一份代码，不并列两套写法）：
//   - inline 绑定：`facade/inline.New` 直接构造本服务（同进程直调，不经 MQ）；
//   - mq  绑定：persist 的 `data-scenario-*` handler **委托本文件的同一批方法**，
//     只负责信封（reply/fail）——故两条路径行为等价（有测试）。
//
// 变更广播（订阅面；23 §7）：Save / Delete / Restore 成功后由本实现广播既有
// `data-scenario-refresh`（{instance_id, id, op, list}）——任何绑定下订阅方照旧收到。
//
// internal 门禁：本包不导出给模块外（见 internal/kernel 包注释）。
package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// scenarioRootForWrite 解析写入目标级：只允许 user / project（app = 随发布只读资源）。
// 缺省 user；显式传 app → 视为复制到 user（编辑出厂场景 = 先复制到本机再改）。
func (s *Service) scenarioRootForWrite(level, workDir string) (string, string) {
	if level == capfs.KindProject && workDir != "" {
		return capfs.KindProject, capfs.ScenarioProjectRoot(workDir)
	}
	return capfs.KindUser, capfs.ScenarioUserRoot(s.UsrPath)
}

// scenarioListAll 合并三级场景根（app → user → project）。场景 id 全局唯一（跨级亦然，
// 25 §6）→ 三级并集**不会重名**，无需去重（原"同名可在不同级并存"语义已废除）。
// 出厂默认场景 = **app 级**（`scenarios/default/`，随发布只读资源，25 §8.1 #8 / T6）
// → 无需再向 user 级物化（原 `materializeDefaultScenario` 已随代码内嵌一并撤除）。
func (s *Service) scenarioListAll(levels []capfs.Level) []map[string]any {
	out := []map[string]any{}
	for _, lv := range levels {
		for _, dir := range capfs.ListScenarioDirs(lv.Root) {
			sc, err := capfs.ReadScenarioDir(lv.Kind, lv.Root, dir)
			if err != nil {
				continue
			}
			out = append(out, sc)
		}
	}
	return out
}

// scenarioLoadOne 按 id 定位场景；level 非空时限定级别，否则按 具体级优先（project → user → app）。
func (s *Service) scenarioLoadOne(levels []capfs.Level, id, level string) (map[string]any, error) {
	order := []string{capfs.KindProject, capfs.KindUser, capfs.KindApp}
	if level != "" {
		order = []string{level}
	}
	for _, want := range order {
		for _, lv := range levels {
			if lv.Kind != want || !capfs.ScenarioDirExists(lv.Root, id) {
				continue
			}
			return capfs.ReadScenarioDir(lv.Kind, lv.Root, id)
		}
	}
	return nil, errors.New("scenario not found: " + id)
}

// scenarioLevelsFor 解析场景可见的三级根（含实例 work_dir → project 级；未登记则仅 app+user）。
func (s *Service) scenarioLevelsFor(instanceID string, scope facade.Scope) []capfs.Level {
	return capfs.ScenarioRoots(s.AppDir, s.UsrPath, s.WorkDirLoose(instanceID, scope))
}

// ensureScenarioIDUnique 跨级重名校验（25-MCP与场景分层模型 §6）：场景 id **全局唯一（跨级亦然）**
// → 三级"覆盖"语义整体不存在。**同级别同名 = 更新自己那份，放行**；id 已存在于**其它**级别
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
		return "应用级（app，随发布只读）"
	case capfs.KindUser:
		return "用户级（user）"
	case capfs.KindProject:
		return "项目级（project）"
	}
	return kind
}

// ScenarioList 列举场景（app → user → project；出厂默认场景 = app 级随发布资源）。
func (s *Service) ScenarioList(req facade.ScenarioListRequest) (facade.ScenarioListResponse, error) {
	raw := s.scenarioListAll(s.scenarioLevelsFor(req.InstanceID, req.Scope))
	list := make([]facade.Scenario, 0, len(raw))
	for _, m := range raw {
		list = append(list, wire.ScenarioFromWire(m))
	}
	return facade.ScenarioListResponse{List: list}, nil
}

// ScenarioGet 按 id 定位场景（Level 空 = 具体级优先 project → user → app）。
func (s *Service) ScenarioGet(req facade.ScenarioGetRequest) (facade.ScenarioGetResponse, error) {
	m, err := s.scenarioLoadOne(s.scenarioLevelsFor(req.InstanceID, req.Scope), req.ScenarioID, req.Level)
	if err != nil {
		return facade.ScenarioGetResponse{}, err
	}
	return facade.ScenarioGetResponse{Scenario: wire.ScenarioFromWire(m)}, nil
}

// ScenarioSave 保存场景（级别只允许 user / project；app 级 = 复制到本机再改）。
// id **全局唯一（跨级亦然，25 §6）**：已存在于**其它**级别 → 拒绝（无覆盖语义）；
// 同级别同名 = 更新自己那份（放行）。
// 另：写盘前经 `capfs.WriteScenarioDir` 做**同场景 agent 重名校验** → 重名拒绝、不落盘（42 §2 (175)）。
func (s *Service) ScenarioSave(req facade.ScenarioSaveRequest) (facade.ScenarioSaveResponse, error) {
	sc := req.Scenario
	if sc.ID == "" {
		return facade.ScenarioSaveResponse{}, errors.New("id required")
	}
	workDir := s.WorkDirLoose(req.InstanceID, req.Scope)
	kind, root := s.scenarioRootForWrite(sc.Level, workDir)
	if err := s.ensureScenarioIDUnique(capfs.ScenarioRoots(s.AppDir, s.UsrPath, workDir), sc.ID, kind); err != nil {
		return facade.ScenarioSaveResponse{}, err
	}
	if err := capfs.WriteScenarioDir(kind, root, sc.ID, capfs.NormalizeScenarioPayload(wire.ScenarioToWire(sc))); err != nil {
		return facade.ScenarioSaveResponse{}, err
	}
	s.RefreshScoped("scenario", req.InstanceID, sc.ID, "save", req.Scope)
	return facade.ScenarioSaveResponse{OK: true, ID: sc.ID}, nil
}

// ScenarioDelete 删除场景（app 级只读；Level 空 = 具体级优先查找可写副本）。
func (s *Service) ScenarioDelete(req facade.ScenarioDeleteRequest) (facade.ScenarioDeleteResponse, error) {
	if req.ScenarioID == "" {
		return facade.ScenarioDeleteResponse{}, errors.New("id required")
	}
	workDir := s.WorkDirLoose(req.InstanceID, req.Scope)
	levels := capfs.ScenarioRoots(s.AppDir, s.UsrPath, workDir)
	level := req.Level
	if level == "" || level == capfs.KindApp {
		// 未指定级别：按 具体级优先 找可写副本（app 级只读）
		for _, want := range []string{capfs.KindProject, capfs.KindUser} {
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
	if level == capfs.KindApp {
		return facade.ScenarioDeleteResponse{}, fmt.Errorf("scenario at app level is read-only: %s", req.ScenarioID)
	}
	_, root := s.scenarioRootForWrite(level, workDir)
	if err := os.RemoveAll(filepath.Join(root, req.ScenarioID)); err != nil {
		return facade.ScenarioDeleteResponse{}, err
	}
	s.RefreshScoped("scenario", req.InstanceID, req.ScenarioID, "delete", req.Scope)
	return facade.ScenarioDeleteResponse{OK: true}, nil
}

// ScenarioRestore 还原场景：用**出厂（app 级）同名场景**覆盖 user 级该场景
// （`scenarios/<id>/`，随发布只读资源；app 级无同名 → 报错，无「代码内嵌默认场景」可回落）。
// 非 save 路径：不走跨级重名校验（25 §6 的重名校验只作用于 `ScenarioSave`）。
func (s *Service) ScenarioRestore(req facade.ScenarioRestoreRequest) (facade.ScenarioRestoreResponse, error) {
	id := req.ScenarioID
	if id == "" {
		id = capfs.DefaultScenarioKey
	}
	sys, err := capfs.ScenarioSystemRoot(s.AppDir)
	if err != nil {
		return facade.ScenarioRestoreResponse{}, err
	}
	if !capfs.ScenarioDirExists(sys, id) {
		return facade.ScenarioRestoreResponse{}, fmt.Errorf("no builtin scenario to restore: %s", id)
	}
	if err := capfs.CopyScenarioDir(sys, capfs.ScenarioUserRoot(s.UsrPath), id); err != nil {
		return facade.ScenarioRestoreResponse{}, err
	}
	s.RefreshScoped("scenario", req.InstanceID, id, "restore", req.Scope)
	return facade.ScenarioRestoreResponse{OK: true}, nil
}
