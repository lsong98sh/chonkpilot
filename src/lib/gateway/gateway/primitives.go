// 原语面（prompts / skills / resources）聚合（GATEWAY-2 L5，2026-10-21）。
//
// L5 之前由 gateway 自持「能力源节点」（self = 进程内 go-sdk server；dir = 契约目录扫描 +
// 进程内内存传输会话）提供原语 —— 那是 gateway 持有能力源，已删除。
// 现在原语面改为**标准 MCP client 面聚合**：遍历已接入下游（core-mcp-server 的 http 端点、
// capability 目录节点的 stdio server 等）的 list/get/read，按 scope/node 标注返回，
// gateway 只做协议层转发，不再持有任何契约扫描或 in-process 会话。
package mcpgateway

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// primitiveSource 是下游原语面（可选能力；provider 未实现 = 无 prompts/resources）。
// 由 sdkConn（官方 client 会话）实现，proxyProvider 透传到共享槽连接。
type primitiveSource interface {
	ListPrompts(ctx context.Context) ([]*mcp.Prompt, error)
	GetPrompt(ctx context.Context, name string) (*mcp.GetPromptResult, error)
	ListResources(ctx context.Context) ([]*mcp.Resource, error)
	ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)
}

// primitiveTimeout 是单次下游原语拉取上限（原语面非执行面：失败/超时只跳过该节点）。
const primitiveTimeout = 5 * time.Second

// eachPrimitive 遍历原语来源：all=true 全量（UI 视图）；否则仅 global ∪ 归属该 instance
// （find/load 的 instance 隔离，与改前 eachMemNode 口径一致）。
func (g *Gateway) eachPrimitive(instance string, all bool, fn func(nodeName, scope string, src primitiveSource)) {
	type item struct {
		node  string
		scope string
		src   primitiveSource
	}
	var items []item
	for _, key := range g.reg.providerKeys() {
		ps, ok := g.reg.provider(key)
		if !ok || ps == nil || ps.status != "connected" || ps.prov == nil {
			continue
		}
		src, ok := ps.prov.(primitiveSource)
		if !ok {
			continue
		}
		if !all && ps.scope != scopeGlobal && ps.scope != instance {
			continue
		}
		name := key
		if ps.entry != nil && ps.entry.ID != "" {
			name = ps.entry.ID
		}
		items = append(items, item{node: name, scope: ps.scope, src: src})
	}
	for _, it := range items {
		fn(it.node, it.scope, it.src)
	}
}

// handlePromptsList 返回全量 prompts（下游节点原语 + 注册 prompt/skill 资产），每项带 type
// （prompt/skill）+ scope + node；不带参数过滤——UI 按需分流。
// 25 §5/T2（2026-09-25）：**agent 已撤出资产面** → 本表不再列 agent。
func (g *Gateway) handlePromptsList(v *call) error {
	list := make([]any, 0, 32)
	g.eachPrimitive("", true, func(nodeName, scope string, src primitiveSource) {
		ctx, cancel := context.WithTimeout(context.Background(), primitiveTimeout)
		ps, err := src.ListPrompts(ctx)
		cancel()
		if err != nil {
			return // 单节点失败不阻塞
		}
		for _, p := range ps {
			typ := "prompt"
			if p.Meta != nil {
				if tv, ok := p.Meta["type"].(string); ok && tv != "" {
					typ = tv
				}
			}
			entry := map[string]any{
				"name": p.Name, "description": p.Description,
				"type": typ, "scope": scope, "node": nodeName,
			}
			if len(p.Arguments) > 0 {
				args := make([]map[string]any, 0, len(p.Arguments))
				for _, a := range p.Arguments {
					args = append(args, map[string]any{
						"name":        a.Name,
						"description": a.Description,
						"required":    a.Required,
					})
				}
				entry["arguments"] = args
			}
			list = append(list, entry)
		}
	})
	// 注册资产并入（prompt/skill，全量 + scope/node）；agent 已撤出资产面 → 不再列举。
	for _, a := range g.reg.allAssets() {
		if a.Kind != KindPrompt && a.Kind != KindSkill {
			continue
		}
		entry := map[string]any{
			"name": a.Name, "description": a.Description,
			"type": a.Kind, "scope": a.Scope,
		}
		if a.Node != "" {
			entry["node"] = a.Node
		}
		if len(a.Arguments) > 0 {
			entry["arguments"] = a.Arguments
		}
		list = append(list, entry)
	}
	v.Result = map[string]any{"prompts": list}
	return nil
}

