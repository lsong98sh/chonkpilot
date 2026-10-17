<template>
  <div class="main-layout">
    <SplitPanel direction="vertical" :gap="0" :panes="outerConfig">
      <template #pane-0>
        <!-- 自由布局形态（P4⑤ 默认）：顶栏由 `layout.free` 的 topToolbar 条目渲染（含窗口控件固定右端） -->
        <ChonkTopBar v-if="freeMode" />
        <Toolbar v-else />
      </template>
      <template #pane-1>
        <!-- 自由布局容器（UILAYOUT-1 P3⑤ / P4⑤）：**默认形态**（`?layout=legacy` 可回退） -->
        <ChonkLayout v-if="freeMode" />
        <!-- 旧布局分支（**保留**：`?layout=legacy` 时使用 —— 可回退 / 可对比） -->
        <SplitPanel v-else direction="horizontal" :gap="4" :panes="bodyConfig" @size-changed="saveLayoutDebounced">
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
      <template #pane-2>
        <!-- 自由布局形态（P4⑤ 默认）：状态栏由 `layout.free` 的 statusbar 条目渲染 -->
        <ChonkStatusBar v-if="freeMode" />
        <StatusBar v-else />
      </template>
    </SplitPanel>

    <!-- 自由布局容器的运行期外壳（仅 free 形态挂载）：具名抽屉 + 内容设置编辑器 -->
    <template v-if="freeMode">
      <LfDrawerHost />
      <LfContentSettingsDialog />
    </template>

    <AskUserDialog />

    <!-- frameless 主窗口边缘 resize 命中层（WIN-1 / G-59）：仅 GUI/桌面形态渲染（browser 无宿主） -->
    <WindowResizeHandles />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, defineAsyncComponent, h } from 'vue'
import { dialog } from '../../components/dialog'
import { useI18n } from 'vue-i18n'
import { SplitPanel } from '../../components/split'
import WindowResizeHandles from '../../components/shell/WindowResizeHandles.vue'
import { loadInitDataPrefetched, saveLayoutState } from '../../api/file'
import { GuiInitDataKeys } from '../../events/msgkeys'
import { computeContentHeight, readRootPx, FALLBACK_TOOLBAR_HEIGHT, FALLBACK_STATUSBAR_HEIGHT } from '../../utils/cssToken'
import Toolbar from '../toolbar/Toolbar.vue'
import ExplorerPane from '../filetree/ExplorerPane.vue'
import ChatPanel from '../chat/ChatPanel.vue'
import SessionTree from '../tasks/SessionTree.vue'
import SessionChat from '../tasks/SessionChat.vue'
import AskUserDialog from '../chat/AskUserDialog.vue'
import StatusBar from '../statusbar/StatusBar.vue'
import { setLocale } from '../../plugins/i18n'
import { maybeAutoOpenWizard } from '../scenario/scenarioWizard'
import mq, { currentInstanceId } from '../../utils/mq'
import { EventNames } from '../../events/event-names'
// ── 自由布局容器（UILAYOUT-1 §7 P3⑤）──────────────────────────────
// 宿主唯一装配点 = `layout/index.js`（main.js 在 pinia/mqPlugin 之后调用）；
// `ChonkLayout` = 容器渲染点；抽屉 / 内容设置编辑器为容器的运行期外壳（仅 free 形态挂载）。
import ChonkLayout from '../../layout/ChonkLayout.vue'
import ChonkTopBar from '../../layout/ChonkTopBar.vue'
import ChonkStatusBar from '../../layout/ChonkStatusBar.vue'
import LfDrawerHost from '../../layout-free/components/drawer/LfDrawerHost.vue'
import LfContentSettingsDialog from '../../layout-free/components/settings/LfContentSettingsDialog.vue'
import { getLayoutEngine, getLayoutPersist } from '../../layout'
import { LAYOUT_MODE_FREE, LAYOUT_MODE_LEGACY, parseLayoutParam, resolveLayoutMode } from '../../layout/mode.js'
import { extractFreeDoc, toFreeString } from '../../layout/persist.js'

// 懒加载：仅在打开场景弹窗时加载
const ScenarioDialogContent = defineAsyncComponent(() => import('../scenario/ScenarioDialogContent.vue'))

