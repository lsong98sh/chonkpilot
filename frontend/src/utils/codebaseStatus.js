import { ref, computed } from 'vue'
import bridge from './bridge'

/**
 * Shared reactive codebase index status — module-level singletons.
 * Auto-subscribes to Go's `codebase:status` push events.
 */

const files = ref(0)
const symbols = ref(0)
const pending = ref(0)
const indexing = ref(0)
const failed = ref(0)
const failedExhausted = ref(0)
const totalFiles = ref(0)
const loading = ref(true)

const total = computed(() => files.value + pending.value + indexing.value)
const progress = computed(() => {
  const t = total.value
  if (t === 0) return 100
  return +((files.value / t) * 100).toFixed(1)
})
const ok = computed(() => pending.value === 0 && indexing.value === 0)

// Auto-subscribe once
bridge.on('codebase:status', (payload) => {
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

async function resetFailed() {
  try {
    await window.go.main.App.ResetFailedCodebaseIndex()
  } catch (e) {
    console.error('reset failed items error:', e)
  }
}

export {
  files,
  symbols,
  pending,
  indexing,
  failed,
  failedExhausted,
  totalFiles,
  loading,
  total,
  progress,
  ok,
  resetFailed,
}
