/**
 * themeResolve —— 主题纯逻辑（THEME-1，2026-10-23）
 *
 * 主题是一份**静态可编辑资产** `themes.css`（运行期由宿主经 `GET /themes.css` 提供，
 * 宿主 / 插件页面都用 `<link>` 引入）；本模块只做纯函数判定，**不碰 DOM、不落盘**
 * （DOM 写点唯一在 `composables/useTheme.js#applyTheme`）。
 *
 * - `THEMES`：出厂主题 id（与 `src/initdata/themes/themes.css` 的三套块一一对应）。
 * - `THEME_TOKENS`：快照白名单（与 `public/shared/ck-theme.js` 的同名常量一一对应，
 *   单测守卫两者一致）。
 * - 运行态主题集合以 `window.ckTheme.themes`（从样式表 `[data-theme="x"]` 扫描）为准，
 *   用户自行往 themes.css 增主题时无需改前端。
 */

/** 出厂主题 id（light = `:root` 隐式默认）。 */
export const THEMES = ['light', 'dark', 'nord']

/** 主题快照 token 白名单（`snapshot().tokens` 的键）。 */
export const THEME_TOKENS = [
  '--ck-bg',
  '--ck-surface',
  '--ck-border',
  '--ck-text',
  '--ck-text-muted',
  '--ck-accent',
  '--ck-accent-bg',
  '--ck-success',
  '--ck-warning',
  '--ck-danger',
  '--ck-radius',
  '--ck-font-sans',
  '--ck-font-mono',
]

/** 已知暗色主题（其余按 id 命名推断）。 */
const DARK_THEMES = ['dark', 'nord']

/** `window.ckTheme`（无 DOM / 脚本未加载 → null）。 */
function runtime() {
  if (typeof window === 'undefined') return null
  return window.ckTheme || null
}

/** 当前可用主题 id（优先运行态发现结果，缺失回落出厂列表）。 */
export function themes() {
  const ct = runtime()
  const list = ct && Array.isArray(ct.themes)
    ? ct.themes.filter((x) => typeof x === 'string' && x)
    : []
  return list.length ? list : THEMES.slice()
}

/** 主题 id 是否可用（出厂列表 ∪ 运行态发现结果）。 */
export function isValidTheme(id) {
  if (typeof id !== 'string' || !id) return false
  if (/\s/.test(id)) return false
  return themes().indexOf(id) >= 0
}

/** 主题 → 配色方案（供 `color-scheme` / 系统配色兜底）。 */
export function schemeOf(id) {
  if (typeof id !== 'string' || !id) return 'light'
  if (DARK_THEMES.indexOf(id) >= 0) return 'dark'
  return /(^|[-_])(dark|nord|night|dim|black)($|[-_])/i.test(id) ? 'dark' : 'light'
}

/**
 * 读取当前生效的语义 token 快照（**薄包装**，白名单由 `window.ckTheme` 实现，
 * 此处不重复实现；无运行态 → 空对象）。
 */
export function readTokens() {
  const ct = runtime()
  if (!ct || typeof ct.snapshot !== 'function') return {}
  try {
    const snap = ct.snapshot()
    return (snap && snap.tokens) || {}
  } catch (_) {
    return {}
  }
}
