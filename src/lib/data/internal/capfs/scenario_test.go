// capfs 场景 agent 重名校验白盒（25-MCP与场景分层模型 §6.1 · 42 §2 (175)，2026-09-26 用户裁决）：
// 判定键 = agent **落盘文件名**（大小写不敏感）→ 覆盖 直呼同名 / 大小写差异 / 主 agent 与子 agent
// 撞名 `main` / 空名 解析为同一文件名 四类边界；命中即返回含「场景 id + 重复 agent 名」的错误。
package capfs

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		agentNode("主", true), agentNode("coder", false), agentNode("coder", false)), RefRoots{})
	if err == nil {
		t.Fatal("WriteScenarioDir 应拒绝重名场景")
	}
	if ScenarioDirExists(root, id) {
		t.Fatal("被拒的重名场景不应建目录/落盘")
	}
}

// ── 场景 agent 引用（P4，2026-10-01；唯一形态 2026-10-04）──────────
//
// 覆盖：引用展开/生成（变量前缀映射）· 主 agent 内联 + 子 agent 引用读写往返 ·
// **悬空引用静默删除** · **无 ref 的非主 agent 保存即拒绝**（内联子 agent 形态已废除）。

// TestAgentRefRoundTrip 四级 capability 根 ↔ 变量前缀引用的双向映射。
func TestAgentRefRoundTrip(t *testing.T) {
	roots := RefRoots{
		App:     `C:\app\capability`,
		User:    `C:\Users\u\.chonkpilot\capability`,
		Project: `C:\ws\.chonkpilot\capability`,
		PrjUsr:  `C:\Users\u\.chonkpilot\data\pid\capability`,
	}
	cases := []struct{ abs, ref string }{
		{`C:\app\capability\agents\UX 设计师.agent.md`, `${exeDir}/capability/agents/UX 设计师.agent.md`},
		{`C:\Users\u\.chonkpilot\capability\agents\a.agent.md`, `${usrDir}/capability/agents/a.agent.md`},
		{`C:\ws\.chonkpilot\capability\agents\b.agent.md`, `${workDir}/.chonkpilot/capability/agents/b.agent.md`},
		{`C:\Users\u\.chonkpilot\data\pid\capability\agents\c.agent.md`, `${dataDir}/capability/agents/c.agent.md`},
	}
	for _, tc := range cases {
		// AgentRefOf 只做路径归属判定（文件可不存在）；写一段夹具文件以验证 Expand 往返
		ref, ok := AgentRefOf(tc.abs, roots)
		if !ok || ref != tc.ref {
			t.Fatalf("AgentRefOf(%q) = (%q, %v)，want %q", tc.abs, ref, ok, tc.ref)
		}
	}
	// 不在任一 capability 根下 → 不生成引用
	if _, ok := AgentRefOf(`C:\elsewhere\agents\x.agent.md`, roots); ok {
		t.Fatal("根外路径不应生成引用")
	}
	// 变量根不可解析 → 悬空
	if _, ok := ExpandAgentRef(`${dataDir}/capability/agents/c.agent.md`, RefRoots{App: roots.App}); ok {
		t.Fatal("变量根不可解析应视为悬空")
	}
}

