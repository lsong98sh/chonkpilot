<template>
  <div
    class="split-panel"
    :class="`split-${direction}`"
    ref="containerRef"
  >
    <template v-for="(pane, i) in visiblePanes" :key="pane.id">
      <!-- Pane wrapper -->
      <div
        class="split-pane"
        :class="{
          'is-flex': pane.flex,
          'is-fixed': !pane.flex,
        }"
        :style="getPaneStyle(pane, i)"
        :ref="(el) => setPaneRef(el, i)"
      >
        <div v-if="pane.header" class="split-pane-header">
          {{ pane.header }}
        </div>
        <div class="split-pane-body">
          <slot :name="`pane-${pane._originalIndex}`" />
        </div>
      </div>

      <!-- Resizer (between visible panes; both sides must be resizable) -->
      <div
        v-if="shouldShowResizer(visiblePanes, i)"
        class="split-resizer"
        :class="[
          `resizer-${direction}`,
          { 'is-active': activeResizer === pane._originalIndex }
        ]"
        @mousedown.stop.prevent="startDrag(i, $event)"
      />
    </template>
  </div>
</template>

<script setup>
import { ref, computed, onUpdated, onBeforeUnmount } from 'vue'
import { shouldShowResizer, resolveDragTarget, paneMin, paneMax } from './splitLayout'

// ── Props ──────────────────────────────────────────────────────

const props = defineProps({
  direction: { type: String, default: 'horizontal' },
  // gap：保留以兼容既有调用方（MainLayout 传 4 / 0）。自 2026-09-28 起**不再参与**
  // resizer 的视觉/命中宽度 —— 视觉 = `--split-hairline`（1px 发丝线），命中 = `--split-hit`
  // （5px 伪元素），两者与 pane 尺寸无关（拖拽测量走 pane 的 getBoundingClientRect）。
  gap: { type: Number, default: 4 },
  panes: { type: Array, required: true },
})

// 拖拽结束通知（供上层持久化分隔条位置）。
const emit = defineEmits(['size-changed'])

// ── Refs ───────────────────────────────────────────────────────

const containerRef = ref(null)
const paneElements = ref([])
const activeResizer = ref(-1)

/**
 * Runtime sizes keyed by pane id.
 * We keep this separate from props to avoid mutating prop objects.
 */
const runtimeSizes = ref({})

// ── Computed ───────────────────────────────────────────────────

/** Filter to visible panes only, attaching original index */
const visiblePanes = computed(() => {
  if (!props.panes) return []
  return props.panes
    .map((pane, i) => ({
      ...pane,
      _originalIndex: i,
    }))
    .filter((pane) => pane.visible !== false)
})

// ── onUpdated: clear paneElements when visiblePanes changes ──
let prevPaneSig = ''
onUpdated(() => {
  const sig = JSON.stringify(visiblePanes.value.map(p => `${p.id}:${p.visible}`))
  if (sig !== prevPaneSig) {
    paneElements.value = []
  }
  prevPaneSig = sig
})

/**
 * Resizer 渲染条件见 `./splitLayout.js` 的 shouldShowResizer：
 * 相邻两侧都不得显式 resizable:false（否则会出现 0 宽 resizer）。
 */

// ── Pane ref management ───────────────────────────────────────

function setPaneRef(el, index) {
  paneElements.value[index] = el
}

// ── Style helpers ──────────────────────────────────────────────

function getPaneStyle(pane, visibleIndex) {
  const h = props.direction === 'horizontal'

  if (pane.flex) {
    // flex 面板：主轴保留最小尺寸保护（pane.min），防被定尺邻居挤成 0
    const flexMin = pane.min ?? 0
    return h
      ? { flex: '1', minWidth: `${flexMin}px`, minHeight: '0' }
      : { flex: '1', minWidth: '0', minHeight: `${flexMin}px` }
  }

  const rtSize = runtimeSizes.value[pane.id]
  const sizeVal = rtSize ?? pane.size ?? 200

  // Non-resizable panes (e.g., titlebar, statusbar) should not have min/max constraints
  if (pane.resizable === false) {
    if (h) {
      return {
        width: `${sizeVal}px`,
        flex: '0 0 auto',
      }
    } else {
      return {
        height: `${sizeVal}px`,
        flex: '0 0 auto',
      }
    }
  }

  // Resizable panes: apply min/max constraints
  const minVal = paneMin(pane)
  const maxVal = paneMax(pane)

  if (h) {
    return {
      width: `${sizeVal}px`,
      minWidth: `${minVal}px`,
      maxWidth: `${maxVal}px`,
      flex: '0 0 auto',
    }
  } else {
    return {
      height: `${sizeVal}px`,
      minHeight: `${minVal}px`,
      maxHeight: `${maxVal}px`,
      flex: '0 0 auto',
    }
  }
}

// ── Drag logic (closure pattern, no global pollution) ─────────

let _cleanupDrag = null

