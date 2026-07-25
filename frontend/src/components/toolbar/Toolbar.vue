<template>
  <div class="toolbar" @dblclick="toggleMaximizeWin">
    <div class="toolbar-left">
      <!-- Brand -->
      <span class="brand">Chonk Pilot</span>
      <span class="separator" />

      <!-- Open Directory: click to open, dropdown for recent dirs -->
      <div class="open-group">
        <Button class="tb-btn open-btn" title="打开目录" @click="handleOpenClick">
          <Icon name="folder-opened" />
          <span class="tb-label">打开</span>
        </Button>
        <div class="dropdown-trigger" ref="recentDirsTriggerRef">
          <Button class="tb-btn dropdown-arrow" title="最近目录" @click="toggleRecentDirs">
            <Icon name="arrow-down" />
          </Button>
          <Teleport to="body">
            <div v-if="showRecentDirs" class="b-dropdown-popper" :style="recentDirsPopperStyle" @click.stop>
              <div
                v-for="dir in recentDirs"
                :key="dir"
                class="b-dropdown-item"
                @click="handleRecentDir(dir)"
              >
                <Icon name="folder" />
                <span class="recent-item">{{ dir }}</span>
              </div>
              <div v-if="recentDirs.length === 0" class="b-dropdown-item is-disabled">
                暂无最近目录
              </div>
            </div>
          </Teleport>
        </div>
      </div>

      <!-- Settings -->
      <Button class="tb-btn" title="设置" @click="$emit('open-config')">
        <Icon name="setting" />
        <span class="tb-label">设置</span>
      </Button>

      <!-- Scenarios -->
      <Button class="tb-btn" title="场景" @click="$emit('open-scenario')">
        <Icon name="collection" />
        <span class="tb-label">场景</span>
      </Button>
    </div>

    <div class="toolbar-center">
      <!-- Global search with custom dropdown -->
      <div class="search-wrapper">
        <div class="search-input-wrap">
          <Icon name="search" class="search-prefix" />
          <input
            v-model="searchQuery"
            placeholder="搜索文件..."
            @keyup.enter="handleSearchEnter"
            @input="onSearchInput"
            @focus="showSearchResults = true"
            @blur="hideSearchResults"
          />
          <Teleport to="body">
            <div v-if="showSearchResults && searchResults.length > 0" class="search-popper" :style="searchPopperStyle">
              <div
                v-for="item in searchResults"
                :key="item.path"
                class="search-result-item"
                @mousedown.prevent="handleSearchSelect(item)"
              >
                <Tag :type="item.matchType === 'filename' ? 'info' : (item.matchType === 'symbol' ? 'warning' : 'success')" size="mini">
                  {{ item.matchType === 'filename' ? '文件名' : (item.matchType === 'symbol' ? '符号' : '路径') }}
                </Tag>
                <span class="search-filename">{{ basename(item.path) }}</span>
                <span class="search-path">{{ item.path }}</span>
              </div>
            </div>
          </Teleport>
        </div>
      </div>
    </div>
    <div class="toolbar-right">
      <!-- Theme -->
      <div class="dropdown-trigger" ref="themeTriggerRef">
        <Button class="tb-btn" title="主题" @click="toggleThemeDropdown">
          <Icon name="magic-stick" />
          <span class="tb-label">主题</span>
        </Button>
        <Teleport to="body">
          <div v-if="showThemeDropdown" class="b-dropdown-popper" :style="themePopperStyle" @click.stop>
            <div
              v-for="t in themes"
              :key="t.id"
              class="b-dropdown-item"
              @click="setTheme(t.id)"
            >
              <Icon v-if="currentTheme === t.id" name="check" />
              <span v-else style="display:inline-block;width:16px" />
              {{ t.label }}
            </div>
          </div>
        </Teleport>
      </div>

      <!-- Sessions -->
      <Button class="tb-btn" title="会话" @click="$emit('open-session')">
        <Icon name="message-box" />
        <span class="tb-label">会话</span>
      </Button>

      <!-- Tasks toggle -->
      <Button
        class="tb-btn"
        :title="taskVisible ? '隐藏任务' : '显示任务'"
        @click="$emit('toggle-tasks')"
      >
        <Icon name="list" :color="taskVisible ? 'var(--accent)' : ''" />
      </Button>

      <!-- Filetree toggle -->
      <Button
        class="tb-btn"
        :title="filetreeVisible ? '隐藏文件树' : '显示文件树'"
        @click="$emit('toggle-filetree')"
      >
        <Icon name="folder" :color="filetreeVisible ? 'var(--accent)' : ''" />
      </Button>

      <!-- Chat toggle -->
      <Button
        class="tb-btn"
        :title="chatVisible ? '隐藏聊天' : '显示聊天'"
        @click="$emit('toggle-chat')"
      >
        <Icon name="chat-dot-square" :color="chatVisible ? 'var(--accent)' : ''" />
      </Button>

      <!-- Window controls (frameless) -->
      <span class="win-controls-sep" />
      <div class="win-controls">
        <button class="win-btn win-minimize" title="最小化" @click="minimizeWin">
          <svg width="10" height="10" viewBox="0 0 12 12"><rect x="1" y="5.5" width="10" height="1" fill="currentColor"/></svg>
        </button>
        <button class="win-btn win-maximize" :title="isMaximized ? '还原' : '最大化'" @click="toggleMaximizeWin">
          <svg v-if="!isMaximized" width="10" height="10" viewBox="0 0 12 12"><rect x="1.5" y="1.5" width="9" height="9" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
          <svg v-else width="10" height="10" viewBox="0 0 12 12">
            <rect x="3.5" y="0.5" width="8" height="8" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/>
            <path d="M3.5 3.5H1.5a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1v-2" fill="none" stroke="currentColor" stroke-width="1.2"/>
          </svg>
        </button>
        <button class="win-btn win-close" title="关闭" @click="closeWin">
          <svg width="10" height="10" viewBox="0 0 12 12"><path d="M1 1l10 10M11 1L1 11" stroke="currentColor" stroke-width="1.4" fill="none"/></svg>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { getRecentDirs, saveRecentDir, openDirDialog } from '../../api/config'
