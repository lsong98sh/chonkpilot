// scenario 域门面实现白盒（25-MCP与场景分层模型 §6/§8.1 #8 · T6，2026-09-25）：
//   - app 级场景（随发布只读资源 `scenarios/<id>/`，与 capability/ 平级）可被 list / get 命中；
//   - 场景 id **全局唯一（跨级亦然）**：向 user 级保存与 app 级同名的场景 → **拒绝且不落盘**；
//     同级别同名 = 更新自己那份（放行）；
//   - app 级只读（删除被拒）；
//   - 出厂默认场景 = app 级（不再有「代码内嵌默认场景」物化到 user 级）。
//
// 夹具直接按 capfs 既有目录规则落盘（`scenario.json` + `main.agent.md` + `*.agent.md`）。
package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// testRoots 造一对 app 级根（`<root>/capability` 与 `<root>/scenarios` **平级**，25 §6）
// + 隔离的 user 库路径；返回 (服务, app capability 根, user usr 库路径)。
func testRoots(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	appRoot := filepath.Join(root, "capability")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		t.Fatalf("mkdir capability: %v", err)
	}
	usrPath := filepath.Join(root, "usr", "usr.db")
	return New(kernel.NewBase(nil, kernel.Options{UsrPath: usrPath, AppDir: appRoot})), appRoot, usrPath
}

// writeScenarioDir 按 capfs 规则在 root（某级**场景根**）下写一个场景目录：
// scenario.json + 可选 main.agent.md + 每子 agent 一个 `<名>.agent.md`。
func writeScenarioDir(t *testing.T, root, id, name, mainPrompt string, subs map[string]string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenario.json"),
		[]byte("{\"name\":\""+name+"\"}\n"), 0o644); err != nil {
		t.Fatalf("write scenario.json: %v", err)
	}
	if mainPrompt != "" {
		doc := "# 主\n\n[meta]\nismain=true\n\n[description]\n主\n\n[content]\n" + mainPrompt + "\n"
		if err := os.WriteFile(filepath.Join(dir, "main.agent.md"), []byte(doc), 0o644); err != nil {
			t.Fatalf("write main.agent.md: %v", err)
		}
	}
	for agent, prompt := range subs {
		doc := "# " + agent + "\n\n[description]\n" + agent + "\n\n[content]\n" + prompt + "\n"
		if err := os.WriteFile(filepath.Join(dir, agent+".agent.md"), []byte(doc), 0o644); err != nil {
			t.Fatalf("write %s.agent.md: %v", agent, err)
		}
	}
}

// TestScenarioAppLevelListAndGet：app 级场景可被 list / get 命中（level=app、含 agents 与
// **保留供兼容的派生 systemPrompt**〔= 主 agent prompt；25 §3 起场景层提示词不取用该字段〕），
// 且 list 无需去重（三级 id 全局唯一）。
func TestScenarioAppLevelListAndGet(t *testing.T) {
	s, appRoot, _ := testRoots(t)
	writeScenarioDir(t, filepath.Join(filepath.Dir(appRoot), capfs.ScenariosDirName),
		"app-scn-a", "演示场景", "演示主提示词", map[string]string{"coder": "编码提示词"})

	list, err := s.ScenarioList(facade.ScenarioListRequest{})
	if err != nil {
		t.Fatalf("ScenarioList: %v", err)
	}
	var got *facade.Scenario
	for i := range list.List {
		if list.List[i].ID == "app-scn-a" {
			got = &list.List[i]
		}
	}
	if got == nil {
		t.Fatalf("app 级场景未被 list 命中：%+v", list.List)
	}
	if got.Level != "app" {
		t.Fatalf("level=%q want app", got.Level)
	}
	if got.Name != "演示场景" {
		t.Fatalf("name=%q want 演示场景（取 scenario.json）", got.Name)
	}
	if got.SystemPrompt != "演示主提示词" {
		t.Fatalf("systemPrompt=%q want 演示主提示词（保留供兼容的派生值 = 主 agent prompt，25 §3）", got.SystemPrompt)
	}
	if len(got.Agents) != 2 || !got.Agents[0].IsMain {
		t.Fatalf("agents 形状异常（主 agent 应恒列首位）：%+v", got.Agents)
	}

	get, err := s.ScenarioGet(facade.ScenarioGetRequest{ScenarioID: "app-scn-a", Level: "app"})
	if err != nil {
		t.Fatalf("ScenarioGet(level=app): %v", err)
	}
	if get.Scenario.ID != "app-scn-a" || get.Scenario.Level != "app" {
		t.Fatalf("get 结果异常：%+v", get.Scenario)
	}
}

