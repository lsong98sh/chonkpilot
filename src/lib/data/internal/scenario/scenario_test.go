// scenario 域门面实现白盒（25-MCP与场景分层模型 §6，2026-09-26 更新）：
//   - app 级场景（`<capability>/scenarios/<id>/`；出厂内容 = 磁盘目录 src/initdata/capability/scenarios）可被 list / get 命中；
//   - 场景 id **全局唯一（跨级亦然）**：向 user 级保存与 app 级同名的场景 → **拒绝且不落盘**；
//     同级别同名 = 更新自己那份（放行）；
//   - **app 级可编辑**（保存写 app 根、删除允许）；
//   - 出厂场景 = app 级（磁盘目录；不再 embed、不再自动物化）。
//
// 夹具直接按 capfs 既有目录规则落盘（`scenario.json`（子 agent 引用）+ `main.agent.md` +
// `capability/agents/*.agent.md` 被引用文件）。
package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/capfs"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// defScenarioID 出厂场景目录名（app 级 `scenarios/default/`）。
const defScenarioID = "default"

// testRoots 造 app 级 **capability 根**（含场景子目录 `<appRoot>/scenarios`，25 §6）
// + 隔离的 user 库路径；返回 (服务, app capability 根, user usr 库路径)。
func testRoots(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	appRoot := filepath.Join(root, "capability")
	if err := os.MkdirAll(filepath.Join(appRoot, capfs.ScenariosDirName), 0o755); err != nil {
		t.Fatalf("mkdir capability/scenarios: %v", err)
	}
	usrPath := filepath.Join(root, "usr", "usr.db")
	return New(kernel.NewBase(nil, kernel.Options{UsrPath: usrPath, AppDir: appRoot})), appRoot, usrPath
}

// writeScenarioDir 按 capfs 规则在 root（某级**场景根**）下写一个场景目录：
// scenario.json（含子 agent **引用**）+ 可选 main.agent.md + 每子 agent 一个被引用文件
// `<capRoot>/agents/<名>.agent.md`（capRoot = filepath.Dir(root) = app 级 capability 根）。
func writeScenarioDir(t *testing.T, root, id, name, mainPrompt string, subs map[string]string) {
	t.Helper()
	capRoot := filepath.Dir(root)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	refs := make([]string, 0, len(subs))
	if len(subs) > 0 {
		agentsDir := filepath.Join(capRoot, "agents")
		if err := os.MkdirAll(agentsDir, 0o755); err != nil {
			t.Fatalf("mkdir agents: %v", err)
		}
		for agent, prompt := range subs {
			doc := "# " + agent + "\n\n[description]\n" + agent + "\n\n[content]\n" + prompt + "\n"
			if err := os.WriteFile(filepath.Join(agentsDir, agent+".agent.md"), []byte(doc), 0o644); err != nil {
				t.Fatalf("write %s.agent.md: %v", agent, err)
			}
			refs = append(refs, "${exeDir}/capability/agents/"+agent+".agent.md")
		}
	}
	meta := map[string]any{"name": name}
	if len(refs) > 0 {
		meta["agents"] = refs
	}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "scenario.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write scenario.json: %v", err)
	}
	if mainPrompt != "" {
		doc := "# 主\n\n[meta]\nismain=true\n\n[description]\n主\n\n[content]\n" + mainPrompt + "\n"
		if err := os.WriteFile(filepath.Join(dir, "main.agent.md"), []byte(doc), 0o644); err != nil {
			t.Fatalf("write main.agent.md: %v", err)
		}
	}
}

// TestScenarioInvalidIDRejected 非法场景 id（空 / `.` / `..` / `../x` / 路径分隔符）→
// Save/Get/Delete 一律**拒绝**（A-31：防 `..` 拼接越界删掉整棵 capability 树）；合法 id 放行，
// 且非法 id 操作不波及已存在场景。
func TestScenarioInvalidIDRejected(t *testing.T) {
	s, appRoot, usrPath := testRoots(t)
	if _, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
		ID: "ok-scn", Level: capfs.KindUser,
		Agents: []facade.ScenarioAgent{{Name: "主", IsMain: true, Prompt: "p"}},
	}}); err != nil {
		t.Fatalf("合法 id 保存应放行：%v", err)
	}
	for _, id := range []string{"", ".", "..", "../x", `..\x`, "a/b"} {
		if _, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{ID: id, Level: capfs.KindUser}}); err == nil {
			t.Fatalf("Save 非法 id %q 应被拒", id)
		}
		if _, err := s.ScenarioGet(facade.ScenarioGetRequest{ScenarioID: id}); err == nil {
			t.Fatalf("Get 非法 id %q 应被拒", id)
		}
		if _, err := s.ScenarioDelete(facade.ScenarioDeleteRequest{ScenarioID: id, Level: capfs.KindUser}); err == nil {
			t.Fatalf("Delete 非法 id %q 应被拒", id)
		}
	}
	// 非法 id 操作不得波及已存在的合法场景（`..` 曾会删掉场景根 / 整棵 capability 树）
	if !capfs.ScenarioDirExists(capfs.ScenarioUserRoot(usrPath), "ok-scn") {
		t.Fatal("非法 id 操作不应波及已存在场景")
	}
	if _, err := os.Stat(appRoot); err != nil {
		t.Fatalf("非法 id 操作不应损坏 capability 树：%v", err)
	}
	if _, err := s.ScenarioGet(facade.ScenarioGetRequest{ScenarioID: "ok-scn", Level: capfs.KindUser}); err != nil {
		t.Fatalf("合法 id get 应放行：%v", err)
	}
}

