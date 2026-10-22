/**
 * 区块定义表（纯数据，UILAYOUT-1 §3.4 / §7 P3①）。
 *
 * 从 `views.registry.js` 拆出来的原因（分层）：
 *   - 本文件**只含定义**（零 Vue / 零 `.vue` import）→ 适配层其它模块（`commands.js` 落点解析、
 *     单测）可以只依赖定义，不必把宿主区块组件拖进依赖图；
 *   - `views.registry.js` 负责「定义 + 组件映射」的组装（注入 `createLayoutEngine({ views })`）。
 *
 * 定义字段：`key / labelKey / label / icon / mode / host / designable / params / default*`
 * （逐字段含义见 `views.registry.js` 文件头）。
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
    key: 'logout',
    labelKey: 'view.logout',
    label: '登出',
    icon: 'user',
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
  {
    key: 'index-badge',
    labelKey: 'view.indexBadge',
    label: '索引徽标',
    icon: 'collection',
    mode: 'singleton',
    host: 'bar',
    designable: true,
    defaultWidth: 200,
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
  /* 布局档案下拉（UILAYOUT-2 / WIN-026）：切换 / 新建 / 编辑当前 / 恢复初始布局 */
  {
    key: 'layout-profiles',
    labelKey: 'view.layoutProfiles',
    label: '布局',
    icon: 'grid',
    mode: 'singleton',
    host: 'bar',
    designable: false,
    defaultWidth: 120,
    defaultHeight: 26,
  },
  /* ── 长任务详情页（STB-1）：状态栏条目点击 → `open-view{status-*}` → preview 功能页 ──
     数据驱动（真实任务状态），设计态不挂载真实组件（designable:false）。 */
  {
    key: 'status-memory',
    labelKey: 'view.statusMemory',
    label: '记忆提取详情',
    icon: 'collection',
    mode: 'singleton',
    host: 'layout',
    designable: false,
  },
  {
    key: 'status-compress',
    labelKey: 'view.statusCompress',
    label: '上下文压缩详情',
    icon: 'document',
    mode: 'singleton',
    host: 'layout',
    designable: false,
  },
  {
    key: 'status-index',
    labelKey: 'view.statusIndex',
    label: '代码索引详情',
    icon: 'search',
    mode: 'singleton',
    host: 'layout',
    designable: false,
  },
]

/** 按 viewKey 取定义（纯数据表；机制层 `deps.viewDefinitions()` 注入的版本带已解析 label）。 */
export function definitionOf(viewKey) {
  return VIEW_DEFINITIONS.find((v) => v.key === viewKey) || null
}

/** 该 view 允许被放的位置（layout | dialog | bar；未知 → null）。 */
export function hostOf(viewKey) {
  return definitionOf(viewKey)?.host || null
}

/**
 * 组装注册表对象（注入 `createLayoutEngine({ views })`）——**纯逻辑**（组件表由调用方给）。
 *
 * `definitions` 用 **getter**：每次读取按当前语言解析 `label` ——
 * 语言切换（`setLocale`）后编辑器下拉 / 标签页文案即时跟随（无需 watch，§4.2）。
 *
 * @param {(key: string) => string} [translate] 宿主 i18n 取值函数（`layout/i18n.js` 提供）
 * @param {Record<string, any>} [components] viewKey → 组件（`views.registry.js` 传真实组件表）
 */
export function buildViewRegistry(translate, components = {}) {
  const t = typeof translate === 'function' ? translate : (key) => key
  return {
    get definitions() {
      return VIEW_DEFINITIONS.map((d) => ({ ...d, label: t(d.labelKey) }))
    },
    components,
    /** 兼容 `deps` 的 resolve 形态（与 components 表等价） */
    resolve(viewKey) {
      return components[viewKey] || null
    },
  }
}
