// AG 批次白盒（40-演进计划 §AG · AG-1 / AG-2，2026-09-25）：agent 定义接线。
//
// 断言口径（AG-C1…AG-C5）：
//   - 子轮次（runChildTurn）按被委派 agent 的定义执行：`prompt` 进 system、`llmRef` 选 provider、
//     `tools` 过滤工具面、`delegateCond` 附加；**空值 = 不限制 / 不干预**（AG-C4）；
//   - `llmRef` 为空 → 逐级回落：子系统键（数据层读侧已含 defaultLLM 回落）→ 现有通道 → defaultLLM
//     → 系统默认（exe 启动参数）；
//   - 主 agent 的 `llmRef` 作用于顶层轮次（AG-2）；
//   - 委派对象名可为**当前场景内同名 agent**（AG-1 让场景子 agent 可被委派）。
//
// 项 5b（2026-09-25）追加：agent 定义**统一判据** —— 同名（场景子 agent + app 级场景内 agent）时以
// instance 级为准，子轮次定义 / `mcp_load` 内容 / 委派判定三者同源。
//
// 25 §8.1 #8 / T6（2026-09-25）：内存注册表来源 = **app 级场景**（`<exeDir>/capability/scenarios/*/`，
// 随发布只读），非代码 embed（见 `appCapRootWithScenario` 夹具）。
package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// appCapRootWithScenario 造 app 级 **capability 根**（场景根 = `<capRoot>/scenarios`，25 §6）：
// 返回 capability 根（传 `Options.MCPServerRoot` → 数据层 AppDir 同源），并在
// `<capRoot>/scenarios/<id>/` 写入场景（`scenario.json`（子 agent 引用）+ 每 agent 一个
// `<capRoot>/agents/<名>.agent.md` 被引用文件）。agents = agent 名 → 提示词（content 段）；
// 名为 `main` 的成员写为 `main.agent.md`（主 agent 内联）。用于「app 级场景内 agent」的用例。
func appCapRootWithScenario(t *testing.T, id string, agents map[string]string) string {
	t.Helper()
	root := t.TempDir()
	capRoot := filepath.Join(root, "capability")
	dir := filepath.Join(capRoot, "scenarios", id)
	agentsDir := filepath.Join(capRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scenario: %v", err)
	}
	refs := []string{}
	for name, prompt := range agents {
		doc := "# " + name + "\n\n[description]\n" + name + " 描述\n\n[content]\n" + prompt + "\n"
		if name == "main" {
			if err := os.WriteFile(filepath.Join(dir, "main.agent.md"), []byte(doc), 0o644); err != nil {
				t.Fatalf("write main.agent.md: %v", err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(agentsDir, name+".agent.md"), []byte(doc), 0o644); err != nil {
			t.Fatalf("write %s.agent.md: %v", name, err)
		}
		refs = append(refs, "${exeDir}/capability/agents/"+name+".agent.md")
	}
	meta, _ := json.Marshal(map[string]any{"name": id, "agents": refs})
	if err := os.WriteFile(filepath.Join(dir, "scenario.json"), append(meta, '\n'), 0o644); err != nil {
		t.Fatalf("write scenario.json: %v", err)
	}
	return capRoot
}

// writeProjectAgentDoc 依据 agent 领域字段落一个**项目级 capability agent 文件**（契约分区文本），
// 返回场景引用串（`<workDir>/.chonkpilot/capability/agents/<名>.agent.md`）。字段口径与
// `capfs.agentToDoc` 一致（roletag/tools/llm/delegate）；子 agent 唯一形态 = 引用。
func writeProjectAgentDoc(t *testing.T, am map[string]any) string {
	t.Helper()
	name, _ := am["name"].(string)
	desc, _ := am["description"].(string)
	prompt, _ := am["prompt"].(string)
	meta := []string{}
	if rt, _ := am["roleTag"].(string); strings.TrimSpace(rt) != "" {
		meta = append(meta, "roletag="+rt)
	}
	if v, ok := am["tools"]; ok && v != nil {
		if b, err := json.Marshal(v); err == nil && string(b) != "null" {
			meta = append(meta, "tools="+string(b))
		}
	}
	if llm, _ := am["llmRef"].(string); strings.TrimSpace(llm) != "" {
		meta = append(meta, "llm="+llm)
	}
	if dc, _ := am["delegateCond"].(string); strings.TrimSpace(dc) != "" {
		meta = append(meta, "delegate="+dc)
	}
	var b strings.Builder
	b.WriteString("# " + name + "\n\n")
	if len(meta) > 0 {
		b.WriteString("[meta]\n" + strings.Join(meta, "\n") + "\n\n")
	}
	if strings.TrimSpace(desc) != "" {
		b.WriteString("[description]\n" + desc + "\n\n")
	}
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("[content]\n" + prompt + "\n")
	}
	agentsDir := filepath.Join(persist.CapProjectRoot(testWorkDir), "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, name+".agent.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s.agent.md: %v", name, err)
	}
	return "${workDir}/.chonkpilot/capability/agents/" + name + ".agent.md"
}

// agSaveScenario 保存一个场景（id + agents 定义列表）。**子 agent 唯一形态 = 引用**：
// 非主 agent 自动落为项目级 capability agent 文件并补 `ref`（与生产落盘口径一致）；
// 主 agent 保持内联（prompt）。
func agSaveScenario(t *testing.T, s *Server, id string, agents []any) {
	t.Helper()
	for _, a := range agents {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if isMain, _ := am["isMain"].(bool); isMain {
			continue
		}
		am["ref"] = writeProjectAgentDoc(t, am)
	}
	res := dataCall(t, s, "data-scenario-save", map[string]any{
		"instance_id": "ins-test",
		"data":        map[string]any{"id": id, "level": "project", "agents": agents},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("data-scenario-save(%s) failed: %+v", id, res)
	}
}

// agRunChild 驱动一次子轮次（llm_run 的 LLM 委派步骤等价入口）。
func agRunChild(t *testing.T, s *Server, parentReq StartReq, subSession, prompt, agent string) {
	t.Helper()
	if _, _, err := s.runChildTurn(context.Background(), parentReq, subSession, prompt, agent); err != nil {
		t.Fatalf("runChildTurn(%s/%s): %v", subSession, agent, err)
	}
}

// agSystemTexts 取请求体中全部 system 消息文本（content 为字符串形态）。
func agSystemTexts(body map[string]any) []string {
	var out []string
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if text, _ := mm["role"].(string); text != "system" {
			continue
		}
		if c, _ := mm["content"].(string); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// agToolNames 取请求体 tools 的工具名（兼容 {name} 与 {function:{name}} 两种外形）。
func agToolNames(body map[string]any) []string {
	tools, _ := body["tools"].([]any)
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		if n, _ := m["name"].(string); n != "" {
			out = append(out, n)
			continue
		}
		if fn, _ := m["function"].(map[string]any); fn != nil {
			if n, _ := fn["name"].(string); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// agSubAgent 构造场景子 agent（可选扩展字段按需带上；空 = 不输出）。
func agSubAgent(name, prompt string, extra map[string]any) map[string]any {
	a := map[string]any{"name": name, "isMain": false, "prompt": prompt}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

// TestScenarioAgentPromptAndDelegateCondInChildTurn（AG-1 / AG-C4 ①②）：子轮次 system 含被委派
// agent 的 prompt 与 delegateCond；场景内 agent 未配扩展字段时 = 仅 agent 标注（不干预）。
func TestScenarioAgentPromptAndDelegateCondInChildTurn(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerProviderInstance(t, s)
	agSaveScenario(t, s, "ag-sc", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主提示词"},
		agSubAgent("sub1", "子代理提示词XYZ", map[string]any{"delegateCond": "当需要写代码时"}),
		agSubAgent("plain", "", nil),
	})

	parentReq := StartReq{InstanceID: "ins-test", Session: "s-ag-1", Turn: "t-ag-1", ScenarioID: "ag-sc"}
	agRunChild(t, s, parentReq, "s-ag-1-sub1", "do work", "sub1")

	body, _, _ := rec.first(t, 0)
	sysText := strings.Join(agSystemTexts(body), "\n---\n")
	for _, want := range []string{"[子会话 agent: sub1]", "子代理提示词XYZ", "[委派条件] 当需要写代码时"} {
		if !strings.Contains(sysText, want) {
			t.Fatalf("子轮次 system 缺 %q：\n%s", want, sysText)
		}
	}
	// 主 agent 的 prompt 属 **agent 层**：子轮次的 agent 层 = 被委派 agent（25 §3）
	// → 子轮次 system 不再携带主 agent 提示词（旧「场景 systemPrompt（派生=主 agent prompt）恒注入」已作废）。
	if strings.Contains(sysText, "主提示词") {
		t.Fatalf("子轮次 system 不应携带主 agent 提示词（agent 层 = 当前 agent）：\n%s", sysText)
	}

	// ② 空值 = 不干预：prompt / tools / llmRef / delegateCond 全空的 agent → agent 层仅标注一行
	// （25 §3：三层拼成一条 system → 断言"标注在、且无额外附加段"）
	agRunChild(t, s, parentReq, "s-ag-1-plain", "do work", "plain")
	body2, _, _ := rec.first(t, 1)
	sysText2 := strings.Join(agSystemTexts(body2), "\n")
	if !strings.Contains(sysText2, "[子会话 agent: plain]") {
		t.Fatalf("子轮次缺 agent 标注：%s", sysText2)
	}
	if strings.Contains(sysText2, "[委派条件]") {
		t.Fatalf("空值 agent 不应附加委派条件：%s", sysText2)
	}
}

// TestScenarioAgentLLMRefSelectsChildProvider（AG-1 / AG-C1 ①）：子轮次按被委派 agent 的
// llmRef 选 provider（父轮次的 llm 不再被使用）。
func TestScenarioAgentLLMRefSelectsChildProvider(t *testing.T) {
	parentRec, parentSrv := newProviderServer(t)
	childRec, childSrv := newProviderServer(t)
	s := newTestServer(t, parentSrv)
	registerProviderInstance(t, s)
	saveTestUserConfig(t, s, map[string]any{"llms": []any{
		map[string]any{"name": "prov-parent", "baseUrl": parentSrv.URL, "model": "mparent"},
		map[string]any{"name": "prov-child", "baseUrl": childSrv.URL, "apiKey": "sk-child", "model": "mchild"},
	}})
	agSaveScenario(t, s, "ag-llm", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主"},
		agSubAgent("sub-llm", "子", map[string]any{"llmRef": "prov-child"}),
	})

	parentReq := StartReq{
		InstanceID: "ins-test", Session: "s-ag-llm", Turn: "t-ag-llm",
		ScenarioID: "ag-llm", LLMModel: "prov-parent",
	}
	agRunChild(t, s, parentReq, "s-ag-llm-sub", "do work", "sub-llm")

	body, auth, _ := childRec.first(t, 0)
	if got, _ := body["model"].(string); got != "mchild" {
		t.Fatalf("body.model=%v want mchild（agent.llmRef 命中的 provider）", body["model"])
	}
	if auth != "Bearer sk-child" {
		t.Fatalf("Authorization=%q want Bearer sk-child", auth)
	}
	if n := parentRec.count(); n != 0 {
		t.Fatalf("agent.llmRef 生效时不应请求父轮次 provider（%d 次）", n)
	}
}

// TestResolveAgentLLMNameFallbackChain（AG-C1 ③）：llmRef 为空时的逐级回落 ——
// 子系统键（数据层读侧已含 defaultLLM 回落）→ 现有通道 → defaultLLM → 系统默认（""）。
func TestResolveAgentLLMNameFallbackChain(t *testing.T) {
	_, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerProviderInstance(t, s)

	// ① 未配置任何 usr 配置：agent.llmRef 空 + 无子系统键 + 无现有通道 → 系统默认（""）
	if got := s.resolveAgentLLMName("ins-test", "", "", ""); got != "" {
		t.Fatalf("无配置回落 = %q，want \"\"（系统默认 = exe 启动参数）", got)
	}

	saveTestUserConfig(t, s, map[string]any{
		"llms": []any{
			map[string]any{"name": "p-first", "baseUrl": srv.URL, "model": "m1"},
			map[string]any{"name": "p-def", "baseUrl": srv.URL, "model": "m2"},
		},
		"defaultLLM":   "p-def",
		"llm.compress": "p-cmp",
	})

	cases := []struct {
		name                      string
		agentRef, subKey, inherit string
		want                      string
	}{
		{"agent.llmRef 显式 → 用它", "p-agent", "llm.compress", "p-inherit", "p-agent"},
		{"子系统键已配置 → 用它", "", "llm.compress", "", "p-cmp"},
		{"子系统键未配置 → 数据层回落 defaultLLM", "", "llm.memory", "", "p-def"},
		{"无子系统键 → 现有通道优先", "", "", "p-inherit", "p-inherit"},
		{"无子系统键且无现有通道 → defaultLLM", "", "", "", "p-def"},
	}
	for _, tc := range cases {
		if got := s.resolveAgentLLMName("ins-test", tc.agentRef, tc.subKey, tc.inherit); got != tc.want {
			t.Fatalf("%s：got=%q want=%q", tc.name, got, tc.want)
		}
	}

	// defaultLLM 指向旧 int 索引形态（数据层读侧值）→ 经 data.LLMRefName 折算为 name
	saveTestUserConfig(t, s, map[string]any{"defaultLLM": float64(1)})
	if got := s.resolveAgentLLMName("ins-test", "", "", ""); got != "p-def" {
		t.Fatalf("defaultLLM=int 索引回落 = %q，want p-def", got)
	}
}

// TestScenarioAgentToolsWhitelist（AG-1 / AG-C4 ④）：tools 非空 → 仅白名单工具进 LLM 工具面；
// tools 空 → 不限制（全量下发）。
func TestScenarioAgentToolsWhitelist(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServerMCP(t, srv)
	writeToolContractDesc(t, persist.CapProjectRoot(testWorkDir), "ag_tool_a", "工具 A")
	writeToolContractDesc(t, persist.CapProjectRoot(testWorkDir), "ag_tool_b", "工具 B")
	registerTestInstance(t, s) // instance-register：persist 绑定 + capability dir 节点（项目级工具）接入

	var visible []string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		visible = nil
		for _, d := range s.toolsForLLM("ins-test") {
			visible = append(visible, d.Name)
		}
		if len(visible) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(visible) < 2 {
		t.Fatalf("工具面未就绪（可见 %v）", visible)
	}

	agSaveScenario(t, s, "ag-tools", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主"},
		agSubAgent("sub-allow", "子", map[string]any{"tools": []string{visible[0]}}),
		agSubAgent("sub-all", "子", nil),
	})

	parentReq := StartReq{InstanceID: "ins-test", Session: "s-ag-tools", Turn: "t-ag-tools", ScenarioID: "ag-tools"}
	agRunChild(t, s, parentReq, "s-ag-tools-allow", "do", "sub-allow")
	allowBody, _, _ := rec.first(t, 0)
	names := agToolNames(allowBody)
	if len(names) != 1 || names[0] != visible[0] {
		t.Fatalf("白名单未生效：tools=%v want [%s]", names, visible[0])
	}

	agRunChild(t, s, parentReq, "s-ag-tools-all", "do", "sub-all")
	allBody, _, _ := rec.first(t, 1)
	if got := agToolNames(allBody); len(got) != len(visible) {
		t.Fatalf("tools 空应不限制：tools=%v want %v", got, visible)
	}
}

// TestMainAgentLLMRefTopTurn（AG-2）：主 agent 的 llmRef 作用于顶层轮次；为空 → 沿用请求体 llm
// （客户端选择，现状不回归）。
func TestMainAgentLLMRefTopTurn(t *testing.T) {
	clientRec, clientSrv := newProviderServer(t)
	mainRec, mainSrv := newProviderServer(t)
	s := newTestServer(t, clientSrv)
	registerProviderInstance(t, s)
	saveTestUserConfig(t, s, map[string]any{"llms": []any{
		map[string]any{"name": "prov-client", "baseUrl": clientSrv.URL, "model": "mclient"},
		map[string]any{"name": "prov-main", "baseUrl": mainSrv.URL, "model": "mmain"},
	}})
	agSaveScenario(t, s, "ag-top", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主提示词", "llmRef": "prov-main"},
	})
	agSaveScenario(t, s, "ag-top-empty", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主提示词"},
	})

	// 主 agent llmRef 非空 → 顶层轮次用它的 provider（客户端 llm 被覆盖）
	agStartTurn(t, s, "s-ag-top", "t-ag-top", "prov-client", "ag-top")
	sendAndWait(t, s, "s-ag-top", "t-ag-top", "hello")
	body, _, _ := mainRec.first(t, 0)
	if got, _ := body["model"].(string); got != "mmain" {
		t.Fatalf("顶层轮次 body.model=%v want mmain（主 agent.llmRef）", body["model"])
	}
	if n := clientRec.count(); n != 0 {
		t.Fatalf("主 agent.llmRef 生效时不应请求客户端 provider（%d 次）", n)
	}

	// 主 agent llmRef 空 → 现状：沿用请求体 llm
	agStartTurn(t, s, "s-ag-top2", "t-ag-top2", "prov-client", "ag-top-empty")
	sendAndWait(t, s, "s-ag-top2", "t-ag-top2", "hello")
	body2, _, _ := clientRec.first(t, 0)
	if got, _ := body2["model"].(string); got != "mclient" {
		t.Fatalf("llmRef 空 body.model=%v want mclient（请求体 llm 现状）", body2["model"])
	}
}

// TestAgentDelegableScenarioAgent（AG-1 委派面）：app 级场景注册表非空时，场景内同名 agent 亦可
// 作为 llm_run 的委派对象；两者皆无 → 不可委派。
// （注册表 = app 级场景内的 agent，见 registerDomainAgents；此处直接写内存表模拟已登记。）
func TestAgentDelegableScenarioAgent(t *testing.T) {
	_, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerTestInstance(t, s)

	s.mu.Lock()
	s.agents["coder"] = AgentDef{Name: "coder", Content: "app 级场景 agent 提示词"}
	s.mu.Unlock()

	if !s.agentDelegable("ins-test", "ag-del", "coder") {
		t.Fatal("注册表内 agent 应可委派")
	}
	if s.agentDelegable("ins-test", "ag-del", "ghost") {
		t.Fatal("注册表非空且场景内无同名 agent 时不应可委派")
	}
	agSaveScenario(t, s, "ag-del", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主"},
		agSubAgent("ghost", "场景子 agent", nil),
	})
	if !s.agentDelegable("ins-test", "ag-del", "ghost") {
		t.Fatal("场景内同名 agent 应可委派（AG-1）")
	}
}

