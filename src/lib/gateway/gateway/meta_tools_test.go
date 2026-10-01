// mcp_find `type` / mcp_load `kind` 的**枚举式**表驱动覆盖（RB-4 ④ 收敛后的完整取值面）。
//
// 背景：`findTypeScope` 把检索面收敛为 `tool | skill | resource | all`（25 §5/T2：prompt/agent
// 已移出 LLM 检索面），未知取值一律**明确报错**（不静默返回空）；`handleMCPLoad` 同理只受理
// `tool | skill | resource`。此前仅有代表用例（TestMCPFindHitsNodePrimitiveAssets /
// TestMCPFindUnknownTypeErrors / TestMCPLoadNodeAssetReadsLive）——本文件逐值枚举，杜绝取值漏测。
package mcpgateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// enumScanner 提供 1 工具 + 1 技能（prompt 且 `_meta.type=skill`）+ 1 资源，
// 供 mcp_find type / mcp_load kind 的枚举式命中与拒绝断言（检索面 = tool/skill/resource）。
type enumScanner struct{}

func (enumScanner) Scan(_ string) (*mcp.Server, error) {
	ms := mcp.NewServer(&mcp.Implementation{Name: "enum", Version: "1.0.0"}, nil)
	ms.AddTool(&mcp.Tool{
		Name:        "enum_tool",
		Description: "枚举工具",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	ms.AddPrompt(&mcp.Prompt{Name: "enum_skill", Description: "枚举技能", Meta: mcp.Meta{"type": "skill"}},
		func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: "enum skill content"}},
			}}, nil
		})
	ms.AddResource(&mcp.Resource{URI: "file://enum_res", Name: "enum_res", Description: "枚举资源", MIMEType: "text/plain"},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: "file://enum_res", MIMEType: "text/plain", Text: "enum res content"},
			}}, nil
		})
	return ms, nil
}

// enumGateway 建一个带「user 级 dir 节点」的 gateway（节点名 ins-enum-user / scope ins-enum）：
// 暴露名 = `ins-enum-user_<原名>`。不经总线 Start，直接调用 handler 白盒断言。
func enumGateway(t *testing.T) *Gateway {
	t.Helper()
	bus, err := mq.New(mq.Options{Prefix: "chonk."})
	if err != nil {
		t.Fatalf("bus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })
	g, err := New(Params{Bus: bus, ContractScanner: enumScanner{}, Logf: t.Logf})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()) })
	if err := g.registerDirNode("ins-enum-user", t.TempDir(), "ins-enum"); err != nil {
		t.Fatalf("registerDirNode: %v", err)
	}
	return g
}

// hasEntry 判定 findNames（kind:name）结果里含目标条目。
func hasEntry(got []string, kind, name string) bool {
	want := kind + ":" + name
	for _, g := range got {
		if g == want {
			return true
		}
	}
	return false
}

