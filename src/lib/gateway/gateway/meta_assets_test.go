// 原语面聚合白盒（GATEWAY-2 L5）：prompts / resources 由**已接入下游 provider** 的
// list/get/read 转发 + 注册资产并入（prompts/register、resources/register）。
//
// 覆盖：
//   - prompts/list：下游节点原语（带 type/scope/node）+ 注册 prompt/skill 资产合并；
//   - prompts/get：按 instance 归属过滤，命中下游原语返回文本内容，未知 → 明确报错；
//   - resources/list + resources/read：下游资源与注册资源合并，按 uri 读取内容。
package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// primProvider 是实现了 primitiveSource 的下游 provider 测试替身（无工具，仅原语）。
type primProvider struct {
	id              string
	prompts         []*mcp.Prompt
	promptContent   map[string]string
	resources       []*mcp.Resource
	resourceContent map[string]string
}

func (p *primProvider) Name() string { return "prim:" + p.id }

func (p *primProvider) ListTools(context.Context) ([]*mcp.Tool, error) { return nil, nil }

func (p *primProvider) Call(context.Context, string, map[string]any) (*mcp.CallToolResult, error) {
	return nil, fmt.Errorf("prim provider has no tools")
}

func (p *primProvider) Invalidate(context.Context) (bool, error) { return true, nil }

func (p *primProvider) Terminate(context.Context, string, string) (bool, error) { return true, nil }

func (p *primProvider) Close() error { return nil }

func (p *primProvider) ListPrompts(context.Context) ([]*mcp.Prompt, error) { return p.prompts, nil }

func (p *primProvider) GetPrompt(_ context.Context, name string) (*mcp.GetPromptResult, error) {
	content, ok := p.promptContent[name]
	if !ok {
		return nil, fmt.Errorf("prompt %q not found", name)
	}
	return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
		{Role: "user", Content: &mcp.TextContent{Text: content}},
	}}, nil
}

func (p *primProvider) ListResources(context.Context) ([]*mcp.Resource, error) {
	return p.resources, nil
}

func (p *primProvider) ReadResource(_ context.Context, uri string) (*mcp.ReadResourceResult, error) {
	content, ok := p.resourceContent[uri]
	if !ok {
		return nil, fmt.Errorf("resource %q not found", uri)
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
		{URI: uri, MIMEType: "text/plain", Text: content},
	}}, nil
}

// addPrimProvider 把原语 provider 注册进 registry（scope = "" 全局 / instance id 归属域）。
func addPrimProvider(t *testing.T, g *Gateway, p *primProvider, scope string) {
	t.Helper()
	ps := &providerState{
		key:    provKey("proxied", scope, p.id),
		entry:  &ServerEntry{ID: p.id, Name: p.id, Enabled: true, Origin: OriginBuiltin},
		prov:   p,
		status: "connected",
		cb:     newBreaker(1, 30*time.Second, false),
		scope:  scope,
	}
	if err := g.reg.registerProvider(ps, nil, ""); err != nil {
		t.Fatalf("register prim provider: %v", err)
	}
}

// primGateway 建一个不含总线订阅的 gateway（直接调用 handler 白盒断言），并注册原语 provider。
func primGateway(t *testing.T, p *primProvider, scope string) *Gateway {
	t.Helper()
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
	addPrimProvider(t, g, p, scope)
	return g
}

