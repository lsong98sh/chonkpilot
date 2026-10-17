// useQueueStatus.js — 记忆提取 / 上下文压缩**长任务状态**（状态栏条目 + 详情页，STB-1）：
// 数据源 = 既有通知面 tool-notify（`{memory,compress}-{queued,start,done}` + `plugin-failure`
// 的 memory / compress 失败；零新增主题，见 61-消息一览 §4.3）。
//
// 三个消费面（模块级共享状态，同 useCompressStatus 范式）：
//   - `memoryTask` / `compressTask`：**当前活动**任务状态（done 保持 3s 后自动隐藏；error 常驻）；
//   - `memoryLast` / `compressLast`：**最近一次任务**（不清空，详情页「最近一次任务」用）；
//   - `memoryTimeline` / `compressTimeline`：真实 notice **状态时间线**（有界 20 条；不伪造队列条目）。
//
// 订阅在消费方（ChatPanel / StatusBar / 栏视图）内；本文件只做 notice → 状态的归并。
// 失败语义：notice=plugin-failure（plugin=memory|compress）→ state='error'；随后 worker 仍会
// 发 `*-done`——**error 优先**（done 不覆盖错误态，见 reduceTask），避免失败被"完成"洗掉。
import { ref } from 'vue'

const DONE_HOLD_MS = 3000
const TIMELINE_CAP = 20
const SEEN_CAP = 200

/** 任务状态集合（空闲 = null，不在此列） */
export const TASK_STATES = ['queued', 'running', 'done', 'error']

/** 状态 → 字色 tone（tone 在组件内映射宿主语义 token） */
export const TASK_TONE = {
  queued: 'progress',
  running: 'progress',
  done: 'ready',
  error: 'error',
  idle: 'disabled',
  disabled: 'disabled',
}

/**
 * parseNotice 把 tool-notify 载荷映射为 {kind, state, error, errorKind}；非长任务取值 → null。
 * 失败面 = 既有 `notice=plugin-failure`（payload 带 plugin/kind/reason，见 61 §4.3）。
 */
export function parseNotice(payload) {
  const notice = String((payload && payload.notice) || '')
  switch (notice) {
    case 'memory-queued': return { kind: 'memory', state: 'queued', error: '', errorKind: '' }
    case 'memory-start': return { kind: 'memory', state: 'running', error: '', errorKind: '' }
    case 'memory-done': return { kind: 'memory', state: 'done', error: '', errorKind: '' }
    case 'compress-queued': return { kind: 'compress', state: 'queued', error: '', errorKind: '' }
    case 'compress-start': return { kind: 'compress', state: 'running', error: '', errorKind: '' }
    case 'compress-done': return { kind: 'compress', state: 'done', error: '', errorKind: '' }
    case 'plugin-failure': {
      const plugin = String((payload && payload.plugin) || '')
      if (plugin !== 'memory' && plugin !== 'compress') return null
      const error = String((payload && (payload.reason || payload.message)) || '').trim()
      return { kind: plugin, state: 'error', error, errorKind: String((payload && payload.kind) || '') }
    }
    default:
      return null
  }
}

/**
 * reduceTask 由「上一条任务状态 + 一条 notice」推导下一条任务状态（纯函数）。
 * @param {object|null} prev 上一条任务状态（`{state, startedAt, session, turn, ...}`）
 * @param {{kind:string,state:string,error?:string,errorKind?:string}} e parseNotice 结果
 * @param {object} payload 原始 tool-notify 载荷（取 session_id / turn_id / message）
 * @param {number} now 时间戳（ms）
 * @returns {object|null} 下一条状态；null = 忽略该 notice（失败后到达的 done）
 */
