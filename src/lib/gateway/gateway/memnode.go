// 官方 go-sdk server 的 in-memory 节点（2026-09-07 收敛：替代原 mcp-server inprocess 封装）。
// gateway 不自建 mcp-server 容器、不查进程内注册表——装配方把官方 go-sdk server 以参数传入
// （Params.MCPServer，capability 已 RegisterContracts），或 dir 目录节点（@mcp 等）由 gateway
// 自建官方 server + RegisterContracts 扫描；二者统一经官方 InMemoryTransports 建同进程 client
// 会话，全原语（tools/prompts/skills/resources）拉取与调用。
package mcpgateway

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// memNode 是官方 go-sdk server 的同进程 in-memory 节点。
type memNode struct {
	name   string
	ms     *mcp.Server
	cancel context.CancelFunc
	ss     *mcp.ServerSession
	cs     *mcp.ClientSession
}

// newMemNode 与官方 server 建立 in-memory 会话（服务端先连，客户端连接即 initialize 握手）。
func newMemNode(name string, ms *mcp.Server) (*memNode, error) {
	ctx, cancel := context.WithCancel(context.Background())
	srvTr, cliTr := mcp.NewInMemoryTransports()
	ss, err := ms.Connect(ctx, srvTr, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mem node %s server connect: %w", name, err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "chonkpilot-gateway-mem", Version: "1.0.0"}, nil)
	cs, err := c.Connect(ctx, cliTr, nil)
	if err != nil {
		_ = ss.Close()
		cancel()
		return nil, fmt.Errorf("mem node %s client connect: %w", name, err)
	}
	return &memNode{name: name, ms: ms, cancel: cancel, ss: ss, cs: cs}, nil
}

// ListTools 列出节点全量工具（官方分页默认 1000/页，跟随 cursor 收齐）。
func (n *memNode) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	cursor := ""
	for {
		res, err := n.cs.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// Call 调用节点工具（args 直通官方 CallToolParams；meta = 调用上下文 _meta，nil = 不携带）。
func (n *memNode) Call(ctx context.Context, tool string, args map[string]any, meta mcp.Meta) (*mcp.CallToolResult, error) {
	return n.cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args, Meta: meta})
}

// ListPrompts 列出节点 prompts（含 skill，_meta.type 区分）。
func (n *memNode) ListPrompts(ctx context.Context) ([]*mcp.Prompt, error) {
	var out []*mcp.Prompt
	cursor := ""
	for {
		res, err := n.cs.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Prompts...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// GetPrompt 取单个 prompt 渲染结果。
func (n *memNode) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	return n.cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
}

// ListResources 列出节点 resources。
func (n *memNode) ListResources(ctx context.Context) ([]*mcp.Resource, error) {
	var out []*mcp.Resource
	cursor := ""
	for {
		res, err := n.cs.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Resources...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// ReadResource 读取单个 resource。
func (n *memNode) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return n.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
}

// Close 关闭 client 会话与配对 server 会话（幂等）。
func (n *memNode) Close() {
	if n == nil {
		return
	}
	n.cancel()
	_ = n.cs.Close()
	_ = n.ss.Close()
}

// memNodeProvider 把 memNode 适配为 gateway 注册用 Provider（tools-only 聚合面）。
// Call 把执行 ctx 中的调用上下文（session/turn/instance/tool_call_id + work_dir/data_dir）以
// 协议 _meta 透传给 in-memory 节点（决策 R-11 二次升级：**不再塞进 tool arguments**）。
// self 节点（内置能力源）与 dir 节点（本仓 spawn executor 的 capability 目录）均属
// **builtin 来源**，故 inject=true；_meta 注入与否由来源标注决定（非 in-process 与否）。
type memNodeProvider struct {
	node   *memNode
	inject bool
}

func (p *memNodeProvider) Name() string { return p.node.name }

func (p *memNodeProvider) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	return p.node.ListTools(ctx)
}

func (p *memNodeProvider) Call(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	var meta mcp.Meta
	if p.inject {
		meta = callContextMeta(ctx)
	}
	return p.node.Call(ctx, tool, args, meta)
}

func (p *memNodeProvider) Close() error {
	p.node.Close()
	return nil
}

// Invalidate 内存节点无独立子进程/连接可作废 → no-op（始终可用）。
func (p *memNodeProvider) Invalidate(context.Context) (bool, error) { return true, nil }

// Terminate 内嵌执行器（in-memory 线）为**协作式**终止：取消已由执行池 ctx 经 in-memory 传输
// 送达 handler（探针证「能传递」，见 18 §3.7），真停在执行体侧（mcp-server callTool 继承 ctx +
// CommandContext）→ 此处 no-op（无 OS 级 kill 可做）。
func (p *memNodeProvider) Terminate(context.Context, string, string) (bool, error) { return true, nil }

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
	if len(ns) == 0 {
		return nil
	}
	return mcp.Meta{"chonkpilot": ns}
}

// execCtxFromMeta 从 tools/call 请求的调用上下文 _meta 重建执行 ctx（self/dir 节点 handler 经
// AddTool 包装调用——in-memory 传输不携带 client Go ctx 值，以协议 _meta 往返调用上下文）。
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
	return withTurnContext(ctx, c)
}
