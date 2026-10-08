// 「场景向导」方法面（补充）：agent 提示词合成器（方案 C）。
//
//	agent-wizard-compose {instance_id, mode, description, probe{}, choices{}, team:[{name, roletag}]}
//	                                                              → {ok, agents:[{name, roletag, prompt, source}]}
//
// 语义：按项目特点（探测 + 向导选项 + 顶层模式）把**出厂 archetype**（`<exeDir>/capability/agents/
// <名>.agent.md`）合成为**定制化 agent 提示词**，逐成员内联产出（合成结果**只回文本**，不写文件）。
// 合成文本末尾以 {{path.*}} 占位符给出技能/规范的**四种根路径**（组装时为绝对路径，指示 LLM 用
// 文件工具自读；LLM 经 mcp_find/mcp_load 读不到能力面 capability/skills/*）。失败 → `{ok:false, error}`。
//
// 关键约束：
//   - **纯字符串组装**（确定性模板，不调 LLM、不写文件、无副作用）→ 便于单测；数据访问仅 archetype
//     读取一项，经 data 门面知识库（`KnowledgeRoot`(app) + `KnowledgeRead`），不直开库（23 §7）。
//   - **同步方法面**（结果写 v.Result，无 `.reply` 事件），同 wizard.go 的 probe / generate / skip。
//   - 合成结果**只回文本**；落文件 + 引用由 `agent-wizard-generate`（wizard.go）负责
//     （非主 agent → ProjectAgentWrite 落项目级 capability agent 文件 + 引用；子 agent 唯一形态 = 引用）。
package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// agentWizardComposeTeamMember 是 team 中的一个成员（名称 + 角色标签）。
type agentWizardComposeTeamMember struct {
	Name    string `json:"name"`
	RoleTag string `json:"roletag"`
}

// agentWizardComposeProbe 是合成所需的探测摘要（可直接透传 `agent-wizard-probe` 的 probe）。
type agentWizardComposeProbe struct {
	Languages      []string `json:"languages"`
	Frameworks     []string `json:"frameworks"`
	PackageManager string   `json:"package_manager"`
	BuildTool      string   `json:"build_tool"`
	TestTool       string   `json:"test_tool"`
	LintTool       string   `json:"lint_tool"`
	// Structure 目录结构摘要（缺省回落 TopDirs，兼容 probe 原样透传）。
	Structure string   `json:"structure"`
	TopDirs   []string `json:"top_dirs"`
}

// agentWizardComposeReq 是合成请求载荷（instance_id 由桥/入口注入）。
type agentWizardComposeReq struct {
	InstanceID  string                         `json:"instance_id"`
	Mode        string                         `json:"mode"`        // A|B|C|custom（空/未知 → custom 口径）
	Description string                         `json:"description"` // 项目描述（技术栈兜底来源）
	Probe       agentWizardComposeProbe        `json:"probe"`
	Choices     map[string]any                 `json:"choices"` // 向导选项（技术栈兜底来源）
	Team        []agentWizardComposeTeamMember `json:"team"`
}

// agentWizardComposeAgent 是合成结果中的一个 agent。
type agentWizardComposeAgent struct {
	Name    string `json:"name"`
	RoleTag string `json:"roletag"`
	Prompt  string `json:"prompt"`
	Source  string `json:"source"` // archetype | 自建
}

// onAgentWizardCompose 合成团队成员的角色提示词（纯字符串组装，无副作用）。
func (s *Server) onAgentWizardCompose(_ context.Context, _ string, v *mq.Value) error {
	var req agentWizardComposeReq
	if err := json.Unmarshal(v.Payload, &req); err != nil {
		v.Result = map[string]any{"ok": false, "error": "invalid payload"}
		return nil
	}
	scope := s.cfgScope(req.InstanceID)
	agents := make([]agentWizardComposeAgent, 0, len(req.Team))
	for _, m := range req.Team {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue // 无名成员跳过（前端团队成员恒有 name）
		}
		desc, content, fromArchetype := s.readAgentArchetype(name, req.InstanceID, scope)
		source := "自建"
		if fromArchetype {
			source = "archetype"
		}
		agents = append(agents, agentWizardComposeAgent{
			Name:    name,
			RoleTag: m.RoleTag,
			Prompt:  composeAgentPrompt(req, name, m.RoleTag, desc, content, fromArchetype),
			Source:  source,
		})
	}
	v.Result = map[string]any{"ok": true, "agents": agents}
	return nil
}

