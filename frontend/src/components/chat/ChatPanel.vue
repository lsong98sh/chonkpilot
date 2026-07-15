<template>
  <div class="chat-panel">
    <MessageList ref="messageListRef" :messages="messages" :turn-active="isLoading" :collapse-reasoning="collapseReasoningKey" :session-id="currentSessionId" />
    <div v-if="taskProgress" class="task-progress-bar">{{ taskProgress }}</div>
    <InputBox @send="handleSend" @cancel="handleCancel" :loading="isLoading">
      <template #controls>
        <el-select v-model="selectedLLM" placeholder="Default LLM" size="small" clearable class="llm-select">
            <el-option v-for="llm in llmList" :key="llm.name" :label="llm.name" :value="llm.name" />
          </el-select>
          <el-tooltip content="Thinking mode" placement="top">
            <el-button
              size="small"
              :type="thinkEnabled ? 'primary' : 'default'"
              @click="thinkEnabled = !thinkEnabled"
              circle
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M12 2a7 7 0 0 0-7 7c0 2.5 1.5 4.7 3.5 5.8V17a1 1 0 0 0 1 1h5a1 1 0 0 0 1-1v-2.2A7 7 0 0 0 12 2z"/>
                <path d="M9 18h6"/>
                <path d="M10 22h4"/>
              </svg>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="'Effort: ' + effortLevel" placement="top">
            <el-button
              size="small"
              :type="effortLevel === 'max' ? 'primary' : 'default'"
              @click="toggleEffort"
              circle
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M12 2L2 12h4v8h12v-8h4L12 2z"/>
              </svg>
            </el-button>
          </el-tooltip>
        </template>
      </InputBox>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { sendChatMessage, cancelChat } from '../../api/chat'
import { createSession, getLatestSessionID, getSession } from '../../api/session'
import { getUserConfig } from '../../api/config'
import bridge from '../../utils/bridge'
import MessageList from './MessageList.vue'
import InputBox from './InputBox.vue'
import { useSessionMessages } from '../../composables/useSessionMessages'
import { useSession } from '../../composables/useSession'

const {
  messages, turnActive: _turnActive,
  handleToken, handleDone, handleError,
  loadMessages, teardown: resetMessages,
} = useSessionMessages()

const { currentSessionId } = useSession()

const messageListRef = ref(null)
const isLoading = ref(false)
const currentTurnId = ref(null)

function scrollTop() {
  messageListRef.value?.scrollTop()
}
function scrollBottom() {
  messageListRef.value?.scrollBottom()
}
defineExpose({ scrollTop, scrollBottom })
const activeUnsubs = []
const taskProgress = ref('')
const showReasoning = ref(true)
const collapseReasoningKey = ref(0)

// Cancel LLM handler — called from SessionDrawer via custom event
function handleCancelLLM() {
  if (currentTurnId.value) {
    cancelChat(currentTurnId.value)
  }
  activeUnsubs.forEach(fn => { try { fn() } catch(_) {} })
  activeUnsubs.length = 0
  isLoading.value = false
  currentTurnId.value = null
}

// Runtime LLM controls
const llmList = ref([])
const selectedLLM = ref('')
const thinkEnabled = ref(true)
const effortLevel = ref('high')

function toggleEffort() {
  effortLevel.value = effortLevel.value === 'high' ? 'max' : 'high'
}

function addMessage(role, content, type, createdAt) {
  messages.value.push({
    role,
    content,
    type: type || 'text',
    id: Date.now().toString() + Math.random().toString(36).slice(2, 6),
    createdAt: createdAt || new Date().toISOString(),
  })
}

