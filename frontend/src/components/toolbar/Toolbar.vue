<template>
  <div class="toolbar" @dblclick="toggleMaximizeWin">
    <div class="toolbar-left">
      <!-- Brand -->
      <span class="brand">Chonk Pilot</span>
      <span class="separator" />

      <!-- Open Directory: click to open, dropdown for recent dirs -->
      <div class="open-group">
        <el-button size="small" class="tb-btn open-btn" title="打开目录" @click="handleOpenClick">
          <el-icon><FolderOpened /></el-icon>
          <span class="tb-label">打开</span>
        </el-button>
        <el-dropdown trigger="click" @command="handleRecentDir">
          <el-button size="small" class="tb-btn dropdown-arrow" title="最近目录">
            <el-icon><ArrowDown /></el-icon>
          </el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="dir in recentDirs"
                :key="dir"
                :command="dir"
              >
                <el-icon><Folder /></el-icon>
                <span class="recent-item">{{ dir }}</span>
              </el-dropdown-item>
              <el-dropdown-item v-if="recentDirs.length === 0" disabled>
                暂无最近目录
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>

      <!-- Settings -->
      <el-button size="small" class="tb-btn" title="设置" @click="$emit('open-config')">
        <el-icon><Setting /></el-icon>
        <span class="tb-label">设置</span>
      </el-button>

      <!-- Scenarios -->
      <el-button size="small" class="tb-btn" title="场景" @click="$emit('open-scenario')">
        <el-icon><Collection /></el-icon>
        <span class="tb-label">场景</span>
      </el-button>
    </div>

    <div class="toolbar-center">
      <!-- Global search with autocomplete -->
      <div class="search-wrapper">
        <el-autocomplete
          v-model="searchQuery"
          :fetch-suggestions="searchSuggestions"
          placeholder="搜索文件..."
          size="small"
          clearable
          popper-class="search-popper"
          :trigger-on-focus="false"
          :debounce="300"
          value-key="path"
          @select="handleSearchSelect"
          @keyup.enter="handleSearchEnter"
        >
          <template #prefix>
            <el-icon><Search /></el-icon>
          </template>
          <template #default="{ item }">
            <div class="search-result-item">
              <el-tag size="small" :type="item.matchType === 'filename' ? '' : (item.matchType === 'symbol' ? 'warning' : 'info')" class="search-tag">
                {{ item.matchType === 'filename' ? '文件名' : (item.matchType === 'symbol' ? '符号' : '路径') }}
              </el-tag>
              <span class="search-filename">{{ basename(item.path) }}</span>
              <span class="search-path">{{ item.path }}</span>
            </div>
          </template>
        </el-autocomplete>
      </div>
    </div>
    <div class="toolbar-right">
      <!-- Theme -->
      <el-dropdown trigger="click" @command="setTheme">
        <el-button size="small" class="tb-btn" title="主题">
          <el-icon><MagicStick /></el-icon>
          <span class="tb-label">主题</span>
        </el-button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item
              v-for="t in themes"
              :key="t.id"
              :command="t.id"
            >
              <el-icon v-if="currentTheme === t.id"><Check /></el-icon>
              <span v-else style="display:inline-block;width:14px" />
              {{ t.label }}
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>

      <!-- Sessions -->
      <el-button size="small" class="tb-btn" title="会话" @click="$emit('open-session')">
        <el-icon><MessageBox /></el-icon>
        <span class="tb-label">会话</span>
      </el-button>

      <!-- Tasks toggle -->
      <el-button
        size="small"
        class="tb-btn"
        :title="taskVisible ? '隐藏任务' : '显示任务'"
        @click="$emit('toggle-tasks')"
      >
        <el-icon :color="taskVisible ? 'var(--accent)' : ''"><List /></el-icon>
      </el-button>

      <!-- Filetree toggle -->
      <el-button
        size="small"
        class="tb-btn"
        :title="filetreeVisible ? '隐藏文件树' : '显示文件树'"
        @click="$emit('toggle-filetree')"
      >
        <el-icon :color="filetreeVisible ? 'var(--accent)' : ''"><Folder /></el-icon>
      </el-button>

      <!-- Chat toggle -->
      <el-button
        size="small"
        class="tb-btn"
        :title="chatVisible ? '隐藏聊天' : '显示聊天'"
        @click="$emit('toggle-chat')"
      >
        <el-icon :color="chatVisible ? 'var(--accent)' : ''"><ChatDotSquare /></el-icon>
      </el-button>

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
import { ref, onMounted } from 'vue'
import { getRecentDirs, saveRecentDir, openDirDialog } from '../../api/config'
import { WindowMinimise, WindowToggleMaximise, WindowIsMaximised, Quit } from '../../../wailsjs/runtime/runtime'

const props = defineProps({
  chatVisible: { type: Boolean, default: true },
  filetreeVisible: { type: Boolean, default: true },
  taskVisible: { type: Boolean, default: true },
})

const emit = defineEmits(['open-config', 'open-analyze', 'open-session', 'open-scenario', 'toggle-chat', 'toggle-filetree', 'toggle-tasks', 'open-dir', 'search-file'])

const searchQuery = ref('')
const recentDirs = ref([])
const currentTheme = ref('light')
const isMaximized = ref(false)

const themes = [
  { id: 'light', label: '浅色' },
  { id: 'dark', label: '深色' },
  { id: 'nord', label: 'Nord' },
]

function setTheme(id) {
  currentTheme.value = id
  document.documentElement.setAttribute('data-theme', id)
  localStorage.setItem('chonkpilot-theme', id)
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
}

function basename(path) {
  const parts = path.replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || path
}

async function searchSuggestions(query, cb) {
  if (!query || !query.trim()) {
    cb([])
    return
  }
  try {
    const results = await window.go.main.App.SearchProjectFiles(query.trim()) || []
    cb(results.slice(0, 15))
  } catch (_) {
    cb([])
  }
}

function handleSearchSelect(item) {
  if (item?.path) {
    emit('search-file', item.path)
  }
}

function handleSearchEnter() {
  if (!searchQuery.value.trim()) return
  // Fetch results first, then open the first one
  window.go.main.App.SearchProjectFiles(searchQuery.value.trim()).then((results) => {
    if (results && results.length > 0 && results[0].path) {
      emit('search-file', results[0].path)
    }
  }).catch(() => {})
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
  // Try to load theme from user config (set via Settings dialog)
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
.toolbar :deep(.el-button),
.toolbar :deep(.el-input),
.toolbar :deep(.el-dropdown) {
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
.search-wrapper :deep(.el-input__wrapper) {
  background: var(--input-bg);
  border-radius: 6px;
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
  padding: 4px 0;
  gap: 2px;
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
.search-tag {
  position: absolute;
  right: 8px;
  top: 50%;
  transform: translateY(-50%);
}

.open-group .dropdown-arrow {
  border-radius: 0 4px 4px 0;
  padding: 0 4px;
  min-width: unset;
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
