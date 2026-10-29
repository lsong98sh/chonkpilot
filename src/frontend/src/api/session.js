import mq from '../utils/mq'
import { resolvePublishError } from '../utils/publishError.js'
import {
  FieldKeys,
  DataSessionListKeys, DataSessionGetKeys, DataSessionHistoryKeys,
  DataSessionLoadMessagesKeys, DataSessionContentKeys,
} from '../events/msgkeys.js'

// 一树一库（P3-4b）：进程内备忘 `session→dialog` / `turn→session`，来源 = 应答里的 `dialog_id`
// 与 history 轮次行。**非权威**（持久化真相在 persist）；未命中 → 不写键（旧布局兼容）。
const dialogBySession = new Map()
const sessionByTurn = new Map()

function rememberDialog(sessionId, dialogId) {
  if (sessionId && dialogId) dialogBySession.set(sessionId, dialogId)
}

function dialogOf(sessionId) {
  return (sessionId && dialogBySession.get(sessionId)) || ''
}

// dialogIdFor 取进程内备忘记下的 session→dialog（**非权威**；未命中返回空串）。
// 供需要显式带 `dialog_id` 的跨域载荷使用（61 §0.6；如 memory.flush）。
export function dialogIdFor(sessionId) {
  return dialogOf(sessionId)
}

function rememberTurns(turns) {
  if (!Array.isArray(turns)) return
  for (const t of turns) {
    if (t && t.turn_id && t.session_id) sessionByTurn.set(t.turn_id, t.session_id)
  }
}

// withDialog 给请求体补 `dialog_id`（显式带值优先；按 session_id / id / turn_id 反查）。
function withDialog(body) {
  if (!body || body.dialog_id) return body
  const sid = body.session_id || body.id || (body.turn_id ? sessionByTurn.get(body.turn_id) : '')
  const dlg = dialogOf(sid)
  if (dlg) body.dialog_id = dlg
  return body
}

// data-session-* 消息辅助：发送并等待回复（E-25：统一 30s 超时，对齐 fileRequest/guiReq 口径，
// 后端卡死时 fetch abort → resolve(null) → 按 backend unreachable 报错，不永久挂起）。
function sessionReqOnce(action, body) {
  return mq.emit(`data-session-${action}`, body, { timeout: 30000 }).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(`data-session-${action}: backend unreachable`)
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = resolvePublishError(backend) || (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) {
      const e = new Error(emsg || `data-session-${action} failed`)
      e.action = action
      throw e
    }
    return p
  })
}

// sessionReq = 请求 + **dialog_id 自愈**：一树一库默认生效，但前端备忘（dialogBySession）可能
// 尚未预热（页面刚打开 / 会话行由外部夹具直接落库 / 进程重启）。此时 persist 明确回
// `dialog_required` —— 这里先拉一次会话列表补备忘；若会话行本就不存在（外部事件直接切到
// 新 session_id），对**只读**动作再幂等 `ensure-session` 一次（自举对话库），然后原样重试。
// 不新增任何 MQ 主题；delete/title 等写动作**不**走 ensure（避免把已删会话复活）。
const READ_ACTIONS = new Set(['history', 'load-messages', 'content', 'get'])

async function sessionReq(action, body = {}) {
  withDialog(body)
  try {
    return await sessionReqOnce(action, body)
  } catch (e) {
    const msg = String((e && e.message) || '')
    if (action === 'list' || body.dialog_id || !msg.includes('dialog_required')) throw e
    let sid = body.session_id || body.id || (body.turn_id ? sessionByTurn.get(body.turn_id) : '')
    const list = await sessionReqOnce('list', {}).then((r) => r[DataSessionListKeys.list] || []).catch(() => [])
    for (const row of list) {
      rememberDialog(row.session_id || row.id, row.dialog_id)
      if (!sid) sid = row.session_id || row.id
    }
    withDialog(body)
    if (!body.dialog_id && sid && READ_ACTIONS.has(action)) {
      const r = await sessionReqOnce('ensure-session', { [FieldKeys.session_id]: sid }).catch(() => null)
      rememberDialog(sid, r && r.dialog_id)
      withDialog(body)
    }
    if (!body.dialog_id) throw e
    return sessionReqOnce(action, body)
  }
}

export function listSessions() {
  return sessionReq('list').then((r) => {
    const list = r[DataSessionListKeys.list] || []
    // shard=on 时列表项自带 dialog_id（每对话一行）；顺带预热备忘，后续请求免再查。
    for (const row of list) rememberDialog(row.session_id || row.id, row.dialog_id)
    return list
  })
}

export function getSession(id) {
  return sessionReq('get', { id }).then((r) => {
    const d = r[DataSessionGetKeys.data]
    if (d) rememberDialog(d.session_id || d.id || id, d.dialog_id)
    return d
  })
}

export function deleteSession(id) {
  return sessionReq('delete', { id }).then(() => {})
}

export function updateSessionTitle(id, title) {
  return sessionReq('title', { id, title }).then(() => {})
}

