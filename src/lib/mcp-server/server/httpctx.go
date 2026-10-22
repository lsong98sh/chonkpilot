// HTTP 调用上下文（core-mcp-server 2026-10-19）：把 HTTP 头里的实例/会话/轮次/call_id/
// 工作目录/scope/截止时间带进 tools/call handler，再与协议 _meta 合并后交给执行器 spawn。
//
// 设计要点：
//   - **头是首选来源**（同一进程内 gateway 每个请求注入），_meta 作为兼容回落；
//   - 头与 _meta 都为空 → Present=false（外部直连客户端不强制 instance）；
//   - deadline 只在本进程内派生 ctx 超时；真正 kill executor 仍由 callTool 的 ctx 取消驱动。
package server

import (
	"context"
	"strconv"
	"strings"
)

// HTTPCallContext 是 core-mcp-server HTTP 前门解析出的调用上下文。
type HTTPCallContext struct {
	InstanceID string
	SessionID  string
	TurnID     string
	CallID     string
	WorkDir    string
	DataDir    string
	Purpose    string
	Scope      string
	DeadlineMS int64
	NoTimeout  bool
}

type httpCallContextKey struct{}

// WithHTTPCallContext 把 HTTP 调用上下文放进请求 ctx（HTTP 中间件使用）。
func WithHTTPCallContext(ctx context.Context, h HTTPCallContext) context.Context {
	return context.WithValue(ctx, httpCallContextKey{}, h)
}

// HTTPCallContextFrom 从请求 ctx 取 HTTP 调用上下文（无 → 零值）。
func HTTPCallContextFrom(ctx context.Context) HTTPCallContext {
	if ctx == nil {
		return HTTPCallContext{}
	}
	h, _ := ctx.Value(httpCallContextKey{}).(HTTPCallContext)
	return h
}

// CallContextFromMeta 导出 callContextFromMeta（进程外装配方读取协议 _meta）。
func CallContextFromMeta(meta map[string]any) CallContext {
	return callContextFromMeta(meta)
}

// MergeCallContext 以 HTTP 头覆盖/补齐 _meta 上下文；两者都空 → Present=false。
// 头的 work_dir/data_dir 只做缺省补齐，显式 _meta 优先（gateway 已按实例解析过）。
func MergeCallContext(base CallContext, h HTTPCallContext) CallContext {
	out := base
	if out.InstanceID == "" {
		out.InstanceID = h.InstanceID
	}
	if out.WorkDir == "" {
		out.WorkDir = h.WorkDir
	}
	if out.DataDir == "" {
		out.DataDir = h.DataDir
	}
	if out.Purpose == "" {
		out.Purpose = h.Purpose
	}
	if out.SessionID == "" {
		out.SessionID = h.SessionID
	}
	if out.TurnID == "" {
		out.TurnID = h.TurnID
	}
	if out.ToolCallID == "" {
		out.ToolCallID = h.CallID
	}
	out.Present = out.Present || out.InstanceID != "" || out.WorkDir != "" || out.DataDir != "" || out.Purpose != "" ||
		out.SessionID != "" || out.TurnID != "" || out.ToolCallID != ""
	return out
}

// DeadlineFromHeader 解析 `X-Chonk-Deadline-Ms`（毫秒；<=0 或非法 → 0 = 不设超时）。
func DeadlineFromHeader(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ValidateCallContext 校验合并后的上下文（存在但 instance 为空 → 错误）。
func ValidateCallContext(cx CallContext) error {
	return cx.err()
}
