// payload 定死结构的解析白盒：宽容标量与两形载荷（顶层扁平 / `{data:{…}}`）。
package persist

import (
	"encoding/json"
	"testing"
)

// TestScenarioAgentSkillsResources：skills / resources 兼容数组与会话面历史出现过的
// JSON 数组文本（capfs 读盘形态），空元素剔除、空名单 → nil。
func TestScenarioAgentSkillsResources(t *testing.T) {
	payload := `{
		"req_id": "r1", "instance_id": "ins",
		"id": "s1", "level": "user",
		"agents": [
			{"name":"主","isMain":true,"prompt":"p","tools":"a,b",
			 "skills":["a","b"],"resources":["r1"]},
			{"name":"sub","ref":"x","skills":"[\"a\",\"b\"]","resources":["r1"]}
		]
	}`
	var p ScenarioPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sc := scenarioOf(p.merged())
	if len(sc.Agents) != 2 {
		t.Fatalf("agents 数 = %d，want 2", len(sc.Agents))
	}
	if got := sc.Agents[0].Skills; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("数组形态 skills = %v，want [a b]", got)
	}
	if got := sc.Agents[0].Resources; len(got) != 1 || got[0] != "r1" {
		t.Fatalf("数组形态 resources = %v，want [r1]", got)
	}
	if got := sc.Agents[0].Tools; got != "a,b" {
		t.Fatalf("tools = %q，want %q", got, "a,b")
	}
	if got := sc.Agents[1].Skills; len(got) != 2 || got[0] != "a" {
		t.Fatalf("JSON 文本 skills = %v，want [a b]", got)
	}
	if sc.ID != "s1" || sc.Level != "user" {
		t.Fatalf("scenario = %+v，want id=s1 level=user", sc)
	}
}

// TestScenarioPayloadDataForm：`{data:{…}}` 形态优先、顶层补缺；agent 名单空元素剔除。
func TestScenarioPayloadDataForm(t *testing.T) {
	payload := `{"req_id":"r2","instance_id":"ins","id":"top","data":{
		"id":"inner","name":"场景","agents":[{"name":"主","isMain":"true","skills":[" a ",""]}]}}`
	var p ScenarioPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sc := scenarioOf(p.merged())
	if sc.ID != "inner" {
		t.Fatalf("id = %q，want inner（data 优先）", sc.ID)
	}
	if len(sc.Agents) != 1 || !sc.Agents[0].IsMain {
		t.Fatalf("agent = %+v，want 单条且 IsMain（兼容 \"true\" 字符串）", sc.Agents)
	}
	if got := sc.Agents[0].Skills; len(got) != 1 || got[0] != "a" {
		t.Fatalf("skills = %v，want [a]（空白元素剔除）", got)
	}
}

// TestMcpPayloadTriState：isolate / sandbox 三态按「有键/无键」保真（nil ≠ false）。
func TestMcpPayloadTriState(t *testing.T) {
	var p McpPayload
	if err := json.Unmarshal([]byte(`{"instance_id":"ins","name":"srv","level":"user","sandbox":false}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m := p.merged()
	if m.target() != "srv" || m.Level != "user" {
		t.Fatalf("mcp = %+v，want name=srv level=user", m)
	}
	if m.Isolate != nil {
		t.Fatalf("isolate 无键应为 nil，实得 %v", *m.Isolate)
	}
	if m.Sandbox == nil || *m.Sandbox {
		t.Fatalf("sandbox 显式 false 应保留，实得 %v", m.Sandbox)
	}
}
