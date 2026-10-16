/**
 * useSubChats —— 子聊天（`subchat-*` 业务消息）前端**接线层**（取数 / 动作 / 启动提醒）。
 *
 * 设计依据：`docs/agent-chat/单聊会话设计方案.md` §3.4.4（状态承载）· §3.4.5（状态条）·
 * §3.4.6（回看入口）· §3.4.7（启动提醒）· §3.7（生命周期）· §3.8 F1–F10；
 * 消息面契约 = `docs/spec/60-reference/61-消息一览.md` §4.2 / §4.3（**载荷零改动**）。
 *
 * 消息面（均为**同名相对主题**）：
 *   - `subchat-list {parent_id?}` → `{list:[{session_id, purpose, title, lifecycle,
 *     parent_session_id, parent_session_title}]}`：`parent_id` 有值 → 该父会话**全部**子聊天
 *     （**含 `finished`**，前端自取所需过滤）；缺省 → **全局未结束**子聊天（启动提醒）。
 *   - `subchat-finish {session_id}` → `{ok}`：用户拍板结束 → `lifecycle=finished`。
 *   - `subchat-opened` / `subchat-state`（下行，server→client）：状态变更信号——订阅由
 *     `SubChatBar.vue`（状态条刷新 / 点亮）与 `ChatPanel.vue`（F2 自动开窗 / F9 只读）各自持有。
 *
 * 纯逻辑（kind/lifecycle 判定与文案）见 `utils/subchat.js`（叶子模块）。
 */
import { h } from 'vue'
import { useI18n } from 'vue-i18n'
import mq from '../utils/mq'
import { dialog } from '../components/dialog'
import { message } from '../components/ui'
import {
  MsgTopics,
  FieldKeys,
  SubchatListKeys,
  SubchatFinishKeys,
} from '../events/msgkeys.js'
import { isGroupChatKind } from '../utils/groupchat.js'
import { cachedSessionKind, loadSessionKinds } from './useSessionKinds.js'
import SubChatStartupDialog from '../views/chat/SubChatStartupDialog.vue'

/**
 * 批量补齐子聊天的会话类型（`sessions.kind`）——**单聊 / 群聊图标区分**依据（群聊 §4 D41）。
 * `subchat-list` 结果项**无 `kind`** → 经 `useSessionKinds`（既有只读 `data-session-get` + 进程内
 * 缓存）补；**零消息面改动**。
 * @param {string[]} sessionIds
 * @returns {Promise<Map<string, string>>} id → kind（取不到 → 空串）
 */
export const loadSubChatKinds = loadSessionKinds

/** 取已缓存的 `kind`（未取过 → 空串）。 */
export const cachedSubChatKind = cachedSessionKind

/** 给子聊天清单逐项补 `kind`（不新增消息面字段，仅前端视图行加一个键）。 */
async function attachKinds(list) {
  await loadSessionKinds(list.map((it) => it[FieldKeys.session_id]))
  return list.map((it) => ({ ...it, [FieldKeys.kind]: cachedSessionKind(it[FieldKeys.session_id]) }))
}

/**
 * 取子聊天清单（61 §4.2 `subchat-list`）。
 * @param {string} parentId 父会话 id；**空串 = 全局未结束**（启动提醒用，§3.4.7）。
 * @returns {Promise<Array>} 每项 `{session_id, purpose, title, lifecycle, parent_session_id,
 *   parent_session_title, kind}`（`kind` = 前端补的会话类型，见 `loadSubChatKinds`）
 */
export async function listSubChats(parentId = '') {
  const body = {}
  if (parentId) body[FieldKeys.parent_id] = parentId
  const env = await mq.emit(MsgTopics.subchatList, body, { timeout: 30000 })
  const backend = env && env.backend
  if (!backend || !backend.ok) throw new Error('subchat-list: backend unreachable')
  const r = backend.result && typeof backend.result === 'object' ? backend.result : {}
  if (r.error) throw new Error(String(r.error))
  const list = r[SubchatListKeys.list]
  if (!Array.isArray(list)) return []
  return attachKinds(list)
}

/**
 * 用户拍板结束某子聊天（61 §4.2 `subchat-finish`）——窗口内「结束」与状态条「强制结束」**同一条
 * 消息**（§3.5 / §3.8 F5）；**不区分**正常完成 / 强制中止（D25）。
 * @returns {Promise<boolean>} 服务端是否受理（`result.ok`）
 */
export async function finishSubChat(sessionId) {
  if (!sessionId) return false
  const env = await mq.emit(MsgTopics.subchatFinish, { [FieldKeys.session_id]: sessionId }, { timeout: 30000 })
  const r = (env && env.backend && env.backend.result) || {}
  return r[SubchatFinishKeys.ok] === true
}

/**
 * 打开 / 前置子聊天窗口（`gui.window.open-chat`；幂等：未开建窗、已开 `bringTop`，24 §6.4）。
 * @returns {Promise<boolean>} 是否成功（撞 5 窗口上限 → false，§6 E5）
 */
export async function openSubChatWindow(sessionId) {
  if (!sessionId) return false
  // 动态 import：与 useChatWindows（模块级单例）解耦，避免加载顺序耦合。
  const { openChat } = await import('./useChatWindows')
  const r = await openChat(sessionId)
  return !!(r && r.ok)
}

// ── F10 · 启动提醒对话框（§3.4.7 / D38）────────────────────────────────
// 「打开 exe、主窗口就绪后判断一次」：存在未结束子聊天 → 弹对话框列出（每次启动都判断，
// 不记"已提醒过"状态 → 无需新增存储）；本页会话内只判一次（多条初始化路径不重弹）。
let startupChecked = false

/** 启动提醒：全局未结束子聊天 → 弹对话框；无 → 不弹（**不做**「全部打开」「终止」，§3.4.7）。 */
export async function maybeShowUnfinishedSubChats() {
  if (startupChecked) return
  startupChecked = true
  let items = []
  try {
    items = await listSubChats('')
  } catch (e) {
    console.warn('[subchat] startup list failed:', e)
    return
  }
  if (!items || items.length === 0) return

  const { t } = useI18n()
  let handle = null
  const close = () => { try { handle && handle.close() } catch (_) { /* noop */ } }
  handle = dialog.show(h(SubChatStartupDialog, {
    items,
    onOpen: (item) => {
      close()
      openSubChatWindow(item[FieldKeys.session_id]).then((ok) => {
        // 撞上限 → 按会话类型给**不同**文案（群聊独立额度，群聊 §9 D28/D30 · 阶段 G G3）。
        if (!ok) message.warning(t(isGroupChatKind(item[FieldKeys.kind])
          ? 'chat.group_chat_window_limit'
          : 'chat.chat_window_limit'))
      })
    },
  }), {
    title: t('chat.subchat_startup_title', { n: items.length }),
    width: 560,
    height: 420,
    closable: true,
  })
}
