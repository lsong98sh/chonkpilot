<template>
  <div
    ref="containerRef"
    class="layout-split"
    :class="[node.direction === 'row' ? 'is-row' : 'is-column', { selected: isSelected }]"
    @mousedown="onMouseDown"
    @contextmenu="onContextMenu"
  >
    <template v-for="(child, i) in node.children" :key="child.id">
      <!-- 隐藏区域用 v-show 保活（不卸载）：见 WIN-025 / 「区块保活」硬约束 -->
      <div
        v-show="isVisible(child)"
        class="split-child"
        :data-child-id="child.id"
        :style="childStyle(i)"
      >
        <LfLayoutNodeRenderer
          :node="child"
          :depth="depth + 1"
          :design="design"
          :selected-id="selectedId"
        />
      </div>
      <!-- 分割条只出现在相邻两个可见子区之间 -->
      <div
        v-if="hasNextVisible(i)"
        class="split-handle"
        :class="[
          node.direction === 'row' ? 'vertical' : 'horizontal',
          { locked: !handleEnabled(i), active: dragging === i },
        ]"
        :title="handleEnabled(i) ? '拖拽调整大小' : '两侧区域尺寸被锁定（最大 = 最小）'"
        @mousedown.stop.prevent="onDragStart(i, $event)"
      />
    </template>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, onUpdated, reactive, ref } from 'vue'
import { useEditStore } from '../../stores/edit'
import { useConfigStore } from '../../stores/config'
import { useRuntimeStore } from '../../stores/runtime'
import { nodeSizeRange, pairDragRange } from '../../utils/regions'
import { EventNames } from '../../events/event-names'
import { getMq } from '../../deps.js'
import { PAIR_PROBE, clampPx, splitDragRange } from './splitPanel'
import LfLayoutNodeRenderer from './LfLayoutNodeRenderer.vue'

/**
 * 布局树里的分割容器 —— 拖拽实现**照抄 chonkpilot 的 components/split/LfSplitPanel.vue**：
 *
 *  1. mousedown 时 `preventDefault()`，然后监听 **window 的 mousemove / mouseup**
 *     （capture: true），闭包式收尾，组件卸载时兜底清理；
 *  2. 拖拽期间**直接改 DOM 的像素尺寸**（`el.style.width/height = Npx`），
 *     同时把像素记进响应式 `paneSizes`，保证重渲染后尺寸不丢；
 *  3. 尺寸用**像素定尺**（`flex: 0 0 auto`），不用 flexGrow 比例 ——
 *     于是「子区像素之和 + 分割条像素 = 容器像素」是硬不变量，数学与 rect 完全一致；
 *  4. 松手时把像素换算成占比写回配置（占比只用于持久化），并按模式落盘。
 */
const props = defineProps({
  /** 容器节点 */
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  /** 设计态（编辑器预览）。运行态与设计态**都能拖分割条**：
   *  运行态改 live 布局（拖完落盘），设计态改 draft 草稿（保存后生效）；
   *  设计态额外支持点选与右键菜单。 */
  design: { type: Boolean, default: false },
  /** 设计态下选中的节点 id */
  selectedId: { type: String, default: '' },
})

const containerRef = ref(null)
/** 正在拖拽的分割条下标（-1 = 未拖拽） */
const dragging = ref(-1)
const edit = useEditStore()
const config = useConfigStore()
const runtime = useRuntimeStore()

const isSelected = computed(() => props.design && props.selectedId === props.node.id)
const isRow = computed(() => props.node.direction === 'row')

/* ── 尺寸：像素为准 ─────────────────────────────────────── */

/** 子区实测像素（按子节点 id 记录；参考工程的 runtimeSizes 同款做法） */
const paneSizes = reactive({})

/** 配置里的占比（只用于持久化与「还没量到像素时」的兜底） */
const ratios = computed(() => {
  const n = props.node.children.length
  return props.node.sizes?.length === n ? props.node.sizes : Array.from({ length: n }, () => 1 / n)
})

