package facade

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// extParams / extResult：扩展方法（2026-09-06 裸方法名 tools/register、servers/list 等；
// 官方 SDK AddReceivingCustomMethod 仅拒绝与标准方法同名——扩展名全部非标准）的通用入参/回包。
// 入参字段与 msg-ref §5 payload 对齐（按需取用）；回包统一 {data: <网关应答>}。
type extParams struct {
	mcp.ParamsBase
	Name           string          `json:"name,omitempty"`
	URL            string          `json:"url,omitempty"`
	Transport      string          `json:"transport,omitempty"`
	Namespace      string          `json:"namespace,omitempty"`
	Description    string          `json:"description,omitempty"`
	Server         string          `json:"server,omitempty"`
	Schema         string          `json:"schema,omitempty"`
	HandlerSubject string          `json:"handler_subject,omitempty"`
	Owner          string          `json:"owner,omitempty"`
	Hot            bool            `json:"hot,omitempty"`
	AssetKind      string          `json:"asset_kind,omitempty"`
	URI            string          `json:"uri,omitempty"`
	MIMEType       string          `json:"mimetype,omitempty"`
	Content        string          `json:"content,omitempty"`
	Arguments      json.RawMessage `json:"arguments,omitempty"`
	Scope          string          `json:"scope,omitempty"`
	MCPServer      json.RawMessage `json:"mcp_server,omitempty"`
}

type extResult struct {
	mcp.ResultBase
	Data any `json:"data,omitempty"`
}

// extMethod 扩展方法 → 内部方法面映射（ext == bus == 裸方法名；内部经 gwSubject 转
// mcp-<组>-<动作> 主题）。标准方法（tools/list、prompts/get 等）由官方 SDK 直供，不在本表。
type extMethod struct {
	ext  string // 对外 JSON-RPC 方法（裸方法名）
	body func(p *extParams) map[string]any
}

func extPayloads() []extMethod {
	byServer := func(p *extParams) map[string]any { return map[string]any{"server": p.Server} }
	reg := func(p *extParams) map[string]any {
		body := map[string]any{
			"name":            p.Name,
			"url":             p.URL,
			"transport":       p.Transport,
			"namespace":       p.Namespace,
			"description":     p.Description,
			"schema":          p.Schema,
			"handler_subject": p.HandlerSubject,
			"owner":           p.Owner,
			"hot":             p.Hot,
			"asset_kind":      p.AssetKind,
			"uri":             p.URI,
			"mimetype":        p.MIMEType,
			"content":         p.Content,
			"scope":           p.Scope,
		}
		if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
			var args any
			if err := json.Unmarshal(p.Arguments, &args); err == nil {
				body["arguments"] = args
			}
		}
		if len(p.MCPServer) > 0 && string(p.MCPServer) != "null" {
			var spec any
			if err := json.Unmarshal(p.MCPServer, &spec); err == nil {
				body["mcp_server"] = spec
			}
		}
		return body
	}
	none := func(_ *extParams) map[string]any { return map[string]any{} }
	return []extMethod{
		// 分组注册/注销（2026-09-06 取代 mcp.register/unregister 统一 kind）
		{ext: "tools/register", body: reg},
		{ext: "tools/unregister", body: reg},
		{ext: "prompts/register", body: reg},
		{ext: "prompts/unregister", body: reg},
		{ext: "resources/register", body: reg},
		{ext: "resources/unregister", body: reg},
		{ext: "servers/register", body: reg},
		{ext: "servers/unregister", body: reg},
		// 接入视图
		{ext: "servers/list", body: byServer},
		{ext: "servers/get", body: byServer},
		// 异步任务（tasks/list|status|result|cancel）已于 2026-09-18 移除（用户决定）：
		// 取消/转后台归任务层 + 进程内 sink；状态/结果以任务层 + message 表为准。
		// 网关管理（始终注册）
		{ext: "gateway/check", body: none},
		{ext: "gateway/reload", body: none},
	}
}

func (a *Adapter) registerExtMethods() {
	for _, m := range extPayloads() {
		m := m
		_ = mcp.AddReceivingCustomMethod[*extParams, *extResult](a.srv, m.ext,
			func(ctx context.Context, _ *mcp.ServerSession, p *extParams) (*extResult, error) {
				res, err := a.busCall(ctx, m.ext, m.body(p))
				if err != nil {
					return nil, err
				}
				return &extResult{Data: res}, nil
			})
	}
}
