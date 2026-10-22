// P6（2026-10-19）：外部工具模式（cfg.SkipTools）下 RegisterContracts 只注册资产，
// 不注册工具 —— 系统级 app 根的可执行工具由外部进程 chonkpilot-self-tools 承接。
package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
)

func writeContract(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestRegisterContractsSkipTools：SkipTools=true → tools/list 为空；prompts（prompt+skill）与
// resources 照旧注册。
func TestRegisterContractsSkipTools(t *testing.T) {
	root := t.TempDir()
	writeContract(t, filepath.Join(root, "tools", "core"), "app_tool.tool.md",
		"# 基本信息\n```yaml\nname: app_tool\ncategory: core\nruntime: ../../executors/core.exe\n```\n\n# 摘要\napp tool\n\n# 参数\n```json\n{\"type\":\"object\",\"properties\":{}}\n```\n")
	writeContract(t, filepath.Join(root, "prompts"), "p.prompt.md",
		"# 基本信息\n```yaml\nname: p\n```\n\n# 摘要\nprompt p\n\n# 正文\nhello {{name}}\n")
	writeContract(t, filepath.Join(root, "skills"), "s.skill.md",
		"# 基本信息\n```yaml\nname: s\n```\n\n# 摘要\nskill s\n\n# 正文\nsteps\n")
	writeContract(t, filepath.Join(root, "resources"), "r.resource.md",
		"# 基本信息\n```yaml\nname: r\n```\n\n# 摘要\nresource r\n\n# 正文\ncontent\n")

	ms := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0.0"}, nil)
	cfg := mcpms.DefaultConfig()
	cfg.SkipTools = true
	if err := mcpms.RegisterContracts(ms, root, cfg); err != nil {
		t.Fatalf("RegisterContracts: %v", err)
	}

	ctx := context.Background()
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := ms.Connect(ctx, srvTr, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, cliTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 0 {
		t.Fatalf("SkipTools=true 不应注册工具，got %d", len(tools.Tools))
	}
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(prompts.Prompts) != 2 {
		t.Fatalf("prompts = %d，期望 2（prompt + skill）", len(prompts.Prompts))
	}
	resources, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources.Resources) != 1 {
		t.Fatalf("resources = %d，期望 1", len(resources.Resources))
	}
}

// TestRegisterContractsWithToolsIgnoresSkip：dir 节点扫描（WithTools）仍注册工具。
func TestRegisterContractsWithToolsIgnoresSkip(t *testing.T) {
	root := t.TempDir()
	writeContract(t, filepath.Join(root, "tools"), "app_tool.tool.md",
		"# 基本信息\n```yaml\nname: app_tool\ncategory: core\nruntime: ../../executors/core.exe\n```\n\n# 摘要\napp tool\n\n# 参数\n```json\n{\"type\":\"object\",\"properties\":{}}\n```\n")
	ms := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0.0"}, nil)
	cfg := mcpms.DefaultConfig()
	cfg.SkipTools = true
	if err := mcpms.RegisterContractsWithTools(ms, root, cfg); err != nil {
		t.Fatalf("RegisterContractsWithTools: %v", err)
	}
	ctx := context.Background()
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := ms.Connect(ctx, srvTr, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, cliTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "app_tool" {
		t.Fatalf("WithTools 应注册 app_tool，got %v", tools.Tools)
	}
}