/* ── 区域显隐（WIN-025）───────────────────────────────────
   隐藏 = `visible === false`（缺省可见）。隐藏子区**保持挂载**（v-show），
   但不参与尺寸分配、不渲染分割条；占比只在可见子区之间归一。 */

function isVisible(node) {
  return node?.visible !== false
}

const visibleChildren = computed(() => props.node.children.filter(isVisible))

/** 可见子区的归一占比（按配置占比在可见子集上重新归一） */
const visibleRatios = computed(() => {
  const list = props.node.children
  const vals = list.map((c, i) => (isVisible(c) ? Math.max(0, Number(ratios.value[i]) || 0) : 0))
  const sum = vals.reduce((a, b) => a + b, 0)
  const n = visibleChildren.value.length
  if (!(sum > 0)) return Array.from({ length: n }, () => 1 / Math.max(1, n))
  return vals.filter((_, i) => isVisible(list[i])).map((v) => v / sum)
})

/** 第 i 个子区在「可见子区序列」里的下标（不可见 → -1） */
function visibleIndexOf(i) {
  const list = props.node.children
  if (!isVisible(list[i])) return -1
  let n = -1
  for (let k = 0; k <= i; k += 1) if (isVisible(list[k])) n += 1
  return n
}

/** i 之后的下一个可见子区下标（没有 → -1） */
function nextVisibleIndex(i) {
  const list = props.node.children
  for (let k = i + 1; k < list.length; k += 1) if (isVisible(list[k])) return k
  return -1
}

/** 第 i 个子区后面是否要画分割条（i 可见且后面还有可见子区） */
function hasNextVisible(i) {
  return isVisible(props.node.children[i]) && nextVisibleIndex(i) !== -1
}

/** 子区 style：隐藏 → 不占尺寸（v-show 承担 display:none）；可见 → 像素定尺 / 占比兜底 */
function childStyle(i) {
  const child = props.node.children[i]
  if (!isVisible(child)) return {}
  const px = paneSizes[child?.id]
  if (px == null) {
    // 首帧：还没有量到像素 → 用可见子集占比撑开，避免闪一下 0 宽
    const vi = visibleIndexOf(i)
    const gr = visibleRatios.value[vi] ?? 1 / Math.max(1, visibleChildren.value.length)
    return { flexGrow: String(gr), flexShrink: '1', flexBasis: '0' }
  }
  return isRow.value
    ? { width: `${px}px`, flex: '0 0 auto', minWidth: '0', minHeight: '0' }
    : { height: `${px}px`, flex: '0 0 auto', minWidth: '0', minHeight: '0' }
}

/**
 * 运行态像素当前对应的**配置占比**快照（G-57 方案②）。
 *
 * 用途 = 区分「外部配置变更」与「用户拖拽」：
 *   · 外部配置变更（重启后 `gui.init-data` 的持久文档被 adopt → **整棵树换成新对象** → 本组件
 *     重渲染 → `onUpdated`）：`ratios` 与快照不一致 → 以配置为权威**重量测**（修复「占比不回灌」）；
 *   · 用户拖拽（拖拽中只写 `paneSizes`，`commit()` 再把占比写回配置）：commit 时**记账**成同一份
 *     占比 → 配置随后被原地改写也判为「无变化」→ **不重量测**，拖出来的像素不被覆盖。
 * 即「**配置值变了才权威化**」——与 §7.3「值变才写」同口径，且天然幂等（重量测不回写配置）。
 */
let knownRatios = null

/** 占比数组近似相等（容差远小于持久化精度，避免浮点抖动误判） */
function ratiosEqual(a, b) {
  if (!a || !b || a.length !== b.length) return false
  return a.every((v, i) => Math.abs(Number(v) - Number(b[i])) <= 1e-6)
}

/** 记账：当前运行态像素对应的配置占比（消费配置变更 / 用户拖拽落账时调用） */
function markRatiosKnown(sizes) {
  knownRatios = (sizes || ratios.value).map((v) => Number(v))
}

