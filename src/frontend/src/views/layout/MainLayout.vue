<template>
  <div class="main-layout">
    <SplitPanel direction="vertical" :gap="0" :panes="outerConfig">
      <template #pane-0>
        <Toolbar
          :chat-visible="chatOpen"
          :filetree-visible="filetreeOpen && !narrow"
          :task-visible="taskOpen"
        />
      </template>
      <template #pane-1>
        <SplitPanel direction="horizontal" :gap="4" :panes="bodyConfig" @size-changed="saveLayoutDebounced">
          <template #pane-0>
            <SplitPanel direction="vertical" :gap="4" :panes="topBottomConfig" @size-changed="saveLayoutDebounced">
              <template #pane-0>
                <SplitPanel direction="horizontal" :gap="4" :panes="filetreeConfig" @size-changed="saveLayoutDebounced">
                  <template #pane-0><ExplorerPane ref="fileTreeEl" /></template>
                  <template #pane-1><CodeView /></template>
                </SplitPanel>
              </template>
              <template #pane-1>
                <SplitPanel direction="horizontal" :gap="4" :panes="taskConfig" @size-changed="saveLayoutDebounced">
                  <template #pane-0><SessionTree ref="sessionTreeEl" /></template>
                  <template #pane-1><SessionChat /></template>
                </SplitPanel>
              </template>
            </SplitPanel>
          </template>
          <template #pane-1>
            <ChatPanel ref="chatPanelEl" />
          </template>
        </SplitPanel>
      </template>
      <template #pane-2><StatusBar /></template>
    </SplitPanel>

    <AskUserDialog />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, defineAsyncComponent, h, nextTick } from 'vue'
import { dialog } from '../../components/dialog'
import { useI18n } from 'vue-i18n'
import { SplitPanel } from '../../components/split'
import { loadInitDataPrefetched, saveLayoutState } from '../../api/file'
import { computeContentHeight, readRootPx, FALLBACK_TOOLBAR_HEIGHT, FALLBACK_STATUSBAR_HEIGHT } from '../../utils/cssToken'
import Toolbar from '../toolbar/Toolbar.vue'
import ExplorerPane from '../filetree/ExplorerPane.vue'
import ChatPanel from '../chat/ChatPanel.vue'
import SessionTree from '../tasks/SessionTree.vue'
import SessionChat from '../tasks/SessionChat.vue'
import AskUserDialog from '../chat/AskUserDialog.vue'
import StatusBar from '../statusbar/StatusBar.vue'
import { setLocale } from '../../plugins/i18n'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

// 懒加载：仅在打开场景弹窗时加载
const ScenarioDialogContent = defineAsyncComponent(() => import('../scenario/ScenarioDialogContent.vue'))

const { t } = useI18n()

const CodeView = defineAsyncComponent(() => import('../codeview/CodeView.vue'))

const chatOpen = ref(true)
const filetreeOpen = ref(true)
// 任务区（SessionTree + SessionChat）默认**收起**（首屏减负）：无已保存值时收起；
// 存在已保存值（applyLayout 读 layout.taskOpen）仍以保存值为准，用户拖拽/开关后的持久化与恢复不变。
const taskOpen = ref(false)
const analyzeDialog = ref(null)

// ── 布局分隔条位置（持久化 + 恢复校验）──────────────────────
// 保存：拖拽结束（SplitPanel size-changed）/ 窗口 resize / 退出（beforeunload
// sendBeacon）。恢复：LoadInitData 返回 layout → applyLayout 做「与窗口大小的
// 关系」合理性校验（clamp 到 pane min/max 与窗口可用空间，防脏值/小窗口溢出）。
const fileTreeEl = ref(null)
const chatPanelEl = ref(null)
const sessionTreeEl = ref(null)

// ── 尺寸基线与保护下限 ─────────────────────────────────────────
// toolbar / statusbar 高取自 CSS token（variables.css），JS clamp 与样式同源；
// preview / session-chat 为 flex 面板 → 给主轴最小尺寸保护，防被定尺邻居挤成 0。
const TOOLBAR_H = readRootPx('--toolbar-height', FALLBACK_TOOLBAR_HEIGHT)
const STATUSBAR_H = readRootPx('--statusbar-height', FALLBACK_STATUSBAR_HEIGHT)
const PREVIEW_MIN = 280 // preview（CodeView）最小可视宽
const SESSION_CHAT_MIN = 240 // session-chat 最小可视宽
const LAYOUT_GAPS = 2 // content|chat + filetree|preview 两条 resizer（各 1px 发丝线，--split-hairline）
const NARROW_MAX = 900 // 窄屏断点：以下三列无法同时容纳 → 自动收起文件树

// 用户布局偏好（持久化原值，不因窗口缩放被改写）
const filetreeWidth = ref(320)
const filetreeHeight = ref(0) // 记录 top 面板（filetree 所在）高度，仅用于保存/恢复
const chatWidth = ref(520)
const sessiontreeWidth = ref(400)
const taskHeight = ref(500) // topBottom 下方面板（SessionTree 区）高度

