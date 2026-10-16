/**
 * 配置的**规范化 / 栏 key 解析 / 引用统计**（纯逻辑，不含 store 状态）。
 *
 * 从 stores/config.js 抽出来的原因：这些函数是「输入 → 输出」的纯函数，
 * 在 store 里和 1200 行的 actions 混在一起时很难单测、也很难看清数据形状。
 * store 只负责持有 live / draft 与调度，形状规则全部集中在这里。
 */
import { uid } from '../utils/layout'
import { getView } from '../config/views'
import { defaultConfig } from '../config/defaultConfig'
import { walk, isLeaf } from '../utils/layout'
import { regionKind, upgradeLeaf, TOOLBAR_POSITIONS } from '../utils/regions'

export const STORAGE_KEY = 'layout-free:config:v1'

/** 栏条目的三个区 */
export const BAR_ZONES = ['left', 'center', 'right']

/** 配置版本：6 = 栏统一成对象结构（background / zoneBackground / items）+ 背景色 */
export const CONFIG_VERSION = 6

/** 自定义工具栏在 barKey 里的前缀 */
export function customBarKey(id) {
  return `custom:${id}`
}

/** 旧写法 'toolbar' → 'topToolbar' */
export function migrateBarKey(key) {
  return key === 'toolbar' ? 'topToolbar' : key
}

/** 取某个栏的完整对象：{ background, zoneBackground, items } */
export function resolveBarObject(config, barKey) {
  const key = migrateBarKey(barKey)
  if (!config) return { background: '', zoneBackground: { left: '', center: '', right: '' }, items: [] }
  if (key === 'topToolbar') return config.topToolbar ?? { background: '', zoneBackground: {}, items: [] }
  if (key === 'statusbar') return config.statusbar ?? { background: '', zoneBackground: {}, items: [] }
  if (typeof key === 'string' && key.startsWith('custom:')) {
    return config.toolbars?.find((t) => t.id === key.slice(7)) ?? { background: '', zoneBackground: {}, items: [] }
  }
  return { background: '', zoneBackground: {}, items: [] }
}

/** 在配置里按 barKey 取条目数组（找不到返回空数组） */
export function resolveBarArray(config, barKey) {
  const obj = resolveBarObject(config, barKey)
  return Array.isArray(obj?.items) ? obj.items : []
}

/** 栏显示名 */
export function barLabelOf(config, barKey) {
  const key = migrateBarKey(barKey)
  if (key === 'topToolbar') return '顶部工具栏'
  if (key === 'statusbar') return '状态栏'
  const t = config?.toolbars?.find((x) => x.id === key.slice(7))
  return t ? `工具栏「${t.label}」` : '工具栏'
}

/** 三个区的背景色：只保留非空字符串，缺省为「跟随主题」 */
function normZoneBg(raw) {
  const out = { left: '', center: '', right: '' }
  if (raw && typeof raw === 'object') {
    for (const z of BAR_ZONES) {
      if (typeof raw[z] === 'string') out[z] = raw[z]
    }
  }
  return out
}

/** 背景色字段：只接受字符串（'' = 跟随主题） */
function normBg(v) {
  return typeof v === 'string' ? v : ''
}

/** 把「可能是数组（旧结构）」的栏统一成对象结构 */
function asBar(raw) {
  if (Array.isArray(raw)) {
    return { background: '', zoneBackground: normZoneBg(null), items: raw }
  }
  if (raw && typeof raw === 'object') {
    return {
      background: normBg(raw.background),
      zoneBackground: normZoneBg(raw.zoneBackground),
      items: Array.isArray(raw.items) ? raw.items : [],
    }
  }
  return { background: '', zoneBackground: normZoneBg(null), items: [] }
}

/**
 * 收集某个自定义工具栏的引用位置（显示名数组）。
 *
 * ⚠️ 必须同时扫「区域（leaf）」「容器（split）」「抽屉」三种挂载点：
 * 只扫叶子会漏掉容器，于是 removeToolbar 认为没人引用 → 允许删除 →
 * 容器上留下悬空 toolbarId（要到下次 normalize 才被清掉）。
 * getter `toolbarUsage` 与 action `removeToolbar` 共用这一份实现，避免两处判断分叉。
 */
export function collectToolbarRefs(cfg, id) {
  const out = []
  if (!cfg || !id) return out
  walk(cfg.layout, (n) => {
    if (n.toolbar?.toolbarId !== id) return
    out.push(n.type === 'leaf' ? `区域「${n.title || regionKind(n)}」` : `容器「${n.id}」`)
  })
  ;(cfg.drawers ?? []).forEach((d) => {
    if (d.toolbarId === id) out.push(`抽屉「${d.label}」`)
  })
  return out
}

