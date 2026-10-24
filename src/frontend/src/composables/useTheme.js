/**
 * useTheme —— 主题运行态（模块级单例，THEME-1，2026-10-23）
 *
 * 口径：
 *   - **唯一 DOM/持久化写点** = `applyTheme(id, {persist})`：设 `data-theme` + `color-scheme`
 *     + 写 `localStorage['chonkpilot-theme']`，**仅 `persist:true` 时**再 `saveUIState({theme})`，
 *     最后通知订阅者（插件 iframe 靠它推主题）。
 *   - 收 usr 配置广播 / 启动加载一律 `persist:false` —— 事件驱动的回写会形成
 *     save → changed → save 自激（与 `plugins/i18n.js` 的 applyLocale/setLocale 分离同口径）。
 *   - 主题资产本身是静态 CSS（`themes.css`），本模块只切 `data-theme`，不注入样式。
 *   - 模块级单例（每窗口一份 JS 上下文）：多处 `useTheme()` 共享同一 `theme` ref 与订阅表；
 *     **不用 Pinia**（与 `useUserConfigSync.js` 同风格）。
 */
import { ref } from 'vue'
import { saveUIState } from '../api/file'
import { isValidTheme, schemeOf, readTokens } from '../utils/themeResolve.js'

/** 本窗口当前生效主题（Toolbar 勾选态 / 插件 iframe 推送以此为准）。 */
const theme = ref('light')

/** 主题变更订阅者（`subscribeTheme` 注册，返回退订函数）。 */
const subscribers = new Set()

/** 当前主题快照 `{id, scheme, tokens}`（tokens 取自 `window.ckTheme`）。 */
export function themeSnapshot() {
  const id = theme.value
  return { id, scheme: schemeOf(id), tokens: readTokens() }
}

/** 订阅主题变更（cb 收到 `themeSnapshot()`）；返回退订函数。 */
export function subscribeTheme(cb) {
  if (typeof cb !== 'function') return () => {}
  subscribers.add(cb)
  return () => {
    subscribers.delete(cb)
  }
}

function notify(snap) {
  for (const cb of Array.from(subscribers)) {
    try {
      cb(snap)
    } catch (_) {
      /* 单个订阅者异常不影响其它订阅者 */
    }
  }
}

/**
 * 应用主题（**唯一写点**）。
 * @param {string} id 主题 id（须在 `themeResolve.themes()` 内，否则忽略）
 * @param {{persist?: boolean}} [opts] persist=true 才回写 usr 配置（用户主动切换时用）
 * @returns {{id: string, scheme: string, tokens: object}} 应用后的快照
 */
export function applyTheme(id, { persist = false } = {}) {
  if (!isValidTheme(id)) return themeSnapshot()
  theme.value = id
  const scheme = schemeOf(id)
  if (typeof document !== 'undefined') {
    document.documentElement.setAttribute('data-theme', id)
    if (document.documentElement.style) document.documentElement.style.colorScheme = scheme
  }
  try {
    localStorage.setItem('chonkpilot-theme', id)
  } catch (_) {
    /* 沙箱 / 隐私模式：忽略 */
  }
  // 同步 ck-theme 运行态（内部订阅者 / themes 清单一致；不重复落盘）
  const ct = typeof window !== 'undefined' ? window.ckTheme : null
  if (ct && typeof ct.apply === 'function') {
    try {
      ct.apply(id, { persist: false, scheme })
    } catch (_) {
      /* 公共资产异常不影响宿主切换 */
    }
  }
  if (persist) saveUIState({ theme: id }).catch(() => {})
  const snap = themeSnapshot()
  notify(snap)
  return snap
}

/** 接线入口（供组件 / 动作模块取用）。 */
export function useTheme() {
  return { theme, applyTheme, snapshot: themeSnapshot, subscribe: subscribeTheme }
}
