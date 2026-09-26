/**
 * CSS 尺寸 token 读取（供 JS 布局计算复用 CSS 的单一取值口径）。
 *
 * 背景：toolbar / statusbar 高度在 `assets/styles/variables.css` 以 token 声明，
 * 但布局 clamp 需要具体像素值 → 从 `:root` 计算值读取（与 CSS 同源）；无 DOM 环境
 * （如 node:test）或 token 缺失/非法时回退到默认常量。
 */

/** token 缺失时的回退：toolbar 高（px） */
export const FALLBACK_TOOLBAR_HEIGHT = 44
/** token 缺失时的回退：statusbar 高（px） */
export const FALLBACK_STATUSBAR_HEIGHT = 24

/** 解析 CSS 长度字符串（'44px' / '44'）→ 数字；非法 / 空 → fallback */
export function parseCssPx(raw, fallback) {
  const n = parseFloat(raw)
  return Number.isFinite(n) ? n : fallback
}

/** 读取 `:root` 上指定 CSS 长度 token；无 DOM / 缺失 → fallback */
export function readRootPx(name, fallback) {
  if (typeof document === 'undefined' || typeof getComputedStyle !== 'function') return fallback
  const raw = getComputedStyle(document.documentElement).getPropertyValue(name)
  return parseCssPx(raw, fallback)
}

/** 内容区高 = 视口高 − toolbar 高 − statusbar 高（三者取同一 token 口径） */
export function computeContentHeight(vh) {
  return vh
    - readRootPx('--toolbar-height', FALLBACK_TOOLBAR_HEIGHT)
    - readRootPx('--statusbar-height', FALLBACK_STATUSBAR_HEIGHT)
}
