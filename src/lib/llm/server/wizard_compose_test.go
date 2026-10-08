// 场景向导 · agent 提示词合成器（agent-wizard-compose）单测：验证「按项目特点合成定制化
// agent 提示词」的确定性模板组装 —— archetype 命中 / 自建回落 / 空探测项省略 / 空入参不 panic。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// composeCapRoot 造 app 级 capability 根，含 `agents/<名>.agent.md`（内容 = content 段正文）。
// 返回 capability 根（传 Options.MCPServerRoot → 数据层 AppDir 同源）。
func composeCapRoot(t *testing.T, agents map[string]string) string {
	t.Helper()
	capRoot := filepath.Join(t.TempDir(), "capability")
	dir := filepath.Join(capRoot, "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	for name, content := range agents {
		doc := "# " + name + "\n\n[meta]\nroletag=前端\n\n[description]\n" + name + " 描述\n\n[content]\n" + content + "\n"
		if err := os.WriteFile(filepath.Join(dir, name+".agent.md"), []byte(doc), 0o644); err != nil {
			t.Fatalf("write %s.agent.md: %v", name, err)
		}
	}
	return capRoot
}

// composeCall 直接驱动 onAgentWizardCompose 并取应答（不入总线，纯 handler 白盒）。
func composeCall(t *testing.T, s *Server, payload map[string]any) map[string]any {
	t.Helper()
	v := &mq.Value{Payload: jb(payload)}
	if err := s.onAgentWizardCompose(context.Background(), "agent-wizard-compose", v); err != nil {
		t.Fatalf("onAgentWizardCompose: %v", err)
	}
	res, ok := v.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result 非 map：%T", v.Result)
	}
	return res
}

// composeAgentsOf 取应答 agents 并按 name 建索引。
func composeAgentsOf(t *testing.T, res map[string]any) ([]agentWizardComposeAgent, map[string]agentWizardComposeAgent) {
	t.Helper()
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("应答 ok!=true：%+v", res)
	}
	agents, ok := res["agents"].([]agentWizardComposeAgent)
	if !ok {
		t.Fatalf("agents 类型不符：%T", res["agents"])
	}
	byName := make(map[string]agentWizardComposeAgent, len(agents))
	for _, a := range agents {
		byName[a.Name] = a
	}
	return agents, byName
}

// TestWizardComposeKnownRole ① 已知角色（archetype 命中）→ 含 archetype 正文关键片段 + 项目上下文
// 含给定 build/test；主 agent 的协作段为编排者口径。
func TestWizardComposeKnownRole(t *testing.T) {
	capRoot := composeCapRoot(t, map[string]string{
		"前端开发": "实现可直接运行的前端代码：遵循项目现有组件风格。",
	})
	s := newTestServerMCPAppRoot(t, mockLLMServer(), capRoot)
	registerProviderInstance(t, s)

	res := composeCall(t, s, map[string]any{
		"instance_id": "ins-test",
		"mode":        "A",
		"description": "从零构建 Web 应用",
		"probe": map[string]any{
			"languages":  []any{"TypeScript"},
			"build_tool": "vite",
			"test_tool":  "vitest",
		},
		"team": []any{
			map[string]any{"name": "前端开发", "roletag": "前端"},
			map[string]any{"name": "主协调者", "roletag": "主"},
		},
	})
	_, byName := composeAgentsOf(t, res)

	fe := byName["前端开发"]
	if fe.Source != "archetype" {
		t.Fatalf("已知角色 source=%q want archetype（prompt=%q）", fe.Source, fe.Prompt)
	}
	if !strings.Contains(fe.Prompt, "遵循项目现有组件风格") {
		t.Fatalf("prompt 未含 archetype 正文关键片段：\n%s", fe.Prompt)
	}
	if !strings.Contains(fe.Prompt, "构建：vite") || !strings.Contains(fe.Prompt, "测试：vitest") {
		t.Fatalf("【项目上下文】未含给定 build/test：\n%s", fe.Prompt)
	}
	if !strings.Contains(fe.Prompt, "向主协调者汇报") {
		t.Fatalf("普通成员协作段应为汇报口径：\n%s", fe.Prompt)
	}

	main := byName["主协调者"]
	if !strings.Contains(main.Prompt, "你是编排者，统一接收任务并委派给团队成员。") {
		t.Fatalf("主 agent 协作段应为编排者口径：\n%s", main.Prompt)
	}
}

