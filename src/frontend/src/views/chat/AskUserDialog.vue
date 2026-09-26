<template>
  <!-- This component is a mq listener only.
       It delegates all dialog display to AskUserManager. -->
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { askUserManager } from '../../utils/askUserManager'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

let unsubAskUser = null

onMounted(() => {
  // 20-gui：提问由 server `ask-user` 事件承载（替代 tool-pair{user_ask}）。
  // 载荷字段（21-llm-server）：ask-id / question / options / custom / session / turn /
  // expires_at（unix ms）；server 补 multi/recommended（P3）。
  unsubAskUser = mq.on(EventNames.askUser, (data) => {
    if (!data?.question) return
    askUserManager.enqueue({
      // 路由键 = ask-id（server ask 通道注册键，已归一）；不传则 ask-user-reply 的
      // ask-id 为空，server 无法路由回答。
      askId: data['ask-id'] || data.ask_id || '',
      question: data.question,
      options: data.options || [],
      custom: data.options && data.options.length === 0 ? true : !!data.custom,
      multi: !!data.multi,
      recommended: Array.isArray(data.recommended) ? data.recommended : [],
      turnId: data.turn || data.turn_id || '',
      subSessionId: data.sub_session_id || '',
      sessionId: data.session || data.session_id || data.sub_session_id || '',
      deadline: data.expires_at && Number(data.expires_at) > 0 ? Number(data.expires_at) : 0,
      scenarioId: data.scenario_id,
      scenarioName: data.scenario_name,
    })
  })
})

onUnmounted(() => {
  if (unsubAskUser) unsubAskUser()
})
</script>
