// prompt 占位符替换（chonkpilot-server 侧）——两类：
//
//	{{toolchain.<key>}}  取值来源 = usr/prj 配置（经既有 data-user-config 通道，对齐 loadExecConfig 的读法）
//	{{path.<key>}}       取值来源 = 四级根路径（exe/user/data/work；见 replacePaths）
//
// 覆盖的 prompt 落点（均为"拼装后送给 LLM 的文本"，不改消息面结构）：
//   - 系统提示词**分层拼接后**的整段（globalLayerPrompt + systemDirectoryLayer + scenarioLayer
//   - agentLayer → newTurnCtx 注入 LLM 上下文最前，25 §3；数据层 `systemPrompt` 字段**已废除**）；
//   - 记忆库带出指引（memoryGuide，随每轮请求拼在最前）；
//   - 发往 LLM 的工具契约描述（toolsForLLM，gateway tools/list 的 description）；
//   - 无上下文单轮 LLM（onLLMSimple → llm-simple，如 compress 摘要 system 提示词）；
//   - 内嵌 mcp-server 的 capability 原语渲染（loadExecConfig → Config.SetToolchain →
//     prompts/get，见 chonkpilot-mcp-server/server.ReplaceToolchain）。
//
// 替换语义（已知 key 未配置 → 空串；未知 key → 原样保留；不影响 {{env.X}}/{{arg}}）见
// chonkpilot-mcp-server/server/toolchain.go。DSL 的 {{env.X}}（jobdsl.go）与本机制互不影响。
package server

