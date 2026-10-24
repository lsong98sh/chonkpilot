/**
 * 长任务状态栏条目 / 详情页的**展示模型**（纯函数，便于单测；STB-1）。
 *
 * 数据源（零新增消息面）：
 *   - 记忆提取 / 上下文压缩：`useQueueStatus`（tool-notify 的 `{memory,compress}-*` + plugin-failure）；
 *   - 代码索引：prj 配置键 `plugins.codebase.codegraph.enabled` / `codegraph.status`
 *     （见 utils/indexStatus.js）。
 *
 * 口径（用户）：空闲 / 未启用 → 中性色（条目仍可见 = 「每种任务都有一个状态 view」）；
 * 排队 / 进行中 → 次要色；完成 → 主文本色；失败 → 错误色（title 携带错误原文，点开看详情）。
 */
import { TASK_TONE } from '../composables/useQueueStatus.js'

/**
 * 状态 → i18n 键（`statusBar.*`）。
 * @param {'memory'|'compress'} kind
 * @param {{state?:string}|null} task 当前任务（null = 空闲）
 * @param {boolean} [enabled] 记忆库开关（仅 memory 生效；false = 未启用）
 */
export function taskStateKey(kind, task, enabled = true) {
  const base = kind === 'compress' ? 'compress' : 'memory'
  if (!task) return enabled === false ? `statusBar.task_memory_disabled` : `statusBar.task_${base}_idle`
  switch (task.state) {
    case 'queued': return `statusBar.queue_${base}_queued`
    case 'running': return `statusBar.queue_${base}_running`
    case 'done': return `statusBar.queue_${base}_done`
    case 'error': return `statusBar.task_${base}_error`
    default: return `statusBar.task_${base}_idle`
  }
}

/** 状态 → tone（idle / disabled → disabled；queued / running → progress；done → ready；error → error） */
export function taskTone(kind, task, enabled = true) {
  if (!task) return enabled === false ? TASK_TONE.disabled : TASK_TONE.idle
  return TASK_TONE[task.state] || TASK_TONE.idle
}

/**
 * 状态栏条目模型：`{tone, text, title}`。
 * title = 错误原文（失败时）+ 「点击查看详情」；无错误时 = 点击提示。
 * @param {{kind:string, task:object|null, enabled?:boolean, t:(k:string)=>string}} p
 */
export function taskEntry({ kind, task, enabled = true, t }) {
  const tone = taskTone(kind, task, enabled)
  const text = t(taskStateKey(kind, task, enabled))
  const open = t(kind === 'memory' ? 'statusBar.task_open_memory' : 'statusBar.task_open_compress')
  const error = task && task.state === 'error' ? String(task.error || '').trim() : ''
  const title = error ? `${error} · ${open}` : open
  return { tone, text, title }
}

/** 时间戳（ms）→ 本地时间文本；缺省 / 非法 → ''（不编造时间）。 */
export function stampText(ms) {
  const n = Number(ms)
  if (!Number.isFinite(n) || n <= 0) return ''
  return new Date(n).toLocaleString()
}
