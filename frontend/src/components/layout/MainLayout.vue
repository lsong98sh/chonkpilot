<template>
  <div class="main-layout">
    <SplitPanel direction="vertical" :gap="0" :panes="outerConfig">
      <template #pane-0>
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
      </template>
      <template #pane-1>
        <SplitPanel direction="horizontal" :gap="4" gap-color="var(--border)" :panes="bodyConfig">
          <template #pane-0>
            <SplitPanel direction="vertical" :gap="4" gap-color="var(--border)" :panes="topBottomConfig">
              <template #pane-0>
                <SplitPanel direction="horizontal" :gap="4" gap-color="var(--border)" :panes="filetreeConfig">
                  <template #pane-0>
                    <div class="filetree-panel">
                      <div class="panel-header">
                        <Icon name="folder" />
                        <span class="header-path" :title="workDir">{{ displayPath }}</span>
                        <Icon v-if="vcsInfo.git" name="git" class="vcs-icon vcs-git" title="Git" />
                        <Icon v-if="vcsInfo.svn" name="svn" class="vcs-icon vcs-svn" title="SVN" />
                        <Icon name="setting" class="header-settings-icon" title="IDE Config" @click="openIDEConfig" />
                      </div>
                      <FileTree class="panel-scroll" />
                    </div>
                  </template>
                  <template #pane-1><CodeView /></template>
                </SplitPanel>
              </template>
              <template #pane-1>
                <SplitPanel direction="horizontal" :gap="4" gap-color="var(--border)" :panes="taskConfig">
                  <template #pane-0>
                    <div class="panel-inner">
                      <div class="panel-header">
                        <Icon name="list" />
                        <span>SESSIONS</span>
                        <div class="header-spacer" />
                        <Button text @click="loadSessions" class="icon-btn" title="Refresh session list">
                          <Icon name="refresh" :size="14" color="#000" />
                        </Button>
                      </div>
                      <SessionTree ref="sessionTreeRef" class="panel-scroll" />
                    </div>
                  </template>
                  <template #pane-1>
                    <div class="panel-inner">
                      <div class="panel-header">
                        <Icon name="chat-dot-square" />
                        <span>SESSION DETAIL</span>
                        <template v-if="selectedSessionId">
                          <Tag size="small" type="info" class="session-id-tag">#{{ selectedSessionId.slice(0, 8) }}</Tag>
                          <span v-if="turnCount > 0" class="turn-count">{{ turnCount }} turns</span>
                        </template>
                        <div class="header-spacer" />
                        <template v-if="selectedSessionId">
                          <Button text @click="chatScrollTop" title="Scroll to top">
                            <Icon name="arrow-up" />
                          </Button>
                          <Button text @click="chatScrollBottom" title="Scroll to bottom">
                            <Icon name="arrow-down" />
                          </Button>
                        </template>
                      </div>
                      <SessionChat
                        ref="sessionChatRef"
                        :session-id="selectedSessionId"
                        @turn-count-change="onTurnCountChange"
                        class="panel-scroll"
                      />
                    </div>
                  </template>
                </SplitPanel>
              </template>
            </SplitPanel>
          </template>
          <template #pane-1>
            <div class="panel-inner">
              <div class="panel-header">
                <Icon name="chat-dot-square" />
                <span v-show="chatOpen">CHAT</span>
                <Tag v-if="chatSessionId" size="small" type="info" class="chat-session-tag">
                  #{{ chatSessionId.slice(0, 8) }}
                </Tag>
                <Button text class="new-session-btn icon-btn" @click="handleNewSession" title="New Session">
                  <Icon name="circle-plus" color="#555" />
                </Button>
                <div class="header-spacer" />
                <Popover placement="bottom-end" :width="160">
                  <template #reference>
                    <Tag size="small" :type="activeScenarioId ? 'info' : 'danger'" style="cursor:pointer">
                      {{ activeScenarioLabel }}
                    </Tag>
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
                </Popover>
              </div>
              <div v-show="chatOpen" class="panel-scroll">
                <ChatPanel ref="chatPanelRef" />
              </div>
            </div>
          </template>
        </SplitPanel>
      </template>
      <template #pane-2><StatusBar @open-config="handleOpenConfig" /></template>
    </SplitPanel>

    <ConfigDialog ref="configDialog" />
    <ScenarioDialog ref="scenarioDialog" @changed="loadScenarioOptions" />
    <SessionDrawer ref="sessionDrawer" />
    <AnalyzeDialog ref="analyzeDialog" />
    <AskUserDialog />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, defineAsyncComponent } from 'vue'
