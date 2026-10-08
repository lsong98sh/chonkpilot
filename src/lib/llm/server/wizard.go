// 「场景向导」（Agent Wizard）方法面：探测工作目录 / 生成（写场景 + 项目记忆 + 辅助选项配置
// + 工程规格 [+ git init]）/ 跳过。
//
//	agent-wizard-probe    {instance_id}                          → {ok, probe}
//	agent-wizard-generate {instance_id, scenario_id, scene_name, description, agents[], memory[], project_spec,
//	                       config{}, init_git}
//	                                                              → {ok, scenario_id, spec_path, memory_saved[],
//	                                                                 errors, config_saved[], git_initialized}
//	agent-wizard-skip     {instance_id}                          → {ok}（无副作用；本会话不再自动弹）
//
// 均为**同步方法面**（结果写 v.Result，无 `.reply` 事件）。数据访问一律经 data 门面（s.cfg），
// 不直开库（23 §7）：探测 = ProjectProbe（只读）；生成 = ProjectAgentWrite（非主 agent 的合成
// 提示词落**项目级 capability agent 文件**，改以引用承载 → 子 agent 唯一形态 = 引用）
// + ScenarioSave（场景，Level 恒 "project"）+ MemorySave（逐类项目记忆）+ ConfigKVSet
// （prj-config 辅助选项，写入由门面广播 data-prj-config-refresh 热生效）+ ProjectSpecWrite
// （写 `<workDir>/.chonkpilot/project_spec.md` = 初始化完成标记）+ 可选 `git init`。
// 记忆 / 配置 / 规格 / git 失败只记入 errors，不改变已成功步骤（设计 01 §7.4 分步可重试）；
// agent 落文件失败 = 失败（scenario 保存的前置）。
package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/winproc"
)

// agentWizardProbeReq / agentWizardSkipReq 是向导探测 / 跳过请求载荷（instance_id 由桥/入口注入）。
type agentWizardProbeReq struct {
	InstanceID string `json:"instance_id"`
}

// agentWizardAgentReq 是生成请求里的一个 agent（映射 facade.ScenarioAgent）。
type agentWizardAgentReq struct {
	Name    string `json:"name"`
	RoleTag string `json:"roletag"`
	IsMain  bool   `json:"is_main"`
	Prompt  string `json:"prompt"`
	Ref     string `json:"ref"`
}

// agentWizardMemoryReq 是一个项目记忆条目（映射 facade.MemorySaveRequest）。
type agentWizardMemoryReq struct {
	Category string `json:"category"`
	Content  string `json:"content"`
}

// agentWizardGenerateReq 是生成请求载荷。
type agentWizardGenerateReq struct {
	InstanceID  string                 `json:"instance_id"`
	ScenarioID  string                 `json:"scenario_id"`
	SceneName   string                 `json:"scene_name"`
	Description string                 `json:"description"`
	Agents      []agentWizardAgentReq  `json:"agents"`
	Memory      []agentWizardMemoryReq `json:"memory"`
	ProjectSpec string                 `json:"project_spec"`
	// Config 辅助选项对应的 prj 配置键 → 值（字符串 "true"/"false"；可空）。
	Config map[string]string `json:"config"`
	// InitGit 为 true 且 `<workDir>/.git` 不存在时，在工作目录执行 `git init`。
	InitGit bool `json:"init_git"`
}

// onAgentWizardProbe 只读探测工作目录（空目录判定 + 语言/框架/包管理/构建/测试/lint/结构）。
func (s *Server) onAgentWizardProbe(_ context.Context, _ string, v *mq.Value) error {
	var req agentWizardProbeReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Result = map[string]any{"ok": false, "error": "invalid payload"}
		return nil
	}
	if req.InstanceID == "" {
		v.Result = map[string]any{"ok": false, "error": "instance_id required"}
		return nil
	}
	if s.cfg == nil {
		v.Result = map[string]any{"ok": false, "error": "data facade unavailable"}
		return nil
	}
	probe, err := s.cfg.ProjectProbe(facade.ProjectProbeRequest{
		InstanceID: req.InstanceID, Scope: s.cfgScope(req.InstanceID),
	})
	if err != nil {
		logf("[chonkpilot-server] agent-wizard-probe: ProjectProbe failed: %v\n", err)
		v.Result = map[string]any{"ok": false, "error": err.Error()}
		return nil
	}
	v.Result = map[string]any{"ok": true, "probe": probe}
	return nil
}

