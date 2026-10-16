/**
 * 区域（leaf 节点）的三种类型 —— tabs 属于「区域本身」，不属于 view：
 *
 *   kind: 'single'      单容器：区域里放一个 view
 *   kind: 'fixed-tabs'  固定 tab 容器：区域里是若干个固定的 view（标签页）
 *   kind: 'free-tabs'   自由 tab 容器：区域是具名容器，运行期由命令装入 view（可预置内容）
 *
 * 数据结构：
 *   { id, type: 'leaf', title, kind,
 *     view?,        // kind = single 时：{ viewKey, instanceKey }
 *     tabs?,        // kind = fixed-tabs / free-tabs 时：[{ viewKey, instanceKey, label, pinned }]
 *     tabPosition?, // tab 容器标签栏位置：top | bottom | left | right
 *     toolbar?,     // 区域自定义工具栏：{ toolbarId, position: top|bottom|left|right }
 *     min, max,     // 尺寸约束（主轴像素；row 方向 = 宽，column 方向 = 高）
 *   }
 * 旧结构（view 里带 viewKey: 'fixed-tabs' | 'free-tabs'）由 upgradeLeaf 自动升级。
 */
export const REGION_KINDS = [
  { value: 'single', label: '单容器', icon: 'square', hint: '一个区域放一个 view' },
  { value: 'fixed-tabs', label: '固定 tab 容器', icon: 'gallery-horizontal', hint: '区域内是若干固定的 view 标签页' },
  { value: 'free-tabs', label: '自由 tab 容器', icon: 'gallery-vertical', hint: '具名容器，运行期由命令装入 view' },
]

/** tab 容器的标签栏位置 */
export const TAB_POSITIONS = [
  { value: 'top', label: '上', icon: 'panel-top' },
  { value: 'bottom', label: '下', icon: 'panel-bottom' },
  { value: 'left', label: '左', icon: 'panel-left' },
  { value: 'right', label: '右', icon: 'panel-right' },
]

/** 区域自定义工具栏的位置（与标签栏位置同一套取值） */
export const TOOLBAR_POSITIONS = TAB_POSITIONS

/** 尺寸约束默认值 */
export const DEFAULT_MIN_SIZE = 120
export const DEFAULT_MAX_SIZE = 4000
/** 判断「最大 = 最小」的容差（像素） */
export const FIXED_TOLERANCE = 1

/** 标签栏位置（默认上）；仅 tab 容器有意义 */
export function tabPosition(leaf) {
  const p = leaf?.tabPosition
  return TAB_POSITIONS.some((x) => x.value === p) ? p : 'top'
}

export function isTabBarVertical(leaf) {
  const p = tabPosition(leaf)
  return p === 'left' || p === 'right'
}

/** 区域类型（兼容旧结构） */
export function regionKind(leaf) {
  if (!leaf) return 'single'
  if (leaf.kind) return leaf.kind
  const key = leaf.view?.viewKey
  if (key === 'fixed-tabs') return 'fixed-tabs'
  if (key === 'free-tabs') return 'free-tabs'
  return 'single'
}

export function isSingle(leaf) {
  return regionKind(leaf) === 'single'
}

export function isFixedTabs(leaf) {
  return regionKind(leaf) === 'fixed-tabs'
}

export function isFreeTabs(leaf) {
  return regionKind(leaf) === 'free-tabs'
}

export function isTabsContainer(leaf) {
  const k = regionKind(leaf)
  return k === 'fixed-tabs' || k === 'free-tabs'
}

/** 该区域包含的 view（单容器 1 个；tab 容器 N 个） */
export function regionViews(leaf) {
  const k = regionKind(leaf)
  if (k === 'single') return leaf.view ? [leaf.view] : []
  return (leaf.tabs ?? []).filter(Boolean)
}

/* ── 尺寸约束 ─────────────────────────────────────────── */

