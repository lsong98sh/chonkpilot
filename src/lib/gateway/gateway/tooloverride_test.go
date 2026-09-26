// 工具级覆盖**下沉 gateway 统一施加**（I-82）白盒：
//   - tool_async 按**暴露名**覆盖第三方（孤儿键修复）→ tools/list 的 `_meta` 变化 + doCall 生效；
//   - tool_sandbox 按提供方归属归一到 mcp-server 契约名（builtin dir 节点；第三方不可施加）。
//
// RB-2（2026-09-21）：gateway lib 不再依赖 chonkpilot-mcp-server 包 —— dir 扫描经注入的
// `ContractScanner`（本文件用 fake），沙箱开关经注入的 `SandboxConfig` 窄接口（本文件用 fake 记录
// 写入的契约名键位）。断言语义与改前一致（映射目标键位 = 契约名）。
package mcpgateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeScanner 是 ContractScanner 的测试替身：按 toolNames 建官方 server（原语名 = 工具名）。
type fakeScanner struct{ toolNames []string }

func (f fakeScanner) Scan(root string) (*mcp.Server, error) {
	ms := mcp.NewServer(&mcp.Implementation{Name: "test-dir", Version: "1.0.0"}, nil)
	for _, n := range f.toolNames {
		ms.AddTool(&mcp.Tool{
			Name:        n,
			Description: "测试工具",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
	return ms, nil
}

// fakeSandboxCfg 是 SandboxConfig 的测试替身：记录最近一次写入的契约名开关表。
type fakeSandboxCfg struct {
	mu  sync.Mutex
	eff map[string]bool
}

func (f *fakeSandboxCfg) SetToolSandbox(eff map[string]bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.eff = eff
	return true
}

// enabled 报告最近一次写入中该契约名是否开启隔离。
func (f *fakeSandboxCfg) enabled(contract string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.eff[contract]
}

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

// TestSandboxOverrideMapsDirNodeToolToContract：dir 节点工具（执行经本仓 executor）开启
// tool_sandbox → 共享执行配置的**契约名键位**生效；未开启 → 不隔离。
func TestSandboxOverrideMapsDirNodeToolToContract(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	cfg := &fakeSandboxCfg{}
	g, err := New(Params{
		Bus: bus, MCPConfig: cfg,
		ContractScanner: fakeScanner{toolNames: []string{"sbx_tool"}},
		Logf:            t.Logf,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })

	// dir 节点：契约扫描经注入的 scanner（原语名 = sbx_tool）
	if err := g.registerDirNode("pd", "irrelevant", ""); err != nil {
		t.Fatalf("registerDirNode: %v", err)
	}

	// 未开启 → 不隔离
	if cfg.enabled("sbx_tool") {
		t.Fatal("未开启时不应隔离")
	}
	// 按 dir 节点工具的**暴露名**（pd_sbx_tool）开启 → 归一到契约名 sbx_tool 后生效
	g.SetSandboxOverrides(map[string]bool{"pd_sbx_tool": true})
	if !cfg.enabled("sbx_tool") {
		t.Fatal("dir 节点工具开启 sandbox 后应在共享执行配置生效")
	}
	// 关闭 → 收回
	g.SetSandboxOverrides(map[string]bool{"pd_sbx_tool": false})
	if cfg.enabled("sbx_tool") {
		t.Fatal("关闭后应收回隔离")
	}
}

// TestDirRegisterRequiresScanner：未注入 ContractScanner → dir 注册返回明确错误（gateway 可独立
// 运行，仅无 dir 能力；RB-2 依赖倒置的可回退形态）。
func TestDirRegisterRequiresScanner(t *testing.T) {
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })
	g, err := New(Params{Bus: bus, Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })
	if err := g.registerDirNode("pd", "irrelevant", ""); err == nil {
		t.Fatal("未注入 scanner 时 dir 注册应报错")
	}
}