const { t } = useI18n()

const CodeView = defineAsyncComponent(() => import('../codeview/CodeView.vue'))

// 区块显隐（UILAYOUT-1 裁决 2，2026-10-16）：`chat` / `task` / `filetree` 三区**去掉显隐功能**
// （「显示/隐藏」入口与切换能力一并移除，后续用其他方式实现）→ 三区**始终渲染**（不再传
// `SplitPanel` 的 `visible`），故此处不再有 `chatOpen / filetreeOpen / taskOpen` 三个开关 ref。
// 唯一的例外 = **窄屏自适应**收起文件树（`narrow`，见 `filetreeConfig`）——那是响应式适配，
// 不是用户显隐入口（P3 按设计稿 §5 映射为区域 `minWidth` + 窄屏自动隐藏）。
const analyzeDialog = ref(null)

// ── 布局形态（UILAYOUT-1 §3.9 / §3.13 / §7 P3⑤ / P4⑤）────────────────────────
//   free   = 自由布局容器（`<ChonkLayout>` + 条目化顶栏 / 状态栏）—— **默认**；
//   legacy = 既有硬编码分支（**始终保留**，可回退 / 可对比）。
// 判定：`?layout=legacy` → 回退旧分支；`?layout=free` / 未指定 → free（P4⑤ 裁决 3「切默认 free」）。
const layoutForced = parseLayoutParam(window.location.search)
const freeMode = ref(layoutForced !== LAYOUT_MODE_LEGACY)

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
  // chat 区：始终渲染（裁决 2，2026-10-16 —— 不再有 visible 显隐）
  { id: 'chat', size: effChatWidth.value, min: 280, max: 800 },
])

const topBottomConfig = computed(() => [
  { id: 'top', flex: true, min: PREVIEW_MIN },
  // 任务区：始终渲染（裁决 2 —— 原「默认收起 / 首屏减负」随显隐功能一并取消）
  { id: 'bottom', size: taskHeight.value, min: 100, max: 1200 },
])

// 窄屏（≤ NARROW_MAX）自适应收起文件树（保留占位索引 → preview 槽位不位移）。
// 三列无法同时容纳时优先保 preview（CodeView / 设置 / 场景页 = 主内容面，须持续挂载）
// 与 chat；文件树为导航面，放大窗口即恢复。**非用户显隐入口**（裁决 2 只移除用户显隐）。
const filetreeConfig = computed(() => [
  {
    id: 'filetree',
    size: effFiletreeWidth.value,
    min: 180,
    max: 600,
    visible: !narrow.value,
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
// 〔裁决 2（2026-10-16）〕**不再读** `layout.filetreeOpen / taskOpen / chatOpen` —— 三区已无显隐：
// 持久层里残留的旧键（旧版本落库）**一律忽略**（缺失即按显示处理），不做数据迁移。
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
}

// ── 自由布局形态（UILAYOUT-1 §3.9 恢复 / §7 P3⑤ / P4⑤）────────────────────────
// 启动恢复：`gui.init-data` 的 `layout.free` 文档 → 灌进布局域 store → `<ChonkLayout>` 渲染。
// 无文档 = 采用适配层默认文档（P4⑤ 起 free 是默认形态）；`?layout=legacy` → 回退旧分支。
function applyFreeLayout(layoutState) {
  const engine = getLayoutEngine()
  const persist = getLayoutPersist()
  if (!engine || !persist) return
  const doc = extractFreeDoc(layoutState)
  const mode = resolveLayoutMode({ forced: layoutForced })
  if (mode !== LAYOUT_MODE_FREE) {
    freeMode.value = false
    return
  }
  if (doc) persist.adopt(engine.state.config, layoutState)
  else persist.adoptDefault(engine.state.config)
  freeMode.value = true
}

// 默认 free：setup 期即灌默认文档 → 首帧就是容器（不等 init-data 往返，避免旧分支闪现）
if (freeMode.value) {
  const persist = getLayoutPersist()
  const engine = getLayoutEngine()
  if (persist && engine) persist.adoptDefault(engine.state.config)
}

// 测量各面板实际渲染尺寸（拖拽/缩放后的真实值）
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
  }
}

