// 域 meta 工具：mcp_find/mcp_load/mcp_invoke。
// 在 gateway 自持的注册提供方（regProv）中注册为本地 handler 工具，tools/call 同步执行。
// 对齐 3A-工具与编排（2026-09-03）。分类浏览（原 mcp_category）已移除：tools/list 的
// `_meta.category`/`_meta.hot` 已足够，UI/LLM 直接按 _meta 处理（2026-09-11 决策）。
package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/msgkeys"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMetaTools 把 3 个 meta 工具（mcp_find/mcp_load/mcp_invoke）注入
// 参数能力源 server（Params.MCPServer，self 节点）；gateway 聚合时 tools 来自其 list，
// tools/call 经 self 节点路由 → server handler 同步执行。无 MCPServer 时跳过（不自建）。
func (g *Gateway) registerMetaTools() {
	metaTools := []struct {
		name        string
		description string
		schema      string
		handler     func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error)
	}{
		{
			name:        "mcp_find",
			description: "检索/推荐工具与资产：按查询词（query，本地精确匹配）或任务目标（purpose，LLM 语义推荐——**对所有 type 生效**，可返回组合）在当前可见的**工具与资产**（skill/resource，含知识库原语）中查找匹配。**query/purpose 均省略时返回该类型全部条目**（受 limit）。返回匹配条目列表（名称、kind、描述、来源节点）或 LLM 推荐 JSON。",
			schema:      `{"type":"object","properties":{"purpose":{"type":"string","description":"任务目标（自然语言），如：把当前项目文档批量转成 markdown 并全文检索——语义推荐（可组合，候选按 type 收敛）；提供时优先 LLM，LLM 不可用降级本地"},"query":{"type":"string","description":"检索关键词（本地精确匹配；purpose 未提供时生效；省略则不按关键词过滤=返回该类型全部）"},"type":{"type":"string","description":"检索类型：tool（工具）/skill（技能）/resource（资源）/all（全部，默认）。其它取值一律报错。","default":"all"},"limit":{"type":"integer","description":"最大返回数","default":20}}}`,
			handler:     g.handleMCPFind,
		},
		{
			name:        "mcp_load",
			description: "加载条目详情：按名称（kind 可选，缺省 tool）获取其完整定义。tool = 名称/描述/参数 schema/来源 server/meta（hot/category 等）；skill/resource = 资产详情（description/content/uri 等）——skill/resource 为**节点原语实时读取**（与 prompts/get 同语义）。",
			schema:      `{"type":"object","properties":{"name":{"type":"string","description":"条目名（精确匹配）"},"kind":{"type":"string","description":"条目类型：tool（缺省）/skill/resource"}},"required":["name"]}`,
			handler:     g.handleMCPLoad,
		},
		{
			name:        "mcp_invoke",
			description: "直接调用工具：按工具名和参数调用，返回执行结果。相当于 tools/call 的封装，用于 LLM 自主调用已知工具。",
			schema:      `{"type":"object","properties":{"name":{"type":"string","description":"工具名"},"arguments":{"type":"object","description":"工具参数，按工具 schema 传入"}},"required":["name","arguments"]}`,
			handler:     g.handleMCPInvoke,
		},
	}

	if g.params.MCPServer == nil {
		g.logf("[gateway] meta tools skipped: no MCPServer (self)")
		return
	}
	for _, mt := range metaTools {
		var schema map[string]any
		_ = json.Unmarshal([]byte(mt.schema), &schema)
		norm, err := normalizeInputSchema(mt.name, schema)
		if err != nil {
			// meta 工具 schema 为内置常量，不会失败；防御性兜底
			g.logf("[gateway] meta tool %s schema invalid: %v", mt.name, err)
			continue
		}
		// _meta.hot=true 必填：meta 工具必须进 LLM 的 tools 参数，否则 LLM 无法发现/
		// 加载/调用任何工具（toolsForLLM 只放 _meta.hot==true 者；self 节点 entry 无
		// HotTools，故不会由 isHot 自动补）。category=meta 供 UI 分组。
		// _meta.async=never：类① 三个 meta 工具**只能同步**（18-工具异步超时与取消 §3.7 B /
		// §4「类①③④ 仅同步」）——声明后 gateway doCall 判为契约显式 never（不可被调用级覆盖），
		// 且工具异步页按「仅同步」显示。
		// _meta.timeout=0（= **无上限**）：三者均为 gateway 自持同步 handler（无执行硬上限）→ 显式声明
		// 无上限（与 tool_stop/tool_result/ask_user 同口径）：**绝对优先、不被调用级/server 级/全局覆盖**，
		// gateway 不设裁决点（永远等，用户可取消）。
		t := &mcp.Tool{
			Name:        mt.name,
			Description: mt.description,
			InputSchema: norm,
			Meta:        mcp.Meta{"hot": true, "category": "meta", "async": "never", "timeout": float64(0)},
		}
		handler := mt.handler
		g.params.MCPServer.AddTool(t, func(ctx context.Context, callReq *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := argsFromRequest(callReq.Params.Arguments)
			if err != nil {
				return toolErrorText(err.Error()), nil
			}
			// 调用上下文经协议 _meta（doCall 调用前透传）→ 重建执行 ctx（instance 等可见性依据）
			return handler(execCtxFromMeta(ctx, callReq.Params.Meta), args)
		})
		g.logf("[gateway] meta tool injected into self: %s", mt.name)
	}
	_ = g.reconcileSelf()
}

