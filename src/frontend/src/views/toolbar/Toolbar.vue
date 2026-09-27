<template>
  <div class="toolbar" v-mq:[EventNames.windowToggle].dblclick>
    <div class="toolbar-left">
      <!-- Brand -->
      <span class="brand">Chonk Pilot</span>
      <span class="separator" />

      <!-- Open Directory: click to open, dropdown for recent dirs -->
      <div class="open-group">
        <Button class="tb-btn open-btn" :title="$t('toolbar.open_dir')" v-mq:[EventNames.workdirOpen].click>
          <Icon name="folder-opened" />
          <span class="tb-label">{{ $t('toolbar.open') }}</span>
        </Button>
        <div class="dropdown-trigger" ref="recentDirsTriggerRef">
          <Button class="tb-btn dropdown-arrow" :title="$t('toolbar.recent_dirs')" v-mq:[EventNames.recentDirsToggle].click>
            <Icon name="arrow-down" />
          </Button>
          <Teleport to="body">
            <div v-if="showRecentDirs" class="b-dropdown-popper" :style="recentDirsPopperStyle" v-mq:[EventNames.containerClick].click.stop>
              <div
                v-for="dir in recentDirs"
                :key="dir"
                class="b-dropdown-item"
                v-mq:[EventNames.recentDirSelect].click="{ dir: dir }"
              >
                <Icon name="folder" />
                <span class="recent-item">{{ dir }}</span>
              </div>
              <div v-if="recentDirs.length === 0" class="b-dropdown-item is-disabled">
                {{ $t('toolbar.no_recent_dirs') }}
              </div>
            </div>
          </Teleport>
        </div>
      </div>

      <!-- Settings：下拉菜单 → preview 区打开对应配置页（12-数据层 / CFG-002） -->
      <div class="dropdown-trigger" ref="settingsTriggerRef">
        <Button class="tb-btn" :title="$t('toolbar.settings')" v-mq:[EventNames.configMenuToggle].click>
          <Icon name="setting" />
          <span class="tb-label">{{ $t('toolbar.settings') }}</span>
        </Button>
        <Teleport to="body">
          <div v-if="showSettingsMenu" class="b-dropdown-popper" :style="settingsPopperStyle" v-mq:[EventNames.containerClick].click.stop>
            <div
              v-for="item in settingsItems"
              :key="item.kind"
              class="b-dropdown-item"
              v-mq:[EventNames.configMenuSelect].click="{ kind: item.kind }"
            >
              <Icon :name="item.icon" />
              <span>{{ item.label }}</span>
            </div>
          </div>
        </Teleport>
      </div>

      <!-- Scenarios -->
      <Button class="tb-btn" :title="$t('toolbar.scenarios')" v-mq:[EventNames.scenarioOpen].click>
        <Icon name="collection" />
        <span class="tb-label">{{ $t('toolbar.scenarios') }}</span>
      </Button>
    </div>

    <div class="toolbar-center">
      <!-- Global search with custom dropdown -->
      <div class="search-wrapper">
        <div class="search-input-wrap">
          <Icon name="search" class="search-prefix" />
          <Input
            v-model="searchQuery"
            :placeholder="$t('toolbar.search_placeholder')"
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
                <Tag :type="matchTagType(item.matchType)" size="mini">
                  {{ matchTagLabel(item.matchType) }}
                </Tag>
                <span class="search-filename">{{ basename(item.path) }}</span>
                <span class="search-path">{{ item.path }}</span>
                <span v-if="item.snippet" class="search-snippet">{{ snippetPreview(item.snippet) }}</span>
              </div>
            </div>
          </Teleport>
        </div>
      </div>
    </div>
    <div class="toolbar-right">
      <!-- Chrome 未检测告警（真实信号：gui.toolchain.detect ∪ usr chromePath，见 utils/chromeStatus.js）：
           hover 显示 chromeTip；点击跳「路径配置」以设置 Chrome 路径。
           注：类名**不用** `tb-btn` —— test_toolbar 按 `.toolbar-right .tb-btn` 固定下标定位（0=theme…），
           新按钮若带该类会错位；此处用自有类 `chrome-warn`。 -->
      <Button
        v-if="chromeMissing"
        class="chrome-warn"
        :title="$t('config.chromeTip')"
        v-mq:[EventNames.previewTabOpen].click="{ kind: 'settings-paths' }"
      >
        <Icon name="warning-filled" :size="15" color="var(--warning, #e8a317)" />
      </Button>

      <!-- Theme -->
      <div class="dropdown-trigger" ref="themeTriggerRef">
        <Button class="tb-btn" :title="$t('toolbar.theme')" v-mq:[EventNames.themeToggle].click>
          <Icon name="magic-stick" />
          <span class="tb-label">{{ $t('toolbar.theme') }}</span>
        </Button>
        <Teleport to="body">
          <div v-if="showThemeDropdown" class="b-dropdown-popper" :style="themePopperStyle" v-mq:[EventNames.containerClick].click.stop>
            <div
              v-for="t in themes"
              :key="t.id"
              class="b-dropdown-item"
              v-mq:[EventNames.themeSelect].click="{ themeId: t.id }"
            >
              <Icon v-if="currentTheme === t.id" name="check" />
              <span v-else style="display:inline-block;width:16px" />
              {{ t.label }}
            </div>
          </div>
        </Teleport>
      </div>

      <!-- Language -->
      <div class="dropdown-trigger" ref="langTriggerRef">
        <Button class="tb-btn" :title="$t('toolbar.language')" v-mq:[EventNames.langToggle].click>
          <span class="tb-label">{{ currentLangLabel }}</span>
        </Button>
        <Teleport to="body">
          <div v-if="showLangDropdown" class="b-dropdown-popper" :style="langPopperStyle" v-mq:[EventNames.containerClick].click.stop>
            <div
              v-for="lang in languages"
              :key="lang.code"
              class="b-dropdown-item"
              v-mq:[EventNames.langSelect].click="{ lang: lang.code }"
            >
              <Icon v-if="currentLocale === lang.code" name="check" />
              <span v-else style="display:inline-block;width:16px" />
              {{ lang.label }}
            </div>
          </div>
        </Teleport>
      </div>

      <!-- Sessions -->
      <Button class="tb-btn" :title="$t('toolbar.sessions')" v-mq:[EventNames.sessionsOpen].click>
        <Icon name="message-box" />
        <span class="tb-label">{{ $t('toolbar.sessions') }}</span>
      </Button>

      <!-- Tasks toggle（图标 + 文字；元素顺序 / 类名不变 → test_toolbar 固定下标 3 仍成立） -->
      <Button
        class="tb-btn"
        :title="taskVisible ? $t('toolbar.hide_tasks') : $t('toolbar.show_tasks')"
        v-mq:[EventNames.tasksToggle].click
      >
        <Icon name="list" :color="taskVisible ? 'var(--accent)' : ''" />
        <span class="tb-label">{{ $t('toolbar.tasks') }}</span>
      </Button>

      <!-- Filetree toggle（图标 + 文字；下标 4） -->
      <Button
        class="tb-btn"
        :title="filetreeVisible ? $t('toolbar.hide_filetree') : $t('toolbar.show_filetree')"
        v-mq:[EventNames.filetreeToggle].click
      >
        <Icon name="folder" :color="filetreeVisible ? 'var(--accent)' : ''" />
        <span class="tb-label">{{ $t('toolbar.filetree') }}</span>
      </Button>

      <!-- Chat toggle（图标 + 文字；下标 5） -->
      <Button
        class="tb-btn"
        :title="chatVisible ? $t('toolbar.hide_chat') : $t('toolbar.show_chat')"
        v-mq:[EventNames.chatToggle].click
      >
        <Icon name="chat-dot-square" :color="chatVisible ? 'var(--accent)' : ''" />
        <span class="tb-label">{{ $t('toolbar.chat') }}</span>
      </Button>

      <!-- 登出（认证形态 desktop/browser 才有；61 §4.6 认证域；阶段 2b-2）。
           注：类名**不用** `tb-btn` —— test_toolbar 按 `.toolbar-right .tb-btn` 固定下标定位
           （0=theme…5=chat），新按钮带该类会错位；此处用自有类 `auth-btn`（同 chrome-warn）。 -->
      <Button
        v-if="needsAuth"
        class="auth-btn"
        :title="$t('toolbar.sign_out')"
        @click="onLogout"
      >
        <Icon name="user" />
      </Button>

      <!-- Window controls (frameless) -->
      <span class="win-controls-sep" />
      <div class="win-controls">
        <Button text size="mini" class="win-btn win-minimize" :title="$t('toolbar.minimize')" v-mq:[EventNames.guiWindowStatus].click="winCmdMinimize">
          <svg width="10" height="10" viewBox="0 0 12 12"><rect x="1" y="5.5" width="10" height="1" fill="currentColor"/></svg>
        </Button>
        <Button text size="mini" class="win-btn win-maximize" :title="isMaximized ? $t('toolbar.restore') : $t('toolbar.maximize')" v-mq:[EventNames.guiWindowStatus].click="winCmdMaximize">
          <svg v-if="!isMaximized" width="10" height="10" viewBox="0 0 12 12"><rect x="1.5" y="1.5" width="9" height="9" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
          <svg v-else width="10" height="10" viewBox="0 0 12 12">
            <rect x="3.5" y="0.5" width="8" height="8" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/>
            <path d="M3.5 3.5H1.5a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1v-2" fill="none" stroke="currentColor" stroke-width="1.2"/>
          </svg>
        </Button>
        <Button text size="mini" class="win-btn win-close" :title="$t('toolbar.close')" v-mq:[EventNames.guiWindowStatus].click="winCmdClose">
          <svg width="10" height="10" viewBox="0 0 12 12"><path d="M1 1l10 10M11 1L1 11" stroke="currentColor" stroke-width="1.4" fill="none"/></svg>
        </Button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { getRecentDirs, saveRecentDir, openDir, getUserConfig, detectToolchains } from '../../api/config'
