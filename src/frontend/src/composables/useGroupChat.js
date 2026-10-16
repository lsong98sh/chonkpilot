/**
 * useGroupChat —— 群聊窗口（只读展示板）**接线层**（取数 / 动作）。
 *
 * 设计依据：`docs/agent-chat/群聊会话设计方案.md` §4（窗口 UI：参与者头像区 + 结论流 + 右侧
 * 子会话分栏）· §5（数据模型）· §9 D14/D29/D47/D48。**消息面零改动**（既有主题 + 既有载荷）：
 *   - `data-session-group-participants-load {session_id}` → `{list:[{session_id, agent, role, seq}]}`
 *     （参与者名单 + 角色；按 `seq` 升序 = 依次叫号的固定顺序）—— 61 §3.1；
 *   - `data-session-history {session_id}` → `{messages:{messages:[…]}}`（展示板结论流；视图行带
 *     `sender_agent` / `mentions`）—— 61 §3.2；
 *   - `subchat-continue {session_id}` → `{ok}`（用户「继续」：到顶续跑 / 关窗后续聊补跑）—— 61 §4.2；
 *   - `subchat-participant-state`（下行）→ 头像实时状态（`thinking`/`idle`/`spoken`）—— 61 §4.3；
 *     订阅由 `GroupChatBoard.vue` 持有（组件级，随窗口卸载清理）。
 *
 * 模型/纯逻辑（work_session id 推导、左右对齐等）见 `utils/groupchat.js`（叶子模块）。
 */
import mq from '../utils/mq'
import {
  MsgTopics,
  FieldKeys,
  DataSessionGroupParticipantsLoadKeys,
  SubchatContinueKeys,
} from '../events/msgkeys.js'

/**
 * 读某群聊的**参与者名单 + 角色**（61 §3.1 `data-session-group-participants-load`）。
 * 进程内缓存（名册建成后不变；`refresh=true` 强制重取）——状态条 / 展示板多次取用零额外往返。
 * @returns {Promise<Array<{session_id:string, agent:string, role:string, seq:number}>>}
 */
export async function loadGroupParticipants(sessionId, { refresh = false } = {}) {
  if (!sessionId) return []
  if (!refresh && participantCache.has(sessionId)) return participantCache.get(sessionId)
  const env = await mq.emit(
    MsgTopics.dataSessionGroupParticipantsLoad,
    { [FieldKeys.session_id]: sessionId },
    { timeout: 30000 },
  )
  const r = (env && env.backend && env.backend.result) || {}
  const list = r[DataSessionGroupParticipantsLoadKeys.list]
  const out = Array.isArray(list) ? list : []
  participantCache.set(sessionId, out)
  return out
}

/** 参与者名单缓存（`session_id` → 名册数组）。 */
const participantCache = new Map()

/**
 * 读群聊**展示板结论流**（全部消息；单条展示板轮次承载，D31「谁结束谁上屏」）。
 * 分页向后翻（`has_more`）直至取完；返回**升序**消息视图行数组
 * （视图行含 `role`/`content`/`sender_agent`/`mentions`/`created_at`）。
 */
export async function loadGroupBoard(sessionId) {
  if (!sessionId) return []
  const out = []
  let beforeTurnId = ''
  for (let i = 0; i < 100; i++) {
    const env = await mq.emit(
      MsgTopics.dataSessionHistory,
      {
        [FieldKeys.session_id]: sessionId,
        [FieldKeys.before_turn_id]: beforeTurnId,
        [FieldKeys.target_messages]: 1000000,
        [FieldKeys.target_bytes]: 1000000000,
      },
      { timeout: 30000 },
    )
    const r = (env && env.backend && env.backend.result) || {}
    const page = (r && r.messages) || {}
    const msgs = Array.isArray(page.messages) ? page.messages : []
    const turns = Array.isArray(page.turns) ? page.turns : []
    out.unshift(...msgs)
    if (page.has_more !== true || turns.length === 0) break
    beforeTurnId = turns[0].turn_id
  }
  return out
}

/**
 * 用户点「继续」（61 §4.2 `subchat-continue`）：群聊到顶 / 关窗后停 → 回 `chatting` 并**断点补跑**
 * （只补本轮无结论者，D45 甲案）。**仅 `kind=group_chat` 有效**。
 * @returns {Promise<boolean>} 服务端是否受理（`result.ok`）
 */
export async function continueGroupChat(sessionId) {
  if (!sessionId) return false
  const env = await mq.emit(
    MsgTopics.subchatContinue,
    { [FieldKeys.session_id]: sessionId },
    { timeout: 30000 },
  )
  const r = (env && env.backend && env.backend.result) || {}
  return r[SubchatContinueKeys.ok] === true
}
