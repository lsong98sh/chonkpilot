<template>
  <div class="drawer-shell" v-if="visible">
    <div class="drawer-overlay" @click="close" />
    <div class="drawer-content" :style="{ width: '360px' }">
      <div class="drawer-header">
        <div class="drawer-header-row">
          <span style="font-weight:600">Sessions</span>
          <Button size="small" type="primary" @click="handleCreate">
            + New
          </Button>
        </div>
      </div>

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
              <Icon name="folder-opened" />
              {{ session.work_dir }}
            </div>
          </div>
          <div class="session-actions">
            <Button
              size="small"
              text
              title="Rename session"
              @click.stop="handleRename(session)"
            >
              <Icon name="edit" />
            </Button>
            <Button
              size="small"
              text
              type="danger"
              title="Delete session"
              @click.stop="handleDelete(session)"
            >
              <Icon name="delete" />
            </Button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { confirm } from '../ui'
import { message } from '../ui'
import Icon from '../icon/Icon.vue'
import { Button } from '../ui'
import EmptyState from '../common/EmptyState.vue'
import bridge from '../../utils/bridge'
import * as sessionApi from '../../api/session'

const sessions = ref([])
const currentSessionId = ref(null)
const visible = ref(false)

async function handleSelect(session) {
  currentSessionId.value = session.session_id
  sessionApi.setActiveSessionID(session.session_id).catch(e => console.warn('[SessionDrawer] setActiveSessionID error:', e))
  bridge.emit('session:selected', { session_id: session.session_id })
  visible.value = false
}

async function handleDelete(session) {
  try {
    const confirmed = await confirm(
      `确定要删除会话 #${session.session_id?.slice(0, 8)}？`,
      '确认删除'
    )
    if (!confirmed) return
  } catch {
    return
  }

  const wasCurrent = session.session_id === currentSessionId.value
  if (wasCurrent) {
    window.dispatchEvent(new CustomEvent('session:cancel-llm'))
  }

  await sessionApi.deleteSession(session.session_id)
  sessions.value = sessions.value.filter(s => s.session_id !== session.session_id)
  bridge.emit('session:selected', { session_id: null })

  const remaining = sessions.value
  if (remaining.length === 0) {
    currentSessionId.value = null
    sessionApi.setActiveSessionID('').catch(e => console.warn('[SessionDrawer] setActiveSessionID error:', e))
    visible.value = false
  } else if (wasCurrent) {
    const next = remaining[0]
    currentSessionId.value = next.session_id
    sessionApi.setActiveSessionID(next.session_id).catch(e => console.warn('[SessionDrawer] setActiveSessionID error:', e))
  }
}

async function handleRename(session) {
  const newName = prompt('Enter a new name for this session', session.title || '')
  if (newName) {
    try {
      await sessionApi.updateSessionTitle(session.session_id, newName)
      sessions.value = (await sessionApi.listSessions()).sessions || []
    } catch (e) {
      message.error('Failed to rename: ' + (e.message || e))
    }
  }
}

async function handleCreate() {
  currentSessionId.value = null
  sessionApi.setActiveSessionID('').catch(e => console.warn('[SessionDrawer] setActiveSessionID error:', e))
  bridge.emit('session:selected', { session_id: null })
  visible.value = false
}

async function open() {
  visible.value = true
  try {
    const res = await sessionApi.listSessions()
    sessions.value = res.sessions || []
  } catch (e) {
    console.warn('[SessionDrawer] Failed to load sessions:', e)
  }
}

function close() {
  visible.value = false
}

defineExpose({ open, close })
</script>

<style scoped>
.drawer-shell {
  position: fixed;
  top: 0;
  right: 0;
  height: 100%;
  z-index: 1000;
}
.drawer-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0,0,0,0.3);
}
.drawer-content {
  position: fixed;
  top: 0;
  right: 0;
  height: 100%;
  background: var(--bg-primary);
  border-left: 1px solid var(--border);
  box-shadow: -2px 0 8px rgba(0,0,0,0.1);
  display: flex;
  flex-direction: column;
}
.drawer-header {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.drawer-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  font-weight: 600;
}

.session-list {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
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