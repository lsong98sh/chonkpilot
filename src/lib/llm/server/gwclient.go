// gateway 客户端：tools/list 缓存 + 方法调用（mq promise：Emit 方法主题 → await 同主题
// v.Result/v.Err()，无 -reply 主题、无 req_id——对齐 26-mcp-gateway 2026-09-05 promise 化）。
// 2026-09-06 主题统一 mcp 域：方法调用相对主题 = mcp-<组>-<动作>（tools/list → mcp-tools-list）。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/chonkpilot/chonkpilot-lib/mq"
	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/chonkpilot/chonkpilot-mcp-gateway/gateway"
)

// ToolDef 是 LLM 工具定义（对齐 21-llm-server FetchTools 映射）。
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  any            `json:"inputSchema,omitempty"`
	Hot         bool           `json:"-"` // hot 标记（gateway tools/list _meta.hot；toolsForLLM 过滤用，不发 LLM）
	Async       string         `json:"-"` // async 模式（gateway tools/list _meta.async；缺省 auto；G-17 转后台门控用）
	Scope       string         `json:"-"` // 归属域（""=global / instance id；list 全量含 instance，toolsForLLM 按此过滤）
	Meta        map[string]any `json:"-"` // 工具自身 _meta 原文（tools/list 透出；I-60 随 tool-call 消息落库，不发 LLM）
	// 域工具契约 [meta]（loadDomainTools 填充；registerDomainTools 经 tools/register 透出为 gateway `_meta`）
	Category   string `json:"-"` // 契约 meta.category（如 server）
	AsyncTh    int    `json:"-"` // 契约 meta.async-threshold（秒；0 = 未声明）
	Timeout    int    `json:"-"` // 契约 meta.timeout（秒；0 = 未声明）
	TimeoutSet bool   `json:"-"` // 契约 meta.timeout 键是否**显式声明**（0/-1 = 无上限；未声明 = 不写 _meta）
}

// gwClient 封装对 gateway 的方法调用（mcp-* 相对主题；chonk. 前缀由总线 Options.Prefix 注入）。
type gwClient struct {
	bus mq.Bus

	mu    sync.RWMutex
	tools []ToolDef // tools/list 缓存（全量；GUI/管理面与按名反查用）
	// RB-5 L2：**按归属域分桶**（scope → 该域工具，保持 tools/list 顺序）——`toolsForLLM`
	// 不再对全量切片"每次过滤"，而是按 instance 直接取「global ∪ 该 instance」两桶拼接。
	// 分桶与全量同源（同一份 ListTools 结果），不新增任何 MQ 调用。
	buckets map[string][]ToolDef
}

func newGWClient(bus mq.Bus) *gwClient {
	return &gwClient{bus: bus, buckets: map[string][]ToolDef{}}
}

// gwSubject 把 gateway 方法名（组/动作）映射为相对主题：tools/list → mcp-tools-list。
//
// 2026-09-18（用户决定）：`tasks/*` 与 `tools/background` 方法面已从 gateway 移除
// ——异步结果读 message 表（`data-session-content` key `tool_result:<tool_call_id>`）、
// 状态以任务层为准；取消/转后台走进程内 sink（`taskExecSink.CancelExec` / `DetachExec`）。
func gwSubject(method string) string {
	return "mcp-" + strings.ReplaceAll(method, "/", "-")
}

// emitWait 对方法主题做一次 Emit + await：返回 gateway 写回的 v.Result（JSON 归一 map）。
func (g *gwClient) emitWait(ctx context.Context, subject string, payload any) (map[string]any, error) {
	v := g.bus.Emit(ctx, subject, payload).Wait()
	if err := v.Err(); err != nil {
		return nil, &toolError{msg: err.Error()}
	}
	return resultJSON(v.Result), nil
}

// call 统一的请求-回执入口（context 携带 instance/session/turn）。
func (g *gwClient) call(ctx context.Context, subject string, c mcpgateway.Context, body map[string]any) (map[string]any, error) {
	payload := map[string]any{}
	if c.WorkDir != "" {
		payload["work_dir"] = c.WorkDir
	}
	if c.DataDir != "" {
		payload["data_dir"] = c.DataDir
	}
	if c.Session != "" {
		payload["session"] = c.Session
	}
	if c.Turn != "" {
		payload["turn"] = c.Turn
	}
	if c.InstanceID != "" {
		payload["instance_id"] = c.InstanceID
	}
	if c.ToolCallID != "" {
		payload["tool_call_id"] = c.ToolCallID // LLM tool-call 关联 id 层层传递（域工具回调节点归属用）
	}
	// 任务树归属（I-90）：gateway 记入执行池并随 mcp-tasks-report 回传 → 任务层可独立建节点。
	if c.TopSession != "" {
		payload["top_session"] = c.TopSession
	}
	if c.Parent != "" {
		payload["parent"] = c.Parent
	}
	for k, val := range body {
		payload[k] = val
	}
	return g.emitWait(ctx, subject, payload)
}

