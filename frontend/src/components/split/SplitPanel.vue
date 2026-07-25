<template>
  <div
    class="split-panel"
    :class="[`split-${direction}`, { 'is-dragging': activeResizer >= 0 }]"
    :style="{ '--split-gap': `${gap}px` }"
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

      <!-- Resizer (between visible panes, only if both are resizable and non-flex) -->
      <div
        v-if="shouldShowResizer(i)"
        class="split-resizer"
        :class="[
          `resizer-${direction}`,
          { 'is-active': activeResizer === pane._originalIndex }
        ]"
        :style="{ background: gapColor }"
        @mousedown.prevent="startDrag(i, $event)"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onUpdated, onBeforeUnmount } from 'vue'

// ── Types ──────────────────────────────────────────────────────

export interface PaneConfig {
  id: string
  size?: number
  min?: number
  max?: number
  resizable?: boolean
  visible?: boolean
  flex?: boolean
  header?: string
  /** Internal: original index in input panes array (for slot mapping) */
  _originalIndex?: number
}

// ── Props ──────────────────────────────────────────────────────

const props = withDefaults(defineProps<{
  direction?: 'horizontal' | 'vertical'
  gap?: number
  gapColor?: string
  panes: PaneConfig[]
}>(), {
  direction: 'horizontal',
  gap: 4,
  gapColor: 'transparent',
})

// ── Refs ───────────────────────────────────────────────────────

const containerRef = ref<HTMLElement | null>(null)
const paneElements = ref<(HTMLElement | null)[]>([])
const activeResizer = ref(-1)

/**
 * Runtime sizes keyed by pane id.
 * We keep this separate from props to avoid mutating prop objects.
 */
const runtimeSizes = ref<Record<string, number>>({})

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
  const sig = JSON.stringify(visiblePanes.value.map(p => p.name))
  if (sig !== prevPaneSig) {
    paneElements.value = []
  }
  prevPaneSig = sig
})

/**
 * Check if resizer should be shown between pane[i] and pane[i+1].
 * Conditions:
 * 1. i is not the last pane
 * 2. At least one of the two panes is resizable
 */
function shouldShowResizer(i: number) {
  if (i >= visiblePanes.value.length - 1) return false

  const currentPane = visiblePanes.value[i]
  const nextPane = visiblePanes.value[i + 1]

  // Show resizer if at least one pane is resizable
  // This allows resizing fixed panes next to flex panes
  return currentPane.resizable !== false || nextPane.resizable !== false
}

// ── Pane ref management ───────────────────────────────────────

function setPaneRef(el: unknown, index: number) {
  paneElements.value[index] = el as HTMLElement | null
}

// ── Style helpers ──────────────────────────────────────────────

function getPaneStyle(pane: PaneConfig, visibleIndex: number) {
  const h = props.direction === 'horizontal'

  if (pane.flex) {
    return {
      flex: '1',
      minWidth: '0',
      minHeight: '0',
    }
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
  const minVal = pane.min ?? 100
  const maxVal = pane.max ?? 2000

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

let _cleanupDrag: (() => void) | null = null

function startDrag(visibleIndex: number, e: MouseEvent) {
  e.preventDefault()

  const isHorizontal = props.direction === 'horizontal'
  const prop = isHorizontal ? 'clientX' : 'clientY'
  const sizeProp = isHorizontal ? 'width' : 'height'
  const cursor = isHorizontal ? 'col-resize' : 'row-resize'

  const pane = visiblePanes.value[visibleIndex]
  if (!pane) return

  const paneEl = paneElements.value[visibleIndex]
  if (!paneEl) return

  // Determine which pane to resize
  // If current pane is flex, resize the next pane (fixed)
  // Otherwise, resize current pane (fixed)
  let targetIndex = visibleIndex
  if (pane.flex && visibleIndex < visiblePanes.value.length - 1) {
    targetIndex = visibleIndex + 1
  }

  const targetPane = visiblePanes.value[targetIndex]
  const targetEl = paneElements.value[targetIndex]
  if (!targetPane || !targetEl) return

  // Don't resize if target is flex (can happen if both are flex)
  if (targetPane.flex) return

  activeResizer.value = pane._originalIndex!

  const startPos = e[prop]
  const startSize = targetEl.getBoundingClientRect()[sizeProp]
  const minSize = targetPane.min ?? 100
  const maxSize = targetPane.max ?? 2000

  // Determine resize direction
  // If resizing the next pane (right/bottom), delta should be inverted
  const isResizingNextPane = targetIndex !== visibleIndex

  function onMove(ev: MouseEvent) {
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

/* ── Resizer ── */
.split-resizer {
  flex-shrink: 0;
  transition: background 0.12s;
  z-index: 1;
  position: relative;
}

.split-resizer.resizer-horizontal {
  width: var(--split-gap, 4px);
  cursor: col-resize;
}

.split-resizer.resizer-vertical {
  height: var(--split-gap, 4px);
  cursor: row-resize;
}

.split-resizer:hover {
  background: var(--accent) !important;
}

.split-resizer.is-active {
  background: var(--accent) !important;
}

/* Slightly larger hit area during drag */
.split-panel.is-dragging .split-resizer.resizer-horizontal {
  width: calc(var(--split-gap, 4px) + 2px);
}

.split-panel.is-dragging .split-resizer.resizer-vertical {
  height: calc(var(--split-gap, 4px) + 2px);
}
</style>
