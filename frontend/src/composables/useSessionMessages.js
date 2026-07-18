import { ref } from 'vue'
import { ElMessage } from 'element-plus'

/**
 * Shared session message management.
 *
 * NOT a singleton — each chat/session view needs its own message list.
 * Extracts the common logic between ChatPanel and SessionChat:
 * - Stream event processing (reasoning / tool_call→tool_pair / tool_result / text)
 * - DB message loading & mapping
 *
 * Usage:
 *   const { messages, turnActive, handleToken, handleDone, handleError, loadMessages, teardown } = useSessionMessages()
 */
export function useSessionMessages() {
  const messages = ref([])
  const turnActive = ref(false)
  let hasAssistantText = false

  // Track last-seen index per turn to prevent duplicate chunk appends
  // When the same chunk index arrives twice (e.g. from event re-delivery),
  // we skip the duplicate to avoid content inflation.
  let lastTextIndex = -1

  /**
   * Process a stream token (reasoning / tool_call / tool_result / text).
   * Caller is responsible for filtering by turn_id or session_id before calling this.
   */
  function handleToken(data) {
    turnActive.value = true

    if (data.type === 'reasoning') {
      // Reasoning events don't carry a reliable Index, so always process them.
      // Append to last reasoning message, or create new one
      const last = messages.value[messages.value.length - 1]
      if (last && last.type === 'reasoning' && last.role === 'assistant') {
        last.content += (data.content || '')
      } else {
        messages.value.push({
          id: Date.now().toString() + 'r',
          role: 'assistant',
          type: 'reasoning',
          content: data.content || '',
          createdAt: new Date().toISOString(),
        })
      }
      hasAssistantText = false
    } else if (data.type === 'tool_call') {
      // Create a tool_pair message (pending tool result)
      const callSimplified = data.simplified || (data.tool + '(...)')
      messages.value.push({
        role: 'assistant',
        type: 'tool_pair',
        tool_call_id: data.tool_call_id,
        tool: data.tool,
        simplified: callSimplified,
        result_simplified: null,
        status: 'pending',
        arguments: data.arguments,
        result: null,
        result_success: null,
        id: 'tp_' + data.tool_call_id,
        createdAt: new Date().toISOString(),
      })
      hasAssistantText = false
    } else if (data.type === 'tool_result') {
      // Find matching tool_pair and update with result
      const pair = messages.value.find(m => m.type === 'tool_pair' && m.tool_call_id === data.tool_call_id)
      if (pair) {
        pair.status = data.success === false ? 'failed' : 'done'
        pair.result_simplified = data.simplified || ''
        pair.result = data.content || data.result || ''
        pair.result_success = data.success !== false
      }
      hasAssistantText = false
    } else {
      // Regular text — append to last text message or create new one.
      // Deduplicate by index: skip if this index was already processed.
      if (data.index !== undefined && data.index <= lastTextIndex) {
        return
      }
      lastTextIndex = data.index !== undefined ? data.index : lastTextIndex + 1

      if (!hasAssistantText) {
        messages.value.push({
          id: Date.now().toString() + 't',
          role: 'assistant',
          type: 'text',
          content: data.content || '',
          createdAt: new Date().toISOString(),
        })
        hasAssistantText = true
      } else {
        const last = messages.value[messages.value.length - 1]
        if (last) {
          last.content += data.content || ''
        }
      }
    }
  }

  /**
   * Handle stream completion (done event).
   */
  function handleDone() {
    hasAssistantText = false
    lastTextIndex = -1
    turnActive.value = false
  }

  /**
   * Handle stream error.
   * @param {Object} data - { message, code }
   */
  function handleError(data) {
    messages.value.push({
      id: Date.now().toString() + 'e',
      role: 'assistant',
      type: 'text',
      content: `Error: ${data.message || data.code || 'Unknown error'}`,
      createdAt: new Date().toISOString(),
    })
    hasAssistantText = false
    turnActive.value = false
  }

  /**
   * Map a DB message object to view format.
   * Supports tool_pair type with full fields.
   */
  function dbMsgToView(dbMsg) {
    if (dbMsg.type === 'tool_pair') {
      return {
        id: 'tp_' + (dbMsg.tool_call_id || Math.random().toString(36).slice(2, 10)),
        role: dbMsg.role || 'assistant',
        type: 'tool_pair',
        tool_call_id: dbMsg.tool_call_id,
        tool: dbMsg.tool,
        simplified: dbMsg.simplified || '',
        result_simplified: dbMsg.result_simplified || '',
        status: dbMsg.status || 'done',
        arguments: dbMsg.arguments || '',
        result: dbMsg.result || '',
        has_more: !!dbMsg.has_more,
        result_success: dbMsg.result_success !== false,
        createdAt: dbMsg.created_at,
      }
    }
    return {
      id: dbMsg.message_id,
      role: dbMsg.role,
      type: dbMsg.type || 'text',
      content: dbMsg.content || '',
      has_more: !!dbMsg.has_more,
      createdAt: dbMsg.created_at,
    }
  }

  /**
   * Load session messages from DB via getTurnsBySession.
   * Resets messages and turnActive before loading.
   */
  async function loadMessages(sessionId) {
    if (!sessionId) return
    messages.value = []
    turnActive.value = false
    hasAssistantText = false
    try {
      const { getTurnsBySession } = await import('../api/session')
      const res = await getTurnsBySession(sessionId, true) // brief=true: omit large content
      const msgs = (res.messages || []).map(dbMsgToView)
      messages.value = msgs
      return res // caller can also read res.turns etc.
    } catch (e) {
      console.warn('[useSessionMessages] Failed to load messages:', e)
      ElMessage.error('加载消息失败: ' + (e.message || e))
    }
  }

  /**
   * Reset all internal state.
   */
  function teardown() {
    messages.value = []
    turnActive.value = false
    hasAssistantText = false
    lastTextIndex = -1
  }

  return {
    messages,
    turnActive,
    handleToken,
    handleDone,
    handleError,
    loadMessages,
    teardown,
  }
}
