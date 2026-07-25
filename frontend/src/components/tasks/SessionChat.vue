<template>
  <div class="session-chat">
    <div v-if="!sessionId" class="empty-prompt">
      <p>Select a sub-session to view its conversation</p>
    </div>
    <template v-else>
      <MessageList ref="messageListRef" :messages="messages" :turn-active="turnActive" :session-id="sessionId" :has-more="hasMore" :loading-more="loadingMore" @load-more="loadMoreMessages" />
    </template>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { subscribeSession, unsubscribeSession } from '../../api/session'
import bridge from '../../utils/bridge'
import MessageList from '../chat/MessageList.vue'
import { createSessionMessages } from '../../utils/sessionMessages'

const emit = defineEmits(['turn-count-change'])

const sessionId = ref(null)
const messageListRef = ref(null)

const {
  messages, turnActive,
  hasMore, loadingMore,
  handleToken, handleDone, handleError,
  loadMessages, loadMoreMessages, teardown: resetMessages,
} = createSessionMessages()

async function loadWithCount(sid) {
  const res = await loadMessages(sid)
  const count = (res?.turns || []).length
  emit('turn-count-change', count)
  return res
}

function scrollTop() { messageListRef.value?.scrollTop() }
function scrollBottom() { messageListRef.value?.scrollBottom() }

defineExpose({ scrollTop, scrollBottom })

// ── SessionChat 通过 EventBus 订阅 subsessionchanged，不再依赖父组件 prop + watch ──

function onSubSessionChanged({ session_id: newId }) {
  const oldId = sessionId.value
  if (newId === oldId) return

  // Unsubscribe old
  if (oldId) unsubscribeSession(oldId)

  if (newId) {
    sessionId.value = newId
    subscribeSession(newId)
    loadWithCount(newId)
  } else {
    sessionId.value = null
    resetMessages()
  }
}

function onLLMEvent(data) {
  if (!sessionId.value || data?.session_id !== sessionId.value) return
  const et = data._event_type || ''
  if (et === 'message_chunk' || et === 'tool_call' || et === 'tool_result') {
    handleToken(data)
  } else if (et === 'complete') {
    handleDone()
  } else if (et === 'error') {
    handleError(data)
  }
}

function onSessionRefresh(data) {
  if (!sessionId.value || data?.session_id !== sessionId.value) return
  loadWithCount(sessionId.value)
}

// ── Lifecycle ──
const unsubs = []

onMounted(() => {
  unsubs.push(bridge.on('subsessionchanged', onSubSessionChanged))
  unsubs.push(bridge.on('llm:event', onLLMEvent))
  unsubs.push(bridge.on('session:refresh', onSessionRefresh))
})

onUnmounted(() => {
  if (sessionId.value) unsubscribeSession(sessionId.value)
  unsubs.forEach(fn => fn())
  unsubs.length = 0
  resetMessages()
})
</script>

<style scoped>
.session-chat {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.empty-prompt {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-muted);
  font-size: 13px;
}
</style>