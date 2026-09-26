// 命名与唯一性（2026-09-26，in-memory 注册名唯一性规则）：
//
//	① 同类同名注册 → **报错**（拒绝，**不静默覆盖**）：tool / skill / resource 三面各验一次；
//	③ 第三方（Origin=user）条目**一律带来源别名（前缀）**——即使显式 namespace "-" 也不得去前缀，
//	   故与内置（self_*）同名工具经别名区分后**都可 find + 都可 load**（并可调用）；
//	④ 名字**往返一致**：mcp_find 返回的名字可直接喂 mcp_load / mcp_invoke。
//
// 驱动：内存总线 + 自备能力源 server（self）+ httptest 承载的 streamable HTTP 下游（第三方）。
package mcpgatewaytest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// probeServer 是承载「与内置同名工具」的第三方下游（streamable HTTP）。
type probeServer struct{ ts *httptest.Server }

// startProbeServer 起一个暴露工具 `dup_probe` 的下游 server。
func startProbeServer(t *testing.T) *probeServer {
	t.Helper()
	ms := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "1.0.0"}, nil)
	ms.AddTool(&mcp.Tool{
		Name:        "dup_probe",
		Description: "第三方同名工具",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "probe-ok"}}}, nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil))
	t.Cleanup(ts.Close)
	return &probeServer{ts: ts}
}

// callMethodErr 经总线调用方法面并返回错误（不 fatal；用于断言失败路径）。
func callMethodErr(t *testing.T, bus mq.Bus, method string, payload map[string]any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return bus.Emit(ctx, subj(method), payload).Wait().Err()
}

// toolsListNames 取 tools/list 的暴露名集合。
func toolsListNames(t *testing.T, bus mq.Bus) map[string]bool {
	t.Helper()
	res := callMethod(t, bus, "tools/list", map[string]any{})
	out := map[string]bool{}
	items, _ := res["tools"].([]any)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				out[n] = true
			}
		}
	}
	return out
}

// findToolNames 解析 mcp_find 文本结果（{tools:[{name},...]}）的 name 集合。
func findToolNames(t *testing.T, res map[string]any) map[string]bool {
	t.Helper()
	var body struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(textOf(t, res)), &body); err != nil {
		t.Fatalf("解析 mcp_find 结果失败：%v（txt=%s）", err, textOf(t, res))
	}
	out := map[string]bool{}
	for _, x := range body.Tools {
		out[x.Name] = true
	}
	return out
}

// TestNamingDuplicateRejected ① 同类同名注册 → 报错（不静默覆盖）；且既有条目内容不被改写。
func TestNamingDuplicateRejected(t *testing.T) {
	bus, _ := startTestGW(t, nil)

	// 工具：同 (scope, 名) 重复注册 → 报错
	if err := callMethodErr(t, bus, "tools/register", map[string]any{"name": "dup_tool", "description": "首个"}); err != nil {
		t.Fatalf("首次 tools/register 应成功: %v", err)
	}
	if err := callMethodErr(t, bus, "tools/register", map[string]any{"name": "dup_tool", "description": "第二个"}); err == nil {
		t.Fatal("同名工具重复注册应报错（不得静默覆盖）")
	}

	// skill（prompts/register asset_kind=skill）：重复 → 报错 + 原内容不变
	if err := callMethodErr(t, bus, "prompts/register", map[string]any{
		"name": "dup_skill", "description": "首个技能", "asset_kind": "skill", "content": "内容-首个",
	}); err != nil {
		t.Fatalf("首次 prompts/register(skill) 应成功: %v", err)
	}
	if err := callMethodErr(t, bus, "prompts/register", map[string]any{
		"name": "dup_skill", "asset_kind": "skill", "content": "内容-第二个",
	}); err == nil {
		t.Fatal("同名 skill 重复注册应报错（不得静默覆盖）")
	}
	load := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_mcp_load", "arguments": map[string]any{"name": "dup_skill", "kind": "skill"},
	})
	if txt := textOf(t, load); !strings.Contains(txt, "内容-首个") || strings.Contains(txt, "内容-第二个") {
		t.Fatalf("重复注册失败后原 skill 内容应保持首个: %q", txt)
	}

	// resource（resources/register）：重复 → 报错
	if err := callMethodErr(t, bus, "resources/register", map[string]any{
		"name": "dup_res", "uri": "mem://dup-res", "content": "资源首个",
	}); err != nil {
		t.Fatalf("首次 resources/register 应成功: %v", err)
	}
	if err := callMethodErr(t, bus, "resources/register", map[string]any{
		"name": "dup_res", "uri": "mem://dup-res", "content": "资源第二个",
	}); err == nil {
		t.Fatal("同名 resource 重复注册应报错（不得静默覆盖）")
	}
}

