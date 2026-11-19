/**
 * CK Store —— 前端全局 getter 仓库 + 状态注册表。
 *
 * 口径（PLUGIN-LOAD-1 D16；阶段1「前端状态注册 v1」，2026-11-19）：
 *   - **getter 仓库（既有，完全兼容）**：每窗口一份；组件 `onMounted` 注册
 *     `key → () => value`、`onBeforeUnmount` 注销（`register` 返回 disposer）；
 *     `get(key)` 统一按 Promise 返回，未注册 → `null`；
 *   - **状态注册表（新增）**：`create` 登记一个有明确 owner / scope / persist 的状态，
 *     `ref(key)` 返回**稳定、只读**的 Vue ref（同一 key 多次调用返回同一对象）。
 *     消费者只读；写入统一走 `set(key, value)`（owner 写，内部手动通知 listeners，
 *     **不用 Vue watch**）；
 *   - `get(key)` 的状态优先级：已注册状态 → 当前值（同步值包成 Promise）；否则走旧 getter。
 *     CkStoreView 等既有消费方无需改动；
 *   - **alias**：兼容 layout-free 旧 key（`project` / `sessionId` / `filePath` / `taskId`）。
 *     alias 不持有独立值，`ref/get/set/onChange` 都解引用到目标状态；
 *   - 重复 `create` / `alias` 保留先注册者并 `console.warn`（幂等返回已有 handle）；
 *   - `destroy` 幂等；既有只读 ref 不崩，保留 ref 并把值置 `undefined`；
 *   - 唯一消费方 = 插件 iframe（同源）经 `await window.top.ckStore.get(key)` 读取。
 *     **不要把 ref 跨 iframe 传递**：本 API 的 ref 只在宿主窗口 JS 上下文有效。
 */

import { computed, readonly, shallowRef } from 'vue'

/** getter key → { getter }。Map 保序：抽屉列表按注册顺序展示。 */
const entries = new Map()

/** 状态 key → entry。Map 保序：definitions() 按注册顺序返回。 */
const stateEntries = new Map()

/** persist='session' 的 localStorage 前缀。 */
const STATE_PERSIST_PREFIX = 'chonkpilot.state.'

/** 轻量 schema 允许的类型（不做复杂校验）。 */
const STATE_SCHEMA_TYPES = new Set(['string', 'number', 'boolean', 'object'])

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
 *
 * 状态优先：已注册状态（含 alias）→ 当前值；否则走旧 getter。
 * 都没有 → `null`；getter 抛错 / reject → Promise reject（由调用方展示错误）。
 *
 * @param {string} key
 * @returns {Promise<any>}
 */
export async function get(key) {
  const k = normalizeKey(key)
  if (stateEntries.has(k)) {
    const entry = resolveKey(k)
    return entry ? entry.ref.value : null
  }
  const entry = entries.get(k)
  if (!entry) {
    if (typeof console !== 'undefined' && typeof console.debug === 'function') {
      console.debug(`[ckStore] 未注册的 key: ${k || '(empty)'}`)
    }
    return null
  }
  return await entry.getter()
}

/** 当前窗口已注册的全部 getter key（注册顺序）。 */
export function listKeys() {
  return [...entries.keys()]
}

/** 当前窗口已注册的全部状态 key（含 alias；注册顺序）。 */
export function listStateKeys() {
  return [...stateEntries.keys()]
}

/* ── 状态注册表 ─────────────────────────────────────────────────────── */

/** key → 直接 entry（不解引用 alias）。 */
function directEntry(key) {
  return stateEntries.get(normalizeKey(key)) || null
}

/**
 * 解引用 alias（递归；检测环）。
 * @param {object|null} entry
 * @param {Set<string>} [seen]
 * @returns {object|null} 目标状态 entry（canonical）
 */
function resolveEntry(entry, seen) {
  if (!entry) return null
  if (!entry.aliasOf) return entry
  const visited = seen || new Set()
  if (visited.has(entry.key)) {
    console.warn(`[ckStore] alias 成环: ${entry.key} → ${entry.aliasOf}`)
    return null
  }
  visited.add(entry.key)
  return resolveEntry(stateEntries.get(entry.aliasOf) || null, visited)
}

function resolveKey(key) {
  return resolveEntry(directEntry(key))
}

/** 轻量类型检查：失败仅告警，调用方仍按原值写入。 */
function schemaOk(schema, value) {
  if (!schema || !STATE_SCHEMA_TYPES.has(schema)) return true
  if (value === null) return schema === 'object'
  return typeof value === schema
}

/** persist='session' 读取；返回 { found, value }。 */
function persistRead(key) {
  if (typeof localStorage === 'undefined') return { found: false }
  try {
    const raw = localStorage.getItem(STATE_PERSIST_PREFIX + key)
    if (raw === null) return { found: false }
    return { found: true, value: JSON.parse(raw) }
  } catch (e) {
    console.warn(`[ckStore] 读取持久化状态失败 key=${key}: ${(e && e.message) || e}`)
    return { found: false }
  }
}

