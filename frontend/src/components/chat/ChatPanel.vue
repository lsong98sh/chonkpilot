<template>
  <div class="chat-panel">
    <MessageList ref="messageListRef" :messages="messages" :turn-active="isLoading" :session-id="currentSessionId" :has-more="hasMore" :loading-more="loadingMore" @load-more="loadMoreMessages" />
    <div v-if="taskProgress" class="task-progress-bar">{{ taskProgress }}</div>
    <InputBox @send="handleSend" @cancel="handleCancel" :loading="isLoading">
      <template #controls>
        <Popover placement="top-end" :width="160">
          <template #reference>
            <Tag type="info" style="cursor:pointer">
              {{ selectedLLMLabel }}
            </Tag>
          </template>
          <div class="popover-list">
            <div
              v-for="llm in llmList"
              :key="llm.name"
              class="popover-item"
              :class="{ active: selectedLLM === llm.name }"
              @click="selectLLM(llm.name)"
            >
              {{ llm.name }}
            </div>
          </div>
        </Popover>
          <Button
            text
            class="icon-btn"
            title="Thinking mode"
            @click="thinkEnabled = !thinkEnabled""
          >
            <Icon name="think" :size="14" :color="thinkEnabled ? '#1890ff' : '#999'" />
          </Button>
          <Button
            text
            class="icon-btn"
            :title="'Effort: ' + effortLevel"
            @click="toggleEffort"
          >
            <Icon name="effort" :size="14" :color="effortLevel === 'max' ? '#1890ff' : '#999'" />
          </Button>
        </template>
      </InputBox>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { sendChatMessage, cancelChat } from '../../api/chat'
import { createSession, getLatestSessionID, getSession, getActiveSessionID, setActiveSessionID } from '../../api/session'
import { getUserConfig } from '../../api/config'
import bridge from '../../utils/bridge'
import MessageList from './MessageList.vue'
import InputBox from './InputBox.vue'
import Icon from '../icon/Icon.vue'
import { Button, Tag, Popover } from '../ui'
import { createSessionMessages } from '../../utils/sessionMessages'
const {
  messages, turnActive: _turnActive,
  hasMore, loadingMore,
  handleToken, handleDone, handleError,
  loadMessages, loadMoreMessages, teardown: resetMessages,
} = createSessionMessages()

const currentSessionId = ref(null)

const messageListRef = ref(null)
const isLoading = ref(false)
const isInitializing = ref(true) // 防止 initSession 完成前用户快速发消息
const currentTurnId = ref(null)

function scrollTop() {
  messageListRef.value?.scrollTop()
}
function scrollBottom() {
  messageListRef.value?.scrollBottom()
}
defineExpose({ scrollTop, scrollBottom })
const turnUnsubs = []    // 每轮监听器（llm:event, chat:executor_done），cleanupAndFinish 时清除
const permanentUnsubs = []  // 挂载时监听器（config:refresh, session:event, session:cancel-llm），onUnmounted 时清除
const taskProgress = ref('')
const showReasoning = ref(true)
// collapseReasoning replaced by bridge.emit('message:collapse-reasoning')

// Cancel LLM handler — called from SessionDrawer via custom event
function handleCancelLLM() {
  if (currentTurnId.value) {
    cancelChat(currentTurnId.value)
  }
  cleanupAndFinish()
  // Also cancel any pending ask_user dialogs
  import('../../utils/askUserManager').then(({ askUserManager }) => {
    askUserManager.cancelAll()
  })
}

// Unified session event handler — replaces session:select + session:loaded CustomEvents
function handleSessionEvent(data) {
  if (!data) return
  if (data.type === 'selected') {
    if (data.session_id) {
      // NOTE: currentSessionId may already be set by SessionDrawer before the backend
      // event arrives. Always reload to ensure messages are fetched.
      loadSessionMessages(data.session_id)
    }
  } else if (data.type === 'cleared') {
    currentSessionId.value = null
    currentTurnId.value = null
    resetMessages()
  }
}

// Runtime LLM controls
const llmList = ref([])
const selectedLLM = ref('')
const thinkEnabled = ref(true)
const effortLevel = ref('high')
const llmPopoverVisible = ref(false)

const selectedLLMLabel = computed(() => {
  if (!selectedLLM.value) return 'Default LLM'
  return selectedLLM.value
})

