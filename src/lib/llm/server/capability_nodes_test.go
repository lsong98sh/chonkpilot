// T-29 capability 根运行时接入测试（21-llm-server）：
// 用户级 (~/.chonkpilot/capability) + 项目级 (<workDir>/.chonkpilot/capability) + 项目私有级
// (<prjusr 数据根>/capability) 经 gateway dir 节点（servers/register，scope=instance）进入运行时
// 能力面；hot=true 契约进 LLM 工具面。覆盖：四级根解析 / 目录不存在跳过 / 幂等注册 / 退出对称清理。
package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-data/persist"
)

// writeHotToolContract 在 <root>/tools 写一个 hot=true 的 *.tool.md 契约。
func writeHotToolContract(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := buildV2ToolDoc(name, "运行时能力面测试工具", `{"type":"object","properties":{"x":{"type":"string"}}}`, []string{"hot=true"})
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
	if specs := s.capNodeSpecs("", testWorkDir); len(specs) != 0 {
		t.Fatalf("目录不存在应跳过，got %+v", specs)
	}

	userRoot := persist.CapUserRoot(s.opts.UsrPath)
	prjRoot := persist.CapProjectRoot(testWorkDir)
	writeHotToolContract(t, userRoot, "user_demo")
	writeHotToolContract(t, prjRoot, "prj_demo")

	specs := s.capNodeSpecs("", testWorkDir)
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
	// 目录节点接入在后台（gateway servers/register 会 spawn 下游进程）→ 按事件最终收敛断言。
	if _, ok := waitTool(t, s, userTool, true, 5*time.Second); !ok {
		t.Fatalf("用户级工具未接入 tools/list")
	}
	if _, ok := waitTool(t, s, prjTool, true, 5*time.Second); !ok {
		t.Fatalf("项目级工具未接入 tools/list")
	}
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

	// 幂等：重复接入不重复建节点、不产生重复工具（直调同步路径，便于退出前收口）。
	s.registerCapabilityNodes("ins-test", testWorkDir)
	if n := len(capDirNodesOf(s, "ins-test")); n != 2 {
		t.Fatalf("幂等注册后节点数应为 2，got %d", n)
	}
	defs2, _ := s.gc.ListTools(context.Background())
	if c := countTool(defs2, userTool); c != 1 {
		t.Fatalf("幂等注册后用户级工具应唯一，got %d", c)
	}

	// 退出 → 对称注销两个节点，运行时工具面清除。
	s.onExit("instance-exit", jb(map[string]any{"instance_id": "ins-test"}))
	if len(capDirNodesOf(s, "ins-test")) != 0 {
		t.Fatalf("退出后 dirNodes 应清空")
	}
	defs3, _ := s.gc.ListTools(context.Background())
	if countTool(defs3, userTool) != 0 || countTool(defs3, prjTool) != 0 {
		t.Fatalf("退出后工具应注销: %v", toolNames(defs3))
	}
}