import { WindowMinimise, WindowToggleMaximise, WindowIsMaximised, Quit } from '../../../wailsjs/runtime/runtime'
import { Button, Tag } from '../ui'
import Icon from '../icon/Icon.vue'

const props = defineProps({
  chatVisible: { type: Boolean, default: true },
  filetreeVisible: { type: Boolean, default: true },
  taskVisible: { type: Boolean, default: true },
})

const emit = defineEmits(['open-config', 'open-analyze', 'open-session', 'open-scenario', 'toggle-chat', 'toggle-filetree', 'toggle-tasks', 'open-dir', 'search-file'])

const searchQuery = ref('')
const searchResults = ref([])
const recentDirs = ref([])
const currentTheme = ref('light')
const isMaximized = ref(false)

const showRecentDirs = ref(false)
const showThemeDropdown = ref(false)
const showSearchResults = ref(false)

const recentDirsTriggerRef = ref(null)
const themeTriggerRef = ref(null)

const themes = [
  { id: 'light', label: '浅色' },
  { id: 'dark', label: '深色' },
  { id: 'nord', label: 'Nord' },
]

const recentDirsPopperStyle = computed(() => {
  if (!recentDirsTriggerRef.value) return {}
  const rect = recentDirsTriggerRef.value.getBoundingClientRect()
  return {
    position: 'fixed',
    top: rect.bottom + 4 + 'px',
    left: rect.left + 'px',
    minWidth: rect.width + 'px',
    zIndex: 9999,
  }
})

const themePopperStyle = computed(() => {
  if (!themeTriggerRef.value) return {}
  const rect = themeTriggerRef.value.getBoundingClientRect()
  return {
    position: 'fixed',
    top: rect.bottom + 4 + 'px',
    left: rect.left + 'px',
    minWidth: rect.width + 'px',
    zIndex: 9999,
  }
})

const searchPopperStyle = computed(() => {
  const el = document.querySelector('.search-input-wrap')
  if (!el) return {}
  const rect = el.getBoundingClientRect()
  return {
    position: 'fixed',
    top: rect.bottom + 2 + 'px',
    left: rect.left + 'px',
    width: rect.width + 'px',
    zIndex: 9999,
  }
})

