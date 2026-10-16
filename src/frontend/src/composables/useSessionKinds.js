/**
 * useSessionKinds —— 会话 `kind` 的**只读批量取数 + 进程内缓存**（前端视图分派与窗口额度共用）。
 *
 * 为什么需要：`subchat-list` 的结果项**没有 `kind`**（61 §4.2 载荷只有
 * `{session_id, purpose, title, lifecycle, parent_*, parent_session_title}`），而
 * **单聊 / 群聊图标区分**（群聊 §4 D41）与**群聊窗口单独额度**（群聊 §9 D28/D30）都要它。
 * 取数走既有只读消息 `data-session-get`（零消息面改动），结果进程内缓存 → 同一会话只往返一次。
 */
import { FieldKeys } from '../events/msgkeys.js'
import { getSession } from '../api/session'

/** 会话 id → `kind` 缓存（一次取到即长存；`kind` 建成后不变）。 */
const kindCache = new Map()

/**
 * 批量取会话 `kind`（只补未缓存者）。
 * @param {string[]} sessionIds
 * @returns {Promise<Map<string, string>>} id → kind（取不到 → 空串）
 */
export async function loadSessionKinds(sessionIds) {
  const ids = [...new Set((sessionIds || []).filter(Boolean))]
  const missing = ids.filter((id) => !kindCache.has(id))
  await Promise.all(missing.map(async (id) => {
    try {
      const s = await getSession(id)
      kindCache.set(id, (s && s[FieldKeys.kind]) || '')
    } catch (_) {
      kindCache.set(id, '') // 取不到按普通（非群聊）→ 不阻塞列表 / 不改变既有额度判定
    }
  }))
  return kindCache
}

/** 取已缓存的 `kind`（未取过 → 空串）。 */
export function cachedSessionKind(sessionId) {
  return kindCache.get(sessionId) || ''
}

/** 取会话 id → kind 的平铺对象（供纯逻辑模块按 `session_id` 计数）。 */
export function sessionKindMap(sessionIds) {
  const out = {}
  for (const id of [...new Set((sessionIds || []).filter(Boolean))]) {
    out[id] = cachedSessionKind(id)
  }
  return out
}
