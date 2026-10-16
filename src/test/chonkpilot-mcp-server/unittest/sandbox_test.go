// sandbox_test.go — chonkpilot-mcp-server 的 agentbox 沙箱接线单测（L2，工程目录
// chonkpilot-test/chonkpilot-mcp-server/unittest）。
//
// 覆盖「按被 spawn 的执行体开关 → 策略下发」的判定与查表语义（决策 42 §2 (109)；
// 2026-10-12 由 executor 类别（core/desktop/browser）重整为**被 spawn 的执行体**单位）：
//   - 未配置 tool_sandbox → **不注入**（SandboxPolicyFor = ""，默认兼容）；
//   - 开关按键为**执行体单位键**（内置 executor = exe 基名，如 chonkpilot-core-executor.exe）；
//   - 单位键开启 → 注入 prj `security-*` 换算出的允许目录 JSON；其它单位键不受影响；
//   - 显式 false / 清空 → 不注入；
//   - **旧形状（按 executor 类别 core/desktop/browser）读取时迁移**（ParseToolSandbox）：
//     core → core 与 dsl 两个 exe 同置；
//   - 允许目录集更新后策略随之更新（运行期注入路径）。
package mcpmservertest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

// TestSandboxPolicyNotInjectedByDefault：未配置开关 → 一律不注入（默认兼容）。
func TestSandboxPolicyNotInjectedByDefault(t *testing.T) {
	cfg := mcpms.DefaultConfig()
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: t.TempDir(), Writable: true}})
	for _, unit := range []string{
		mcpms.ExecutorExeCore, mcpms.ExecutorExeDesktop, mcpms.ExecutorExeBrowser, mcpms.ExecutorExeDSL, "some-mcp",
	} {
		if got := cfg.SandboxPolicyFor(unit); got != "" {
			t.Fatalf("未配置 tool_sandbox 时不应注入策略：%s → %q", unit, got)
		}
	}
}

// TestSandboxPolicyInjectedPerUnit：按执行体单位键开启 → 注入允许目录 JSON；仅该单位命中，
// 其它单位不受影响；返回开关表是否变化（executors + mcps 两张子表）。
func TestSandboxPolicyInjectedPerUnit(t *testing.T) {
	allow := filepath.Join(t.TempDir(), "proj")
	ro := filepath.Join(t.TempDir(), "ro")
	cfg := mcpms.DefaultConfig()
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: allow, Writable: true}, {Dir: ro}})
	if !cfg.SetToolSandbox(map[string]bool{mcpms.ExecutorExeCore: true}, nil) {
		t.Fatal("首次设置开关应报告变化")
	}
	if cfg.SetToolSandbox(map[string]bool{mcpms.ExecutorExeCore: true}, nil) {
		t.Fatal("重复设置同值不应报告变化")
	}
	// 开启的单位命中（策略 = security-* 换算结果）
	got := cfg.SandboxPolicyFor(mcpms.ExecutorExeCore)
	if got == "" {
		t.Fatal("开关开启后应注入策略：core exe")
	}
	var rules []agentbox.Rule
	if err := json.Unmarshal([]byte(got), &rules); err != nil {
		t.Fatalf("策略应为 []Rule JSON：%q err=%v", got, err)
	}
	if len(rules) != 2 || rules[0].Dir != allow || !rules[0].Writable || rules[1].Writable {
		t.Fatalf("策略内容应为 security-* 换算结果：%+v", rules)
	}
	// 未开启的单位不注入（含 mcps 键）
	for _, unit := range []string{mcpms.ExecutorExeDesktop, mcpms.ExecutorExeBrowser, mcpms.ExecutorExeDSL, "some-mcp"} {
		if got := cfg.SandboxPolicyFor(unit); got != "" {
			t.Fatalf("未开启的单位不应注入：%s → %q", unit, got)
		}
	}
	// mcps 子表也可命中
	cfg.SetToolSandbox(nil, map[string]bool{"some-mcp": true})
	if got := cfg.SandboxPolicyFor("some-mcp"); got == "" {
		t.Fatal("mcps 子表命中后应注入策略")
	}
	if got := cfg.SandboxPolicyFor(mcpms.ExecutorExeCore); got != "" {
		t.Fatalf("切换为 mcps 后 executors 应清空：%q", got)
	}
	// 显式 false → 不注入
	cfg.SetToolSandbox(map[string]bool{mcpms.ExecutorExeCore: false}, nil)
	if got := cfg.SandboxPolicyFor(mcpms.ExecutorExeCore); got != "" {
		t.Fatalf("显式 false 不应注入：%q", got)
	}
	// 清空 → 全部不注入
	cfg.SetToolSandbox(map[string]bool{mcpms.ExecutorExeCore: true}, nil)
	cfg.SetToolSandbox(nil, nil)
	if got := cfg.SandboxPolicyFor(mcpms.ExecutorExeCore); got != "" {
		t.Fatalf("清空开关后不应注入：%q", got)
	}
}