// TestScenarioAppLevelListAndGet：app 级场景可被 list / get 命中（level=app、含主 agent +
// 子 agent 引用展开），且 list 无需去重（四级 id 全局唯一）。
func TestScenarioAppLevelListAndGet(t *testing.T) {
	s, appRoot, _ := testRoots(t)
	writeScenarioDir(t, capfs.ScenariosRoot(appRoot),
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
	if len(got.Agents) != 2 || !got.Agents[0].IsMain {
		t.Fatalf("agents 形状异常（主 agent 应恒列首位）：%+v", got.Agents)
	}
	if got.Agents[1].Ref == "" || got.Agents[1].Prompt != "编码提示词" {
		t.Fatalf("子 agent 应按引用展开（ref + 被引文件正文）：%+v", got.Agents[1])
	}

	get, err := s.ScenarioGet(facade.ScenarioGetRequest{ScenarioID: "app-scn-a", Level: "app"})
	if err != nil {
		t.Fatalf("ScenarioGet(level=app): %v", err)
	}
	if get.Scenario.ID != "app-scn-a" || get.Scenario.Level != "app" {
		t.Fatalf("get 结果异常：%+v", get.Scenario)
	}
}

// TestScenarioSaveRejectsCrossLevelSameID：app 级出厂场景落地后，向 user 级保存同名场景
// **被拒且不落盘**（25 §6「不允许同名场景」）；同级别同名 = 更新自己那份（放行）；
// **app 级可编辑**（删除允许）。
func TestScenarioSaveRejectsCrossLevelSameID(t *testing.T) {
	s, appRoot, usrPath := testRoots(t)
	writeScenarioDir(t, capfs.ScenariosRoot(appRoot),
		defScenarioID, "开发场景", "出厂主提示词", nil)

	// ① 跨级同名（app 已有 default）→ save 到 user 被拒
	_, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
		ID: defScenarioID, Level: capfs.KindUser,
		Agents: []facade.ScenarioAgent{{Name: "主", IsMain: true, Prompt: "改过的提示词"}},
	}})
	if err == nil {
		t.Fatal("跨级同名 save 应被拒（场景 id 全局唯一）")
	}
	if !strings.Contains(err.Error(), "全局唯一") {
		t.Fatalf("错误文案未说明重名口径：%v", err)
	}
	if capfs.ScenarioDirExists(capfs.ScenarioUserRoot(usrPath), defScenarioID) {
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

	// ③ app 级可编辑：保存到 app 根（同级别更新）→ app 根回读命中
	if _, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
		ID: defScenarioID, Level: capfs.KindApp,
		Agents: []facade.ScenarioAgent{{Name: "主", IsMain: true, Prompt: "app 改后提示词"}},
	}}); err != nil {
		t.Fatalf("app 级同名保存应放行（可编辑）：%v", err)
	}
	appRec, err := s.ScenarioGet(facade.ScenarioGetRequest{ScenarioID: defScenarioID, Level: capfs.KindApp})
	if err != nil || len(appRec.Scenario.Agents) == 0 || appRec.Scenario.Agents[0].Prompt != "app 改后提示词" {
		t.Fatalf("app 级保存未写 app 根：%v %+v", err, appRec.Scenario)
	}
	// 删除 app 级允许
	if _, err := s.ScenarioDelete(facade.ScenarioDeleteRequest{
		ScenarioID: defScenarioID, Level: capfs.KindApp,
	}); err != nil {
		t.Fatalf("app 级场景应可删除：%v", err)
	}
	if capfs.ScenarioDirExists(capfs.ScenariosRoot(appRoot), defScenarioID) {
		t.Fatal("app 级场景应被删除")
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

// TestScenarioSaveAllowsSameAgentNameAcrossScenarios：**不同场景**同名 agent（引用同一 user 级
// agent 文件）→ 允许（不报错）。
func TestScenarioSaveAllowsSameAgentNameAcrossScenarios(t *testing.T) {
	s, _, usrPath := testRoots(t)
	// 预置被引用的共享子 agent 文件（user 级 capability/agents）
	agentsDir := filepath.Join(filepath.Dir(usrPath), "capability", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "shared.agent.md"),
		[]byte("# shared\n\n[content]\n共享子 agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := "${usrDir}/capability/agents/shared.agent.md"
	for _, id := range []string{"shared-a", "shared-b"} {
		if _, err := s.ScenarioSave(facade.ScenarioSaveRequest{Scenario: facade.Scenario{
			ID: id, Level: capfs.KindUser,
			Agents: []facade.ScenarioAgent{
				{Name: "主", IsMain: true, Prompt: "主提示词"},
				{Name: "shared", Prompt: "共享子 agent", Ref: ref},
			},
		}}); err != nil {
			t.Fatalf("不同场景同名 agent 应允许（%s）：%v", id, err)
		}
	}
}