function startDrag(visibleIndex, e) {
  e.preventDefault()

  const isHorizontal = props.direction === 'horizontal'
  const prop = isHorizontal ? 'clientX' : 'clientY'
  const sizeProp = isHorizontal ? 'width' : 'height'
  const cursor = isHorizontal ? 'col-resize' : 'row-resize'

  const pane = visiblePanes.value[visibleIndex]
  if (!pane) return

  // 拖拽目标：flex 面板作用于其后一个固定面板；固定面板作用于自身。
  // 目标为 flex 或显式 resizable:false（如 toolbar/statusbar）→ 不可拖拽。
  const targetIndex = resolveDragTarget(visiblePanes.value, visibleIndex)
  if (targetIndex === null) return

  const targetPane = visiblePanes.value[targetIndex]
  const targetEl = paneElements.value[targetIndex]
  if (!targetPane || !targetEl) return

  activeResizer.value = pane._originalIndex

  const startPos = e[prop]
  const startSize = targetEl.getBoundingClientRect()[sizeProp]
  const minSize = paneMin(targetPane)
  const maxSize = paneMax(targetPane)

  // Determine resize direction
  // If resizing the next pane (right/bottom), delta should be inverted
  const isResizingNextPane = targetIndex !== visibleIndex

  function onMove(ev) {
    ev.preventDefault()
    const delta = ev[prop] - startPos

    // Invert delta if resizing right/bottom pane
    const effectiveDelta = isResizingNextPane ? -delta : delta
    const newSize = Math.max(minSize, Math.min(maxSize, startSize + effectiveDelta))

    // Update DOM directly for jank-free dragging
    if (isHorizontal) {
      targetEl.style.width = `${newSize}px`
    } else {
      targetEl.style.height = `${newSize}px`
    }

    // Persist size so it survives re-renders
    runtimeSizes.value[targetPane.id] = newSize
  }

  function onUp() {
    activeResizer.value = -1
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    window.removeEventListener('mousemove', onMove, true)
    window.removeEventListener('mouseup', onUp, true)
    _cleanupDrag = null
    emit('size-changed')
  }

  // Remove previous leftover listeners (safety)
  _cleanupDrag?.()
  _cleanupDrag = onUp

  document.body.style.cursor = cursor
  document.body.style.userSelect = 'none'
  window.addEventListener('mousemove', onMove, { passive: false, capture: true })
  window.addEventListener('mouseup', onUp, { passive: false, capture: true })
}

onBeforeUnmount(() => {
  _cleanupDrag?.()
  _cleanupDrag = null
})
</script>

<style scoped>
/* ── Container ── */
.split-panel {
  display: flex;
  overflow: hidden;
  width: 100%;
  height: 100%;
}

.split-horizontal {
  flex-direction: row;
}

.split-vertical {
  flex-direction: column;
}

/* ── Pane wrapper ── */
.split-pane {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  position: relative;
}

.split-pane.is-flex {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.split-pane.is-fixed {
  flex-shrink: 0;
}

/* ── Pane header ── */
.split-pane-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  min-height: 28px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.8px;
  color: var(--text-muted);
  text-transform: uppercase;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}

/* ── Pane body ── */
.split-pane-body {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}

/* ── Resizer ──
   视觉与命中解耦（方案 A）：
   - 本体 = 1px 发丝线（--split-hairline），常态取边框色 --border（不再是「露容器底色」）；
   - 命中区 = 伪元素 ::after（--split-hit），向两侧各外溢 (hit-hairline)/2，常态透明；
   - hover / 拖拽激活（.is-active）只给伪元素上 --accent → 变粗为 5px 蓝块；本体恒 1px，
     故拖拽瞬间不重排、不抖动（拖拽数学走 pane 的 getBoundingClientRect，与本宽度无关）。 */
.split-resizer {
  flex-shrink: 0;
  z-index: 1;
  position: relative;
  background: var(--border);
  transition: background 0.12s;
}

.split-resizer.resizer-horizontal {
  width: var(--split-hairline);
  cursor: col-resize;
}

.split-resizer.resizer-vertical {
  height: var(--split-hairline);
  cursor: row-resize;
}

/* 命中区：绝对定位伪元素，不占布局、不改变 1px 视觉 */
.split-resizer::after {
  content: '';
  position: absolute;
  background: transparent;
  transition: background 0.12s;
}

.split-resizer.resizer-horizontal::after {
  top: 0;
  bottom: 0;
  left: calc((var(--split-hairline) - var(--split-hit)) / 2);
  right: calc((var(--split-hairline) - var(--split-hit)) / 2);
}

.split-resizer.resizer-vertical::after {
  left: 0;
  right: 0;
  top: calc((var(--split-hairline) - var(--split-hit)) / 2);
  bottom: calc((var(--split-hairline) - var(--split-hit)) / 2);
}

.split-resizer:hover::after,
.split-resizer.is-active::after {
  background: var(--accent);
}
</style>
