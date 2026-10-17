/**
 * 区块注册表：本系统区块 → 自由布局容器的 view（UILAYOUT-1 §3.4 / §7 P3①）。
 *
 * 分层（§2）：**机制层只按 viewKey 解析**（`deps.resolveViewComponent` / `deps.viewDefinitions`），
 * 区块 ↔ viewKey ↔ 组件 的映射**只在本文件**（适配层）。新增区块 = 定义表加一条（`view-definitions.js`）
 * + 这里加一个组件映射。
 *
 * 定义字段（沿用参考工程 `config/views.js` 并扩展）：
 *   key          viewKey（配置文档里引用它）
 *   labelKey     i18n 键（`layoutFree.view.*`）→ label（经宿主 i18n 注入解析，语言切换即时生效）
 *   icon         图标名（宿主 `components/icon/icons.js` 的键）
 *   mode         singleton | multi —— **一期全部 singleton**（现有主题是全局广播、无实例过滤，§6）
 *   host         layout | dialog | bar —— 该 view 允许被放的位置
 *   designable   设计态是否挂载真实组件（§3.11 兜底：false → 设计态只渲染标题 + 骨架）
 *   params       需要从区域 `view.params` 取哪些字段（经 `layout/views/*Slot.vue` 转成组件 props）
 *
 * 〔一期 8 区块，逐条对齐 §3.4 表〕
 *   explorer · preview · chat · session-tree · session-chat · task-detail · dsl-steps · search
 *
 * ⚠️ `search` 的**组件映射**刻意留空：§3.4 表注明它「随栏条目化抽出独立组件后注册」，
 * 而栏条目化属 §7 P4 —— 一期先登记定义（编辑器下拉 / 单例占用登记需要它），
 * 组件抽取（把 `Toolbar.vue` 中段搜索箱抽成独立 view）与 P4 同批接线。
 */
import { defineAsyncComponent } from 'vue'
import ExplorerPane from '../views/filetree/ExplorerPane.vue'
import ChatPanel from '../views/chat/ChatPanel.vue'
import SessionTree from '../views/tasks/SessionTree.vue'
import SessionChat from '../views/tasks/SessionChat.vue'
import TaskDetailSlot from './views/TaskDetailSlot.vue'
import DslStepsSlot from './views/DslStepsSlot.vue'
import SearchBoxView from './bar-views/SearchBoxView.vue'
import RecentDirsView from './bar-views/RecentDirsView.vue'
import ChromeWarnView from './bar-views/ChromeWarnView.vue'
import LogoutView from './bar-views/LogoutView.vue'
import MemoryTokenView from './bar-views/MemoryTokenView.vue'
import QueueStatusView from './bar-views/QueueStatusView.vue'
import IndexBadgeView from './bar-views/IndexBadgeView.vue'
import LangSwitcherView from './bar-views/LangSwitcherView.vue'
import MemoryStatusView from './bar-views/MemoryStatusView.vue'
import CompressStatusView from './bar-views/CompressStatusView.vue'
import MemoryStatusDetail from '../views/status/MemoryStatusDetail.vue'
import CompressStatusDetail from '../views/status/CompressStatusDetail.vue'
import IndexStatusDetail from '../views/status/IndexStatusDetail.vue'
import { buildViewRegistry } from './view-definitions.js'

export { VIEW_DEFINITIONS, definitionOf, hostOf } from './view-definitions.js'

/** 预览区：与既有 `MainLayout.vue` 同口径（懒加载 CodeView，不改变既有分包）。 */
const PreviewView = defineAsyncComponent(() => import('../views/codeview/CodeView.vue'))

/**
 * 组件映射（viewKey → 组件）。
 * 带 props 需求的区块经 `layout/views/*Slot.vue` 适配壳转发（容器的 `viewRef.params` → 组件 props）；
 * `search` 等**栏视图条目**（`host:'bar'`，UILAYOUT-1 §3.4 / §7 P4①）在 `layout/bar-views/` 下，
 * 由本表接线（P3 时刻意留空，P4 抽取后补齐）。
 */
export const VIEW_COMPONENTS = {
  explorer: ExplorerPane,
  preview: PreviewView,
  chat: ChatPanel,
  'session-tree': SessionTree,
  'session-chat': SessionChat,
  'task-detail': TaskDetailSlot,
  'dsl-steps': DslStepsSlot,
  // ── 栏视图条目（P4①）──
  search: SearchBoxView,
  'recent-dirs': RecentDirsView,
  'chrome-warn': ChromeWarnView,
  logout: LogoutView,
  'memory-token': MemoryTokenView,
  'queue-status': QueueStatusView,
  'index-badge': IndexBadgeView,
  'lang-switcher': LangSwitcherView,
  // ── 长任务状态条目 + 详情页（STB-1）──
  'memory-status': MemoryStatusView,
  'compress-status': CompressStatusView,
  'status-memory': MemoryStatusDetail,
  'status-compress': CompressStatusDetail,
  'status-index': IndexStatusDetail,
}

/**
 * 组装注册表对象（注入 `createLayoutEngine({ views })`）。
 * 组装逻辑在 `view-definitions.js`（纯逻辑，可直跑单测）；这里把**真实组件表**接上。
 *
 * @param {(key: string) => string} [translate] 宿主 i18n 取值函数（`layout/i18n.js` 提供）
 */
export function createViewRegistry(translate) {
  return buildViewRegistry(translate, VIEW_COMPONENTS)
}