let layoutTimer = null
function saveLayoutDebounced() {
  // 自由布局形态：尺寸由容器的 `saveLiveLayout` → `layout.free` 落库，旧 7 字段不再写（§3.9）
  if (freeMode.value) return
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
    // beacon 退路不经 mq.emit → 手工补 instance_id（与 mq 注入同口径，61-消息一览 §0；E-03）。
    // 自由布局形态：投递 `layout.free`（§3.9 退出兜底；与 `gui.ui.save` 同 payload 形状）。
    const engine = getLayoutEngine()
    const layoutState = freeMode.value
      ? { free: toFreeString(engine ? engine.state.config.live : null) }
      : measureLayout()
    const payload = JSON.stringify({ layout: layoutState, instance_id: currentInstanceId() })
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

// agent-wizard：桥检测到 <workDir>/.chonkpilot/project_spec.md 缺失 → 打开场景向导。
// 去重由 maybeAutoOpenWizard 的模块级 flag 承担（见 views/scenario/scenarioWizard.js）。
function handleAgentWizard(payload) {
  maybeAutoOpenWizard(payload && typeof payload === 'object' ? payload : {})
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
  // 向导自动弹出（project_spec.md 缺失）：由瘦插件发布 agent-wizard → 打开向导
  _mqUnsubs.push(mq.on(EventNames.agentWizard, handleAgentWizard))
  // 会话入口（工具栏 Sessions 按钮）→ 切到左侧导航「会话」页签（原会话抽屉已迁入该页签，P3-C1）
  _mqUnsubs.push(mq.on(EventNames.sessionsOpen, () => mq.emit(EventNames.filetreeModeSelect, { mode: 'sessions' })))
  // 〔裁决 2（2026-10-16）〕`chat-toggle / filetree-toggle / tasks-toggle` 三个显隐主题**不再订阅**：
  // 三区始终渲染（无显隐能力）。主题本身仍定义在 `events/event-names.js`（零增删改；
  // 由聊天内「查看任务详情」等既有发送方继续发，收到即 no-op）。

  // 启动恢复：LoadInitData 返回 layout / ui；应用前已做窗口关系校验。
  // locale：localStorage（getInitialLocale 同步初始化）优先，缺失时用 DB
  // ui.locale 兜底（WebView2 缓存被清时语言不丢）。theme 由 Toolbar 恢复。
  // 取 App 引导期**预取**的 init-data（desktop：与 instance-claim 并行发起）→ 挂载时已就绪，
  // 恢复随首帧生效；无预取（gui/browser 或已被消费）→ 回落实时读取（行为同改前）。
  loadInitDataPrefetched().then(r => {
    applyLayout(r[GuiInitDataKeys.layout] || null)
    // 自由布局形态（P3⑤）：有 `layout.free` → 采用文档并切到容器；无 → 保持旧分支
    applyFreeLayout(r[GuiInitDataKeys.layout] || null)
    const ui = r[GuiInitDataKeys.ui] || null
    if (ui && ui.locale && !localStorage.getItem('chonkpilot-locale')) {
      setLocale(ui.locale)
    }
    // 启动检测兜底：init-data 在 App 引导期预取，早于本组件订阅 agent-wizard →
    // 桥同期下发的事件可能漏收；改用应答字段 wizard_required 兜底自动打开（本会话仅一次）。
    if (r && r[GuiInitDataKeys.wizard_required] === true) {
      // init-data 应答的 workdir 键名为 camelCase `workDir`（见 bridge/local.go callLoadInitData）。
      maybeAutoOpenWizard({ reason: 'missing_spec', work_dir: r[GuiInitDataKeys.workDir] || '' })
    }
  }).catch(() => {})
  window.addEventListener('resize', onWindowResize)
  window.addEventListener('beforeunload', beaconSaveLayout)
})

onUnmounted(() => {
  for (const fn of _mqUnsubs) fn()
  _mqUnsubs.length = 0
  // 卸载时清掉在飞布局去抖定时器，避免组件销毁后回调仍触发（E-16）。
  if (layoutTimer) { clearTimeout(layoutTimer); layoutTimer = null }
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