/** 容器主轴像素 */
function containerPx() {
  const el = containerRef.value
  if (!el) return 0
  return isRow.value ? el.clientWidth : el.clientHeight
}

/**
 * 量测并写入各子区像素。
 * 容器像素 - 所有分割条像素 = 各子区应占总量。
 *
 * 尺寸两态（UILAYOUT-2）：
 *   - 数值节点（`flex` 缺省 / 0）：按可见子集占比取目标，clamp 到 `[min, max]`；
 *     非强制量测时**保留当前像素**（用户拖拽 / 窗口缩放后不跳变）。
 *   - 弹性节点（`flex > 0`）：吃掉数值节点之外的**剩余空间**，份额按占比归一。
 *   - 无弹性节点 → 全部按占比分配（等价一期行为）。
 */
function measure(force = false) {
  const el = containerRef.value
  if (!el) return
  const total = containerPx()
  if (total <= 0) return
  // 只量可见子区：隐藏子区不参与「子区之和 + 分割条 = 容器」不变量
  const children = visibleChildren.value
  if (!children.length) return
  const handlePx = handleThickness() * Math.max(0, children.length - 1)
  const avail = Math.max(0, total - handlePx)

  const existing = children.map((c) => paneSizes[c.id])
  const haveAll = existing.every((v) => Number.isFinite(v) && v > 0)

  // 已量过 & 容器没变 & 不强制 → 不动（避免无谓重排）
  if (haveAll && !force) {
    const used = existing.reduce((a, b) => a + b, 0)
    if (Math.abs(used - avail) <= 1) return
  }

  const isFlex = (c) => Number(c?.flex) > 0
  const flexIdx = []
  children.forEach((c, i) => {
    if (isFlex(c)) flexIdx.push(i)
  })

  // 无弹性：按可见子集占比分配（一期行为）
  if (!flexIdx.length) {
    const weights = children.map((c, i) => {
      if (force) return Math.max(0.0001, visibleRatios.value[i] ?? 1 / children.length)
      const px = existing[i]
      return Number.isFinite(px) && px > 0 ? px : Math.max(0.0001, visibleRatios.value[i] ?? 1 / children.length)
    })
    const sum = weights.reduce((a, b) => a + b, 0) || 1
    children.forEach((c, i) => {
      paneSizes[c.id] = (weights[i] / sum) * avail
    })
    return
  }

  const ratioSum = visibleRatios.value.reduce((a, b) => a + b, 0) || 1
  const target = new Array(children.length).fill(0)
  let numericSum = 0

  children.forEach((c, i) => {
    if (isFlex(c)) return
    const ratio = Math.max(0.0001, visibleRatios.value[i] ?? 1 / children.length)
    let px =
      force || !Number.isFinite(existing[i]) || existing[i] <= 0
        ? (avail * ratio) / ratioSum
        : existing[i]
    const range = rangeOf(c, props.node.direction)
    px = Math.max(range.min, Math.min(range.max, px))
    target[i] = px
    numericSum += px
  })

  let remaining = avail - numericSum
  if (remaining < 0) {
    // 数值节点挤爆容器 → 等比缩小，弹性节点归零
    const k = numericSum > 0 ? avail / numericSum : 0
    children.forEach((c, i) => {
      if (!isFlex(c)) target[i] *= k
    })
    remaining = 0
  }

  const flexRatioSum = flexIdx.reduce((a, i) => a + Math.max(0.0001, visibleRatios.value[i] ?? 0), 0) || 1
  flexIdx.forEach((i) => {
    const r = Math.max(0.0001, visibleRatios.value[i] ?? 0)
    target[i] = remaining * (r / flexRatioSum)
  })

  children.forEach((c, i) => {
    paneSizes[c.id] = target[i]
  })
}

/** 分割条本体像素（读 CSS 变量，和参考工程一样把视觉宽度做成变量） */
function handleThickness() {
  const el = containerRef.value
  if (!el) return 1
  const raw = getComputedStyle(el).getPropertyValue('--split-hairline').trim()
  const n = Number.parseFloat(raw)
  return Number.isFinite(n) && n > 0 ? n : 1
}