import Icon from '../icon/Icon.vue'
import { Button, Tag, Popover } from '../ui'
import { SplitPanel } from '../split'
import Toolbar from '../toolbar/Toolbar.vue'
import FileTree from '../filetree/FileTree.vue'
import ChatPanel from '../chat/ChatPanel.vue'
import SessionTree from '../tasks/SessionTree.vue'
import SessionChat from '../tasks/SessionChat.vue'
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
import { message } from '../ui'
import bridge from '../../utils/bridge'
import { setActiveSessionID } from '../../api/session'
import { openDir, getAllConfig } from '../../api/config'

const workDir = ref('')
const vcsInfo = ref({ git: false, svn: false })
let unsubFileChanged = null
const chatOpen = ref(true)
const filetreeOpen = ref(true)
const taskOpen = ref(true)
const configDialog = ref(null)
const scenarioDialog = ref(null)
const sessionDrawer = ref(null)
const analyzeDialog = ref(null)

const chatPanelRef = ref(null)
const chatSessionId = ref(null)

const activeScenarioId = ref(0)
const scenarioOptions = ref([])
const scenarioPopoverVisible = ref(false)

const selectedSessionId = ref(null)
const turnCount = ref(0)
const sessionTreeRef = ref(null)
const sessionChatRef = ref(null)

const outerConfig = [
  { id: 'toolbar', size: 44, resizable: false },
  { id: 'content', flex: true },
  { id: 'statusbar', size: 24, resizable: false },
]

const bodyConfig = computed(() => [
  { id: 'content', flex: true },
  { id: 'chat', size: 360, min: 280, max: 800, visible: chatOpen.value },
])

const topBottomConfig = computed(() => [
  { id: 'top', flex: true },
  { id: 'bottom', size: 250, min: 100, visible: taskOpen.value },
])

const filetreeConfig = computed(() => [
  { id: 'filetree', size: 260, min: 180, max: 600, visible: filetreeOpen.value },
  { id: 'preview', flex: true },
])

const taskConfig = [
  { id: 'session-tree', size: 280, min: 200, max: 600 },
  { id: 'session-chat', flex: true },
]

const activeScenarioLabel = computed(() => {
  if (!activeScenarioId.value) return '选择场景'
  const found = scenarioOptions.value.find(s => s.id === activeScenarioId.value)
  return found ? found.name : '选择场景'
})

const displayPath = computed(() => {
  const d = workDir.value
  if (!d) return ''
  const parts = d.replace(/\\\\/g, '/').split('/').filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
})

function toggleChat() { chatOpen.value = !chatOpen.value }
function toggleFiletree() { filetreeOpen.value = !filetreeOpen.value }
function toggleTasks() { taskOpen.value = !taskOpen.value }

function onSubSessionChanged({ session_id }) {
  selectedSessionId.value = session_id || null
}

function onTurnCountChange(count) {
  turnCount.value = count
}

function chatScrollTop() {
  sessionChatRef.value?.scrollTop?.()
}

function chatScrollBottom() {
  sessionChatRef.value?.scrollBottom?.()
}

function loadSessions() {
  sessionTreeRef.value?.loadSessions?.()
}