// 视口宽（响应式）：窗口缩放 → 有效尺寸随视口重算 clamp；窄屏收起文件树
const viewportW = ref(window.innerWidth)
const narrow = computed(() => viewportW.value <= NARROW_MAX)

// 有效尺寸 = 偏好值按当前视口 clamp（不改写偏好，窗口放大后自动复原）
const effChatWidth = computed(() =>
  clamp(chatWidth.value, 280, Math.min(800, Math.round(viewportW.value * 0.45))))
const effSessiontreeWidth = computed(() => {
  const leftW = viewportW.value - effChatWidth.value - 4 // 左列（content 区）宽
  const maxByFit = leftW - SESSION_CHAT_MIN - 4 // 给 session-chat 留最小宽
  return clamp(sessiontreeWidth.value, 200,
    Math.max(200, Math.min(600, Math.round(viewportW.value * 0.4), maxByFit)))
})
const effFiletreeWidth = computed(() => {
  const maxByFit = viewportW.value - effChatWidth.value - PREVIEW_MIN - LAYOUT_GAPS
  return clamp(filetreeWidth.value, 180, Math.max(180, Math.min(600, maxByFit)))
})

const outerConfig = computed(() => [
  { id: 'toolbar', size: TOOLBAR_H, resizable: false },
  { id: 'content', flex: true },
  { id: 'statusbar', size: STATUSBAR_H, resizable: false },
])

const bodyConfig = computed(() => [
  { id: 'content', flex: true, min: PREVIEW_MIN },
  { id: 'chat', size: effChatWidth.value, min: 280, max: 800, visible: chatOpen.value },
])

const topBottomConfig = computed(() => [
  { id: 'top', flex: true, min: PREVIEW_MIN },
  { id: 'bottom', size: taskHeight.value, min: 100, max: 1200, visible: taskOpen.value },
])

// 窄屏（≤ NARROW_MAX）：自动收起文件树（保留占位索引 → preview 槽位不位移）。
// 三列无法同时容纳时优先保 preview（CodeView / 设置 / 场景页 = 主内容面，须持续挂载）
// 与 chat；文件树为导航面，放大窗口即恢复。
const filetreeConfig = computed(() => [
  {
    id: 'filetree',
    size: effFiletreeWidth.value,
    min: 180,
    max: 600,
    visible: filetreeOpen.value && !narrow.value,
  },
  { id: 'preview', flex: true, min: PREVIEW_MIN },
])

const taskConfig = computed(() => [
  { id: 'session-tree', size: effSessiontreeWidth.value, min: 200, max: 600 },
  { id: 'session-chat', flex: true, min: SESSION_CHAT_MIN },
])

function clamp(v, min, max) {
  return Math.max(min, Math.min(max, v))
}

// 应用保存的布局（启动恢复）。所有尺寸先按窗口实际大小 clamp：
// 保存值来自上一次会话，窗口可能已变小/换显示器，直接套用会溢出。
function applyLayout(l) {
  if (!l) return
  const vw = window.innerWidth
  const vh = window.innerHeight
  if (vw < 200 || vh < 200) return // 窗口尺寸异常（Hidden 阶段）→ 保持默认
  const contentH = computeContentHeight(vh) // 内容区高 = 总高 − toolbar − statusbar（token 口径）
  const minCode = PREVIEW_MIN // 给 preview（CodeView）至少保留的宽度
  // chat 宽度：280..800，且不超过窗口 45%
  chatWidth.value = clamp(Number(l.chatWidth) || 520, 280, Math.min(800, Math.round(vw * 0.45)))
  // sessiontree 宽度：200..600，且不超过窗口 40%
  sessiontreeWidth.value = clamp(Number(l.sessiontreeWidth) || 400, 200, Math.min(600, Math.round(vw * 0.4)))
  // filetree 宽度：180..600，且给 preview 至少留 minCode
  const maxFile = vw - chatWidth.value - minCode - LAYOUT_GAPS
  filetreeWidth.value = clamp(Number(l.filetreeWidth) || 320, 180, Math.min(600, Math.max(180, maxFile)))
  // filetree 高度（top 面板）：200..(contentH - bottomMin)；bottom=SessionTree 区随之推导
  const fh = clamp(Number(l.filetreeHeight) || (contentH - 4 - 500), 200, contentH - 4 - 100)
  filetreeHeight.value = fh
  taskHeight.value = Math.max(100, contentH - 4 - fh)
  // toolbar 开关状态（filetree / 子 session(task) / chat 是否显示）
  if (typeof l.filetreeOpen === 'boolean') filetreeOpen.value = l.filetreeOpen
  if (typeof l.taskOpen === 'boolean') taskOpen.value = l.taskOpen
  if (typeof l.chatOpen === 'boolean') chatOpen.value = l.chatOpen
}

