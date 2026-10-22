// sandbox_test.go — chonkpilot-mcp-gateway 的沙箱规则单测（L2，工程目录
// chonkpilot-test/chonkpilot-mcp-gateway/unittest）。
//
// 覆盖 `sandbox.Rule` 的 JSON 解析与 `sandbox.WritableRoots` 的提取结果：
// 进程级沙箱（gateway 在 spawn 时施加 Low IL）的「允许写目录」即由此规则集换算而来。
// 注：应用层路径校验（原 agentbox.Check/Allowed）已废弃，本测试不再断言下发/拦截行为。
package mcpgatewaytest

import (
	"encoding/json"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/sandbox"
)

// TestSandboxRuleParseAndWritableRoots：`security-*` 的 value 形状 [{dir, writable}] 可解析，
// 且 WritableRoots 只取可写目录（TrimSpace / 跳过空 Dir / 去重 / 保序）。
func TestSandboxRuleParseAndWritableRoots(t *testing.T) {
	raw := `[
		{"dir":"C:\\work\\proj","writable":true},
		{"dir":"C:\\work\\ro","writable":false},
		{"dir":"  C:\\work\\proj  ","writable":true},
		{"dir":"   ","writable":true}
	]`
	var rules []sandbox.Rule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatalf("sandbox.Rule JSON 解析失败：%v", err)
	}
	if len(rules) != 4 {
		t.Fatalf("规则条数 = %d, want 4", len(rules))
	}
	if rules[0].Dir != `C:\work\proj` || !rules[0].Writable {
		t.Fatalf("首条规则解析不符：%+v", rules[0])
	}
	if rules[1].Writable {
		t.Fatalf("只读目录不应被标记为可写：%+v", rules[1])
	}

	roots := sandbox.WritableRoots(rules)
	if len(roots) != 1 || roots[0] != `C:\work\proj` {
		t.Fatalf("WritableRoots = %v, want [C:\\work\\proj]", roots)
	}
}