import { Button, Tag, Input, message } from '../../components/ui'
import { isChromeMissing } from '../../utils/chromeStatus'
import { configMenuItems } from '../../utils/configMenu'
import { onDataRefresh } from '../../utils/dataClient'
import { useDirPicker } from '../../composables/useDirPicker'
import { isBrowserForm } from '../../utils/runtimeForm'
import Icon from '../../components/icon/Icon.vue'
import { setLocale, SUPPORTED_LANGUAGES } from '../../plugins/i18n'
import { saveUIState, loadInitData } from '../../api/file'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { requireAuth } from '../../utils/instanceState'
import { useAuth } from '../../composables/useAuth'
import { resetClaim } from '../../composables/useInstanceClaim'
import { useUserConfigSync } from '../../composables/useUserConfigSync'

const { t, locale } = useI18n()

// 认证形态（gui / browser）→ 显示登出入口（61 §4.6 认证域；阶段 2b-2）。
// desktop（requireAuth=false）免鉴权 → 不显示。
const needsAuth = requireAuth()
const { signOut } = useAuth()

/**
 * 登出：`login-out {}`（令牌由服务端删行 + 入口清 cookie / 删文件；前端不接触令牌）
 * → 复位实例认领（旧 instance 上下文不得继续携带）→ App 据 `signedIn` 回登录视图。
 */