// TestMCPFindTypeEnumeration 是 mcp_find `type` 的**枚举式**表驱动用例：
//
//	正例逐值 tool / skill / resource / all → 均不报错，且返回结构含 tools 列表、命中对应类型条目；
//	反例逐值 prompt / agent / scenario 及其它未知 → 均返回 unknown type 错误。
//
// （prompt/agent 必须是**显式拒绝**：mcp_find 面只含 tool/skill/resource/all，与现状一致。）
func TestMCPFindTypeEnumeration(t *testing.T) {
	g := enumGateway(t)
	ctx := instCtx("ins-enum")

	type hit struct{ kind, name string }
	positives := []struct {
		typ  string
		want []hit
	}{
		{"tool", []hit{{"tool", "ins-enum-user_enum_tool"}}},
		{"skill", []hit{{"skill", "enum_skill"}}},
		{"resource", []hit{{"resource", "enum_res"}}},
		{"all", []hit{
			{"tool", "ins-enum-user_enum_tool"},
			{"skill", "enum_skill"},
			{"resource", "enum_res"},
		}},
	}
	for _, c := range positives {
		res, err := g.handleMCPFind(ctx, map[string]any{"type": c.typ})
		if err != nil {
			t.Fatalf("handleMCPFind(type=%s): %v", c.typ, err)
		}
		if res.IsError {
			t.Fatalf("type=%s 不应报错: %+v", c.typ, res.Content)
		}
		// 返回结构须含 tools 列表（正例逐值均成功）
		tc, ok := res.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("type=%s content 类型 = %T", c.typ, res.Content[0])
		}
		var out struct {
			Tools []map[string]any `json:"tools"`
		}
		if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
			t.Fatalf("type=%s 解析结果: %v (%s)", c.typ, err, tc.Text)
		}
		if out.Tools == nil {
			t.Fatalf("type=%s 返回结构应含 tools 列表: %s", c.typ, tc.Text)
		}
		got := findNames(t, res)
		for _, w := range c.want {
			if !hasEntry(got, w.kind, w.name) {
				t.Fatalf("type=%s 应命中 %s:%s，实得 %v", c.typ, w.kind, w.name, got)
			}
		}
	}

	negatives := []string{"prompt", "agent", "scenario", "knowledge", "category", "assets", "*", "toolz"}
	for _, typ := range negatives {
		res, err := g.handleMCPFind(ctx, map[string]any{"type": typ})
		if err != nil {
			t.Fatalf("handleMCPFind(type=%s): %v", typ, err)
		}
		if !res.IsError {
			t.Fatalf("type=%s 应明确报错（unknown type），实得 %+v", typ, res.Content)
		}
		tc, _ := res.Content[0].(*mcp.TextContent)
		if tc == nil || !strings.Contains(tc.Text, "unknown type") {
			t.Fatalf("type=%s 错误消息应含 unknown type: %+v", typ, res.Content)
		}
	}
}

// TestMCPLoadKindEnumeration 是 mcp_load `kind` 的枚举式表驱动用例：
//
//	正例逐值 tool / skill / resource → 均成功（返回 kind 与请求一致）；
//	反例逐值 prompt / agent / scenario / 未知 → 均返回 unknown kind 错误。
//
// （kind 缺省 = tool，另由 TestMCPLoadNodeAssetReadsLive 覆盖。）
func TestMCPLoadKindEnumeration(t *testing.T) {
	g := enumGateway(t)
	ctx := instCtx("ins-enum")

	positives := []struct{ kind, name string }{
		{"tool", "ins-enum-user_enum_tool"},
		{"skill", "enum_skill"},
		{"resource", "enum_res"},
	}
	for _, c := range positives {
		res, err := g.handleMCPLoad(ctx, map[string]any{"kind": c.kind, "name": c.name})
		if err != nil {
			t.Fatalf("handleMCPLoad(kind=%s): %v", c.kind, err)
		}
		if res.IsError {
			t.Fatalf("kind=%s 不应报错: %+v", c.kind, res.Content)
		}
		tc, ok := res.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("kind=%s content 类型 = %T", c.kind, res.Content[0])
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(tc.Text), &m); err != nil {
			t.Fatalf("kind=%s 解析结果: %v (%s)", c.kind, err, tc.Text)
		}
		if got, _ := m["kind"].(string); got != c.kind {
			t.Fatalf("kind=%s 返回 kind = %q，want %q", c.kind, got, c.kind)
		}
	}

	for _, kind := range []string{"prompt", "agent", "scenario", "unknown"} {
		res, err := g.handleMCPLoad(ctx, map[string]any{"kind": kind, "name": "enum_skill"})
		if err != nil {
			t.Fatalf("handleMCPLoad(kind=%s): %v", kind, err)
		}
		if !res.IsError {
			t.Fatalf("kind=%s 应明确报错（unknown kind），实得 %+v", kind, res.Content)
		}
		tc, _ := res.Content[0].(*mcp.TextContent)
		if tc == nil || !strings.Contains(tc.Text, "unknown kind") {
			t.Fatalf("kind=%s 错误消息应含 unknown kind: %+v", kind, res.Content)
		}
	}
}
