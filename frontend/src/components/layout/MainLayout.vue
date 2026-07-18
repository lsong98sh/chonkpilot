<template>
  <div class="main-layout">
    <Toolbar
      :chat-visible="chatOpen"
      :filetree-visible="filetreeOpen"
      :task-visible="taskOpen"
      @open-config="handleOpenConfig"
      @open-session="sessionDrawer.open()"
      @toggle-chat="toggleChat"
      @toggle-filetree="toggleFiletree"
      @toggle-tasks="toggleTasks"
      @open-dir="handleOpenDir"
      @search-file="handleSearchFile"
      @open-analyze="analyzeDialog.open()"
      @open-scenario="handleOpenScenario"
    />

    <div class="body-area">
      <div class="content-area">
        <div class="top-row" :style="{ height: topRowHeight }">
          <div v-show="filetreeOpen" ref="sidebarPanel" class="filetree-panel" :style="{ width: sidebarWidth + 'px' }">
            <div class="panel-header">
              <el-icon><Folder /></el-icon>
              <el-tooltip :content="workDir" placement="top" :show-after="300">
                <span class="header-path" :title="workDir">{{ displayPath }}</span>
              </el-tooltip>
              <el-icon v-if="vcsInfo.git" class="vcs-icon vcs-git" title="Git">
                <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor">
                  <path d="M10.226 17.284c-2.965-.36-5.054-2.493-5.054-5.256 0-1.123.404-2.336 1.078-3.144-.292-.741-.247-2.314.09-2.965.898-.112 2.111.36 2.83 1.01.853-.269 1.752-.404 2.853-.404 1.1 0 1.999.135 2.807.382.696-.629 1.932-1.1 2.83-.988.315.606.36 2.179.067 2.942.72.854 1.101 2 1.101 3.167 0 2.763-2.089 4.852-5.098 5.234.763.494 1.28 1.572 1.28 2.807v2.336c0 .674.561 1.056 1.235.786 4.066-1.55 7.255-5.615 7.255-10.646C23.5 6.188 18.334 1 11.978 1 5.62 1 .5 6.188.5 12.545c0 4.986 3.167 9.12 7.435 10.669.606.225 1.19-.18 1.19-.786V20.63a2.9 2.9 0 0 1-1.078.224c-1.483 0-2.359-.808-2.987-2.313-.247-.607-.517-.966-1.034-1.033-.27-.023-.359-.135-.359-.27 0-.27.45-.471.898-.471.652 0 1.213.404 1.797 1.235.45.651.921.943 1.483.943.561 0 .92-.202 1.437-.719.382-.381.674-.718.944-.943"/>
                </svg>
              </el-icon>
              <el-icon v-if="vcsInfo.svn" class="vcs-icon vcs-svn" title="SVN">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" width="16" height="16">
                  <ellipse cx="12" cy="14" rx="7" ry="4"/>
                  <circle cx="8" cy="14" r="0.6" fill="currentColor"/>
                  <circle cx="16" cy="14" r="0.6" fill="currentColor"/>
                  <circle cx="12" cy="10" r="0.6" fill="currentColor"/>
                  <path d="M7 14 Q5 12 6 9"/>
                  <path d="M17 14 Q19 12 18 9"/>
                  <path d="M11 6 L10 8 M13 6 L14 8"/>
                </svg>
              </el-icon>
              <el-icon class="header-settings-icon" title="IDE Config" @click="openIDEConfig"><Setting /></el-icon>
            </div>
            <FileTree class="panel-scroll" />
          </div>
          <div v-show="filetreeOpen" class="resizer resizer-col" :class="{ active: resizingSidebar }" @mousedown="onStartResizeSidebar" />
          <div class="preview-panel">
            <CodeView :filetree-visible="filetreeOpen" />
          </div>
        </div>
        <div v-show="taskOpen" class="resizer resizer-row" :class="{ active: resizingRow }" @mousedown="onStartResizeRow" />
        <div v-show="taskOpen" class="bottom-row" :style="{ height: bottomRowHeight }">
          <TaskPanel class="task-panel-full" />
        </div>
      </div>
      <div v-show="chatOpen" class="resizer resizer-col" :class="{ active: resizingChat }" @mousedown="onStartResizeChat" />
      <div v-show="chatOpen" ref="chatPanel" class="chat-panel" :style="{ width: chatWidth + 'px' }">
        <div class="panel-header">
          <el-icon><ChatDotSquare /></el-icon>
          <span v-show="chatOpen">CHAT</span>
          <el-tag v-if="chatSessionId" size="small" type="info" class="chat-session-tag">#{{ chatSessionId.slice(0, 8) }}</el-tag>
          <div class="header-spacer" />
          <el-popover trigger="click" placement="bottom-end" :width="160" popper-class="scenario-popover" v-model:visible="scenarioPopoverVisible">
            <template #reference>
              <el-tag size="small" :type="activeScenarioId ? 'info' : 'danger'" style="cursor:pointer">
                {{ activeScenarioLabel }}
              </el-tag>
            </template>
            <div class="popover-list">
              <div
                v-for="s in scenarioOptions"
                :key="s.id"
                class="popover-item"
                :class="{ active: activeScenarioId === s.id }"
                @click="selectScenario(s.id)"
              >
                {{ s.name }}
              </div>
            </div>
          </el-popover>
          <el-button
            text
            size="small"
            @click="toggleChat"
          >
            <el-icon><Close v-if="chatOpen" /><ChatDotSquare v-else /></el-icon>
          </el-button>
        </div>
        <div v-show="chatOpen" class="panel-scroll">
          <ChatPanel ref="chatPanelRef" />
        </div>
      </div>
    </div>

    <ConfigDialog ref="configDialog" />
    <ScenarioDialog ref="scenarioDialog" @changed="loadScenarioOptions" />
    <SessionDrawer ref="sessionDrawer" @session-selected="handleSessionSelected" />
    <AnalyzeDialog ref="analyzeDialog" />
    <AskUserDialog />
    <StatusBar @open-config="handleOpenConfig" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, defineAsyncComponent, watch } from 'vue'
