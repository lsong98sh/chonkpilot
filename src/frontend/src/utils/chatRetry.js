/**
 * 消息区「工具重试 / 继续」候选判定（纯逻辑，供 MessageList 使用）。
 *
 * 分工（后端任务层语义约束，勿混用）：
 *   - **工具重试**只接受 `interrupted`（进程重启/abort 遗留，任务层允许
 *     interrupted→running）；`cancelled` 在任务层是**不可逆终态**，`FindRetryTarget` /
 *     `dbRetryNode` 也不会命中 → 不得作为工具重试候选。
 *   - `cancelled` / `interrupted` 的轮次走**轮次级「继续」**（`isUnfinishedTurn` →
 *     llm-start{continue} 复用原 turn），不是 tool-retry。
 *   - 通知轮（后端自动发起的 Notify 轮）两者都不提供：避免把通知误当用户提问重复发送。
 */

const RETRYABLE_STATUSES = ['interrupted']

// 轮次级未完成终态（LLM 未自然结束）：length 截断 / 资源不足 / 取消 / 中断
const UNFINISHED_REASONS = ['length', 'insufficient_system_resource', 'interrupted', 'cancelled']

/**
 * 轮次是否未完成（显示「继续」的权威信号）：
 *   - turn.status / finish_reason 为 cancelled / interrupted（用户取消、断链、abort）；
 *   - 既有 finish_reason：length / insufficient_system_resource。
 */
export function isUnfinishedTurn(turn) {
  if (!turn) return false
  const status = turn.status || ''
  const reason = turn.finish_reason || ''
  if (status === 'cancelled' || status === 'interrupted') return true
  return UNFINISHED_REASONS.includes(reason)
}

function metaOf(pair) {
  return pair && pair._meta && typeof pair._meta === 'object' ? pair._meta : null
}

/** 工具卡片自身是否带通知轮标记（顶层字段或 `_meta` 子集）。 */
export function hasNotifyMarker(pair) {
  if (!pair) return false
  if (pair.notify === true || pair.notify_turn === true || pair.kind === 'notify') return true
  const meta = metaOf(pair)
  return !!meta && (meta.notify === true || meta.notify_turn === true || meta.kind === 'notify')
}

/** 单条消息是否为通知（历史行 `kind=notify` / 实时气泡 `type=user-notify`）。 */
export function isNotifyMessage(msg) {
  if (!msg) return false
  return msg.type === 'user-notify' || msg.kind === 'notify' || msg.notify === true || msg.notify_turn === true
}

/** 用户消息是否为通知。 */
export function isNotifyUserMessage(msg) {
  return !!msg && msg.role === 'user' && isNotifyMessage(msg)
}

/** 消息列表的最后一条 user 消息是否为通知（= 本轮为后端通知轮）。 */
export function isNotifyTurn(messages) {
  if (!Array.isArray(messages) || messages.length === 0) return false
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (m && m.role === 'user') return isNotifyUserMessage(m)
  }
  return false
}

/**
 * 事件 / 消息双来源判定通知轮：
 *   - 事件显式带 `notify` / `notify_turn` → 以事件为准（布尔值）；
 *   - 事件缺省 → 回退消息形态推断（历史恢复 / 实时通知气泡）。
 */
export function detectNotifyTurn(event, messages) {
  if (event && typeof event.notify === 'boolean') return event.notify
  if (event && typeof event.notify_turn === 'boolean') return event.notify_turn
  return isNotifyTurn(messages)
}

/** 该 tool_pair 是否可重试（仅 interrupted；cancelled 走轮次级「继续」）。 */
export function isRetryableToolPair(pair) {
  if (!pair || pair.type !== 'tool_pair') return false
  if (!RETRYABLE_STATUSES.includes(pair.status)) return false
  if (pair.retryable === false) return false
  if (hasNotifyMarker(pair)) return false
  const meta = metaOf(pair)
  if (meta && meta.retryable === false) return false
  return true
}

/**
 * 从消息列表尾部找最后一条可重试 tool_pair。
 * @param {Array} messages
 * @param {boolean} notifyTurn 当前轮是否为通知轮（true → 不提供重试）
 */
export function findRetryableToolPair(messages, notifyTurn = false) {
  if (notifyTurn || !Array.isArray(messages)) return null
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (isRetryableToolPair(m)) return m
  }
  return null
}