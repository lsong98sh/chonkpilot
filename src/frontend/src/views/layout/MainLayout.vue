<template>
  <div class="main-layout">
    <SplitPanel direction="vertical" :gap="0" :panes="outerConfig">
      <template #pane-0>
        <!-- 顶栏由 `layout.free` 的 topToolbar 条目渲染（含窗口控件固定右端） -->
        <ChonkTopBar />
      </template>
      <template #pane-1>
        <!-- 自由布局容器（唯一形态；legacy 硬编码布局已于 UILAYOUT-2-A 删除） -->
        <ChonkLayout />
      </template>
      <template #pane-2>
        <!-- 状态栏由 `layout.free` 的 statusbar 条目渲染 -->
        <ChonkStatusBar />
      </template>
    </SplitPanel>

    <!-- 自由布局容器的运行期外壳：具名抽屉 + 内容设置编辑器 -->
    <LfDrawerHost />
    <LfContentSettingsDialog />
    <!-- 视图池（WIN-027）：单例 view 各挂载一次；切换布局只搬 DOM、不重建实例 -->
    <LfViewPool />

    <AskUserDialog />

    <!-- frameless 主窗口边缘 resize 命中层（WIN-1 / G-59）：仅 GUI/桌面形态渲染（browser 无宿主） -->
    <WindowResizeHandles />
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import { SplitPanel } from '../../components/split'
import WindowResizeHandles from '../../components/shell/WindowResizeHandles.vue'
import { loadInitDataPrefetched } from '../../api/file'
import { GuiInitDataKeys } from '../../events/msgkeys'
import { readRootPx, FALLBACK_TOOLBAR_HEIGHT, FALLBACK_STATUSBAR_HEIGHT } from '../../utils/cssToken'
import AskUserDialog from '../chat/AskUserDialog.vue'
import { setLocale } from '../../plugins/i18n'
import { maybeAutoOpenWizard } from '../scenario/scenarioWizard'
import mq, { currentInstanceId } from '../../utils/mq'
import { EventNames } from '../../events/event-names'
// ── 自由布局容器（UILAYOUT-1 §7 P3⑤ / P4⑤）──────────────────────
// 宿主唯一装配点 = `layout/index.js`（main.js 在 pinia/mqPlugin 之后调用）；
// `ChonkLayout` = 容器渲染点；抽屉 / 内容设置编辑器 / 视图池为容器的运行期外壳。
import ChonkLayout from '../../layout/ChonkLayout.vue'
import ChonkTopBar from '../../layout/ChonkTopBar.vue'
import ChonkStatusBar from '../../layout/ChonkStatusBar.vue'
import LfDrawerHost from '../../layout-free/components/drawer/LfDrawerHost.vue'
import LfContentSettingsDialog from '../../layout-free/components/settings/LfContentSettingsDialog.vue'
import LfViewPool from '../../layout-free/components/layout/LfViewPool.vue'
import { getLayoutEngine, getLayoutPersist } from '../../layout'
import { useProfileStore } from '../../layout/profiles.js'
import { extractFreeDoc, toFreeString } from '../../layout/persist.js'

// ── 外壳尺寸（顶栏 / 状态栏固定高）────────────────────────────
const TOOLBAR_H = readRootPx('--toolbar-height', FALLBACK_TOOLBAR_HEIGHT)
const STATUSBAR_H = readRootPx('--statusbar-height', FALLBACK_STATUSBAR_HEIGHT)

const outerConfig = computed(() => [
  { id: 'toolbar', size: TOOLBAR_H, resizable: false },
  { id: 'content', flex: true },
  { id: 'statusbar', size: STATUSBAR_H, resizable: false },
])

// ── 启动恢复：`gui.init-data` 的 `layout.free` 文档 → 灌进布局域 store ──
// 无文档 = 采用适配层默认文档。
function applyFreeLayout(layoutState) {
  const engine = getLayoutEngine()
  const persist = getLayoutPersist()
  if (!engine || !persist) return
  const doc = extractFreeDoc(layoutState)
  if (doc) persist.adopt(engine.state.config, layoutState)
  else persist.adoptDefault(engine.state.config)
}

// 默认文档：setup 期即灌 → 首帧就是容器（不等 init-data 往返）
{
  const persist = getLayoutPersist()
  const engine = getLayoutEngine()
  if (persist && engine) persist.adoptDefault(engine.state.config)
}

// 退出兜底：Alt+F4 / WM_CLOSE 关闭路径不触发后端 window-close 事件，
// 用 sendBeacon 尽力投递（fetch 在卸载阶段不可靠）；走 gui.ui.save 消息面。
function beaconSaveLayout() {
  try {
    // beacon 退路不经 mq.emit → 手工补 instance_id（与 mq 注入同口径，61-消息一览 §0；E-03）。
    const engine = getLayoutEngine()
    const layoutState = { free: toFreeString(engine ? engine.state.config.live : null) }
    const payload = JSON.stringify({ layout: layoutState, instance_id: currentInstanceId() })
    const body = JSON.stringify({ type: 'gui.ui.save', payload })
    navigator.sendBeacon('/publish', new Blob([body], { type: 'application/json' }))
  } catch (_) { /* noop */ }
}

// 配置/场景改为 preview 区 tab（CodeView 按 kind 渲染）
function handleOpenConfig() {
  mq.emit(EventNames.previewTabOpen, { kind: 'settings-llm' })
}

// project-config-open 可选 payload.tab = 定位到的项目配置页签（如 'codebase' / 'history'；
// 旧页签名 'codegraph' / 'vfts' 由 ProjectConfig 兼容映射到 'codebase'，见 UIREG-1）；
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

onMounted(() => {
  _mqUnsubs.push(mq.on(EventNames.configOpen, handleOpenConfig))
  _mqUnsubs.push(mq.on(EventNames.projectConfigOpen, handleOpenProjectConfig))
  _mqUnsubs.push(mq.on(EventNames.scenarioOpen, handleOpenScenario))
  // 向导自动弹出（project_spec.md 缺失）：由瘦插件发布 agent-wizard → 打开向导
  _mqUnsubs.push(mq.on(EventNames.agentWizard, handleAgentWizard))
  // 会话入口（工具栏 Sessions 按钮）→ 切到左侧导航「会话」页签（原会话抽屉已迁入该页签，P3-C1）
  _mqUnsubs.push(mq.on(EventNames.sessionsOpen, () => mq.emit(EventNames.filetreeModeSelect, { mode: 'sessions' })))

  // 启动恢复：LoadInitData 返回 layout / ui；应用前已做窗口关系校验。
  // locale：localStorage（getInitialLocale 同步初始化）优先，缺失时用 DB
  // ui.locale 兜底（WebView2 缓存被清时语言不丢）。theme 由顶栏栏视图恢复。
  loadInitDataPrefetched().then(r => {
    const layoutState = r[GuiInitDataKeys.layout] || null
    applyFreeLayout(layoutState)
    // 布局档案（UILAYOUT-2）：缺失才写种子；活动档案无效则清空标记
    useProfileStore().load(layoutState)
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
  window.addEventListener('beforeunload', beaconSaveLayout)
})

onUnmounted(() => {
  for (const fn of _mqUnsubs) fn()
  _mqUnsubs.length = 0
  window.removeEventListener('beforeunload', beaconSaveLayout)
})
</script>

<style scoped>
.main-layout {
  height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
  /* 视图池停车场（0×0 + overflow:hidden）的定位基准 */
  position: relative;
}
</style>
