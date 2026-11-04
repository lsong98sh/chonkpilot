/**
 * 插件平台前端状态（PLUGIN-1）。
 *
 * 数据源 = `plugin-list` 快照 + `plugin-registered/unregistered/status` 事件链；
 * 零轮询、零 watch（项目规则：状态用 composable 封装，组件只读派生值）。
 */
import { reactive } from 'vue'
import { pluginHost } from './host.js'

// 宿主端口适配（保持包内调用形态不变）：mq / 视图注册表均由宿主注入。
const mq = {
  on: (...args) => pluginHost().mq.on(...args),
  emit: (...args) => pluginHost().mq.emit(...args),
}
const registerPluginViews = (plugin) => pluginHost().views.register(plugin)
const unregisterPluginViews = (id) => pluginHost().views.unregister(id)

const state = reactive({
  plugins: [],
  ready: false,
  error: '',
})

let started = false

/* 插件清单 / 状态变更订阅（`subscribePluginChange`）：**事件驱动**，供消费方（如插件页
 * 就绪态）在状态转 `running` 时行动 —— 组件侧因此不需要 `watch` store（项目规则）。 */
const changeSubscribers = new Set()

function notifyChange(id) {
  for (const cb of Array.from(changeSubscribers)) {
    try {
      cb(id)
    } catch (e) {
      console.warn('[plugins] 变更订阅回调异常:', e)
    }
  }
}

function upsert(plugin) {
  if (!plugin || !plugin.id) return
  const idx = state.plugins.findIndex((p) => p.id === plugin.id)
  if (idx >= 0) state.plugins.splice(idx, 1, plugin)
  else state.plugins.push(plugin)
  state.plugins.sort((a, b) => String(a.id).localeCompare(String(b.id)))
  registerPluginViews(plugin)
  notifyChange(plugin.id)
}

function patchStatus({ id, status, detail }) {
  if (!id) return
  const p = state.plugins.find((x) => x.id === id)
  if (p) {
    p.status = status
    if (detail !== undefined) p.detail = detail
  }
  notifyChange(id)
}

export function initPlugins() {
  if (started) return
  started = true
  mq.on('plugin-registered', (ev) => {
    upsert(ev?.plugin)
  })
  mq.on('plugin-unregistered', (ev) => {
    const id = ev?.id
    if (!id) return
    state.plugins = state.plugins.filter((p) => p.id !== id)
    unregisterPluginViews(id)
    notifyChange(id)
  })
  mq.on('plugin-status', (ev) => {
    patchStatus(ev || {})
  })
  refreshPlugins()
}

export async function refreshPlugins(retry = 5) {
  try {
    const res = await mq.emit('plugin-list', {})
    const env = res?.backend
    if (!env || env.ok === false) {
      throw new Error((env?.errors || ['plugin-list failed']).join('; '))
    }
    const list = env.result?.plugins
    if (Array.isArray(list)) applySnapshot(list)
    state.ready = true
    state.error = ''
  } catch (e) {
    state.error = e?.message || String(e)
    // 后端（server/persist）可能尚未就绪：短退避重试，就绪后自然收敛。
    if (retry > 0) setTimeout(() => refreshPlugins(retry - 1), 800)
  }
}

/** 用全量快照替换本地台账，并同步视图注册表（plugin-list / plugin-refresh 共用）。 */
function applySnapshot(list) {
  state.plugins = list
  for (const p of list) {
    registerPluginViews(p)
    notifyChange(p.id)
  }
}

let refreshing = null

/**
 * 硬刷新（PLUGIN-LOAD-1 D2）：重扫插件根 + 差量收敛 + 拉起新插件，返回全量快照。
 *
 * 触发点 = 打开布局编辑框 / 新建布局对话框之前；并发调用复用同一在飞请求（单飞）。
 * 失败 → **抛出**（D4：调用方关闭对话框并提示，不带着未收敛的插件清单继续编辑）。
 */
export function refreshPluginsHard() {
  if (refreshing) return refreshing
  refreshing = (async () => {
    try {
      const res = await mq.emit('plugin-refresh', {})
      const env = res?.backend
      if (!env || env.ok === false) {
        throw new Error((env?.errors || ['plugin-refresh failed']).join('; '))
      }
      const list = env.result?.plugins
      if (Array.isArray(list)) applySnapshot(list)
      state.ready = true
      state.error = ''
      return list || []
    } finally {
      refreshing = null
    }
  })()
  return refreshing
}

/**
 * 订阅插件清单 / 状态变更（回调收到变更的插件 id；读最新值仍走 `pluginById`）。
 * 返回退订函数。**事件驱动**（零 watch / 零轮询），供插件页就绪态等消费方使用。
 */
export function subscribePluginChange(cb) {
  if (typeof cb !== 'function') return () => {}
  changeSubscribers.add(cb)
  return () => {
    changeSubscribers.delete(cb)
  }
}

export function pluginById(id) {
  return state.plugins.find((p) => p.id === id) || null
}

/** 首份台账快照是否已到位（用于区分「还没加载」与「未安装」，D11）。 */
export function pluginsReady() {
  return state.ready
}

export function pluginView(id, viewId) {
  const p = pluginById(id)
  if (!p) return null
  return (p.views || []).find((v) => v.id === viewId) || null
}

export function usePlugins() {
  return { state, refresh: refreshPlugins, pluginById, pluginView }
}
