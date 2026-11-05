<template>
  <!-- This component is a mq listener only.
       It delegates display: Ask 中心可用（桌面宿主）→ 独立待办窗口（gui.window.open-ask）；
       不可用（browser / 旧宿主）→ 回落既有 AskUserManager 弹窗（兜底，避免 turn 空等）。 -->
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { askUserManager } from '../../utils/askUserManager'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../../events/event-names'

let unsubAskUser = null

/** 过期时刻归一（server = RFC3339 字符串；测试夹具 = unix ms 数字）。 */
function deadlineMs(v) {
  if (!v) return 0
  if (typeof v === 'number') return Number.isFinite(v) && v > 0 ? v : 0
  const n = Number(v)
  if (Number.isFinite(n) && n > 0) return n
  const parsed = Date.parse(v)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

/** 归一 ask 载荷（保留 session-ask 原字段名，供 Ask 中心窗口直接消费）。 */
function toAskPayload(data) {
  const options = data.options || []
  return {
    ask_id: data.ask_id || '',
    question: data.question || '',
    options,
    // 无 options = 自由文本问题（与既有弹窗口径一致）；显式 custom=true 才放行「其他」。
    custom: options.length === 0 ? true : !!data.custom,
    multi: !!data.multi,
    recommended: Array.isArray(data.recommended) ? data.recommended : [],
    session: data.session || data.session_id || data.sub_session_id || '',
    sub_session_id: data.sub_session_id || '',
    turn: data.turn || data.turn_id || '',
    // 会话标识（61 §4.3 session-ask.dialog_id）：作答原样回带 / Ask 中心「聊一聊」定位。
    dialog_id: data.dialog_id || '',
    owner_instance: data.owner_instance || '',
    session_title: data.session_title || '',
    context: data.context || '',
    expires_at: deadlineMs(data.expires_at),
    scenario_id: data.scenario_id || '',
    scenario_name: data.scenario_name || '',
  }
}

/**
 * 把提问路由到 Ask 中心（独立待办窗口，2026-11-04）。
 * 返回 true = 宿主已接管（窗口打开/激活 + 待办入表）→ 本窗不再弹 dialog；
 * 返回 false = 宿主不支持（browser / 旧宿主 / 建窗失败）→ 调用方回落既有弹窗。
 */
async function routeToAskCenter(ask) {
  try {
    const env = await mq.emit(EventNames.guiWindowOpenAsk, { ask }, { timeout: 5000 })
    const backend = env && env.backend
    const result = backend && backend.result
    return !!(backend && backend.ok && result && result.ok === true)
  } catch (e) {
    console.warn('[AskUserDialog] gui.window.open-ask 失败（回落弹窗）:', e)
    return false
  }
}

onMounted(() => {
  // 20-gui：提问由 server `ask-user` 事件承载（替代 tool-pair{user_ask}）。
  // 载荷字段（21-llm-server）：ask_id / question / options / custom / session / turn /
  // expires_at（unix ms）；server 补 multi/recommended（P3）。
  unsubAskUser = mq.on(EventNames.askUser, async (data) => {
    if (!data?.question) return
    const ask = toAskPayload(data)
    if (await routeToAskCenter(ask)) return
    askUserManager.enqueue({
      // 路由键 = ask_id（server ask 通道注册键，已归一）；不传则 ask-user-reply 的
      // ask_id 为空，server 无法路由回答。
      askId: data.ask_id || '',
      question: data.question,
      options: data.options || [],
      custom: data.options && data.options.length === 0 ? true : !!data.custom,
      multi: !!data.multi,
      recommended: Array.isArray(data.recommended) ? data.recommended : [],
      turnId: data.turn || data.turn_id || '',
      subSessionId: data.sub_session_id || '',
      sessionId: data.session || data.session_id || data.sub_session_id || '',
      // 会话标识（61 §4.3 session-ask.dialog_id，§0.6 必带；AskUser S1-3/S1-4）：
      // 作答时原样回带（ask-user-reply.dialog_id）。
      dialogId: data.dialog_id || '',
      context: data.context || '',
      deadline: deadlineMs(data.expires_at),
      scenarioId: data.scenario_id,
      scenarioName: data.scenario_name,
    })
  })
})

onUnmounted(() => {
  if (unsubAskUser) unsubAskUser()
})
</script>