// TestScenarioSaveRejectsCrossLevelSameID：app 级出厂（只读）场景落地后，向 user 级保存同名场景
// **被拒且不落盘**（25 §6「不允许同名场景」）；同级别同名 = 更新自己那份（放行）；app 级只读。
func TestScenarioSaveRejectsCrossLevelSameID(t *testing.T) {
	s, appRoot, usrPath := testRoots(t)
	writeScenarioDir(t, filepath.Join(filepath.Dir(appRoot), capfs.ScenariosDirName),
		capfs.DefaultScenarioKey, "开发场景", "出厂主提示词", nil)

	// ① 跨级同名（app 已有 default）→ save 到 user 被拒
	_, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
		ID: capfs.DefaultScenarioKey, Level: capfs.KindUser,
		Agents: []facade.ScenarioAgent{{Name: "主", IsMain: true, Prompt: "改过的提示词"}},
	}})
	if err == nil {
		t.Fatal("跨级同名 save 应被拒（场景 id 全局唯一）")
	}
	if !strings.Contains(err.Error(), "全局唯一") {
		t.Fatalf("错误文案未说明重名口径：%v", err)
	}
	if capfs.ScenarioDirExists(capfs.ScenarioUserRoot(usrPath), capfs.DefaultScenarioKey) {
		t.Fatal("被拒的 save 不应落盘")
	}

	// ② 同级别同名 = 更新自己那份（放行；不误判为跨级重名）
	for i := 0; i < 2; i++ {
		if _, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
			ID: "mine", Level: capfs.KindUser,
			Agents: []facade.ScenarioAgent{{Name: "主", IsMain: true, Prompt: "我的提示词"}},
		}}); err != nil {
			t.Fatalf("同级别 save 应放行（第 %d 次）：%v", i+1, err)
		}
	}
	rec, err := s.ScenarioGet(facade.ScenarioGetRequest{ScenarioID: "mine", Level: capfs.KindUser})
	if err != nil || rec.Scenario.Level != capfs.KindUser {
		t.Fatalf("user 级自建场景回读异常：%v %+v", err, rec.Scenario)
	}

	// ③ app 级只读
	if _, err := s.ScenarioDelete(facade.ScenarioDeleteRequest{
		ScenarioID: capfs.DefaultScenarioKey, Level: capfs.KindApp,
	}); err == nil {
		t.Fatal("app 级场景应只读（删除被拒）")
	}
	if !capfs.ScenarioDirExists(filepath.Join(filepath.Dir(appRoot), capfs.ScenariosDirName), capfs.DefaultScenarioKey) {
		t.Fatal("app 级场景不应被删除")
	}
}

// TestScenarioSaveRejectsDuplicateAgentNames：**同一场景内** agent 重名（含大小写差异）→ 保存**被拒
// 且不落盘**，错误信息含「场景 id + 重复 agent 名」（42 §2 (175)，2026-09-26 用户裁决）。
func TestScenarioSaveRejectsDuplicateAgentNames(t *testing.T) {
	s, _, usrPath := testRoots(t)
	const id = "dup-agent-scn"
	_, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
		ID: id, Level: capfs.KindUser,
		Agents: []facade.ScenarioAgent{
			{Name: "主", IsMain: true, Prompt: "主提示词"},
			{Name: "coder", Prompt: "a"},
			{Name: "Coder", Prompt: "b"}, // 与 coder 大小写等价 → 同落 Coder.agent.md
		},
	}})
	if err == nil {
		t.Fatal("同场景内 agent 重名应被拒")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, strings.ToLower(id)) || !strings.Contains(msg, "coder") {
		t.Fatalf("错误应含场景 id + 重复 agent 名：%v", err)
	}
	if capfs.ScenarioDirExists(capfs.ScenarioUserRoot(usrPath), id) {
		t.Fatal("被拒的重名场景不应落盘")
	}
}

// TestScenarioSaveAllowsSameAgentNameAcrossScenarios：**不同场景**同名 agent → 允许（不报错）。
func TestScenarioSaveAllowsSameAgentNameAcrossScenarios(t *testing.T) {
	s, _, _ := testRoots(t)
	for _, id := range []string{"shared-a", "shared-b"} {
		if _, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
			ID: id, Level: capfs.KindUser,
			Agents: []facade.ScenarioAgent{
				{Name: "主", IsMain: true, Prompt: "主提示词"},
				{Name: "shared", Prompt: "共享子 agent"},
			},
		}}); err != nil {
			t.Fatalf("不同场景同名 agent 应允许（%s）：%v", id, err)
		}
	}
}