/** 数值化（空值 / 非法值 → fallback） */
function num(v, fallback) {
  const n = Number(v)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

/**
 * 区域在某个分割方向上的尺寸约束。
 * direction: 'row'（左右排布 → 约束的是**宽度**）| 'column'（上下排布 → **高度**）
 * 返回 { min, max, fixed }（fixed = max-min 在容差内 → 不可调整）
 */
export function leafSizeRange(leaf, direction) {
  const isRow = direction === 'row'
  const min = num(isRow ? leaf?.minWidth ?? leaf?.min : leaf?.minHeight ?? leaf?.min, DEFAULT_MIN_SIZE)
  const max = num(isRow ? leaf?.maxWidth ?? leaf?.max : leaf?.maxHeight ?? leaf?.max, DEFAULT_MAX_SIZE)
  const lo = Math.min(min, max)
  const hi = Math.max(min, max)
  return { min: lo, max: hi, fixed: hi - lo <= FIXED_TOLERANCE }
}

/** 容器自身的尺寸约束（容器没有尺寸设定 → 退化区间） */
export function nodeSizeRange(node, direction) {
  if (!node || node.type === 'leaf') return leafSizeRange(node, direction)
  return { min: 0, max: Number.MAX_SAFE_INTEGER, fixed: false }
}

/** 区域是否被锁定为固定尺寸（不可拖拽） */
export function isLeafFixedSize(leaf, direction) {
  return leafSizeRange(leaf, direction).fixed
}

/**
 * 相邻两区之间可拖拽的范围：a 是要改变尺寸的一侧，b 是邻居。
 *   a 变大 ⇒ b 变小 → a 上界还受 b.min 限制；
 *   a 变小 ⇒ b 变大 → a 下界还受 b.max 限制。
 * 返回 null 表示不可调整（任一区固定尺寸 / 无有效交集）。
 */
export function pairDragRange(a, b, direction, pairPx) {
  const ra = nodeSizeRange(a, direction)
  const rb = nodeSizeRange(b, direction)
  if (ra.fixed || rb.fixed) return null
  const lo = Math.max(ra.min, pairPx - rb.max, 40)
  const hi = Math.min(ra.max, pairPx - rb.min, pairPx - 40)
  if (!Number.isFinite(lo) || !Number.isFinite(hi) || hi - lo <= FIXED_TOLERANCE) return null
  return { min: lo, max: hi }
}

/* ── 旧结构升级 ───────────────────────────────────────── */

/* ── 标签页固定（pin）───────────────────────────────── */

/**
 * 固定优先排序：**固定的排到最前**（横排 = 最左，竖排 = 最上），
 * 其余保持原有相对顺序。返回新数组（不原地改）。
 */
export function sortPinnedFirst(tabs) {
  const list = Array.isArray(tabs) ? tabs : []
  const pinned = list.filter((t) => t?.pinned === true)
  if (!pinned.length) return list.slice()
  return [...pinned, ...list.filter((t) => t?.pinned !== true)]
}

export function upgradeLeaf(leaf) {
  if (!leaf || leaf.type !== 'leaf') return leaf
  if (!leaf.kind) {
    const key = leaf.view?.viewKey
    if (key === 'fixed-tabs') {
      leaf.kind = 'fixed-tabs'
      leaf.tabs = leaf.view?.tabs ?? []
      leaf.view = undefined
    } else if (key === 'free-tabs') {
      leaf.kind = 'free-tabs'
      leaf.tabs = leaf.view?.tabs ?? []
      leaf.view = undefined
    } else {
      leaf.kind = 'single'
    }
  }
  if (leaf.kind === 'single') {
    leaf.tabs = undefined
  } else {
    leaf.view = undefined
    if (!Array.isArray(leaf.tabs)) leaf.tabs = []
    if (!TAB_POSITIONS.some((x) => x.value === leaf.tabPosition)) leaf.tabPosition = 'top'
  }
  return leaf
}

/** 区域显示名（区域标题 → 类型名） */
export function regionLabel(leaf) {
  if (!leaf) return '区域'
  if (leaf.title) return leaf.title
  const k = regionKind(leaf)
  if (k === 'single') return leaf.view?.viewKey || '空白区域'
  return REGION_KINDS.find((x) => x.value === k)?.label ?? '区域'
}

/** 区域类型的展示信息 */
export function regionKindMeta(leaf) {
  return REGION_KINDS.find((x) => x.value === regionKind(leaf)) ?? REGION_KINDS[0]
}
