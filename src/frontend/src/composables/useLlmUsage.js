/**
 * 上下文用量环的状态订阅（`llm-usage` → 按会话保存「最近一次 step」）。
 *
 * 口径：
 *   - 只接收**主轮次**（`parents` 为空）；子轮次用量不进主会话的环；
 *   - 每个会话保留一条状态（send 估算 → done 真实值，见 utils/llmUsage.mergeUsage）；
 *   - 模块级共享状态：会话切换回来仍能看到最近一次用量（应用重启后为空，等下一次 step）。
 *
 * 订阅在消费方（ChatPanel）挂载/卸载时建立/释放；状态本身模块级共享。
 */
import { onMounted, onUnmounted, reactive } from 'vue'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../events/event-names'
import { isMainTurn, mergeUsage } from '../utils/llmUsage'

const bySession = reactive({})
let seq = 0

/** 处理一条 `llm-usage` 载荷（导出便于单测；组件内由订阅调用）。 */
export function handleUsage(payload) {
  if (!payload || !payload.session || !isMainTurn(payload)) return
  const sid = String(payload.session)
  bySession[sid] = mergeUsage(bySession[sid] || null, payload, ++seq)
}

/** 取某会话最近一次用量（无 → null）。 */
export function usageOf(sessionId) {
  if (!sessionId) return null
  return bySession[sessionId] || null
}

export function useLlmUsage() {
  const unsubs = []
  onMounted(() => {
    unsubs.push(mq.on(EventNames.llmUsage, handleUsage))
  })
  onUnmounted(() => {
    unsubs.forEach(fn => fn())
    unsubs.length = 0
  })
  return { usageOf, handleUsage }
}