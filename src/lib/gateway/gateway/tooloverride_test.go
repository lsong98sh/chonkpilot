// 工具级覆盖**下沉 gateway 统一施加**（I-82）白盒：
//   - tool_async 按**暴露名**覆盖第三方（孤儿键修复）→ tools/list 的 `_meta` 变化 + doCall 生效；
//   - tool_sandbox 的 gateway 侧只保留**外部 MCP 连接侧**开关（L5：gateway 不再持有执行配置）。
package mcpgateway

import (
	"context"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// toolMetaByName 从 tools/list 结果里取指定工具名的 `_meta`（无 → nil）。
func toolMetaByName(t *testing.T, res map[string]any, name string) map[string]any {
	t.Helper()
	arr, _ := res["tools"].([]any)
	for _, it := range arr {
		m, _ := it.(map[string]any)
		if n, _ := m["name"].(string); n == name {
			meta, _ := m["_meta"].(map[string]any)
			return meta
		}
	}
	t.Fatalf("tools/list 缺工具 %s", name)
	return nil
}

// TestToolAsyncOverrideAppliedToThirdPartyList：第三方工具（无契约 async 声明）配置 tool_async
// → tools/list 的 `_meta.async` / `async-threshold` 随覆盖变化（孤儿键修复，I-82）。
func TestToolAsyncOverrideAppliedToThirdPartyList(t *testing.T) {
	bus, g := newTestGW(t, 2*time.Second)
	addFakeProvider(t, g, newFakeProvider("ext", OriginUser), "slow")

	// 基线：第三方（user）工具无 async 声明 → 覆盖前不写 async
	base := toolMetaByName(t, gwCall(t, bus, "tools/list", map[string]any{}), "ext_slow")
	if base != nil && base["async"] != nil {
		t.Fatalf("覆盖前不应有 async: %+v", base)
	}

	g.SetAsyncOverrides(map[string]ToolAsyncOverride{
		"ext_slow": {Mode: "always", Threshold: 30},
	})
	meta := toolMetaByName(t, gwCall(t, bus, "tools/list", map[string]any{}), "ext_slow")
	if meta == nil {
		t.Fatal("覆盖后应有 _meta")
	}
	if got, _ := meta["async"].(string); got != "always" {
		t.Fatalf("_meta.async = %q，want always", got)
	}
	if got, _ := meta["async-threshold"].(float64); got != 30 {
		t.Fatalf("_meta.async-threshold = %v，want 30", meta["async-threshold"])
	}
}

// TestToolAsyncOverrideAffectsDoCall：覆盖 mode=always → 第三方工具调用立即转后台（pending），
// 证明覆盖在 doCall 侧同样生效（非仅 _meta 展示）。
func TestToolAsyncOverrideAffectsDoCall(t *testing.T) {
	bus, g := newTestGW(t, 2*time.Second)
	addFakeProvider(t, g, newFakeProvider("ext", OriginUser), "slow")
	g.SetAsyncOverrides(map[string]ToolAsyncOverride{"ext_slow": {Mode: "always"}})

	res := gwCall(t, bus, "tools/call", map[string]any{
		"name": "ext_slow", "arguments": map[string]any{},
	})
	sc, _ := res["structuredContent"].(map[string]any)
	if sc == nil || sc["status"] != "pending" {
		t.Fatalf("mode=always 应返回 pending: %+v", res)
	}
}

// TestSandboxMCPOverridesGatewaySide：L5 后 gateway 只保留外部 MCP 连接侧开关
// （executors 由装配方写入执行配置，gateway 不再持有）；mcps 子表仍可被 gateway 按名查询，
// 旧 `mcps[].sandbox=true` 读取时迁移并入。
func TestSandboxMCPOverridesGatewaySide(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })
	g, err := New(Params{Transport: newTestTransport(bus), Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })

	if g.sandboxMCPOn("ext") {
		t.Fatal("未开启时不应命中")
	}
	g.SetSandboxOverrides(nil, map[string]bool{"ext": true})
	if !g.sandboxMCPOn("ext") {
		t.Fatal("mcps 开关应可被 gateway 按名查询")
	}
	if g.sandboxMCPOn("missing") {
		t.Fatal("未配置的 MCP 不应命中")
	}
	g.SetSandboxOverrides(map[string]bool{"chonkpilot-core-executor.exe": true}, nil)
	if g.sandboxMCPOn("ext") {
		t.Fatal("executors 表不应写入 mcps")
	}

	// 读取时迁移：旧 mcps[].sandbox=true 并入 mcps（connectServer 调用 mergeLegacySandboxMCP）
	g.mergeLegacySandboxMCP("legacy-mcp")
	if !g.sandboxMCPOn("legacy-mcp") {
		t.Fatal("旧 mcps[].sandbox 迁移后应命中")
	}
}