/* ── 尺寸约束 ─────────────────────────────────────────── */

/** 取某个子节点在主轴上的约束区间 */
function rangeOf(child, direction) {
  return nodeSizeRange(child, direction)
}

/**
 * 分割条两侧都可调整时才可拖。
 * 容器（split）自身没有尺寸约束，所以容器旁边的分割条总可拖。
 */
function handleEnabled(index) {
  const direction = props.node.direction
  const a = props.node.children[index]
  const b = props.node.children[nextVisibleIndex(index)]
  if (!a || !b) return false
  // 只需要判「有没有交集」，pairPx 用一个足够大的探测值即可
  return pairDragRange(a, b, direction, PAIR_PROBE) !== null
}

/* ── 选中 / 右键（仅设计态）───────────────────────────── */

function onMouseDown() {
  // 运行态：仅记录当前激活区域（供命令解析「当前自由 tabs 区」）
  runtime.setActiveLeaf(props.node.id)
}

function onContextMenu(event) {
  if (!props.design) return
  event.preventDefault()
  event.stopPropagation()
  edit.openContextMenu(props.node.id, event.clientX, event.clientY)
}

/* ── 拖拽：window 上监听 mousemove（照抄参考工程）────────── */

let cleanupDrag = null

function onDragStart(index, event) {
  const el = containerRef.value
  if (!el) return
  if (!handleEnabled(index)) return

  const horizontal = isRow.value
  const children = props.node.children
  const a = children[index]
  const b = children[nextVisibleIndex(index)]
  if (!a || !b) return

  const total = containerPx()
  if (total <= 0) return

  const prop = horizontal ? 'clientX' : 'clientY'
  const sizeProp = horizontal ? 'width' : 'height'
  const cursor = horizontal ? 'col-resize' : 'row-resize'

  // 拖拽前的像素快照
  const startA = paneSizes[a.id] ?? 0
  const startB = paneSizes[b.id] ?? 0
  const pairPx = startA + startB
  if (pairPx <= 0) return

  const range = splitDragRange(a, b, props.node.direction, pairPx, rangeOf)
  if (!range) return

  const startPos = event[prop]
  // 隐藏子区仍在 DOM（v-show），不能用 children 下标推算 → 按 data-child-id 精确取
  const elA = el.querySelector(`[data-child-id="${a.id}"]`)
  const elB = el.querySelector(`[data-child-id="${b.id}"]`)

  dragging.value = index
  document.body.style.cursor = cursor
  document.body.style.userSelect = 'none'

  /** 拖拽中：直接改 DOM 像素 + 记录到 paneSizes（重渲染也不丢） */
  function onMove(ev) {
    ev.preventDefault()
    const want = startA + (ev[prop] - startPos)
    const nextA = clampPx(want, range)
    const nextB = pairPx - nextA
    paneSizes[a.id] = nextA
    paneSizes[b.id] = nextB
    if (elA) elA.style[sizeProp] = `${nextA}px`
    if (elB) elB.style[sizeProp] = `${nextB}px`
  }

  function onUp() {
    dragging.value = -1
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    window.removeEventListener('mousemove', onMove, true)
    window.removeEventListener('mouseup', onUp, true)
    cleanupDrag = null
    commit()
  }

  // 清掉上一次遗留的监听（安全网）
  cleanupDrag?.()
  cleanupDrag = onUp
  window.addEventListener('mousemove', onMove, { passive: false, capture: true })
  window.addEventListener('mouseup', onUp, { passive: false, capture: true })
}