// onAgentWizardGenerate 一次生成：写场景（Level 恒 project）→ 逐类写项目记忆 → 写辅助选项配置
// （prj-config）→ 写工程规格 →（可选）`git init`。
//
// 失败口径：场景保存失败 = 失败（后续步骤全不做）；记忆 / 配置 / 规格 / git 单步失败只记 errors
// 不阻断（已成功步骤保留）。
func (s *Server) onAgentWizardGenerate(_ context.Context, _ string, v *mq.Value) error {
	var req agentWizardGenerateReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Result = map[string]any{"ok": false, "error": "invalid payload"}
		return nil
	}
	if req.InstanceID == "" {
		v.Result = map[string]any{"ok": false, "error": "instance_id required"}
		return nil
	}
	if s.cfg == nil {
		v.Result = map[string]any{"ok": false, "error": "data facade unavailable"}
		return nil
	}
	scope := s.cfgScope(req.InstanceID)

	// ① agent 提示词落文件（子 agent 唯一形态 = 引用）：非主 agent 且 ref 为空（= 由向导合成的
	// 定制提示词）→ 写成**项目级 capability agent 文件**并改以引用承载；主 agent 仍内联（prompt）。
	// 落文件失败 = 失败（后续步骤全不做，返回 ok:false）。
	agents := make([]facade.ScenarioAgent, 0, len(req.Agents))
	for _, a := range req.Agents {
		ref := strings.TrimSpace(a.Ref)
		if !a.IsMain && ref == "" {
			resp, err := s.cfg.ProjectAgentWrite(facade.ProjectAgentWriteRequest{
				InstanceID: req.InstanceID, Name: a.Name, RoleTag: a.RoleTag,
				Prompt: a.Prompt, Scope: scope,
			})
			if err != nil {
				logf("[chonkpilot-server] agent-wizard-generate: ProjectAgentWrite(%s) failed: %v\n", a.Name, err)
				v.Result = map[string]any{"ok": false, "error": "agent " + a.Name + ": " + err.Error()}
				return nil
			}
			ref = resp.Ref
		}
		agents = append(agents, facade.ScenarioAgent{
			Name: a.Name, RoleTag: a.RoleTag, IsMain: a.IsMain, Prompt: a.Prompt, Ref: ref,
		})
	}
	if _, err := s.cfg.ScenarioSave(facade.ScenarioSaveRequest{
		InstanceID: req.InstanceID,
		Scenario: facade.Scenario{
			ID:          req.ScenarioID,
			Name:        req.SceneName,
			Description: req.Description,
			Level:       "project",
			Agents:      agents,
		},
		Scope: scope,
	}); err != nil {
		logf("[chonkpilot-server] agent-wizard-generate: ScenarioSave failed: %v\n", err)
		v.Result = map[string]any{"ok": false, "error": err.Error()}
		return nil
	}

	// ② 项目记忆：逐类写；单条失败记 errors、继续（不阻断场景）。
	memorySaved := []string{}
	var errs []string
	for _, mem := range req.Memory {
		if _, err := s.cfg.MemorySave(facade.MemorySaveRequest{
			InstanceID: req.InstanceID, Category: mem.Category, Content: mem.Content, Scope: scope,
		}); err != nil {
			errs = append(errs, mem.Category+": "+err.Error())
			continue
		}
		memorySaved = append(memorySaved, mem.Category)
	}

	// ③ 辅助选项：写 prj 配置（memory.enabled / memory.category.* / enable-codegraph / enable-vfts
	// / history.enabled）。失败记 errors、继续（不阻断已成功的场景/记忆）；写成功由门面广播
	// data-prj-config-refresh（data-<domain>-refresh）热生效。
	configSaved := []string{}
	if len(req.Config) > 0 {
		if _, err := s.cfg.ConfigKVSet(facade.ConfigKVSetRequest{
			Domain: facade.DomainPrjConfig, InstanceID: req.InstanceID,
			Entries: req.Config, Scope: scope,
		}); err != nil {
			errs = append(errs, "config: "+err.Error())
		} else {
			for k := range req.Config {
				configSaved = append(configSaved, k)
			}
			sort.Strings(configSaved)
		}
	}

	// ④ 工程规格文件（初始化完成标记）：失败记 errors（已成功的场景/记忆/配置保留，可重试）。
	specPath := ""
	if resp, err := s.cfg.ProjectSpecWrite(facade.ProjectSpecWriteRequest{
		InstanceID: req.InstanceID, Content: req.ProjectSpec, Scope: scope,
	}); err != nil {
		errs = append(errs, "project_spec: "+err.Error())
	} else {
		specPath = resp.Path
	}

	// ⑤ 可选 `git init`：仅当请求 true 且 `<workDir>/.git` 不存在。工作目录取实例绑定
	// （cfgScope 最小路径）；绑定缺失时回落门面 ProjectProbe。失败记 errors（非阻断）。
	gitInitialized := false
	if req.InitGit {
		workDir := scope.WorkDir
		if workDir == "" {
			if probe, err := s.cfg.ProjectProbe(facade.ProjectProbeRequest{
				InstanceID: req.InstanceID, Scope: scope,
			}); err == nil {
				workDir = probe.WorkDir
			}
		}
		switch {
		case workDir == "":
			errs = append(errs, "git_init: unresolved workdir")
		default:
			if _, statErr := os.Stat(filepath.Join(workDir, ".git")); statErr != nil {
				cmd := exec.Command("git", "init")
				cmd.Dir = workDir
				cmd.SysProcAttr = winproc.SysProcAttr() // 宿主为 windowsgui：隐藏 git 控制台窗口
				if out, err := cmd.CombinedOutput(); err != nil {
					errs = append(errs, "git_init: "+err.Error())
					logf("[chonkpilot-server] agent-wizard-generate: git init in %s failed: %v\n%s", workDir, err, out)
				} else {
					gitInitialized = true
				}
			}
		}
	}

	v.Result = map[string]any{
		"ok":              true,
		"scenario_id":     req.ScenarioID,
		"spec_path":       specPath,
		"memory_saved":    memorySaved,
		"errors":          errs, // 无错误 → nil（JSON null）
		"config_saved":    configSaved,
		"git_initialized": gitInitialized,
	}
	return nil
}

// onAgentWizardSkip 受理「稍后」（无副作用）：本会话前端据返回自行记忆，不再自动弹。
func (s *Server) onAgentWizardSkip(_ context.Context, _ string, v *mq.Value) error {
	v.Result = map[string]any{"ok": true}
	return nil
}
