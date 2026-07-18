<template>
  <div class="session-chat">
    <div v-if="!sessionId" class="empty-prompt">
      <p>Select a sub-session to view its conversation</p>
    </div>
    <template v-else>
      <MessageList ref="messageListRef" :messages="messages" :turn-active="turnActive" :session-id="sessionId" />
    </template>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { subscribeSession, unsubscribeSession } from '../../api/session'
import bridge from '../../utils/bridge'
import MessageList from '../chat/MessageList.vue'
import { useSessionMessages } from '../../composables/useSessionMessages'

const emit = defineEmits(['turn-count-change'])

const props = defineProps({
  sessionId: { type: String, default: null },
})

const messageListRef = ref(null)

const {
  messages, turnActive,
  handleToken, handleDone, handleError,
  loadMessages, teardown: resetMessages,
} = useSessionMessages()

async function loadWithCount(sid) {
  const res = await loadMessages(sid)
  const count = (res?.turns || []).length
  emit('turn-count-change', count)
  return res
}

function scrollTop() { messageListRef.value?.scrollTop() }
function scrollBottom() { messageListRef.value?.scrollBottom() }

defineExpose({ scrollTop, scrollBottom })
const unsubs = []

// ── Unified llm:event handler (filter by session_id) ──
function onLLMEvent(data) {
  if (!props.sessionId || data?.session_id !== props.sessionId) return
  const et = data._event_type || ''
  if (et === 'message_chunk' || et === 'tool_call' || et === 'tool_result') {
    handleToken(data)
  } else if (et === 'complete') {
    handleDone()
  } else if (et === 'error') {
    handleError(data)
  }
  // llm_error/llm_retry etc. are not rendered in SessionChat
}

function onSessionRefresh(data) {
  if (!props.sessionId || data?.session_id !== props.sessionId) return
  loadWithCount(props.sessionId)
}

// ── Lifecycle ──
function doSubscribe(sid) {
  if (!sid) return
  subscribeSession(sid)
  loadWithCount(sid)
}

function doUnsubscribe(sid) {
  if (!sid) return
  unsubscribeSession(sid)
}

watch(() => props.sessionId, (newId, oldId) => {
  doUnsubscribe(oldId)
  if (newId) {
    doSubscribe(newId)
  } else {
    resetMessages()
  }
})

onMounted(() => {
  unsubs.push(bridge.on('llm:event', onLLMEvent))
  unsubs.push(bridge.on('session:refresh', onSessionRefresh))
  if (props.sessionId) {
    doSubscribe(props.sessionId)
  }
})

onUnmounted(() => {
  doUnsubscribe(props.sessionId)
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
