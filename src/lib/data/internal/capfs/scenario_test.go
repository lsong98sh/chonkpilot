// capfs 场景 agent 重名校验白盒（25-MCP与场景分层模型 §6.1 · 42 §2 (175)，2026-09-26 用户裁决）：
// 判定键 = agent **落盘文件名**（大小写不敏感）→ 覆盖 直呼同名 / 大小写差异 / 主 agent 与子 agent
// 撞名 `main` / 空名 解析为同一文件名 四类边界；命中即返回含「场景 id + 重复 agent 名」的错误。
package capfs

import (
	"strings"
	"testing"
)

// agentNode 造一个 agent 节点（name + 可选 isMain）。
func agentNode(name string, isMain bool) map[string]any {
	m := map[string]any{"name": name}
	if isMain {
		m["isMain"] = true
	}
	return m
}

// scenarioWith 造一个只含 agents 的场景载荷。
func scenarioWith(agents ...map[string]any) map[string]any {
	list := make([]any, 0, len(agents))
	for _, a := range agents {
		list = append(list, a)
	}
	return map[string]any{"agents": list}
}

// TestValidateScenarioAgentsRejectsDuplicate 四类边界均拒绝，且错误含场景 id + 重复名。
func TestValidateScenarioAgentsRejectsDuplicate(t *testing.T) {
	const scnID = "dup-scn"
	cases := []struct {
		desc     string
		sc       map[string]any
		mustHave []string // 错误文案必须包含（大小写不敏感）
	}{
		{
			"直呼同名（两子 agent）",
			scenarioWith(agentNode("主", true), agentNode("coder", false), agentNode("coder", false)),
			[]string{scnID, "coder"},
		},
		{
			"大小写差异",
			scenarioWith(agentNode("主", true), agentNode("Coder", false), agentNode("coder", false)),
			[]string{scnID, "coder.agent.md"},
		},
		{
			"主 agent 与子 agent 撞名 main（主名不同、子名 main）",
			scenarioWith(agentNode("Loop Engineer", true), agentNode("main", false)),
			[]string{scnID, "main.agent.md"},
		},
		{
			"主 agent 自身名 main + 子 agent 名 main",
			scenarioWith(agentNode("main", true), agentNode("main", false)),
			[]string{scnID, "main"},
		},
		{
			"空名（两子 agent 均空 → 同落 agent.agent.md）",
			scenarioWith(agentNode("主", true), agentNode("", false), agentNode("", false)),
			[]string{scnID, "agent.agent.md"},
		},
	}
	for _, tc := range cases {
		err := ValidateScenarioAgents(scnID, tc.sc)
		if err == nil {
			t.Fatalf("%s：应拒绝重名", tc.desc)
		}
		msg := strings.ToLower(err.Error())
		for _, want := range tc.mustHave {
			if !strings.Contains(msg, strings.ToLower(want)) {
				t.Fatalf("%s：错误文案缺 %q：%v", tc.desc, want, err)
			}
		}
	}
}

// TestValidateScenarioAgentsAllowsUnique 唯一命名放行（含主 agent + 不同子 agent、空名单个）。
func TestValidateScenarioAgentsAllowsUnique(t *testing.T) {
	cases := []struct {
		desc string
		sc   map[string]any
	}{
		{"主 + 两不同子", scenarioWith(agentNode("主", true), agentNode("coder", false), agentNode("writer", false))},
		{"仅主 agent", scenarioWith(agentNode("主", true))},
		{"无 agents", scenarioWith()},
		{"空白名仅一个（不与他人冲突）", scenarioWith(agentNode("主", true), agentNode("", false))},
		{"主 agent 名 main 但无同名子", scenarioWith(agentNode("main", true), agentNode("coder", false))},
	}
	for _, tc := range cases {
		if err := ValidateScenarioAgents("ok-scn", tc.sc); err != nil {
			t.Fatalf("%s：应放行，却报错 %v", tc.desc, err)
		}
	}
}

// TestWriteScenarioDirRejectsDuplicate 校验在**写盘前**生效：重名 → 拒绝且**不建目录/不落盘**。
func TestWriteScenarioDirRejectsDuplicate(t *testing.T) {
	root := t.TempDir()
	const id = "dup-write"
	err := WriteScenarioDir(KindUser, root, id, scenarioWith(
		agentNode("主", true), agentNode("coder", false), agentNode("coder", false)))
	if err == nil {
		t.Fatal("WriteScenarioDir 应拒绝重名场景")
	}
	if ScenarioDirExists(root, id) {
		t.Fatal("被拒的重名场景不应建目录/落盘")
	}
}