// handlePromptsGet 返回单个 prompt/skill 详细内容（下游节点原语实时读取）。
// 25 §5/T2（2026-09-25）：**agent 已撤出资产面** —— 不可经此读取。
func (g *Gateway) handlePromptsGet(v *call) error {
	var req struct {
		Context
		Name string `json:"name"`
	}
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	if req.Name == "" {
		return methodError(-32602, "name is required")
	}
	found := false
	g.eachPrimitive(req.InstanceID, false, func(_, _ string, src primitiveSource) {
		if found {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), primitiveTimeout)
		res, err := src.GetPrompt(ctx, req.Name)
		cancel()
		if err != nil || len(res.Messages) == 0 {
			return
		}
		if tc, ok := res.Messages[0].Content.(*mcp.TextContent); ok {
			found = true
			v.Result = map[string]any{
				"name":        req.Name,
				"description": "",
				"content":     tc.Text,
				"type":        "prompt",
			}
		}
	})
	if !found {
		return methodError(-32602, "prompt %q not found", req.Name)
	}
	return nil
}

// handleResourcesList 返回全量 resources（下游节点原语 + 注册 resource 资产），带 scope/node。
func (g *Gateway) handleResourcesList(v *call) error {
	list := make([]any, 0, 32)
	g.eachPrimitive("", true, func(nodeName, scope string, src primitiveSource) {
		ctx, cancel := context.WithTimeout(context.Background(), primitiveTimeout)
		rs, err := src.ListResources(ctx)
		cancel()
		if err != nil {
			return
		}
		for _, r := range rs {
			list = append(list, map[string]any{
				"uri":         r.URI,
				"name":        r.Name,
				"description": r.Description,
				"mimeType":    r.MIMEType,
				"scope":       scope,
				"node":        nodeName,
			})
		}
	})
	for _, a := range g.reg.allAssets() {
		if a.Kind != KindResource {
			continue
		}
		entry := map[string]any{
			"uri": a.URI, "name": a.Name, "description": a.Description,
			"mimeType": a.MIMEType, "scope": a.Scope,
		}
		if a.Node != "" {
			entry["node"] = a.Node
		}
		list = append(list, entry)
	}
	v.Result = map[string]any{"resources": list}
	return nil
}

// handleResourcesRead 返回单个 resource 详细内容（下游节点原语按 uri 读取）。
func (g *Gateway) handleResourcesRead(v *call) error {
	var req struct {
		Context
		URI string `json:"uri"`
	}
	if err := v.JSON(&req); err != nil {
		return methodError(-32700, "parse error: %s", err.Error())
	}
	if req.URI == "" {
		return methodError(-32602, "uri is required")
	}
	var hit *mcp.ResourceContents
	g.eachPrimitive(req.InstanceID, false, func(_, _ string, src primitiveSource) {
		if hit != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), primitiveTimeout)
		res, err := src.ReadResource(ctx, req.URI)
		cancel()
		if err != nil || len(res.Contents) == 0 {
			return
		}
		hit = res.Contents[0]
	})
	if hit == nil {
		return methodError(-32602, "resource %q not found", req.URI)
	}
	v.Result = map[string]any{
		"uri":         hit.URI,
		"name":        hit.URI,
		"description": "",
		"mimeType":    hit.MIMEType,
		"content":     hit.Text,
	}
	return nil
}
