package persist_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// factoryAgentNames 是出厂预置**子 agent** 期望集（23 个）——与向导 `AGENT_ROLE_TAGS`
// （24 项 − 主协调者）及设计 `docs/agents-wizard/02-预置智能体目录.md` 一一对应。
//
// 守卫目的：预置 agent 被误删 / 改名 → 场景内 `${exeDir}/capability/agents/<名>.agent.md`
// 引用**悬空**（悬空引用在读取时被**静默删除** → 子 agent 凭空消失，无报错）。
var factoryAgentNames = []string{
	// 已有 10
	"需求分析师", "架构设计师", "UX 设计师",
	"前端开发", "后端开发",
	"前端测试工程师", "后端测试工程师", "系统测试工程师",
	"UI 测试工程师", "代码审查",
	// 新增 13（2026-10-04）
	"数据工程师", "集成协调者", "移动端开发", "DevOps 工程师",
	"安全审查员", "性能优化师", "调试工程师", "依赖审计员",
	"侦察工程师", "遗留分析员", "兼容守护", "迁移规划师", "文档撰写者",
}

// factoryAgentsDir 定位仓库内出厂 agents 资源根 `src/initdata/capability/agents`
// （由本测试文件位置反推仓库根；黑盒测试不复制数据层私有路径规则）。
func factoryAgentsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// <repo>/src/test/chonkpilot-data/unittest/persist/factory_agents_test.go
	//   → <repo>/src/initdata/capability/agents
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..",
		"src", "initdata", "capability", "agents")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("出厂 agents 资源缺失（%s）：%v", dir, err)
	}
	return dir
}

// TestFactoryAgentsComplete 断言 23 个出厂预置 agent 文件齐备且结构完整（[meta]/roletag/[description]/[content]）。
func TestFactoryAgentsComplete(t *testing.T) {
	dir := factoryAgentsDir(t)
	if len(factoryAgentNames) != 23 {
		t.Fatalf("期望集应含 23 个 agent，实际 %d", len(factoryAgentNames))
	}
	for _, name := range factoryAgentNames {
		raw, err := os.ReadFile(filepath.Join(dir, name+".agent.md"))
		if err != nil {
			t.Errorf("预置 agent 缺失：%s（%v）", name, err)
			continue
		}
		text := string(raw)
		if !strings.Contains(text, "[meta]") || !strings.Contains(text, "roletag=") {
			t.Errorf("%s：缺 [meta]/roletag", name)
		}
		if !strings.Contains(text, "[description]") {
			t.Errorf("%s：缺 [description]", name)
		}
		if !strings.Contains(text, "[content]") {
			t.Errorf("%s：缺 [content]", name)
		}
		// [content] 非空（防占位/空壳）：提炼后须是可用正文。
		if idx := strings.Index(text, "[content]"); idx >= 0 {
			content := strings.TrimSpace(text[idx+len("[content]"):])
			if utf8.RuneCountInString(content) < 30 {
				t.Errorf("%s：[content] 过短（疑为占位/空壳，%d 字）", name, utf8.RuneCountInString(content))
			}
		}
		// 禁止 `${...}` 模板占位符（prompts-ref 提炼须去平台耦合）。
		if strings.Contains(text, "${") {
			t.Errorf("%s：含 `${...}` 占位符（须去模板耦合）", name)
		}
	}
}

// TestFactoryScenarioAgentRefsResolve 断言出厂默认场景的 agent 引用全部可解析（无悬空引用）。
func TestFactoryScenarioAgentRefsResolve(t *testing.T) {
	agentsDir := factoryAgentsDir(t)
	scn := filepath.Join(filepath.Dir(agentsDir), "scenarios", "default", "scenario.json")
	raw, err := os.ReadFile(scn)
	if err != nil {
		t.Fatalf("出厂默认场景缺失（%s）：%v", scn, err)
	}
	var meta struct {
		Agents []string `json:"agents"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("解析 scenario.json 失败：%v", err)
	}
	for _, ref := range meta.Agents {
		base := strings.TrimSuffix(filepath.Base(strings.ReplaceAll(ref, "\\", "/")), ".agent.md")
		if base == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(agentsDir, base+".agent.md")); err != nil {
			t.Errorf("场景引用悬空：%s（%v）", ref, err)
		}
	}
}