// TestSandboxPolicyIgnoresNonUnitKeys：**非单位键**（旧 executor 类别 core/desktop/browser、
// 工具暴露名）不命中任何执行体单位键。
func TestSandboxPolicyIgnoresNonUnitKeys(t *testing.T) {
	cfg := mcpms.DefaultConfig()
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: t.TempDir(), Writable: true}})
	cfg.SetToolSandbox(map[string]bool{
		"core": true, "desktop": true, "browser": true, // 旧类别键
		"self_filesys_run": true, "filesys_run": true, "file_read": true, // 旧工具名键
	}, nil)
	for _, unit := range []string{mcpms.ExecutorExeCore, mcpms.ExecutorExeDesktop, mcpms.ExecutorExeBrowser, mcpms.ExecutorExeDSL} {
		if got := cfg.SandboxPolicyFor(unit); got != "" {
			t.Fatalf("非单位键不应命中执行体单位：%s → %q", unit, got)
		}
	}
}

// TestSandboxPolicyTracksSecurityDirs：允许目录更新后策略随之更新（运行期注入路径）。
func TestSandboxPolicyTracksSecurityDirs(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	cfg := mcpms.DefaultConfig()
	cfg.SetToolSandbox(map[string]bool{mcpms.ExecutorExeCore: true}, nil)

	// 无允许目录 → 空集（执行器侧解释为全拒）
	if got := cfg.SandboxPolicyFor(mcpms.ExecutorExeCore); got != "[]" {
		t.Fatalf("无允许目录时应下发空集：%q", got)
	}
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: a, Writable: true}})
	got := cfg.SandboxPolicyFor(mcpms.ExecutorExeCore)
	var rules []agentbox.Rule
	if err := json.Unmarshal([]byte(got), &rules); err != nil || len(rules) != 1 || rules[0].Dir != a {
		t.Fatalf("SetSecurityDirs 后策略未更新：%q (err=%v)", got, err)
	}
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: b, Writable: true}, {Dir: a}})
	if err := json.Unmarshal([]byte(cfg.SandboxPolicyFor(mcpms.ExecutorExeCore)), &rules); err != nil || len(rules) != 2 {
		t.Fatalf("SetSecurityDirs 覆盖未生效：err=%v rules=%+v", err, rules)
	}
	// 显式空集清空
	cfg.SetSecurityDirs(nil)
	if got := cfg.SandboxPolicyFor(mcpms.ExecutorExeCore); got != "[]" {
		t.Fatalf("清空允许目录应为空集：%q", got)
	}
}

// TestParseToolSandboxMigration：usr `tool_sandbox` 新旧形状解析 + **旧形状读取时迁移**。
func TestParseToolSandboxMigration(t *testing.T) {
	// 新形状原样解析
	got, err := mcpms.ParseToolSandbox(`{"executors":{"chonkpilot-core-executor.exe":true},"mcps":{"foo":true,"bar":false}}`)
	if err != nil {
		t.Fatalf("新形状解析失败：%v", err)
	}
	if !got.Executors[mcpms.ExecutorExeCore] || !got.MCPs["foo"] || got.MCPs["bar"] {
		t.Fatalf("新形状解析结果不符：%+v", got)
	}
	// 空串 → 空配置
	if got, err := mcpms.ParseToolSandbox("  "); err != nil || len(got.Executors) != 0 || len(got.MCPs) != 0 {
		t.Fatalf("空串应得空配置：%+v err=%v", got, err)
	}
	// 非法 JSON → 报错
	if _, err := mcpms.ParseToolSandbox("{not json"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	// 旧形状（executor 类别）→ 迁移：core → core + dsl 两 exe；desktop → desktop；browser → browser
	got, err = mcpms.ParseToolSandbox(`{"core":true,"desktop":true,"browser":false}`)
	if err != nil {
		t.Fatalf("旧形状解析失败：%v", err)
	}
	want := map[string]bool{
		mcpms.ExecutorExeCore:    true,
		mcpms.ExecutorExeDSL:     true,
		mcpms.ExecutorExeDesktop: true,
		mcpms.ExecutorExeBrowser: false,
	}
	if len(got.Executors) != len(want) {
		t.Fatalf("旧形状迁移项数不符：%+v want %+v", got.Executors, want)
	}
	for k, v := range want {
		if got.Executors[k] != v {
			t.Fatalf("旧形状迁移 %s = %v，want %v（%+v）", k, got.Executors[k], v, got.Executors)
		}
	}
	if len(got.MCPs) != 0 {
		t.Fatalf("旧形状迁移不应产生 mcps：%+v", got.MCPs)
	}
	// 迁移结果可直接驱动查询（core 开 → core exe 命中）
	cfg := mcpms.DefaultConfig()
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: t.TempDir(), Writable: true}})
	cfg.SetToolSandbox(got.Executors, got.MCPs)
	if cfg.SandboxPolicyFor(mcpms.ExecutorExeCore) == "" || cfg.SandboxPolicyFor(mcpms.ExecutorExeDSL) == "" {
		t.Fatal("旧形状迁移后 core/dsl 应命中")
	}
	if cfg.SandboxPolicyFor(mcpms.ExecutorExeBrowser) != "" {
		t.Fatal("旧形状迁移后 browser=false 不应命中")
	}
}