function selectLLM(name) {
  selectedLLM.value = name
  llmPopoverVisible.value = false
}

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
  if (!text.trim() || isLoading.value || isInitializing.value) return

  // Auto-create session if none exists (null = empty session, not yet in DB)
  if (!currentSessionId.value) {
    try {
      const s = await createSession('', '')
      const sid = s.session_id || s.id
      currentSessionId.value = sid
      setActiveSessionID(sid).catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
    } catch (e) {
      addMessage('assistant', `Error: 创建会话失败 — ${e.message}`, 'text')
      return
    }
  }

  addMessage('user', text, 'text')
  isLoading.value = true

  // Reset reasoning display for new turn
  showReasoning.value = true
  bridge.emit('message:collapse-reasoning')

  // Unified llm:event — handle all LLM stream events in one handler.
  const unsubLLMEvent = bridge.on('llm:event', (data) => {
    if (currentSessionId.value && data.session_id && data.session_id !== currentSessionId.value) return
    if (data.turn_id && currentTurnId.value && data.turn_id !== currentTurnId.value) return

    const et = data._event_type || ''

    if (et === 'message_chunk' || et === 'tool_call' || et === 'tool_result') {
      if (showReasoning.value && data.type && data.type === 'text') {
        showReasoning.value = false
        bridge.emit('message:collapse-reasoning')
      }
      handleToken(data)
      return
    }

    if (et === 'complete') {
      handleDone()
      cleanupAndFinish()
      return
    }

    if (et === 'error') {
      handleError(data)
      cleanupAndFinish()
      return
    }

    if (et === 'tool_progress') {
      if (data?.task_id) {
        taskProgress.value = `Running task ${data.completed || 0}/${data.total || '?'}`
        if (data.failed > 0) taskProgress.value += ` (${data.failed} failed)`
      }
      return
    }

    if (et === 'llm_error') {
      const code = data.code || 'ERR_LLM_UNKNOWN'
      const msg = data.message || 'Unknown LLM error'
      const retryable = data.retryable === true
      const attempt = data.retry_attempt || 1
      const maxRetries = data.retry_count || 1
      let displayMsg = `[${code}] ${msg}`
      if (retryable && attempt <= maxRetries) {
        taskProgress.value = `LLM error, retrying ${attempt}/${maxRetries}...`
      } else {
        addMessage('assistant', `LLM Error: ${displayMsg}`, 'text')
        taskProgress.value = ''
        cleanupAndFinish()
      }
      return
    }

    if (et === 'llm_retry') {
      const attempt = data.retry_attempt || 1
      const maxRetries = data.retry_count || 1
      const waitSec = data.wait_seconds || 5
      taskProgress.value = `LLM retry ${attempt}/${maxRetries} (waiting ${waitSec}s)...`
      return
    }
  })
  turnUnsubs.push(unsubLLMEvent)

  const unsubExecutorDone = bridge.on('chat:executor_done', () => {
    cleanupAndFinish()
  })
  turnUnsubs.push(unsubExecutorDone)

  try {
    const thinkFlag = thinkEnabled.value ? 'on' : 'off'
    const result = await sendChatMessage(currentSessionId.value, '', text, selectedLLM.value, thinkFlag, effortLevel.value)
    currentTurnId.value = result.turn_id
  } catch (e) {
    addMessage('assistant', `Error: ${e.message}`, 'text')
    cleanupAndFinish()
  }
}

function cleanupAndFinish() {
  turnUnsubs.forEach(fn => { try { fn() } catch(e) { console.error('[ChatPanel] turnUnsubs cleanup error:', e) } })
  turnUnsubs.length = 0
  isLoading.value = false
  currentTurnId.value = null
  handleDone()
}

function handleCancel() {
  if (currentTurnId.value) {
    cancelChat(currentTurnId.value)
  }
  cleanupAndFinish()
}

async function loadSessionMessages(sessionId) {
  if (!sessionId) return
  currentSessionId.value = sessionId
  await loadMessages(sessionId)
}

async function initSession() {
  try {
    try {
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

      const activeRes = await getActiveSessionID()
      let targetSessionID = activeRes?.session_id || null

      if (targetSessionID) {
        try {
          const sessionRes = await getSession(targetSessionID)
          const session = sessionRes?.session
          if (session && !session.parent_id) {
            await loadSessionMessages(targetSessionID)
            setActiveSessionID(targetSessionID).catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
            return
          }
        } catch (e) { console.error('[ChatPanel] getSession error:', e) }
      }

      const res = await getLatestSessionID()
      const topSessionID = res?.session_id
      if (topSessionID) {
        await loadSessionMessages(topSessionID)
        setActiveSessionID(topSessionID).catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
        return
      }
    } catch (e) { console.warn('[ChatPanel] Failed to init session:', e) }
    currentSessionId.value = null
    setActiveSessionID('').catch(e => console.warn('[ChatPanel] setActiveSessionID error:', e))
  } finally {
    isInitializing.value = false
  }
}

onMounted(() => {
  initSession()
  const unsubSessionSelected = bridge.on('session:selected', (data) => {
    currentSessionId.value = data?.session_id || null
  })
  permanentUnsubs.push(unsubSessionSelected)
  const unsubSessionEvent = bridge.on('session:event', handleSessionEvent)
  permanentUnsubs.push(unsubSessionEvent)
  const unsubRefresh = bridge.on('config:refresh', async () => {
    try {
      const ures = await getUserConfig()
      const uc = ures.config || ures
      if (uc.llms && uc.llms.length > 0) {
        llmList.value = uc.llms
      }
    } catch (e) { console.warn('[ChatPanel] config:refresh error:', e) }
  })
  permanentUnsubs.push(unsubRefresh)
  window.addEventListener('session:cancel-llm', handleCancelLLM)
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
        currentSessionId.value = null
        resetMessages()
      }
    }
  })
  permanentUnsubs.push(unsubSessionRefresh)
})

onUnmounted(() => {
  window.removeEventListener('session:cancel-llm', handleCancelLLM)
  permanentUnsubs.forEach(fn => fn())
  permanentUnsubs.length = 0
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

:deep(.popover-list) {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
:deep(.popover-item) {
  padding: 6px 10px;
  font-size: 12px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-primary, #333);
  transition: background 0.12s;
}
:deep(.popover-item:hover) {
  background: var(--bg-hover, #f0f0f0);
}
:deep(.popover-item.active) {
  background: var(--accent, #409eff);
  color: #fff;
}
</style>