// TestChildAgentPromptFallsBackToRegisteredDef（AG-1）：场景内无同名 agent 时，提示词回落
// global 级（app 级场景内的 agent）；注册表也无 → 仅标注（不干预）。
func TestChildAgentPromptFallsBackToRegisteredDef(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServer(t, srv)
	registerProviderInstance(t, s)

	s.mu.Lock()
	s.agents["coder"] = AgentDef{Name: "coder", Content: "app 级场景 agent 提示词"}
	s.mu.Unlock()

	parentReq := StartReq{InstanceID: "ins-test", Session: "s-ag-reg", Turn: "t-ag-reg"}
	agRunChild(t, s, parentReq, "s-ag-reg-coder", "do", "coder")
	sysText := strings.Join(agSystemTexts(mustBody(t, rec, 0)), "\n")
	if !strings.Contains(sysText, "app 级场景 agent 提示词") {
		t.Fatalf("未回落 app 级场景 agent 提示词：\n%s", sysText)
	}
}

// TestRegisteredAgentDefFallsBackToAppScenario（25 §8.1 #8 / T6，2026-09-25）：app 级场景内 agent 来源 =
// **app 级场景**（`<exeDir>/scenarios/*/`，与 capability/ 平级、随发布只读），非代码 embed ——
// 全量链路：Start 经数据层门面读 app 级场景登记注册表 → 委派该 agent → 子轮次 system 携带其提示词。
func TestRegisteredAgentDefFallsBackToAppScenario(t *testing.T) {
	rec, srv := newProviderServer(t)
	capRoot := appCapRootWithScenario(t, "app-scn-a", map[string]string{
		"app-agent": "app 级场景内提示词XYZ",
	})
	s := newTestServerMCPAppRoot(t, srv, capRoot)
	registerProviderInstance(t, s)

	d, ok := s.registeredAgentDef("app-agent")
	if !ok || !strings.Contains(d.Content, "app 级场景内提示词XYZ") {
		t.Fatalf("app 级场景内 agent 未登记到内存注册表：ok=%v def=%+v", ok, d)
	}

	parentReq := StartReq{InstanceID: "ins-test", Session: "s-app-ag", Turn: "t-app-ag"}
	agRunChild(t, s, parentReq, "s-app-ag-1", "do", "app-agent")
	sysText := strings.Join(agSystemTexts(mustBody(t, rec, 0)), "\n")
	if !strings.Contains(sysText, "app 级场景内提示词XYZ") {
		t.Fatalf("未从 app 级场景回落提示词：\n%s", sysText)
	}
}

