// 「场景向导」生成（agent-wizard-generate）白盒：非主 agent 的合成提示词落**项目级 capability
// agent 文件**、场景以**引用**承载（子 agent 唯一形态 = 引用）；主 agent 仍内联（prompt）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/persist"
	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// wizardGenerateCall 直接驱动 onAgentWizardGenerate 并取应答（纯 handler 白盒）。
func wizardGenerateCall(t *testing.T, s *Server, payload map[string]any) map[string]any {
	t.Helper()
	v := &mq.Value{Payload: jb(payload)}
	if err := s.onAgentWizardGenerate(context.Background(), "agent-wizard-generate", v); err != nil {
		t.Fatalf("onAgentWizardGenerate: %v", err)
	}
	res, ok := v.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result 非 map：%T", v.Result)
	}
	return res
}

// TestAgentWizardGenerateWritesAgentFilesAndRefs：非主 agent（ref 空、带合成 prompt）→ 落
// `<workDir>/.chonkpilot/capability/agents/<名>.agent.md`，场景以**引用**承载；主 agent 内联；
// load 回读 ref + 被引文件正文。
func TestAgentWizardGenerateWritesAgentFilesAndRefs(t *testing.T) {
	s := newTestServer(t, mockLLMServer())
	registerTestInstance(t, s)

	res := wizardGenerateCall(t, s, map[string]any{
		"instance_id": "ins-test",
		"scenario_id": "wiz-1",
		"scene_name":  "向导场景",
		"description": "团队",
		"agents": []any{
			map[string]any{"name": "主协调者", "roletag": "主", "is_main": true, "prompt": "主提示"},
			map[string]any{"name": "前端开发", "roletag": "前端", "is_main": false, "prompt": "合成的前端提示词"},
		},
	})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("generate 失败：%+v", res)
	}

	// 落文件：<workDir>/.chonkpilot/capability/agents/前端开发.agent.md
	abs := filepath.Join(persist.CapProjectRoot(testWorkDir), "agents", "前端开发.agent.md")
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("agent 文件未落盘（%s）：%v", abs, err)
	}
	if !strings.Contains(string(raw), "合成的前端提示词") || !strings.Contains(string(raw), "roletag=前端") {
		t.Fatalf("agent 文件内容不符：\n%s", raw)
	}

	// 场景以引用承载
	sc, err := s.cfg.ScenarioGet(facade.ScenarioGetRequest{
		InstanceID: "ins-test", ScenarioID: "wiz-1", Level: "project",
	})
	if err != nil {
		t.Fatalf("ScenarioGet: %v", err)
	}
	if len(sc.Scenario.Agents) != 2 || !sc.Scenario.Agents[0].IsMain {
		t.Fatalf("agents 形状异常：%+v", sc.Scenario.Agents)
	}
	wantRef := "${workDir}/.chonkpilot/capability/agents/前端开发.agent.md"
	sub := sc.Scenario.Agents[1]
	if sub.Ref != wantRef || sub.Prompt != "合成的前端提示词" {
		t.Fatalf("子 agent 应以引用承载：%+v（want ref=%s）", sub, wantRef)
	}
}
