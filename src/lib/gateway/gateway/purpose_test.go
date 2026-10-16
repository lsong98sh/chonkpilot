// C1 白盒（2026-10-15 · SUBCHAT-1）：`tool_call_display_name`（= purpose）注入通道。
//
// 断言三件事：
//
//	① 取值时机 = doCall 剥离**之前** → 值进 req.Context.Purpose（args 仍不传下游，剥离逻辑不变）；
//	② 进程内通道 = withTurnContext → ctxKeyPurpose → PurposeFromContext；
//	③ 域工具通道 = turnContextPayload 的 payload["context"]["purpose"]；协议 _meta 往返（memNode 包装）同源。
package mcpgateway

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestTurnContextPurpose 覆盖进程内通道（withTurnContext → PurposeFromContext）与
// 域工具通道（turnContextPayload 的 "purpose" 键）：有值则带、无值则不带。
func TestTurnContextPurpose(t *testing.T) {
	ctx := withTurnContext(context.Background(), Context{Session: "s1", Purpose: "审查这次改动"})
	if got := PurposeFromContext(ctx); got != "审查这次改动" {
		t.Fatalf("PurposeFromContext = %q, want 审查这次改动", got)
	}
	if payload := turnContextPayload(ctx); payload["purpose"] != "审查这次改动" {
		t.Fatalf("turnContextPayload[purpose] = %q, want 审查这次改动（域工具 payload.context.purpose）", payload["purpose"])
	}
	// 无 purpose → 不注入 ctx key / 不写 payload 键（保持既有形态）
	empty := withTurnContext(context.Background(), Context{Session: "s1"})
	if got := PurposeFromContext(empty); got != "" {
		t.Fatalf("无 purpose 时 PurposeFromContext 应为空，got %q", got)
	}
	if _, ok := turnContextPayload(empty)["purpose"]; ok {
		t.Fatal("无 purpose 时 turnContextPayload 不应写 purpose 键")
	}
}

// TestExecCtxFromMetaPurpose 覆盖协议 _meta 往返（memNode 包装：_meta.chonkpilot.purpose → ctx）。
func TestExecCtxFromMetaPurpose(t *testing.T) {
	ctx := execCtxFromMeta(context.Background(), mcp.Meta{"chonkpilot": map[string]any{
		"instance_id": "ins-1", "purpose": "再来一次",
	}})
	if got := PurposeFromContext(ctx); got != "再来一次" {
		t.Fatalf("execCtxFromMeta 未还原 purpose，got %q", got)
	}
}

// purposeProbe 记录下游调用实际收到的 (ctx purpose, args)。
type purposeProbe struct {
	purpose string
	args    map[string]any
}

func (p *purposeProbe) Name() string { return "probe:purpose" }

func (p *purposeProbe) ListTools(context.Context) ([]*mcp.Tool, error) {
	return []*mcp.Tool{{
		Name:        "ptool",
		Description: "ptool",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}}, nil
}

func (p *purposeProbe) Call(ctx context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
	p.purpose = PurposeFromContext(ctx)
	p.args = args
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
}

func (p *purposeProbe) Invalidate(context.Context) (bool, error)                { return true, nil }
func (p *purposeProbe) Terminate(context.Context, string, string) (bool, error) { return true, nil }
func (p *purposeProbe) Close() error                                            { return nil }

// TestDoCallInjectsPurposeBeforeStripping 断言 doCall：展示名在**剥离前**取出写入调用上下文
// （下游经 PurposeFromContext 取到），同时 args 仍被剥离（不传下游）——「注入与剥离不冲突」。
func TestDoCallInjectsPurposeBeforeStripping(t *testing.T) {
	_, g := newTestGW(t, 30*time.Second)
	p := &purposeProbe{}
	ps := &providerState{
		key:    provKey("proxied", scopeGlobal, "pp"),
		entry:  &ServerEntry{ID: "pp", Name: "pp", Enabled: true, Origin: "builtin"},
		prov:   p,
		status: "connected",
		cb:     newBreaker(5, 30*time.Second, false),
		scope:  scopeGlobal,
	}
	tools, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if err := g.reg.registerProvider(ps, tools, ""); err != nil {
		t.Fatalf("registerProvider: %v", err)
	}

	_, code, msg := g.doCall(CallReq{
		Context: Context{InstanceID: "ins-1"},
		Name:    "pp_ptool",
		Arguments: map[string]any{
			"tool_call_display_name": "审查这次改动",
			"foo":                    "bar",
		},
	})
	if code != 0 {
		t.Fatalf("doCall 失败 code=%d msg=%s", code, msg)
	}
	if p.purpose != "审查这次改动" {
		t.Fatalf("下游 PurposeFromContext = %q, want 审查这次改动（取值应在剥离前）", p.purpose)
	}
	if _, exists := p.args["tool_call_display_name"]; exists {
		t.Fatalf("args 仍含展示名（剥离逻辑被破坏）: %v", p.args)
	}
	if p.args["foo"] != "bar" {
		t.Fatalf("其它 args 应原样下发，got %v", p.args)
	}
}