// TestAgentDelegableSameNameInstanceFirst（项 5b，统一判据；25 §5/T2 修订 + T6 来源改口）：同名
// （场景子 agent + app 级场景内 agent）时以 **instance 级**为准 —— 子轮次 system（agent 层）取 instance 级
// 定义，global 级（**app 级场景**内的 agent 注册表）仅在 instance 级未命中时回落。
func TestAgentDelegableSameNameInstanceFirst(t *testing.T) {
	rec, srv := newProviderServer(t)
	// global 级（app 级场景内 agent）来源 = app 级场景（T6）："coder" 与场景子 agent 同名（验 instance 优先）；
	// "solo-global" 仅 global 级存在（验 global 级回落）。
	capRoot := appCapRootWithScenario(t, "app-scn-a", map[string]string{
		"coder":       "app 级场景 coder 提示词",
		"solo-global": "app 级场景独立提示词",
	})
	s := newTestServerMCPAppRoot(t, srv, capRoot)
	registerTestInstance(t, s)

	domainCoder, ok := s.registeredAgentDef("coder")
	if !ok || strings.TrimSpace(domainCoder.Content) == "" {
		t.Fatalf("app 级场景内 agent coder 未登记（global 级基线缺失）：%+v", domainCoder)
	}
	agSaveScenario(t, s, "ag-same", []any{
		map[string]any{"name": "main", "isMain": true, "prompt": "主"},
		agSubAgent("coder", "场景内 instance 级提示词", nil),
	})

	// ① 委派同名 "coder" → 子轮次 system 取 **instance 级**定义（不落 global 级）
	parentReq := StartReq{InstanceID: "ins-test", Session: "s-same", Turn: "t-same", ScenarioID: "ag-same"}
	agRunChild(t, s, parentReq, "s-same-coder", "do", "coder")
	sysText := strings.Join(agSystemTexts(mustBody(t, rec, 0)), "\n")
	if !strings.Contains(sysText, "场景内 instance 级提示词") {
		t.Fatalf("同名子轮次未取 instance 级定义：\n%s", sysText)
	}
	if strings.Contains(sysText, strings.TrimSpace(domainCoder.Content)) {
		t.Fatalf("同名子轮次误用 global 级定义：\n%s", sysText)
	}

	// ② 委派判定：instance 级命中 / 仅 global 级存在 → 均可委派；两级皆无 → 不可委派
	if !s.agentDelegable("ins-test", "ag-same", "coder") {
		t.Fatal("同名 agent（instance 级）应可委派")
	}
	if !s.agentDelegable("ins-test", "ag-same", "solo-global") {
		t.Fatal("仅 global 级（app 级场景内 agent）应可委派")
	}
	if s.agentDelegable("ins-test", "ag-same", "ghost") {
		t.Fatal("两级皆无 → 不应可委派")
	}

	// ③ global 级回落：委派仅 global 级存在的 "solo-global" → system 用 app 级场景内的提示词
	agRunChild(t, s, parentReq, "s-same-solo", "do", "solo-global")
	soloText := strings.Join(agSystemTexts(mustBody(t, rec, 1)), "\n")
	if !strings.Contains(soloText, "app 级场景独立提示词") {
		t.Fatalf("global 级回落未生效：\n%s", soloText)
	}
}