/**
 * 规范化配置：
 * - 栏条目补齐 kind / zone；
 * - 每个区域升级为 kind 结构（tabs 挂在区域上），补齐尺寸约束与背景色；
 * - 旧字段 toolbar → topToolbar（顶部工具栏），自定义工具栏列表补齐；
 * - 区域 / 容器 / 抽屉引用的自定义工具栏做存在性校验，悬空引用清空；
 * - 背景色：顶部工具栏 / 状态栏（含每区）/ 自定义工具栏（含每区）/ 容器 / 区域。
 *
 * ⚠️ 会**原地修改** `config.layout`（`walk` 直接改 node）并复用其中的节点对象。
 * 调用方如果要保留原对象，必须先 `cloneTree` 再传进来（store 里的 save/init 都是这么做的）。
 */
export function normalize(config) {
  const base = defaultConfig()
  if (!config || typeof config !== 'object' || !config.layout) return base

  const bar = (list) =>
    (Array.isArray(list) ? list : []).map((it) => {
      const kind = it.kind === 'menu' || it.kind === 'view' ? it.kind : 'button'
      const out = {
        ...it,
        kind,
        zone: BAR_ZONES.includes(it.zone) ? it.zone : 'left',
      }
      if (kind === 'menu') {
        if (!Array.isArray(out.children)) out.children = []
        if (!out.style) out.style = 'text'
        if (!out.icon) out.icon = 'menu'
      } else if (kind === 'view') {
        out.children = undefined
        out.command = undefined
        const w = Number(out.width)
        const h = Number(out.height)
        const def = getView(out.viewKey)
        out.width = Number.isFinite(w) && w > 0 ? w : (def?.defaultWidth ?? 260)
        out.height = Number.isFinite(h) && h > 0 ? h : (def?.defaultHeight ?? 120)
        out.grow = out.grow === true
      } else {
        out.children = undefined
        if (!out.style) out.style = 'icon-text'
      }
      return out
    })

  const toolbars = (Array.isArray(config.toolbars) ? config.toolbars : []).map((t) => {
    const b = asBar(t)
    return {
      id: t.id || uid('toolbar'),
      label: t.label || '自定义工具栏',
      background: b.background,
      zoneBackground: b.zoneBackground,
      items: bar(b.items),
    }
  })
  const toolbarIds = new Set(toolbars.map((t) => t.id))
  const topBar = asBar(config.topToolbar ?? config.toolbar)
  const statusBar = asBar(config.statusbar)

  const layout = config.layout
  walk(layout, (node) => {
    // 容器（split）与区域都能设背景色
    node.background = normBg(node.background)
    // 容器（split）也可以挂自定义工具栏
    if (!isLeaf(node)) {
      const tb = node.toolbar
      if (tb && !toolbarIds.has(tb.toolbarId)) node.toolbar = null
      else if (tb && !TOOLBAR_POSITIONS.some((p) => p.value === tb.position)) tb.position = 'top'
      return
    }
    upgradeLeaf(node)
    // 尺寸约束：只保留合法正数
    for (const k of ['minWidth', 'maxWidth', 'minHeight', 'maxHeight']) {
      const n = Number(node[k])
      if (!Number.isFinite(n) || n <= 0) delete node[k]
    }
    // 区域自定义工具栏：位置合法 + 引用存在
    const tb = node.toolbar
    if (tb && !toolbarIds.has(tb.toolbarId)) node.toolbar = null
    else if (tb && !TOOLBAR_POSITIONS.some((p) => p.value === tb.position)) tb.position = 'top'
  })

  const drawers = (Array.isArray(config.drawers) ? config.drawers : []).map((d) => ({
    ...d,
    toolbarId: toolbarIds.has(d.toolbarId) ? d.toolbarId : null,
    toolbarPosition: TOOLBAR_POSITIONS.some((p) => p.value === d.toolbarPosition) ? d.toolbarPosition : 'top',
  }))

  return {
    version: config.version ?? CONFIG_VERSION,
    layout,
    topToolbar: { ...topBar, items: bar(topBar.items) },
    statusbar: { ...statusBar, items: bar(statusBar.items) },
    toolbars,
    drawers,
  }
}

/** 从 localStorage 读配置（已规范化）；读不到或坏了返回 null */
export function readStorage() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    return normalize(JSON.parse(raw))
  } catch {
    return null
  }
}
