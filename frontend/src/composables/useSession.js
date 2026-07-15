import { ref, computed } from 'vue'
import bridge from '../utils/bridge'
import * as sessionApi from '../api/session'

/**
 * Shared reactive session state.
 *
 * Singleton pattern: module-level refs shared across all consumers.
 * Auto-subscribes to Go's `session:refresh` and `sub:*` push events.
 *
 * Usage:
 *   const { sessions, currentSessionId, loadSessions, subSessionStatus } = useSession()
 */

// Singleton reactive state
const sessions = ref([])
const currentSessionId = ref(null)
const loading = ref(false)

// Sub-session status map: session_id → 'running' | 'completed' | 'error' | 'idle'
const subSessionStatus = ref({})

// Subscription ref-counting
let refCount = 0
let unsubRefresh = null
let unsubSubs = []

function subscribe() {
  refCount++
  if (unsubRefresh) return // already subscribed

  unsubRefresh = bridge.on('session:refresh', async () => {
    await loadSessionList()
    // Validate current session still exists after any session change
    if (currentSessionId.value && !sessions.value.some(s => s.session_id === currentSessionId.value)) {
      currentSessionId.value = null
    }
  })

  const statusEvents = [
    ['sub:token', 'running'],
    ['sub:progress', 'running'],
    ['sub:llm_retry', 'running'],
    ['sub:done', 'completed'],
    ['sub:executor_done', 'completed'],
    ['sub:error', 'error'],
    ['sub:llm_error', 'error'],
  ]

  for (const [eventName, status] of statusEvents) {
    const unsub = bridge.on(eventName, (data) => {
      if (data?.sub_session_id) {
        subSessionStatus.value[data.sub_session_id] = status
      }
    })
    unsubSubs.push(unsub)
  }
}

function unsubscribe() {
  refCount--
  if (refCount <= 0 && unsubRefresh) {
    unsubRefresh()
    unsubRefresh = null
    unsubSubs.forEach(fn => fn())
    unsubSubs = []
    subSessionStatus.value = {}
    refCount = 0
  }
}

async function loadSessionList() {
  loading.value = true
  try {
    const res = await sessionApi.listSessions()
    sessions.value = res.sessions || []
  } catch (e) {
    console.error('[useSession] Failed to load sessions:', e)
  } finally {
    loading.value = false
  }
}

export function useSession() {
  subscribe()

  function teardown() {
    unsubscribe()
  }

  async function loadSessions() {
    await loadSessionList()
  }

  async function createSession(workDir, title) {
    const s = await sessionApi.createSession(workDir, title)
    const newSession = s.session || s
    sessions.value.unshift(newSession)
    currentSessionId.value = newSession.session_id || newSession.id
    return newSession
  }

  async function deleteSession(sessionId) {
    await sessionApi.deleteSession(sessionId)
    sessions.value = sessions.value.filter(s => s.session_id !== sessionId)
    if (currentSessionId.value === sessionId) {
      currentSessionId.value = null
    }
  }

  function selectSession(sessionId) {
    currentSessionId.value = sessionId
  }

  function getSubStatus(sessionId) {
    return computed(() => subSessionStatus.value[sessionId] || 'idle')
  }

  return {
    // State
    sessions,
    currentSessionId,
    loading,
    subSessionStatus,
    // Derived
    getSubStatus,
    // Actions
    loadSessions,
    createSession,
    deleteSession,
    selectSession,
    // Lifecycle
    teardown,
  }
}
