import { ref } from 'vue'
import { ElMessage } from 'element-plus'

/** Compute total content size of a tool_pair (arguments + result) in bytes. */
function computeContentSize(callArgs, result) {
  let sz = 0
  if (callArgs) sz += typeof callArgs === 'string' ? callArgs.length : JSON.stringify(callArgs).length
  if (result) sz += result.length
  return sz
}

/**
 * Build a brief (≤160 chars) for the tool_pair line from call + result info.
 */
function computeBrief(tool, simplified, resultSimplified, status, resultSuccess) {
  let brief = simplified || (tool + '(...)')
  if (status === 'done') {
    const rb = resultSimplified || 'ok'
    brief += '  ' + rb
  } else if (status === 'failed') {
    const rb = resultSimplified || 'failed'
    brief += '  ' + rb
  }
  if (brief.length > 160) {
    brief = brief.slice(0, 157) + '...'
  }
  return brief
}

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
  let currentSection = null // 'reasoning', 'text', or null — tracks what type we're currently building

  /**
   * Process a stream token (reasoning / tool_call / tool_result / text).
   * Caller is responsible for filtering by turn_id or session_id before calling this.
   */
  function handleToken(data) {
    turnActive.value = true

    // tool_call — always creates a new entry
    if (data.type === 'tool_call') {
      const callSimplified = data.simplified || (data.tool + '(...)')
      const msg = {
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
      }
      msg.brief = computeBrief(msg.tool, msg.simplified, null, 'pending', null)
      msg.content_size = computeContentSize(data.arguments, null)
      messages.value.push(msg)
      currentSection = null // next reasoning/text starts fresh
      return
    }

    // tool_result — merge into matching tool_pair, discard if not found
    if (data.type === 'tool_result') {
      const pair = messages.value.find(m => m.type === 'tool_pair' && m.tool_call_id === data.tool_call_id)
      if (pair) {
        pair.status = data.success === false ? 'failed' : 'done'
        pair.result_simplified = data.simplified || ''
        pair.result = data.content || data.result || ''
        pair.result_success = data.success !== false
        pair.brief = computeBrief(pair.tool, pair.simplified, pair.result_simplified, pair.status, pair.result_success)
        pair.content_size = computeContentSize(pair.arguments, pair.result)
      }
      currentSection = null // next reasoning/text starts fresh
      return
    }

    // reasoning or text (reply) — determine if new round
    const sectionKey = data.type === 'reasoning' ? 'reasoning' : 'text'

    if (currentSection !== sectionKey) {
      // Type changed (vs previous section) → new round, start fresh
      messages.value.push({
        id: Date.now().toString() + (sectionKey === 'reasoning' ? 'r' : 't'),
        role: 'assistant',
        type: sectionKey,
        content: data.content || '',
        createdAt: new Date().toISOString(),
      })
      currentSection = sectionKey
    } else {
      // Same type → append to current section
      const last = messages.value[messages.value.length - 1]
      if (last) {
        last.content += (data.content || '')
      }
    }
  }

  /**
   * Handle stream completion (done event).
   */
  function handleDone() {
    currentSection = null
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
    currentSection = null
    turnActive.value = false
  }

  /**
   * Map a DB message object to view format.
   * Supports tool_pair type with full fields.
   */
  function dbMsgToView(dbMsg) {
    if (dbMsg.type === 'tool_pair') {
      const simplified = dbMsg.simplified || ''
      const resultSimplified = dbMsg.result_simplified || ''
      const status = dbMsg.status || 'done'
      const resultSuccess = dbMsg.result_success !== false
      // recompute brief for historical messages
      let brief = simplified || (dbMsg.tool + '(...)')
      if (status === 'done') {
        brief += '  ' + (resultSimplified || 'ok')
      } else if (status === 'failed') {
        brief += '  ' + (resultSimplified || 'failed')
      }
      if (brief.length > 160) brief = brief.slice(0, 157) + '...'
      return {
        id: 'tp_' + (dbMsg.tool_call_id || Math.random().toString(36).slice(2, 10)),
        role: dbMsg.role || 'assistant',
        type: 'tool_pair',
        tool_call_id: dbMsg.tool_call_id,
        tool: dbMsg.tool,
        simplified: simplified,
        result_simplified: resultSimplified,
        status: status,
        brief: brief,
        content_size: computeContentSize(dbMsg.arguments, dbMsg.result),
        arguments: dbMsg.arguments || '',
        result: dbMsg.result || '',
        has_more: !!dbMsg.has_more,
        result_success: resultSuccess,
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
    currentSection = null
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
    currentSection = null
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
