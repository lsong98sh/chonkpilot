/**
 * 聊天消息列表「虚拟滚动 / 窗口化渲染」纯逻辑（2026-10-11）。
 *
 * 无任何 Vue 依赖（纯函数）→ 便于 node:test 直接单测（见 test/virtualWindow.test.js）。
 *
 * 语义：
 *   - 仅当消息总数 > VIRTUALIZE_THRESHOLD 时启用窗口化；否则原样全量渲染（enabled=false，
 *     start=0 / end=total / pad=0），保证小会话与既有 e2e 的 DOM / 行为不变；
 *   - 启用时：`start = max(0, firstVisible - overscan)`、`end = min(total, lastVisible + 1 + overscan)`；
 *     `topPad = offsets[start]`、`bottomPad = offsets[total] - offsets[end]`（上下用占位撑高）；
 *   - 高度用「已测高度（heights[i]）+ 估算高度（estHeight）回退」的前缀和定位。
 */

/** 超过该条数才启用窗口化（≤ 该值沿用全量渲染）。 */
export const VIRTUALIZE_THRESHOLD = 120
/** 视口上下各多渲染的消息条数（滚动缓冲，减少快速滚动白屏）。 */
export const OVERSCAN = 8
/** 未测得高度时的单条估算高度（px）。 */
export const EST_ITEM_HEIGHT = 80

/**
 * 前缀和：offsets[i] = 前 i 条消息的累计高度；offsets 长度 = heights.length + 1。
 * heights[i] 为有效正数时用其值，否则回退 estHeight（null/undefined/NaN/≤0 均回退）。
 */
export function buildOffsets(heights, estHeight = EST_ITEM_HEIGHT) {
  const n = Array.isArray(heights) ? heights.length : 0
  const out = new Array(n + 1)
  out[0] = 0
  for (let i = 0; i < n; i++) {
    const h = heights[i]
    out[i + 1] = out[i] + (typeof h === 'number' && Number.isFinite(h) && h > 0 ? h : estHeight)
  }
  return out
}

/** 二分：返回 [0, total] 内首个满足 offsets[i] >= target 的 i（无则返回 total）。 */
function lowerBound(offsets, target, total) {
  let lo = 0
  let hi = total
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (offsets[mid] < target) lo = mid + 1
    else hi = mid
  }
  return lo
}

/**
 * 计算窗口区间与上下占位高度。
 * @returns {{enabled:boolean,start:number,end:number,topPad:number,bottomPad:number}}
 *   未启用（total ≤ threshold 或空列表）时 start=0 / end=total / pad=0。
 */
export function computeRange({
  total = 0,
  scrollTop = 0,
  viewport = 0,
  offsets = [],
  threshold = VIRTUALIZE_THRESHOLD,
  overscan = OVERSCAN,
} = {}) {
  const n = Number.isFinite(total) && total > 0 ? Math.floor(total) : 0
  const enabled = n > threshold
  if (!enabled) {
    return { enabled: false, start: 0, end: n, topPad: 0, bottomPad: 0 }
  }
  // offsets 长度须 ≥ total+1；调用方未给足时用估算高度兜底（防御性）
  const off = Array.isArray(offsets) && offsets.length >= n + 1
    ? offsets
    : buildOffsets(new Array(n), EST_ITEM_HEIGHT)
  const st = Number.isFinite(scrollTop) ? scrollTop : 0
  const vp = Number.isFinite(viewport) && viewport > 0 ? viewport : 0
  const first = lowerBound(off, st, n)
  const last = lowerBound(off, st + vp, n)
  const start = Math.max(0, first - overscan)
  const end = Math.min(n, last + 1 + overscan)
  const topPad = Math.max(0, off[start] - off[0])
  const bottomPad = Math.max(0, off[n] - off[end])
  return { enabled: true, start, end, topPad, bottomPad }
}

/**
 * 回填单条实测高度（原地更新并返回同数组；非法输入安全忽略）。
 *
 * 说明：本函数为**纯工具**，当前**无生产引用**（MessageList 的高度回填改用「msg.id 键的 Map」+
 * heightsVersion 自增，不按原始下标写数组回填；见 `views/chat/MessageList.vue` measureItemHeights）。
 * 保留不删除：属该模块「窗口化纯逻辑」的对称 API（与 buildOffsets/computeRange 配套），供单测覆盖
 * 与后续复用；删除会牵动 test/virtualWindow.test.js 的两条用例，风险高于收益。
 *
 * @param {Array<number|null|undefined>} heights
 * @param {number} index 消息的**原始**下标
 * @param {number} h 实测高度（px，须为正有限数）
 */
export function applyMeasured(heights, index, h) {
  if (!Array.isArray(heights)) return []
  if (!Number.isInteger(index) || index < 0) return heights
  if (typeof h !== 'number' || !Number.isFinite(h) || h <= 0) return heights
  heights[index] = h
  return heights
}
