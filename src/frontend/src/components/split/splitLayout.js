/**
 * SplitPanel 纯布局逻辑（从组件抽出，便于 node:test 直测）。
 *
 * 约定：
 * - pane.resizable === false 为「固定不可拖」（如 toolbar / statusbar）；
 *   未显式声明 (= undefined) 视为可拖拽。
 * - pane.flex === true 为撑满区（主轴不写死尺寸），非 flex 为定尺区。
 */

/** 未显式传 min 时的退化下限（避免过窄） */
export const DEFAULT_MIN_SIZE = 160
/** 未显式传 max 时的退化上限 */
export const DEFAULT_MAX_SIZE = 2000

/**
 * 相邻两个可见 pane 之间是否渲染 resizer。
 * 两侧都不得显式 resizable:false（否则会出现 0 宽 resizer / 可拖「固定区」）。
 */
export function shouldShowResizer(panes, i) {
  if (!panes || i >= panes.length - 1) return false
  return panes[i].resizable !== false && panes[i + 1].resizable !== false
}

/**
 * 解析拖拽目标 pane 下标：
 * - 当前为 flex → 目标 = 其后一个 pane（拖 flex 的边界即调固定邻居）；
 * - 否则 → 目标 = 自身。
 * 目标缺失 / 目标为 flex / 目标显式 resizable:false → 返回 null（不启动拖拽）。
 */
export function resolveDragTarget(panes, visibleIndex) {
  const pane = panes?.[visibleIndex]
  if (!pane) return null
  const targetIndex = pane.flex && visibleIndex < panes.length - 1 ? visibleIndex + 1 : visibleIndex
  const target = panes[targetIndex]
  if (!target || target.flex || target.resizable === false) return null
  return targetIndex
}

/** 定尺 pane 生效下限 */
export function paneMin(pane) {
  return pane.min ?? DEFAULT_MIN_SIZE
}

/** 定尺 pane 生效上限 */
export function paneMax(pane) {
  return pane.max ?? DEFAULT_MAX_SIZE
}