// normalizeInputSchema 校验/补齐工具 schema：顶层 type 必须为 object（缺失补 {"type":"object"}，
// 其它 type 报错）——官方 SDK AddTool 对非 object/缺失 schema 直接 panic（参考 mcp-server contract.go buildTool）。
func normalizeInputSchema(name string, schema map[string]any) (map[string]any, error) {
	if len(schema) == 0 {
		return map[string]any{"type": "object"}, nil
	}
	switch typ, _ := schema["type"].(string); typ {
	case "object":
		return schema, nil
	case "":
		out := map[string]any{"type": "object"}
		for k, v := range schema {
			out[k] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tool %s: input schema must have type \"object\", got %q", name, typ)
	}
}

// argsFromRequest 把官方 tools/call 请求参数（json.RawMessage）解码为 args map。
func argsFromRequest(raw json.RawMessage) (map[string]any, error) {
	args := map[string]any{}
	if len(raw) == 0 || string(raw) == "null" {
		return args, nil
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	return args, nil
}

// toolTextResult 工具成功文本结果。
func toolTextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// toolErrorText 工具级错误结果（文本 content + isError:true）。
func toolErrorText(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

// assetKindsAll 是目录资产在 **LLM 检索面**纳入的 kind（25 §5 / T2，2026-09-25：`mcp_find` 的
// type 收敛为 `tool | skill | resource | all`）—— **prompt** 与 **agent** 均已移出 LLM 检索面
// （prompt 改由知识库维护 + chat 面用户选择注入；agent 只经系统提示词"注入"不注册资产）。
var assetKindsAll = []string{KindSkill, KindResource}

// findTypeScope 把 mcp_find 的 type 参数解析为候选范围（25 §5/T2 收敛后**只留 LLM 检索面类型**）：
//   - assetKinds：纳入的目录资产 kind（nil = 不含资产）
//   - includeTools：工具段是否参与
//   - ok=false：**未知类型 → 调用方明确报错**（不再静默返回空）
//
// 取值：`tool`（仅工具）/ `skill` / `resource`（仅对应资产）/ `all`（缺省 = 全部）。
// **删**：`prompt`·`agent`（已移出 LLM 检索面）与 `category`·`kb`|`knowledge`·`codebase`·`file`
// （工具 category 维度，已并入 tool）与 `assets`；`*` 亦视为未知（RB-4 口径）。
func findTypeScope(typ string) (assetKinds []string, includeTools bool, ok bool) {
	switch typ {
	case "", "all":
		return assetKindsAll, true, true
	case "tool":
		return nil, true, true
	case KindSkill, KindResource:
		return []string{typ}, false, true
	}
	return nil, false, false // 未知类型（含 prompt/agent）→ 由调用方明确报错
}

// findTypeNames 返回 mcp_find 的合法 type 取值清单（错误消息 / schema 文案单源）。
func findTypeNames() string { return "tool / skill / resource / all" }

// nodeAsset 是**节点原语**（skill / resource）的统一视图项（RB-4 ②：find/load 的资产视图 =
// 节点原语 ∪ 注册资产；25 §5/T2：prompt 已移出 LLM 检索面，故原语视图只剩 skill / resource）。
type nodeAsset struct {
	Kind        string
	Name        string
	Description string
	URI         string
	Node        string
	Scope       string
}

// memNodeAssets 枚举请求 instance 可见的**节点原语**（self + global ∪ 归属 instance 的 dir 节点）：
// kind 空 = 全部（**prompt 类原语恒不纳入** —— 25 §5/T2「prompt 移出 LLM 检索面」）。
// 原语内容**不驻留**（按需经 GetPrompt / ReadResource 取，RB-4 ③）。
func (g *Gateway) memNodeAssets(inst, kind string) []nodeAsset {
	wantPrompt := kind == "" || kind == KindSkill
	wantResource := kind == "" || kind == KindResource
	if !wantPrompt && !wantResource {
		return nil
	}
	ctx := context.Background()
	var out []nodeAsset
	_ = g.eachMemNodeVisible(inst, func(n *memNode, nodeName, scope string) error {
		if wantPrompt {
			ps, err := n.ListPrompts(ctx)
			if err != nil {
				return nil // 单节点失败不阻塞
			}
			for _, p := range ps {
				k := KindPrompt
				if p.Meta != nil {
					if tv, ok := p.Meta["type"].(string); ok && tv != "" {
						k = tv
					}
				}
				if k == KindPrompt || k == KindAgent {
					continue // prompt / agent 不进 LLM 检索面（25 §5/T2）
				}
				if kind != "" && k != kind {
					continue
				}
				out = append(out, nodeAsset{Kind: k, Name: p.Name, Description: p.Description, Node: nodeName, Scope: scope})
			}
		}
		if wantResource {
			rs, err := n.ListResources(ctx)
			if err != nil {
				return nil
			}
			for _, r := range rs {
				out = append(out, nodeAsset{
					Kind: KindResource, Name: r.Name, Description: r.Description,
					URI: r.URI, Node: nodeName, Scope: scope,
				})
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// loadNodeAsset 实时拉取节点原语内容（RB-4 ③：与 prompts/get 同语义 —— 读**实时**而非
// 常驻副本）：skill → GetPrompt（{{arg}} 占位不展开）；resource → 按 uri
// （uri 空时按 name 经 ListResources 反查）ReadResource。命中返回条目 map（含 content/mimetype）。
// 25 §5/T2：prompt/agent 已移出 LLM 检索面 → 本函数只受理 skill/resource。
func (g *Gateway) loadNodeAsset(inst, kind, name, uri string) (map[string]any, bool) {
	if name == "" && uri == "" {
		return nil, false
	}
	ctx := context.Background()
	var hit map[string]any
	_ = g.eachMemNodeVisible(inst, func(n *memNode, nodeName, scope string) error {
		if hit != nil {
			return nil
		}
		if kind == KindResource {
			target := uri
			if target == "" {
				rs, err := n.ListResources(ctx)
				if err != nil {
					return nil
				}
				for _, r := range rs {
					if r.Name == name {
						target = r.URI
						break
					}
				}
				if target == "" {
					return nil
				}
			}
			res, err := n.ReadResource(ctx, target)
			if err != nil || len(res.Contents) == 0 {
				return nil
			}
			c := res.Contents[0]
			hit = map[string]any{
				"kind": KindResource, "name": name, "uri": c.URI, "mimetype": c.MIMEType,
				"content": c.Text, "node": nodeName, "scope": scope,
			}
			return nil
		}
		p, err := n.GetPrompt(ctx, name, nil)
		if err != nil || len(p.Messages) == 0 {
			return nil
		}
		tc, ok := p.Messages[0].Content.(*mcp.TextContent)
		if !ok {
			return nil
		}
		hit = map[string]any{
			"kind": kind, "name": name, "content": tc.Text, "node": nodeName, "scope": scope,
		}
		return nil
	})
	return hit, hit != nil
}

// llmToolPick 把任务目标交给无上下文单轮 LLM（llm-simple mq，server 订阅）做语义推荐。
// typ = mcp_find 的 type 参数：候选 = 该类型范围内当前 instance 可见条目（global ∪ 归属）
// 的 name+description（**故语义推荐对所有类型生效**：tool / skill / resource / all）；
// 资产候选 = 节点原语 ∪ 注册资产（RB-4 ②）。
// 无订阅/超时/空结果/未知类型 → ok=false（调用方降级本地检索 / 明确报错）。
func (g *Gateway) llmToolPick(ctx context.Context, inst, purpose, typ string, limit int) (string, bool) {
	if g.bus == nil {
		return "", false
	}
	assetKinds, includeTools, ok := findTypeScope(typ)
	if !ok {
		return "", false // 未知类型：不推荐（调用方明确报错）
	}
	inScope := func(kind string) bool {
		for _, k := range assetKinds {
			if k == kind {
				return true
			}
		}
		return false
	}
	var sb strings.Builder
	sb.WriteString("当前可用候选条目（name 或 name(kind): 描述）：\n")
	count := 0
	appendLine := func(line string) {
		if count >= 200 {
			return
		}
		sb.WriteString(line)
		count++
	}
	if includeTools {
		for _, rt := range g.reg.routesAll() {
			if rt.Scope != scopeGlobal && rt.Scope != inst {
				continue
			}
			desc := ""
			if rt.Tool != nil {
				desc = rt.Tool.Description
			}
			if len(desc) > 120 {
				desc = desc[:120]
			}
			appendLine(fmt.Sprintf("- %s: %s\n", rt.Name, desc))
		}
	}
	// 资产候选视图 = 节点原语 ∪ 注册资产（RB-4 ②：与 mcp_find 本地检索同视图）
	for _, a := range g.memNodeAssets(inst, "") {
		if !inScope(a.Kind) {
			continue
		}
		desc := a.Description
		if len(desc) > 120 {
			desc = desc[:120]
		}
		appendLine(fmt.Sprintf("- %s(%s): %s\n", a.Name, a.Kind, desc))
	}
	for _, kind := range assetKinds {
		for _, a := range g.reg.assetsByKind(kind) {
			if a.Scope != scopeGlobal && a.Scope != inst {
				continue
			}
			desc := a.Description
			if len(desc) > 120 {
				desc = desc[:120]
			}
			appendLine(fmt.Sprintf("- %s(%s): %s\n", a.Name, kind, desc))
		}
	}
	prompt := fmt.Sprintf("任务目标：%s\n请从候选条目中选择完成该任务最相关者（可多个/组合，按相关性排序，至多 %d 个，条目可为工具或资产），"+
		"输出 JSON {\"tools\":[{\"name\":\"\",\"reason\":\"\"}]}（无匹配输出 {\"tools\":[]}），仅输出 JSON。", purpose, limit)
	ctx2, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	v := g.bus.Emit(ctx2, msgkeys.TopicLlmSimple, map[string]any{"prompt": prompt, "system": sb.String(), "instance_id": inst}).Wait()
	if err := v.Err(); err != nil {
		return "", false
	}
	if m, ok := v.Result.(map[string]any); ok {
		if text, ok := m["text"].(string); ok && text != "" {
			return text, true
		}
	}
	return "", false
}

// handleMCPFind 按类型和关键词检索工具与**资产（节点原语 ∪ 注册资产）**。
// RB-4 ②：资产检索面统一 —— 知识库原语（用户/项目级 capability 的 prompt/skill/resource，
// 即节点原语）与注册资产同视图可被命中（此前只查 registry 段 → 知识库资产检索不到）。
// RB-4 ④：type 只留 tool/skill/resource/all，**未知类型明确报错**（25 §5/T2：prompt/agent 已移出）。
// 可见性按执行 ctx 注入的 instance 过滤：tools = global ∪ 归属 instance；资产同理。
func (g *Gateway) handleMCPFind(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	inst := InstanceFromContext(ctx)
	query, _ := args["query"].(string)
	purpose, _ := args["purpose"].(string)
	typ, _ := args["type"].(string)
	if typ == "" {
		typ = "all"
	}
	assetKinds, includeTools, ok := findTypeScope(typ)
	if !ok {
		return toolErrorText(fmt.Sprintf("unknown type %q（支持：%s）", typ, findTypeNames())), nil
	}
	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	// query / purpose 均省略 → 不做关键词过滤，返回该类型全部条目（受 limit；type 缺省 = all），
	// 供"列出全部子代理/工具"场景（原为报错 "query or purpose is required"，2026-09-11 放开）。
	// purpose（任务目标）：优先 LLM 语义推荐（无上下文单轮 LLM mq llm-simple，可含组合），
	// **对所有 type 生效**（候选范围按 typ 收敛，见 llmToolPick/findTypeScope）；
	// LLM 不可用（无订阅/超时）→ 降级把 purpose 作关键词走本地检索。
	if purpose != "" {
		if res, ok := g.llmToolPick(ctx, inst, purpose, typ, limit); ok {
			return toolTextResult(res), nil
		}
		query = purpose
	}
	q := strings.ToLower(query)

	matches := []map[string]any{}
	seenAsset := map[string]bool{} // (kind|name) 去重：节点原语优先（实时视图）
	if includeTools {
		for _, t := range g.reg.allToolsFor(inst) {
			if !strings.Contains(strings.ToLower(t.Name), q) &&
				!strings.Contains(strings.ToLower(t.Description), q) {
				continue
			}
			meta := toolMeta(t)
			entry := map[string]any{
				"kind":        "tool",
				"name":        t.Name,
				"description": t.Description,
				"category":    meta["category"],
				"hot":         meta["hot"],
			}
			// 参数定义必带：LLM 对非 hot 工具只能经 mcp_invoke 调用，需据 schema 组装 arguments
			if t.InputSchema != nil {
				entry["inputSchema"] = t.InputSchema
			}
			if rt, ok := g.reg.findFor(t.Name, inst); ok {
				entry["provider"] = rt.Provider
				if srv := g.serverInfoOf(rt.Provider); srv != nil {
					entry["server"] = srv
				}
			}
			matches = append(matches, entry)
			if len(matches) >= limit {
				break
			}
		}
	}
	// ② 节点原语（skill/resource；实时视图）+ 注册资产 —— 统一资产检索面（25 §5/T2：prompt/agent 不进）
	inScope := func(kind string) bool {
		for _, k := range assetKinds {
			if k == kind {
				return true
			}
		}
		return false
	}
	appendAsset := func(kind, name, desc, node string) {
		if len(matches) >= limit {
			return
		}
		if !inScope(kind) {
			return
		}
		key := kind + "|" + name
		if seenAsset[key] {
			return
		}
		if !strings.Contains(strings.ToLower(name), q) && !strings.Contains(strings.ToLower(desc), q) {
			return
		}
		seenAsset[key] = true
		entry := map[string]any{
			"kind":        kind,
			"name":        name,
			"description": desc,
			"category":    kind,
		}
		if node != "" {
			entry["node"] = node
		}
		matches = append(matches, entry)
	}
	for _, a := range g.memNodeAssets(inst, "") {
		appendAsset(a.Kind, a.Name, a.Description, a.Node)
	}
	for _, kind := range assetKinds {
		for _, a := range g.reg.assetsByKind(kind) {
			// 资产可见性：global ∪ 归属当前 instance
			if a.Scope != scopeGlobal && a.Scope != inst {
				continue
			}
			appendAsset(a.Kind, a.Name, a.Description, a.Node)
		}
	}
	if matches == nil {
		matches = []map[string]any{}
	}
	result, _ := json.Marshal(map[string]any{
		"query": query,
		"type":  typ,
		"count": len(matches),
		"tools": matches,
	})
	return toolTextResult(string(result)), nil
}

// toolMeta 返回工具 _meta 的只读视图（nil → 空 map；官方 Meta = map[string]any）。
func toolMeta(t *mcp.Tool) map[string]any {
	if t == nil || t.Meta == nil {
		return map[string]any{}
	}
	return t.Meta
}

// handleMCPLoad 加载条目详情：kind 缺省 tool；skill/resource = 资产全文（25 §5/T2：agent/prompt
// 已移出 LLM 检索面，非 tool/skill/resource 的 kind 一律报错）。
// 按执行 ctx instance 过滤可见性（scoped 只对归属 instance 命中；工具 scoped 遮蔽 global）。
func (g *Gateway) handleMCPLoad(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	inst := InstanceFromContext(ctx)
	name, _ := args["name"].(string)
	if name == "" {
		return toolErrorText("name is required"), nil
	}
	kind, _ := args["kind"].(string)
	if kind == "" {
		kind = KindTool
	}
	if kind == KindTool {
		route, ok := g.reg.findFor(name, inst)
		if !ok {
			return toolErrorText(fmt.Sprintf("tool %q not found", name)), nil
		}
		meta := toolMeta(route.Tool)
		out := map[string]any{
			"kind":        "tool",
			"name":        route.Name,
			"original":    route.Original,
			"provider":    route.Provider,
			"description": route.Tool.Description,
			"meta":        meta,
			"hot":         route.Hot,
		}
		// 参数定义：非 hot 工具不在 LLM 的 tools 参数里（server.toolsForLLM 只放 hot 且归属本
		// instance 的工具），LLM 只能经 mcp_invoke 调用 —— 因此必须能从这里拿到入参 schema。
		if route.Tool.InputSchema != nil {
			out["inputSchema"] = route.Tool.InputSchema
		}
		if srv := g.serverInfoOf(route.Provider); srv != nil {
			out["server"] = srv
		}
		result, _ := json.Marshal(out)
		return toolTextResult(string(result)), nil
	}
	if kind == KindSkill || kind == KindResource {
		// ① 节点原语：**实时**拉取内容（RB-4 ②③：与 prompts/get 同语义，覆盖知识库原语）
		if m, ok := g.loadNodeAsset(inst, kind, name, ""); ok {
			result, _ := json.Marshal(m)
			return toolTextResult(string(result)), nil
		}
		// ② 注册资产（内容随注册载荷承载，无独立来源可实时读）
		a, ok := g.reg.getAssetFor(kind, name, inst)
		if !ok {
			return toolErrorText(fmt.Sprintf("asset %q (kind=%s) not found", name, kind)), nil
		}
		m := map[string]any{"kind": a.Kind, "name": a.Name, "description": a.Description}
		if a.URI != "" {
			m["uri"] = a.URI
		}
		if a.MIMEType != "" {
			m["mimetype"] = a.MIMEType
		}
		c, cerr := assetContent(a) // RB-4 ①：有 path 实时读盘；无 path 取驻留 content
		if cerr != nil {
			return toolErrorText(cerr.Error()), nil
		}
		if c != "" {
			m["content"] = c
		}
		if len(a.Arguments) > 0 {
			m["arguments"] = a.Arguments
		}
		result, _ := json.Marshal(m)
		return toolTextResult(string(result)), nil
	}
	return toolErrorText(fmt.Sprintf("unknown kind %q（支持：%s）", kind, findTypeNames())), nil
}

// handleMCPInvoke 直接调用工具（封装 tools/call）。按执行 ctx instance 路由 scoped 工具。
func (g *Gateway) handleMCPInvoke(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	inst := InstanceFromContext(ctx)
	name, _ := args["name"].(string)
	arguments, _ := args["arguments"].(map[string]any)
	if name == "" {
		return toolErrorText("name is required"), nil
	}

	route, ok := g.reg.findFor(name, inst)
	if !ok {
		return toolErrorText(fmt.Sprintf("tool %q not found", name)), nil
	}
	ps, ok := g.reg.provider(route.Provider)
	if !ok {
		return toolErrorText("provider not found"), nil
	}
	// 熔断放行：与主调用链 doCall 同口径——allow() 放行后本调用须以熔断器收尾
	// （cancel/success/failure）。试探放行（isProbe）被取消时须 cancel(isProbe) 归还
	// half-open 单飞标志，否则 probing 恒真 → 该 provider 永久 fail-closed（C-47）。
	allowed, isProbe := ps.cb.allow()
	if !allowed {
		return toolErrorText(fmt.Sprintf("server %s unavailable (circuit open)", route.Provider)), nil
	}

	// 转发**调用方 ctx**（G-19）：此前此处传 context.Background() → 调用链 ctx 被丢弃，
	// 经 mcp_invoke 发起的下游调用**无法被取消**（turn 停止/工具取消传不到下游）。
	// 与主调用链 doCall run 闭包同口径：先判调用方取消；用户取消（ctx.Canceled）不计熔断失败
	// （否则一次停止即可能把 provider 熔断，后续正常调用被 "circuit open" 误伤）。
	res, err := ps.prov.Call(ctx, route.Original, arguments)
	if ctx.Err() == context.Canceled {
		// 仅试探调用复位 probing（普通取消不得复位，否则打破 half-open 单飞，C-32）。
		ps.cb.cancel(isProbe)
		if err != nil {
			return toolErrorText(err.Error()), nil
		}
		return res, nil
	}
	if err != nil {
		ps.cb.failure()
		return toolErrorText(err.Error()), nil
	}
	ps.cb.success()
	if res.IsError {
		return res, nil
	}
	return res, nil
}
