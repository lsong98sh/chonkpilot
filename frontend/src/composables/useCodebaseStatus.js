import { ref, computed, onUnmounted } from 'vue'
import bridge from '../utils/bridge'

/**
 * Shared reactive codebase index status.
 *
 * Usage:
 *   const { pending, files, progress, ok } = useCodebaseStatus()
 *
 * The store auto-subscribes to Go's `codebase:status` push events
 * (app.go pollCodebaseStatus → runtime.EventsEmit).
 * Multiple consumers share the same reactive references.
 */

// Singleton reactive state
const files = ref(0)
const symbols = ref(0)
const pending = ref(0)
const indexing = ref(0)
const failed = ref(0)
const failedExhausted = ref(0)
const totalFiles = ref(0)
const loading = ref(true)

// Derived
const total = computed(() => files.value + pending.value + indexing.value)
const progress = computed(() => {
  const t = total.value
  if (t === 0) return 100
  return +((files.value / t) * 100).toFixed(1)
})
const ok = computed(() => pending.value === 0 && indexing.value === 0)

// Subscription ref-counting
let refCount = 0
let unsub = null

function subscribe() {
  refCount++
  if (unsub) return // already subscribed
  unsub = bridge.on('codebase:status', (payload) => {
    if (!payload) return
    files.value = payload.files ?? 0
    symbols.value = payload.symbols ?? 0
    pending.value = payload.pending ?? 0
    indexing.value = payload.indexing ?? 0
    failed.value = payload.failed ?? 0
    failedExhausted.value = payload.failed_exhausted ?? 0
    totalFiles.value = payload.totalFiles ?? 0
    if (loading.value) loading.value = false
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

let cleanupRegistered = false

export function useCodebaseStatus() {
  // Auto-subscribe on first use
  subscribe()

  // Register onUnmounted only once (component-level auto cleanup)
  // We use a trick: return a cleanup pair; the caller should call it
  // in onUnmounted. But most callers just want the data.

  // For component auto-cleanup, return a teardown function
  function teardown() {
    unsubscribe()
  }

  return {
    // State
    files,
    symbols,
    pending,
    indexing,
    failed,
    failedExhausted,
    totalFiles,
    loading,
    // Derived
    total,
    progress,
    ok,
    // Actions
    /** Reset failed items back to pending for retry */
    async resetFailed() {
      try {
        await window.go.main.App.ResetFailedCodebaseIndex()
      } catch (e) {
        console.error('reset failed items error:', e)
      }
    },
    // Lifecycle
    teardown,
  }
}
