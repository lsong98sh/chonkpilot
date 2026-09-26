// sessionStats.js — 会话 token 统计共享 store
//
// 订阅后端推送的 `session-statistic-refresh` 事件，按 session_id 缓存 stats，
// 供 SessionChat.vue / SessionsPane.vue 读取并展示在 session-id 的 tooltip 上。
import { reactive } from 'vue'
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'

// 后端事件名（与 EventNames 保持一致）
export const SESSION_STATISTIC_REFRESH = EventNames.sessionStatisticRefresh

// 响应式 map：session_id -> stats。条目带写入时间戳，超限时淘汰最旧的，
// 防止会话不断创建导致 statsMap 无界增长（会话删除事件不推送到前端）。
const MAX_STATS = 300

const statsMap = reactive({})

function onStatisticRefresh(data) {
  if (!data || !data.session_id || !data.stats) return
  statsMap[data.session_id] = { ts: Date.now(), stats: data.stats }
  const keys = Object.keys(statsMap)
  if (keys.length > MAX_STATS) {
    const sorted = keys.sort((a, b) => statsMap[a].ts - statsMap[b].ts)
    const toRemove = sorted.slice(0, keys.length - MAX_STATS)
    for (const k of toRemove) delete statsMap[k]
  }
}

// 模块级单例：只在首次 import 时注册一次监听
let _subscribed = false
function ensureSubscribed() {
  if (_subscribed) return
  _subscribed = true
  mq.on(SESSION_STATISTIC_REFRESH, onStatisticRefresh)
}
ensureSubscribed()

/**
 * 读取指定 session 的 stats。无数据时返回 undefined。
 */
export function getSessionStats(sessionId) {
  if (!sessionId) return undefined
  const entry = statsMap[sessionId]
  return entry ? entry.stats : undefined
}

/**
 * 展示用格式化：把数值转成字符串，缺失时显示占位符 '-'。
 */
export function fmtStat(v) {
  return v === undefined || v === null || Number.isNaN(v) ? '-' : String(v)
}

/**
 * 供 tooltip 展示：取对应字段的展示字符串，无 stats 或字段缺失时返回 '-'。
 */
export function statDisplay(sessionId, key) {
  const s = getSessionStats(sessionId)
  return s ? fmtStat(s[key]) : '-'
}
