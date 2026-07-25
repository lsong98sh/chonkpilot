<template>
  <teleport to="body">
    <!-- Modal overlay -->
    <div
      v-if="resolvedOptions.modal && state !== 'minimized'"
      class="dialog-overlay"
      :style="{ zIndex: currentZIndex - 1 }"
      @click="onOverlayClick"
    />

    <!-- Dialog shell -->
    <div
      ref="dialogRef"
      class="dialog-shell"
      :class="[
        `dialog-state-${state}`,
        resolvedOptions.class,
        { 'dialog-modal': resolvedOptions.modal }
      ]"
      :style="shellStyle"
    >
      <!-- Header -->
      <div
        ref="headerRef"
        class="dialog-header"
        :class="resolvedOptions.headerClass"
        @mousedown.prevent="onHeaderMouseDown"
        @dblclick="toggleMaximize"
      >
        <span class="dialog-title">{{ resolvedOptions.title }}</span>
        <div class="dialog-header-buttons">
          <button
            v-if="resolvedOptions.collapsible"
            class="dialog-btn"
            :title="state === 'collapsed' ? '展开' : '折叠'"
            @click.stop="toggleCollapse"
          >
            {{ state === 'collapsed' ? '□' : '━' }}
          </button>
          <button
            v-if="resolvedOptions.minimizable"
            class="dialog-btn"
            title="最小化"
            @click.stop="minimize"
          >
            _
          </button>
          <button
            v-if="state !== 'maximized'"
            class="dialog-btn"
            title="最大化"
            @click.stop="maximize"
          >
            □
          </button>
          <button
            v-else
            class="dialog-btn"
            title="还原"
            @click.stop="restore"
          >
            ❐
          </button>
          <button
            v-if="resolvedOptions.closable"
            class="dialog-btn dialog-btn-close"
            title="关闭"
            @click.stop="onClose"
          >
            ✕
          </button>
        </div>
      </div>

      <!-- Body -->
      <div
        v-show="state !== 'collapsed' && state !== 'minimized'"
        class="dialog-body"
        :class="resolvedOptions.bodyClass"
        ref="bodyRef"
      >
        <component :is="component" v-bind="componentProps" />
        <slot />
      </div>

      <!-- Resize handle -->
      <div
        v-if="resolvedOptions.resizable && state === 'normal'"
        class="dialog-resize-handle"
        @mousedown.prevent="onResizeMouseDown"
      />

      <!-- Minimized bar (non-modal only) -->
      <div
        v-if="state === 'minimized'"
        class="dialog-minimized-bar"
        @click.stop="restore"
      >
        <span class="dialog-minimized-title">{{ resolvedOptions.title }}</span>
      </div>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { ref, computed, reactive, onMounted, onUnmounted, markRaw, type Component } from 'vue'
import type { DialogOptions, DialogPosition, DialogState } from '../../types/dialog'
import { DefaultDialogOptions } from '../../types/dialog'

const props = defineProps<{
  dialogId: string
  options: DialogOptions
  component?: Component
  componentProps?: Record<string, any>
  zIndex: number
  onDialogAction?: (action: string, payload?: any) => void
}>()

const emit = defineEmits<{
  (e: 'action', action: string, payload?: any): void
}>()

// Resolve options with defaults
const resolvedOptions = computed<Required<DialogOptions>>(() => ({
  ...DefaultDialogOptions,
  ...props.options,
}) as Required<DialogOptions>)

// State
const state = ref<DialogState>('normal')
const currentZIndex = ref(props.zIndex)
const position = reactive<DialogPosition>({ x: 0, y: 0 })
const size = reactive<{ width: number | string; height: number | string }>({
  width: resolvedOptions.value.width || 640,
  height: resolvedOptions.value.height || 'auto',
})

const dialogRef = ref<HTMLElement | null>(null)
const headerRef = ref<HTMLElement | null>(null)
const bodyRef = ref<HTMLElement | null>(null)

// Initialize position
function initPosition() {
  if (resolvedOptions.value.position) {
    position.x = resolvedOptions.value.position.x
    position.y = resolvedOptions.value.position.y
  } else {
    // Center
    const w = typeof size.width === 'number' ? size.width : 640
    const h = typeof size.height === 'number' ? size.height : 400
    position.x = Math.max(0, (window.innerWidth - w) / 2)
    position.y = Math.max(20, (window.innerHeight - h) / 2)
  }

  // Try to restore persisted position
  try {
    const stored = localStorage.getItem('chonkpilot_dialog_positions')
    if (stored) {
      const all = JSON.parse(stored)
      const saved = all[props.dialogId]
      if (saved) {
        if (saved.x >= 0 && saved.x < window.innerWidth - 100) position.x = saved.x
        if (saved.y >= 0 && saved.y < window.innerHeight - 50) position.y = saved.y
        if (saved.w && typeof size.width === 'number') size.width = saved.w
        if (saved.h && typeof size.height === 'number') size.height = saved.h
      }
    }
  } catch { /* ignore */ }
}

