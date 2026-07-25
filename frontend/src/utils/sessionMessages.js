import { ref } from 'vue'
import { message } from '../components/ui'

function computeContentSize(callArgs, result) {
  let sz = 0
  if (callArgs) sz += typeof callArgs === 'string' ? callArgs.length : JSON.stringify(callArgs).length
  if (result) sz += result.length
  return sz
}

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

function dbMsgToView(dbMsg) {
  if (dbMsg.type === 'tool_pair') {
    const simplified = dbMsg.simplified || ''
    const resultSimplified = dbMsg.result_simplified || ''
    const status = dbMsg.status || 'done'
    const resultSuccess = dbMsg.result_success !== false
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
 * Create a new session message state instance with paginated loading support.
 * Each chat view (ChatPanel, SessionChat) should have its own instance.
 */
export function createSessionMessages() {
  const messages = ref([])
  const turnActive = ref(false)
  const hasMore = ref(true)        // whether there are older turns to load
  const loadingMore = ref(false)   // whether a load-more is in progress
  let currentSection = null
  let loadedTurnIds = new Set()    // set of turn IDs already loaded
  let currentSessionId = null       // track which session is loaded

  function handleToken(data) {
    turnActive.value = true

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
      currentSection = null
      return
    }

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
      currentSection = null
      return
    }

    const sectionKey = data.type === 'reasoning' ? 'reasoning' : 'text'

    if (currentSection !== sectionKey) {
      messages.value.push({
        id: Date.now().toString() + (sectionKey === 'reasoning' ? 'r' : 't'),
        role: 'assistant',
        type: sectionKey,
        content: data.content || '',
        createdAt: new Date().toISOString(),
      })
      currentSection = sectionKey
    } else {
      const last = messages.value[messages.value.length - 1]
      if (last) {
        last.content += (data.content || '')
      }
    }
  }

  function handleDone() {
    currentSection = null
    turnActive.value = false
  }

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
   * Load the latest batch of messages for a session.
   * Replaces the entire messages array.
   */
  async function loadMessages(sessionId) {
    if (!sessionId) return
    sessionId = sessionId.trim()
    if (!sessionId) return

    messages.value = []
    turnActive.value = false
    currentSection = null
    hasMore.value = true
    loadingMore.value = false
    loadedTurnIds = new Set()
    currentSessionId = sessionId

    try {
      const { getTurnsPaginated } = await import('../api/session')
      const res = await getTurnsPaginated(sessionId, '', 50, 200 * 1024)
      if (res && res.messages) {
        messages.value = res.messages.map(dbMsgToView)
      }
      hasMore.value = !!(res && res.has_more)
      if (res && res.turns) {
        for (const t of res.turns) {
          loadedTurnIds.add(t.turn_id)
        }
      }
      // Auto-preload: if there are more turns, fetch one extra batch in background
      // so the user doesn't hit the top immediately.
      if (hasMore.value && messages.value.length > 0) {
        setTimeout(() => {
          loadMoreMessages().catch(() => {})
        }, 100)
      }
      return res
    } catch (e) {
      console.warn('[sessionMessages] Failed to load messages:', e)
      message.error('加载消息失败: ' + (e.message || e))
    }
  }

  /**
   * Load older messages (before the current oldest loaded turn).
   * Prepends to the messages array.
   */
  async function loadMoreMessages() {
    if (!hasMore.value || loadingMore.value || !currentSessionId) return
    loadingMore.value = true

    try {
      // Find the oldest loaded turn ID
      const turnIds = Array.from(loadedTurnIds)
      const beforeTurnId = turnIds.length > 0 ? turnIds[0] : ''

      const { getTurnsPaginated } = await import('../api/session')
      const res = await getTurnsPaginated(currentSessionId, beforeTurnId, 50, 200 * 1024)

      if (res && res.messages && res.messages.length > 0) {
        const oldMsgs = res.messages.map(dbMsgToView)
        messages.value = [...oldMsgs, ...messages.value]
      }
      hasMore.value = !!(res && res.has_more)
      if (res && res.turns) {
        for (const t of res.turns) {
          loadedTurnIds.add(t.turn_id)
        }
      }
    } catch (e) {
      console.warn('[sessionMessages] Failed to load more messages:', e)
    } finally {
      loadingMore.value = false
    }
  }

  function teardown() {
    messages.value = []
    turnActive.value = false
    currentSection = null
    hasMore.value = true
    loadingMore.value = false
    loadedTurnIds = new Set()
    currentSessionId = null
  }

  return {
    messages,
    turnActive,
    hasMore,
    loadingMore,
    handleToken,
    handleDone,
    handleError,
    loadMessages,
    loadMoreMessages,
    teardown,
  }
}
