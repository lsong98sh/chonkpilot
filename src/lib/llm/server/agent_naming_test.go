// 命名与唯一性（2026-09-26）：agent **允许跨场景重名** → in-memory 注册表键 = `<场景id>/<agent名>`
// （场景前缀消歧，见 domainmcp.go agentRegKey），注入面（团队成员段）/ 委派判定（agentDelegable）/
// 子轮 system 读取（registeredAgentDef / resolveAgentDef）**统一带前缀**。
//
// 本文件覆盖注册表 / 解析 / 可委派判定（委派执行由 gateway `dsl_run` 承担，不在本包单测内）。
package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// appMultiScenarioRoot 造 app 级 **capability 根**（场景根 = `<capRoot>/scenarios`），含**多个**场景：
// scenarios = 场景 id → (agent 名 → 提示词)。用于「跨场景同名 agent」用例。
// **子 agent 唯一形态 = 引用**：非 main 成员落 `<capRoot>/agents/<场景id>-<名>.agent.md`
// （跨场景同名不互相覆盖）并写入 `scenario.json.agents`；名为 `main` 的成员写为 `main.agent.md`（内联）。
func appMultiScenarioRoot(t *testing.T, scenarios map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	capRoot := filepath.Join(root, "capability")
	agentsDir := filepath.Join(capRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	for id, agents := range scenarios {
		dir := filepath.Join(capRoot, "scenarios", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir scenario %s: %v", id, err)
		}
		refs := []string{}
		for name, prompt := range agents {
			doc := buildV2AgentDoc(name, name+" 描述", prompt, "", nil, nil)
			if name == "main" {
				if err := os.WriteFile(filepath.Join(dir, "main.agent.md"), []byte(doc), 0o644); err != nil {
					t.Fatalf("write main.agent.md: %v", err)
				}
				continue
			}
			fileName := id + "-" + name + ".agent.md"
			if err := os.WriteFile(filepath.Join(agentsDir, fileName), []byte(doc), 0o644); err != nil {
				t.Fatalf("write %s: %v", fileName, err)
			}
			refs = append(refs, "${exeDir}/capability/agents/"+fileName)
		}
		meta, _ := json.Marshal(map[string]any{"name": id, "agents": refs})
		if err := os.WriteFile(filepath.Join(dir, "scenario.json"), append(meta, '\n'), 0o644); err != nil {
			t.Fatalf("write scenario.json: %v", err)
		}
	}
	return capRoot
}

// sseRecorder 记录 mock LLM 收到的请求体（并带 mutex 供并发子轮次读取）。
type sseRecorder struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (r *sseRecorder) add(b map[string]any) {
	r.mu.Lock()
	r.bodies = append(r.bodies, b)
	r.mu.Unlock()
}

// bodyAt 取第 i 个请求体（持锁；i 越界 → nil）。
func (r *sseRecorder) bodyAt(i int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.bodies) {
		return nil
	}
	return r.bodies[i]
}

// systemTexts 汇总全部请求体的 system 文本。
func (r *sseRecorder) systemTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, b := range r.bodies {
		out = append(out, agSystemTexts(b)...)
	}
	return out
}

// TestAgentNamingRegistryUnique 注册表键 = `<场景id>/<agent名>`：跨场景同名 agent 各自独立
// （不覆盖）；裸名歧义 → 不解析（须用前缀）；带前缀引用**可委派**。
func TestAgentNamingRegistryUnique(t *testing.T) {
	_, srv := newProviderServer(t)
	root := appMultiScenarioRoot(t, map[string]map[string]string{
		"scen-a": {"main": "A-主提示词", "dup": "场景A-dup提示词"},
		"scen-b": {"dup": "场景B-dup提示词"},
	})
	s := newTestServerMCPAppRoot(t, srv, root)
	registerProviderInstance(t, s)

	// ① 两个同名 agent 各自独立登记（不互相覆盖）
	a, okA := s.registeredAgentDef("scen-a/dup")
	b, okB := s.registeredAgentDef("scen-b/dup")
	if !okA || !strings.Contains(a.Content, "场景A-dup提示词") {
		t.Fatalf("scen-a/dup 未登记或内容不符: ok=%v %+v", okA, a)
	}
	if !okB || !strings.Contains(b.Content, "场景B-dup提示词") {
		t.Fatalf("scen-b/dup 未登记或内容不符: ok=%v %+v", okB, b)
	}
	// ② 裸名歧义（两处同名）→ 不解析（须带场景前缀）
	if _, ok := s.registeredAgentDef("dup"); ok {
		t.Fatal("跨场景重名时裸名应不可解析（须带场景前缀）")
	}
	if s.lookupAgent("dup") {
		t.Fatal("跨场景重名时裸名不应视为已注册")
	}
	// ③ 带前缀引用各自可委派
	if !s.agentDelegable("ins-test", "", "scen-a/dup") || !s.agentDelegable("ins-test", "", "scen-b/dup") {
		t.Fatal("带场景前缀的同名 agent 应各自可委派")
	}
	if s.agentDelegable("ins-test", "", "scen-a/ghost") {
		t.Fatal("不存在的前缀引用不应可委派")
	}
}
