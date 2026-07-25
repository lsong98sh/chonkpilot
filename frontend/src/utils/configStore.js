import { ref } from 'vue'
import bridge from './bridge'
import { getAllConfig, setConfig } from '../api/config'

/**
 * Shared reactive project config state — module-level singletons.
 * Auto-subscribes to `config:refresh` events from Go backend.
 */

const config = ref({})
const loading = ref(true)

async function loadConfigInternal() {
  try {
    const res = await getAllConfig()
    config.value = { ...(res.config || {}), llms: res.llms, workDir: res.workDir, projectTools: res.projectTools }
  } catch (e) {
    console.error('[configStore] Failed to load config:', e)
  } finally {
    loading.value = false
  }
}

// Auto-subscribe + initial load
bridge.on('config:refresh', () => {
  loadConfigInternal()
})
loadConfigInternal()

async function updateConfig(key, value) {
  try {
    await setConfig(key, value)
    config.value[key] = value
  } catch (e) {
    console.error('[configStore] Failed to update config:', e)
  }
}

export {
  config,
  loading,
  updateConfig,
}