/** persist='session' 写入；`undefined` = 清除。 */
function persistWrite(key, value) {
  if (typeof localStorage === 'undefined') return
  try {
    if (value === undefined) localStorage.removeItem(STATE_PERSIST_PREFIX + key)
    else localStorage.setItem(STATE_PERSIST_PREFIX + key, JSON.stringify(value))
  } catch (e) {
    console.warn(`[ckStore] 写入持久化状态失败 key=${key}: ${(e && e.message) || e}`)
  }
}

function persistRemove(key) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(STATE_PERSIST_PREFIX + key)
  } catch (_) {
    /* ignore */
  }
}

/** 通知 entry 的 listeners（由 set 显式触发；不使用 Vue watch）。 */
function notify(entry, value) {
  for (const cb of [...entry.listeners]) {
    try {
      cb(value, entry.key)
    } catch (e) {
      console.warn(`[ckStore] onChange 回调失败 key=${entry.key}: ${(e && e.message) || e}`)
    }
  }
}

/**
 * 写状态（alias 自动解引用到目标）。写后：持久化 → 通知 listeners。
 * @returns {boolean} 状态不存在 → false
 */
function writeEntry(entry, value) {
  const target = resolveEntry(entry)
  if (!target) return false
  if (!schemaOk(target.meta.schema, value)) {
    console.warn(
      `[ckStore] state key=${target.key} 值类型 ${typeof value} 不符合 schema=${target.meta.schema}（按原值写入）`,
    )
  }
  target.ref.value = value
  if (target.meta.persist === 'session') persistWrite(target.key, value)
  notify(target, value)
  return true
}

/** 生成 handle（稳定对象：重复获取同一 entry 返回同一 handle）。 */
function makeHandle(entry) {
  if (entry.handle) return entry.handle
  // handle.ref 也是可写 ref：直接 `.value = x` 走同一套 persist / listener 路径。
  const writableRef = computed({
    get() {
      const target = resolveEntry(entry)
      return target ? target.ref.value : undefined
    },
    set(value) {
      writeEntry(entry, value)
    },
  })
  const handle = {
    key: entry.key,
    ref: writableRef,
    get value() {
      const target = resolveEntry(entry)
      return target ? target.ref.value : undefined
    },
    set value(value) {
      writeEntry(entry, value)
    },
    set(value) {
      return writeEntry(entry, value)
    },
    get() {
      const target = resolveEntry(entry)
      return target ? target.ref.value : undefined
    },
    destroy() {
      return destroy(entry.key)
    },
  }
  entry.handle = handle
  return handle
}

/**
 * 注册一个状态。
 *
 * 两种调用形态：
 *   - `create('selected-session', { initial, ... })`
 *   - `create({ key: 'selected-session', initial, ... })`
 *
 * options：`{ initial, owner='host', scope='window', persist='none'|'session',
 *            label, description, defaultValue, schema }`。
 *   - `initial` = 初始值（缺省回落 `defaultValue`）；`persist='session'` 时优先读 localStorage；
 *   - `defaultValue` = 元数据默认值；`schema` = `'string' | 'number' | 'boolean' | 'object'`（轻量检查）。
 *
 * 重复 create：**不覆盖先注册者**，`console.warn` 后返回已有 handle。
 *
 * @returns {{key:string, ref:import('vue').WritableComputedRef, value:any,
 *            set:(v:any)=>boolean, get:()=>any, destroy:()=>boolean}}
 */
export function create(keyOrOptions, maybeOptions) {
  let key
  let options
  if (keyOrOptions && typeof keyOrOptions === 'object') {
    options = { ...keyOrOptions }
    key = options.key
    delete options.key
  } else {
    key = keyOrOptions
    options = maybeOptions ? { ...maybeOptions } : {}
  }

  const k = normalizeKey(key)
  if (!k) return null
  if (stateEntries.has(k)) {
    console.warn(`[ckStore] 重复注册 state key=${k}（保留先注册者）`)
    return makeHandle(stateEntries.get(k))
  }

  const persist = options.persist === 'session' ? 'session' : 'none'
  const defaultValue =
    options.defaultValue !== undefined
      ? options.defaultValue
      : options.initial !== undefined
        ? options.initial
        : undefined
  let initial = options.initial !== undefined ? options.initial : defaultValue
  if (persist === 'session') {
    const saved = persistRead(k)
    if (saved.found) initial = saved.value
  }

  const entry = {
    key: k,
    aliasOf: null,
    ref: shallowRef(initial),
    readonlyRef: null,
    listeners: new Set(),
    handle: null,
    meta: {
      label: options.label,
      description: options.description,
      defaultValue,
      scope: options.scope || 'window',
      owner: options.owner || 'host',
      persist,
      schema: options.schema,
      hidden: options.hidden === true,
    },
  }
  stateEntries.set(k, entry)
  return makeHandle(entry)
}

