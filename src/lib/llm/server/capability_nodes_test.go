// T-29 三级 capability 根运行时接入测试（21-llm-server）：
// 用户级 (~/.chonkpilot/capability) + 项目级 (<workDir>/.chonkpilot/capability) 经 gateway
// dir 节点（servers/register，scope=instance）进入运行时能力面；hot=true 契约进 LLM 工具面。
// 覆盖：三级根解析 / 目录不存在跳过 / 幂等注册 / 退出对称清理。
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// writeHotToolContract 在 <root>/tools 写一个 hot=true 的 *.tool.md 契约。
func writeHotToolContract(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "# " + name + "\n\n[meta]\nhot=true\n\n[description]\n运行时能力面测试工具\n\n" +
		"[parameters]\n{\"type\":\"object\",\"properties\":{\"x\":{\"type\":\"string\"}}}\n"
	if err := os.WriteFile(filepath.Join(dir, name+".tool.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
}

// toolNames 返回 tools/list 中全部工具名（调试断言用）。
func toolNames(defs []ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

// countTool 统计 tools/list 中某工具名的出现次数（幂等断言用）。
func countTool(defs []ToolDef, name string) int {
	n := 0
	for _, d := range defs {
		if d.Name == name {
			n++
		}
	}
	return n
}

// TestCapabilityNodeSpecs 三级根解析：目录不存在 → 跳过（空）；
// 用户/项目根存在 → 依序解析出 user/project 两项（系统级由 self 节点承载，不在此列）。
func TestCapabilityNodeSpecs(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	// 目录均不存在 → 空（不影响启动）。
	if specs := s.capNodeSpecs(testWorkDir); len(specs) != 0 {
		t.Fatalf("目录不存在应跳过，got %+v", specs)
	}

	userRoot := persist.CapUserRoot(s.opts.UsrPath)
	prjRoot := persist.CapProjectRoot(testWorkDir)
	writeHotToolContract(t, userRoot, "user_demo")
	writeHotToolContract(t, prjRoot, "prj_demo")

	specs := s.capNodeSpecs(testWorkDir)
	if len(specs) != 2 {
		t.Fatalf("应有 user/project 两项，got %+v", specs)
	}
	if specs[0].suffix != "user" || specs[0].root != userRoot {
		t.Fatalf("user 根解析错误: %+v", specs[0])
	}
	if specs[1].suffix != "project" || specs[1].root != prjRoot {
		t.Fatalf("project 根解析错误: %+v", specs[1])
	}
}

// TestCapabilityNodesRuntime 用户/项目级 capability 经 dir 节点进入运行时能力面：
// tools-list 可见（带节点前缀）、hot=true 契约进 LLM 工具面、重复注册幂等、退出对称清理。
func TestCapabilityNodesRuntime(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)

	writeHotToolContract(t, persist.CapUserRoot(s.opts.UsrPath), "user_demo")
	writeHotToolContract(t, persist.CapProjectRoot(testWorkDir), "prj_demo")

	// 实例注册 → 接入两个 dir 节点（直接调 handler，避免异步等待）。
	regPayload := jb(map[string]any{
		"instance_id": "ins-test", "client_type": "unittest", "work_dir": testWorkDir,
	})
	s.onInstanceRegister("instance-register", regPayload)

	userTool := "ins-test-user_user_demo"
	prjTool := "ins-test-project_prj_demo"
	defs, err := s.gc.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]ToolDef{}
	for _, d := range defs {
		got[d.Name] = d
	}
	if _, ok := got[userTool]; !ok {
		t.Fatalf("用户级工具未接入 tools/list: %v", toolNames(defs))
	}
	if _, ok := got[prjTool]; !ok {
		t.Fatalf("项目级工具未接入 tools/list: %v", toolNames(defs))
	}
	if !got[userTool].Hot || !got[prjTool].Hot {
		t.Fatalf("hot=true 契约应标记 hot: user=%v prj=%v", got[userTool].Hot, got[prjTool].Hot)
	}

	// hot 工具进 LLM 工具面（归属当前 instance）。
	llmNames := map[string]bool{}
	for _, d := range s.toolsForLLM("ins-test") {
		llmNames[d.Name] = true
	}
	if !llmNames[userTool] || !llmNames[prjTool] {
		t.Fatalf("LLM 工具面缺用户/项目级工具: %v", llmNames)
	}

	// 幂等：重复 instance-register 不重复建节点、不产生重复工具。
	s.onInstanceRegister("instance-register", regPayload)
	if n := len(s.dirNodes["ins-test"]); n != 2 {
		t.Fatalf("幂等注册后节点数应为 2，got %d", n)
	}
	defs2, _ := s.gc.ListTools(context.Background())
	if c := countTool(defs2, userTool); c != 1 {
		t.Fatalf("幂等注册后用户级工具应唯一，got %d", c)
	}

	// 退出 → 对称注销两个节点，运行时工具面清除。
	s.onExit("instance-exit", jb(map[string]any{"instance_id": "ins-test"}))
	if _, ok := s.dirNodes["ins-test"]; ok {
		t.Fatalf("退出后 dirNodes 应清空")
	}
	defs3, _ := s.gc.ListTools(context.Background())
	if countTool(defs3, userTool) != 0 || countTool(defs3, prjTool) != 0 {
		t.Fatalf("退出后工具应注销: %v", toolNames(defs3))
	}
}
