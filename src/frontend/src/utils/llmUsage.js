/**
 * 上下文用量环的纯函数（`llm-usage` 事件 → 环/悬停明细）。
 *
 * 事件口径（61 §4.3 `session-usage`）：
 *   - `phase=send`：请求发出前，只有本地估算 `est_prompt_tokens` + 上限；
 *   - `phase=done`：step 结束，带上游真实 usage（prompt/completion/total/reasoning/cached）。
 *
 * 环的分子 = 真实 `prompt_tokens`（> 0 时），否则回落到本地估算；分母 = `max_context_token`。
 * 本文件不依赖 Vue / mq，便于 node:test 直测；订阅与按会话存储见 composables/useLlmUsage.js。
 */

/** 是否为子轮次（子 agent / 群聊参与者工作会话）→ 环只跟主会话，不接收子轮次。 */
export function isMainTurn(ev) {
  const parents = ev && ev.parents
  return !Array.isArray(parents) || parents.length === 0
}

function num(v) {
  const n = Number(v)
  return Number.isFinite(n) && n > 0 ? n : 0
}

/**
 * 合并一条用量事件（同会话内状态机）。
 *
 * - send 覆盖为「当前请求」状态（不继承上一轮的真实值，避免环显示旧数据）；
 * - done 用真实值替换，并保留同 turn/step 的估算值（用于偏差展示）；
 * - 同一 step 的 done 之后迟到的 send 不覆盖（乱序/重试场景）。
 *
 * @param {object|null} prev 该会话上一状态
 * @param {object} ev `llm-usage` 载荷
 * @param {number} seq 收到顺序（单调递增；供调用方判定新旧）
 * @returns {object|null}
 */
export function mergeUsage(prev, ev, seq = 0) {
  if (!ev || !ev.session) return prev || null
  const phase = ev.phase || ''
  if (phase !== 'send' && phase !== 'done') return prev || null

  const sameStep =
    prev &&
    prev.turn === ev.turn &&
    Number(prev.step) === Number(ev.step)

  if (phase === 'send') {
    if (sameStep && prev.phase === 'done') return prev
    return { ...ev, seq, est_prompt_tokens: num(ev.est_prompt_tokens) }
  }

  const est = num(ev.est_prompt_tokens) || (sameStep ? num(prev && prev.est_prompt_tokens) : 0)
  const next = { ...ev, seq, est_prompt_tokens: est }
  // done 事件可能只带真实 usage（不同发布方/旧服务端）→ 同 step 的 send 里已有的上限/标识沿用。
  if (sameStep) {
    for (const k of ['max_context_token', 'max_output_token', 'llm', 'model']) {
      if (next[k] == null || next[k] === '') next[k] = prev[k]
    }
  }
  return next
}

/** 环的分子：真实输入 token 优先，缺失（0 = 上游未提供）→ 本地估算。 */
export function usageTokens(u) {
  if (!u) return 0
  const actual = num(u.prompt_tokens)
  return actual > 0 ? actual : num(u.est_prompt_tokens)
}

/** 环的分母：该次调用对应 LLM 的上下文窗口；未配置（0/缺失）→ 0。 */
export function usageLimit(u) {
  return num(u && u.max_context_token)
}

/**
 * 占用百分比（0..100）；分母未配置 → null（环显示「—」，不伪造 0%）。
 * 超过 100% 时封顶 100（环不溢出）。
 */
export function usagePercent(u) {
  const limit = usageLimit(u)
  if (!limit) return null
  const used = usageTokens(u)
  return Math.max(0, Math.min(100, (used / limit) * 100))
}

/** 环配色 tone：`muted`（无数据/无上限）· `normal` <60% · `warn` <85% · `danger` ≥85%。 */
export function toneOf(pct) {
  if (pct == null) return 'muted'
  if (pct >= 85) return 'danger'
  if (pct >= 60) return 'warn'
  return 'normal'
}

/** 千分位数字（不依赖运行环境 locale）。 */
export function formatTokens(n) {
  const v = num(n)
  if (!v) return '0'
  return String(Math.round(v)).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

/** 百分比文本（保留 1 位小数，去掉多余的 .0）。 */
export function formatPercent(pct) {
  if (pct == null) return '—'
  const rounded = Math.round(pct * 10) / 10
  return Number.isInteger(rounded) ? `${rounded}%` : `${rounded.toFixed(1)}%`
}

/** 带符号百分比（估算偏差：+3.2% / -1.0%）。 */
export function formatSignedPercent(v) {
  const n = Number(v)
  if (!Number.isFinite(n)) return '—'
  const text = `${Math.abs(n).toFixed(1)}%`
  return n >= 0 ? `+${text}` : `-${text}`
}

/**
 * 估算偏差（%）：真实输入相对本地估算；任一为 0 → null（无法校准）。
 * 正数 = 实际比估算多。
 */
export function usageDeviation(u) {
  if (!u) return null
  const actual = num(u.prompt_tokens)
  const est = num(u.est_prompt_tokens)
  if (!actual || !est) return null
  return ((actual - est) / est) * 100
}

/** 缓存命中占比（%，相对真实输入）；无真实输入或无缓存 → null。 */
export function cachePercent(u) {
  if (!u) return null
  const prompt = num(u.prompt_tokens)
  const cached = num(u.cached_tokens)
  if (!prompt || !cached) return null
  return Math.min(100, (cached / prompt) * 100)
}