// TestWizardComposeUnknownRole ② 未知角色（无 archetype）→ source=自建 且 prompt 非空。
func TestWizardComposeUnknownRole(t *testing.T) {
	capRoot := composeCapRoot(t, map[string]string{"前端开发": "x"})
	s := newTestServerMCPAppRoot(t, mockLLMServer(), capRoot)
	registerProviderInstance(t, s)

	res := composeCall(t, s, map[string]any{
		"instance_id": "ins-test",
		"mode":        "B",
		"team":        []any{map[string]any{"name": "前端代码审查", "roletag": "审查"}},
	})
	_, byName := composeAgentsOf(t, res)

	a, ok := byName["前端代码审查"]
	if !ok {
		t.Fatal("未合成未知角色")
	}
	if a.Source != "自建" {
		t.Fatalf("未知角色 source=%q want 自建", a.Source)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		t.Fatal("未知角色 prompt 不得为空")
	}
	if !strings.Contains(a.Prompt, "本项目自建角色「前端代码审查」") {
		t.Fatalf("未知角色应回落通用骨架：\n%s", a.Prompt)
	}
}

// TestWizardComposeMissingProbe ③ 探测字段缺失 → 不出「未知」字样；全空则整段【项目上下文】省略。
func TestWizardComposeMissingProbe(t *testing.T) {
	capRoot := composeCapRoot(t, map[string]string{"后端开发": "实现后端接口。"})
	s := newTestServerMCPAppRoot(t, mockLLMServer(), capRoot)
	registerProviderInstance(t, s)

	res := composeCall(t, s, map[string]any{
		"instance_id": "ins-test",
		"mode":        "C",
		"probe":       map[string]any{},
		"choices":     map[string]any{},
		"team":        []any{map[string]any{"name": "后端开发", "roletag": "后端"}},
	})
	_, byName := composeAgentsOf(t, res)

	p := byName["后端开发"].Prompt
	if strings.Contains(p, "未知") {
		t.Fatalf("缺失探测项不得写「未知」：\n%s", p)
	}
	if strings.Contains(p, "【项目上下文】") {
		t.Fatalf("探测与选项全空时应整段省略【项目上下文】：\n%s", p)
	}
	if !strings.Contains(p, "严格行为保持") {
		t.Fatalf("mode=C 约束口径不符：\n%s", p)
	}
}

// TestWizardComposePathPlaceholders ⑤ 合成文本给出技能/规范的**四种根路径占位符**
// （{{path.exeDir}}/{{path.userDir}}/{{path.dataDir}}/{{path.workDir}} + capability/skills 相对路径），
// 交由系统提示词组装期（replacePaths）渲染；不再指示经 mcp_load 读技能。
func TestWizardComposePathPlaceholders(t *testing.T) {
	s := newTestServerMCPAppRoot(t, mockLLMServer(), composeCapRoot(t, nil))
	registerProviderInstance(t, s)

	res := composeCall(t, s, map[string]any{
		"instance_id": "ins-test",
		"team":        []any{map[string]any{"name": "自建角色"}},
	})
	agents, _ := composeAgentsOf(t, res)
	p := agents[0].Prompt
	for _, ph := range []string{"{{path.exeDir}}", "{{path.userDir}}", "{{path.dataDir}}", "{{path.workDir}}"} {
		if !strings.Contains(p, ph) {
			t.Fatalf("合成文本缺路径占位符 %s：\n%s", ph, p)
		}
	}
	if !strings.Contains(p, "/capability/skills/") {
		t.Fatalf("合成文本应给出 capability/skills 相对路径：\n%s", p)
	}
	if strings.Contains(p, "mcp_load") {
		t.Fatalf("不应再指示经 mcp_load 读技能：\n%s", p)
	}
}

// TestWizardComposeEmptyInput ④ 描述 / choices 为空不 panic，且返回 ok。
func TestWizardComposeEmptyInput(t *testing.T) {
	s := newTestServerMCPAppRoot(t, mockLLMServer(), composeCapRoot(t, nil))
	registerProviderInstance(t, s)

	res := composeCall(t, s, map[string]any{
		"instance_id": "ins-test",
		"team":        []any{map[string]any{"name": "自建角色"}},
	})
	agents, _ := composeAgentsOf(t, res)
	if len(agents) != 1 || strings.TrimSpace(agents[0].Prompt) == "" {
		t.Fatalf("空描述 / 空 choices 仍应产出非空 prompt：%+v", agents)
	}
	if agents[0].Source != "自建" {
		t.Fatalf("无 archetype 时应为自建：%+v", agents[0])
	}
}
