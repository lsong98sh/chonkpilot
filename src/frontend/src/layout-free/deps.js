/**
 * 机制层依赖容器（UILAYOUT-1 §2 分层职责 / §3.2 大组件 API）。
 *
 * 机制层（`layout-free/`）**不认识任何一个 ChonkPilot 区块**：宿主经
 * `createLayoutEngine({...})` 注入 mq / views / persist / i18n / icons / dialog，
 * 深层模块（`<LfIcon>` 注册、`LfIconPicker`、`stores/mqCommands`、`utils/commands`）
 * 一律从这里取值，不直接 import 宿主代码。
 *
 * 缺省（未注入）时的行为：
 *   - `mq`      → 内置 standalone mq（`utils/mq.js`，纯前端本地分发）；
 *   - 其它      → 空实现（恒等 i18n / 无图标 / 无 view），**不报错、不静默改行为**。
 *
 * 注意：本模块**不 import** `config/*`，避免与 `config-normalize → config/views → deps`
 * 形成环。
 */
import standaloneMq from './utils/mq.js'

const state = {
  /** 宿主 mq（emit/on/off）；缺省 = 内置 standalone */
  mq: null,
  /** { definitions: [{key,label,icon,mode,host,designable}], resolve(key) | components: {key: Component} } */
  views: null,
  /** { load(): Promise<doc|null>, save(doc): Promise<void> }；本阶段（P0/P1）仅持有，落库接线见 §7 P3 */
  persist: null,
  /** t(key, params)；缺省恒等（返回 key） */
  i18n: null,
  /** (name) => svgString | null —— 接管图标取值（§3.2） */
  iconResolver: null,
  /** 图标名清单（供图标选择器分组；来自宿主既有 icons.js） */
  iconNames: [],
  /**
   * 去重注入（§3.10）：复用宿主既有实现，避免同 bundle 两份同名组件。
   *   Icon        → 注册为全局组件 `LfIcon`
   *   SplitPanel  → 注册为全局组件 `LfSplitPanel`（栏三区用）
   */
  components: {},
  /** 宿主全局对话框服务（既有 `components/dialog` 的 `dialog` 单例） */
  dialog: null,
}

/** 注入依赖（只覆盖显式传入的非空项；`components` 做浅合并）。 */
export function setLayoutDeps(deps = {}) {
  for (const [key, value] of Object.entries(deps)) {
    if (value === undefined) continue
    if (key === 'components' && value && typeof value === 'object') {
      state.components = { ...state.components, ...value }
      continue
    }
    state[key] = value
  }
  return state
}

export function getLayoutDeps() {
  return state
}

/** 宿主 mq，缺省回落到内置 standalone mq。 */
export function getMq() {
  return state.mq || standaloneMq
}

/** 宿主全局对话框服务（未注入 → null，调用方自行降级）。 */
export function getDialog() {
  return state.dialog
}

/**
 * 落库布局文档（UILAYOUT-1 §3.9 / §7 P3③）：把当前文档交给宿主注入的 `persist.save`。
 *
 * 机制层**不认识**宿主存储面（`gui.ui.save` → prj 配置 `layout.free`），只负责"在配置真正
 * 生效后把文档交出去"。返回值：
 *   - 未注入 persist → `null`（standalone / 测试场景，调用方视为"无需落库"）；
 *   - 否则 → `Promise<{ok:true} | {ok:false, error}>`（**不吞错**：调用方据此提示 / 保持编辑器打开）。
 */
export function saveLayoutDoc(doc) {
  const p = state.persist
  if (!p || typeof p.save !== 'function' || !doc) return null
  try {
    return Promise.resolve(p.save(doc)).then(
      (r) => (r && r.ok === false ? r : { ok: true }),
      (e) => ({ ok: false, error: (e && e.message) || String(e) }),
    )
  } catch (e) {
    return Promise.resolve({ ok: false, error: (e && e.message) || String(e) })
  }
}

/** 文案：未注入 i18n 时返回 key 本身（standalone 期组件内硬编码中文，§3.12）。 */
export function tr(key, params) {
  return state.i18n ? state.i18n(key, params) : key
}

/** 图标取值（name → svg 串）；未注入 → 空串（渲染为空，不报错）。 */
export function resolveIcon(name) {
  return state.iconResolver ? state.iconResolver(name) : ''
}

/** view 定义读取（`config/views.js` 用） */
export function viewDefinitions() {
  const defs = state.views && state.views.definitions
  return Array.isArray(defs) ? defs : []
}

/** viewKey → 组件（`views/LfViewHost.vue` 用）：支持 resolve() 或 components 表两种注入形态。 */
export function resolveViewComponent(viewKey) {
  if (!viewKey) return null
  const views = state.views
  if (!views) return null
  if (typeof views.resolve === 'function') return views.resolve(viewKey) || null
  return (views.components && views.components[viewKey]) || null
}