initPosition()

// Computed shell style
const shellStyle = computed(() => {
  const base: Record<string, string> = {
    zIndex: String(currentZIndex.value),
  }

  if (state.value === 'maximized') {
    base.position = 'fixed'
    base.top = '8px'
    base.left = '8px'
    base.right = '8px'
    base.bottom = '8px'
    base.width = 'auto'
    base.height = 'auto'
  } else if (state.value === 'minimized') {
    base.position = 'fixed'
    base.bottom = '0px'
    base.left = '0px'
    base.width = '200px'
    base.height = 'auto'
  } else {
    base.position = 'fixed'
    base.left = `${position.x}px`
    base.top = `${position.y}px`
    base.width = typeof size.width === 'number' ? `${size.width}px` : size.width
    base.height = state.value === 'collapsed'
      ? 'auto'
      : (typeof size.height === 'number' ? `${size.height}px` : size.height)
  }

  return base
})

// --- Drag ---
let dragging = false
let dragStartX = 0
let dragStartY = 0
let dragOrigX = 0
let dragOrigY = 0

function onHeaderMouseDown(e: MouseEvent) {
  if (state.value !== 'normal' || !resolvedOptions.value.draggable) return
  // Ignore clicks on buttons
  const target = e.target as HTMLElement
  if (target.closest('.dialog-header-buttons')) return

  dragging = true
  dragStartX = e.clientX
  dragStartY = e.clientY
  dragOrigX = position.x
  dragOrigY = position.y

  document.addEventListener('mousemove', onDragMove, { passive: true })
  document.addEventListener('mouseup', onDragEnd, { once: true })
}

function onDragMove(e: MouseEvent) {
  if (!dragging) return
  position.x = dragOrigX + (e.clientX - dragStartX)
  position.y = dragOrigY + (e.clientY - dragStartY)
  emit('action', 'move', { x: position.x, y: position.y })
  props.onDialogAction?.('move', { x: position.x, y: position.y })
}

function onDragEnd() {
  dragging = false
  document.removeEventListener('mousemove', onDragMove)
  savePosition()
}

// --- Resize ---
let resizing = false
let resizeStartX = 0
let resizeStartY = 0
let resizeOrigW = 0
let resizeOrigH = 0

function onResizeMouseDown(e: MouseEvent) {
  e.stopPropagation()
  resizing = true
  resizeStartX = e.clientX
  resizeStartY = e.clientY
  resizeOrigW = typeof size.width === 'number' ? size.width : 640
  resizeOrigH = typeof size.height === 'number' ? size.height : 400

  document.addEventListener('mousemove', onResizeMove, { passive: true })
  document.addEventListener('mouseup', onResizeEnd, { once: true })
}

function onResizeMove(e: MouseEvent) {
  if (!resizing) return
  const newW = Math.max(resolvedOptions.value.minWidth || 300, resizeOrigW + (e.clientX - resizeStartX))
  const newH = Math.max(resolvedOptions.value.minHeight || 200, resizeOrigH + (e.clientY - resizeStartY))
  size.width = newW
  size.height = newH
  emit('action', 'resize', { width: newW, height: newH })
  props.onDialogAction?.('resize', { width: newW, height: newH })
}

function onResizeEnd() {
  resizing = false
  document.removeEventListener('mousemove', onResizeMove)
  savePosition()
}

// --- State transitions ---
function toggleCollapse() {
  if (state.value === 'collapsed') {
    state.value = 'normal'
    emit('action', 'restore')
    props.onDialogAction?.('restore')
  } else {
    state.value = 'collapsed'
    emit('action', 'collapse')
    props.onDialogAction?.('collapse')
  }
}

function maximize() {
  state.value = 'maximized'
  emit('action', 'maximize')
  props.onDialogAction?.('maximize')
}

function minimize() {
  state.value = 'minimized'
  emit('action', 'minimize')
  props.onDialogAction?.('minimize')
}

function restore() {
  state.value = 'normal'
  emit('action', 'restore')
  props.onDialogAction?.('restore')
}

