// 可复用执行 API（P6 2026-10-19）：把「契约扫描 → mcp.Tool 定义 + executor handler」的
// 同一份实现暴露给**进程外**装配方（如 chonkpilot-self-tools），使系统级 app 根的工具
// 不再由宿主进程内注册执行。
//
// 与 RegisterContracts 共用 loadTools / buildTool / makeToolHandler：契约解析、用户级异步
// 覆盖、并发限流、参数解析、_meta 调用上下文、spawn executor 语义**完全一致**，不产生第二套口径。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExecTool 是一个可由外部进程执行的契约工具。
// Call 执行一次工具：args = 已解析参数（由调用方从协议载荷解析）；cx = 调用上下文
// （instance / work_dir / data_dir / purpose / session / turn / call_id / top_session / parent），
// 经 _meta 传给 handler → spawn executor 环境。
type ExecTool struct {
	Name string
	Tool *mcp.Tool
	Doc  *ToolDoc
	// Call 执行一次工具（args = 已解析参数；cx = 调用上下文 instance/work_dir/data_dir/purpose）。
	Call func(ctx context.Context, args map[string]any, cx CallContext) (*mcp.CallToolResult, error)
}

// LoadExecTools 扫描 root 的 *.tool.md（跳过 category=server），返回可执行工具（按名称排序）。
// cfg=nil → DefaultConfig()；limiter 每个进程共享一个（同一 cfg 上的 RegisterContracts /
// LoadExecTools 复用同一并发限流器）。
func LoadExecTools(root string, cfg *Config) ([]*ExecTool, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 契约根缺失：空工具集（与 RegisterContracts 的 no-op 口径一致）
		}
		return nil, err
	}
	docs, err := loadTools(root)
	if err != nil {
		return nil, fmt.Errorf("load tools: %w", err)
	}
	lim := cfg.ensureLimiter()
	out := make([]*ExecTool, 0, len(docs))
	for _, td := range docs {
		// category=server 且无 runtime：无 executor 可执行（当前无此类契约；dsl_run 已有 runtime）。
		if isServerTool(td) {
			continue
		}
		t, err := buildTool(td, cfg)
		if err != nil {
			return nil, fmt.Errorf("build tool %s: %w", td.Name, err)
		}
		handler := makeToolHandler(td, cfg, lim)
		out = append(out, &ExecTool{
			Name: td.Name,
			Tool: t,
			Doc:  td,
			Call: func(ctx context.Context, args map[string]any, cx CallContext) (*mcp.CallToolResult, error) {
				raw, err := json.Marshal(args)
				if err != nil {
					return nil, fmt.Errorf("tool %s: marshal arguments: %w", td.Name, err)
				}
				// 调用上下文经 _meta 透传；全空 → 不带 _meta（与 memnode.callContextMeta 的
				// 「无上下文字段 → nil」口径一致，避免 Present=true 但 instance 空触发 ErrNoInstance）。
				var meta mcp.Meta
				if cx.Present || cx.InstanceID != "" || cx.WorkDir != "" || cx.DataDir != "" || cx.Purpose != "" ||
					cx.SessionID != "" || cx.TurnID != "" || cx.ToolCallID != "" || cx.TopSession != "" || cx.Parent != "" {
					meta = CallContextMetaTree(cx.InstanceID, cx.WorkDir, cx.DataDir, cx.Purpose,
						cx.SessionID, cx.TurnID, cx.ToolCallID, cx.TopSession, cx.Parent)
				}
				req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
					Name:      td.Name,
					Arguments: raw,
					Meta:      meta,
				}}
				return handler(ctx, req)
			},
		})
	}
	return out, nil
}
