/**
 * 工具来源判定（数据源 = 既有 `tools-list` 的每项 `_meta.server`；I-82/I-109 边界标注用）。
 *
 * 依据（**以代码为准**，chonkpilot-mcp-gateway/gateway）：
 *   - 每个 provider 有 `ServerEntry`：
 *       self 节点  = { ID:"self", Category:"core", Origin:builtin }（mcpgateway.go 建 self 节点）
 *       dir 节点   = { ID:<name>, Category:"dir",  Origin:builtin }（dirnode.go 建 dir 节点）
 *       用户 MCP   = usr `mcps` 条目（Origin:user；category 用户自定）
 *   - tools-list 每项 `_meta.server` = { alias, node, category, description[, url] }
 *     （registry.serverInfoOf）—— **不含 origin**；且 gateway 客户端面 `servers-list`
 *     已移除（bridge frontMethodSubjects 白名单不保留）→ 前端只能据 alias/node/category 判定。
 *
 * 边界（I-109）：agentbox 沙箱 / `tool_async.hard_timeout` **仅对 builtin（self/dir，
 * 本仓 spawn executor）可施加**；第三方（spawned/proxied）工具在第三方进程内执行 → 不可注入。
 * 判定原则：**无法判定（无 `_meta.server`）→ 返回 null（不标注，宁缺勿错）**。
 */

export const SELF_NODE_ID = 'self'
export const DIR_NODE_CATEGORY = 'dir'

// toolProvider 取 `_meta.server` 归属对象（缺省 null）。
export function toolProvider(meta) {
  const s = meta && meta.server
  return s && typeof s === 'object' ? s : null
}

// isBuiltinRuntimeProvider 是否本仓自有 runtime 提供方（self / dir 节点）。
// 无 `_meta.server` → null（无法判定）。
export function isBuiltinRuntimeProvider(meta) {
  const s = toolProvider(meta)
  if (!s) return null
  if (s.alias === SELF_NODE_ID || s.node === SELF_NODE_ID) return true
  if (s.category === DIR_NODE_CATEGORY) return true
  return false
}

// isDirNode 是否 dir 节点工具（`_meta.server.category === "dir"`）。
export function isDirNode(meta) {
  const s = toolProvider(meta)
  return !!s && s.category === DIR_NODE_CATEGORY
}

// isThirdPartyProvider 是否第三方（spawned/proxied）工具：有 server 归属且非 self/dir。
// 无 `_meta.server` → null（无法判定，不标注）。
export function isThirdPartyProvider(meta) {
  const builtin = isBuiltinRuntimeProvider(meta)
  if (builtin === null) return null
  return !builtin
}

/**
 * stripToolPrefix 剥离网关为工具暴露名加的前缀（**仅展示用**）——不改配置键 / `data-tool`。
 *
 * 生成规则（gateway `registry.go:288 applyPrefix`）= `ns + <原名>`，`ns` 默认 = `<节点 ID> + "_"`
 * （self 节点 ID="self" → `self_<契约名>`；dir 节点 → `<节点名>_<原名>`；`ns="-"` 才无前缀）。
 * `tools-list` 每项 `_meta.server.alias` / `node` = 节点 ID（`registry.serverInfoOf`）
 * → 反解 = 名字以 `<alias>_` 或 `<node>_` 开头则剥掉该前缀。
 *
 * 边界（宁缺勿错）：**无 `_meta.server`（或缺 alias/node）→ 原样返回**（不猜、不误剥）；
 * 网关 `Namespace` 被自定义（`_meta` 未透出）时前缀名不匹配 → 同样原样返回。
 * 重名可区分：各展示处保留 `:title="<暴露名>"`，hover 可见完整暴露名。
 */
export function stripToolPrefix(name, server) {
  const n = String(name || '')
  const id = server ? [server.alias, server.node] : []
  if (!n || id.length === 0) return n
  for (const raw of id) {
    const p = String(raw || '').trim()
    if (p && n.startsWith(p + '_')) return n.slice(p.length + 1)
  }
  return n
}
