/**
 * CK Store —— 前端全局 getter 仓库（PLUGIN-LOAD-1 D16）。
 *
 * 口径（设计稿 §3.5，2026-11-02 定稿）：
 *   - **每窗口一份**：组件状态本身是窗口内状态；`installGlobalCkStore()` 在 `main.js` 装配一次；
 *   - 组件 `onMounted` 注册 `key → () => value`、`onBeforeUnmount` 注销（`register` 返回 disposer）；
 *   - 只提供 `get`：**统一按 Promise 返回**；未注册 → `null`（不抛错，只记一条 debug 日志）；
 *   - **不做订阅 / 变更通知**：需要“变化”的场景走既有 MQ 事件；
 *   - 唯一消费方 = 插件 iframe（同源）：`await window.top.ckStore.get(key)`；
 *     插件**进程**不支持（需要上下文时由前端随消息携带，D18）；
 *   - 无权限白名单、无 key 登记表；重复注册保留先注册者并告警。
 */

/** key → { getter }。Map 保序：抽屉列表按注册顺序展示。 */
const entries = new Map()

/** 规范化 key（非字符串 / 空白 → ''）。 */
function normalizeKey(key) {
  return typeof key === 'string' ? key.trim() : ''
}

/**
 * 注册一个 getter。
 * @param {string} key 自由命名（如 `chat.currentSession`）
 * @param {() => any} getter 取值函数（可返回 Promise）
 * @returns {() => void} 注销函数（幂等；仅当仍是本次注册的那一条时才删除）
 */
export function register(key, getter) {
  const k = normalizeKey(key)
  if (!k || typeof getter !== 'function') return () => {}
  if (entries.has(k)) {
    console.warn(`[ckStore] 重复注册 key=${k}（保留先注册者）`)
    return () => {}
  }
  const entry = { getter }
  entries.set(k, entry)
  return () => {
    if (entries.get(k) === entry) entries.delete(k)
  }
}

/** 注销一个 key（缺省/未注册 → 无动作）。 */
export function unregister(key) {
  const k = normalizeKey(key)
  if (k) entries.delete(k)
}

/**
 * 读取一个 key 的当前值（统一 Promise）。
 * 未注册 → `null`；getter 抛错 / reject → Promise reject（由调用方展示错误）。
 * @param {string} key
 * @returns {Promise<any>}
 */
export async function get(key) {
  const k = normalizeKey(key)
  const entry = entries.get(k)
  if (!entry) {
    if (typeof console !== 'undefined' && typeof console.debug === 'function') {
      console.debug(`[ckStore] 未注册的 key: ${k || '(empty)'}`)
    }
    return null
  }
  return await entry.getter()
}

/** 当前窗口已注册的全部 key（注册顺序）。 */
export function listKeys() {
  return [...entries.keys()]
}

/** CK Store 单例（宿主代码与 iframe 共用的同一份）。 */
export const ckStore = { register, unregister, get, listKeys }

/** 在宿主窗口自挂 `window.ckStore`（`main.js` 装配时调用一次；幂等）。 */
export function installGlobalCkStore(store = ckStore) {
  if (typeof window === 'undefined') return null
  if (!window.ckStore) window.ckStore = store
  return window.ckStore
}
