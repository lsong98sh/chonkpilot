import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import bridge from '../utils/bridge'
import { getFileTree, getFileTreeChildren } from '../api/file'

/**
 * Shared reactive file tree state and operations.
 *
 * Singleton pattern: module-level refs are shared across all consumers.
 * Subscribes to Go's `file:dir-contents` and `file:watcher-error` push events.
 *
 * File change events are collected into a queue (Map of dir → {dir,children})
 * and flushed after 300ms of quiescence. The callback receives an array of
 * DirContent objects so the consumer can do incremental updates with a single
 * expanded-keys save/restore cycle, preventing auto-collapse.
 *
 * Usage:
 *   const { loading, onFileChanged, teardown } = useFileTree()
 *   onFileChanged((changes) => { ... })
 *     // changes: [{dir: string, children: [{name,path,is_dir}]}, ...]
 */

// Singleton reactive state
const loading = ref(false)
const lastChangedDir = ref('')

// Event queue: Map<dir, {dir, children}> – dedup by directory path
const changeMap = new Map()

let flushTimer = null
let batchCallback = null

// Subscription ref-counting
let refCount = 0
let unsubDirContents = null
let unsubFileWatcherError = null

function subscribe() {
  refCount++
  if (unsubDirContents) return // already subscribed
  unsubDirContents = bridge.on('file:dir-contents', (data) => {
    const dir = data?.dir
    if (!dir) return
    lastChangedDir.value = dir
    changeMap.set(dir, { dir, children: data.children || [] })
    scheduleFlush()
  })
  unsubFileWatcherError = bridge.on('file:watcher-error', (data) => {
    ElMessage.error(data?.error || `File watcher error: ${JSON.stringify(data)}`)
  })
}

function scheduleFlush() {
  if (flushTimer) clearTimeout(flushTimer)
  flushTimer = setTimeout(flushQueue, 300)
}

function flushQueue() {
  flushTimer = null
  if (changeMap.size === 0) return

  // Snapshot and clear
  const changes = [...changeMap.values()]
  changeMap.clear()

  if (batchCallback) {
    batchCallback(changes)
  }

  // If more events arrived during callback, schedule another flush
  if (changeMap.size > 0) {
    scheduleFlush()
  }
}

function unsubscribe() {
  refCount--
  if (refCount <= 0 && unsubDirContents) {
    unsubDirContents()
    unsubDirContents = null
    if (unsubFileWatcherError) {
      unsubFileWatcherError()
      unsubFileWatcherError = null
    }
    if (flushTimer) {
      clearTimeout(flushTimer)
      flushTimer = null
    }
    changeMap.clear()
    batchCallback = null
    refCount = 0
  }
}

export function useFileTree() {
  subscribe()

  function teardown() {
    unsubscribe()
  }

  /**
   * Register a callback that fires when file changes are batched (debounced).
   * The callback receives an array of DirContent objects:
   *   { dir: string, children: [{name, path, is_dir}] }
   */
  function onFileChanged(cb) {
    batchCallback = cb
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
    // Lifecycle
    teardown,
  }
}
