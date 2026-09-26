// sandbox_test.go — chonkpilot-mcp-server 的 agentbox 沙箱接线单测（L2，工程目录
// chonkpilot-test/chonkpilot-mcp-server/unittest）。
//
// 覆盖「工具级开关 → 策略下发」的判定与查表语义（决策 42 §2 (109)）：
//   - 未配置 tool_sandbox → **不注入**（SandboxPolicyFor = ""，默认兼容）；
//   - 开关开启 → 注入 prj `security-*` 换算出的允许目录 JSON（暴露名 self_<契约名> 与契约名均可命中）；
//   - 显式 false / 清空 → 不注入；
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

// TestSandboxPolicyNotInjectedByDefault：未配置工具开关 → 一律不注入（默认兼容）。
func TestSandboxPolicyNotInjectedByDefault(t *testing.T) {
	cfg := mcpms.DefaultConfig()
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: t.TempDir(), Writable: true}})
	for _, name := range []string{"filesys_run", "self_filesys_run", "file_read"} {
		if got := cfg.SandboxPolicyFor(name); got != "" {
			t.Fatalf("未配置 tool_sandbox 时不应注入策略：%s → %q", name, got)
		}
	}
}

// TestSandboxPolicyInjectedPerToolSwitch：开关开启 → 注入允许目录 JSON；两种键形态均命中。
func TestSandboxPolicyInjectedPerToolSwitch(t *testing.T) {
	allow := filepath.Join(t.TempDir(), "proj")
	ro := filepath.Join(t.TempDir(), "ro")
	cfg := mcpms.DefaultConfig()
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: allow, Writable: true}, {Dir: ro}})
	if !cfg.SetToolSandbox(map[string]bool{"self_filesys_run": true}) {
		t.Fatal("首次设置开关应报告变化")
	}
	if cfg.SetToolSandbox(map[string]bool{"self_filesys_run": true}) {
		t.Fatal("重复设置同值不应报告变化")
	}
	// 暴露名形态（UI 工具页写入 self_<契约名>）与契约名形态均命中
	for _, name := range []string{"filesys_run", "self_filesys_run"} {
		got := cfg.SandboxPolicyFor(name)
		if got == "" {
			t.Fatalf("开关开启后应注入策略：%s", name)
		}
		var rules []agentbox.Rule
		if err := json.Unmarshal([]byte(got), &rules); err != nil {
			t.Fatalf("策略应为 []Rule JSON：%q err=%v", got, err)
		}
		if len(rules) != 2 || rules[0].Dir != allow || !rules[0].Writable || rules[1].Writable {
			t.Fatalf("策略内容应为 security-* 换算结果：%+v", rules)
		}
	}
	// 未开启的工具不注入
	if got := cfg.SandboxPolicyFor("file_read"); got != "" {
		t.Fatalf("未开启的工具不应注入：%q", got)
	}
	// 显式 false → 不注入
	cfg.SetToolSandbox(map[string]bool{"filesys_run": false})
	if got := cfg.SandboxPolicyFor("filesys_run"); got != "" {
		t.Fatalf("显式 false 不应注入：%q", got)
	}
	// 清空 → 全部不注入
	cfg.SetToolSandbox(nil)
	if got := cfg.SandboxPolicyFor("self_filesys_run"); got != "" {
		t.Fatalf("清空开关后不应注入：%q", got)
	}
}

// TestSandboxPolicyTracksSecurityDirs：允许目录更新后策略随之更新（运行期注入路径）。
func TestSandboxPolicyTracksSecurityDirs(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	cfg := mcpms.DefaultConfig()
	cfg.SetToolSandbox(map[string]bool{"filesys_run": true})

	// 无允许目录 → 空集（执行器侧解释为全拒）
	if got := cfg.SandboxPolicyFor("filesys_run"); got != "[]" {
		t.Fatalf("无允许目录时应下空空集：%q", got)
	}
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: a, Writable: true}})
	got := cfg.SandboxPolicyFor("filesys_run")
	var rules []agentbox.Rule
	if err := json.Unmarshal([]byte(got), &rules); err != nil || len(rules) != 1 || rules[0].Dir != a {
		t.Fatalf("SetSecurityDirs 后策略未更新：%q (err=%v)", got, err)
	}
	cfg.SetSecurityDirs([]agentbox.Rule{{Dir: b, Writable: true}, {Dir: a}})
	if err := json.Unmarshal([]byte(cfg.SandboxPolicyFor("filesys_run")), &rules); err != nil || len(rules) != 2 {
		t.Fatalf("SetSecurityDirs 覆盖未生效：err=%v rules=%+v", err, rules)
	}
	// 显式空集清空
	cfg.SetSecurityDirs(nil)
	if got := cfg.SandboxPolicyFor("filesys_run"); got != "[]" {
		t.Fatalf("清空允许目录应为空集：%q", got)
	}
}

// TestSandboxEndToEndThroughMCPServer：**端到端**（mcp-server → spawn executor 真实进程）——
// 用已构建的 capability 根（契约 + executor exe 同目录）注册，开启 filesys_run 的工具级开关并
// 配置允许目录，经官方 in-memory MCP 客户端发起 tools/call：
//   - 允许目录内 INS → 成功且落盘；
//   - 允许目录外 INS → isError + agentbox 拒绝消息 + 不落盘。
//
// 未构建 dist/mcp-server 时 Skip（先跑 build-mcp-server.ps1）。
func TestSandboxEndToEndThroughMCPServer(t *testing.T) {
	root := filepath.Join(repoRoot(t), "dist", "mcp-server", "capability")
	exe := filepath.Join(root, "tools", "core", "chonkpilot-core-executor.exe")
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("dist/mcp-server/capability 未构建（先跑 build-mcp-server.ps1）: %v", err)
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
	cfg.SetToolSandbox(map[string]bool{"filesys_run": true})

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
	meta := mcpms.CallContextMeta("sandbox-e2e", base, filepath.Join(base, "data"))
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
