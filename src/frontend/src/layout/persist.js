/**
 * 持久化：`layout.free` ↔ 既有 `gui.ui.save` / `gui.init-data`（UILAYOUT-1 §3.9 · §7 P3③）。
 *
 * 复用既有面、零新增字段名（§3.9）：
 *   - 保存：`gui.ui.save`，payload `{ layout: { free: '<布局文档 JSON 字符串>' } }`
 *           → 桥落 prj 配置 `layout.free`（`local.go` 的 `entries["layout."+k]`）；
 *   - 读取：`gui.init-data` 结果的 `layout` 子对象（`layout.*` 前缀自动带出）→ 取 `.free`；
 *   - 退出兜底：`navigator.sendBeacon('/publish', {type:'gui.ui.save', ...})`（由 MainLayout 发）。
 *
 * **为何不落 localStorage**（§3.9）：布局是「项目实例态」（与既有 `layout.*` 同域），
 * 沿用 prj 配置面才能跨 desktop / gui / browser / flash 形态一致。
 *
 * 旧字段迁移（§3.9 + **2026-10-16 更正注**）：只承接**尺寸类**四字段
 * （`filetreeWidth` / `filetreeHeight` / `chatWidth` / `sessiontreeWidth`）；
 * `filetreeOpen` / `taskOpen` / `chatOpen` 三键**已随「三区常显」裁决失去语义 → 忽略、不迁移**。
 *
 * 本模块是**纯逻辑 + 注入 IO**（`createLayoutPersist({ save, load })`）：
 * 不 import `api/file.js`（宿主 IO 由装配层注入）→ 可在 node:test 直跑。
 */
import { normalize } from '../layout-free/config/config-normalize'
import { cloneTree } from '../layout-free/utils/layout'
import { EventNames as LfEventNames } from '../layout-free/events/event-names.js'

/** 文档版本（沿用参考工程 `defaultConfig.js` 的单文档模型，结构不改） */
export const DOC_VERSION = 6

/* ── 默认尺寸（与既有 MainLayout 的默认值/下限同源）────────────
   〔对齐〕`MainLayout.vue`：filetreeWidth 320 / chatWidth 520 / sessiontreeWidth 400；
   PREVIEW_MIN 280 / SESSION_CHAT_MIN 240；filetree 180..600 / session-tree 200..600 / chat 280..800。 */
const D = {
  filetreeWidth: 320,
  filetreeHeight: 516,
  chatWidth: 520,
  sessiontreeWidth: 400,
  taskHeight: 500,
  previewMin: 280,
  sessionChatMin: 240,
  filetreeMin: 180,
  filetreeMax: 600,
  sessiontreeMin: 200,
  sessiontreeMax: 600,
  chatMin: 280,
  chatMax: 800,
}

/** 空栏结构（与机制层 `createBar()` 同形：对象而非数组） */
function createBar() {
  return { background: '', zoneBackground: { left: '', center: '', right: '' }, items: [] }
}

