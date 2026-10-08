// 注册工具提供方（registeredProvider）：server 域工具/网关自持工具经 mcp.register(kind=tool)
// 注册进 gateway（tools.register 已并入，61-消息一览 §5.1.1 2026-09-05），
// 命中执行时：本地 handler 直调，远程回调 = Emit 到注册时声明的回调主题（handler_subject，
// 相对主题，chonk. 前缀注入；61-消息一览 §5.4）——订阅者写 v.Result，发送方 await 同一主题，
// 无独立回执主题、无 req_id。
package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chonkpilot/chonkpilot-lib/mq"
)

// registeredTool 是注册的工具录（含本地 handler 或远程回调主题）。
type registeredTool struct {
	Tool *mcp.Tool
	// Handler 非空 = 本地 handler（gateway 自持工具如 meta 工具）。
	Handler func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error)
	// HandlerSubject 非空 = 远程回调主题（注册时声明的相对主题，见 61-消息一览 §5.4）。
	HandlerSubject string
	// PreHookSubject 非空 = **前置钩子主题**（注册时可选声明，2026-09-27 新增）：
	// gateway 在执行**任意**工具（tools/call）之前，先向该相对主题发一次**同步**请求，
	// 钩子失败 → 拒绝该工具调用（工具不执行）。仅在**有工具声明了钩子**时才发（零开销）。
	PreHookSubject string
	// Owner 所属提供方标识（服务器名/ID）。
	Owner string
	// Scope 归属域："" = global；否则 = instance id（uuid；该工具仅归属 instance 可见）
	Scope string
}

// registeredProvider 是注册工具的执行服务。
// 支持两种模式：本地 handler（gateway 自持工具）+ 远程回调（server 域工具，bus 转发）。
type registeredProvider struct {
	mu          sync.RWMutex
	tools       map[string]*registeredTool
	bus         mq.Bus // 回调总线（进程内内存实现）
	callTimeout time.Duration
	// preHooks = 前置钩子主题引用计数（同主题多工具注册 → 计数）；preHookN = 快速判定（原子，无钩子时零开销）。
	preHooks map[string]int
	preHookN atomic.Int64
}

// newRegisteredProvider 创建注册工具表。
func newRegisteredProvider(bus mq.Bus, callTimeout time.Duration) *registeredProvider {
	return &registeredProvider{
		tools:       map[string]*registeredTool{},
		bus:         bus,
		callTimeout: callTimeout,
		preHooks:    map[string]int{},
	}
}

// Name 返回提供方标识。
func (p *registeredProvider) Name() string { return "registered" }

// ListTools 返回全部注册工具。
func (p *registeredProvider) ListTools(_ context.Context) ([]*mcp.Tool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*mcp.Tool, 0, len(p.tools))
	for _, rt := range p.tools {
		out = append(out, rt.Tool)
	}
	return out, nil
}

// Call 执行注册工具。
// 本地 handler 直接调用；远程回调 Emit 回调主题并 await 同一主题（同主题 promise）。
// 归属解析按执行 ctx 注入的 instance（doCall withTurnContext）：scoped 命中 → 回退 global。
func (p *registeredProvider) Call(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	inst := InstanceFromContext(ctx)
	rt, ok := p.toolFor(inst, tool)
	if !ok {
		return nil, fmt.Errorf("tool %q not registered", tool)
	}

	// 本地 handler
	if rt.Handler != nil {
		return rt.Handler(ctx, args)
	}

	// 远程回调（注册方 = server 域工具）：Emit 到回调主题并 await 同一主题——
	// 订阅者同步执行并写回 v.Result（成功 = map{content, isError} 或 *mcp.CallToolResult），
	// 失败以 error 返回（收集进 v.Errors）。无独立 -reply 主题、payload 无 req_id。
	if rt.HandlerSubject == "" || p.bus == nil {
		return nil, fmt.Errorf("tool %q has no handler and no callback subject", tool)
	}

	payload := map[string]any{"tool": tool, "args": args}
	// 携带 doCall 经 withTurnContext 注入的 turn 上下文（session/turn/instance/tool_call_id）——
	// 回调方（server 域工具 handler）据此恢复对应 server turn。
	if c := turnContextPayload(ctx); len(c) > 0 {
		payload["context"] = c
	}
	f := p.bus.Emit(ctx, rt.HandlerSubject, payload)
	v := f.Wait()
	if err := v.Err(); err != nil {
		return nil, fmt.Errorf("exec %s: %w", tool, err)
	}
	return toolResultFromValue(v)
}

// turnContextPayload 从执行 ctx 提取 turn 上下文（provider.go withTurnContext 注入）→ JSON 形态。
func turnContextPayload(ctx context.Context) map[string]string {
	m := map[string]string{}
	if v := TurnFromContext(ctx); v != "" {
		m["turn"] = v
	}
	if v := SessionFromContext(ctx); v != "" {
		m["session"] = v
	}
	if v := InstanceFromContext(ctx); v != "" {
		m["instance_id"] = v
	}
	if v := ToolCallIDFromContext(ctx); v != "" {
		m["tool_call_id"] = v
	}
	return m
}

// toolResultFromValue 把订阅者写回的 v.Result 归一为 *mcp.CallToolResult。
// 支持 *mcp.CallToolResult 直写与 map{content, isError} 形态；未写回 → 视为超时失败。
func toolResultFromValue(v *mq.Value) (*mcp.CallToolResult, error) {
	if v.Result == nil {
		return nil, fmt.Errorf("exec timeout after no result")
	}
	if res, ok := v.Result.(*mcp.CallToolResult); ok {
		return res, nil
	}
	if m, ok := v.Result.(map[string]any); ok {
		res := &mcp.CallToolResult{}
		if content, ok := m["content"]; ok {
			res.Content = contentList(content)
		}
		if isErr, ok := m["isError"].(bool); ok {
			res.IsError = isErr
		}
		return res, nil
	}
	return nil, fmt.Errorf("exec reply: unexpected result type %T", v.Result)
}

