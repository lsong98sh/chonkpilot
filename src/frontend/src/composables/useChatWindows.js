/**
 * useChatWindows —— 纯对话窗口（多窗口）前端唯一接线层（24-多窗口模型设计方案 §6.4 T-1…T-5）。
 *
 * 消息面（61 §1；**payload 零新增字段**）：
 *   - `gui.window.list {}` → `{windows:[{window_id, session_id}]}`（**只含对话窗口**）；
 *   - `gui.window.open-chat {session_id}` → `{ok, window_id, activated}`（**幂等**：已开即激活）；
 *   - `gui.window.closed {window_id, session_id}`（宿主下行）→ 解除本地「已打开」态。
 *
 * 判定（置灰 / 激活 / 建窗）全部委托**纯模块** `utils/chatWindows.js`（可直跑单测）；
 * 本模块只负责**状态与消息接线**（模块级单例，主窗口的多个入口共享同一份已开窗口清单）。
 *
 * 「主窗口当前活动会话 → 拒开」的判定在**前端**（宿主不追踪主窗会话，42 §2 (161) 拍板 A）→
 * 调用方把主窗当前会话作为 `mainSessionId` 传入 `entryStateFor`。
 */
import { computed, ref } from 'vue'
import mq from '../utils/mq.js'
import { EventNames } from '../events/event-names'
import { newSessionId } from '../api/chat'
import { ensureSession } from '../api/session'
import {
  MAX_CHAT_WINDOWS,
  chatEntryState,
  mergeOpenedWindow,
  removeClosedWindow,
} from '../utils/chatWindows.js'

/** 已开对话窗口清单（模块级单例；只含对话窗口，主窗口不入注册表）。 */
const windows = ref([])

let subscribed = false

/** 订阅一次宿主下行广播（gui.window.closed）→ 窗口关闭即解除占用（T-3 恢复可点）。 */
function ensureSubscribed() {
  if (subscribed) return
  subscribed = true
  mq.on(EventNames.guiWindowClosed, (d) => {
    windows.value = removeClosedWindow(windows.value, d || {})
  })
}

/** 拉取已开对话窗口（初始置灰依据）。失败 → 保持现状（不阻塞入口）。 */
export async function refreshWindows() {
  try {
    const env = await mq.emit(EventNames.guiWindowList, {})
    const r = env && env.backend && env.backend.result
    windows.value = r && Array.isArray(r.windows) ? r.windows : []
  } catch (e) {
    console.warn('[chatWindows] gui.window.list failed:', e)
  }
  return windows.value
}

/**
 * 打开/激活纯对话窗口（幂等；`open-chat` 命中已开窗即激活，返回 `activated=true`）。
 * @returns {Promise<{ok:boolean, window_id?:string, activated?:boolean}>}
 */
export async function openChat(sessionId) {
  if (!sessionId) return { ok: false }
  const env = await mq.emit(EventNames.guiWindowOpenChat, { session_id: sessionId })
  const r = (env && env.backend && env.backend.result) || {}
  if (r.ok) {
    windows.value = mergeOpenedWindow(windows.value, {
      window_id: r.window_id,
      session_id: sessionId,
      activated: r.activated,
    })
  }
  return r
}

/**
 * T-1 / T-2：**新会话**开对话窗口 —— session_id 由前端分配（既有客户端分配语义），
 * 开窗前先 `session-ensure`（幂等建会话行，否则标题/列表不一致，24 §6.4）。
 */
export async function openNewChatWindow() {
  const sid = newSessionId()
  try {
    await ensureSession(sid)
  } catch (e) {
    console.warn('[chatWindows] ensure-session failed:', e)
    return { ok: false, reason: 'ensure-failed' }
  }
  return openChat(sid)
}

/** T-3 / T-4：对**已有会话**开窗（已开 → 激活，不新建）。 */
export function openSessionChatWindow(sessionId) {
  return openChat(sessionId)
}

/** 入口态（I-1 判定唯一入口；`mainSessionId` 仅主窗口传）。 */
export function entryStateFor(sessionId, mainSessionId = '') {
  return chatEntryState({
    windows: windows.value,
    sessionId,
    mainSessionId,
    max: MAX_CHAT_WINDOWS,
  })
}

export function useChatWindows() {
  ensureSubscribed()
  return {
    windows,
    atLimit: computed(() => windows.value.length >= MAX_CHAT_WINDOWS),
    refreshWindows,
    openChat,
    openNewChatWindow,
    openSessionChatWindow,
    entryStateFor,
  }
}