async function onLogout() {
  await signOut()
  resetClaim()
}

// 目录选择统一入口：GUI/native → gui.dir.open-dialog；browser → GET /dirs 只读选择器
const { pickDir } = useDirPicker()

const languages = SUPPORTED_LANGUAGES

const currentLocale = computed(() => locale.value)
const currentLang = computed(() => languages.find(l => l.code === currentLocale.value) || languages[0])
const currentLangFlag = computed(() => currentLang.value.flag)
const currentLangLabel = computed(() => currentLang.value.label)

const props = defineProps({
  chatVisible: { type: Boolean, default: true },
  filetreeVisible: { type: Boolean, default: true },
  taskVisible: { type: Boolean, default: true },
})


const searchQuery = ref('')
const searchResults = ref([])
const recentDirs = ref([])
// 主题勾选态 = usr 配置跨窗口同步的共享 ref（useUserConfigSync；订阅 data-user-config-changed，
// 61 §3.1 · 24 §6.5）：其它窗口切主题 → 本窗口当前值同步更新（**不回写 DB**，避免自激）。
const { theme: currentTheme } = useUserConfigSync()
const isMaximized = ref(false)
// Chrome 未检测告警（真实信号：gui.toolchain.detect 探测 + usr chromePath 覆盖）
const chromeMissing = ref(false)