/**
 * 定义兼容 alias：`alias('sessionId', 'selected-session')`。
 * alias 不持有独立值 —— `ref/getSync/set/onChange` 都解引用到目标状态。
 *
 * @param {string} aliasKey
 * @param {string} targetKey
 * @param {{label?:string, description?:string, hidden?:boolean}} [options]
 * @returns {boolean} 是否新建（已存在 / 非法 → false，且已存在时告警）
 */
export function alias(aliasKey, targetKey, options = {}) {
  const k = normalizeKey(aliasKey)
  const target = normalizeKey(targetKey)
  if (!k || !target || k === target) return false
  if (stateEntries.has(k)) {
    console.warn(`[ckStore] 重复定义 alias key=${k}（保留先注册者）`)
    return false
  }
  stateEntries.set(k, {
    key: k,
    aliasOf: target,
    ref: null,
    readonlyRef: null,
    listeners: null,
    handle: null,
    meta: {
      label: options.label,
      description: options.description,
      defaultValue: options.defaultValue,
      scope: options.scope || 'window',
      owner: options.owner || 'host',
      persist: 'none',
      schema: options.schema,
      hidden: options.hidden === true,
    },
  })
  const entry = stateEntries.get(k)
  makeHandle(entry)
  return true
}

/**
 * 返回稳定、**只读**的 Vue ref（同一 key 多次调用返回同一对象）。
 * alias 返回目标的同一个只读 ref；未注册 → `null`。
 */
export function ref(key) {
  const entry = resolveKey(key)
  if (!entry) return null
  if (!entry.readonlyRef) entry.readonlyRef = readonly(entry.ref)
  return entry.readonlyRef
}

/** 同步取状态值；key 不是已注册状态（旧 getter / 未注册）→ `undefined`。 */
export function getSync(key) {
  if (!directEntry(key)) return undefined
  const entry = resolveKey(key)
  return entry ? entry.ref.value : undefined
}

/**
 * 写状态（不存在 → false）。写后：持久化（persist='session'）→ 通知 listeners。
 * alias 解引用到目标。
 */
export function set(key, value) {
  const entry = directEntry(key)
  if (!entry) return false
  return writeEntry(entry, value)
}

/** 删除状态（幂等）。既有只读 ref 保留，值置 `undefined`。 */
export function destroy(key) {
  const k = normalizeKey(key)
  const entry = stateEntries.get(k)
  if (!entry) return false
  stateEntries.delete(k)
  if (!entry.aliasOf) {
    // canonical 删除后，指向它的 alias 也一并删除，避免留下永久悬空 key。
    for (const [aliasKey, aliasEntry] of stateEntries) {
      if (aliasEntry.aliasOf === k) stateEntries.delete(aliasKey)
    }
    entry.ref.value = undefined
    persistRemove(k)
    entry.listeners.clear()
  }
  return true
}

/**
 * 状态定义快照（含 alias；按注册顺序）。
 * alias 定义带 `aliasOf`；`hidden:true` 的条目供 layout-free 下拉过滤，
 * 但 `ref(aliasKey)` 仍可用。
 *
 * @returns {Array<{key:string,label?:string,description?:string,defaultValue?:any,
 *                  scope:string,owner:string,persist:string,aliasOf?:string,hidden?:boolean}>}
 */
export function definitions() {
  return [...stateEntries.values()].map((entry) => {
    const def = {
      key: entry.key,
      label: entry.meta.label,
      description: entry.meta.description,
      defaultValue: entry.meta.defaultValue,
      scope: entry.meta.scope,
      owner: entry.meta.owner,
      persist: entry.meta.persist,
    }
    if (entry.aliasOf) def.aliasOf = entry.aliasOf
    if (entry.meta.hidden) def.hidden = true
    return def
  })
}

/**
 * 订阅状态变化（只由 `set` 触发，不使用 Vue watch）。
 * alias 订阅目标状态。未注册 → 返回空的退订函数。
 *
 * @param {string} key
 * @param {(value:any, key:string)=>void} cb
 * @returns {() => void} unsubscribe
 */
export function onChange(key, cb) {
  const entry = resolveKey(key)
  if (!entry || typeof cb !== 'function') return () => {}
  entry.listeners.add(cb)
  return () => {
    entry.listeners.delete(cb)
  }
}

/** 取消单个 onChange 回调（幂等）。 */
export function offChange(key, cb) {
  const entry = resolveKey(key)
  if (entry) entry.listeners.delete(cb)
}

/** CK Store 单例（宿主代码与 iframe 共用的同一份）。 */
export const ckStore = {
  register,
  unregister,
  get,
  listKeys,
  create,
  set,
  getSync,
  ref,
  destroy,
  alias,
  definitions,
  listStateKeys,
  onChange,
  offChange,
}

/** 在宿主窗口自挂 `window.ckStore`（`main.js` 装配时调用一次；幂等）。 */
export function installGlobalCkStore(store = ckStore) {
  if (typeof window === 'undefined') return null
  if (!window.ckStore) window.ckStore = store
  return window.ckStore
}