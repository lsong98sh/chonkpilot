/**
 * 宿主侧视图装配点（UIREG-1）：内置视图与插件视图**共用同一条注册 API**（`registerViews`）。
 *
 * 分层（UILAYOUT-1 §2）：
 *   - 元数据（key / label / icon / host / designable …）在 `view-definitions.js`（纯数据）；
 *   - 组件注册 / 解析的**唯一实现**在 `view-registry.js`（纯模块，`registerViews` → `resolve`）；
 *   - 本文件只做**宿主装配**：把真实 `.vue` 组件按 key 交给注册表，并把插件视图
 *     （`plugin:<id>:<view>`，iframe 承载）接到同一 API。**不再有静态组件映射表**。
 *
 * 新增区块 = `view-definitions.js` 加一条元数据 + 这里 `registerViews` 加一条
 * `{ key, component }`；新增插件视图 = 插件 `plugin.json` 的 `ui.views`（无需改本文件）。
 */
import { defineAsyncComponent, defineComponent, h } from 'vue'
import ExplorerPane from '../views/filetree/ExplorerPane.vue'
import ChatPanel from '../views/chat/ChatPanel.vue'
import SessionTree from '../views/tasks/SessionTree.vue'
import SessionChat from '../views/tasks/SessionChat.vue'
import TaskDetailSlot from './views/TaskDetailSlot.vue'
import DslStepsSlot from './views/DslStepsSlot.vue'
import SearchBoxView from './bar-views/SearchBoxView.vue'
import RecentDirsView from './bar-views/RecentDirsView.vue'
import ChromeWarnView from './bar-views/ChromeWarnView.vue'
import MemoryTokenView from './bar-views/MemoryTokenView.vue'
import QueueStatusView from './bar-views/QueueStatusView.vue'
import IndexBadgeView from './bar-views/IndexBadgeView.vue'
import LangSwitcherView from './bar-views/LangSwitcherView.vue'
import LayoutProfilesView from './bar-views/LayoutProfilesView.vue'
import MemoryStatusView from './bar-views/MemoryStatusView.vue'
import CompressStatusView from './bar-views/CompressStatusView.vue'
import MemoryStatusDetail from '../views/status/MemoryStatusDetail.vue'
import CompressStatusDetail from '../views/status/CompressStatusDetail.vue'
import IndexStatusDetail from '../views/status/IndexStatusDetail.vue'
import PluginPageHost from '../components/plugin/PluginPageHost.vue'
import {
  createViewRegistry,
  registerPluginViews as registerPluginViewsWith,
  registerViews,
  registeredViewKeys,
  unregisterPluginViews,
} from './view-registry.js'

export { VIEW_DEFINITIONS, definitionOf, hostOf } from './view-definitions.js'
export { createViewRegistry, registeredViewKeys, unregisterPluginViews }

/** 预览区：与既有 `MainLayout.vue` 同口径（懒加载 CodeView，不改变既有分包）。 */
const PreviewView = defineAsyncComponent(() => import('../views/codeview/CodeView.vue'))

/*
 * 内置视图注册（与 `view-definitions.js` 的元数据 key 一一对应）。
 * 带 props 需求的区块经 `layout/views/*Slot.vue` 适配壳转发（容器的 `viewRef.params` → 组件 props）；
 * 栏视图条目（`host:'bar'`，UILAYOUT-1 §3.4 / §7 P4①）在 `layout/bar-views/` 下。
 */
registerViews([
  // ── 布局区块 ──
  { key: 'explorer', component: ExplorerPane },
  { key: 'preview', component: PreviewView },
  { key: 'chat', component: ChatPanel },
  { key: 'session-tree', component: SessionTree },
  { key: 'session-chat', component: SessionChat },
  { key: 'task-detail', component: TaskDetailSlot },
  { key: 'dsl-steps', component: DslStepsSlot },
  // ── 栏视图条目（P4①）──
  { key: 'search', component: SearchBoxView },
  { key: 'recent-dirs', component: RecentDirsView },
  { key: 'chrome-warn', component: ChromeWarnView },
  { key: 'memory-token', component: MemoryTokenView },
  { key: 'queue-status', component: QueueStatusView },
  { key: 'index-badge', component: IndexBadgeView },
  { key: 'memory-status', component: MemoryStatusView },
  { key: 'compress-status', component: CompressStatusView },
  { key: 'lang-switcher', component: LangSwitcherView },
  { key: 'layout-profiles', component: LayoutProfilesView },
  // ── 长任务详情页（STB-1）──
  { key: 'status-memory', component: MemoryStatusDetail },
  { key: 'status-compress', component: CompressStatusDetail },
  { key: 'status-index', component: IndexStatusDetail },
], 'builtin')

/** 插件 iframe view 的组件工厂（`PluginPageHost` 适配壳，与内置视图同一注册 API）。 */
function pluginViewComponent({ plugin, view, entry }) {
  return defineComponent({
    name: `PluginView_${plugin.id}_${view.id}`,
    render() {
      return h(PluginPageHost, {
        pluginId: plugin.id,
        pageId: view.id,
        entry,
        title: view.title || '',
      })
    },
  })
}

/** 注册/覆盖一个插件的全部 iframe view（`plugin:<id>:<view>`）。幂等：同 id 先卸载再注册。 */
export function registerPluginViews(plugin) {
  registerPluginViewsWith(plugin, pluginViewComponent)
}
