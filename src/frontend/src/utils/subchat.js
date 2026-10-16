/**
 * 子聊天（`subchat-*`）前端**纯逻辑**（叶子模块：无 import、无副作用 → 可直跑单测）。
 *
 * 设计依据：`docs/agent-chat/单聊会话设计方案.md` §3.4.4（可见性判据）· §3.4.5（状态条范围与
 * 文案）· §3.4.6（会话列表挂子聊天）· §3.7（生命周期三态）· §3.8 F1–F10。
 * 消息面契约 = `docs/spec/60-reference/61-消息一览.md` §4.2 / §4.3（载荷零改动）。
 *
 * 会话类型（`sessions.kind`，§3.2 D23/D39）：`""` 普通顶层 · `sub_chat` 子聊天 · `group_chat`
 * 群聊（与子聊天同体系）· `sub_session` = `dsl_run` 的 LLM 步骤派生的子会话（**不属子聊天体系**，
 * 仍走任务执行树，不进状态条 / 会话列表子节点）。
 */
import { FieldKeys } from '../events/msgkeys.js'

/** 子聊天体系会话类型（`sub_chat` 子聊天 / `group_chat` 群聊）。 */
export const SUB_CHAT_KINDS = ['sub_chat', 'group_chat']

/** 是否子聊天体系会话（固定参数会话 → 隐藏参数切换入口 / 走状态条与会话列表子节点）。 */
export function isSubChatKind(kind) {
  return SUB_CHAT_KINDS.includes(String(kind || ''))
}

/** 生命周期三态（与后端 `facade.SessionLifecycle*` 同值）。 */
export const SUBCHAT_CHATTING = 'chatting'
export const SUBCHAT_READY = 'ready'
export const SUBCHAT_FINISHED = 'finished'

/** 展开时状态条可视条数（§10 Q5 / D40：最多显示 15 条 + 超出可滚动）。 */
export const SUBCHAT_MAX_VISIBLE = 15

/** 是否「未结束」（状态条列出口径，§3.4.4）：`chatting` / `ready`；`finished` 不列（转会话列表回看）。 */
export function isSubChatUnfinished(item) {
  const lc = item && item[FieldKeys.lifecycle]
  return lc === SUBCHAT_CHATTING || lc === SUBCHAT_READY
}

/** 显示名 = `purpose`（§10 Q5：文案来源 = gateway 注入的展示名，非自动摘要标题）→ 回退 `title` / 短 id。 */
export function subChatName(item) {
  if (!item) return ''
  const p = String(item[FieldKeys.purpose] || '').trim()
  if (p) return p
  const t = String(item[FieldKeys.title] || '').trim()
  if (t) return t
  return String(item[FieldKeys.session_id] || '').slice(0, 8)
}

/** 状态条文案三态映射（§10 Q5）：`chatting` = 「正在 <purpose>」；`ready` = 「<purpose> · 可以结束了」。 */
export function subChatStatusText(item, t) {
  const name = subChatName(item)
  return item && item[FieldKeys.lifecycle] === SUBCHAT_READY
    ? t('chat.subchat_status_ready', { name })
    : t('chat.subchat_status_chatting', { name })
}

/** 短状态标签 i18n 键（会话列表子节点 / 启动提醒列表用）。 */
export function subChatStateKey(lifecycle) {
  if (lifecycle === SUBCHAT_FINISHED) return 'chat.subchat_state_finished'
  if (lifecycle === SUBCHAT_READY) return 'chat.subchat_state_ready'
  return 'chat.subchat_state_chatting'
}
