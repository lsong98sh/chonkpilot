package server

import (
	"context"
	"time"
)

// RegisterPluginServer 把一个进程外插件自带的 Streamable HTTP MCP server 登记到 gateway。
//
// 与 RegisterCoreServer 的区别：name / namespace / hot_tools / origin 全部由插件 manifest
// 决定；插件进程由 PluginManager 独占管理，因此 spec 里**不带 runtime**（禁止 gateway 再 spawn）。
func (s *Server) RegisterPluginServer(ctx context.Context, name, url, token, namespace string, hotTools []string, category, origin string, timeoutSec int) error {
	if s == nil || s.gw == nil {
		return nil
	}
	mcp := map[string]any{
		"url":       url,
		"transport": "http",
		"headers":   map[string]string{"Authorization": "Bearer " + token},
	}
	if namespace != "" {
		mcp["namespace"] = namespace
	}
	if len(hotTools) > 0 {
		mcp["hot_tools"] = hotTools
	}
	if category != "" {
		mcp["category"] = category
	}
	if origin != "" {
		mcp["origin"] = origin
	}
	if timeoutSec > 0 {
		mcp["timeout"] = timeoutSec
	}
	_, err := s.emitGateway(ctx, "servers/register", map[string]any{
		"name":       name,
		"mcp_server": mcp,
	})
	return err
}

// UnregisterPluginServer 注销插件 MCP（幂等；失败忽略）。
func (s *Server) UnregisterPluginServer(name string) {
	if s == nil || s.gw == nil || name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.emitGateway(ctx, "servers/unregister", map[string]any{"name": name})
}
