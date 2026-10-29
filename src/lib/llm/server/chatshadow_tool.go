// chat 工具端口宿主适配器（MOD-1 M5b-②）：ToolCatalog。
//
// 与 `chatshadow.go`（LLMClient）同属宿主装配层：策略类判断（白名单 / hot / denyTools）仍归
// 宿主，通过 list 钩子注入；chat 内核只拿到「已过滤的下发面」。
package server

import (
	"context"
	"encoding/json"

	"github.com/lsong98sh/chonkpilot-agent/chat"
)

// ─── ToolCatalog ─────────────────────────────────────────────────────

// chatToolCatalog 把宿主工具目录接到 `chat.ToolCatalog`。
//
// `list(instanceID)` 必须返回**宿主已过滤后的下发面**（hot 过滤 / agent 白名单 /
// denyTools 与生产 `turnCtx.llmTools` 同口径）；chat 不认识这些策略。
type chatToolCatalog struct {
	s    *Server
	list func(instanceID string) []ToolDef
}

// newChatToolCatalog 构造目录适配器（list 为 nil → 空目录）。
func newChatToolCatalog(s *Server, list func(instanceID string) []ToolDef) chat.ToolCatalog {
	return &chatToolCatalog{s: s, list: list}
}

// Servers 返回服务目录视图。MCP 服务列表是管理面 / UI 关注点，不在 chat 端口的
// 会话语义内（chat 只用 Tools），故恒空——不是遗漏。
func (c *chatToolCatalog) Servers(context.Context, string) ([]chat.ServerInfo, error) {
	return nil, nil
}

// Tools 把 ToolDef 转成 chat.ToolSpec（Parameters → InputSchema JSON 原文）。
//
// LLM-P4：`d.ParamJSON`（tools/list 刷新时预序列化）非空则直接复用 —— 原实现每轮组装
// 都把每个工具的 schema map 重新 marshal 一次；现仅在缓存缺失（域工具等程序内构造）时兜底。
func (c *chatToolCatalog) Tools(_ context.Context, instanceID string) ([]chat.ToolSpec, error) {
	if c == nil || c.list == nil {
		return nil, nil
	}
	defs := c.list(instanceID)
	out := make([]chat.ToolSpec, 0, len(defs))
	for _, d := range defs {
		spec := chat.ToolSpec{Name: d.Name, Description: d.Description, Meta: d.Meta}
		if d.ParamJSON != nil {
			spec.InputSchema = d.ParamJSON
		} else if d.Parameters != nil {
			if b, err := json.Marshal(d.Parameters); err == nil {
				spec.InputSchema = b
			}
		}
		out = append(out, spec)
	}
	return out, nil
}

// 编译期断言：工具目录适配器。
var _ chat.ToolCatalog = (*chatToolCatalog)(nil)