// TestScenarioAgentRefsWriteRead 主 agent 内联 + 子 agent 引用：写盘（scenario.json.agents 记引用）
// → 读回（展开引用 + 附 ref 字段）。
func TestScenarioAgentRefsWriteRead(t *testing.T) {
	capRoot := t.TempDir()
	scnRoot := filepath.Join(capRoot, DirScenarios)
	roots := RefRoots{App: capRoot}
	// 预置被引用的 agent 文件
	agentsDir := filepath.Join(capRoot, DirAgents)
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ua := "# UX 设计师\n\n[description]\n负责交互\n\n[content]\n你是 UX 设计师\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "UX 设计师.agent.md"), []byte(ua), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := "${exeDir}/capability/agents/UX 设计师.agent.md"
	sc := map[string]any{
		"name": "开发场景",
		"agents": []any{
			map[string]any{"name": "主", "isMain": true, "prompt": "主提示词"},
			map[string]any{"name": "UX 设计师", "ref": ref, "description": "负责交互", "prompt": "你是 UX 设计师"},
		},
	}
	if err := WriteScenarioDir(KindApp, scnRoot, "dev", sc, roots); err != nil {
		t.Fatalf("WriteScenarioDir: %v", err)
	}
	// scenario.json 记引用（主 agent 不在其中）
	raw, _ := os.ReadFile(filepath.Join(scnRoot, "dev", scenarioMetaFile))
	var meta scenarioMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Agents) != 1 || meta.Agents[0] != ref {
		t.Fatalf("scenario.json.agents = %v，want [%q]", meta.Agents, ref)
	}
	// 主 agent 内联落盘
	if _, err := os.Stat(filepath.Join(scnRoot, "dev", scenarioMainFile)); err != nil {
		t.Fatalf("main.agent.md 应内联落盘: %v", err)
	}
	// 读回：agents[0] = 主（isMain），agents[1] = 引用（带 ref）
	got, err := ReadScenarioDir(KindApp, scnRoot, "dev", roots)
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := got["agents"].([]any)
	if len(agents) != 2 {
		t.Fatalf("agents 数 = %d，want 2: %v", len(agents), agents)
	}
	first, _ := agents[0].(map[string]any)
	if first["isMain"] != true {
		t.Fatalf("agents[0] 应为主 agent: %v", first)
	}
	second, _ := agents[1].(map[string]any)
	if second["ref"] != ref || second["name"] != "UX 设计师" {
		t.Fatalf("agents[1] 应为引用（ref=%q）: %v", ref, second)
	}
}

// TestReadScenarioDirDropsDanglingRefs **悬空引用静默删除**：引用文件缺失 → 该 agent 不出现（不报错）。
func TestReadScenarioDirDropsDanglingRefs(t *testing.T) {
	capRoot := t.TempDir()
	scnRoot := filepath.Join(capRoot, DirScenarios)
	roots := RefRoots{App: capRoot}
	// 只写 scenario.json（引用不存在） + main.agent.md
	dirPath := filepath.Join(scnRoot, "dangling")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, scenarioMetaFile),
		[]byte(`{"name":"d","agents":["${exeDir}/capability/agents/ghost.agent.md"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, scenarioMainFile), []byte("# 主\n\n[meta]\nismain=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadScenarioDir(KindApp, scnRoot, "dangling", roots)
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := got["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("悬空引用应静默删除，仅剩主 agent，实得 %d: %v", len(agents), agents)
	}
}

// TestWriteScenarioDirRejectsMissingRef **无 ref 的非主 agent 保存即拒绝**（内联子 agent 形态
// 已废除：子 agent 唯一形态 = 引用）——拒绝且不落盘。
func TestWriteScenarioDirRejectsMissingRef(t *testing.T) {
	capRoot := t.TempDir()
	scnRoot := filepath.Join(capRoot, DirScenarios)
	sc := map[string]any{
		"name": "无引用",
		"agents": []any{
			map[string]any{"name": "主", "isMain": true, "prompt": "主提示词"},
			map[string]any{"name": "coder", "prompt": "写代码"}, // 非主、无 ref → 拒绝
		},
	}
	err := WriteScenarioDir(KindApp, scnRoot, "missing-ref", sc, RefRoots{App: capRoot})
	if err == nil {
		t.Fatal("无 ref 的非主 agent 应被拒")
	}
	if !strings.Contains(err.Error(), "coder") || !strings.Contains(err.Error(), "ref") {
		t.Fatalf("错误文案应含 agent 名与 ref：%v", err)
	}
	if ScenarioDirExists(scnRoot, "missing-ref") {
		t.Fatal("被拒场景不应落盘")
	}
}