// contentList 把订阅者写回的 content（[]mcp.Content / []any / []map，每项 {type,...}）
// 归一为官方 SDK Content 列表——接口切片不可直接 JSON 反序列化，须按 type 构造具体类型。
func contentList(content any) []mcp.Content {
	var out []mcp.Content
	if typed, ok := content.([]mcp.Content); ok {
		for _, c := range typed {
			out = append(out, contentFromItem(c))
		}
		return out
	}
	items, ok := content.([]any)
	if !ok {
		if ms, ok := content.([]map[string]any); ok {
			items = make([]any, len(ms))
			for i, m := range ms {
				items[i] = m
			}
		} else {
			return nil
		}
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			if c, ok := it.(mcp.Content); ok {
				out = append(out, contentFromItem(c))
			}
			continue
		}
		switch m["type"] {
		case "text":
			text, _ := m["text"].(string)
			out = append(out, &mcp.TextContent{Text: text})
		case "image":
			data, _ := m["data"].(string)
			mime, _ := m["mimeType"].(string)
			out = append(out, &mcp.ImageContent{Data: []byte(data), MIMEType: mime})
		case "resource":
			// 嵌入式资源：原文透传（JSON 兜底为文本）
			raw, _ := json.Marshal(it)
			out = append(out, &mcp.TextContent{Text: string(raw)})
		default:
			raw, _ := json.Marshal(it)
			out = append(out, &mcp.TextContent{Text: string(raw)})
		}
	}
	return out
}

// contentFromItem 透传已构造的官方 Content（官方 Content 实现均为指针形态，值类型不做分支）。
func contentFromItem(c mcp.Content) mcp.Content {
	switch v := c.(type) {
	case *mcp.TextContent:
		return &mcp.TextContent{Text: v.Text, Meta: v.Meta, Annotations: v.Annotations}
	case *mcp.ImageContent:
		return &mcp.ImageContent{Data: v.Data, MIMEType: v.MIMEType, Meta: v.Meta, Annotations: v.Annotations}
	default:
		return c
	}
}

// Close 释放资源（无操作，注册工具不维护独立生命周期）。
func (p *registeredProvider) Close() error { return nil }

// Invalidate 注册工具无独立子进程/连接 → no-op（始终可用）。
func (p *registeredProvider) Invalidate(context.Context) (bool, error) { return true, nil }

// Terminate 域工具（in-memory 线）为**协作式**终止：注册回调 Emit 继承执行池 ctx（`Call` 的
// `bus.Emit(ctx,...)`），真停在执行体侧（llm-server runSubJob 的 ctx 感知 / mcp-server）→
// 此处 no-op（无独立子进程/连接可作废）。
func (p *registeredProvider) Terminate(context.Context, string, string) (bool, error) {
	return true, nil
}

// toolFor 按 (instance, tool) 查注册工具：先精确归属域（scoped 遮蔽），未命中回退 global。
func (p *registeredProvider) toolFor(instance, tool string) (*registeredTool, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if instance != scopeGlobal {
		if rt, ok := p.tools[routeKey(instance, tool)]; ok {
			return rt, true
		}
	}
	rt, ok := p.tools[routeKey(scopeGlobal, tool)]
	return rt, ok
}

// RegisterTool 注册一个工具到注册提供方。
// 注册后立即生效（tools.list 可见、tools/call 可调用）。同 (scope, name) 重复注册报错。
func (p *registeredProvider) RegisterTool(rt *registeredTool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := routeKey(rt.Scope, rt.Tool.Name)
	if _, dup := p.tools[key]; dup {
		return fmt.Errorf("tool %q (scope=%q) already registered", rt.Tool.Name, rt.Scope)
	}
	p.tools[key] = rt
	if rt.PreHookSubject != "" {
		p.preHooks[rt.PreHookSubject]++
		p.preHookN.Add(1)
	}
	return nil
}

// UnregisterTool 注销 (scope, name) 工具（同时回收其前置钩子声明）。
func (p *registeredProvider) UnregisterTool(scope, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := routeKey(scope, name)
	if rt, ok := p.tools[key]; ok && rt.PreHookSubject != "" {
		if n := p.preHooks[rt.PreHookSubject]; n <= 1 {
			delete(p.preHooks, rt.PreHookSubject)
		} else {
			p.preHooks[rt.PreHookSubject] = n - 1
		}
		p.preHookN.Add(-1)
	}
	delete(p.tools, key)
}

// HasPreHooks 是否存在已注册的前置钩子（**零开销**快速判定：原子计数，无钩子时 doCall 不发任何消息）。
func (p *registeredProvider) HasPreHooks() bool { return p.preHookN.Load() > 0 }

// PreHookSubjects 返回去重后的前置钩子主题（稳定序；无钩子 → nil）。
func (p *registeredProvider) PreHookSubjects() []string {
	if p.preHookN.Load() <= 0 {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.preHooks))
	for subj := range p.preHooks {
		out = append(out, subj)
	}
	sort.Strings(out)
	return out
}

// GetToolFor 按 (instance, name) 查询注册工具（scoped 优先回退 global）。
func (p *registeredProvider) GetToolFor(instance, name string) (*registeredTool, bool) {
	return p.toolFor(instance, name)
}

// GetTool 按全局名查询注册工具（兼容无 instance 语境）。
func (p *registeredProvider) GetTool(name string) (*registeredTool, bool) {
	return p.toolFor(scopeGlobal, name)
}