function setTheme(id) {
  currentTheme.value = id
  document.documentElement.setAttribute('data-theme', id)
  localStorage.setItem('chonkpilot-theme', id)
  showThemeDropdown.value = false
}

function minimizeWin() { WindowMinimise() }
async function toggleMaximizeWin() {
  WindowToggleMaximise()
  isMaximized.value = await WindowIsMaximised()
}
function closeWin() { Quit() }

async function syncMaximizeState() {
  try { isMaximized.value = await WindowIsMaximised() } catch (_) {}
}

// Open button: click opens native folder picker and forks new process
async function handleOpenClick() {
  try {
    const res = await openDirDialog()
    if (res?.path) {
      await saveRecentDir(res.path)
      await loadRecentDirs()
    }
  } catch (e) {
    console.error('Failed to open directory dialog:', e)
  }
}

// Dropdown: select a recent directory
async function handleRecentDir(dir) {
  try {
    await saveRecentDir(dir)
    await loadRecentDirs()
  } catch (e) { console.warn('[Toolbar] Failed to save recent dir:', e) }
  emit('open-dir', dir)
  showRecentDirs.value = false
}

function basename(path) {
  const parts = path.replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || path
}

async function onSearchInput() {
  const q = searchQuery.value.trim()
  if (!q) {
    searchResults.value = []
    return
  }
  try {
    const results = await window.go.main.App.SearchProjectFiles(q) || []
    searchResults.value = results.slice(0, 15)
    showSearchResults.value = true
  } catch (_) {
    searchResults.value = []
  }
}

function handleSearchSelect(item) {
  if (item?.path) {
    emit('search-file', item.path)
  }
  showSearchResults.value = false
}

function handleSearchEnter() {
  if (!searchQuery.value.trim()) return
  window.go.main.App.SearchProjectFiles(searchQuery.value.trim()).then((results) => {
    if (results && results.length > 0 && results[0].path) {
      emit('search-file', results[0].path)
    }
  }).catch(() => {})
  showSearchResults.value = false
}

function hideSearchResults() {
  // Use setTimeout to allow click on result item to fire first
  setTimeout(() => {
    showSearchResults.value = false
  }, 200)
}

function toggleRecentDirs() {
  showRecentDirs.value = !showRecentDirs.value
  if (showRecentDirs.value) {
    showThemeDropdown.value = false
  }
}

function toggleThemeDropdown() {
  showThemeDropdown.value = !showThemeDropdown.value
  if (showThemeDropdown.value) {
    showRecentDirs.value = false
  }
}

function handleClickOutside(e) {
  if (showRecentDirs.value && recentDirsTriggerRef.value && !recentDirsTriggerRef.value.contains(e.target)) {
    // Check if click is inside the popper
    const popper = document.querySelector('.b-dropdown-popper')
    if (popper && !popper.contains(e.target)) {
      showRecentDirs.value = false
    }
  }
  if (showThemeDropdown.value && themeTriggerRef.value && !themeTriggerRef.value.contains(e.target)) {
    const popper = document.querySelector('.b-dropdown-popper')
    if (popper && !popper.contains(e.target)) {
      showThemeDropdown.value = false
    }
  }
}

async function loadRecentDirs() {
  try {
    const res = await getRecentDirs()
    recentDirs.value = res.dirs || []
  } catch (_) {
    recentDirs.value = []
  }
}

const saved = localStorage.getItem('chonkpilot-theme')
if (saved) {
  currentTheme.value = saved
  document.documentElement.setAttribute('data-theme', saved)
} else {
  window.go.main.App.GetUserConfig().then((res) => {
    const cfg = res && res.config
    if (cfg && cfg.theme) {
      currentTheme.value = cfg.theme
      document.documentElement.setAttribute('data-theme', cfg.theme)
    }
  }).catch(() => {})
}

onMounted(() => {
  loadRecentDirs()
  syncMaximizeState()
  window.addEventListener('resize', syncMaximizeState)
  document.addEventListener('click', handleClickOutside, true)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside, true)
  window.removeEventListener('resize', syncMaximizeState)
})
</script>