// TestThirdPartyAliasUniqueAndRoundTrip ③④ 第三方条目一律带来源别名 → 与内置同名工具
// 别名区分后都可 find + 都可 load（且 find 的名可直接 load / invoke，名字往返一致）。
func TestThirdPartyAliasUniqueAndRoundTrip(t *testing.T) {
	// 先起下游（cleanup 逆序：gateway.Stop 先于 ts.Close，关闭 SSE 长连再收 httptest，避免 Close 阻塞）
	probe := startProbeServer(t)
	bus, _ := startTestGW(t, nil)

	// 内置自注册工具（暴露名 self_dup_probe）
	if err := callMethodErr(t, bus, "tools/register", map[string]any{
		"name": "dup_probe", "description": "内置同名工具", "hot": true,
	}); err != nil {
		t.Fatalf("tools/register(内置) 应成功: %v", err)
	}
	// 第三方下游（origin=user），**显式 namespace "-"（试图去前缀）** → 仍须带来源别名
	callMethod(t, bus, "servers/register", map[string]any{
		"name": "probe", "url": probe.ts.URL, "origin": "user", "namespace": "-",
	})

	names := toolsListNames(t, bus)
	if !names["self_dup_probe"] {
		t.Fatalf("内置工具暴露名应含 self_dup_probe，got %v", names)
	}
	if !names["probe_dup_probe"] {
		t.Fatalf("第三方工具应带来源别名 probe_dup_probe（namespace \"-\" 不得去前缀），got %v", names)
	}
	if names["dup_probe"] {
		t.Fatalf("不应出现裸名 dup_probe（第三方未加别名 = 撞名风险），got %v", names)
	}

	// ③ 两者都可 find；④ find 给的名字直接喂 mcp_load / mcp_invoke（名字往返一致）
	find := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_mcp_find", "arguments": map[string]any{"type": "tool", "query": "dup_probe"},
	})
	found := findToolNames(t, find)
	for _, want := range []string{"self_dup_probe", "probe_dup_probe"} {
		if !found[want] {
			t.Fatalf("mcp_find 应含 %s，got %v（txt=%s）", want, found, textOf(t, find))
		}
		load := callMethod(t, bus, "tools/call", map[string]any{
			"name": "self_mcp_load", "arguments": map[string]any{"name": want, "kind": "tool"},
		})
		if load["isError"] == true {
			t.Fatalf("mcp_load(%s) 应命中（名字往返一致）: %v", want, textOf(t, load))
		}
	}

	// 第三方别名可调用（find/load 给的名字 = 可传回的名字）
	inv := callMethod(t, bus, "tools/call", map[string]any{
		"name": "self_mcp_invoke", "arguments": map[string]any{
			"name": "probe_dup_probe", "arguments": map[string]any{},
		},
	})
	if inv["isError"] == true {
		t.Fatalf("mcp_invoke(probe_dup_probe) 应成功: %v", textOf(t, inv))
	}
	if !strings.Contains(textOf(t, inv), "probe-ok") {
		t.Fatalf("mcp_invoke(probe_dup_probe) 结果异常: %q", textOf(t, inv))
	}
}