// TestCapabilityNodesPrjUsr（P4 2026-10-01）：项目私有级（prjusr）capability 根经 dir 节点
// 接入运行时能力面——节点名 <instanceID>-prjusr、工具带该前缀进入 tools/list 与 LLM 工具面，
// `_meta.server.node` = <instanceID>-prjusr（级别判定依据）。
// prjusr 根由数据层门面 KnowledgeRoot(kind=prjusr) 解析（实例需先经总线注册 → 落 prj 库 project-id）。
func TestCapabilityNodesPrjUsr(t *testing.T) {
	llm := mockLLMServer()
	defer llm.Close()
	s := newTestServerMCP(t, llm)
	registerTestInstance(t, s) // 经总线注册：persist 实例视图 + prj 库可解析 project-id

	// prjusr 根解析（重试容忍总线异步派发）。
	var root string
	for i := 0; i < 25 && root == ""; i++ {
		root = s.prjUsrCapRoot("ins-test")
		if root == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if root == "" {
		t.Fatal("prjusr capability 根解析失败（期望 ~/.chonkpilot/data/<id>/capability）")
	}

	// 实例注册时该目录尚不存在 → 建目录后显式重扫接入。
	writeHotToolContract(t, root, "prjusr_demo")
	s.registerCapabilityNodes("ins-test", testWorkDir)

	prjUsrTool := "ins-test-prjusr_prjusr_demo"
	// capWatcher 的去抖重扫可能与显式重扫交错（先注销再重建，进程拉起有毫秒级窗口）；
	// 测试按事件最终收敛断言，不在窗口内判失败。
	var got map[string]ToolDef
	var lastDefs []ToolDef
	for i := 0; i < 100; i++ {
		defs, err := s.gc.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		lastDefs = defs
		got = map[string]ToolDef{}
		for _, d := range defs {
			got[d.Name] = d
		}
		if _, ok := got[prjUsrTool]; ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := got[prjUsrTool]; !ok {
		t.Fatalf("prjusr 工具未接入 tools/list: %v", toolNames(lastDefs))
	}
	if !got[prjUsrTool].Hot {
		t.Fatalf("prjusr hot=true 契约应标记 hot")
	}
	llmNames := map[string]bool{}
	for _, d := range s.toolsForLLM("ins-test") {
		llmNames[d.Name] = true
	}
	if !llmNames[prjUsrTool] {
		t.Fatalf("LLM 工具面缺 prjusr 工具: %v", llmNames)
	}
	// 节点元信息：级别判定依据（LevelOfNode("<inst>-prjusr") = prjusr）。
	srv, _ := got[prjUsrTool].Meta["server"].(map[string]any)
	if node, _ := srv["node"].(string); node != "ins-test-prjusr" {
		t.Fatalf("_meta.server.node = %q, want ins-test-prjusr", node)
	}
	if lvl := persist.LevelOfNode("ins-test-prjusr"); lvl != "prjusr" {
		t.Fatalf("LevelOfNode(ins-test-prjusr) = %q, want prjusr", lvl)
	}

	// 退出 → 对称注销（含 prjusr）。
	s.onExit("instance-exit", jb(map[string]any{"instance_id": "ins-test"}))
	if len(capDirNodesOf(s, "ins-test")) != 0 {
		t.Fatalf("退出后 dirNodes 应清空")
	}
}

// capDirNodesOf 持锁读 dirNodes（capWatcher / 扫描 goroutine 会并发写该表）。
func capDirNodesOf(s *Server, instanceID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.dirNodes[instanceID]...)
}

// TestCoreMCPPathInDeploymentLayouts 校验 dir 节点前门 core-mcp-server 的**部署布局解析**：
// 桌面产物把 core-mcp-server 与独立 MCP 部署同置于 `dist/other/`（03-构建与部署 §1 /
// build-mcp-server.ps1），旧实现只认 `<exeDir>/<name>` 与 `<exeDir>/mcps/core-mcp/<name>`
// → 用户/项目/项目私有级 dir 节点全部注册失败（capability 原语不进工具面）。
// 期望（规格口径 KB-001-S05 · 21-llm-server §2.1）：上述布局均能定位到该 exe。
func TestCoreMCPPathInDeploymentLayouts(t *testing.T) {
	name := "chonkpilot-core-mcp-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	layouts := []struct {
		desc string
		rel  string // 相对 exeDir 的目录（"" = exeDir 根）
	}{
		{"扁平产物根", ""},
		{"62 §6 文档口径 mcps/core-mcp", filepath.Join("mcps", "core-mcp")},
		{"build-mcp-server 部署区 other", "other"},
		{"历史便携布局 capability/executors", filepath.Join("capability", "executors")},
	}
	for _, tc := range layouts {
		t.Run(tc.desc, func(t *testing.T) {
			exeDir := t.TempDir()
			want := filepath.Join(exeDir, tc.rel, name)
			if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(want, []byte("stub"), 0o644); err != nil {
				t.Fatalf("write stub: %v", err)
			}
			got, err := coreMCPPathIn(exeDir)
			if err != nil {
				t.Fatalf("应定位到 %s，got err=%v", tc.desc, err)
			}
			if got != want {
				t.Fatalf("定位结果 = %q，want %q", got, want)
			}
		})
	}

	// 旧二元布局：exe 在 dist/desktop → 前门在 dist/other。
	t.Run("旧二元布局 dist/desktop + dist/other", func(t *testing.T) {
		root := t.TempDir()
		exeDir := filepath.Join(root, "desktop")
		if err := os.MkdirAll(exeDir, 0o755); err != nil {
			t.Fatalf("mkdir exe dir: %v", err)
		}
		want := filepath.Join(root, "other", name)
		if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
			t.Fatalf("mkdir other: %v", err)
		}
		if err := os.WriteFile(want, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write stub: %v", err)
		}
		got, err := coreMCPPathIn(exeDir)
		if err != nil || got != want {
			t.Fatalf("got (%q, %v)，want %q", got, err, want)
		}
	})

	// 优先级：扁平整装（现行产物）优先于 other 部署区（同名旧副本不得抢先生效）。
	t.Run("扁平产物优先", func(t *testing.T) {
		exeDir := t.TempDir()
		flat := filepath.Join(exeDir, name)
		if err := os.WriteFile(flat, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write flat stub: %v", err)
		}
		other := filepath.Join(exeDir, "other", name)
		if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
			t.Fatalf("mkdir other: %v", err)
		}
		if err := os.WriteFile(other, []byte("stub"), 0o644); err != nil {
			t.Fatalf("write other stub: %v", err)
		}
		if got, err := coreMCPPathIn(exeDir); err != nil || got != flat {
			t.Fatalf("got (%q, %v)，want %q", got, err, flat)
		}
	})

	// 全部布局缺失 → 明确报错（含已查找路径，便于部署诊断）。
	if _, err := coreMCPPathIn(t.TempDir()); err == nil {
		t.Fatal("全部候选缺失应返回错误（不得静默成功）")
	}
}