// 测量各面板实际渲染尺寸 + toolbar 开关状态（拖拽/缩放/切换后的真实值）
function measureLayout() {
  const rect = (el) => (el && el.getBoundingClientRect ? el.getBoundingClientRect() : null)
  const fw = rect(fileTreeEl.value?.$el)
  const cw = rect(chatPanelEl.value?.$el)
  const sw = rect(sessionTreeEl.value?.$el)
  const ok = (r) => r && r.width > 50 && r.height > 50 // 隐藏面板宽度/高度为 0，跳过
  return {
    filetreeWidth: ok(fw) ? Math.round(fw.width) : filetreeWidth.value,
    filetreeHeight: ok(fw) ? Math.round(fw.height) : filetreeHeight.value,
    chatWidth: ok(cw) ? Math.round(cw.width) : chatWidth.value,
    sessiontreeWidth: ok(sw) ? Math.round(sw.width) : sessiontreeWidth.value,
    filetreeOpen: filetreeOpen.value,
    taskOpen: taskOpen.value,
    chatOpen: chatOpen.value,
  }
}

let layoutTimer = null
function saveLayoutDebounced() {
  clearTimeout(layoutTimer)
  layoutTimer = setTimeout(() => {
    layoutTimer = null
    saveLayoutState(measureLayout())
      .catch(e => console.warn('[Layout] saveLayoutState error:', e))
  }, 500)
}

// 退出兜底：Alt+F4 / WM_CLOSE 关闭路径不触发后端 window-close 事件，
// 用 sendBeacon 尽力投递（fetch 在卸载阶段不可靠）；走 gui.ui.save 消息面
// （原 /call/SaveLayoutState 随 /call 清零，2026-09-04）。
function beaconSaveLayout() {
  try {
    const payload = JSON.stringify({ layout: measureLayout() })
    const body = JSON.stringify({ type: 'gui.ui.save', payload })
    navigator.sendBeacon('/publish', new Blob([body], { type: 'application/json' }))
  } catch (_) { /* noop */ }
}

// 配置/场景改为 preview 区 tab（CodeView 按 kind 渲染）
// configOpen（状态栏等入口）→ LLM 配置页；项目配置入口 → 项目配置页。
function handleOpenConfig() {
  mq.emit(EventNames.previewTabOpen, { kind: 'settings-llm' })
}

// project-config-open 可选 payload.tab（如 'codegraph' / 'vfts'）= 定位到的项目配置页签；
// 缺省/非法 → 不携带 tab，落到项目配置默认页签（向后兼容旧发送方）。
function handleOpenProjectConfig(event) {
  const tab = event && typeof event === 'object' ? event.tab : undefined
  const payload = { kind: 'settings-project' }
  if (tab) payload.tab = tab
  mq.emit(EventNames.previewTabOpen, payload)
}

function handleOpenScenario() {
  mq.emit(EventNames.previewTabOpen, { kind: 'scenario' })
}

const _mqUnsubs = []

function onWindowResize() {
  // 视口宽变化 → 有效尺寸/窄屏收起随视口重算（防面板塌成 0）
  viewportW.value = window.innerWidth
  saveLayoutDebounced()
}

onMounted(() => {
  _mqUnsubs.push(mq.on(EventNames.configOpen, handleOpenConfig))
  _mqUnsubs.push(mq.on(EventNames.projectConfigOpen, handleOpenProjectConfig))
  _mqUnsubs.push(mq.on(EventNames.scenarioOpen, handleOpenScenario))
  // 会话入口（工具栏 Sessions 按钮）→ 切到左侧导航「会话」页签（原会话抽屉已迁入该页签，P3-C1）
  _mqUnsubs.push(mq.on(EventNames.sessionsOpen, () => mq.emit(EventNames.filetreeModeSelect, { mode: 'sessions' })))
  _mqUnsubs.push(mq.on(EventNames.chatToggle, () => { chatOpen.value = !chatOpen.value; nextTick(saveLayoutDebounced) }))
  _mqUnsubs.push(mq.on(EventNames.filetreeToggle, () => { filetreeOpen.value = !filetreeOpen.value; nextTick(saveLayoutDebounced) }))
  _mqUnsubs.push(mq.on(EventNames.tasksToggle, () => { taskOpen.value = !taskOpen.value; nextTick(saveLayoutDebounced) }))

  // 启动恢复：LoadInitData 返回 layout / ui；应用前已做窗口关系校验。
  // locale：localStorage（getInitialLocale 同步初始化）优先，缺失时用 DB
  // ui.locale 兜底（WebView2 缓存被清时语言不丢）。theme 由 Toolbar 恢复。
  // 取 App 引导期**预取**的 init-data（desktop：与 instance-claim 并行发起）→ 挂载时已就绪，
  // 恢复随首帧生效；无预取（gui/browser 或已被消费）→ 回落实时读取（行为同改前）。
  loadInitDataPrefetched().then(r => {
    applyLayout(r.layout || null)
    const ui = r.ui || null
    if (ui && ui.locale && !localStorage.getItem('chonkpilot-locale')) {
      setLocale(ui.locale)
    }
  }).catch(() => {})
  window.addEventListener('resize', onWindowResize)
  window.addEventListener('beforeunload', beaconSaveLayout)
})

onUnmounted(() => {
  for (const fn of _mqUnsubs) fn()
  _mqUnsubs.length = 0
  window.removeEventListener('resize', onWindowResize)
  window.removeEventListener('beforeunload', beaconSaveLayout)
})
</script>

<style scoped>
.main-layout {
  height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
}
</style>
