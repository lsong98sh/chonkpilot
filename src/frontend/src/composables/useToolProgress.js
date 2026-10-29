/**
 * useToolProgress — 工具完成进度（GATEWAY-2 L8，2026-10-28）
 *
 * 数据源 = **下行事件** `mcp-tools-progress`（61-消息一览 §5.3）：
 * `{instance_id, tool_call_id, progress, total?}`（桥按 `instance_id` 精确过滤，前端只收本实例）。
 * 上游链路 = 工具 handler `NotifyProgress` → `notifications/progress`
 * → gateway（`progressToken = call_id` 归位）→ 宿主 `Params.ProgressHandler`
 * → 本事件。
 *
 * UI 落点 = 工具调用条目的 **title**：仅当 `total > 0` 时显示 `进度 N%`；
 * **无进度事件 / 无 `total` → 不设 title**（不占位、不显示 0%）。
 *
 * 本 composable 是**模块级单例**：全应用懒加载一次并缓存（与 useToolAsyncMode 同模式）；
 * 状态以 `ref` 暴露 → computed 中读取即自动响应，**不用 watch / watchEffect**。
 */
import { ref } from 'vue'
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'
import { FieldKeys } from '../events/msgkeys.js'

// tool_call_id → {progress, total}（total = 0 表示工具未上报总量）
const byCall = ref(new Map())
let subscribed = false

function ensureSubscribed() {
  if (subscribed) return
  subscribed = true
  mq.on(EventNames.mcpToolsProgress, (payload) => {
    if (!payload || typeof payload !== 'object') return
    const id = payload[FieldKeys.tool_call_id]
    if (!id) return
    const progress = Number(payload[FieldKeys.progress])
    if (!Number.isFinite(progress)) return
    const totalRaw = Number(payload[FieldKeys.total])
    const total = Number.isFinite(totalRaw) && totalRaw > 0 ? totalRaw : 0
    const next = new Map(byCall.value)
    next.set(String(id), { progress, total })
    byCall.value = next
  })
}

/**
 * 取某次工具调用已收到的进度（无事件 → null）。
 * @param {string} toolCallId
 * @returns {{progress: number, total: number}|null}
 */
export function getToolProgress(toolCallId) {
  if (!toolCallId) return null
  return byCall.value.get(String(toolCallId)) || null
}

/**
 * 工具调用条目的 title 文案：`total > 0` → `进度 N%`（四舍五入，0–100 钳制）；
 * 无事件 / `total` 缺省 → 空串（调用方据此**不设** title 属性）。
 * @param {string} toolCallId
 * @returns {string}
 */
export function toolProgressTitle(toolCallId) {
  const p = getToolProgress(toolCallId)
  if (!p || !(p.total > 0)) return ''
  const pct = Math.min(100, Math.max(0, Math.round((p.progress / p.total) * 100)))
  return `进度 ${pct}%`
}

export function useToolProgress() {
  ensureSubscribed()
  return { getToolProgress, toolProgressTitle }
}