export function reduceTask(prev, e, payload, now) {
  // 失败优先：同一任务失败后 worker 仍会补发 done —— 不让「完成」覆盖错误态。
  if (prev && prev.state === 'error' && e.state === 'done') return null
  const p = payload || {}
  // 新任务：无前态 / 之前已收口（done|error）/ 本次为排队（新一轮开始）
  const isNew = !prev || prev.state === 'done' || prev.state === 'error' || e.state === 'queued'
  const state = e.state
  return {
    state,
    at: now,
    startedAt: isNew ? now : (Number(prev.startedAt) || now),
    session: String(p.session_id || (isNew ? '' : prev.session) || ''),
    turn: String(p.turn_id || (isNew ? '' : prev.turn) || ''),
    message: String(p.message || ''),
    error: state === 'error' ? String(e.error || p.message || '') : '',
    errorKind: state === 'error' ? String(e.errorKind || '') : '',
  }
}

/** 时间线条目（state + 时间 + 载荷文本；不引入任何虚构字段）。 */
export function timelineEntry(task, kind) {
  return {
    kind,
    state: task.state,
    at: task.at,
    session: task.session,
    turn: task.turn,
    message: task.state === 'error' ? task.error : task.message,
  }
}

// ── 模块级共享状态 ─────────────────────────────────────────────
const memoryTask = ref(null)
const compressTask = ref(null)
const memoryLast = ref(null)
const compressLast = ref(null)
const memoryTimeline = ref([])
const compressTimeline = ref([])

let memoryTimer = null
let compressTimer = null
const seenIds = new Set()
const seenOrder = []

function clearTimer(kind) {
  if (kind === 'memory') {
    if (memoryTimer) { clearTimeout(memoryTimer); memoryTimer = null }
  } else if (compressTimer) {
    clearTimeout(compressTimer); compressTimer = null
  }
}

function refsOf(kind) {
  return kind === 'memory'
    ? { task: memoryTask, last: memoryLast, timeline: memoryTimeline }
    : { task: compressTask, last: compressLast, timeline: compressTimeline }
}

/** message_id 幂等去重（多个消费方同时订阅同一通知面时只记一次）。 */
function alreadySeen(payload) {
  const id = String((payload && payload.message_id) || '')
  if (!id) return false
  if (seenIds.has(id)) return true
  seenIds.add(id)
  seenOrder.push(id)
  if (seenOrder.length > SEEN_CAP) seenIds.delete(seenOrder.shift())
  return false
}

// scheduleHide 完成态保持 DONE_HOLD_MS 后自动隐藏（最近一次任务 / 时间线保留）。
function scheduleHide(kind) {
  clearTimer(kind)
  const hide = () => {
    const { task } = refsOf(kind)
    task.value = null
    if (kind === 'memory') memoryTimer = null
    else compressTimer = null
  }
  if (kind === 'memory') memoryTimer = setTimeout(hide, DONE_HOLD_MS)
  else compressTimer = setTimeout(hide, DONE_HOLD_MS)
}

/**
 * handleNotice 处理一条 tool-notify 载荷。
 * @param {object} payload tool-notify 载荷
 * @param {'memory'|'compress'} [onlyKind] 只处理某类（栏视图各订阅自己的一类；缺省 = 全部）
 */
function handleNotice(payload, onlyKind) {
  const e = parseNotice(payload)
  if (!e) return
  if (onlyKind && e.kind !== onlyKind) return
  if (alreadySeen(payload)) return
  const { task, last, timeline } = refsOf(e.kind)
  const next = reduceTask(task.value, e, payload, Date.now())
  if (!next) return
  clearTimer(e.kind)
  task.value = next
  last.value = next
  timeline.value = [...timeline.value, timelineEntry(next, e.kind)].slice(-TIMELINE_CAP)
  // 完成态保持数秒后自动隐藏；失败态常驻（失败必须可见）。
  if (next.state === 'done') scheduleHide(e.kind)
}

export function useQueueStatus() {
  return {
    memoryTask,
    compressTask,
    memoryLast,
    compressLast,
    memoryTimeline,
    compressTimeline,
    // 兼容旧消费方（QueueStatusView / legacy StatusBar）：活动态即 task（空闲 null → 不渲染）
    memoryQueue: memoryTask,
    compressQueue: compressTask,
    handleNotice,
  }
}
