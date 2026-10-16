package mcpgateway

import (
	"testing"
	"time"
)

// TestBoolMapDiff 验证「值变化」判定（新增 / 移除 / 翻转；同值不计）。
func TestBoolMapDiff(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]bool
		want int
	}{
		{"翻转", map[string]bool{"x": true}, map[string]bool{"x": false}, 1},
		{"新增", map[string]bool{}, map[string]bool{"x": true}, 1},
		{"移除", map[string]bool{"x": true}, map[string]bool{}, 1},
		{"无变化", map[string]bool{"x": true}, map[string]bool{"x": true}, 0},
		{"无关新增也计（由重连侧按名过滤）", map[string]bool{"x": true}, map[string]bool{"x": true, "y": true}, 1},
	}
	for _, c := range cases {
		if got := boolMapDiff(c.a, c.b); len(got) != c.want {
			t.Fatalf("%s：boolMapDiff=%v，want len %d", c.name, got, c.want)
		}
	}
}

// TestSandboxMCPChangeReconnects（I-178）：`tool_sandbox.mcps` 开关**变化**时，对受影响的外部
// stdio MCP 触发 Invalidate（重连）；同值重复注入 / 无关键变化不重连。
func TestSandboxMCPChangeReconnects(t *testing.T) {
	_, g := newTestGW(t, 5*time.Second)
	p := newFakeProvider("mcpA", "") // entry.Name = mcpA；无 URL → TransportName = stdio
	addFakeProvider(t, g, p, "t1")

	waitCount := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if p.invalidateCount() == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("重连计数未达 %d（实为 %d）", want, p.invalidateCount())
	}

	// 开启（新增键）→ 触发一次重连。
	g.SetSandboxOverrides(nil, map[string]bool{"mcpA": true})
	waitCount(1)

	// 同值重复注入 → 不重连。
	g.SetSandboxOverrides(nil, map[string]bool{"mcpA": true})
	time.Sleep(60 * time.Millisecond)
	if got := p.invalidateCount(); got != 1 {
		t.Fatalf("同值不应重连：计数 = %d", got)
	}

	// 翻转（true → false）→ 再重连。
	g.SetSandboxOverrides(nil, map[string]bool{"mcpA": false})
	waitCount(2)

	// 变化的是**无关键**（otherMCP 新增）→ 本 provider 不重连。
	g.SetSandboxOverrides(nil, map[string]bool{"mcpA": false, "otherMCP": true})
	time.Sleep(60 * time.Millisecond)
	if got := p.invalidateCount(); got != 2 {
		t.Fatalf("无关键变化不应重连本 MCP：计数 = %d", got)
	}
}
