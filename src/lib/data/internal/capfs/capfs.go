// Package capfs 是 data 组件门面实现的**文件化 capability 树**共享实现（阶段 4「internal
// 下沉」）：四级根 + 场景目录读写 + mcp 四原语契约解析/组装（原 persist/persist_capability.go
// 与 persist_knowledge.go 的契约 helper 逐字下移，供 knowledge / scenario / config 三域共用）。
//
// 四级根（12-数据层）：
//
//	系统     <exeDir>/capability                        （随发布资源；注入 AppDir 可覆盖）
//	用户     ~/.chonkpilot/capability                   （~ 由 usr 主库所在目录推导，测试可隔离）
//	项目     <workDir>/.chonkpilot/capability
//	项目私有 ~/.chonkpilot/data/<project-id>/capability（prjusr 数据根下；project-id 取自 prj 库）
//
// 每级下**6 个扁平子目录**（**删除**旧 `knowledge/**` 归并层）：
//
//	prompts/（命令/提示词） tools/（工具） resources/（知识/资源）
//	skills/（技能）         agents/（智能体） scenarios/（场景）
//
// 系统级另有 `executors/`（内置执行器 exe，见构建脚本）。
//
// ⚠️ **场景根已移入 capability**：场景根 = `<级别根>/capability/scenarios/`（不再有与
// capability **平级**的独立 `scenarios/` 根），见 ScenariosRoot / ScenarioSystemRoot /
// ScenarioUserRoot / ScenarioProjectRoot / ScenarioPrjUsrRoot / ScenarioRoots。
//
// internal 门禁：本包不导出给模块外（见 kernel 包注释）。
package capfs

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/chonkpilot/chonkpilot-data"
)

// 四级 capability 级别标识（kind 沿用 app=系统，见 12-数据层）。
const (
	KindApp     = "app"     // 系统级
	KindUser    = "user"    // 用户级
	KindProject = "project" // 项目级
	KindPrjUsr  = "prjusr"  // 项目私有级（~/.chonkpilot/data/<project-id>）
)

// 每级下的 6 个扁平子目录名（**删除**旧 `knowledge/**` 归并层；12-数据层）。
const (
	DirPrompts   = "prompts"   // 命令/提示词
	DirTools     = "tools"     // 工具
	DirResources = "resources" // 知识/资源
	DirSkills    = "skills"    // 技能
	DirAgents    = "agents"    // 智能体
	DirScenarios = "scenarios" // 场景（场景根 = <级别根>/capability/scenarios）
)

// Level 是一级 capability 根。
type Level struct {
	Kind string // app|user|project|prjusr
	Root string
}

// SystemRoot 系统级 capability 根（AppDir 注入 / 可执行目录/capability）。
func SystemRoot(appDir string) (string, error) {
	if appDir != "" {
		return appDir, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "capability"), nil
}

// UserRoot 用户级 capability 根（~/.chonkpilot/capability；usrPath 注入时随其所在目录）。
// 规则单源（装配层按同一规则解析四级根，避免路径规则分叉）。
func UserRoot(usrPath string) string {
	if usrPath != "" {
		return filepath.Join(filepath.Dir(usrPath), "capability")
	}
	return filepath.Join(filepath.Dir(data.UserPath()), "capability")
}

// ProjectRoot 项目级 capability 根（<workdir>/.chonkpilot/capability）。
func ProjectRoot(workDir string) string {
	return filepath.Join(workDir, ".chonkpilot", "capability")
}

// PrjUsrRoot 项目私有级 capability 根（<prjusr 数据根>/capability =
// ~/.chonkpilot/data/<project-id>/capability）。project-id 由调用方从 prj 库解析（EnsureProjectID）。
func PrjUsrRoot(projectID string) string {
	return filepath.Join(data.PrjUsrPath(projectID), "capability")
}

// PromptsRoot 某级 capability 的 prompts 分类根（= <capRoot>/prompts）。
// 属**知识库 prompt 分类**（如 summary.prompt.md），与场景**无关**——场景根 = <capRoot>/scenarios
// （capfs.ScenariosRoot，25-MCP与场景分层模型 §6）。
func PromptsRoot(capRoot string) string {
	return filepath.Join(capRoot, DirPrompts)
}

// LevelPriority 返回级别的**具体度**（数值越大越具体，用于"同名定位/覆盖"优先序）：
// prjusr（项目私有） > project（项目） > user（用户） > app（系统）。
// 未知 kind → 0（最低）。
func LevelPriority(kind string) int {
	switch kind {
	case KindPrjUsr:
		return 3
	case KindProject:
		return 2
	case KindUser:
		return 1
	case KindApp:
		return 0
	}
	return 0
}

// PriorityOrder 返回四级 kind 的**具体度降序**（prjusr → project → user → app），
// 供"同名定位/覆盖"按具体级优先遍历（规则单源，避免各处硬编码同一顺序）。
func PriorityOrder() []string {
	ks := []string{KindApp, KindUser, KindProject, KindPrjUsr}
	sort.SliceStable(ks, func(i, j int) bool { return LevelPriority(ks[i]) > LevelPriority(ks[j]) })
	return ks
}