// readAgentArchetype 读 app 级 archetype（`<exeDir>/capability/agents/<名>.agent.md`）的 description +
// content。读取一律经 data 门面知识库（路径规则留在数据层）；读不到（含自建角色 / 无门面 / 无该文件）→
// ok=false（调用方回落通用骨架）。
func (s *Server) readAgentArchetype(name, instanceID string, scope facade.Scope) (desc, content string, ok bool) {
	if s.cfg == nil {
		return "", "", false
	}
	root, err := s.cfg.KnowledgeRoot(facade.KnowledgeRootRequest{
		InstanceID: instanceID, Kind: "app", Scope: scope,
	})
	if err != nil || strings.TrimSpace(root.Root) == "" {
		return "", "", false
	}
	resp, err := s.cfg.KnowledgeRead(facade.KnowledgeReadRequest{
		InstanceID: instanceID,
		Path:       filepath.Join(root.Root, "agents", name+".agent.md"),
		Scope:      scope,
	})
	if err != nil {
		return "", "", false
	}
	if strings.TrimSpace(resp.Doc.Description) == "" && strings.TrimSpace(resp.Doc.Content) == "" {
		return "", "", false
	}
	return resp.Doc.Description, resp.Doc.Content, true
}

// composeAgentPrompt 按维度框架组装单个 agent 的提示词（纯函数）。
// isMain 判定：roletag ∈ {主, main} 或 name == 主协调者 → 协作段改为编排者口径。
func composeAgentPrompt(req agentWizardComposeReq, name, roleTag, desc, content string, fromArchetype bool) string {
	var b strings.Builder

	// 【身份】
	if fromArchetype {
		identity := "【身份】你是「" + name + "」"
		if d := strings.TrimSpace(desc); d != "" {
			identity += "，" + d
		}
		identity += "。仅负责本角色职责，超出职责的决策上报主协调者。"
		b.WriteString(identity + "\n")
	} else {
		b.WriteString("【身份】你是本项目自建角色「" + name + "」。仅负责本角色职责，超出职责的决策上报主协调者。\n")
	}

	// 【职责与方法】archetype 正文原文；自建 → 通用骨架。
	if fromArchetype {
		b.WriteString("【职责与方法】" + strings.TrimSpace(content) + "\n")
	} else if rt := strings.TrimSpace(roleTag); rt != "" {
		b.WriteString("【职责与方法】职责见角色标签「" + rt + "」；按该定位与项目约定开展工作。\n")
	} else {
		b.WriteString("【职责与方法】按该角色定位与项目约定开展工作。\n")
	}

	// 【项目上下文】空字段省略对应分句；全空 → 整段省略（不写「未知」）。
	var ctx []string
	if tech := composeTechStack(req); tech != "" {
		ctx = append(ctx, "技术栈："+tech)
	}
	if v := strings.TrimSpace(req.Probe.BuildTool); v != "" {
		ctx = append(ctx, "构建："+v)
	}
	if v := strings.TrimSpace(req.Probe.TestTool); v != "" {
		ctx = append(ctx, "测试："+v)
	}
	if v := strings.TrimSpace(req.Probe.LintTool); v != "" {
		ctx = append(ctx, "风格："+v)
	}
	if v := composeStructure(req.Probe); v != "" {
		ctx = append(ctx, "结构："+v)
	}
	if len(ctx) > 0 {
		b.WriteString("【项目上下文】" + strings.Join(ctx, "；") + "\n")
	}

	// 【协作】
	if isMainComposeMember(name, roleTag) {
		b.WriteString("【协作】你是编排者，统一接收任务并委派给团队成员。\n")
	} else {
		b.WriteString("【协作】向主协调者汇报；产物写入 .chonkpilot/tmp/；按接口契约与其它成员交互。\n")
	}

	// 【约束】按顶层模式
	b.WriteString("【约束】" + composeConstraint(req.Mode) + "\n")

	// 【输出契约】+ 维度标准指引
	b.WriteString("【输出契约】结论用 Markdown；定位问题给 file:line 证据；简洁、不臆造。\n")
	// 技能 / 规范改**给绝对路径**（LLM 经 mcp_find/mcp_load 读不到能力面 capability/skills/*）：
	// 四根占位符 {{path.*}} 在系统提示词组装时按四级 capability 根渲染（见 replacePaths）。
	b.WriteString("技能与规范文件按绝对路径自读（用文件工具），位于能力目录：")
	b.WriteString("{{path.exeDir}}/capability/skills/、")
	b.WriteString("{{path.userDir}}/capability/skills/、")
	b.WriteString("{{path.dataDir}}/capability/skills/、")
	b.WriteString("{{path.workDir}}/.chonkpilot/capability/skills/")
	b.WriteString("（如提示词维度标准见 core/agent-prompt-rule.skill.md）。")
	return b.String()
}

