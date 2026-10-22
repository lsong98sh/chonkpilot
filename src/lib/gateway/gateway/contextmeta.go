// 调用上下文 ↔ 协议 `_meta` 映射（原 memnode.go 的遗留助手，GATEWAY-2 L5 纯化后 memNode
// 已删除，但下游调用仍需要把执行 ctx 的调用上下文以 `_meta` 透传）。
package mcpgateway

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callContextMeta 从执行 ctx 提取调用上下文 → 协议 _meta（命名空间 chonkpilot，对齐 25-mcp-server
// CallContextMeta；无任何上下文字段 → nil，不携带）。instance 存在但为空 → 上游（mcp-server）报异常。
func callContextMeta(ctx context.Context) mcp.Meta {
	ns := map[string]any{}
	if v := InstanceFromContext(ctx); v != "" {
		ns["instance_id"] = v
	}
	if v := WorkDirFromContext(ctx); v != "" {
		ns["work_dir"] = v
	}
	if v := DataDirFromContext(ctx); v != "" {
		ns["data_dir"] = v
	}
	if v := SessionFromContext(ctx); v != "" {
		ns["session"] = v
	}
	if v := TurnFromContext(ctx); v != "" {
		ns["turn"] = v
	}
	if v := ToolCallIDFromContext(ctx); v != "" {
		ns["tool_call_id"] = v
	}
	// 任务树归属（I-90 增补，随 _meta 透传）：供域工具（如 dsl_run）把作业节点挂到正确的主会话 /
	// 父节点下（任务层落库定位）。mcp-server 侧解析未知键即忽略（向后兼容）。
	if v := TopSessionFromContext(ctx); v != "" {
		ns["top_session"] = v
	}
	if v := ParentFromContext(ctx); v != "" {
		ns["parent"] = v
	}
	// C1（2026-10-15）：本次调用的运行目的 → 透传给内嵌 mcp-server（其 executorEnv 落
	// CHONKPILOT_PURPOSE 供 executor 进程读取）。
	if v := PurposeFromContext(ctx); v != "" {
		ns["purpose"] = v
	}
	if len(ns) == 0 {
		return nil
	}
	return mcp.Meta{"chonkpilot": ns}
}

// execCtxFromMeta 从 tools/call 请求的调用上下文 _meta 重建执行 ctx。
func execCtxFromMeta(ctx context.Context, meta mcp.Meta) context.Context {
	if meta == nil {
		return ctx
	}
	ns, ok := meta["chonkpilot"].(map[string]any)
	if !ok {
		return ctx
	}
	c := Context{}
	if s, ok := ns["session"].(string); ok {
		c.Session = s
	}
	if s, ok := ns["turn"].(string); ok {
		c.Turn = s
	}
	if s, ok := ns["instance_id"].(string); ok {
		c.InstanceID = s
	}
	if s, ok := ns["tool_call_id"].(string); ok {
		c.ToolCallID = s
	}
	if s, ok := ns["work_dir"].(string); ok {
		c.WorkDir = s
	}
	if s, ok := ns["data_dir"].(string); ok {
		c.DataDir = s
	}
	if s, ok := ns["top_session"].(string); ok {
		c.TopSession = s
	}
	if s, ok := ns["parent"].(string); ok {
		c.Parent = s
	}
	if s, ok := ns["purpose"].(string); ok {
		c.Purpose = s
	}
	return withTurnContext(ctx, c)
}