function num(v, fallback) {
  const n = Number(v)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

/** 占比归一（和 = 1；非法/全 0 → 均分） */
function ratio(list) {
  const arr = (list || []).map((v) => (Number.isFinite(Number(v)) && Number(v) > 0 ? Number(v) : 0))
  const sum = arr.reduce((a, b) => a + b, 0)
  if (!(sum > 0)) return arr.map(() => 1 / Math.max(1, arr.length))
  return arr.map((v) => Number((v / sum).toFixed(4)))
}

function leaf(id, viewKey, extra = {}) {
  return { id, type: 'leaf', kind: 'single', view: { viewKey, instanceKey: null }, ...extra }
}

function split(id, direction, children, sizes) {
  return { id, type: 'split', direction, sizes, children }
}

/**
 * 旧 7 字段（实际只读尺寸类 4 项）→ 布局文档（等价现有硬编码布局）。
 *
 * 结构（与现有 `MainLayout` 五层 `SplitPanel` 一一对应；`toolbar` / `statusbar` 由宿主组件承担
 * —— 一期不做栏条目化，故 `topToolbar` / `statusbar` 保持空条目）：
 *   row [ column [ row [explorer | preview], row [session-tree | session-chat] ] | chat ]
 *
 * @param {object} [legacy] `gui.init-data` 的 `layout` 子对象（旧版 7 字段）；缺省 = 默认尺寸
 */
export function migrateLegacyDoc(legacy = {}) {
  const l = legacy && typeof legacy === 'object' ? legacy : {}
  const filetreeW = num(l.filetreeWidth, D.filetreeWidth)
  const filetreeH = num(l.filetreeHeight, D.filetreeHeight)
  const chatW = num(l.chatWidth, D.chatWidth)
  const sessiontreeW = num(l.sessiontreeWidth, D.sessiontreeWidth)
  // 下方面板高（旧字段未直接持久化 → 由内容高与上方面板高推导；缺省取 D.taskHeight）
  const taskH = num(l.taskHeight, D.taskHeight)
  // 尺寸类三键按 §3.9 只做 clamp（与既有 applyLayout 的窗口关系校验同口径，此处按名义值 clamp）
  const filetree = Math.min(D.filetreeMax, Math.max(D.filetreeMin, filetreeW))
  const sessiontree = Math.min(D.sessiontreeMax, Math.max(D.sessiontreeMin, sessiontreeW))
  const chat = Math.min(D.chatMax, Math.max(D.chatMin, chatW))

  const leftTop = split(
    'split_left_top',
    'row',
    [
      leaf('leaf_file', 'explorer', { minWidth: D.filetreeMin, maxWidth: D.filetreeMax }),
      leaf('leaf_preview', 'preview', { minWidth: D.previewMin }),
    ],
    ratio([filetree, D.previewMin]),
  )
  const leftBottom = split(
    'split_left_bottom',
    'row',
    [
      leaf('leaf_session_tree', 'session-tree', { minWidth: D.sessiontreeMin, maxWidth: D.sessiontreeMax }),
      leaf('leaf_session_chat', 'session-chat', { minWidth: D.sessionChatMin }),
    ],
    ratio([sessiontree, D.sessionChatMin]),
  )
  const leftCol = split('split_left', 'column', [leftTop, leftBottom], ratio([filetreeH, taskH]))
  const chatLeaf = leaf('leaf_chat', 'chat', { minWidth: D.chatMin, maxWidth: D.chatMax })
  // 左列宽 vs 对话宽：旧字段未记「左列总宽」→ 用左右两列各自的代表宽之和（尺度关系等价）
  const body = split(
    'split_body',
    'row',
    [leftCol, chatLeaf],
    ratio([Math.max(filetree + D.previewMin, sessiontree + D.sessionChatMin), chat]),
  )

  return {
    version: DOC_VERSION,
    layout: body,
    topToolbar: createBar(),
    statusbar: createBar(),
    toolbars: [],
    drawers: [],
  }
}

/** 适配层默认文档（无任何落盘配置时的布局：等价现有硬编码布局 + **ChonkPilot 默认栏 / 抽屉**）。 */
export function defaultLayoutDoc() {
  return {
    ...migrateLegacyDoc({}),
    topToolbar: chonkTopToolbar(),
    statusbar: chonkStatusbar(),
    toolbars: chonkToolbars(),
    drawers: chonkDrawers(),
  }
}

/* ── ChonkPilot 默认栏 / 抽屉（UILAYOUT-1 §7 P4①③）────────────────────────
   条目化后 `views/toolbar/Toolbar.vue`（961 行）/ `views/statusbar/StatusBar.vue`（235 行）
   的入口按 §3.4「栏条目化拆解」表逐项落成条目：
     · **配置按钮 / 菜单**：只引用**既有宿主主题**（裁决 1 的「发布 MQ 动作」`publish-mq`）；
     · **实时 / 条件 / 动态项**（搜索 / 最近项目 / Chrome 告警 / 登出 / 记忆 token / 队列 / 索引徽标 /
       语言切换）：做成 `kind:'view'` 的**栏视图条目**（裁决 2）——不新增 valueExpr/badgeKey 协议；
     · **窗口控件**（最小化 / 最大化 / 关闭）+ 品牌 + 拖拽区：容器外壳的**固定件，不进配置**（§4.2）。 */

/** 发布既有 MQ 主题的动作（裁决 1） */
function pmq(topic, payload) {
  return { type: 'publish-mq', topic, payload: payload ?? {} }
}

function barButton(id, zone, label, icon, command, { style = 'icon-text', tooltip = '' } = {}) {
  return { id, kind: 'button', zone, label, icon, style, tooltip, visible: true, command }
}

function barMenu(id, zone, label, icon, children, { style = 'text', tooltip = '' } = {}) {
  return { id, kind: 'menu', zone, label, icon, style, tooltip, visible: true, children }
}

function menuLeaf(id, label, icon, command) {
  return { id, label, icon, command }
}

function barView(id, zone, viewKey, { label = '', instanceKey = null, width = 0, height = 0, grow = false, tooltip = '' } = {}) {
  return { id, kind: 'view', zone, label, viewKey, instanceKey, width, height, grow, visible: true, tooltip }
}

/** 栏对象包装（与机制层 `createBar()` 同形：对象而非数组） */
function barWith(items) {
  return { background: '', zoneBackground: { left: '', center: '', right: '' }, items }
}

/** 顶部工具栏条目（§3.4 栏拆解表） */
export function chonkTopToolbar() {
  const settings = barMenu('tb_settings', 'right', '设置', 'setting',
    [
      menuLeaf('tb_set_llm', 'LLM 配置', 'magic-stick', pmq('preview-tab-open', { kind: 'settings-llm' })),
      menuLeaf('tb_set_mcp', 'MCP 配置', 'link', pmq('preview-tab-open', { kind: 'settings-mcp' })),
      menuLeaf('tb_set_async', '工具异步', 'lightning', pmq('preview-tab-open', { kind: 'settings-tool-async' })),
      menuLeaf('tb_set_sandbox', '工具沙箱', 'shield', pmq('preview-tab-open', { kind: 'settings-tool-sandbox' })),
      menuLeaf('tb_set_paths', '路径配置', 'tool', pmq('preview-tab-open', { kind: 'settings-paths' })),
      menuLeaf('tb_set_params', '参数配置', 'clock', pmq('preview-tab-open', { kind: 'settings-params' })),
      menuLeaf('tb_set_project', '项目配置', 'setting', pmq('preview-tab-open', { kind: 'settings-project' })),
    ])

  const theme = barMenu('tb_theme', 'right', '主题', 'magic-stick', [
    menuLeaf('tb_theme_light', '浅色', 'sunny', pmq('theme-select', { themeId: 'light' })),
    menuLeaf('tb_theme_nord', 'Nord', 'moon-night', pmq('theme-select', { themeId: 'nord' })),
    menuLeaf('tb_theme_dark', '深色', 'moon', pmq('theme-select', { themeId: 'dark' })),
  ])

  const lang = barMenu('tb_lang', 'right', '语言', 'language', [
    menuLeaf('tb_lang_zh', '简体中文', 'flag', pmq('lang-select', { lang: 'zh-CN' })),
    menuLeaf('tb_lang_en', 'English', 'flag', pmq('lang-select', { lang: 'en-US' })),
  ])

  return barWith([
    // 靠左区
    barButton('tb_open', 'left', '打开', 'folder-opened', pmq('workdir-open', {})),
    barView('tb_recent', 'left', 'recent-dirs'),
    // 中间区：全局搜索（栏视图条目，§3.4）
    barView('tb_search', 'center', 'search', { grow: true }),
    // 靠右区（条目顺序 = 从右到左的视觉顺序的反向：机制层靠右区右对齐）
    settings,
    barButton('tb_scenario', 'right', '场景', 'collection', pmq('scenario-open', {})),
    theme,
    lang,
    barButton('tb_sessions', 'right', '会话', 'message-box', pmq('sessions-open', {})),
    barButton('tb_drawer', 'right', '任务详情', 'panel-right', pmq(LfEventNames.drawerToggle, { drawerId: 'drawer_task' })),
    barButton('tb_editor', 'right', '布局设置', 'sliders-horizontal', pmq(LfEventNames.editorOpen, { tab: 'layout' })),
    barView('tb_chrome', 'right', 'chrome-warn'),
    barView('tb_logout', 'right', 'logout'),
  ])
}

/** 状态栏条目（§3.4 栏拆解表：实时项 → 栏视图条目，裁决 2；STB-1 长任务状态条目） */
export function chonkStatusbar() {
  return barWith([
    barView('sb_memory', 'left', 'memory-token'),
    // 长任务状态条目：记忆提取 / 上下文压缩（数据源 = tool-notify；点击 → preview 详情页）
    barView('sb_memory_status', 'left', 'memory-status'),
    barView('sb_compress_status', 'left', 'compress-status'),
    barView('sb_index', 'left', 'index-badge'),
    barButton('sb_devtools', 'right', 'DevTools', 'terminal', pmq('gui.devtools.open', {}), { style: 'icon' }),
    barView('sb_lang', 'right', 'lang-switcher'),
  ])
}

/** 具名自定义工具栏（挂到抽屉；默认文档非空以开放「自定义工具栏」页签） */
export function chonkToolbars() {
  return [
    {
      id: 'tb_task',
      label: '任务工具',
      background: '',
      zoneBackground: { left: '', center: '', right: '' },
      items: [
        barButton('tb_task_project', 'left', '项目配置', 'setting',
          pmq('preview-tab-open', { kind: 'settings-project' }), { style: 'icon' }),
      ],
    },
  ]
}

/** 默认抽屉（P4③：含至少一处抽屉示例，可由栏条目「任务详情」开 / 关） */
export function chonkDrawers() {
  return [
    {
      id: 'drawer_task',
      label: '任务详情',
      icon: 'clipboard-list',
      position: 'right',
      size: 380,
      modal: false,
      toolbarId: 'tb_task',
      toolbarPosition: 'bottom',
      tabs: [{ viewKey: 'task-detail', instanceKey: null, label: '任务详情' }],
    },
  ]
}

/** 解析 `layout.free`（字符串或对象）→ 规范化文档；非法 → null（调用方回落默认文档）。 */
export function parseFreeDoc(raw) {
  if (raw === undefined || raw === null || raw === '') return null
  let obj = raw
  if (typeof raw === 'string') {
    try {
      obj = JSON.parse(raw)
    } catch {
      return null
    }
  }
  if (!obj || typeof obj !== 'object' || !obj.layout) return null
  try {
    return normalize(cloneTree(obj))
  } catch {
    return null
  }
}

/**
 * 从 `gui.init-data` 的 `layout` 子对象里取自由布局文档。
 * @returns {object|null} 文档（已规范化）；无 `layout.free` / 解析失败 → null
 */
export function extractFreeDoc(layoutState) {
  if (!layoutState || typeof layoutState !== 'object') return null
  return parseFreeDoc(layoutState.free)
}

/** 文档 → `layout.free` 字符串（落库/beacon 用）。 */
export function toFreeString(doc) {
  return doc ? JSON.stringify(doc) : ''
}

/**
 * 把文档灌进布局域 store（live/draft/savedJson 三处同步，语义等价机制层 `init()` 读盘）。
 *
 * 直接写 store state（Pinia 允许）而非新增 store action：机制层（`layout-free/`）**不认识宿主**，
 * 宿主侧灌数据不应反向给机制层加「宿主默认文档」这种概念。
 */
export function applyDocToConfig(config, doc) {
  if (!config || !doc) return false
  const snapshot = normalize(cloneTree(doc))
  config.live = snapshot
  config.draft = cloneTree(snapshot)
  config.savedJson = JSON.stringify(snapshot, null, 2)
  config.dirty = false
  config.messages = []
  return true
}

/**
 * 持久化桥（注入宿主 IO）。
 * @param {{save?: (freeState: object) => Promise<any>, load?: () => Promise<object>}} io
 *   save  = `(state) => saveLayoutState(state)`（`gui.ui.save`，state 落 `layout.*`）
 *   load  = `() => loadInitData()`（`gui.init-data` 应答；可选，MainLayout 走预取时不使用）
 */
export function createLayoutPersist({ save, load } = {}) {
  return {
    /** 落库（§3.9）：只写 `layout.free` 子键；失败 → `{ok:false, error}`（不抛）。 */
    async save(doc) {
      if (typeof save !== 'function' || !doc) return { ok: true }
      try {
        await save({ free: toFreeString(doc) })
        return { ok: true }
      } catch (e) {
        return { ok: false, error: (e && e.message) || String(e) }
      }
    },

    /** 读取（§3.9）：`gui.init-data` → `layout.free` → 文档（无/非法 → null）。 */
    async load() {
      if (typeof load !== 'function') return null
      try {
        return extractFreeDoc(await load())
      } catch {
        return null
      }
    },

    /** 采用 init-data 结果里的文档（无 → false，调用方自行决定回落）。 */
    adopt(config, layoutState) {
      return applyDocToConfig(config, extractFreeDoc(layoutState))
    },

    /** 灌默认文档（`?layout=free` 首次进入、或 `layout.free` 非法时的回落文档，§6）。 */
    adoptDefault(config) {
      return applyDocToConfig(config, defaultLayoutDoc())
    },
  }
}
