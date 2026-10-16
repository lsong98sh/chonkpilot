// scenario 域 wire 往返白盒（req10，2026-10-15）：agent 的 skills / resources 名单在
// 「消息面载荷 ↔ 门面 DTO」间往返——前端载荷（数组）与 capfs 读盘结果（JSON 数组文本）两种形态
// 均可解析；写回（ScenarioToWire）输出数组，供 capfs.agentToDoc 的 refListOf 消费。
package wire

import (
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// TestScenarioAgentSkillsResourcesFromWire 解析两种形态：前端数组载荷 + capfs JSON 数组文本。
func TestScenarioAgentSkillsResourcesFromWire(t *testing.T) {
	// 前端载荷形态（数组）
	sc := ScenarioFromWire(map[string]any{
		"id": "s1", "level": "user",
		"agents": []any{
			map[string]any{"name": "主", "isMain": true, "prompt": "p",
				"skills": []any{"a", "b"}, "resources": []any{"r1"}},
			map[string]any{"name": "sub", "ref": "x", "skills": []any{"c"}},
		},
	})
	if len(sc.Agents) != 2 {
		t.Fatalf("agents 数 = %d，want 2", len(sc.Agents))
	}
	if got := sc.Agents[0].Skills; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("主 agent skills = %v，want [a b]", got)
	}
	if got := sc.Agents[0].Resources; len(got) != 1 || got[0] != "r1" {
		t.Fatalf("主 agent resources = %v，want [r1]", got)
	}
	if got := sc.Agents[1].Skills; len(got) != 1 || got[0] != "c" {
		t.Fatalf("子 agent skills = %v，want [c]", got)
	}
	if sc.Agents[1].Resources != nil {
		t.Fatalf("子 agent 无 resources 应为 nil，实得 %v", sc.Agents[1].Resources)
	}
	// capfs 读盘形态（JSON 数组文本）
	read := ScenarioFromWire(map[string]any{
		"agents": []any{map[string]any{"name": "主", "skills": `["a","b"]`, "resources": `["r1"]`}},
	})
	if got := read.Agents[0].Skills; len(got) != 2 || got[0] != "a" {
		t.Fatalf("JSON 文本 skills 解析异常：%v", got)
	}
	if got := read.Agents[0].Resources; len(got) != 1 || got[0] != "r1" {
		t.Fatalf("JSON 文本 resources 解析异常：%v", got)
	}
}

// TestScenarioAgentSkillsResourcesToWire 写回形态：非空才带且为数组（供 refListOf 消费与前端应答）。
func TestScenarioAgentSkillsResourcesToWire(t *testing.T) {
	row := ScenarioToWire(facade.Scenario{
		ID: "s1",
		Agents: []facade.ScenarioAgent{
			{Name: "主", IsMain: true, Prompt: "p", Skills: []string{"a", "b"}, Resources: []string{"r1"}},
			{Name: "sub", Ref: "x"},
		},
	})
	agents, _ := row["agents"].([]any)
	if len(agents) != 2 {
		t.Fatalf("agents 数 = %d，want 2", len(agents))
	}
	a0, _ := agents[0].(map[string]any)
	skills, ok := a0["skills"].([]any)
	if !ok || len(skills) != 2 {
		t.Fatalf("主 agent skills 应为数组 [a b]，实得 %#v", a0["skills"])
	}
	res, _ := a0["resources"].([]any)
	if len(res) != 1 || res[0] != "r1" {
		t.Fatalf("主 agent resources 应为数组 [r1]，实得 %#v", a0["resources"])
	}
	// 空名单不写键（缺省与既有载荷逐字等价）
	a1, _ := agents[1].(map[string]any)
	if _, has := a1["skills"]; has {
		t.Fatalf("空 skills 不应写键：%#v", a1)
	}
	if _, has := a1["resources"]; has {
		t.Fatalf("空 resources 不应写键：%#v", a1)
	}
}