import Toolbar from '../toolbar/Toolbar.vue'
import FileTree from '../filetree/FileTree.vue'
import TaskPanel from '../tasks/TaskPanel.vue'
import ChatPanel from '../chat/ChatPanel.vue'
import ConfigDialog from '../config/ConfigDialog.vue'
import ScenarioDialog from '../scenario/ScenarioDialog.vue'
import SessionDrawer from '../sessions/SessionDrawer.vue'
import AnalyzeDialog from '../config/AnalyzeDialog.vue'
import AskUserDialog from '../chat/AskUserDialog.vue'
import StatusBar from '../statusbar/StatusBar.vue'

const CodeView = defineAsyncComponent({
  loader: () => import('../codeview/CodeView.vue'),
  loadingComponent: {
    template: '<div class="code-view-loading"><div class="loading-spinner"></div><span>Loading editor...</span></div>'
  },
  delay: 200,
})
import { ElMessage } from 'element-plus'
import { openDir, getAllConfig } from '../../api/config'

const workDir = ref('')
const vcsInfo = ref({ git: false, svn: false })
let unsubFileChanged = null
const sidebarWidth = ref(260)
const chatWidth = ref(360)
const chatOpen = ref(true)
const filetreeOpen = ref(true)
const taskOpen = ref(true)
const resizingSidebar = ref(false)
const resizingRow = ref(false)
const resizingChat = ref(false)
const topRowFraction = ref(0.65)
const configDialog = ref(null)
const scenarioDialog = ref(null)
const sessionDrawer = ref(null)
const analyzeDialog = ref(null)

const sidebarPanel = ref(null)
const chatPanel = ref(null)
const chatPanelRef = ref(null)
const chatSessionId = ref(null)

const activeScenarioId = ref(0)
const scenarioOptions = ref([])
const scenarioPopoverVisible = ref(false)

const activeScenarioLabel = computed(() => {
  if (!activeScenarioId.value) return '选择场景'
  const found = scenarioOptions.value.find(s => s.id === activeScenarioId.value)
  return found ? found.name : '选择场景'
})

function selectScenario(id) {
  activeScenarioId.value = id
  onScenarioChange(id)
  scenarioPopoverVisible.value = false
}

