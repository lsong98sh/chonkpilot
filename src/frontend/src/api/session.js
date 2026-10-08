import mq from '../utils/mq'
import {
  FieldKeys,
  DataSessionListKeys, DataSessionGetKeys, DataSessionHistoryKeys,
  DataSessionLoadMessagesKeys, DataSessionContentKeys,
} from '../events/msgkeys.js'

// data-session-* 消息辅助：发送并等待回复。
function sessionReq(action, body = {}) {
  return mq.emit(`data-session-${action}`, body).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(`data-session-${action}: backend unreachable`)
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) {
      const e = new Error(emsg || `data-session-${action} failed`)
      e.action = action
      throw e
    }
    return p
  })
}

export function listSessions() {
  return sessionReq('list').then((r) => r[DataSessionListKeys.list] || [])
}

export function getSession(id) {
  return sessionReq('get', { id }).then((r) => r[DataSessionGetKeys.data])
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
  return sessionReq('ensure-session', body).then(() => {})
}

export function getTurnsPaginated(sessionId, beforeTurnId, targetMessages = 50, targetBytes = 200 * 1024) {
  return sessionReq('history', {
    [FieldKeys.session_id]: sessionId,
    [FieldKeys.before_turn_id]: beforeTurnId || '',
    [FieldKeys.target_messages]: targetMessages,
    [FieldKeys.target_bytes]: targetBytes,
  }).then((r) => r[DataSessionHistoryKeys.messages] || { turns: [], messages: [], has_more: false })
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
  return sessionReq('latest').then((r) => r)
}

export function getActiveSessionID() {
  return sessionReq('active-get').then((r) => r)
}

export function setActiveSessionID(sessionId) {
  return sessionReq('active-set', { [FieldKeys.session_id]: sessionId }).then(() => {})
}