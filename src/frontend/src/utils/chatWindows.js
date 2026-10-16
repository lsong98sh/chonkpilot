/**
 * 对话窗口「入口态」纯逻辑（24-多窗口模型设计方案 §1.1 不变量 I-1 / §6.4 T-1…T-5）。
 *
 * 数据源（61 §1）：
 *   - `gui.window.list` → `{windows:[{window_id, session_id}]}`（**只含对话窗口**，主窗口不入注册表）；
 *   - `gui.window.closed` → `{window_id, session_id}`（解除占用）；
 *   - `gui.window.open-chat {session_id}` → `{ok, window_id, activated}`（**幂等**：已开即激活）。
 *
 * 判定优先级（= I-1，本模块是唯一实现处）：
 *   ① 该 session = 主窗口当前活动会话 → **拒开**（入口置灰；**判定在前端**，宿主不追踪主窗会话，
 *      见 42 §2 (161) 拍板 A）；
 *   ② 该 session 已被某对话窗口绑定 → **激活**该窗口（不新建，也不切主窗口）；
 *   ③ 已达上限（独立对话窗口 ≤5，不含主窗口）→ **拒开**（入口置灰）；
 *   ④ 其余 → 建新窗。
 *
 * 本模块为**叶子模块**（无 import、无副作用）→ 可直跑单测。
 */

/** 独立对话窗口数量上限（24 §8 U-5 已决：≤5，不含主窗口；与宿主 maxChatWindows 同值）。 */
export const MAX_CHAT_WINDOWS = 5

/**
 * 群聊窗口**单独**数量上限（群聊 §9 D28/D30 · N13/N17：独立额度 5，**不与普通聊天窗口联动**；
 * 与宿主 `maxGroupChatWindows` 同值）。
 */
export const MAX_GROUP_CHAT_WINDOWS = 5

/** 群聊会话类型（与后端 `sessions.kind` 同值；本模块为叶子模块 → 本地声明，不 import 其它模块）。 */
export const GROUP_CHAT_KIND = 'group_chat'

/** 入口动作：建窗 / 激活已开窗 / 置灰。 */
export const CHAT_ENTRY_OPEN = 'open'
export const CHAT_ENTRY_ACTIVATE = 'activate'
export const CHAT_ENTRY_DISABLED = 'disabled'

/**
 * 该 session 已绑定的对话窗口 id（未开 → ''）。
 * @param {Array<{window_id:string, session_id:string}>} windows gui.window.list 结果
 * @param {string} sessionId 目标会话
 */
export function windowForSession(windows, sessionId) {
  if (!sessionId || !Array.isArray(windows)) return ''
  const hit = windows.find((w) => w && w.session_id === sessionId)
  return (hit && hit.window_id) || ''
}

/** 是否已达对话窗口上限（达上限 → 新建入口置灰；`open-chat` 亦会返回 ok=false）。 */
export function atChatWindowLimit(windows, max = MAX_CHAT_WINDOWS) {
  return Array.isArray(windows) && windows.length >= max
}

/** 归一 kindOf（`sessionId => kind`；非法入参 → 恒空串 = 按普通窗口计，保持既有行为）。 */
function kindResolver(kindOf) {
  return typeof kindOf === 'function' ? kindOf : () => ''
}

/** 已开窗口里**群聊窗口**数量（`kindOf` = 会话 id → `kind`）。 */
export function countGroupChatWindows(windows, kindOf) {
  if (!Array.isArray(windows)) return 0
  const f = kindResolver(kindOf)
  return windows.filter((w) => w && f(w.session_id) === GROUP_CHAT_KIND).length
}

/** 已开窗口里**普通对话窗口**数量（单聊 `sub_chat` / 普通会话；群聊**不计入**）。 */
export function countNormalChatWindows(windows, kindOf) {
  if (!Array.isArray(windows)) return 0
  const f = kindResolver(kindOf)
  return windows.filter((w) => w && f(w.session_id) !== GROUP_CHAT_KIND).length
}

/** 是否已达群聊窗口单独上限（群聊 §9 D28/D30）。 */
export function atGroupChatWindowLimit(windows, kindOf, max = MAX_GROUP_CHAT_WINDOWS) {
  return countGroupChatWindows(windows, kindOf) >= max
}

/**
 * 计算某会话「开新窗」入口的状态（T-1/T-2 只用到 limit；T-3 另用 mainSessionId 判 I-1 ①）。
 * @param {object} o
 * @param {Array} o.windows gui.window.list 结果（只含对话窗口）
 * @param {string} o.sessionId 目标会话（新会话场景由前端分配后再调用）
 * @param {string} o.mainSessionId 主窗口**当前活动会话**（对话窗口内不传/传空）
 * @param {number} o.max 上限（缺省 MAX_CHAT_WINDOWS）
 * @param {number} [o.count] 已占额度（缺省 = `windows.length`）。群聊按**独立额度**计数时传入
 *   `countGroupChatWindows(...)`，普通窗口传 `countNormalChatWindows(...)`——不传则行为不变。
 * @returns {{action:'open'|'activate'|'disabled', reason:''|'no-session'|'main-active'|'limit', windowId:string}}
 */
export function chatEntryState({ windows = [], sessionId = '', mainSessionId = '', max = MAX_CHAT_WINDOWS, count = null } = {}) {
  if (!sessionId) return { action: CHAT_ENTRY_DISABLED, reason: 'no-session', windowId: '' }
  // I-1 ①：主窗口当前活动会话不可开新窗口（判定在前端）
  if (mainSessionId && sessionId === mainSessionId) {
    return { action: CHAT_ENTRY_DISABLED, reason: 'main-active', windowId: '' }
  }
  // I-1 ②：已绑定对话窗口 → 激活（幂等）
  const windowId = windowForSession(windows, sessionId)
  if (windowId) return { action: CHAT_ENTRY_ACTIVATE, reason: 'opened', windowId }
  // ③：达上限 → 置灰（按调用方给出的额度口径计数）
  const used = typeof count === 'number' ? count : (Array.isArray(windows) ? windows.length : 0)
  if (used >= max) return { action: CHAT_ENTRY_DISABLED, reason: 'limit', windowId: '' }
  return { action: CHAT_ENTRY_OPEN, reason: '', windowId: '' }
}

/**
 * 打开 `open-chat` 成功后把新窗口并入本地清单（**幂等**：已存在则原样返回，不重复登记）。
 * `activated=true`（命中已开窗）不新增条目。
 */
export function mergeOpenedWindow(windows, { window_id: windowId, session_id: sessionId, activated } = {}) {
  const list = Array.isArray(windows) ? windows : []
  if (activated || !windowId || !sessionId) return list
  if (list.some((w) => w && (w.window_id === windowId || w.session_id === sessionId))) return list
  return [...list, { window_id: windowId, session_id: sessionId }]
}

/** 窗口关闭（gui.window.closed）→ 从本地清单移除（解除「已打开」态）。 */
export function removeClosedWindow(windows, { window_id: windowId, session_id: sessionId } = {}) {
  const list = Array.isArray(windows) ? windows : []
  return list.filter((w) => w && w.window_id !== windowId && w.session_id !== sessionId)
}