// Call 工具调用：发 mcp-tools-call。
// 返回 (result, taskID, err)：taskID 非空 = 转异步（structuredContent.status=="pending"，调用方登记挂起）。
// instance 为空 = 异常（R-11 二次升级：调用上下文必带 instance，为空即顶层错误、不下发）。
func (g *gwClient) Call(ctx context.Context, name string, args map[string]any, c mcpgateway.Context) (map[string]any, string, error) {
	if strings.TrimSpace(c.InstanceID) == "" {
		return nil, "", &toolError{msg: "缺少 instance（宿主未注入调用上下文），无法执行工具调用"}
	}
	body := map[string]any{"name": name}
	if args != nil {
		body["arguments"] = args
	}
	res, err := g.call(ctx, msgkeys.TopicMcpToolsCall, c, body)
	if err != nil {
		return nil, "", err
	}
	if sc, ok := res["structuredContent"].(map[string]any); ok {
		if st, _ := sc["status"].(string); st == "pending" {
			tid, _ := sc["task_id"].(string)
			return res, tid, nil
		}
	}
	return res, "", nil
}

// resultTextFromMap 从工具结果 map 提取文本（content 可能是 string 或 TextContent 数组）。
func resultTextFromMap(res map[string]any) string {
	if res == nil {
		return "工具无返回"
	}
	if text, ok := res["text"].(string); ok {
		return text
	}
	if content, ok := res["content"].(string); ok {
		return content
	}
	if arr, ok := res["content"].([]any); ok {
		for _, c := range arr {
			if m, ok := c.(map[string]any); ok {
				if t, ok := m["text"].(string); ok && t != "" {
					return t
				}
			}
		}
	}
	b, _ := json.Marshal(res)
	return string(b)
}

// resultStatus 取工具结果 structuredContent.status（转后台 = pending；超时裁决取消 = cancelled；
// 无则空串）。同步返回路径据此判定终态语义（I-62：取消不靠文本判定）。
func resultStatus(res map[string]any) string {
	if res == nil {
		return ""
	}
	if sc, ok := res["structuredContent"].(map[string]any); ok {
		return str(sc["status"])
	}
	return ""
}

// ListTools 拉取 tools/list → 缓存 []ToolDef（并**按归属域分桶**，RB-5 L2）。
func (g *gwClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	res, err := g.emitWait(ctx, msgkeys.TopicMcpToolsList, map[string]any{})
	if err != nil {
		return nil, err
	}
	var defs []ToolDef
	buckets := map[string][]ToolDef{}
	if tools, ok := res["tools"].([]any); ok {
		for _, t := range tools {
			if m, ok := t.(map[string]any); ok {
				def := ToolDef{
					Name:        str(m["name"]),
					Description: str(m["description"]),
					Parameters:  m["inputSchema"],
					Hot:         metaHot(m),
					Async:       metaAsync(m),
					Scope:       str(m["scope"]),
					Meta:        metaMap(m),
				}
				defs = append(defs, def)
				buckets[def.Scope] = append(buckets[def.Scope], def)
			}
		}
	}
	g.mu.Lock()
	g.tools = defs
	g.buckets = buckets
	g.mu.Unlock()
	return defs, nil
}

// Tools 返回缓存的工具定义（全量；未预热返回空）。
func (g *gwClient) Tools() []ToolDef {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tools
}

// ToolsFor 返回某 instance **可见**的工具定义（RB-5 L2：按 instance 分桶直取）：
// = global 桶 ∪ 归属该 instance 的桶（顺序 = tools/list 顺序：global 在前，与「全量过滤」
// 逐项同序）。instance 为空 → 仅 global 桶（等价旧口径：`scope==""`）。
func (g *gwClient) ToolsFor(instance string) []ToolDef {
	g.mu.RLock()
	defer g.mu.RUnlock()
	global := g.buckets[""] // scope 空串 = global（61-消息一览 §5：tools/list 每项 scope 缺省/""=全局）
	if instance == "" {
		return global
	}
	out := make([]ToolDef, 0, len(global)+len(g.buckets[instance]))
	out = append(out, global...)
	out = append(out, g.buckets[instance]...)
	return out
}

