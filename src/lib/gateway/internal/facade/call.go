package facade

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// handleCall：tools/call → Bus tool-call（同主题 promise）→ 结果回包。
func (a *Adapter) handleCall(ctx context.Context, name string, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if req.Params.Arguments != nil {
		b, err := json.Marshal(req.Params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("marshal arguments: %w", err)
		}
		if len(b) > 0 && string(b) != "null" {
			if err := json.Unmarshal(b, &args); err != nil {
				return nil, fmt.Errorf("arguments must be an object: %w", err)
			}
		}
	}
	res, err := a.busCall(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	return buildCallResult(res), nil
}

// buildCallResult 把 gateway tools/call 应答 map 转官方 CallToolResult。
func buildCallResult(res map[string]any) *mcp.CallToolResult {
	out := &mcp.CallToolResult{Content: []mcp.Content{}}
	if isErr, _ := res["isError"].(bool); isErr {
		out.IsError = true
	}
	if sc, ok := res["structuredContent"]; ok && sc != nil {
		out.StructuredContent = sc
	}
	items, _ := res["content"].([]any)
	if len(items) == 0 && out.StructuredContent != nil {
		// 无内容块但带结构化结果 → 文本兜底
		b, _ := json.Marshal(out.StructuredContent)
		out.Content = append(out.Content, &mcp.TextContent{Text: string(b)})
		return out
	}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch m["type"] {
		case "text":
			text, _ := m["text"].(string)
			out.Content = append(out.Content, &mcp.TextContent{Text: text})
		case "image":
			data, _ := m["data"].(string)
			mime, _ := m["mimeType"].(string)
			out.Content = append(out.Content, &mcp.ImageContent{Data: []byte(data), MIMEType: mime})
		default:
			b, _ := json.Marshal(item)
			out.Content = append(out.Content, &mcp.TextContent{Text: string(b)})
		}
	}
	return out
}

// handlePromptGet：prompts/get → Bus prompt-get（同主题 promise）→ 结果回包。
func (a *Adapter) handlePromptGet(ctx context.Context, name string, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	res, err := a.busCall(ctx, "prompts/get", map[string]any{"name": name})
	if err != nil {
		return nil, err
	}
	content, _ := res["content"].(string)
	desc, _ := res["description"].(string)
	return &mcp.GetPromptResult{
		Description: desc,
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: content}},
		},
	}, nil
}

// handleResourceRead：resources/read → Bus resource-read（同主题 promise）→ 结果回包。
func (a *Adapter) handleResourceRead(ctx context.Context, uri string, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	res, err := a.busCall(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return nil, err
	}
	content, _ := res["content"].(string)
	mime, _ := res["mimeType"].(string)
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{URI: uri, MIMEType: mime, Text: content},
		},
	}, nil
}