// checkChrome：读**真实后端信号**判定是否告警 —— 工具链探测（chrome 行）+ usr 配置 chromePath。
async function checkChrome() {
  try {
    const tools = await detectToolchains()
    const chrome = (tools || []).find((x) => x && x.id === 'chrome') || {}
    const res = await getUserConfig()
    const cfg = (res && res.config) || res || {}
    chromeMissing.value = isChromeMissing({
      detectedPath: chrome.path,
      userChromePath: cfg.chromePath,
    })
  } catch (_) {
    // 读信号失败 → 不告警（避免误报）
    chromeMissing.value = false
  }
}

// 窗口控制（gui.window.status）：按钮 v-mq 以 payload {command} 发送；null/缺省 = 仅查询状态
const winCmdMinimize = { command: 'minimize' }
const winCmdMaximize = { command: 'maximize' }
const winCmdClose = { command: 'close' }

// 全局搜索防抖与过期结果丢弃：击键后 250ms 无新输入才真正请求；
// searchSeq 递增使在途请求作废，只采纳最后一次请求的结果。
let searchDebounceTimer = null
let searchSeq = 0

const showRecentDirs = ref(false)
const showThemeDropdown = ref(false)
const showSearchResults = ref(false)
const showLangDropdown = ref(false)
const showSettingsMenu = ref(false)

const recentDirsTriggerRef = ref(null)
const themeTriggerRef = ref(null)
const langTriggerRef = ref(null)
const settingsTriggerRef = ref(null)

// 设置下拉菜单项 → preview tab kind（12-数据层）；清单与状态栏配置入口共用（utils/configMenu.js）
const settingsItems = computed(() => configMenuItems(t))

const themes = computed(() => [
  { id: 'light', label: t('toolbar.theme_light') },
  { id: 'dark', label: t('toolbar.theme_dark') },
  { id: 'nord', label: t('toolbar.theme_nord') },
])

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

const langPopperStyle = computed(() => {
  if (!langTriggerRef.value) return {}
  const rect = langTriggerRef.value.getBoundingClientRect()
  return {
    position: 'fixed',
    top: rect.bottom + 4 + 'px',
    left: rect.left + 'px',
    minWidth: rect.width + 'px',
    zIndex: 9999,
  }
})