<style scoped>
.toolbar {
  height: var(--toolbar-height);
  display: flex;
  align-items: center;
  padding: 0 8px;
  background: var(--toolbar-bg);
  border-bottom: 1px solid var(--border);
  gap: 4px;
  user-select: none;
  /* frameless drag region */
  --wails-draggable: drag;
}
.toolbar :deep(.b-btn),
.toolbar :deep(.b-input),
.toolbar :deep(.b-tag) {
  --wails-draggable: no-drag;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 180px;
}

.brand {
  font-size: 14px;
  font-weight: 700;
  color: var(--accent);
  letter-spacing: 0.5px;
  white-space: nowrap;
  padding: 0 8px;
}

.separator {
  width: 1px;
  height: 20px;
  background: var(--border);
  margin: 0 4px;
}

.tb-btn {
  background: transparent;
  border: 1px solid transparent;
  color: var(--text-secondary);
  height: 28px;
  padding: 0 6px;
  font-size: 12px;
  gap: 3px;
}

.tb-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.tb-label {
  display: none;
}

@media (min-width: 800px) {
  .tb-label { display: inline; }
}

.dir-display {
  font-size: 11px;
  color: var(--text-muted);
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.toolbar-center {
  flex: 1;
  display: flex;
  justify-content: center;
  overflow: hidden;
}

.search-wrapper {
  width: 50%;
  min-width: 160px;
  max-width: 380px;
}

.search-input-wrap {
  position: relative;
  display: flex;
  align-items: center;
  background: var(--input-bg);
  border-radius: 6px;
  border: 1px solid var(--border);
  padding: 0 8px;
  gap: 4px;
}

.search-input-wrap input {
  flex: 1;
  border: none;
  background: transparent;
  padding: 4px 0;
  font-size: 13px;
  color: var(--text-primary);
  outline: none;
  font-family: inherit;
  line-height: 1.5;
}

.search-input-wrap input::placeholder {
  color: var(--text-muted);
}

.search-prefix {
  flex-shrink: 0;
  color: var(--text-muted);
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 2px;
  overflow: visible;
  width: fit-content;
}

.recent-item {
  font-size: 12px;
}

.open-group {
  display: flex;
  align-items: center;
}

.open-group .open-btn {
  border-radius: 4px 0 0 4px;
  border-right: 1px solid var(--border);
}

.search-result-item {
  display: flex;
  flex-direction: column;
  padding: 6px 8px;
  gap: 2px;
  cursor: pointer;
}
.search-result-item:hover {
  background: var(--bg-hover);
}
.search-filename {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}
.search-path {
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.open-group .dropdown-arrow {
  border-radius: 0 4px 4px 0;
  padding: 0 4px;
  min-width: unset;
}

/* Dropdown popper (Teleported) */
.b-dropdown-popper {
  background: var(--bg-primary, #fff);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0,0,0,0.12);
  padding: 4px 0;
  max-height: 320px;
  overflow-y: auto;
}

.b-dropdown-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  white-space: nowrap;
}

.b-dropdown-item:hover {
  background: var(--bg-hover);
}

.b-dropdown-item.is-disabled {
  color: var(--text-muted);
  cursor: default;
  opacity: 0.6;
}

.b-dropdown-item.is-disabled:hover {
  background: transparent;
}

/* Search popper */
.search-popper {
  background: var(--bg-primary, #fff);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0,0,0,0.12);
  padding: 4px 0;
  max-height: 360px;
  overflow-y: auto;
}

/* ── Window controls (frameless) ── */
.win-controls-sep {
  width: 1px;
  height: 20px;
  background: var(--border);
  margin: 0 4px;
  flex-shrink: 0;
}
.win-controls {
  display: flex;
  align-items: center;
  gap: 0;
  --wails-draggable: no-drag;
}
.win-btn {
  width: 36px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  border-radius: 0;
  transition: background 0.1s, color 0.1s;
}
.win-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.win-close:hover {
  background: #e81123;
  color: #fff;
}
</style>
