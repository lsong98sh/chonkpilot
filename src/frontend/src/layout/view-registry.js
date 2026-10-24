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
 * 幂等：同 id 先卸载再注册（插件重连 / 配置变更）。
 *
 * @param {object} plugin 插件描述（`plugin-registered` 事件 / `plugin-list` 快照）
 * @param {(ctx: {plugin: object, view: object, entry: string}) => any} makeComponent
 *        组件工厂（宿主在 `views.registry.js` 注入 `PluginPageHost` 适配壳；测试可注入桩）
 */
export function registerPluginViews(plugin, makeComponent) {
  if (!plugin || !plugin.id || !Array.isArray(plugin.views) || !plugin.views.length) return
  unregisterPluginViews(plugin.id)
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
