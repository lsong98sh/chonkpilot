/**
 * 视图注册表（UIREG-1）：**唯一注册面**——内置视图与插件视图共用同一条注册 / 解析路径。
 *
 * 分层（UILAYOUT-1 §2 / UIREG-1 §目标 1）：
 *   - `view-definitions.js`：**元数据**（key / label / icon / host / designable …），零组件；
 *   - 本文件：**组件解析**的唯一路径（`registerViews` 写入 / `resolve` 读出），
 *     不再存在「静态组件映射表 + 动态组件表」两条路；
 *   - `views.registry.js`：宿主装配点，注册内置组件 + 把插件视图接到同一条 API。
 *
 * 本模块为**纯模块**（零 `.vue` / 零 `vue` import）→ node:test 可直接 import 断言注册面。
 */
import { DYNAMIC_DEFINITIONS, VIEW_DEFINITIONS } from './view-definitions.js'

/** key → { key, component, source }（Map 保序：内置先注册，插件后追加）。 */
const views = new Map()

/** 插件 id → 最近一次注册的**结构签名**（view 集合 / entry / 标题 / 版本），用于幂等跳过重建。 */
const pluginSigs = new Map()

/**
 * 插件视图结构签名：只要它不变，就复用既有组件对象。
 *
 * 为什么必须幂等（PLUGIN-UI-2 回归）：宿主每次 `publishRegistered`（spawn / MCP 注册 / UI 注册 /
 * 状态上报 `running`）都会广播 `plugin-registered`，前端 `upsert` 再调本函数。若每次重建组件
 * 对象，视图池里 `<component :is>` 的类型就变 → **卸载重建宿主页面组件**（`PluginPageHost`），
 * iframe 的导航被反复取消/重来；首帧 3s 内不 load 即判「加载超时」→ 状态栏 badge 永久停在占位
 * （L4 全量里稳定复现）。
 */
function pluginViewSig(plugin) {
  return JSON.stringify({
    v: plugin.version || '',
    n: plugin.name || '',
    i: plugin.icon || '',
    views: (plugin.views || []).map((v) => ({
      id: (v && v.id) || '',
      entry: (v && v.entry) || '',
      title: (v && v.title) || '',
      icon: (v && v.icon) || '',
      containers: (v && Array.isArray(v.containers)) ? v.containers : [],
    })),
  })
}

/** 允许 `[{ key, component }]` 或 `{ key: component }` 两种入参形态。 */
function normalizeEntries(entries) {
  if (!entries) return []
  if (Array.isArray(entries)) return entries
  if (typeof entries === 'object') {
    return Object.entries(entries).map(([key, component]) => ({ key, component }))
  }
  return []
}

/**
 * 注册视图（**唯一入口**；内置 / 插件同一 API）。
 *
 * 重复 key 显式告警且**不互相覆盖**：不同来源注册同一 key → `console.warn` 并保留先注册者
 * （内置与插件视图各自命名空间，正常情况下不会撞车；撞车即装配错误，必须暴露）。
 *
 * @param {Array<{key: string, component: any}>|Record<string, any>} entries
 * @param {string} [source] 来源标识（`builtin` / `plugin:<id>`）
 * @returns {string[]} 本次真正注册成功的 key
 */
export function registerViews(entries, source = 'builtin') {
  const added = []
  for (const entry of normalizeEntries(entries)) {
    if (!entry || !entry.key || !entry.component) continue
    const prev = views.get(entry.key)
    if (prev && prev.source !== source) {
      console.warn(
        `[view-registry] 视图 key 重复注册：${entry.key}（已注册来源=${prev.source}，本次来源=${source}，保留先注册者）`,
      )
      continue
    }
    views.set(entry.key, { key: entry.key, component: entry.component, source })
    added.push(entry.key)
  }
  return added
}

/** 按来源卸载（插件禁用 / 卸载）。 */
export function unregisterViewsBySource(source) {
  if (!source) return
  for (const [key, entry] of views) {
    if (entry.source === source) views.delete(key)
  }
}

/** 已注册的 viewKey（注册顺序 = 内置定义顺序 + 插件注册顺序）。 */
export function registeredViewKeys() {
  return [...views.keys()]
}

/**
 * 注册/覆盖一个插件的全部 iframe view（`plugin:<id>:<view>`）。
 * **幂等**：结构签名（view 集合 / entry / 标题 / 图标 / 版本）未变 → 保留既有组件对象（不重建，
 * 避免视图池里的插件 iframe 被反复重挂）；结构确有变化（插件升级 / 配置变更）或插件重连 → 先卸载
 * 再注册。
 *
 * @param {object} plugin 插件描述（`plugin-registered` 事件 / `plugin-list` 快照）
 * @param {(ctx: {plugin: object, view: object, entry: string}) => any} makeComponent
 *        组件工厂（宿主在 `views.registry.js` 注入 `PluginPageHost` 适配壳；测试可注入桩）
 */
export function registerPluginViews(plugin, makeComponent) {
  if (!plugin || !plugin.id || !Array.isArray(plugin.views) || !plugin.views.length) return
  const sig = pluginViewSig(plugin)
  if (pluginSigs.get(plugin.id) === sig) return
  unregisterPluginViews(plugin.id)
  pluginSigs.set(plugin.id, sig)
  const entries = []
  for (const v of plugin.views) {
    if (!v || !v.id) continue
    const entry = v.entry || `webui/${v.id}.html`
    const key = `plugin:${plugin.id}:${v.id}`
    const containers = Array.isArray(v.containers) && v.containers.length
      ? v.containers
      : ['preview', 'tab', 'drawer']
    DYNAMIC_DEFINITIONS.push({
      key,
      label: v.title || `${plugin.name || plugin.id} · ${v.id}`,
      icon: v.icon || plugin.icon || '',
      mode: 'multi',
      host: 'layout',
      designable: false,
      containers,
      title: v.title || '',
      plugin: { id: plugin.id, view: v.id, entry },
    })
    entries.push({
      key,
      component: typeof makeComponent === 'function' ? makeComponent({ plugin, view: v, entry }) : null,
    })
  }
  registerViews(entries, `plugin:${plugin.id}`)
}

/** 卸载插件全部动态 view（插件禁用 / 卸载）。 */
export function unregisterPluginViews(id) {
  if (!id) return
  pluginSigs.delete(id)
  for (let i = DYNAMIC_DEFINITIONS.length - 1; i >= 0; i--) {
    if (DYNAMIC_DEFINITIONS[i]?.plugin?.id === id) DYNAMIC_DEFINITIONS.splice(i, 1)
  }
  unregisterViewsBySource(`plugin:${id}`)
}

/**
 * 组装注册表对象（注入 `createLayoutEngine({ views })`）。
 *
 * `definitions` 用 **getter**：每次读取按当前语言解析 `label`（语言切换即时跟随）；
 * 定义 = 静态元数据（内置）+ 动态定义（插件），组件解析只经本模块的注册表。
 *
 * @param {(key: string) => string} [translate] 宿主 i18n 取值函数（`layout/i18n.js` 提供）
 */
export function createViewRegistry(translate) {
  const t = typeof translate === 'function' ? translate : (key) => key
  return {
    get definitions() {
      return [...VIEW_DEFINITIONS, ...DYNAMIC_DEFINITIONS].map((d) =>
        d.labelKey ? { ...d, label: t(d.labelKey) } : d,
      )
    },
    resolve(viewKey) {
      return views.get(viewKey)?.component || null
    },
  }
}