// isMainComposeMember 判定成员是否主 agent（`roletag=="主"` 或 `name=="主协调者"`；兼容前端 `main`）。
func isMainComposeMember(name, roleTag string) bool {
	rt := strings.TrimSpace(roleTag)
	return rt == "主" || strings.EqualFold(rt, "main") || strings.TrimSpace(name) == "主协调者"
}

// composeTechStack 拼技术栈：优先探测（语言 / 框架 / 包管理）→ 向导选项 → 项目描述兜底；去重保序、
// 以「、」连接；全空 → 空串（调用方省略该分句）。
func composeTechStack(req agentWizardComposeReq) string {
	parts := make([]string, 0, 6)
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		parts = append(parts, s)
	}
	for _, l := range req.Probe.Languages {
		add(l)
	}
	for _, f := range req.Probe.Frameworks {
		add(f)
	}
	add(req.Probe.PackageManager)
	if len(parts) == 0 && len(req.Choices) > 0 {
		// 按键排序保证确定性；排除 mode（非技术栈）。
		keys := make([]string, 0, len(req.Choices))
		for k := range req.Choices {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "mode" {
				continue
			}
			add(choiceText(req.Choices[k]))
		}
	}
	if len(parts) == 0 {
		add(req.Description)
	}
	return strings.Join(parts, "、")
}

// choiceText 取向导选项值的可读文本（字符串 / 字符串数组）；其余类型忽略。
func choiceText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		ss := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				ss = append(ss, s)
			}
		}
		return strings.Join(ss, "/")
	case []string:
		return strings.Join(t, "/")
	default:
		return ""
	}
}

// composeStructure 目录结构摘要：优先 Structure，缺省回落 TopDirs（上限 8 项，避免冗长）。
func composeStructure(p agentWizardComposeProbe) string {
	if s := strings.TrimSpace(p.Structure); s != "" {
		return s
	}
	dirs := p.TopDirs
	if len(dirs) > 8 {
		dirs = dirs[:8]
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return strings.Join(out, "/")
}

// composeConstraint 顶层模式 → 约束分句（A=从零构建 / B=迭代 / C=重构 / custom=按用户要求）。
func composeConstraint(mode string) string {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "A":
		return "从零构建、保持脚手架一致。"
	case "B":
		return "在既有架构与风格上增量、改动须跑回归。"
	case "C":
		return "严格行为保持、公共接口与既有测试不被破坏。"
	default:
		return "按用户要求。"
	}
}
