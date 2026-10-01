// 命名与唯一性（2026-09-26）：agent **允许跨场景重名** → in-memory 注册表键 = `<场景id>/<agent名>`
// （场景前缀消歧，见 domainmcp.go agentRegKey），注入面（团队成员段）/ 委派判定（agentDelegable）/
// 子轮 system 读取（registeredAgentDef / resolveAgentDef）**统一带前缀**。
//
// 驱动（MQ）：session-start（带 scenario_id）+ session-send；mock LLM 主轮返回 llm_run 委派 →
// 断言主轮 system 团队成员段带前缀 + 子轮 system 用**被委派那一个**同名 agent 的定义。
package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// appMultiScenarioRoot 造 app 级 **capability 根**（场景根 = `<capRoot>/scenarios`），含**多个**场景：
// scenarios = 场景 id → (agent 名 → 提示词)。用于「跨场景同名 agent」用例。
func appMultiScenarioRoot(t *testing.T, scenarios map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	capRoot := filepath.Join(root, "capability")
	if err := os.MkdirAll(capRoot, 0o755); err != nil {
		t.Fatalf("mkdir capability: %v", err)
	}
	for id, agents := range scenarios {
		dir := filepath.Join(capRoot, "scenarios", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir scenario %s: %v", id, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scenario.json"),
			[]byte("{\"name\":\""+id+"\"}\n"), 0o644); err != nil {
			t.Fatalf("write scenario.json: %v", err)
		}
		for name, prompt := range agents {
			doc := "# " + name + "\n\n[description]\n" + name + " 描述\n\n[content]\n" + prompt + "\n"
			if err := os.WriteFile(filepath.Join(dir, name+".agent.md"), []byte(doc), 0o644); err != nil {
				t.Fatalf("write %s.agent.md: %v", name, err)
			}
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

// delegateRecorder LLM：主轮（提示词含 trigger）→ 返回 llm_run 委派 agent；其余（含子轮）回显提示词。
func delegateRecorder(t *testing.T, trigger, agent string) (*sseRecorder, *httptest.Server) {
	t.Helper()
	rec := &sseRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		rec.add(m)
		var typed struct {
			Messages []ChatMsg `json:"messages"`
		}
		_ = json.Unmarshal(raw, &typed)
		text := lastText(typed.Messages)
		if strings.Contains(text, trigger) {
			args := jb(map[string]any{"script": `LLM "` + agent + `" "x"` + "\n"})
			llmSSE(w, []string{
				sseChunk(map[string]any{"content": ""}, ""),
				sseChunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "tc-nm", "type": "function",
					"function": map[string]any{"name": "self_llm_run", "arguments": string(args)},
				}}}, "tool_calls"),
			})
			return
		}
		var chunks []string
		for _, ch := range text {
			chunks = append(chunks, sseChunk(map[string]any{"content": string(ch)}, ""))
		}
		chunks = append(chunks, sseChunk(map[string]any{}, "stop"))
		llmSSE(w, chunks)
	}))
	t.Cleanup(srv.Close)
	return rec, srv
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

// TestAgentScenarioPrefixMQDriven 端到端（MQ 驱动）：注入面（团队成员段）带场景前缀；
// 委派跨场景同名 agent（scen-b/dup）→ 子轮 system 取**那一份**定义（非当前场景的同名）。
func TestAgentScenarioPrefixMQDriven(t *testing.T) {
	rec, srv := delegateRecorder(t, "delegate please", "scen-b/dup")
	root := appMultiScenarioRoot(t, map[string]map[string]string{
		"scen-a": {"main": "A-主提示词", "dup": "场景A-dup提示词"},
		"scen-b": {"dup": "场景B-dup提示词"},
	})
	s := newTestServerMCPAppRoot(t, srv, root)
	registerProviderInstance(t, s)

	agStartTurn(t, s, "s-nm", "t-nm", "", "scen-a")
	sendAndWait(t, s, "s-nm", "t-nm", "delegate please")

	// 注入面：主轮系统提示词的团队成员段带场景前缀 `<场景id>/<agent名>`
	mainBody := rec.bodyAt(0)
	if mainBody == nil {
		t.Fatal("未见主轮 LLM 请求体")
	}
	mainSys := strings.Join(agSystemTexts(mainBody), "\n")
	if !strings.Contains(mainSys, "scen-a/dup") {
		t.Fatalf("团队成员段应带场景前缀 scen-a/dup：\n%s", mainSys)
	}

	// 子轮：被委派 scen-b/dup → agent 层用 scen-b 那一条定义（跨场景前缀消歧）
	childSys := ""
	for _, st := range rec.systemTexts() {
		if strings.Contains(st, "[子会话 agent: scen-b/dup]") {
			childSys = st
			break
		}
	}
	if childSys == "" {
		t.Fatalf("未见子轮 system（[子会话 agent: scen-b/dup]）：%v", rec.systemTexts())
	}
	if !strings.Contains(childSys, "场景B-dup提示词") {
		t.Fatalf("子轮应取 scen-b/dup 的定义：\n%s", childSys)
	}
	if strings.Contains(childSys, "场景A-dup提示词") {
		t.Fatalf("子轮误取同名 scen-a/dup 的定义：\n%s", childSys)
	}
}
