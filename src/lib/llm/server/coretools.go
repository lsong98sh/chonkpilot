// coretools.go —— core-mcp-server 的 gateway 接入（2026-10-19）。
//
// core-mcp-server 由宿主在插件桥就绪后拉起（常驻），本文件只负责把它经既有方法面
// `servers/register`（mcp-servers-register）登记为内置下游：
//   - transport=http（MCP Streamable HTTP，端点 /mcp）；
//   - namespace="-" → 工具名**原样**暴露（P0a 2026-10-23：不再加 `self_` 前缀）；
//   - origin=builtin + Authorization: Bearer <token>；
//   - 固定 URL/token（重启复用），故进程重启无需重新登记。
package server

import (
	"context"
	"time"

	mcpms "github.com/chonkpilot/chonkpilot-mcp-server/server"
	hook "github.com/lsong98sh/chonkpilot-plugin-framework/runtime/hook"
)

// CoreServerName 是 core-mcp-server 在 gateway 中的节点名（内置；namespace 固定 "-" 即原样）。
const CoreServerName = "core"

// SetCoreRuntimePusher 把 core-mcp-server 的运行期配置推送口接到本服务
// （入口层无需依赖 mcp-server 包；push 签名 = coretools.Supervisor.PushRuntime）。
func (s *Server) SetCoreRuntimePusher(push func(any) error) {
	if push == nil {
		s.SetCoreRuntimeSink(nil)
		return
	}
	s.SetCoreRuntimeSink(func(rc mcpms.RuntimeConfig) { _ = push(rc) })
}

// SetToolExec 注入 core-mcp-server 的内部执行面（插件工具委托执行）。
// nil = 断开（插件回落本地引擎）。宿主在 core 就绪后调用。
func (s *Server) SetToolExec(te hook.ToolExec) {
	if s == nil {
		return
	}
	s.toolExecMu.Lock()
	s.toolExec = te
	s.toolExecMu.Unlock()
}

// coreToolExec 返回当前内部执行面（插件经 Deps.ToolExec 每次调用时读取，支持晚绑定）。
func (s *Server) coreToolExec() hook.ToolExec {
	if s == nil {
		return nil
	}
	s.toolExecMu.RLock()
	defer s.toolExecMu.RUnlock()
	return s.toolExec
}

// RegisterCoreServer 登记 core-mcp-server 为内置 HTTP 下游（幂等失败由调用方记日志）。
func (s *Server) RegisterCoreServer(url, token string) error {
	if s == nil || s.gw == nil || url == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := s.emitGateway(ctx, "servers/register", map[string]any{
		"name":      CoreServerName,
		"namespace": "-",
		"mcp_server": map[string]any{
			"url":       url,
			"transport": "http",
			"headers": map[string]string{
				"Authorization": "Bearer " + token,
			},
			"origin":   "builtin",
			"category": "core",
		},
	})
	return err
}

// UnregisterCoreServer 注销 core-mcp-server（宿主收尾；失败忽略）。
func (s *Server) UnregisterCoreServer() {
	if s == nil || s.gw == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.emitGateway(ctx, "servers/unregister", map[string]any{"name": CoreServerName})
}