// sandboxCapabilityRoot 取已构建的 mcp-server capability 根（含 executors/<cat>-executor.exe）。
// 候选顺序（[41 D-28] 源/产物分区）：`dist/other`（build-mcp-server.ps1 现行产物）→
// `dist/desktop`（桌面单体发行目录）→ `dist/mcp-server`（历史布局，回落）。均不存在 → ""。
func sandboxCapabilityRoot(t *testing.T) string {
	t.Helper()
	repo := repoRoot(t)
	for _, rel := range []string{
		filepath.Join("dist", "other", "capability"),
		filepath.Join("dist", "desktop", "capability"),
		filepath.Join("dist", "mcp-server", "capability"),
	} {
		root := filepath.Join(repo, rel)
		if _, err := os.Stat(filepath.Join(root, "executors", "chonkpilot-core-executor.exe")); err == nil {
			return root
		}
	}
	return ""
}

// TestSandboxEndToEndThroughMCPServer：**端到端**（mcp-server → spawn executor 真实进程）——
// 用已构建的 capability 根（契约 + executor exe 同目录）注册，开启 core executor exe 开关并
// 配置允许目录，经官方 in-memory MCP 客户端发起 tools/call（filesys_run 契约 runtime = core executor）：
//   - 允许目录内 INS → 成功且落盘；
//   - 允许目录外 INS → isError + agentbox 拒绝消息 + 不落盘。
//
// 未构建 capability（先跑 build-mcp-server.ps1 / build-desktop.ps1）时 Skip；
// 设 CHONK_REQUIRE_DIST=1 则**强制失败**（防"假绿"——产物没构建却整包全绿）。
func TestSandboxEndToEndThroughMCPServer(t *testing.T) {
	root := sandboxCapabilityRoot(t)
	if root == "" {
		if os.Getenv("CHONK_REQUIRE_DIST") == "1" {
			t.Fatalf("[CHONK_REQUIRE_DIST=1] 未找到 dist/{other,desktop,mcp-server}/capability，强制失败（防假绿）：先跑 build-mcp-server.ps1")
		}
		t.Logf("⚠️ 跳过（产物缺失 → 假绿风险）：未找到 dist/{other,desktop,mcp-server}/capability；设 CHONK_REQUIRE_DIST=1 可强制失败")
		t.Skipf("未找到 dist/{other,desktop,mcp-server}/capability（先跑 build-mcp-server.ps1）")
	}
	base := t.TempDir()
	allow := filepath.Join(base, "allow")
	deny := filepath.Join(base, "deny")
	for _, d := range []string{allow, deny} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	cfg := mcpms.DefaultConfig()
	cfg.Root = root
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: allow, Writable: true}})
	cfg.SetToolSandbox(map[string]bool{mcpms.ExecutorExeCore: true}, nil)

	ms := mcp.NewServer(&mcp.Implementation{Name: "mcp-server-sandbox-e2e", Version: "1.0.0"}, nil)
	if err := mcpms.RegisterContracts(ms, root, cfg); err != nil {
		t.Fatalf("RegisterContracts failed: %v", err)
	}
	ctx := context.Background()
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := ms.Connect(ctx, srvTr, nil)
	if err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "sandbox-e2e-client", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, cliTr, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	// 调用上下文经协议 _meta 透传（executor 侧 BuildToolEnv / 沙箱均依赖 CHONKPILOT_INSTANCE）
	meta := mcpms.CallContextMeta("sandbox-e2e", base, filepath.Join(base, "data"), "沙箱端到端用例")
	callIns := func(p string) *mcp.CallToolResult {
		t.Helper()
		script := `INS #"` + strings.ReplaceAll(p, `\`, `\\`) + `" "hello"`
		raw, _ := json.Marshal(map[string]any{"script": script})
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "filesys_run", Arguments: json.RawMessage(raw), Meta: meta})
		if err != nil {
			t.Fatalf("CallTool(%s) transport error: %v", p, err)
		}
		return res
	}

	inside := filepath.Join(allow, "in.txt")
	res := callIns(inside)
	if res.IsError {
		t.Fatalf("允许目录内应成功：%+v", res.Content)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("允许目录内文件应落盘：%v", err)
	}

	outside := filepath.Join(deny, "out.txt")
	res = callIns(outside)
	if !res.IsError {
		t.Fatalf("允许目录外必须失败（isError）：%+v", res.Content)
	}
	text := ""
	for _, cc := range res.Content {
		if tc, ok := cc.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "agentbox") {
		t.Fatalf("拒绝消息应带 agentbox 标识：%q", text)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("越界写不得落盘（stat err=%v）", err)
	}
}
