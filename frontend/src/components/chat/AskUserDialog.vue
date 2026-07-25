<template>
  <!-- This component is a bridge listener only.
       It delegates all dialog display to AskUserManager. -->
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { askUserManager } from '../../utils/askUserManager'
import bridge from '../../utils/bridge'

let unsubAskUser = null

onMounted(() => {
  unsubAskUser = bridge.on('ask_user', (data) => {
    if (!data?.question) return
    askUserManager.enqueue({
      question: data.question,
      options: data.options || [],
      custom: data.options && data.options.length === 0 ? true : !!data.custom,
      pipeAddr: data.pipe_addr || '',
      subSessionId: data.sub_session_id || '',
    })
  })
})

onUnmounted(() => {
  if (unsubAskUser) unsubAskUser()
})
</script>