// ensureSession 幂等建会话行（data-session-ensure-session，61 §3.2a）：开对话窗口前须先使会话在
// DB 中**存在**（否则标题/列表不一致，24 §6.4；session_id 由前端分配）。
export function ensureSession(id, parentId) {
  const body = { [FieldKeys.session_id]: id }
  if (parentId) body[FieldKeys.parent_session_id] = parentId
  return sessionReq('ensure-session', body).then((r) => {
    // shard=on 时根会话自举会回带 dialog_id；记下供后续请求使用。
    rememberDialog(id, r && r.dialog_id)
  })
}

// dialogId（可选，一树一库）：子会话（任务详情 viewer）不在一树一库的顶层目录里
// （`data-session-list` 只列主会话）→ 调用方须显式带**所属主会话的 dialog_id**，
// 否则 `sessionReq` 自愈会为子会话 id 自举出一个空对话库（读不到既有消息）。
export function getTurnsPaginated(sessionId, beforeTurnId, targetMessages = 50, targetBytes = 200 * 1024,
                                   dialogId = '') {
  const body = {
    [FieldKeys.session_id]: sessionId,
    [FieldKeys.before_turn_id]: beforeTurnId || '',
    [FieldKeys.target_messages]: targetMessages,
    [FieldKeys.target_bytes]: targetBytes,
  }
  if (dialogId) body.dialog_id = dialogId
  return sessionReq('history', body).then((r) => {
    const page = r[DataSessionHistoryKeys.messages] || { turns: [], messages: [], has_more: false }
    rememberTurns(page.turns) // turn→session 备忘（load-messages 仅带 turn_id）
    return page
  })
}

// listAllTurns 取会话**全部**轮次（升序）——供「复制会话对话」逐轮读原文。
// data-session-history 应答外形 = `{messages:{turns:[…], messages:[…], has_more}}`（逐字既有，
// 见 wire.TurnHistoryResult）；turn 主键 = `turn_id`（wire.TurnToWire）。每次给足上限；
// 服务端仍可能回 has_more=true → 以 before_turn_id 向前翻页补齐。
export async function listAllTurns(sessionId) {
  const out = []
  let before = ''
  for (let i = 0; i < 500; i++) {
    const r = await sessionReq('history', {
      [FieldKeys.session_id]: sessionId,
      [FieldKeys.before_turn_id]: before,
      [FieldKeys.target_messages]: 1000000,
      [FieldKeys.target_bytes]: 1000000000,
    })
    const page = (r && r[DataSessionHistoryKeys.messages]) || {}
    const turns = Array.isArray(page.turns) ? page.turns : []
    rememberTurns(turns)
    out.unshift(...turns) // 服务端按「最新往回」取页 → 前插维持升序
    if (page.has_more !== true || turns.length === 0) break
    before = turns[0].turn_id
  }
  return out
}

// getTurnMessages 读某轮次全部消息（data-session-load-messages，61 §3.2）→ 原始 ChatMsg 数组
// （LLM 线格式；复制口径见 utils/sessionCopy.js）。
export function getTurnMessages(turnId) {
  return sessionReq('load-messages', { [FieldKeys.turn_id]: turnId }).then((r) => (Array.isArray(r[DataSessionLoadMessagesKeys.messages]) ? r[DataSessionLoadMessagesKeys.messages] : []))
}

// 供 MessageItem 动态 import 后按 key 拉取完整内容（截断消息展开/复制）
export function getMessageContent(sessionId, keys) {
  return sessionReq('content', { [FieldKeys.session_id]: sessionId, [FieldKeys.keys]: keys }).then((p) => p[DataSessionContentKeys.contents] || {})
}

export function getLatestSessionID() {
  return sessionReq('latest').then((r) => {
    if (r && r.session_id) rememberDialog(r.session_id, r.dialog_id)
    return r
  })
}

// sessionDialogOf 读进程内备忘的 dialog_id（未命中 → ''）。供 tasktree 等其它域拼请求用。
export function sessionDialogOf(sessionId) {
  return dialogOf(sessionId)
}

// forkSession 派生对话（data-session-fork，61 §3.2；SESSION-COMP-1 P4）：
// `dialogId` = 源对话（一树一库，必填）；`mode` 缺省 full（整树拷贝 + id 重映射），
// context = 只建主会话 + 继承源快照（不拷文件）。回带新 `{dialog_id, session_id}`。
export function forkSession(dialogId, { mode = '', title = '' } = {}) {
  const body = { dialog_id: dialogId }
  if (mode) body.mode = mode
  if (title) body.title = title
  return sessionReq('fork', body).then((r) => {
    rememberDialog(r.session_id, r.dialog_id)
    return r
  })
}

export function getActiveSessionID() {
  return sessionReq('active-get').then((r) => r)
}

export function setActiveSessionID(sessionId) {
  return sessionReq('active-set', { [FieldKeys.session_id]: sessionId }).then(() => {})
}