package facade

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// gwSubject 映射方法名（tools/register、servers/list 等）→ 相对主题 mcp-<组>-<动作>
// （2026-09-06 统一 mcp 域；chonk. 前缀由总线注入）。规则对齐 gateway subjects.go：
//
//	tools/list → mcp-tools-list；gateway/check → mcp-gateway-check
func gwSubject(method string) string {
	return "mcp-" + strings.ReplaceAll(method, "/", "-")
}

// busCall 发方法调用并 await 同主题（promise）：返回 v.Result 的 JSON map 形态。
func (a *Adapter) busCall(ctx context.Context, method string, payload map[string]any) (map[string]any, error) {
	v := a.bus.Emit(ctx, gwSubject(method), payload).Wait()
	if err := v.Err(); err != nil {
		return nil, err
	}
	switch res := v.Result.(type) {
	case map[string]any:
		return res, nil
	case nil:
		return map[string]any{}, nil
	default:
		b, err := json.Marshal(v.Result)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return map[string]any{"value": v.Result}, nil
		}
		return m, nil
	}
}

// toolInfo 是 gateway tools/list 单工具（JSON map 扁平视图）。
type toolInfo struct {
	name        string
	description string
	schemaJSON  string // inputSchema 原文（JSON 文本，diff 用）
	schema      any    // inputSchema 解码对象
	metaHot     bool
}

// fetchTools 经 tool-list 拉取全量聚合工具。
func (a *Adapter) fetchTools(ctx context.Context) ([]toolInfo, error) {
	res, err := a.busCall(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	raw, _ := res["tools"].([]any)
	var out []toolInfo
	for _, tr := range raw {
		m, ok := tr.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := m["description"].(string)
		schema, ok := m["inputSchema"].(map[string]any)
		if !ok {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		sb, _ := json.Marshal(schema)
		hot := false
		if meta, ok := m["_meta"].(map[string]any); ok {
			if h, ok := meta["hot"].(bool); ok {
				hot = h
			}
		}
		out = append(out, toolInfo{name: name, description: desc, schemaJSON: string(sb), schema: schema, metaHot: hot})
	}
	return out, nil
}

// promptInfo 是 gateway prompts/list 单 prompt（JSON map 扁平视图）。
type promptInfo struct {
	name        string
	description string
}

// resourceInfo 是 gateway resources/list 单 resource（JSON map 扁平视图）。
type resourceInfo struct {
	uri         string
	name        string
	description string
	mimeType    string
}

// fetchPrompts 经 prompt-list 拉取全量 prompts。
func (a *Adapter) fetchPrompts(ctx context.Context) ([]promptInfo, error) {
	res, err := a.busCall(ctx, "prompts/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	raw, _ := res["prompts"].([]any)
	var out []promptInfo
	for _, pr := range raw {
		m, ok := pr.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := m["description"].(string)
		out = append(out, promptInfo{name: name, description: desc})
	}
	return out, nil
}

// fetchResources 经 resource-list 拉取全量 resources。
func (a *Adapter) fetchResources(ctx context.Context) ([]resourceInfo, error) {
	res, err := a.busCall(ctx, "resources/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	raw, _ := res["resources"].([]any)
	var out []resourceInfo
	for _, rr := range raw {
		m, ok := rr.(map[string]any)
		if !ok {
			continue
		}
		uri, _ := m["uri"].(string)
		if uri == "" {
			continue
		}
		name, _ := m["name"].(string)
		desc, _ := m["description"].(string)
		mime, _ := m["mimeType"].(string)
		out = append(out, resourceInfo{uri: uri, name: name, description: desc, mimeType: mime})
	}
	return out, nil
}

// reconcilePrompts diff 网关聚合 prompts 与已暴露集 → AddPrompt / RemovePrompts。
func (a *Adapter) reconcilePrompts(ctx context.Context) error {
	prompts, err := a.fetchPrompts(ctx)
	if err != nil {
		return fmt.Errorf("fetch prompts: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	next := make(map[string]promptInfo, len(prompts))
	for _, p := range prompts {
		next[p.name] = p
		if _, ok := a.prompts[p.name]; ok {
			continue // 已存在
		}
		prompt := &mcp.Prompt{
			Name:        p.name,
			Description: p.description,
		}
		a.srv.AddPrompt(prompt, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return a.handlePromptGet(ctx, p.name, req)
		})
	}
	// 移除已不再存在的 prompts
	for name := range a.prompts {
		if _, ok := next[name]; !ok {
			a.srv.RemovePrompts(name)
		}
	}
	a.prompts = next
	return nil
}

// reconcileResources diff 网关聚合 resources 与已暴露集 → AddResource / RemoveResources。
func (a *Adapter) reconcileResources(ctx context.Context) error {
	resources, err := a.fetchResources(ctx)
	if err != nil {
		return fmt.Errorf("fetch resources: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	next := make(map[string]resourceInfo, len(resources))
	for _, r := range resources {
		next[r.uri] = r
		if _, ok := a.resources[r.uri]; ok {
			continue // 已存在
		}
		resource := &mcp.Resource{
			URI:         r.uri,
			Name:        r.name,
			Description: r.description,
			MIMEType:    r.mimeType,
		}
		a.srv.AddResource(resource, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return a.handleResourceRead(ctx, r.uri, req)
		})
	}
	// 移除已不再存在的 resources
	for uri := range a.resources {
		if _, ok := next[uri]; !ok {
			a.srv.RemoveResources(uri)
		}
	}
	a.resources = next
	return nil
}

// reconcileTools diff 网关聚合工具集与已暴露集 → AddTool / RemoveTools。
// mcp-go 类型不进本层：只按 name 管理，AddTool 用低层 ToolHandler（schema 原文直通）。
func (a *Adapter) reconcileTools(ctx context.Context) error {
	tools, err := a.fetchTools(ctx)
	if err != nil {
		return fmt.Errorf("fetch tools: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	next := make(map[string]string, len(tools))
	for _, t := range tools {
		next[t.name] = t.schemaJSON
		if cur, ok := a.tools[t.name]; ok && cur == t.schemaJSON {
			continue // 已暴露且 schema 未变
		}
		a.srv.RemoveTools(t.name) // 新增或 schema 变化 → 先移除（未知名 no-op）再重注册
		tool := &mcp.Tool{
			Name:        t.name,
			Description: t.description,
			InputSchema: t.schema,
		}
		callName := t.name
		a.srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return a.handleCall(ctx, callName, req)
		})
	}
	// 移除已不再存在的工具
	for name := range a.tools {
		if _, ok := next[name]; !ok {
			a.srv.RemoveTools(name)
		}
	}
	a.tools = next
	return nil
}