// agStartTurn 发 session-start（带 scenario_id；think=off 简化请求体）。
func agStartTurn(t *testing.T, s *Server, session, turn, llmName, scenarioID string) {
	t.Helper()
	f := s.bus.Emit(context.Background(), "session-start", jb(map[string]any{
		"req_id": "r-" + turn, "instance_id": "ins-test", "session": session, "turn": turn,
		"llm": llmName, "think": "off", "scenario_id": scenarioID,
	}))
	v := f.Wait()
	if v.Err() != nil {
		t.Fatalf("session-start error: %v", v.Err())
	}
	res, _ := v.Result.(map[string]any)
	if ok, _ := res["accepted"].(bool); !ok {
		t.Fatalf("session-start rejected: %+v", res)
	}
}

// mustBody 取第 i 次 LLM 请求体。
func mustBody(t *testing.T, rec *providerRec, i int) map[string]any {
	t.Helper()
	body, _, _ := rec.first(t, i)
	return body
}

// TestSystemPromptLayers（T3 / 25 §3，2026-09-25；全局层身份文案 2026-09-26；目录层 OP-10 2026-10-06）：
// 系统提示词分层拼接 ——
//
//	① 有场景 → system = 全局层（身份 = 当前 agent 名 + 运行环境）+ 目录层（四级数据根 / capability
//	   子目录 / DSL env）+ 场景层（description + 团队成员段）+ agent 层，且**按序**；子轮次全局层
//	   身份 = 被委派 agent 的**裸名**（不带场景前缀）；
//	② 无场景（= 通用模式）→ 注入**全局层 + 目录层**（无场景层 / agent 层），身份 = 肥猫；
//	③ 全局层严格为模板式：`你是 {agentName}，一个全能智能体。你运行在 {env} 中。`
//
// 驱动：session-start（带/不带 scenario_id）+ session-send → 断言 mock LLM 请求体的 system 文本。
func TestSystemPromptLayers(t *testing.T) {
	rec, srv := newProviderServer(t)
	s := newTestServerMCP(t, srv)
	registerProviderInstance(t, s)

	// 场景层取 scenario.description + 团队成员段 → 保存时须显式给 description
	res := dataCall(t, s, "data-scenario-save", map[string]any{
		"instance_id": "ins-test",
		"data": map[string]any{
			"id": "lyr-sc", "level": "project", "description": "我们是一个团队",
			"agents": []any{
				map[string]any{"name": "main", "isMain": true, "prompt": "主 agent 提示词"},
				map[string]any{"name": "scout", "isMain": false, "prompt": "侦察提示词",
					"description": "负责侦察", "roleTag": "侦察员",
					"ref": writeProjectAgentDoc(t, map[string]any{"name": "scout",
						"prompt": "侦察提示词", "description": "负责侦察", "roleTag": "侦察员"})},
			},
		},
	})
	if ok, _ := dataResult(t, res)["ok"].(bool); !ok {
		t.Fatalf("data-scenario-save(lyr-sc) failed: %+v", res)
	}

	// ① 有场景 → 全局层身份 = 当前 agent（顶层 = 主 agent）名；三层按序：全局层 < 场景层
	//    （description < 团队成员段）< agent 层
	agStartTurn(t, s, "s-lyr", "t-lyr", "", "lyr-sc")
	sendAndWait(t, s, "s-lyr", "t-lyr", "hello")
	sysText := strings.Join(agSystemTexts(mustBody(t, rec, 0)), "\n")
	globalWant := "你是 main，一个全能智能体。你运行在 "
	idxGlobal := strings.Index(sysText, globalWant)
	idxDir := strings.Index(sysText, "【目录与运行环境】")
	idxScen := strings.Index(sysText, "【场景】我们是一个团队")
	idxMembers := strings.Index(sysText, "【团队成员】")
	idxMember := strings.Index(sysText, "scout（侦察员）：负责侦察")
	idxAgent := strings.Index(sysText, "主 agent 提示词")
	if idxGlobal < 0 || idxDir < 0 || idxScen < 0 || idxMembers < 0 || idxMember < 0 || idxAgent < 0 {
		t.Fatalf("四层内容缺失（global=%d dir=%d scen=%d members=%d member=%d agent=%d）：\n%s",
			idxGlobal, idxDir, idxScen, idxMembers, idxMember, idxAgent, sysText)
	}
	// ③ 全局层文案严格为模板式（含"一个全能智能体"与"你运行在…中"）
	if !strings.Contains(sysText, "一个全能智能体") || !strings.Contains(sysText, " 中。") {
		t.Fatalf("全局层非模板式：\n%s", sysText)
	}
	// ④ 目录层（OP-10）：含 DSL 只读 env 说明；位于全局层之后、场景层之前
	if !strings.Contains(sysText, "CHONKPILOT_WORKDIR") {
		t.Fatalf("目录层缺 DSL env 说明：\n%s", sysText)
	}
	if !(idxGlobal < idxDir && idxDir < idxScen && idxScen < idxMembers && idxMembers < idxMember && idxMember < idxAgent) {
		t.Fatalf("四层未按序（全局→目录→场景→agent）：\n%s", sysText)
	}

	// ①b 子轮次（被委派 agent）→ 全局层身份 = 被委派 agent 的**裸名**（不带 `<场景id>/` 前缀）
	agRunChild(t, s, StartReq{
		InstanceID: "ins-test", Session: "s-lyr", Turn: "t-lyr", ScenarioID: "lyr-sc",
	}, "s-lyr-sub", "do it", "scout")
	childSys := strings.Join(agSystemTexts(mustBody(t, rec, 1)), "\n")
	if !strings.Contains(childSys, "你是 scout，一个全能智能体。你运行在 ") {
		t.Fatalf("子轮次全局层身份应为被委派 agent 裸名 scout：\n%s", childSys)
	}
	if strings.Contains(childSys, "lyr-sc/scout，一个") {
		t.Fatalf("全局层身份不应带场景前缀：\n%s", childSys)
	}

	// ② 无场景（通用模式）→ 只注入全局层 + 目录层（无场景层 / agent 层），身份 = 肥猫
	agStartTurn(t, s, "s-lyr-none", "t-lyr-none", "", "")
	sendAndWait(t, s, "s-lyr-none", "t-lyr-none", "hi")
	sysText2 := strings.Join(agSystemTexts(mustBody(t, rec, 2)), "\n")
	if !strings.Contains(sysText2, "你是 肥猫，一个全能智能体。你运行在 ") {
		t.Fatalf("通用模式全局层身份应为 肥猫：\n%s", sysText2)
	}
	if strings.Contains(sysText2, "【场景】") || strings.Contains(sysText2, "【团队成员】") {
		t.Fatalf("无场景不应含场景层：\n%s", sysText2)
	}
	// ④ 目录层**不受场景门控**：通用模式也必须注入（OP-10）
	if !strings.Contains(sysText2, "【目录与运行环境】") || !strings.Contains(sysText2, "CHONKPILOT_WORKDIR") {
		t.Fatalf("通用模式应注入目录层：\n%s", sysText2)
	}
}
