// sandbox_test.go — chonkpilot-mcp-gateway 的沙箱规则单测（L2，工程目录
// chonkpilot-test/chonkpilot-mcp-gateway/unittest）。
//
// 覆盖 `mcpgateway.SandboxRule` 的 JSON 解析与 `mcpgateway.WritableRoots` 的提取结果：
// 进程级沙箱（gateway 在 spawn 时施加 Low IL）的「允许写目录」即由此规则集换算而来。
//
// 2026-10-21：Rule / WritableRoots 已从 core/sandbox 收归 gateway 包（沙箱能力随 gateway 发布）。
// 2026-11-03（C1）：安全配置收敛为**只写白名单** —— 规则集里列出的目录一律可写，
// `writable` 字段仅作旧值兼容（读取时忽略）。
package mcpgatewaytest

import (
	"encoding/json"
	"testing"

	mcpgw "github.com/lsong98sh/chonkpilot-agent/gateway"
)

// TestSandboxRuleParseAndWritableRoots：`security-*` 的 value 形状 [{dir}]（兼容旧 {dir,writable}）可解析，
// 且 WritableRoots 取**全部非空目录**（TrimSpace / 跳过空 Dir / 去重 / 保序）。
func TestSandboxRuleParseAndWritableRoots(t *testing.T) {
	raw := `[
		{"dir":"C:\\work\\proj"},
		{"dir":"C:\\work\\proj2","writable":false},
		{"dir":"  C:\\work\\proj  "},
		{"dir":"   "}
	]`
	var rules []mcpgw.SandboxRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatalf("SandboxRule JSON 解析失败：%v", err)
	}
	if len(rules) != 4 {
		t.Fatalf("规则条数 = %d, want 4", len(rules))
	}
	if rules[0].Dir != `C:\work\proj` {
		t.Fatalf("首条规则解析不符：%+v", rules[0])
	}
	// 旧值 writable=false 不再产生约束（只写白名单：列出即可写）。
	if rules[1].Writable {
		t.Fatalf("旧值 writable 字段应按原样解析（不参与过滤）：%+v", rules[1])
	}

	roots := mcpgw.WritableRoots(rules)
	if len(roots) != 2 || roots[0] != `C:\work\proj` || roots[1] != `C:\work\proj2` {
		t.Fatalf("WritableRoots = %v, want [C:\\work\\proj C:\\work\\proj2]", roots)
	}

	// 空规则集 → 空目录集（不返回 nil 元素）。
	if roots := mcpgw.WritableRoots(nil); len(roots) != 0 {
		t.Fatalf("空规则集应返回空目录集，got %v", roots)
	}
}
