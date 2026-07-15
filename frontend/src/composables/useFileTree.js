import { ref, computed, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import bridge from '../utils/bridge'
import { getFileTree, getFileTreeChildren } from '../api/file'

/**
 * Shared reactive file tree state and operations.
 *
 * Singleton pattern: module-level refs are shared across all consumers.
 * Auto-subscribes to Go's `file:changed` and `file:watcher-error` push events.
 *
 * Usage:
 *   const { loading, reloadRoot, reloadNode } = useFileTree()
 */

// Singleton reactive state
const loading = ref(false)
const lastChangedDir = ref('')

// Debounce timer for file:changed events
let debounceTimer = null

// Subscription ref-counting
let refCount = 0
let unsubFileChanged = null
let unsubFileWatcherError = null

let onChangedCallback = null

function subscribe() {
  refCount++
  if (unsubFileChanged) return // already subscribed
  unsubFileChanged = bridge.on('file:changed', (data) => {
    const changedDir = data?.dir
    if (!changedDir) return
    lastChangedDir.value = changedDir

    // Global debounce: only fire within a 500ms window
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => {
      debounceTimer = null
      if (onChangedCallback) {
        onChangedCallback(changedDir)
      }
    }, 500)
  })
  unsubFileWatcherError = bridge.on('file:watcher-error', (data) => {
    ElMessage.error(data?.error || `File watcher error: ${JSON.stringify(data)}`)
  })
}

function unsubscribe() {
  refCount--
  if (refCount <= 0 && unsubFileChanged) {
    unsubFileChanged()
    unsubFileChanged = null
    if (unsubFileWatcherError) {
      unsubFileWatcherError()
      unsubFileWatcherError = null
    }
    if (debounceTimer) {
      clearTimeout(debounceTimer)
      debounceTimer = null
    }
    onChangedCallback = null
    refCount = 0
  }
}

export function useFileTree() {
  subscribe()

  function teardown() {
    unsubscribe()
  }

  /**
   * Register a callback that fires when files change (debounced).
   * Only one callback at a time (last one wins) — sufficient for single-consumer usage.
   */
  function onFileChanged(cb) {
    onChangedCallback = cb
  }

  /**
   * Fetch the first-level tree from root.
   */
  async function loadRootTree() {
    loading.value = true
    try {
      const res = await getFileTree('')
      return res.tree?.children || []
    } catch (e) {
      console.error('[useFileTree] Failed to load root tree:', e)
      return []
    } finally {
      loading.value = false
    }
  }

  /**
   * Lazy-load children of a directory node.
   */
  async function loadChildren(dirPath) {
    try {
      const res = await getFileTreeChildren(dirPath)
      return res.children || []
    } catch (e) {
      console.error('[useFileTree] Failed to load children:', e)
      return []
    }
  }

  return {
    // State
    loading,
    lastChangedDir,
    // Actions
    onFileChanged,
    loadRootTree,
    loadChildren,
    reloadRoot() {
      // Provided for backward compatibility; FileTree.vue manages its own el-tree ref.
      // The actual reloadRoot/reloadNode is in FileTree.vue because it needs the treeRef.
    },
    // Lifecycle
    teardown,
  }
}