// metaHot 从工具 JSON 的 _meta.hot 提取 hot 标记（gateway 注册时写入，21-llm-server）。
func metaHot(m map[string]any) bool {
	if meta, ok := m["_meta"].(map[string]any); ok {
		if h, ok := meta["hot"].(bool); ok {
			return h
		}
	}
	return false
}

// metaAsync 从工具 JSON 的 _meta.async 提取异步模式（缺省 auto，见 72-工具开发规范）。
func metaAsync(m map[string]any) string {
	if meta, ok := m["_meta"].(map[string]any); ok {
		if a, ok := meta["async"].(string); ok && a != "" {
			return a
		}
	}
	return "auto"
}

// metaMap 取工具 JSON 的 _meta 原文（gateway tools/list 透出，含 async/timeout/category/
// server/hot 等；nil/空 → nil。I-60：随 tool-call 消息记录落库，原样透传不裁剪语义）。
func metaMap(m map[string]any) map[string]any {
	if meta, ok := m["_meta"].(map[string]any); ok && len(meta) > 0 {
		return meta
	}
	return nil
}

// ToolMeta 返回工具的 _meta 原文（tools/list 缓存反查；未知工具返回 nil）。
// 供 LLM server 在处理 tool-call 落库时携带该工具自身的 meta 子集（I-60）。
func (g *gwClient) ToolMeta(name string) map[string]any {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, t := range g.tools {
		if t.Name == name {
			return t.Meta
		}
	}
	return nil
}

// AsyncMode 返回工具的 async 模式（tools/list 缓存反查；未知工具返回 ""=无法判定，
// 调用方须放行交 gateway 判定——G-17 转后台门控用）。
func (g *gwClient) AsyncMode(name string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, t := range g.tools {
		if t.Name == name {
			if t.Async == "" {
				return "auto"
			}
			return t.Async
		}
	}
	return ""
}

// gatewayCall 对 gateway 任意方法做一次 Emit+await 调用（servers/list、tools/register 等透传）。
// 方法名为组/动作（tools/register），主题 = mcp-tools-register；载荷按方法直传。
// reqID 参数为兼容旧调用面保留（promise 化后无 req_id，忽略）。
// 注：调用方须确保 gwClient 场景（s.gw 已装配）下使用；此处直接 emit，实例上下文由 ctx 承载。
func (s *Server) gatewayCall(ctx context.Context, method string, args map[string]any, _ string) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}
	return s.emitGateway(ctx, method, args)
}

// instanceCtxKey 携带 instance_id 的 context key（归属隔离注入用；
// 用 ctx 而非可变字段，避免并发多实例下串号）。
type instanceCtxKey struct{}

// withInstance 把 instance_id 绑到 ctx（业务消息一律必带 instance_id，见 61-消息一览 §0.1）。
func withInstance(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, instanceCtxKey{}, id)
}

// instanceFromCtx 取 ctx 上的 instance_id（无则空串）。
func instanceFromCtx(ctx context.Context) string {
	if id, ok := ctx.Value(instanceCtxKey{}).(string); ok {
		return id
	}
	return ""
}

// emitGateway 对 gateway 方法主题 Emit + await（公共入口，域工具注册/资产注册也用）。
// instance 上下文取自 ctx（withInstance 绑定）；未绑定时按全局处理（如域工具/agent 注册）。
func (s *Server) emitGateway(ctx context.Context, method string, args map[string]any) (map[string]any, error) {
	payload := map[string]any{}
	if id := instanceFromCtx(ctx); id != "" {
		payload["instance_id"] = id
	}
	for k, val := range args {
		payload[k] = val
	}
	subject := "mcp-" + strings.ReplaceAll(method, "/", "-")
	v := s.bus.Emit(ctx, subject, payload).Wait()
	if err := v.Err(); err != nil {
		return nil, &toolError{msg: err.Error()}
	}
	return resultJSON(v.Result), nil
}

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// resultJSON 把 v.Result 归一为 JSON 形态纯 map（与旧 -reply 载荷 JSON 编解码等价）。
func resultJSON(res any) map[string]any {
	if res == nil {
		return nil
	}
	if m, ok := res.(map[string]any); ok {
		b, _ := json.Marshal(m)
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			return m
		}
		return out
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