const topRowHeight = computed(() => taskOpen.value ? `calc(${topRowFraction.value * 100}% - 2px)` : '100%')
const bottomRowHeight = computed(() => `calc(${(1 - topRowFraction.value) * 100}% - 2px)`)

const displayPath = computed(() => {
  const d = workDir.value
  if (!d) return ''
  const parts = d.replace(/\\\\/g, '/').split('/').filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
})

function toggleChat() { chatOpen.value = !chatOpen.value }
function toggleFiletree() { filetreeOpen.value = !filetreeOpen.value }
function toggleTasks() { taskOpen.value = !taskOpen.value }

async function loadScenarioOptions() {
  try {
    const res = await window.go.main.App.GetScenarioList()
    scenarioOptions.value = res.scenarios || []
    const activeRes = await window.go.main.App.GetActiveScenario()
    if (activeRes?.prompt) {
      // Find matching scenario
      const found = scenarioOptions.value.find(s => s.systemPrompt === activeRes.prompt)
      activeScenarioId.value = found ? found.id : (scenarioOptions.value[0]?.id || null)
    } else if (scenarioOptions.value.length > 0) {
      // Auto-select first scenario if none active
      activeScenarioId.value = scenarioOptions.value[0].id
      await window.go.main.App.SetActiveScenario(activeScenarioId.value)
    } else {
      activeScenarioId.value = null
    }
  } catch (_) {
    scenarioOptions.value = []
  }
}

async function onScenarioChange(id) {
  try {
    await window.go.main.App.SetActiveScenario(id || 0)
  } catch (e) {
    console.error('Failed to set active scenario:', e)
  }
}

function openIDEConfig() {
  window.dispatchEvent(new CustomEvent('file:open', { detail: { path: 'db://ide.db' } }))
}

async function loadConfig() {
  try {
    const res = await getAllConfig()
    if (res?.workDir) workDir.value = res.workDir
  } catch (e) { console.error(e) }
}

async function fetchVCSInfo() {
  try {
    vcsInfo.value = await window.go.main.App.GetVCSInfo() || { git: false, svn: false }
  } catch (_) {
    vcsInfo.value = { git: false, svn: false }
  }
}

watch(workDir, () => {
  if (workDir.value) fetchVCSInfo()
})

onMounted(async () => {
  await loadConfig()
  // Load scenario options
  loadScenarioOptions()
  // Listen for .git creation/deletion to refresh VCS icon
  unsubFileChanged = window.runtime?.EventsOn('file:changed', (data) => {
    const path = data?.path || ''
    if (path.endsWith('\\.git') || path.endsWith('/.git')) {
      fetchVCSInfo()
    }
  })
  // Listen for config:open-tab from CodeView DB config toolbar
  window.addEventListener('config:open-tab', handleConfigOpenTab)

  // Track current chat session ID for the header tag
  window.addEventListener('session:loaded', handleSessionLoaded)
})

onBeforeUnmount(() => {
  if (typeof unsubFileChanged === 'function') unsubFileChanged()
  window.removeEventListener('config:open-tab', handleConfigOpenTab)
  window.removeEventListener('session:loaded', handleSessionLoaded)
})

function handleSessionLoaded(e) {
  chatSessionId.value = e.detail?.session_id || null
}

function handleConfigOpenTab(e) {
  configDialog.value?.open()
}

function handleOpenConfig() {
  configDialog.value?.open()
}

function handleOpenScenario() {
  scenarioDialog.value?.open()
  loadScenarioOptions()
}

function handleSessionSelected(session) {
  window.dispatchEvent(new CustomEvent('session:select', {
    detail: { session },
  }))
}

async function handleOpenDir(dirPath) {
  try {
    const res = await openDir(dirPath || prompt('Enter directory path:'))
    if (res?.code === 'RESTART_REQUIRED') ElMessage.info(res.message)
  } catch (e) { console.error(e) }
}

function handleSearchFile(filePath) {
  if (filePath) {
    window.dispatchEvent(new CustomEvent('file:open', { detail: { path: filePath } }))
  }
}

/* ────────── Resizer (closure, no global ctx) ────────── */

