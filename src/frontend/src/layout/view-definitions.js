/**
 * 区块定义表（纯数据，UILAYOUT-1 §3.4 / §7 P3①）。
 *
 * 从 `views.registry.js` 拆出来的原因（分层）：
 *   - 本文件**只含定义**（零 Vue / 零 `.vue` import）→ 适配层其它模块（`openView.js` 落点分发、
 *     单测）可以只依赖定义，不必把宿主区块组件拖进依赖图；
 *   - 组件注册 / 解析只有一条路径：`view-registry.js`（UIREG-1；内置与插件视图共用 `registerViews`）。
 *
 * 定义字段：`key / labelKey / label / icon / mode / host / designable / params / default*`
 * （逐字段含义见 `views.registry.js` 文件头）。
 *
 * `host` = **承载方式声明**（不是"翻译表"）：`layout`（布局区）/ `bar`（栏内嵌）/
 * `dialog`（对话框）/ `preview`（预览区功能页页签）。打开逻辑 `openView()` 按此分发。
 */

/** 一期区块定义（顺序 = 编辑器下拉/文档默认顺序）。 */
export const VIEW_DEFINITIONS = [
  {
    key: 'explorer',
    labelKey: 'view.explorer',
    label: '文件',
    icon: 'folder-opened',
    mode: 'singleton',
    host: 'layout',
    designable: true,
    defaultMinWidth: 180,
    defaultMaxWidth: 600,
  },
  {
    key: 'preview',
    labelKey: 'view.preview',
    label: '预览',
    icon: 'document',
    mode: 'singleton',
    host: 'layout',
    designable: true,
    defaultMinWidth: 280,
  },
  {
    key: 'chat',
    labelKey: 'view.chat',
    label: '对话',
    icon: 'chat-dot-round',
    mode: 'singleton',
    host: 'layout',
    designable: true,
    defaultMinWidth: 280,
    defaultMaxWidth: 800,
  },
  {
    key: 'session-tree',
    labelKey: 'view.sessionTree',
    label: '会话树',
    icon: 'list',
    mode: 'singleton',
    host: 'layout',
    designable: true,
    defaultMinWidth: 200,
    defaultMaxWidth: 600,
  },
  {
    key: 'session-chat',
    labelKey: 'view.sessionChat',
    label: '会话内容',
    icon: 'chat-dot-square',
    mode: 'singleton',
    host: 'layout',
    designable: true,
    defaultMinWidth: 240,
  },
  {
    key: 'task-detail',
    labelKey: 'view.taskDetail',
    label: '任务详情',
    icon: 'list',
    mode: 'singleton',
    host: 'layout',
    /** 设计态不挂载真实组件：props `taskId` 取真实任务数据（设计态取数会污染真实状态，§3.11） */
    designable: false,
    params: ['taskId'],
  },
  {
    key: 'dsl-steps',
    labelKey: 'view.dslSteps',
    label: '步骤面板',
    icon: 'list',
    mode: 'singleton',
    host: 'layout',
    designable: false,
    params: ['jobId'],
  },
  {
    key: 'search',
    labelKey: 'view.search',
    label: '搜索',
    icon: 'search',
    mode: 'singleton',
    /** 栏内嵌 view（`kind:'view'` 条目），非布局区域（§3.4） */
    host: 'bar',
    designable: true,
    defaultWidth: 260,
    defaultHeight: 26,
  },
  /* ── 栏视图条目（UILAYOUT-1 §7 P4① / 裁决 2）：实时 / 条件 / 动态项一律做成栏内嵌 view ── */
  {
    key: 'recent-dirs',
    labelKey: 'view.recentDirs',
    label: '最近项目',
    icon: 'folder',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 30,
    defaultHeight: 26,
  },
  {
    key: 'chrome-warn',
    labelKey: 'view.chromeWarn',
    label: 'Chrome 告警',
    icon: 'warning-filled',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 26,
    defaultHeight: 26,
  },
  {
    key: 'memory-token',
    labelKey: 'view.memoryToken',
    label: '记忆 token',
    icon: 'coin',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 120,
    defaultHeight: 20,
  },
  {
    key: 'queue-status',
    labelKey: 'view.queueStatus',
    label: '队列状态',
    icon: 'timer',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 140,
    defaultHeight: 20,
  },
  /* 长任务状态条目（STB-1）：记忆提取 / 上下文压缩各一枚（数据源 = tool-notify） */
  {
    key: 'memory-status',
    labelKey: 'view.memoryStatus',
    label: '记忆提取状态',
    icon: 'collection',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 120,
    defaultHeight: 20,
  },
  {
    key: 'compress-status',
    labelKey: 'view.compressStatus',
    label: '上下文压缩状态',
    icon: 'document',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 120,
    defaultHeight: 20,
  },
  {
    key: 'lang-switcher',
    labelKey: 'view.langSwitcher',
    label: '语言切换',
    icon: 'flag',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 64,
    defaultHeight: 20,
  },
  /* 布局档案下拉（UILAYOUT-2 / WIN-026）：切换 / 新建 / 编辑当前 / 恢复初始布局
     defaultWidth = 单个图标按钮的实际占位（过大曾把右区撑超定宽导致条目被裁） */
  {
    key: 'layout-profiles',
    labelKey: 'view.layoutProfiles',
    label: '布局',
    icon: 'grid',
    mode: 'singleton',
    host: 'bar',
    designable: false,
    defaultWidth: 30,
    defaultHeight: 26,
  },
  /* CK Store 抽屉（PLUGIN-LOAD-1 D17）：状态栏右侧常驻图标（内置附加条目，不写布局文档） */
  {
    key: 'ck-store',
    labelKey: 'view.ckStore',
    label: 'CK Store',
    icon: 'collection',
    mode: 'singleton',
    host: 'bar',
    designable: false,
    defaultWidth: 24,
    defaultHeight: 20,
  },
  /* ── 长任务详情页（STB-1）：状态栏条目点击 → `openView('status-*')` → preview 功能页 ──
     数据驱动（真实任务状态），设计态不挂载真实组件（designable:false）。
     承载方式如实声明为 `preview`（预览区功能页页签），不再借用 `layout`。 */
  {
    key: 'status-memory',
    labelKey: 'view.statusMemory',
    label: '记忆提取详情',
    icon: 'collection',
    mode: 'singleton',
    host: 'preview',
    designable: false,
  },
  {
    key: 'status-compress',
    labelKey: 'view.statusCompress',
    label: '上下文压缩详情',
    icon: 'document',
    mode: 'singleton',
    host: 'preview',
    designable: false,
  },
]

/**
 * 动态 view 定义（插件平台 PLUGIN-1）：`plugin:<id>:<view>`。
 * 由 `view-registry.js` 在插件注册/卸载时增删；`definitionOf` 同时查静态表与动态表。
 */
export const DYNAMIC_DEFINITIONS = []

/** 按 viewKey 取定义（静态 + 动态插件 view）。 */
export function definitionOf(viewKey) {
  return (
    VIEW_DEFINITIONS.find((v) => v.key === viewKey) ||
    DYNAMIC_DEFINITIONS.find((v) => v.key === viewKey) ||
    null
  )
}

/** 该 view 声明的承载方式（layout | dialog | bar | preview；未知 → null）。 */
export function hostOf(viewKey) {
  return definitionOf(viewKey)?.host || null
}