/** 松手：把像素换算成占比写回配置（像素只活在运行时） */
function commit() {
  const children = props.node.children
  const vis = children.filter(isVisible)
  const px = vis.map((c) => paneSizes[c.id] ?? 0)
  const sum = px.reduce((x, y) => x + y, 0)
  if (sum <= 0) return
  // 隐藏子区保留其原占比；可见子区按实测像素分配剩余份额（全部可见时等价旧行为）
  const cur = ratios.value
  let hiddenTotal = 0
  children.forEach((c, i) => {
    if (!isVisible(c)) hiddenTotal += Math.max(0, Number(cur[i]) || 0)
  })
  hiddenTotal = Math.min(0.9, hiddenTotal)
  const remain = 1 - hiddenTotal
  const raw = new Array(children.length).fill(0)
  let vi = 0
  children.forEach((c, i) => {
    if (isVisible(c)) raw[i] = (px[vi++] / sum) * remain
    else raw[i] = Math.max(0, Number(cur[i]) || 0)
  })
  const total = raw.reduce((x, y) => x + y, 0)
  if (!(total > 0)) return
  const sizes = raw.map((v) => v / total)
  // 用户拖拽落账：配置即将等于该占比 → 记账之，避免「配置自己变了」触发重量测覆盖运行态（G-57）
  markRatiosKnown(sizes)
  // 走 mq：设计态改草稿、运行态直接改 live（随后由 saveLiveLayout 落盘）
  getMq().emit(EventNames.layoutSetSizes, {
    nodeId: props.node.id,
    sizes,
    target: props.design ? 'draft' : 'live',
  })
  if (!props.design) config.saveLiveLayout()
}

/* ── 生命周期 ─────────────────────────────────────────── */

let ro = null

onMounted(() => {
  lastChildSig = childSignature()
  lastVisibleIds = visibleIdSet()
  markRatiosKnown()
  measure(true)
  if (typeof ResizeObserver !== 'undefined' && containerRef.value) {
    // 容器尺寸变了（窗口缩放 / 侧栏折叠）→ 按现有比例重分配像素，维持不变量
    ro = new ResizeObserver(() => measure(false))
    ro.observe(containerRef.value)
  }
})

onBeforeUnmount(() => {
  cleanupDrag?.()
  cleanupDrag = null
  if (ro) {
    ro.disconnect()
    ro = null
  }
})

/** 子节点集合签名：仅「结构 / 显隐变了」才重量测 */
function childSignature() {
  return props.node.children.map((c) => `${c.id}:${isVisible(c) ? 1 : 0}`).join('|')
}
let lastChildSig = ''
/** 上一次渲染时的可见子区集合（用于捕获「隐藏瞬间」的占比快照） */
let lastVisibleIds = new Set()

function visibleIdSet() {
  return new Set(props.node.children.filter(isVisible).map((c) => c.id))
}

/**
 * 隐藏瞬间的占比快照（UILAYOUT-2）：把刚被隐藏子区的当前像素占比写回 `sizes[i]`，
 * 其余子区沿用配置占比；随后由配置更新触发一次权威重量测。
 * 返回 true = 已派发 sizes（调用方不必再强制量测）。
 */
function captureHideSnapshot(nextVisible) {
  const total = containerPx()
  if (!total) return false
  const handlePx = handleThickness() * Math.max(0, nextVisible.size - 1)
  const avail = Math.max(0, total - handlePx)
  if (!avail) return false
  const raw = props.node.children.map((c, i) => {
    const wasVisible = lastVisibleIds.has(c.id)
    const nowVisible = nextVisible.has(c.id)
    const px = paneSizes[c.id]
    if (wasVisible && !nowVisible && Number.isFinite(px) && px > 0) return px / avail
    return Math.max(0, Number(ratios.value[i]) || 0)
  })
  const sum = raw.reduce((a, b) => a + b, 0)
  if (!(sum > 0)) return false
  const sizes = raw.map((v) => Number((v / sum).toFixed(6)))
  if (ratiosEqual(sizes, ratios.value)) return false
  getMq().emit(EventNames.layoutSetSizes, {
    nodeId: props.node.id,
    sizes,
    target: props.design ? 'draft' : 'live',
  })
  if (!props.design) config.saveLiveLayout()
  return true
}