import (
	"path/filepath"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/exedir"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

// toolchainUserConfigKeys 占位符 key → 配置键（与 gui 工具链探测 id 一致；usr/prj 同名）。
var toolchainUserConfigKeys = map[string]string{
	"java":   "javaPath",
	"python": "pythonPath",
	"node":   "nodePath",
	"go":     "goPath",
	"rust":   "rustPath",
	"c":      "cCompilerPath",
	"chrome": "chromePath",
}

// pathConfigKeys 是执行层路径键（usr / prj 均可配置；分层 **prj > usr > 默认 ""**，2026-09-19
// 接线 prj 覆盖，见 64-配置项一览 §4.1 的 prj 层路径键条目）。
var pathConfigKeys = []string{"pythonPath", "nodePath", "javaPath", "goPath", "rustPath", "cCompilerPath", "chromePath"}

// layeredPathValues 把 usr 标量（门面 UserConfigGet 的 config）与 prj 平铺表
// （门面 ConfigKVList(prj-config) 的 list）合成为**分层路径取值**：prj > usr > 默认 ""。
// 值统一 TrimSpace；prj 空串/缺失视为未覆盖（回落 usr），避免"置空"误清 usr 值。
// 纯函数，便于单测（不触总线/门面）。
func layeredPathValues(usr map[string]any, prj map[string]string) map[string]string {
	out := make(map[string]string, len(pathConfigKeys))
	for _, k := range pathConfigKeys {
		if usr != nil {
			if v, _ := usr[k].(string); strings.TrimSpace(v) != "" {
				out[k] = strings.TrimSpace(v)
			}
		}
		if prj != nil {
			if v, ok := prj[k]; ok && strings.TrimSpace(v) != "" {
				out[k] = strings.TrimSpace(v)
			}
		}
	}
	return out
}

// readUserConfig 读 usr 配置（门面 UserConfigGet 一次往返）；返回原始 error（调用方决定
// 是否记日志 / 保留现值）。config 为 nil 表示"无配置段"（非读取失败）。
//
// 抽出单点读取：loadExecConfig 与 resolvePathValues 同处一条同步链路时**只读一次**
// （去重前同一 instance 的 usr 配置被读两遍 → 每次 instance-register 多一次门面往返）。
func (s *Server) readUserConfig(instanceID string) (map[string]any, error) {
	res, err := s.cfg.UserConfigGet(facade.UserConfigGetRequest{
		InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		return nil, err
	}
	return res.Config, nil
}

// readPrjConfigList 读 prj 平铺表（门面 ConfigKVList(prj-config) 一次往返；prj + prjusr 合并）。
func (s *Server) readPrjConfigList(instanceID string) (map[string]string, error) {
	res, err := s.cfg.ConfigKVList(facade.ConfigKVListRequest{
		Domain: facade.DomainPrjConfig, InstanceID: instanceID, Scope: s.cfgScope(instanceID),
	})
	if err != nil {
		return nil, err
	}
	return res.List, nil
}

// resolvePathValues 读 usr 标量 + prj 平铺表并合成分层路径取值（**唯一读取入口**）：
// 执行器解释器/env 注入（loadExecConfig）与 {{toolchain.<key>}} 占位符取值（toolchainVars）
// 都走这里，避免"两处各读一份"。返回 ok=false = 两层读取皆失败（调用方保留现值，不误清空）。
// 数据面 = **data 门面**（阶段 4 第二批 / 41 G-34：原 data-user-config-load +
// data-prj-config-list 两条 MQ 请求改为门面 inline 调用）。
func (s *Server) resolvePathValues(instanceID string) (map[string]string, bool) {
	usr, usrErr := s.readUserConfig(instanceID)
	prj, prjErr := s.readPrjConfigList(instanceID)
	return layeredPathValues(usr, prj), usrErr == nil || prjErr == nil
}

// toolchainVars 读分层路径取值（prj > usr > 默认）组装占位符取值表（resolvePathValues）。
// 已知 key 恒出现（未配置 → ""，即"未配置替换为空串"）；两层皆读失败 → 全空表（等价全未配置）。
func (s *Server) toolchainVars(instanceID string) map[string]string {
	values, _ := s.resolvePathValues(instanceID)
	vars := make(map[string]string, len(mcpms.ToolchainKeys))
	for _, k := range mcpms.ToolchainKeys {
		vars[k] = values[toolchainUserConfigKeys[k]]
	}
	return vars
}

// replaceToolchain 替换 prompt 文本中的 {{toolchain.<key>}}（本模块各落点统一入口）：
// 文本不含占位符时零成本直返（不发 data-user-config-load）。
func (s *Server) replaceToolchain(instanceID, text string) string {
	if text == "" || !strings.Contains(text, mcpms.ToolchainPlaceholder) {
		return text
	}
	return mcpms.ReplaceToolchain(text, s.toolchainVars(instanceID))
}

// ── prompt 占位符 {{path.<key>}}（四种根路径，2026-10-04）────────────────────
//
// 用途：agent `[content]`（技能 / 规范）在系统提示词组装时**按绝对路径渲染**，指示 LLM
// 用文件工具按路径自读（LLM 经 mcp_find/mcp_load 读不到能力面 `capability/skills/*`）。
// 四种根与四级 capability 根同构：
//
//	{{path.exeDir}}  → <exeDir>（app 根 = {{path.exeDir}}/capability）
//	{{path.userDir}} → ~/.chonkpilot（user 根 = {{path.userDir}}/capability）
//	{{path.dataDir}} → ~/.chonkpilot/data/<project-id>（prjusr 根 = {{path.dataDir}}/capability）
//	{{path.workDir}} → <workDir>（project 根 = {{path.workDir}}/.chonkpilot/capability）
//
// 替换语义同 ReplaceToolchain：未知 key 原样保留；已知但解析不出 → 空串。

// pathVars 组装 {{path.<key>}} 占位符取值表（exe / user / data / work 四种根）。
// dataDir 需实例的 prjusr 根（经门面 KnowledgeRoot(kind=prjusr) 解析）；解析不出 → 空串。
func (s *Server) pathVars(instanceID string) map[string]string {
	scope := s.cfgScope(instanceID)
	vars := map[string]string{
		"exeDir":  "",
		"userDir": filepath.Dir(persist.CapUserRoot(s.opts.UsrPath)),
		"dataDir": "",
		"workDir": strings.TrimSpace(scope.WorkDir),
	}
	if d, err := exedir.Dir(); err == nil {
		vars["exeDir"] = d
	}
	if s.cfg != nil {
		if resp, err := s.cfg.KnowledgeRoot(facade.KnowledgeRootRequest{
			InstanceID: instanceID, Kind: persist.KindPrjUsr, Scope: scope,
		}); err == nil && strings.TrimSpace(resp.Root) != "" {
			vars["dataDir"] = filepath.Dir(resp.Root)
		}
	}
	return vars
}

// replacePaths 替换 prompt 文本中的 {{path.<key>}}（四种根路径）：
// 文本不含占位符时零成本直返（不解析路径 / 不发门面请求）。
func (s *Server) replacePaths(instanceID, text string) string {
	if text == "" || !strings.Contains(text, "{{path.") {
		return text
	}
	for k, v := range s.pathVars(instanceID) {
		text = strings.ReplaceAll(text, "{{path."+k+"}}", v)
	}
	return text
}
