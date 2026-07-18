import { ref, computed, watch } from 'vue'
import bridge from '../utils/bridge'
import { getTasksByTurn, updateTaskStatus } from '../api/task'
import { useSession } from './useSession'

/**
 * Shared reactive task execution state.
 *
 * Singleton + bridge event pattern (same as useCodebaseStatus).
 * Automatically listens for `llm:event` (tool_progress) push events from Go backend.
 *
 * Usage:
 *   const { tasks, progressMap, loadTasks, teardown } = useTask()
 */

// Singleton reactive state
const tasks = ref([])
const loading = ref(false)

// Track task progress by turn_id → { completed, total, failed }
const progressByTurn = ref({})

// Derived: reactive map of turnId → progress text
const progressMap = computed(() => {
  const map = {}
  for (const [turnId, p] of Object.entries(progressByTurn.value)) {
    let text = `Running task ${p.completed || 0}/${p.total || '?'}`
    if (p.failed > 0) text += ` (${p.failed} failed)`
    map[turnId] = text
  }
  return map
})

// Subscription ref-counting
let refCount = 0
let unsub = null

function subscribe() {
  refCount++
  if (unsub) return // already subscribed
  // Listen for real-time task progress from executor via unified llm:event
  unsub = bridge.on('llm:event', (data) => {
    if (data?._event_type !== 'tool_progress') return
    if (!data?.task_id) return
    const turnId = data.turn_id || '__default__'
    if (!progressByTurn.value[turnId]) {
      progressByTurn.value[turnId] = { completed: 0, total: 0, failed: 0 }
    }
    const p = progressByTurn.value[turnId]
    p.completed = data.completed ?? p.completed
    p.total = data.total ?? p.total
    p.failed = data.failed ?? p.failed
    // Trigger reactivity by replacing the object
    progressByTurn.value = { ...progressByTurn.value }
  })
}

function unsubscribe() {
  refCount--
  if (refCount <= 0 && unsub) {
    unsub()
    unsub = null
    refCount = 0
  }
}

export function useTask() {
  subscribe()

  // Cascade: watch chat session change → auto-clear task state
  const { currentSessionId } = useSession()
  watch(currentSessionId, () => {
    reset()
  })

  async function loadTasks(turnId) {
    loading.value = true
    try {
      tasks.value = await getTasksByTurn(turnId)
    } catch (e) {
      console.error('[useTask] Failed to load tasks:', e)
    } finally {
      loading.value = false
    }
  }

  async function updateTask(taskId, status, progress, result) {
    try {
      await updateTaskStatus(taskId, status, progress, result)
      // Update local cache
      const idx = tasks.value.findIndex(t => t.task_id === taskId)
      if (idx !== -1) {
        tasks.value[idx] = { ...tasks.value[idx], status, progress, result }
      }
    } catch (e) {
      console.error('[useTask] Failed to update task:', e)
    }
  }

  /**
   * Get progress text for a specific turn.
   * @param {string} turnId
   * @returns {string|null}
   */
  function getProgress(turnId) {
    return progressMap.value[turnId] || null
  }

  /**
   * Clear all task state. Called when session changes so stale task data
   * from the previous session does not linger in the UI.
   */
  function reset() {
    tasks.value = []
    progressByTurn.value = {}
  }

  function teardown() {
    unsubscribe()
  }

  return {
    tasks,
    loading,
    progressByTurn,
    progressMap,
    loadTasks,
    updateTask,
    getProgress,
    reset,
    teardown,
  }
}
