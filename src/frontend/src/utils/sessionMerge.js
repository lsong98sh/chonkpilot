/**
 * 会话消息合并（历史回填 vs 本地流式新增）——**唯一去重口径**。
 *
 * 背景（2026-11-07 真机复现）：tool_pair 的本地流式条目 `role='assistant'`，而 DB 落库视图
 * `role='tool'`（`dbMsgToView`），二者 `id` 同为 `tp_<tool_call_id>`。旧合并按
 * `role + type + content` 判重 → role 恒不等 → 同一次工具调用在「流式中途/之后发生历史重载」
 * 时被渲染两张卡（同 `tp_<tool_call_id>`，Vue `:key` 重复）：
 *   DOM 实测 `.toolpair-section` 6 条 = 同 3 个标签各两次（一张 purpose 来自 DB，一张为空）。
 * 本模块按**消息身份**去重：同一 `id` 或同一 `tool_call_id` 的 tool_pair 只保留一份。
 */

/**
 * 历史快照 + 本地新增合并：历史在前、本地在后；与历史重复（同 id / 同 tool_call_id /
 * 同 role+type+content 的非工具消息）的本地条目丢弃。
 * @param {Array<object>} hist 历史视图（`dbMsgToView` 产物）
 * @param {Array<object>} locals 本地新增（`messages` 中 `localOnly=true` 的条目）
 * @returns {Array<object>} 合并后的新数组（不修改入参）
 */
export function mergeHistoryWithLocals(hist, locals) {
  const base = Array.isArray(hist) ? hist.slice() : []
  const histIds = new Set(base.map((m) => m && m.id).filter(Boolean))
  const histToolCalls = new Set(
    base.filter((m) => m && m.type === 'tool_pair' && m.tool_call_id).map((m) => m.tool_call_id),
  )
  const out = base
  for (const m of (Array.isArray(locals) ? locals : [])) {
    if (!m) continue
    // 同一消息（tool_pair 恒为 tp_<tool_call_id>）→ 历史已有一份，不再追加。
    if (m.id && histIds.has(m.id)) continue
    // id 缺失/历史视图 id 不同形态时的兜底：同一 tool_call_id 只允许一张工具卡。
    if (m.type === 'tool_pair' && m.tool_call_id && histToolCalls.has(m.tool_call_id)) continue
    // 非工具消息（乐观 user 气泡 / 流式文本段）：按既有「同角色+同类型+同内容」判重。
    const dup = base.some((h) => h && h.role === m.role && (h.type || '') === (m.type || '')
      && (h.content || '') === (m.content || ''))
    if (!dup) out.push(m)
  }
  return out
}

/**
 * 前插更早历史（loadMore）时去掉与现有列表重复的条目（保留现有条目/顺序）。
 * @param {Array<object>} existing 当前已渲染消息（含本地流式）
 * @param {Array<object>} incoming 更早一批历史视图
 * @returns {Array<object>} 可前插的条目（不含与 existing 重复的）
 */
export function withoutDuplicates(existing, incoming) {
  const cur = Array.isArray(existing) ? existing : []
  const ids = new Set(cur.map((m) => m && m.id).filter(Boolean))
  const toolCalls = new Set(
    cur.filter((m) => m && m.type === 'tool_pair' && m.tool_call_id).map((m) => m.tool_call_id),
  )
  return (Array.isArray(incoming) ? incoming : []).filter((m) => {
    if (!m) return false
    if (m.id && ids.has(m.id)) return false
    if (m.type === 'tool_pair' && m.tool_call_id && toolCalls.has(m.tool_call_id)) return false
    return true
  })
}