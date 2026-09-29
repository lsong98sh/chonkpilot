// Package capfs 是 data 组件门面实现的**文件化 capability 树**共享实现（阶段 4「internal
// 下沉」）：三级根 + 场景目录读写 + mcp 四原语契约解析/组装（原 persist/persist_capability.go
// 与 persist_knowledge.go 的契约 helper 逐字下移，供 knowledge / scenario / config 三域共用）。
//
// 三级根（12-数据层）：
//
//	系统 <exeDir>/capability          （随发布资源；注入 AppDir 可覆盖）
//	用户 ~/.chonkpilot/capability     （~ 由 usr 主库所在目录推导，测试可隔离）
//	项目 <workdir>/.chonkpilot/capability
//
// 每级下分层目录：`tools/`（工具契约）+ `knowledge/{skills,prompts,resources}`（技能/提示词/资源）；
// 系统级另有 `executors/`（内置执行器 exe，见构建脚本）。
//
// ⚠️ **场景不在此树内**：场景用**独立根 `scenarios/`**（与各级 capability 根**平级**，三级同构），
// 见 ScenarioRoots / ScenarioSystemRoot / ScenarioUserRoot / ScenarioProjectRoot（25 §6）。
//
// internal 门禁：本包不导出给模块外（见 kernel 包注释）。
package capfs

import (
	"os"
	"path/filepath"

	"github.com/chonkpilot/chonkpilot-data"
)

// 三级 capability 级别标识（kind 沿用 app=系统，见 12-数据层）。
const (
	KindApp     = "app"
	KindUser    = "user"
	KindProject = "project"
)

// KnowledgeDirName 是知识库原语文档（skills/prompts/resources）在 capability 根下的容器目录名。
const KnowledgeDirName = "knowledge"

// Level 是一级 capability 根。
type Level struct {
	Kind string // app|user|project
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
// 规则单源（装配层按同一规则解析三级根，避免路径规则分叉）。
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

// PromptsRoot 某级 capability 的 prompts 分类根（= <capRoot>/knowledge/prompts）。
// 属**知识库 prompt 分类**（如 summary.prompt.md），与场景**无关**——场景已迁独立根
// `scenarios/`（capfs.ScenarioRoots，25-MCP与场景分层模型 §6）。
func PromptsRoot(capRoot string) string {
	return filepath.Join(capRoot, KnowledgeDirName, "prompts")
}
