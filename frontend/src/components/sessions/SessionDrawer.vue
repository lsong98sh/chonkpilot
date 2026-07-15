<template>
  <el-drawer
    v-model="visible"
    title="Session History"
    :size="360"
    :show-close="false"
    class="session-drawer"
  >
    <template #header>
      <div class="drawer-header">
        <span>Sessions</span>
        <el-button size="small" type="primary" @click="handleCreate">
          + New
        </el-button>
      </div>
    </template>

    <div class="session-list">
      <EmptyState v-if="sessions.length === 0" message="No sessions yet" />

      <div
        v-for="session in sessions"
        :key="session.session_id"
        class="session-card"
        :class="{ active: session.session_id === currentSessionId }"
        @click="handleSelect(session)"
      >
        <div class="session-info">
          <div class="session-title">{{ session.title || 'Untitled' }}</div>
          <div class="session-meta">
            <span class="session-id">#{{ session.session_id?.slice(0, 8) }}</span>
            <span v-if="session.turn_count" class="turn-count">
              {{ session.turn_count }} turns
            </span>
          </div>
          <div v-if="session.work_dir" class="session-dir" :title="session.work_dir">
            <el-icon><FolderOpened /></el-icon>
            {{ session.work_dir }}
          </div>
        </div>
        <div class="session-actions">
          <el-button
            size="small"
            text
            title="Rename session"
            @click.stop="handleRename(session)"
          >
            <el-icon><Edit /></el-icon>
          </el-button>
          <el-button
            size="small"
            text
            type="danger"
            title="Delete session"
            @click.stop="handleDelete(session)"
          >
            <el-icon><Delete /></el-icon>
          </el-button>
        </div>
      </div>
    </div>
  </el-drawer>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessageBox } from 'element-plus'
import { Edit } from '@element-plus/icons-vue'
import { updateSessionTitle } from '../../api/session'
import { useSession } from '../../composables/useSession'
import EmptyState from '../common/EmptyState.vue'

const emit = defineEmits(['session-selected'])

const { sessions, currentSessionId, loadSessions, createSession, deleteSession, teardown } = useSession()

const visible = ref(false)

async function handleSelect(session) {
  currentSessionId.value = session.session_id
  emit('session-selected', session)
  visible.value = false
}

async function handleDelete(session) {
  // 1. Confirm
  try {
    await ElMessageBox.confirm(
      `确定要删除会话 #${session.session_id?.slice(0, 8)}？`,
      '确认删除',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' }
    )
  } catch {
    return // user cancelled
  }

  // 2. Stop LLM if this is the current session
  const wasCurrent = session.session_id === currentSessionId.value
  if (wasCurrent) {
    window.dispatchEvent(new CustomEvent('session:cancel-llm'))
  }

  // 3. Delete from DB
  await deleteSession(session.session_id)

  // 4. Handle post-deletion UI
  const remaining = sessions.value
  if (remaining.length === 0) {
    // No sessions left → set to null (no DB insert), close drawer
    currentSessionId.value = null
    emit('session-selected', { session_id: null })
    visible.value = false
  } else if (wasCurrent) {
    // Was current session, pick first remaining
    const next = remaining[0]
    currentSessionId.value = next.session_id
    emit('session-selected', next)
  }
}

async function handleRename(session) {
  try {
    const { value: newName } = await ElMessageBox.prompt(
      'Enter a new name for this session',
      'Rename Session',
      {
        inputValue: session.title || '',
        confirmButtonText: 'OK',
        cancelButtonText: 'Cancel',
      }
    )
    if (newName) {
      await updateSessionTitle(session.session_id, newName)
      await loadSessions()
    }
  } catch (e) {
    // User cancelled or error
    if (e !== 'cancel') {
      console.error('Failed to rename session:', e)
    }
  }
}

async function handleCreate() {
  currentSessionId.value = null
  emit('session-selected', { session_id: null })
  visible.value = false
}

function open() {
  visible.value = true
  loadSessions()
}

function close() {
  visible.value = false
}

defineExpose({ open, close })
</script>

<style scoped>
.drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  font-weight: 600;
}

.session-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.session-card {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  cursor: pointer;
  transition: all var(--transition-fast);
}

.session-card:hover {
  background: var(--bg-hover);
  border-color: var(--accent);
}

.session-card.active {
  border-color: var(--accent);
  background: var(--bg-hover);
}

.session-info {
  flex: 1;
  min-width: 0;
}

.session-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 4px;
}

.session-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  color: var(--text-muted);
  margin-bottom: 4px;
}

.session-id {
  font-family: var(--font-mono);
}

.turn-count {
  background: var(--bg-surface);
  padding: 1px 6px;
  border-radius: 4px;
}

.session-dir {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-actions {
  flex-shrink: 0;
  margin-left: 8px;
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.session-card:hover .session-actions {
  opacity: 1;
}
</style>