async function handleSend(text) {
  if (!text.trim() || isLoading.value) return

  // Auto-create session if none exists (null = empty session, not yet in DB)
  if (!currentSessionId.value) {
    try {
      const s = await createSession('', '')
      currentSessionId.value = s.session_id || s.id
      window.dispatchEvent(new CustomEvent('session:loaded', { detail: { session_id: currentSessionId.value } }))
    } catch (e) {
      addMessage('assistant', `Error: 创建会话失败 — ${e.message}`, 'text')
      return
    }
  }

  addMessage('user', text, 'text')
  isLoading.value = true

  // Reset reasoning display for new turn
  showReasoning.value = true
  collapseReasoningKey.value++

  const unsubToken = bridge.on('chat:token', (data) => {
    if (data.turn_id && currentTurnId.value && data.turn_id !== currentTurnId.value) return
    // When first text token arrives after reasoning, collapse thinking
    if (showReasoning.value && data.type && data.type === 'text') {
      showReasoning.value = false
      collapseReasoningKey.value++
    }
    handleToken(data)
  })
  activeUnsubs.push(unsubToken)

  const unsubDone = bridge.on('chat:done', (data) => {
    if (data.turn_id && currentTurnId.value && data.turn_id !== currentTurnId.value) return
    handleDone()
    cleanupAndFinish()
  })
  activeUnsubs.push(unsubDone)

  const unsubError = bridge.on('chat:error', (data) => {
    if (data.turn_id && currentTurnId.value && data.turn_id !== currentTurnId.value) return
    handleError(data)
    cleanupAndFinish()
  })
  activeUnsubs.push(unsubError)

  const unsubProgress = bridge.on('chat:progress', (data) => {
    if (data.turn_id && currentTurnId.value && data.turn_id !== currentTurnId.value) return
    if (data?.task_id) {
      taskProgress.value = `Running task ${data.completed || 0}/${data.total || '?'}`
      if (data.failed > 0) taskProgress.value += ` (${data.failed} failed)`
    }
  })
  activeUnsubs.push(unsubProgress)

  const unsubExecutorDone = bridge.on('chat:executor_done', () => {
    cleanupAndFinish()
  })
  activeUnsubs.push(unsubExecutorDone)

  // Handle LLM errors with error code and retry info
  const unsubLLMError = bridge.on('chat:llm_error', (data) => {
    if (data.turn_id && currentTurnId.value && data.turn_id !== currentTurnId.value) return
    const code = data.code || 'ERR_LLM_UNKNOWN'
    const msg = data.message || 'Unknown LLM error'
    const retryable = data.retryable === true
    const attempt = data.retry_attempt || 1
    const maxRetries = data.retry_count || 1
    let displayMsg = `[${code}] ${msg}`
    if (retryable && attempt <= maxRetries) {
      // Wait for retry — don't show final error yet
      taskProgress.value = `LLM error, retrying ${attempt}/${maxRetries}...`
    } else {
      // Final error — show in chat
      addMessage('assistant', `LLM Error: ${displayMsg}`, 'text')
      taskProgress.value = ''
      cleanupAndFinish()
    }
  })
  activeUnsubs.push(unsubLLMError)

  // Handle LLM retry progress
  const unsubLLMRetry = bridge.on('chat:llm_retry', (data) => {
    if (data.turn_id && currentTurnId.value && data.turn_id !== currentTurnId.value) return
    const attempt = data.retry_attempt || 1
    const maxRetries = data.retry_count || 1
    const waitSec = data.wait_seconds || 5
    taskProgress.value = `LLM retry ${attempt}/${maxRetries} (waiting ${waitSec}s)...`
  })
  activeUnsubs.push(unsubLLMRetry)

  function cleanupAndFinish() {
    activeUnsubs.forEach(fn => { try { fn() } catch(_) {} })
    activeUnsubs.length = 0
    isLoading.value = false
    currentTurnId.value = null
  }

  try {
    // Send with runtime overrides and get the server-generated turn_id
    const thinkFlag = thinkEnabled.value ? 'on' : 'off'
    const result = await sendChatMessage(currentSessionId.value, '', text, selectedLLM.value, thinkFlag, effortLevel.value)
    currentTurnId.value = result.turn_id
  } catch (e) {
    addMessage('assistant', `Error: ${e.message}`, 'text')
    cleanupAndFinish()
  }
}

function handleCancel() {
  if (currentTurnId.value) {
    cancelChat(currentTurnId.value)
  }
  cleanupAndFinish()
}

