// sandbox_test.go — chonkpilot-mcp-gateway 的 agentbox 下发判定单测（L2，工程目录
// chonkpilot-test/chonkpilot-mcp-gateway/unittest）。
//
// 覆盖 ServerEntry.SandboxPolicyJSON 的**生效条件**（决策 42 §2 (109)）：
//   - 显式 Sandbox=true + 传输 stdio + 允许目录非空 → 下发策略 JSON；
//   - 缺任一条件（未开开关 / http·sse / 允许目录为空）→ 不下发（默认兼容，不污染上游环境）。
package mcpgatewaytest

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/agentbox"
	mcpgw "github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// boolPtr 返回布尔指针（三态字段用）。
func boolPtr(b bool) *bool { return &b }

// TestSandboxPolicyJSONGate：下发判定的四类边界。
func TestSandboxPolicyJSONGate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	rules := []agentbox.Rule{{Dir: dir, Writable: true}}

	cases := []struct {
		name string
		e    mcpgw.ServerEntry
		want bool
	}{
		{"stdio+开启+目录非空 → 下发", mcpgw.ServerEntry{ID: "s", Runtime: "node", Sandbox: boolPtr(true), SandboxDirs: rules}, true},
		{"未设置开关 → 不下发", mcpgw.ServerEntry{ID: "s", Runtime: "node", SandboxDirs: rules}, false},
		{"显式 false → 不下发", mcpgw.ServerEntry{ID: "s", Runtime: "node", Sandbox: boolPtr(false), SandboxDirs: rules}, false},
		{"http 传输 → 不下发（仅 stdio 隔离）", mcpgw.ServerEntry{ID: "s", Runtime: "node", URL: "http://127.0.0.1:1/mcp", Sandbox: boolPtr(true), SandboxDirs: rules}, false},
		{"sse 传输 → 不下发", mcpgw.ServerEntry{ID: "s", Runtime: "node", URL: "http://127.0.0.1:1/sse", Transport: "sse", Sandbox: boolPtr(true), SandboxDirs: rules}, false},
		{"允许目录为空 → 不下发（避免空策略=全拒）", mcpgw.ServerEntry{ID: "s", Runtime: "node", Sandbox: boolPtr(true)}, false},
	}
	for _, c := range cases {
		got := c.e.SandboxPolicyJSON()
		if c.want && got == "" {
			t.Fatalf("%s：应下发策略，got 空", c.name)
		}
		if !c.want && got != "" {
			t.Fatalf("%s：不应下发策略，got %q", c.name, got)
		}
		if !c.want {
			continue
		}
		var parsed []agentbox.Rule
		if err := json.Unmarshal([]byte(got), &parsed); err != nil || len(parsed) != 1 || parsed[0].Dir != dir {
			t.Fatalf("%s：策略 JSON 内容不符：%q err=%v", c.name, got, err)
		}
	}
}

// TestSandboxEnvValueParsableByAgentbox：下发值可被执行器侧（agentbox.Parse）装载为策略。
func TestSandboxEnvValueParsableByAgentbox(t *testing.T) {
	allow := filepath.Join(t.TempDir(), "allow")
	deny := filepath.Join(t.TempDir(), "deny")
	e := mcpgw.ServerEntry{
		ID: "up", Runtime: "node", Sandbox: boolPtr(true),
		SandboxDirs: []agentbox.Rule{{Dir: allow, Writable: true}},
	}
	raw := e.SandboxPolicyJSON()
	if raw == "" {
		t.Fatal("应下发策略")
	}
	p, err := agentbox.Parse(raw)
	if err != nil || p == nil {
		t.Fatalf("执行器侧应能装载下发策略：err=%v", err)
	}
	if err := p.Check(filepath.Join(allow, "a.txt"), true); err != nil {
		t.Fatalf("允许目录内写应放行：%v", err)
	}
	if err := p.Check(filepath.Join(deny, "a.txt"), true); err == nil {
		t.Fatal("目录外写应拒绝")
	}
}
