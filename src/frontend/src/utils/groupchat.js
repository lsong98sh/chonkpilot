/**
 * 群聊（`kind = group_chat`）前端**纯逻辑**（叶子模块：无 import、无副作用 → 可直跑单测）。
 *
 * 设计依据：`docs/agent-chat/群聊会话设计方案.md` §4（窗口 UI）· §5（会话/数据模型）·
 * §9 决策记录 D14/D17/D23/D25/D29/D41/D47/D48。契约（消息面零改动）= `61-消息一览`
 * §3.2（`data-session-history` 回带 `sender_agent`/`mentions`）· §4.3（`subchat-participant-state`）。
 *
 * ⚠️ 唯一「跨端约定」：`groupWorkSessionId` 是**后端确定性 id 公式的 JS 复刻**
 * （权威实现 = `src/lib/llm/server/groupchat_sched.go: groupWorkSessionID` /
 * `sanitizeIDSeg` / `u32hex`）。群聊窗口右侧分栏要按 agent 打开其干活子会话，而**不新增任何
 * 消息**（无「按 agent 查 work_session」的读面），故此处按同一公式本地推导。
 * 后端改公式 → 必须同步本文件（前端 test/groupChat.test.js 有对账用例）。
 */

/** 群聊会话类型标识（`sessions.kind`，D25）。 */
export const GROUP_CHAT_KIND = 'group_chat'

/** 是否群聊会话类型。 */
export function isGroupChatKind(kind) {
  return String(kind || '') === GROUP_CHAT_KIND
}

/**
 * 会话 id 安全段（复刻后端 `sanitizeIDSeg`）：保留字母数字与 `_`/`-`，其余字符替换为 `_`；
 * 全被替换（空结果）→ `agent`。
 */
export function sanitizeIdSeg(s) {
  let out = ''
  for (const ch of String(s || '')) {
    if (/[A-Za-z0-9_-]/.test(ch)) out += ch
    else out += '_'
  }
  return out.length ? out : 'agent'
}

/**
 * 32 位 FNV-1a（复刻后端 `fnv.New32a()` + `u32hex`）→ 8 位小写十六进制。
 * UTF-8 字节序（agent 名可能含中文）。
 */
export function fnv32aHex(s) {
  const bytes = new TextEncoder().encode(String(s || ''))
  let h = 0x811c9dc5
  for (let i = 0; i < bytes.length; i++) {
    h ^= bytes[i]
    // 乘以 FNV 质数 16777619（32 位溢出取模 2^32）
    h = Math.imul(h, 0x01000193) >>> 0
  }
  return h.toString(16).padStart(8, '0')
}

/**
 * 参与者干活子会话（`work_session`）的确定性 id（D23；跨大轮次复用，同一 `(群聊, agent)` 恒同 id）。
 * = `work-<群聊id>-<净化后的 agent>-<fnv32a(agent) 8hex>`（与后端 `groupWorkSessionID` 同式）。
 */
export function groupWorkSessionId(groupId, agent) {
  return `work-${groupId}-${sanitizeIdSeg(agent)}-${fnv32aHex(agent)}`
}

/**
 * 拆分 agent 引用（复刻后端 `splitAgentRef`）：`<场景id>/<agent名>` → 名字段；裸名 → 原样。
 * 仅用于**展示**（头像标签取末段裸名，短且可读）。
 */
export function agentDisplayName(agent) {
  const ref = String(agent || '')
  const i = ref.indexOf('/')
  return (i >= 0 ? ref.slice(i + 1) : ref).trim()
}

/** 参与者头像状态（`subchat-participant-state.state`，61 §4.3 / D16）。 */
export const PARTICIPANT_THINKING = 'thinking'
export const PARTICIPANT_IDLE = 'idle'
export const PARTICIPANT_SPOKEN = 'spoken'

/**
 * 参与者是不是**主 agent**（D46/D47：主 agent ≡ 「用户」角色 → 其发言靠左显示）。
 * 判据 = 会话行的 `agent` 字段（= 主 agent 名，由 `runGroupChatOpen` 落）。
 */
export function isMainAgent(agent, mainAgent) {
  return !!agent && !!mainAgent && String(agent) === String(mainAgent)
}

/**
 * 展示板消息是否**靠左**（同真人用户消息位置，D47）：
 * 主 agent 的发言（`role=user`）靠左；其余参与者（`role=assistant`）靠右。
 * 非群聊发言（无 `sender_agent`，如系统提示 `role=system`）→ 独立中栏，不参与左右判定。
 */
export function isLeftAligned(msg, mainAgent) {
  if (!msg) return false
  const agent = String(msg.sender_agent || '')
  if (!agent) return false
  return isMainAgent(agent, mainAgent) || String(msg.role || '') === 'user'
}
