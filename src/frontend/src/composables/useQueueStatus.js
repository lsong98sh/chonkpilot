// useQueueStatus.js — 记忆沉淀 / 上下文压缩**队列状态**（statusbar 展示，I-128）：
// 数据源 = 既有通知面 tool-notify（notice ∈ `{memory,compress}-{queued,start,done}`；零新增主题，
// 见 61-消息一览 §4.3）—— `*-queued` = 排队、`*-start` = 进行中、`*-done` = 完成。
//
// 模块级共享状态（同 useCompressStatus 范式）：订阅在消费方（StatusBar）的 onMounted 内，
// 本文件只做 notice → 状态的归并与「完成态保持后自动隐藏」。
import { ref } from 'vue'

const DONE_HOLD_MS = 3000

// memoryQueue / compressQueue：{ state: 'queued' | 'running' | 'done' } 或 null（空闲）。
const memoryQueue = ref(null)
const compressQueue = ref(null)
let memoryTimer = null
let compressTimer = null

// parseNotice 把 tool-notify 的 notice 取值映射为 {kind, state}；非队列取值 → null。
function parseNotice(notice) {
  switch (notice) {
    case 'memory-queued': return { kind: 'memory', state: 'queued' }
    case 'memory-start': return { kind: 'memory', state: 'running' }
    case 'memory-done': return { kind: 'memory', state: 'done' }
    case 'compress-queued': return { kind: 'compress', state: 'queued' }
    case 'compress-start': return { kind: 'compress', state: 'running' }
    case 'compress-done': return { kind: 'compress', state: 'done' }
    default: return null
  }
}

function clearTimer(kind) {
  if (kind === 'memory') {
    if (memoryTimer) { clearTimeout(memoryTimer); memoryTimer = null }
  } else if (compressTimer) {
    clearTimeout(compressTimer); compressTimer = null
  }
}

// scheduleHide 完成态保持 DONE_HOLD_MS 后自动隐藏（避免「完成」常驻）。
function scheduleHide(kind) {
  clearTimer(kind)
  const hide = () => {
    if (kind === 'memory') { memoryQueue.value = null; memoryTimer = null } else { compressQueue.value = null; compressTimer = null }
  }
  if (kind === 'memory') memoryTimer = setTimeout(hide, DONE_HOLD_MS)
  else compressTimer = setTimeout(hide, DONE_HOLD_MS)
}

// handleNotice 处理一条 tool-notify 载荷（无匹配 notice → 忽略，不影响其它取值路径）。
function handleNotice(payload) {
  const p = parseNotice(payload && payload.notice)
  if (!p) return
  // 新状态到来先撤销「完成自动隐藏」定时器（避免旧定时器把新状态清掉）
  clearTimer(p.kind)
  if (p.kind === 'memory') memoryQueue.value = { state: p.state }
  else compressQueue.value = { state: p.state }
  if (p.state === 'done') scheduleHide(p.kind)
}

export function useQueueStatus() {
  return { memoryQueue, compressQueue, handleNotice }
}