function toggleMaximize() {
  if (state.value === 'maximized') {
    restore()
  } else if (state.value === 'normal') {
    maximize()
  }
}

function onClose() {
  emit('action', 'close')
  props.onDialogAction?.('close')
}

function onOverlayClick() {
  // Overlay click does nothing by default — closable controls the X button
}

// Persist position
function savePosition() {
  try {
    const stored = localStorage.getItem('chonkpilot_dialog_positions')
    const all = stored ? JSON.parse(stored) : {}
    all[props.dialogId] = {
      x: position.x,
      y: position.y,
      w: typeof size.width === 'number' ? size.width : undefined,
      h: typeof size.height === 'number' ? size.height : undefined,
    }
    localStorage.setItem('chonkpilot_dialog_positions', JSON.stringify(all))
  } catch { /* ignore */ }
}

// Focus on mount
onMounted(() => {
  dialogRef.value?.focus()
  emit('action', 'open')
  props.onDialogAction?.('open')
})

// Cleanup
onUnmounted(() => {
  document.removeEventListener('mousemove', onDragMove)
  document.removeEventListener('mouseup', onDragEnd)
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', onResizeEnd)
  emit('action', 'closed')
  props.onDialogAction?.('closed')
})

// Expose methods for parent
function setTitle(title: string) {
  resolvedOptions.value.title = title
}

function getState(): DialogState {
  return state.value
}

function collapse() {
  if (state.value !== 'collapsed') toggleCollapse()
}

function expand() {
  if (state.value === 'collapsed') toggleCollapse()
}

defineExpose({
  setTitle,
  getState,
  collapse,
  expand,
  minimize,
  maximize,
  restore,
  close: onClose,
})
</script>

<style scoped>
.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background: var(--dialog-overlay-bg, rgba(0, 0, 0, 0.3));
  pointer-events: auto;
}

.dialog-shell {
  display: flex;
  flex-direction: column;
  background: var(--dialog-bg, #fff);
  border: 1px solid var(--dialog-border, #dcdfe6);
  border-radius: var(--dialog-radius, 8px);
  box-shadow: var(--dialog-shadow, 0 4px 20px rgba(0, 0, 0, 0.15));
  overflow: hidden;
  font-family: var(--font-family, inherit);
  font-size: var(--font-size, 14px);
  color: var(--text-color, #303133);
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--dialog-header-padding, 12px 16px);
  background: var(--dialog-header-bg, #f5f7fa);
  border-bottom: 1px solid var(--dialog-border, #dcdfe6);
  cursor: default;
  user-select: none;
  flex-shrink: 0;
}

.dialog-title {
  font-weight: 600;
  font-size: 15px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dialog-header-buttons {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.dialog-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-secondary, #909399);
  font-size: 14px;
  cursor: pointer;
  border-radius: 4px;
  transition: background 0.15s;
}

.dialog-btn:hover {
  background: var(--dialog-btn-hover-bg, #e4e7ed);
}

.dialog-btn-close:hover {
  background: var(--danger-color, #f56c6c);
  color: #fff;
}

.dialog-body {
  flex: 1;
  overflow: auto;
  padding: var(--dialog-body-padding, 16px);
  min-height: 0;
}

.dialog-footer {
  padding: 12px 16px;
  border-top: 1px solid var(--dialog-border, #dcdfe6);
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  flex-shrink: 0;
}

.dialog-resize-handle {
  position: absolute;
  right: 0;
  bottom: 0;
  width: 14px;
  height: 14px;
  cursor: nwse-resize;
  background: linear-gradient(135deg, transparent 50%, var(--text-secondary, #909399) 50%);
  border-radius: 0 0 8px 0;
}

.dialog-minimized-bar {
  display: flex;
  align-items: center;
  padding: 6px 12px;
  background: var(--dialog-header-bg, #f5f7fa);
  border: 1px solid var(--dialog-border, #dcdfe6);
  border-radius: var(--dialog-radius, 8px);
  cursor: pointer;
  user-select: none;
}

.dialog-minimized-title {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Dark theme support */
:root.dark .dialog-shell,
.dialog-shell.dark {
  --dialog-bg: #2c2c2c;
  --dialog-border: #4c4c4c;
  --dialog-header-bg: #363636;
  --dialog-overlay-bg: rgba(0, 0, 0, 0.5);
  --text-color: #e0e0e0;
  --text-secondary: #a0a0a0;
  --dialog-btn-hover-bg: #505050;
}
</style>