function onStartResizeSidebar(e) {
  e.preventDefault()
  e.stopPropagation()
  resizingSidebar.value = true
  const startX = e.clientX
  const startW = sidebarPanel.value?.offsetWidth || 260

  function onMove(ev) {
    ev.preventDefault()
    const d = ev.clientX - startX
    const w = Math.max(180, Math.min(800, startW + d))
    sidebarWidth.value = w
  }

  function onUp() {
    resizingSidebar.value = false
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

function onStartResizeChat(e) {
  e.preventDefault()
  e.stopPropagation()
  resizingChat.value = true
  const startX = e.clientX
  const startW = chatPanel.value?.offsetWidth || 360

  function onMove(ev) {
    ev.preventDefault()
    const d = ev.clientX - startX
    const w = Math.max(280, Math.min(800, startW - d))
    chatWidth.value = w
  }

  function onUp() {
    resizingChat.value = false
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

function onStartResizeRow(e) {
  e.preventDefault()
  e.stopPropagation()
  resizingRow.value = true
  const startY = e.clientY
  const top = document.querySelector('.top-row')
  const container = document.querySelector('.content-area')
  if (!top || !container) return
  const startH = top.offsetHeight
  const containerH = container.offsetHeight

  function onMove(ev) {
    ev.preventDefault()
    const dy = ev.clientY - startY
    topRowFraction.value = Math.max(0.4, Math.min(0.8, (startH + dy) / containerH))
  }

  function onUp() {
    resizingRow.value = false
    window.removeEventListener('mousemove', onMove, true)
    window.removeEventListener('mouseup', onUp, true)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  document.body.style.cursor = 'row-resize'
  document.body.style.userSelect = 'none'
  window.addEventListener('mouseup', onUp, true)
  window.addEventListener('mousemove', onMove, { passive: false, capture: true })
}
</script>

<style scoped>
.main-layout {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.body-area {
  display: flex;
  flex: 1;
  overflow: hidden;
}

.content-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
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

.panel-header .el-button { margin-left: auto; }

.header-spacer {
  flex: 1;
  min-width: 0;
}
.chat-session-tag {
  flex-shrink: 0;
}
:deep(.popover-list) {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
:deep(.popover-item) {
  padding: 6px 10px;
  font-size: 12px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-primary, #333);
  transition: background 0.12s;
}
:deep(.popover-item:hover) {
  background: var(--bg-hover, #f0f0f0);
}
:deep(.popover-item.active) {
  background: var(--accent, #409eff);
  color: #fff;
}

.header-settings-icon {
  margin-left: auto;
  cursor: pointer;
  color: var(--text-muted);
  font-size: 14px;
}
.header-settings-icon:hover {
  color: var(--accent);
}

.header-path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.8px;
  color: var(--text-muted);
  text-transform: uppercase;
  flex: 1;
  min-width: 0;
}

.vcs-icon {
  margin-left: 4px;
  cursor: default;
  font-size: 14px;
  flex-shrink: 0;
}
.vcs-git {
  color: #f05032;
}
.vcs-svn {
  color: #809cc9;
}

.panel-scroll { flex: 1; overflow-y: auto; }

/* ── Panels ── */
.top-row {
  display: flex;
  overflow: hidden;
  border-bottom: 2px solid var(--border);
}

.bottom-row { display: flex; overflow: hidden; }

.filetree-panel {
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-right: 1px solid var(--border);
  min-width: 180px;
  flex-shrink: 0;
}

.preview-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

.task-panel-full {
  display: flex;
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.chat-panel {
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-left: 1px solid var(--border);
  transition: width 0.2s ease;
  overflow: hidden;
  flex-shrink: 0;
}

.code-view-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 12px;
  color: var(--text-muted);
  font-size: 13px;
}

.loading-spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--border);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* ── Resizer (flex siblings, take real 4px space) ── */
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

.resizer-row {
  height: 4px;
  flex-shrink: 0;
  cursor: row-resize;
  background: transparent;
  transition: background 0.12s;
}

.resizer-row:hover {
  background: var(--accent);
}

.resizer-row.active {
  background: var(--accent);
}

</style>