// jbReg 构造 register/get 载荷 JSON（测试内联）。
func jbReg(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// promptsOf 取 prompts/list 结果的条目列表。
func promptsOf(t *testing.T, v *call) []map[string]any {
	t.Helper()
	m, ok := v.Result.(map[string]any)
	if !ok {
		t.Fatalf("result 类型 = %T", v.Result)
	}
	arr, _ := m["prompts"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		if e, ok := it.(map[string]any); ok {
			out = append(out, e)
		}
	}
	return out
}

// findEntry 在条目列表里按 name 查找。
func findEntry(list []map[string]any, name string) map[string]any {
	for _, e := range list {
		if n, _ := e["name"].(string); n == name {
			return e
		}
	}
	return nil
}

// TestPromptsListMergesProviderAndRegisteredAssets：下游原语（prompt / skill）+ 注册 skill 资产
// 均出现在 prompts/list，且带 type / scope / node。
func TestPromptsListMergesProviderAndRegisteredAssets(t *testing.T) {
	p := &primProvider{
		id: "kb",
		prompts: []*mcp.Prompt{
			{Name: "kb_prompt", Description: "知识库提示"},
			{Name: "kb_skill", Description: "知识库技能", Meta: mcp.Meta{"type": "skill"}},
		},
	}
	g := primGateway(t, p, "ins-kb")
	if err := g.handleRegByMethod("prompts/register", &call{req: jbReg(map[string]any{
		"name": "reg_skill", "description": "注册技能", "asset_kind": "skill", "content": "c",
	})}); err != nil {
		t.Fatalf("prompts/register(skill): %v", err)
	}

	v := &call{req: jbReg(map[string]any{})}
	if err := g.handlePromptsList(v); err != nil {
		t.Fatalf("prompts/list: %v", err)
	}
	list := promptsOf(t, v)

	if e := findEntry(list, "kb_prompt"); e == nil || e["type"] != "prompt" || e["node"] != "kb" || e["scope"] != "ins-kb" {
		t.Fatalf("下游 prompt 条目缺失/字段错: %+v", e)
	}
	if e := findEntry(list, "kb_skill"); e == nil || e["type"] != "skill" {
		t.Fatalf("下游 skill 原语 type 应取 _meta.type: %+v", e)
	}
	if e := findEntry(list, "reg_skill"); e == nil || e["type"] != "skill" {
		t.Fatalf("注册资产应并入 prompts/list: %+v", e)
	}
}

// TestPromptsGetReadsProviderPrimitive：prompts/get 按下游原语实时读取；instance 归属过滤；
// 未知 name → 明确报错。
func TestPromptsGetReadsProviderPrimitive(t *testing.T) {
	p := &primProvider{
		id:            "kb",
		prompts:       []*mcp.Prompt{{Name: "kb_skill", Meta: mcp.Meta{"type": "skill"}}},
		promptContent: map[string]string{"kb_skill": "节点技能内容"},
	}
	g := primGateway(t, p, "ins-kb")

	v := &call{req: jbReg(map[string]any{"name": "kb_skill", "instance_id": "ins-kb"})}
	if err := g.handlePromptsGet(v); err != nil {
		t.Fatalf("prompts/get(kb_skill): %v", err)
	}
	m, _ := v.Result.(map[string]any)
	if s, _ := m["content"].(string); s != "节点技能内容" {
		t.Fatalf("prompts/get = %q，want 节点技能内容", s)
	}

	// 跨 instance 不可见（scoped 原语）
	if err := g.handlePromptsGet(&call{req: jbReg(map[string]any{"name": "kb_skill", "instance_id": "ins-other"})}); err == nil {
		t.Fatal("跨 instance 不应命中 scoped 原语")
	}
	// 未知 name 明确报错
	if err := g.handlePromptsGet(&call{req: jbReg(map[string]any{"name": "missing"})}); err == nil {
		t.Fatal("未知 prompt 应报错")
	}
	// name 必填
	if err := g.handlePromptsGet(&call{req: jbReg(map[string]any{})}); err == nil {
		t.Fatal("name 缺省应报错")
	}
}

// TestResourcesListMergesProviderAndRegisteredAssets + read：下游资源与注册资源合并，
// 按 uri 读取返回 content / mimeType。
func TestResourcesListMergesProviderAndRegisteredAssets(t *testing.T) {
	p := &primProvider{
		id:              "kb",
		resources:       []*mcp.Resource{{URI: "file://kb_res", Name: "kb_res", MIMEType: "text/plain"}},
		resourceContent: map[string]string{"file://kb_res": "r1"},
	}
	g := primGateway(t, p, "")
	if err := g.handleRegByMethod("resources/register", &call{req: jbReg(map[string]any{
		"name": "reg_res", "uri": "file://reg_res", "mimetype": "text/plain", "content": "r0",
	})}); err != nil {
		t.Fatalf("resources/register: %v", err)
	}

	v := &call{req: jbReg(map[string]any{})}
	if err := g.handleResourcesList(v); err != nil {
		t.Fatalf("resources/list: %v", err)
	}
	m, _ := v.Result.(map[string]any)
	arr, _ := m["resources"].([]any)
	if len(arr) != 2 {
		t.Fatalf("resources/list 应含下游 + 注册资源，got %d: %+v", len(arr), arr)
	}

	rv := &call{req: jbReg(map[string]any{"uri": "file://kb_res"})}
	if err := g.handleResourcesRead(rv); err != nil {
		t.Fatalf("resources/read(kb_res): %v", err)
	}
	rm, _ := rv.Result.(map[string]any)
	if rm["content"] != "r1" || rm["mimeType"] != "text/plain" {
		t.Fatalf("resources/read 结果 = %+v", rm)
	}

	if err := g.handleResourcesRead(&call{req: jbReg(map[string]any{"uri": "file://missing"})}); err == nil {
		t.Fatal("未知 uri 应报错")
	}
	if err := g.handleResourcesRead(&call{req: jbReg(map[string]any{})}); err == nil {
		t.Fatal("uri 缺省应报错")
	}
}