/**
 * 子节点集合变化（分割 / 删除 / 交换）→ 重量测；
 * 配置占比变化（外部 adopt / 编辑器保存）→ 以配置为权威幂等重量测（G-57 方案②）。
 * 原为 `watch(children ids)`；本组件 DOM 更新后比对签名/占比即可，语义等价且不侦听 props
 * （§4.2 禁 watch）。⚠️ 必须比对：`measure(true)` 会按配置占比重算像素，
 * 无条件执行会把运行态拖出来的像素尺寸重置掉。
 */
onUpdated(() => {
  const sig = childSignature()
  if (sig !== lastChildSig) {
    // 显隐变化：先把「刚被隐藏」子区的当前占比写回配置（UILAYOUT-2 快照），
    // 配置更新后由下方「配置占比变化」分支做权威重量测。
    const nextVisible = visibleIdSet()
    const snapped = captureHideSnapshot(nextVisible)
    lastVisibleIds = nextVisible
    lastChildSig = sig
    Object.keys(paneSizes).forEach((k) => {
      if (!props.node.children.some((c) => c.id === k)) delete paneSizes[k]
    })
    if (!snapped) {
      markRatiosKnown()
      measure(true)
    }
    return
  }
  // 配置 `sizes` 变了（≠ 用户拖拽自己写回的那份）→ 权威化：按新占比重量测（幂等，不改配置）
  if (!ratiosEqual(ratios.value, knownRatios)) {
    markRatiosKnown()
    measure(true)
  }
})
</script>

<style scoped>
.layout-split {
  display: flex;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  position: relative;
  overflow: hidden;
  /* 分割条视觉宽度 / 命中区宽度：与参考工程一致，做成变量 */
  --split-hairline: 1px;
  --split-hit: 5px;
}

.layout-split.is-row {
  flex-direction: row;
}

.layout-split.is-column {
  flex-direction: column;
}

.split-child {
  min-width: 0;
  min-height: 0;
  display: flex;
  overflow: hidden;
}

.split-child > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

/* ── Resizer ──
   视觉与命中解耦（照参考工程）：
   - 本体 = 1px 发丝线（--split-hairline），常态取边框色；
   - 命中区 = 伪元素 ::after（--split-hit = 5px），向两侧各外溢 2px，常态透明；
   - hover / 拖拽（.active）只给伪元素上强调色 → 变粗为 5px，本体恒 1px，
     所以拖拽瞬间不重排、不抖动（拖拽数学只读 pane 的 rect，与本宽度无关）。 */
.split-handle {
  flex: none;
  flex-shrink: 0;
  position: relative;
  background: var(--lf-border);
  z-index: 1;
  transition: background 0.12s;
}

.split-handle.vertical {
  width: var(--split-hairline);
  cursor: col-resize;
}

.split-handle.horizontal {
  height: var(--split-hairline);
  cursor: row-resize;
}

.split-handle::after {
  content: '';
  position: absolute;
  background: transparent;
  transition: background 0.12s;
}

.split-handle.vertical::after {
  top: 0;
  bottom: 0;
  left: calc((var(--split-hairline) - var(--split-hit)) / 2);
  right: calc((var(--split-hairline) - var(--split-hit)) / 2);
}

.split-handle.horizontal::after {
  left: 0;
  right: 0;
  top: calc((var(--split-hairline) - var(--split-hit)) / 2);
  bottom: calc((var(--split-hairline) - var(--split-hit)) / 2);
}

/* 只有鼠标下那一个变色：hover 只亮自己，拖拽靠 .active（不要用容器级 .dragging） */
.split-handle:hover::after,
.split-handle.active::after {
  background: var(--lf-accent);
}

/* 锁定（最大 = 最小）：不可拖拽 */
.split-handle.locked {
  cursor: not-allowed !important;
}

.split-handle.locked::after {
  display: none;
}

.layout-split.selected > .split-child {
  outline: 1px dashed var(--lf-select-border);
  outline-offset: -1px;
}
</style>
