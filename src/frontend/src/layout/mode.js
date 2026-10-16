/**
 * 布局形态判定（UILAYOUT-1 §3.9 回退口径 / §3.13 回归口径 · §7 P3⑤ / P4⑤）。
 *
 * 两种形态：
 *   - `free`  ：自由布局容器（`<ChonkLayout>`，按 `layout.free` 文档渲染）—— **默认**；
 *   - `legacy`：既有硬编码 `MainLayout` 分支（5 层 `SplitPanel`；**始终保留**，可回退/可对比）。
 *
 * 判定口径（〔P4⑤ 裁决 3（2026-10-16）：切默认 free〕）：
 *   1. URL `?layout=legacy` → 显式回退到旧分支；
 *   2. `?layout=free` / 未指定 → **free**（有 `layout.free` 文档 → 采用之；无 → 适配层默认文档）。
 *
 * 〔为何改默认〕P3 时默认 legacy 是因为「栏条目化 / 编辑器入口」尚未落地（没有 UI 入口产出
 * `layout.free`，切默认会让既有 L4 回环全指新渲染路径）。P4 完成条目化 + 默认文档 + 编辑器入口后，
 * 由题面裁决：**默认 free**，`?layout=legacy` 保留回退（旧分支不删）。原 §3.13「无 `layout.free`
 * → 旧路径」口径**已被本裁决取代**（`hasFreeDoc` 入参保留仅为兼容既有调用/单测签名）。
 *
 * 本模块是**叶子模块**（零 import、零副作用）→ 可直跑单测，也可被 MainLayout 安全引用。
 */

export const LAYOUT_MODE_LEGACY = 'legacy'
export const LAYOUT_MODE_FREE = 'free'

/** URL 参数名（`?layout=...`）。 */
export const LAYOUT_PARAM = 'layout'

/**
 * 解析 URL 里的显式形态（`?layout=legacy|free`）；未指定/非法 → ''（交由数据决定）。
 * @param {string} search `location.search`（含前导 `?`，可缺省）
 */
export function parseLayoutParam(search = '') {
  const params = new URLSearchParams(String(search || ''))
  const v = params.get(LAYOUT_PARAM)
  return v === LAYOUT_MODE_FREE || v === LAYOUT_MODE_LEGACY ? v : ''
}

/**
 * 最终形态（P4⑤：默认 free）。
 * @param {{forced?: string, hasFreeDoc?: boolean}} opts
 *   forced      URL 显式指定（`parseLayoutParam` 的结果；''/undefined = 未指定）
 *   hasFreeDoc  是否已读到可用的 `layout.free` 文档（**P4⑤ 起不再决定形态**，保留入参兼容调用方）
 * @returns {'legacy'|'free'}
 */
export function resolveLayoutMode({ forced = '' } = {}) {
  return forced === LAYOUT_MODE_LEGACY ? LAYOUT_MODE_LEGACY : LAYOUT_MODE_FREE
}
