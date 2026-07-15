<template>
  <div class="session-tree">
    <div class="tree-scroll">
      <EmptyState v-if="loading && sessions.length === 0" message="Loading..." />
      <EmptyState v-else-if="sessions.length === 0" message="No sub-sessions" />
      <div
        v-for="session in sessions"
        :key="session.session_id"
        class="tree-node"
        :class="{ active: session.session_id === selectedId }"
        @click="handleSelect(session)"
      >
        <div class="node-title-row">
          <!-- Status icon -->
          <el-icon
            v-if="getStatus(session.session_id) === 'running'"
            class="status-icon is-loading"
            color="var(--accent)"
            title="Running"
          >
            <Loading />
          </el-icon>
          <el-icon
            v-else-if="getStatus(session.session_id) === 'error'"
            class="status-icon"
            color="#f56c6c"
            title="Error"
          >
            <CloseFilled />
          </el-icon>
          <el-icon
            v-else-if="getStatus(session.session_id) === 'completed'"
            class="status-icon"
            color="#67c23a"
            title="Completed"
          >
            <CircleCheckFilled />
          </el-icon>
          <span class="node-title">{{ session.title || '#' + (session.session_id?.slice(0, 8) || '?') }}</span>
        </div>
        <div class="node-meta-row">
          <span class="node-id">#{{ session.session_id?.slice(0, 8) }}</span>

        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { listAllSessions } from '../../api/session'
import { useSession } from '../../composables/useSession'
import EmptyState from '../common/EmptyState.vue'

const emit = defineEmits(['session-selected'])

const { subSessionStatus, teardown } = useSession()

const sessions = ref([])
const selectedId = ref(null)
const loading = ref(false)
const currentParentId = ref('') // tracks which parent session's sub-sessions to show

function getStatus(sessionId) {
  return subSessionStatus.value[sessionId] || 'idle'
}

async function loadSessions() {
  if (loading.value) return // prevent concurrent reloads causing flickering
  loading.value = true
  try {
    const res = await listAllSessions()
    const all = res.sessions || []
    // Filter: only show sub-sessions for the current parent (or all if no parent set)
    let filtered = all.filter(s => s.parent_id && s.parent_id !== '')
    if (currentParentId.value) {
      filtered = filtered.filter(s => s.parent_id === currentParentId.value)
    }
    // Sort by created_at descending
    filtered.sort((a, b) => {
      const ta = a.created_at || a.createdAt || ''
      const tb = b.created_at || b.createdAt || ''
      return tb.localeCompare(ta)
    })
    sessions.value = filtered
    if (filtered.length === 0) {
      // No sub-sessions — clear selection to avoid stale content in SessionChat
      selectedId.value = null
      emit('session-selected', null)
    } else if (!selectedId.value) {
      // Auto-select the most recent sub-session
      handleSelect(filtered[0])
    }
  } catch (e) {
    console.error('Failed to load sub-sessions:', e)
  } finally {
    loading.value = false
  }
}

function handleSelect(session) {
  selectedId.value = session.session_id
  emit('session-selected', session)
}

// Debounce helper: waits for quiet period before calling fn
let debounceTimer = null
function debouncedLoadSessions() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    debounceTimer = null
    loadSessions()
  }, 300)
}

// Watch subSessionStatus for new sub-sessions and refresh the list (debounced)
watch(subSessionStatus, (statusMap) => {
  for (const sid of Object.keys(statusMap)) {
    const exists = sessions.value.some(s => s.session_id === sid)
    if (!exists) {
      debouncedLoadSessions()
      break
    }
  }
}, { deep: true })

// When the main session loads, switch to its sub-sessions
function handleSessionLoaded(event) {
  const sid = event.detail?.session_id
  if (!sid) return
  currentParentId.value = sid
  selectedId.value = null // force re-select
  loadSessions()
}

onMounted(() => {
  loadSessions()
  window.addEventListener('session:loaded', handleSessionLoaded)
})

defineExpose({ loadSessions })

onUnmounted(() => {
  window.removeEventListener('session:loaded', handleSessionLoaded)
  teardown()
})
</script>

<style scoped>
.session-tree {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.tree-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}

.tree-node {
  padding: 6px 10px;
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: all var(--transition-fast);
}

.tree-node:hover {
  background: var(--bg-hover);
}

.tree-node.active {
  background: var(--bg-hover);
  border-left-color: var(--accent);
}

.node-title-row {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 2px;
}

.status-icon {
  flex-shrink: 0;
  font-size: 14px;
}

.node-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.node-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--text-muted);
}

.node-id {
  font-family: var(--font-mono);
}
</style>
