import { ref } from 'vue'
import bridge from '../utils/bridge'
import { getAllConfig, setConfig } from '../api/config'

/**
 * Shared reactive project config state.
 *
 * Singleton + bridge event pattern (same as useCodebaseStatus).
 * Automatically listens for `config:refresh` push events from Go backend.
 *
 * Usage:
 *   const { config, loading, updateConfig, teardown } = useConfig()
 */

// Singleton reactive state
const config = ref({})
const loading = ref(true)

// Subscription ref-counting
let refCount = 0
let unsub = null

async function loadConfigInternal() {
  try {
    const res = await getAllConfig()
    config.value = { ...(res.config || {}), llms: res.llms, workDir: res.workDir, projectTools: res.projectTools }
  } catch (e) {
    console.error('[useConfig] Failed to load config:', e)
  } finally {
    loading.value = false
  }
}

function subscribe() {
  refCount++
  if (unsub) return // already subscribed
  // Subscribe to config:refresh push events
  unsub = bridge.on('config:refresh', () => {
    loadConfigInternal()
  })
  // Load initial data
  loadConfigInternal()
}

function unsubscribe() {
  refCount--
  if (refCount <= 0 && unsub) {
    unsub()
    unsub = null
    refCount = 0
  }
}

export function useConfig() {
  subscribe()

  async function loadConfig() {
    await loadConfigInternal()
  }

  async function updateConfig(key, value) {
    try {
      await setConfig(key, value)
      config.value[key] = value
    } catch (e) {
      console.error('[useConfig] Failed to update config:', e)
    }
  }

  function teardown() {
    unsubscribe()
  }

  return {
    config,
    loading,
    loadConfig,
    updateConfig,
    teardown,
  }
}