async function loadScenarioOptions() {
  try {
    const res = await window.go.main.App.GetScenarioList()
    scenarioOptions.value = res.scenarios || []
    const activeRes = await window.go.main.App.GetActiveScenario()
    if (activeRes?.prompt) {
      const found = scenarioOptions.value.find(s => s.systemPrompt === activeRes.prompt)
      activeScenarioId.value = found ? found.id : (scenarioOptions.value[0]?.id || null)
    } else if (scenarioOptions.value.length > 0) {
      activeScenarioId.value = scenarioOptions.value[0].id
      await window.go.main.App.SetActiveScenario(activeScenarioId.value)
    } else {
      activeScenarioId.value = null
    }
  } catch (_) {
    scenarioOptions.value = []
  }
}

function selectScenario(id) {
  activeScenarioId.value = id
  onScenarioChange(id)
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
    if (res?.workDir) {
      workDir.value = res.workDir
      // Fetch VCS info immediately after workDir is set
      fetchVCSInfo()
    }
  } catch (e) { console.error(e) }
}

async function fetchVCSInfo() {
  try {
    vcsInfo.value = await window.go.main.App.GetVCSInfo() || { git: false, svn: false }
  } catch (_) {
    vcsInfo.value = { git: false, svn: false }
  }
}

let sessionUnsub = null
let unsubSubsession = null

onMounted(async () => {
  await loadConfig()
  loadScenarioOptions()
  unsubFileChanged = window.runtime?.EventsOn('file:changed', (data) => {
    const path = data?.path || ''
    if (path.endsWith('\\.git') || path.endsWith('/.git')) {
      fetchVCSInfo()
    }
  })
  window.addEventListener('config:open-tab', handleConfigOpenTab)
  const unsubSessionEvent = bridge.on('session:event', handleSessionEvent)
  sessionUnsub = unsubSessionEvent
  // Subscribe to frontend EventBus for sub-session selection
  unsubSubsession = bridge.on('subsessionchanged', onSubSessionChanged)
})

onBeforeUnmount(() => {
  if (typeof unsubFileChanged === 'function') unsubFileChanged()
  window.removeEventListener('config:open-tab', handleConfigOpenTab)
  if (typeof sessionUnsub === 'function') sessionUnsub()
  if (typeof unsubSubsession === 'function') unsubSubsession()
})

function handleSessionEvent(data) {
  chatSessionId.value = data?.session_id || null
  // Do NOT update selectedSessionId here — only SessionTree click controls it.
  // Otherwise, switching main chat session would hijack Task panel's detail view.
  if (!data || data.type === 'cleared') {
    selectedSessionId.value = null
  }
}

function handleNewSession() {
  setActiveSessionID('').catch(e => console.warn('[MainLayout] setActiveSessionID error:', e))
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

async function handleOpenDir(dirPath) {
  try {
    const res = await openDir(dirPath || prompt('Enter directory path:'))
    if (res?.code === 'RESTART_REQUIRED') message.info(res.message)
  } catch (e) { console.error(e) }
}

function handleSearchFile(filePath) {
  if (filePath) {
    window.dispatchEvent(new CustomEvent('file:open', { detail: { path: filePath } }))
  }
}
</script>

<style scoped>
.main-layout {
  height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
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

.chat-session-tag {
  flex-shrink: 0;
}

.icon-btn {
  background: transparent !important;
}

.icon-btn:hover {
  background: transparent !important;
}

.new-session-btn {
  color: #555;
}
.new-session-btn:hover {
  color: #555;
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
  font-size: 12px;
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

.panel-scroll {
  flex: 1;
  overflow-y: auto;
}

.filetree-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-secondary);
}

.panel-inner {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.session-id-tag {
  font-family: var(--font-mono);
}

.turn-count {
  font-size: 10px;
  color: var(--text-muted);
  white-space: nowrap;
}

/* Smaller icons in panel headers */
.filetree-panel .panel-header .b-icon,
.panel-inner .panel-header .b-icon {
  font-size: 12px;
}

/* Smaller VCS icons */
.filetree-panel .panel-header .vcs-icon {
  font-size: 12px;
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
</style>
