// Registry：工具路由表（工具名全局唯一；前缀/alias 应用；同名 = 注册期报错）。
// 对齐 26-mcp-gateway。
package mcpgateway

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolRoute 是一条工具路由：暴露名 → 提供方。
type toolRoute struct {
	Name     string    // 暴露名
	Original string    // 提供方原始工具名（provider.Call 用）
	Provider string    // 提供方 key（spawned:<id> / proxied:<id>）
	Tool     *mcp.Tool // schema（tools/list 输出）
	Hot      bool      // hot 标记（提交给 LLM 的高频工具，见 isHot）
	Scope    string    // 归属域："" = global；否则 = 归属 instance id（uuid）
}

// scopeGlobal 常量：scope 空串 = 全局（对外 payload 缺省 / "global" 归一为空）。
const scopeGlobal = ""

// injectDisplayName 给工具 schema 注入 `tool_call_display_name` 参数（LLM 必填，UI/CLI 展示
// 调用名/摘要；实际调用时由 doCall 剥离，不传下游工具）。
// 若工具已有同名参数（契约定义），不重复添加。
func injectDisplayName(t *mcp.Tool) {
	if t == nil || t.InputSchema == nil {
		return
	}
	schema, ok := t.InputSchema.(map[string]any)
	if !ok {
		return
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	if _, exists := props[displayNameArg]; exists {
		return // 已有（契约定义），不重复
	}
	props[displayNameArg] = map[string]any{
		"type":        "string",
		"description": "调用展示名（≤20 字符），仅 UI 展示，不影响工具执行",
	}
	req, _ := schema["required"].([]any)
	has := false
	for _, r := range req {
		if r == displayNameArg {
			has = true
			break
		}
	}
	if !has {
		schema["required"] = append(req, displayNameArg)
	}
}

// displayNameArg 是 gateway 注入的展示用参数名（**不叫 purpose**，避免与工具自有 purpose 语义冲突：
// 如 mcp_find 的 purpose = 任务目标语义推荐）。doCall 在执行前统一剥离，不传下游工具。
const displayNameArg = "tool_call_display_name"

// normalizeScope 归一注册载荷 scope：缺省（""）与 "global" → 空串（全局）；
// 其余（uuid instance id）原样作为 instance 级归属域。
func normalizeScope(s string) string {
	if s == "" || strings.EqualFold(s, "global") {
		return scopeGlobal
	}
	return s
}

// routeKey 返回 (scope, name) 复合路由键（同暴露名可属不同归属域并存）。
func routeKey(scope, name string) string { return scope + "|" + name }

// assetKey 返回 (scope, kind, name) 复合资产键。
func assetKey(scope, kind, name string) string { return scope + "|" + kind + ":" + name }

// provKey 返回 (type, scope, name) 复合提供方 key（global scope → type:name 兼容旧格式）。
func provKey(typ, scope, name string) string {
	if scope == scopeGlobal {
		return typ + ":" + name
	}
	return typ + ":" + scope + ":" + name
}

// isHot 判定工具是否 hot（21-llm-server）：
// HotTools 含 "*" = 该下游全部 hot（内嵌 mcp-server 登记用）；含原名 = 单工具 hot。
func isHot(e *ServerEntry, orig string) bool {
	if e == nil {
		return false
	}
	for _, h := range e.HotTools {
		if h == "*" || h == orig {
			return true
		}
	}
	return false
}

// providerState 是一个提供方的运行态。
type providerState struct {
	key    string // dir / spawned:<id> / proxied:<id>（scope 化见 provKey）
	entry  *ServerEntry
	prov   Provider
	status string // connected / connecting / failed / disabled / open / half-open
	cb     *breaker
	tools  []*toolRoute
	scope  string  // 归属域："" = global；否则 = instance id（该 provider 工具仅归属 instance 可见）
	ref    *dirRef // 非空 = 本提供方是 dir 共享节点的一次引用（RB-3 ①：工具按引用集即时展开，不物化 routes）
}

// catalogAsset 是目录资产（tool/prompt/skill/resource；25 §5/T2：agent 已撤出资产面）条目：
// list/find/load 的目录元信息；node = 来源节点名（反查 provider 路由执行），scope = 归属域。
type catalogAsset struct {
	Kind        string          `json:"kind"` // tool / prompt / skill / resource
	Name        string          `json:"name"` // 资产名（kind 内唯一）
	Description string          `json:"description,omitempty"`
	URI         string          `json:"uri,omitempty"`       // resource：uri
	MIMEType    string          `json:"mimetype,omitempty"`  // resource：mimetype
	Path        string          `json:"path,omitempty"`      // RB-4 ①：运行时内容来源路径（非空则内容按需读盘，不驻留 Content）
	Content     string          `json:"content,omitempty"`   // 兼容兜底：仅「无 Path 的资产」（内嵌域 agent 等）驻留
	Arguments   json.RawMessage `json:"arguments,omitempty"` // agent/prompt/skill：参数定义（原文）
	Scope       string          `json:"scope,omitempty"`     // 归属域："" = global；否则 = instance id（uuid）
	Node        string          `json:"node,omitempty"`      // 来源节点名（执行层反查 provider；缺省 "server"）
}

// assetContent 取资产内容（RB-4 ①，2026-09-22）：`path` 非空 → **实时读盘**（内容不驻留）；
// 无 `path` → 返回注册载荷携带的 `content`（内嵌域 agent 等无运行时落点的资产，兼容兜底）。
// 读盘失败 → 空串（有 path 即视为唯一来源，不回退陈旧副本）。
func assetContent(a *catalogAsset) string {
	if a == nil {
		return ""
	}
	if a.Path != "" {
		b, err := os.ReadFile(a.Path)
		if err != nil {
			return ""
		}
		return string(b)
	}
	return a.Content
}

// ─── 共享能力节点（RB-3 ①：注册表按 node key 唯一，instance 只持引用集）───

// dirRef 是某归属域对共享能力节点的一次**引用**（RB-3 ①：「instance 只持引用集」）。
// 暴露名 = 节点名前缀 + "_" + 工具原名（与既有 applyPrefix 规则逐字一致，对外零变更）。
type dirRef struct {
	nodeKey string // 共享节点 key（dir = 规范化根路径）
	name    string // 节点名（暴露名前缀 = name + "_"）
	scope   string // 归属域："" = global；否则 = instance id（uuid）
	pk      string // 提供方 key = provKey("dir", scope, name)
}

// sharedNode 是**按 node key 唯一**的能力节点（RB-3 ①）：同根跨 instance 只扫描 / 只建一份
// in-memory 会话与工具对象，各归属域经 refs 引用（不再按 instance 物理复制）。
type sharedNode struct {
	key    string
	node   *memNode
	tools  []*mcp.Tool // 原始工具（原名；按名排序）
	byName map[string]*mcp.Tool
	refs   []*dirRef // 引用集
}

// dirExposedName 计算公开暴露名（节点名前缀 + 原名；与 applyPrefix 同规则）。
func dirExposedName(ref *dirRef, orig string) string { return ref.name + "_" + orig }

// dirOriginalName 由暴露名反解工具原名（前缀不匹配 → ok=false）。
func dirOriginalName(ref *dirRef, exposed string) (string, bool) {
	p := ref.name + "_"
	if !strings.HasPrefix(exposed, p) {
		return "", false
	}
	return exposed[len(p):], true
}

// dirRoute 由共享节点 + 引用即时合成一条路由（**不物化**进 routes 表：RB-3 ②）。
func dirRoute(ref *dirRef, t *mcp.Tool, exposed string) *toolRoute {
	return &toolRoute{
		Name: exposed, Original: t.Name, Provider: ref.pk,
		Tool: t, Hot: t.Meta != nil, Scope: ref.scope,
	}
}

// toolWithName 返回「以 exposed 为名」的工具视图：名一致 → 原对象；否则浅拷贝改名
// （dir 共享工具不改名，避免共享对象被按 instance 改写）。
func toolWithName(rt *toolRoute) *mcp.Tool {
	if rt == nil || rt.Tool == nil || rt.Tool.Name == rt.Name {
		return rt.Tool
	}
	cp := *rt.Tool
	cp.Name = rt.Name
	return &cp
}

type registry struct {
	mu        sync.RWMutex
	routes    map[string]*toolRoute
	providers map[string]*providerState
	order     []string
	assets    map[string]*catalogAsset // key = kind + ":" + name
	shared    map[string]*sharedNode   // node key → 共享能力节点（RB-3 ①）
	refs      map[string]*dirRef       // 提供方 key → dir 引用（反查/注销）
}

func newRegistry() *registry {
	return &registry{
		routes:    map[string]*toolRoute{},
		providers: map[string]*providerState{},
		assets:    map[string]*catalogAsset{},
		shared:    map[string]*sharedNode{},
		refs:      map[string]*dirRef{},
	}
}

// registerProvider 注册一个提供方（dir 或下游 server）。
// proxyTools 为下游 tools/list 原始结果（dir 时为 nil，内部加载契约）。
// 计算暴露名：下游 = 前缀 + 原名（prefix 由 namespace 决定：默认 <id>_ / "-" 禁 / 自定义）；再应用 alias；dir = NsPrefix + 原名。
// 同名冲突 → 返回错误（列出来源）。
func (r *registry) registerProvider(ps *providerState, proxyTools []*mcp.Tool, nsPrefix string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var tools []*mcp.Tool
	if proxyTools != nil {
		tools = proxyTools
	}

	for _, t := range tools {
		orig := t.Name
		name := orig
		if ps.entry != nil {
			// 下游：前缀（alias 优先于前缀，§8.1）
			if al, ok := ps.entry.Aliases[orig]; ok {
				name = al
			} else if name = applyPrefix(orig, ps.entry); name == orig && !ps.entry.IsBuiltin() {
				// 命名与唯一性：**第三方（Origin=user）一律带来源别名（前缀）**——禁止去前缀
				// （namespace "-"）或空前缀，否则其工具暴露名会与内置（self_*）/知识库条目撞名。
				name = entrySourcePrefix(ps.entry) + orig
			}
		} else {
			name = nsPrefix + orig
		}
		// 冲突按 (scope, 暴露名) 判定：global 与各 instance scoped 是独立名字空间
		if existing, dup := r.routes[routeKey(ps.scope, name)]; dup {
			return fmt.Errorf("tool 名冲突 %q（%s 与 %s，来源 %s/%s，scope=%q）：用 servers.list alias 改名或配对", name, existing.Provider, ps.key, existing.Provider, ps.key, ps.scope)
		}
		t.Name = name
		// hot 标记（21-llm-server）：HotTools 含 "*"=全部 hot / 含原名=单工具 hot；
		// 写入工具 _meta.hot（与契约/其他 _meta 扩展字段合并，tools/list 透出，server 据此过滤提交给 LLM 的工具）。
		if isHot(ps.entry, orig) {
			if t.Meta == nil {
				t.Meta = mcp.Meta{}
			}
			t.Meta["hot"] = true
		}
		// 调用展示名注入：每个工具参数列表自动加 tool_call_display_name（LLM 必填，UI/CLI 展示用），
		// 实际调用时由 doCall 剥离（不传下游）。
		injectDisplayName(t)
		route := &toolRoute{Name: name, Original: orig, Provider: ps.key, Tool: t, Hot: t.Meta != nil, Scope: ps.scope}
		r.routes[routeKey(ps.scope, name)] = route
		ps.tools = append(ps.tools, route)
	}
	sort.Slice(ps.tools, func(i, j int) bool { return ps.tools[i].Name < ps.tools[j].Name })
	if _, ok := r.providers[ps.key]; !ok {
		r.order = append(r.order, ps.key)
	}
	r.providers[ps.key] = ps
	return nil
}

// registerFailed 登记一个连接失败的提供方（仅状态可见，无工具注册）——
// 符合"失败不阻塞：状态端点可见，可手动 reload"（26-mcp-gateway）。
func (r *registry) registerFailed(ps *providerState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[ps.key]; !ok {
		r.order = append(r.order, ps.key)
	}
	r.providers[ps.key] = ps
}

// applyPrefix 计算下游工具暴露名（namespace 覆盖默认 <id>_ 前缀）。
func applyPrefix(name string, e *ServerEntry) string {
	ns := e.Namespace
	if ns == "-" {
		return name // 禁前缀
	}
	if ns == "" {
		ns = e.ID + "_"
	}
	return ns + name
}

// entrySourcePrefix 返回第三方（Origin=user）条目的**来源别名前缀**（命名与唯一性：第三方条目
// 一律带别名，与内置 self_*、知识库条目区分）：<id>_；id 为空 → "srv_"。仅用于 applyPrefix
// 未产生前缀（namespace "-"）的兜底，builtin 不走本路径。
func entrySourcePrefix(e *ServerEntry) string {
	if e == nil || e.ID == "" {
		return "srv_"
	}
	return e.ID + "_"
}

// findFor 按 (instance, 暴露名) 查路由：先精确归属域（instance scoped 遮蔽），未命中回退 global；
// 再按引用集解析 dir 共享节点暴露名（RB-3 ②：路由不物化 N×K）。
func (r *registry) findFor(name, instance string) (*toolRoute, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if instance != scopeGlobal {
		if rt, ok := r.routes[routeKey(instance, name)]; ok {
			return rt, true
		}
	}
	if rt, ok := r.routes[routeKey(scopeGlobal, name)]; ok {
		return rt, true
	}
	return r.findDirLocked(name, instance)
}

// findDirLocked 是 dir 暴露名解析（RB-3 ②）：**前缀 → instance+node+tool → 查 refs 校验 →
// 命中共享 node**。持锁调用；scoped 引用遮蔽 global 引用（与 routes 表同规则）。
func (r *registry) findDirLocked(name, instance string) (*toolRoute, bool) {
	scopes := []string{scopeGlobal}
	if instance != scopeGlobal {
		scopes = []string{instance, scopeGlobal}
	}
	for _, scope := range scopes {
		for _, sn := range r.shared {
			for _, ref := range sn.refs {
				if ref.scope != scope {
					continue
				}
				orig, ok := dirOriginalName(ref, name)
				if !ok {
					continue
				}
				t, ok := sn.byName[orig]
				if !ok {
					continue
				}
				return dirRoute(ref, t, name), true
			}
		}
	}
	return nil, false
}

// find 按全局暴露名查路由（global 空间；兼容无 instance 语境）。
func (r *registry) find(name string) (*toolRoute, bool) { return r.findFor(name, scopeGlobal) }

// collectRoutes 汇总路由：all=true → 全量（UI 全量路径）；否则 global ∪ 归属 instance。
// dir 共享节点按其**引用集即时展开**（不物化进 routes 表：RB-3 ②），输出与「按 instance
// 物理复制」逐项等价（名/scope/来源节点均一致）。
func (r *registry) collectRoutes(instance string, all bool) []*toolRoute {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*toolRoute, 0, len(r.routes))
	for _, rt := range r.routes {
		if !all && rt.Scope != scopeGlobal && rt.Scope != instance {
			continue
		}
		out = append(out, rt)
	}
	for _, sn := range r.shared {
		for _, ref := range sn.refs {
			if !all && ref.scope != scopeGlobal && ref.scope != instance {
				continue
			}
			for _, t := range sn.tools {
				out = append(out, dirRoute(ref, t, dirExposedName(ref, t.Name)))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope+out[i].Name < out[j].Scope+out[j].Name })
	return out
}

// routesFor 按 instance 构建可见路由（global ∪ 归属 instance；含 dir 引用展开）——
// tools/list **按 instance 构建路径**（RB-3 ③；UI 全量路径 = routesAll）。
func (r *registry) routesFor(instance string) []*toolRoute { return r.collectRoutes(instance, false) }

// allToolsFor 返回某 instance 可见的工具聚合（global ∪ 归属该 instance；find 用；按名排序）。
func (r *registry) allToolsFor(instance string) []*mcp.Tool {
	routes := r.collectRoutes(instance, false)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	out := make([]*mcp.Tool, 0, len(routes))
	for _, rt := range routes {
		if t := toolWithName(rt); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// allTools 返回扁平聚合工具列表（全量，tools/list 计数用；按名排序）。
func (r *registry) allTools() []*mcp.Tool {
	routes := r.collectRoutes(scopeGlobal, true)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	out := make([]*mcp.Tool, 0, len(routes))
	for _, rt := range routes {
		if t := toolWithName(rt); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// routesAll 返回全部工具路由（含 scope/归属信息；list 全量输出用——UI 按 scope 分流）。
func (r *registry) routesAll() []*toolRoute { return r.collectRoutes(scopeGlobal, true) }

// nodeOf 返回 route 所属提供方的节点名（entry.ID，缺省 provider key）。
func (r *registry) nodeOf(providerKey string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ps, ok := r.providers[providerKey]; ok && ps.entry != nil && ps.entry.ID != "" {
		return ps.entry.ID
	}
	return providerKey
}

// provider 按 key 取提供方状态。
func (r *registry) provider(key string) (*providerState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps, ok := r.providers[key]
	return ps, ok
}

// providerKeys 返回全部提供方 key 副本（重载/停用遍历用）。
func (r *registry) providerKeys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, k)
	}
	return out
}

// setStatus 更新提供方状态。
func (r *registry) setStatus(key, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ps, ok := r.providers[key]; ok {
		ps.status = status
	}
}

// removeProvider 移除一个提供方及其全部工具路由（mcp/unregister 清理缓存用，
// 3A-工具与编排：注销 server = 断连 + 清理缓存）。返回移除的工具数。
func (r *registry) removeProvider(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	ps, ok := r.providers[key]
	if !ok {
		return 0
	}
	n := len(ps.tools)
	for _, rt := range ps.tools {
		delete(r.routes, routeKey(rt.Scope, rt.Name))
	}
	delete(r.providers, key)
	for i, k := range r.order {
		if k == key {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	// 复位提供方工具表（gateway/reload 原地重注册复用同一 ps 对象时防旧路由残留）
	ps.tools = nil
	return n
}

// ─── dir 共享节点 + 引用集（RB-3 ①②）───

// sharedNodeOf 按 node key 取共享能力节点（未登记 → false）。
func (r *registry) sharedNodeOf(key string) (*sharedNode, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sn, ok := r.shared[key]
	return sn, ok
}

// swapSharedNode 登记共享节点（并发竞态下保留先到者）：返回**实际生效**的节点
// （调用方据此决定是否丢弃自己新建的节点）。
func (r *registry) swapSharedNode(sn *sharedNode) *sharedNode {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ex := r.shared[sn.key]; ex != nil {
		return ex
	}
	r.shared[sn.key] = sn
	return sn
}

// addDirRef 给共享节点加一条归属引用，并登记该引用的提供方（视图/状态留在 providers；
// 工具**不进 routes 表**）。同名「(scope, 暴露名)」冲突按既有语义报错（含与既有非 dir 路由判重）。
func (r *registry) addDirRef(sn *sharedNode, ref *dirRef, ps *providerState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.refs[ref.pk]; dup {
		return fmt.Errorf("dir node %s already registered", ref.pk)
	}
	for _, t := range sn.tools {
		exposed := dirExposedName(ref, t.Name)
		if existing, dup := r.routes[routeKey(ref.scope, exposed)]; dup {
			return fmt.Errorf("tool 名冲突 %q（%s 与 %s，scope=%q）", exposed, existing.Provider, ref.pk, ref.scope)
		}
	}
	sn.refs = append(sn.refs, ref)
	r.refs[ref.pk] = ref
	if _, ok := r.providers[ref.pk]; !ok {
		r.order = append(r.order, ref.pk)
	}
	r.providers[ref.pk] = ps
	return nil
}

// removeDirRef 回收一条引用（RB-3 ①「注销回收引用」）：返回该引用侧暴露工具数 + 节点句柄 +
// 是否已无引用（release=true → 调用方关闭并释放共享节点）。
func (r *registry) removeDirRef(pk string) (n int, node *memNode, release bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ref, ok := r.refs[pk]
	if !ok {
		return 0, nil, false
	}
	delete(r.refs, pk)
	sn := r.shared[ref.nodeKey]
	if sn == nil {
		return 0, nil, false
	}
	n = len(sn.tools)
	for i, ex := range sn.refs {
		if ex == ref {
			sn.refs = append(sn.refs[:i], sn.refs[i+1:]...)
			break
		}
	}
	if len(sn.refs) > 0 {
		return n, sn.node, false // 仍有其它引用 → 保留共享节点
	}
	delete(r.shared, ref.nodeKey)
	return n, sn.node, true
}

// dropSharedIfUnused 释放在此刻**已无任何引用**的共享节点（引用添加失败的回滚路径）：
// 返回被移除的节点句柄（nil = 未移除；调用方负责关闭）。
func (r *registry) dropSharedIfUnused(key string) *memNode {
	r.mu.Lock()
	defer r.mu.Unlock()
	sn := r.shared[key]
	if sn == nil || len(sn.refs) > 0 {
		return nil
	}
	delete(r.shared, key)
	return sn.node
}

// refreshSharedNode 重拉共享节点的 tools/list（gateway/reload 对 dir 节点：不重建路由）。
func (r *registry) refreshSharedNode(ref *dirRef, tools []*mcp.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sn := r.shared[ref.nodeKey]
	if sn == nil {
		return
	}
	byName := make(map[string]*mcp.Tool, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
	}
	sn.tools = tools
	sn.byName = byName
}

// ─── 目录资产（agent/prompt/skill/resource，供 mcp_find/mcp_load）───

// putAsset 注册一个资产条目（(scope, kind, name) 内唯一；scope 空 = global）。
func (r *registry) putAsset(a *catalogAsset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := assetKey(a.Scope, a.Kind, a.Name)
	if _, dup := r.assets[key]; dup {
		return fmt.Errorf("asset %q (kind=%s, scope=%q) already registered", a.Name, a.Kind, a.Scope)
	}
	r.assets[key] = a
	return nil
}

// getAssetFor 按 (kind, name, instance) 取资产：先精确归属域（instance scoped 遮蔽），未命中回退 global。
func (r *registry) getAssetFor(kind, name, instance string) (*catalogAsset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if instance != scopeGlobal {
		if a, ok := r.assets[assetKey(instance, kind, name)]; ok {
			return a, true
		}
	}
	a, ok := r.assets[assetKey(scopeGlobal, kind, name)]
	return a, ok
}

// getAsset 按 (kind, name) 取全局资产（兼容无 instance 语境）。
func (r *registry) getAsset(kind, name string) (*catalogAsset, bool) {
	return r.getAssetFor(kind, name, scopeGlobal)
}

// delAssetFor 移除 (scope, kind, name) 资产条目。
func (r *registry) delAssetFor(scope, kind, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.assets, assetKey(scope, kind, name))
}

// delAsset 移除全局 (kind, name) 资产条目。
func (r *registry) delAsset(kind, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.assets, assetKey(scopeGlobal, kind, name))
}

// assetsByKind 列出资产（kind 空 = 全部；按 kind+name 排序）。
func (r *registry) assetsByKind(kind string) []*catalogAsset {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*catalogAsset
	for _, a := range r.assets {
		if kind != "" && a.Kind != kind {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// serverInfoOf 返回 provider 的 server 归属信息（2026-09-11 工具面契约变更：挂进工具
// `_meta.server`，让 UI/LLM 一眼知道"这工具属于哪台 server"，不必再查 servers/get）。
// key = provider key（toolRoute.Provider）。输出 {alias, node, category, description[, url]}；
// alias = server 别名（entry.ID，缺省 provider key），url 仅 proxied/有端点时出现。
func (r *registry) serverInfoOf(providerKey string) map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps, ok := r.providers[providerKey]
	if !ok {
		return nil
	}
	info := map[string]any{
		"alias":       providerKey,
		"node":        providerKey,
		"category":    "",
		"description": "",
	}
	if ps.entry != nil {
		if ps.entry.ID != "" {
			info["alias"] = ps.entry.ID
			info["node"] = ps.entry.ID
		}
		if ps.entry.Category != "" {
			info["category"] = ps.entry.Category
		}
		if ps.entry.Description != "" {
			info["description"] = ps.entry.Description
		}
		if ps.entry.URL != "" {
			info["url"] = ps.entry.URL
		}
	}
	return info
}

// serverView 返回按 server 分组的能力视图（servers/list / 管理 REST）。
type serverView struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"` // dir / spawned / proxied
	Category    string      `json:"category,omitempty"`
	Description string      `json:"description,omitempty"`
	Status      string      `json:"status"`
	Tools       []toolBrief `json:"tools"`
}

type toolBrief struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (r *registry) serverViews(filter string) []serverView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []serverView
	for _, key := range r.order {
		ps := r.providers[key]
		if filter != "" && ps.key != filter && (ps.entry == nil || ps.entry.ID != filter) {
			continue
		}
		v := serverView{Name: key, Type: strings.SplitN(key, ":", 2)[0], Status: ps.status, Tools: []toolBrief{}}
		if ps.entry != nil {
			v.Name = ps.entry.ID
			v.Category = ps.entry.Category
			v.Description = ps.entry.Description
			if v.Name == "" {
				v.Name = key
			}
		}
		if ps.ref != nil {
			// dir 共享节点（RB-3 ①）：工具按引用集即时展开（不物化 routes），按暴露名排序。
			if sn := r.shared[ps.ref.nodeKey]; sn != nil {
				for _, t := range sn.tools {
					v.Tools = append(v.Tools, toolBrief{Name: dirExposedName(ps.ref, t.Name), Description: t.Description})
				}
				sort.Slice(v.Tools, func(i, j int) bool { return v.Tools[i].Name < v.Tools[j].Name })
			}
		} else {
			for _, rt := range ps.tools {
				v.Tools = append(v.Tools, toolBrief{Name: rt.Name, Description: rt.Tool.Description})
			}
		}
		out = append(out, v)
	}
	return out
}