// Load history turns/messages for a session
async function loadSessionMessages(sessionId) {
  if (!sessionId) return
  currentSessionId.value = sessionId
  await loadMessages(sessionId)
}

// Auto-load session: try activeSessionID from config first, fall back to most recent
async function initSession() {
  try {
    // Load LLM list from user config for the dropdown
    const ures = await getUserConfig()
    const uc = ures.config || ures
    if (uc.llms && uc.llms.length > 0) {
      llmList.value = uc.llms
      const defaultIdx = (uc.defaultLLM !== undefined && uc.defaultLLM >= 0 && uc.defaultLLM < uc.llms.length) ? uc.defaultLLM : 0
      const defaultLLM = uc.llms[defaultIdx]
      if (defaultLLM) {
        thinkEnabled.value = defaultLLM.thinking !== false
        if (defaultLLM.reasoningEffort) {
          effortLevel.value = defaultLLM.reasoningEffort
        }
        selectedLLM.value = defaultLLM.name
      }
    }
    // Find the session with the most recent message (top session)
    const res = await getLatestSessionID()
    const topSessionID = res?.session_id
    if (topSessionID) {
      await loadSessionMessages(topSessionID)
      window.dispatchEvent(new CustomEvent('session:loaded', { detail: { session_id: topSessionID } }))
      return
    }
  } catch (e) { console.warn('[ChatPanel] Failed to init session:', e) }
  // No recent messages found — stay in empty state
  currentSessionId.value = null
  window.dispatchEvent(new CustomEvent('session:select', { detail: { session: null } }))
  window.dispatchEvent(new CustomEvent('session:loaded', { detail: { session_id: null } }))
}

async function handleSessionSelect(event) {
  const session = event.detail?.session
  if (!session || !session.session_id) {
    // Null session — clear chat and reset to empty state
    currentSessionId.value = null
    currentTurnId.value = null
    resetMessages()
    window.dispatchEvent(new CustomEvent('session:loaded', { detail: { session_id: null } }))
    return
  }
  try {
    await loadSessionMessages(session.session_id)
  } finally {
    window.dispatchEvent(new CustomEvent('session:loaded', { detail: { session_id: session.session_id } }))
  }
}

onMounted(() => {
  initSession()
  window.addEventListener('session:select', handleSessionSelect)
  // Reload LLM list when config changes
  const unsubRefresh = bridge.on('config:refresh', async () => {
    try {
      const ures = await getUserConfig()
      const uc = ures.config || ures
      if (uc.llms && uc.llms.length > 0) {
        llmList.value = uc.llms
      }
    } catch (_) { /* ignore */ }
  })
  activeUnsubs.push(unsubRefresh)
  // Cancel LLM when session:cancel-llm event fires (from SessionDrawer)
  window.addEventListener('session:cancel-llm', handleCancelLLM)
  // Clear chat when current session is deleted
  const unsubSessionRefresh = bridge.on('session:refresh', async () => {
    if (currentSessionId.value) {
      try {
        const { getSession } = await import('../../api/session')
        const sessionRes = await getSession(currentSessionId.value)
        if (!sessionRes?.session) {
          currentSessionId.value = null
          resetMessages()
        }
      } catch (_) {
        // Session likely doesn't exist — clear
        currentSessionId.value = null
        resetMessages()
      }
    }
  })
  activeUnsubs.push(unsubSessionRefresh)
})

onUnmounted(() => {
  window.removeEventListener('session:select', handleSessionSelect)
  window.removeEventListener('session:cancel-llm', handleCancelLLM)
  // Clean up any bridge listeners that may still be active mid-turn
  activeUnsubs.forEach(fn => fn())
  activeUnsubs.length = 0
  resetMessages()
})
</script>

<style scoped>
.chat-panel {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.no-session-prompt {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-muted);
  font-size: 13px;
}

.task-progress-bar {
  flex-shrink: 0;
  padding: 3px 12px;
  font-size: 11px;
  color: var(--accent);
  background: var(--bg-surface);
  border-top: 1px solid var(--border);
  text-align: center;
}

.llm-select {
  width: 120px;
}
</style>