const settingsPopperStyle = computed(() => {
  if (!settingsTriggerRef.value) return {}
  const rect = settingsTriggerRef.value.getBoundingClientRect()
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
  saveUIState({ theme: id }).catch(() => {})
  showThemeDropdown.value = false
}

function switchLang(code) {
  setLocale(code)
  showLangDropdown.value = false
}

async function syncMaximizeState() {
  // 统一窗口状态查询：gui.window.status {command:null} → result {maximized, minimized}
  try {
    const env = await mq.emit(EventNames.guiWindowStatus, { command: null })
    const r = env && env.backend && env.backend.result
    if (r && typeof r.maximized === 'boolean') isMaximized.value = r.maximized
  } catch (_) {}
}

// Open button：GUI/native 走系统目录选择框（选后开新窗口，行为不变）；
// browser 走服务端等价面 GET /dirs 只读选择器 —— work_dir 由服务端绑定，无法切换/新开窗口
// （明确提示，不假成功；见 19 §8.9）。
async function handleOpenClick() {
  try {
    const path = await pickDir()
    if (!path) return
    if (isBrowserForm()) {
      message.info(t('toolbar.open_dir_browser_unsupported'))
      return
    }
    await saveRecentDir(path)
    await loadRecentDirs()
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
  openDir(dir)
  showRecentDirs.value = false
}

function basename(path) {
  const parts = path.replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || path
}

// 搜索结果命中类型 → Tag 颜色 / i18n 文案（含全文检索的 content 命中）
function matchTagType(matchType) {
  switch (matchType) {
    case 'filename': return 'info'
    case 'symbol': return 'warning'
    case 'content': return 'danger'
    default: return 'success' // path
  }
}

function matchTagLabel(matchType) {
  switch (matchType) {
    case 'filename': return t('toolbar.match_filename')
    case 'symbol': return t('toolbar.match_symbol')
    case 'content': return t('toolbar.match_content')
    default: return t('toolbar.match_path') // path
  }
}

// 全文检索命中的摘要行：截断 ~60 字符
function snippetPreview(snippet) {
  const s = String(snippet || '').trim()
  if (!s) return ''
  return s.length > 60 ? s.slice(0, 60) + '…' : s
}

// 项目内检索（gui.search，61-消息一览 §1：{query} → result {results}；索引未接入 → 占位空）
async function searchFiles(q) {
  try {
    const env = await mq.emit('gui.search', { query: q })
    const r = env && env.backend && env.backend.result
    return (r && Array.isArray(r.results)) ? r.results : []
  } catch (_) {
    return []
  }
}

function onSearchInput() {
  const q = searchQuery.value.trim()
  if (!q) {
    searchSeq++ // 作废在途请求
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer)
    searchResults.value = []
    return
  }
  if (searchDebounceTimer) clearTimeout(searchDebounceTimer)
  const seq = ++searchSeq
  searchDebounceTimer = setTimeout(async () => {
    try {
      const results = await searchFiles(q)
      if (seq !== searchSeq) return // 过期结果丢弃
      searchResults.value = results.slice(0, 15)
      showSearchResults.value = true
    } catch (_) {
      if (seq === searchSeq) searchResults.value = []
    }
  }, 250)
}

function handleSearchSelect(item) {
  if (item?.path) {
    // 命中项路径 = workdir 相对（gui.search 已归一，与 file-open 契约一致）：
    // 预览区打开（临时页签）+ 文件树定位（file-search 仅做树内过滤/展开）
    mq.emit(EventNames.fileOpen, { path: item.path, temporary: true })
    mq.emit(EventNames.fileSearch, item.path)
  }
  showSearchResults.value = false
}

function handleSearchEnter() {
  if (!searchQuery.value.trim()) return
  searchFiles(searchQuery.value.trim()).then((results) => {
    if (results && results.length > 0 && results[0].path) {
      const p = results[0].path
      mq.emit(EventNames.fileOpen, { path: p, temporary: true })
      mq.emit(EventNames.fileSearch, p)
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
    showLangDropdown.value = false
    showSettingsMenu.value = false
  }
}

function toggleLangDropdown() {
  showLangDropdown.value = !showLangDropdown.value
  if (showLangDropdown.value) {
    showRecentDirs.value = false
    showThemeDropdown.value = false
    showSettingsMenu.value = false
  }
}

function toggleThemeDropdown() {
  showThemeDropdown.value = !showThemeDropdown.value
  if (showThemeDropdown.value) {
    showRecentDirs.value = false
    showLangDropdown.value = false
    showSettingsMenu.value = false
  }
}

// 设置下拉菜单：开合 + 选项 → preview 区开页
function toggleSettingsMenu() {
  showSettingsMenu.value = !showSettingsMenu.value
  if (showSettingsMenu.value) {
    showRecentDirs.value = false
    showThemeDropdown.value = false
    showLangDropdown.value = false
  }
}

function handleSettingsSelect({ kind }) {
  showSettingsMenu.value = false
  if (kind) mq.emit(EventNames.previewTabOpen, { kind })
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
  if (showLangDropdown.value && langTriggerRef.value && !langTriggerRef.value.contains(e.target)) {
    const popper = document.querySelector('.b-dropdown-popper')
    if (popper && !popper.contains(e.target)) {
      showLangDropdown.value = false
    }
  }
  if (showSettingsMenu.value && settingsTriggerRef.value && !settingsTriggerRef.value.contains(e.target)) {
    const popper = document.querySelector('.b-dropdown-popper')
    if (popper && !popper.contains(e.target)) {
      showSettingsMenu.value = false
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

// mq 事件注册 — executor 可触发的操作
const _mqUnsubs = []

onMounted(() => {
  loadRecentDirs()
  syncMaximizeState()
  checkChrome()
  // usr 配置变更（如路径配置页保存 chromePath）→ 重新判定告警（data-user-config-refresh 广播）
  _mqUnsubs.push(onDataRefresh('user-config', checkChrome))
  window.addEventListener('resize', syncMaximizeState)
  document.addEventListener('click', handleClickOutside, true)

  // 恢复主题：localStorage（最近选择）→ DB ui（工具栏 setTheme 落库的备份，
  // WebView2 缓存被清时兜底）→ 用户配置 cfg.theme（GeneralConfigForm 设置）。
  const saved = localStorage.getItem('chonkpilot-theme')
  if (saved) {
    currentTheme.value = saved
    document.documentElement.setAttribute('data-theme', saved)
  } else {
    let applied = false
    loadInitData().then((r) => {
      const th = r && r.ui && r.ui.theme
      if (th && !applied) {
        applied = true
        currentTheme.value = th
        document.documentElement.setAttribute('data-theme', th)
        try { localStorage.setItem('chonkpilot-theme', th) } catch (_) {}
      }
    }).catch(() => {})
    // data-user-config-load 消息面（20-gui），替代 GetUserConfig RPC
    getUserConfig().then((res) => {
      const cfg = res && res.config
      if (cfg && cfg.theme && !applied) {
        applied = true
        currentTheme.value = cfg.theme
        document.documentElement.setAttribute('data-theme', cfg.theme)
      }
    }).catch(() => {})
  }

  // 注册 mq 事件监听，供 llm_run 子会话的工具触发
  _mqUnsubs.push(mq.on(EventNames.workdirOpen, handleOpenClick))
  _mqUnsubs.push(mq.on(EventNames.recentDirsToggle, toggleRecentDirs))
  _mqUnsubs.push(mq.on(EventNames.recentDirSelect, (data) => handleRecentDir(data.dir)))
  _mqUnsubs.push(mq.on(EventNames.themeToggle, toggleThemeDropdown))
  _mqUnsubs.push(mq.on(EventNames.themeSelect, (data) => setTheme(data.themeId)))
  _mqUnsubs.push(mq.on(EventNames.langToggle, toggleLangDropdown))
  _mqUnsubs.push(mq.on(EventNames.langSelect, (data) => switchLang(data.lang)))
  _mqUnsubs.push(mq.on(EventNames.configMenuToggle, toggleSettingsMenu))
  _mqUnsubs.push(mq.on(EventNames.configMenuSelect, handleSettingsSelect))
  // 窗口控制按钮：仅发事件（/publish → 后端 EventBus 订阅者执行）；
  // 最大化状态由后端广播 window-maximized-changed 同步。
  _mqUnsubs.push(mq.on(EventNames.windowMaximizedChanged, (data) => {
    if (data && typeof data.maximized === 'boolean') isMaximized.value = data.maximized
  }))

})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside, true)
  window.removeEventListener('resize', syncMaximizeState)
  // 取消所有 mq 监听
  _mqUnsubs.forEach(fn => fn())
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
  /* frameless drag region (WebView2/Chromium 原生支持) */
  -webkit-app-region: drag;
}
.toolbar :deep(.b-btn),
.toolbar :deep(.b-input),
.toolbar :deep(.b-tag) {
  -webkit-app-region: no-drag;
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

/* Chrome 未检测告警按钮（不用 .tb-btn：避免 test_toolbar 按下标定位错位） */
.chrome-warn {
  background: transparent;
  border: 1px solid transparent;
  height: 28px;
  padding: 0 6px;
  display: inline-flex;
  align-items: center;
  cursor: pointer;
}
.chrome-warn:hover {
  background: var(--bg-hover);
}

/* 登出按钮（认证形态）：类名不用 tb-btn（同 chrome-warn 理由，见模板注释）。 */
.auth-btn {
  background: transparent;
  border: 1px solid transparent;
  height: 28px;
  padding: 0 6px;
  display: inline-flex;
  align-items: center;
  cursor: pointer;
}
.auth-btn:hover {
  background: var(--bg-hover);
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

.search-input-wrap :deep(.b-input) {
  flex: 1;
  border: none;
  background: transparent;
}

.search-input-wrap :deep(.b-input) input {
  font-size: 13px;
  color: var(--text-primary);
  outline: none;
  font-family: inherit;
  line-height: 1.5;
}

.search-input-wrap :deep(.b-input) input::placeholder {
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
.search-snippet {
  font-size: 11px;
  color: var(--text-secondary);
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
  -webkit-app-region: no-drag;
}
.search-input-wrap {
  -webkit-app-region: no-drag;
}
:deep(.win-btn) {
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
:deep(.win-btn) .b-btn {
  padding: 0 !important;
  min-height: unset !important;
  border-radius: 0 !important;
}
/* 特异性需高于 Button 的 .b-btn.is-text:hover，否则 hover 背景被浅蓝覆盖 */
:deep(.b-btn.win-btn):hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
:deep(.b-btn.win-btn.win-close):hover {
  background: #e81123;
  color: #fff;
}
</style>