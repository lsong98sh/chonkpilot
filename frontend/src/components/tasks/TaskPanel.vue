<template>
  <div class="task-panel-split">
    <!-- Left: session tree -->
    <div ref="treePanel" class="tree-panel" :style="{ width: treeWidth + 'px' }">
      <div class="panel-header">
        <el-icon><List /></el-icon>
        <span>SESSIONS</span>
        <div class="header-spacer" />
        <el-tooltip content="Refresh session list" placement="top">
          <el-button size="small" text @click="refreshSessions">
            <el-icon><Refresh /></el-icon>
          </el-button>
        </el-tooltip>
      </div>
      <div class="panel-scroll">
        <SessionTree ref="sessionTreeRef" @session-selected="handleSessionSelected" />
      </div>
    </div>
    <!-- Resizer -->
    <div class="resizer resizer-col" :class="{ active: resizing }" @mousedown="onStartResize" />
    <!-- Right: session chat -->
    <div class="chat-panel">
      <div class="panel-header">
        <el-icon><ChatDotSquare /></el-icon>
        <span>SESSION DETAIL</span>
        <template v-if="selectedSession">
          <el-tag size="small" type="info" class="session-id-tag">
            #{{ selectedSession.session_id?.slice(0, 8) }}
          </el-tag>
          <span v-if="sessionTurnCount > 0" class="turn-count">{{ sessionTurnCount }} turns</span>
        </template>
        <div class="header-spacer" />
        <template v-if="selectedSession">
          <el-tooltip content="Scroll to top" placement="bottom">
            <el-button size="small" text @click="chatScrollTop">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
                <line x1="12" y1="20" x2="12" y2="7"/>
                <polyline points="5,14 12,7 19,14"/>
                <line x1="4" y1="12" x2="20" y2="12"/>
              </svg>
            </el-button>
          </el-tooltip>
          <el-tooltip content="Scroll to bottom" placement="bottom">
            <el-button size="small" text @click="chatScrollBottom">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
                <line x1="12" y1="4" x2="12" y2="17"/>
                <polyline points="5,10 12,17 19,10"/>
                <line x1="4" y1="12" x2="20" y2="12"/>
              </svg>
            </el-button>
          </el-tooltip>
        </template>
      </div>
      <div class="panel-scroll">
        <SessionChat ref="sessionChatRef" :session-id="selectedSession?.session_id" @turn-count-change="onTurnCountChange" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import SessionTree from './SessionTree.vue'
import SessionChat from './SessionChat.vue'
import { useTask } from '../../composables/useTask'

const emit = defineEmits(['session-selected'])

// Singleton task progress tracking (auto-subscribes to chat:progress)
const { teardown: taskTeardown } = useTask()

const treePanel = ref(null)
const treeWidth = ref(240)
const resizing = ref(false)
const selectedSession = ref(null)
const sessionTreeRef = ref(null)
const sessionChatRef = ref(null)
const sessionTurnCount = ref(0)

function handleSessionSelected(session) {
  selectedSession.value = session
}

function refreshSessions() {
  sessionTreeRef.value?.loadSessions()
}

function chatScrollTop() {
  sessionChatRef.value?.scrollTop()
}

function chatScrollBottom() {
  sessionChatRef.value?.scrollBottom()
}

function onTurnCountChange(count) {
  sessionTurnCount.value = count
}

function onStartResize(e) {
  e.preventDefault()
  e.stopPropagation()
  resizing.value = true
  const startX = e.clientX
  const startW = treePanel.value?.offsetWidth || 240

  function onMove(ev) {
    ev.preventDefault()
    const d = ev.clientX - startX
    const w = Math.max(180, Math.min(400, startW + d))
    treeWidth.value = w
  }

  function onUp() {
    resizing.value = false
    window.removeEventListener('mousemove', onMove, true)
    window.removeEventListener('mouseup', onUp, true)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
  window.addEventListener('mouseup', onUp, true)
  window.addEventListener('mousemove', onMove, { passive: false, capture: true })
}

onMounted(() => {
  window.addEventListener('session:select', (e) => {
    const session = e.detail?.session
    if (!session || !session.session_id) {
      selectedSession.value = null
    }
  })
})

onUnmounted(() => {
  taskTeardown()
})
</script>

<style scoped>
.task-panel-split {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.tree-panel {
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-right: 1px solid var(--border);
  flex-shrink: 0;
  min-width: 180px;
}

.chat-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  min-width: 0;
  overflow: hidden;
}

.panel-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  min-height: 28px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.8px;
  color: var(--text-muted);
  text-transform: uppercase;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}

.header-spacer {
  flex: 1;
  min-width: 0;
}

.session-id-tag {
  font-family: var(--font-mono);
}

.turn-count {
  font-size: 10px;
  color: var(--text-muted);
  white-space: nowrap;
}

.panel-scroll {
  flex: 1;
  overflow: hidden;
}

/* ── Resizer ── */
.resizer-col {
  width: 4px;
  flex-shrink: 0;
  cursor: col-resize;
  background: transparent;
  transition: background 0.12s;
}

.resizer-col:hover {
  background: var(--accent);
}

.resizer-col.active {
  background: var(--accent);
}
</style>
