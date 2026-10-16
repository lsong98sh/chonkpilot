/**
 * LfLayoutSplit 的纯布局逻辑（照 chonkpilot 的 `components/split/splitLayout.js` 的写法抽出，便于直测）。
 *
 * 与参考工程一致的三条口径：
 *
 *  1. **实际宽度 = 像素定尺**。子区写 `width/height: Npx` + `flex: 0 0 auto`
 *     （参考工程 `getPaneStyle()` 对非 flex pane 就是这么干的）。不用 flexGrow 比例，
 *     于是「子区像素之和 + 分割条像素 = 容器像素」是一条能直接断言的硬不变量，
 *     拖拽数学也与 `getBoundingClientRect()` 完全一致，不会来回换算漂移。
 *     占比**只用于持久化**（配置里存 sizes，渲染时换算成像素一次）。
 *
 *  2. **resizer 宽度与 pane 尺寸无关**。视觉本体恒 `--split-hairline`(1px)，
 *     命中区是伪元素 `--split-hit`(5px)，向两侧各外溢 2px；hover/拖拽只给伪元素上色。
 *     拖拽数学只读 pane 的 rect —— 所以加粗命中区不会引起重排或抖动。
 *
 *  3. **约束取两侧交集**：拖 a 变大 ⇒ b 变小（受 b.min 限制）；a 变小 ⇒ b 变大（受 b.max 限制）。
 *     具体区间由 utils/regions.js 的 `pairDragRange` / `nodeSizeRange` 给出。
 */

/** 退化情况下给两侧各留的最小像素，避免拖成 0 */
export const HARD_FLOOR = 40
/** 判「两侧有没有可拖交集」时的探测宽度（无需真实容器尺寸） */
export const PAIR_PROBE = 4000

/**
 * 两个相邻子区之间的可拖范围（返回值是要改变尺寸那一侧的像素区间）。
 * 返回 null 表示不可调整（任一区锁定 / 无有效交集）。
 *
 * @param a 要改变尺寸的一侧
 * @param b 邻居
 * @param direction 'row'（约束宽）| 'column'（约束高）
 * @param pairPx  两侧当前像素之和（拖拽时保持不变）
 * @param rangeOf (child, direction) => { min, max, fixed }
 */
export function splitDragRange(a, b, direction, pairPx, rangeOf) {
  const ra = rangeOf(a, direction)
  const rb = rangeOf(b, direction)
  if (!ra || !rb || ra.fixed || rb.fixed) return null
  const lo = Math.max(ra.min, pairPx - rb.max, HARD_FLOOR)
  const hi = Math.min(ra.max, pairPx - rb.min, pairPx - HARD_FLOOR)
  if (!Number.isFinite(lo) || !Number.isFinite(hi) || hi - lo <= 1) return null
  return { min: lo, max: hi }
}

/** 把目标像素夹到 [range.min, range.max] */
export function clampPx(px, range) {
  if (!range) return px
  return Math.max(range.min, Math.min(range.max, px))
}
