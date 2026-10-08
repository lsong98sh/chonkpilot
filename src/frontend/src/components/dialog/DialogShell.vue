<template>
    <!-- Modal overlay -->
    <div
      v-if="resolvedOptions.modal && state !== 'minimized'"
      class="dialog-overlay"
      :data-dialog-id="dialogId"
      :style="{ zIndex: currentZIndex - 1 }"
      @click="onOverlayClick"
    />

    <!-- Dialog shell -->
    <div
      ref="dialogRef"
      :data-dialog-id="dialogId"
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
        v-mq:[EventNames.dialogMaximize].dblclick
      >
        <span class="dialog-title">{{ title }}</span>
        <div class="dialog-header-buttons">
          <Button
            v-if="resolvedOptions.collapsible"
            class="dialog-btn"
            text
            size="mini"
            :title="state === 'collapsed' ? $t('dialog.expand') : $t('dialog.collapse')"
            v-mq:[EventNames.dialogToggleCollapse].click.stop
          >
            <svg v-if="state === 'collapsed'" width="10" height="10" viewBox="0 0 12 12"><rect x="1.5" y="1.5" width="9" height="9" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
            <svg v-else width="10" height="10" viewBox="0 0 12 12"><rect x="1" y="5.5" width="10" height="1" fill="currentColor"/></svg>
          </Button>
          <Button
            v-if="resolvedOptions.minimizable"
            class="dialog-btn"
            text
            size="mini"
            :title="$t('dialog.minimize')"
            v-mq:[EventNames.dialogMinimize].click.stop
          >
            <svg width="10" height="10" viewBox="0 0 12 12"><rect x="1" y="5.5" width="10" height="1" fill="currentColor"/></svg>
          </Button>
          <Button
            v-if="resolvedOptions.maximizable && state !== 'maximized'"
            class="dialog-btn"
            text
            size="mini"
            :title="$t('dialog.maximize')"
            v-mq:[EventNames.dialogMaximize].click.stop
          >
            <svg width="10" height="10" viewBox="0 0 12 12"><rect x="1.5" y="1.5" width="9" height="9" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
          </Button>
          <Button
            v-else-if="resolvedOptions.maximizable"
            class="dialog-btn"
            text
            size="mini"
            :title="$t('dialog.restore')"
            v-mq:[EventNames.dialogRestore].click.stop
          >
            <svg width="10" height="10" viewBox="0 0 12 12">
              <rect x="3.5" y="0.5" width="8" height="8" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/>
              <path d="M3.5 3.5H1.5a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1v-2" fill="none" stroke="currentColor" stroke-width="1.2"/>
            </svg>
          </Button>
          <Button
            v-if="resolvedOptions.closable"
            class="dialog-btn dialog-btn-close"
            text
            size="mini"
            :title="$t('dialog.close')"
            v-mq:[EventNames.dialogClose].click.stop
          >
            <svg width="10" height="10" viewBox="0 0 12 12"><path d="M1 1l10 10M11 1L1 11" stroke="currentColor" stroke-width="1.4" fill="none"/></svg>
          </Button>
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

      <!-- Footer（可选具名插槽）：固定在 .dialog-body 之外、不随内容滚动。
           缺省（调用方未提供 footer 插槽）不渲染任何多余 DOM（向后兼容既有弹窗）。 -->
      <div v-if="$slots.footer" class="dialog-footer">
        <slot name="footer" />
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
        v-mq:[EventNames.dialogRestore].click.stop
      >
        <span class="dialog-minimized-title">{{ title }}</span>
      </div>
    </div>
</template>

<script setup>
import { ref, computed, reactive, onMounted, onUnmounted } from 'vue'
import { Button } from '../ui'
import { DefaultDialogOptions } from '../../types/dialog'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const props = defineProps({
  dialogId: { type: String, required: true },
  options: { type: Object, required: true },
  component: { type: [Object, Function], default: null },
  componentProps: { type: Object, default: null },
  zIndex: { type: Number, required: true },
})

const emit = defineEmits(['close', 'collapse', 'expand', 'maximize', 'minimize', 'restore', 'move', 'resize', 'open', 'closed'])

// Resolve options with defaults
const resolvedOptions = computed(() => ({
  ...DefaultDialogOptions,
  ...props.options,
}))

// 标题独立 ref，setTitle 可响应式更新
const title = ref(resolvedOptions.value.title)

// State
const state = ref('normal')
const currentZIndex = ref(props.zIndex)
const position = reactive({ x: 0, y: 0 })
const size = reactive({
  width: resolvedOptions.value.width || 640,
  height: resolvedOptions.value.height || 'auto',
})

const dialogRef = ref(null)
const headerRef = ref(null)
const bodyRef = ref(null)

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

}

initPosition()

// 视口内自动居中：initPosition 只能按声明尺寸估算（height 常为 auto/默认 400），
// 实测（含 maxHeight clamp 后）的真实高度后重新居中，确保 rect.bottom ≤ 视口高、rect.top ≥ 0。
// 用户一旦拖拽即停止自动校正（避免与其位置操作打架）。
let autoCentered = !resolvedOptions.value.position

function centerByMeasuredSize() {
  if (!autoCentered) return
  if (state.value !== 'normal') return
  const el = dialogRef.value
  if (!el) return
  const w = el.offsetWidth
  const h = el.offsetHeight
  if (!w || !h) return
  const margin = 20
  position.x = Math.max(0, Math.round((window.innerWidth - w) / 2))
  // clamp：top 不小于安全边距，且保证下缘不越界
  const centeredY = Math.round((window.innerHeight - h) / 2)
  const maxTop = Math.max(margin, window.innerHeight - h - margin)
  position.y = Math.min(Math.max(margin, centeredY), maxTop)
}

// Computed shell style
const shellStyle = computed(() => {
  const base = {
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
    // 视口约束：杜绝内容超高/超宽时对话框溢出窗口（配合 .dialog-body 的 overflow:auto 内滚动）
    base.maxWidth = typeof resolvedOptions.value.maxWidth === 'number' ? `${resolvedOptions.value.maxWidth}px` : resolvedOptions.value.maxWidth
    // 高度上限再叠加「视口高 − 安全边距(40px)」：无论 options 传什么，弹窗都不会顶出窗口下缘；
    // 表头固定、仅 .dialog-body 内部滚动（overflow:auto）。
    const optMaxH = resolvedOptions.value.maxHeight
    const safeMaxH = 'calc(100vh - 40px)'
    base.maxHeight = optMaxH
      ? `min(${typeof optMaxH === 'number' ? `${optMaxH}px` : optMaxH}, ${safeMaxH})`
      : safeMaxH
  }

  return base
})

// --- Drag ---
let dragging = false
let dragStartX = 0
let dragStartY = 0
let dragOrigX = 0
let dragOrigY = 0

function onHeaderMouseDown(e) {
  // 折叠态（collapsed）仅剩标题栏，也必须允许拖拽；最小化态（minimized）不拖。
  if (state.value === 'minimized' || !resolvedOptions.value.draggable) return
  // Ignore clicks on buttons
  const target = e.target
  if (target.closest('.dialog-header-buttons')) return

  // 用户开始拖拽 → 停止自动居中，尊重用户位置
  autoCentered = false
  dragging = true
  dragStartX = e.clientX
  dragStartY = e.clientY
  dragOrigX = position.x
  dragOrigY = position.y

  document.addEventListener('mousemove', onDragMove, { passive: true })
  document.addEventListener('mouseup', onDragEnd, { once: true })
}

function onDragMove(e) {
  if (!dragging) return
  const el = dialogRef.value
  const vw = window.innerWidth
  const vh = window.innerHeight
  const w = el ? el.offsetWidth : 0
  const h = el ? el.offsetHeight : 0
  const rawX = dragOrigX + (e.clientX - dragStartX)
  const rawY = dragOrigY + (e.clientY - dragStartY)
  // 视口钳制（E-10）：横向允许左右半出界但至少保留 60px 可见（便于拖回）；
  // 纵向保证表头（拖拽把手，位于弹窗顶部）始终可见（top ≥ 0）且下缘不越界。
  position.x = Math.max(-(w - 60), Math.min(vw - 60, rawX))
  position.y = Math.max(0, Math.min(vh - 60, rawY))
  emit('move', { x: position.x, y: position.y })
}

function onDragEnd() {
  dragging = false
  document.removeEventListener('mousemove', onDragMove)
}

// --- Resize ---
let resizing = false
let resizeStartX = 0
let resizeStartY = 0
let resizeOrigW = 0
let resizeOrigH = 0

function onResizeMouseDown(e) {
  e.stopPropagation()
  resizing = true
  resizeStartX = e.clientX
  resizeStartY = e.clientY
  resizeOrigW = typeof size.width === 'number' ? size.width : 640
  resizeOrigH = typeof size.height === 'number' ? size.height : 400

  document.addEventListener('mousemove', onResizeMove, { passive: false, capture: true })
  document.addEventListener('mouseup', onResizeEnd, { once: true })
}

function onResizeMove(e) {
  if (!resizing) return
  e.preventDefault()
  const minW = resolvedOptions.value.minWidth || 300
  const minH = resolvedOptions.value.minHeight || 200
  // 尺寸上限 = 视口（E-10：原先只有下限，可拖到超出视口）。
  const newW = Math.max(minW, Math.min(window.innerWidth, resizeOrigW + (e.clientX - resizeStartX)))
  const newH = Math.max(minH, Math.min(window.innerHeight, resizeOrigH + (e.clientY - resizeStartY)))
  size.width = newW
  size.height = newH
  emit('resize', { width: newW, height: newH })
}

function onResizeEnd() {
  resizing = false
  document.removeEventListener('mousemove', onResizeMove, true)
}

// --- State transitions ---
function toggleCollapse() {
  if (state.value === 'collapsed') {
    state.value = 'normal'
    emit('expand')
  } else {
    state.value = 'collapsed'
    emit('collapse')
  }
}

function maximize() {
  state.value = 'maximized'
  emit('maximize')
}

function minimize() {
  state.value = 'minimized'
  emit('minimize')
}

function restore() {
  state.value = 'normal'
  emit('restore')
}

function toggleMaximize() {
  if (!resolvedOptions.value.maximizable) return
  if (state.value === 'maximized') {
    restore()
  } else if (state.value === 'normal') {
    maximize()
  }
}

function onClose() {
  emit('close')
}

function onOverlayClick() {
  // Overlay click does nothing by default — closable controls the X button
}

// Focus on mount
let resizeObserver = null

onMounted(() => {
  dialogRef.value?.focus()
  // 首帧先居中一次；异步组件/内容渲染后尺寸还会变 → 用 ResizeObserver 持续按实测尺寸校正
  centerByMeasuredSize()
  if (typeof ResizeObserver !== 'undefined' && dialogRef.value) {
    resizeObserver = new ResizeObserver(() => centerByMeasuredSize())
    resizeObserver.observe(dialogRef.value)
  }
  window.addEventListener('resize', centerByMeasuredSize)
  emit('open')
  unsubs.push(mq.on(EventNames.dialogToggleCollapse, toggleCollapse))
  unsubs.push(mq.on(EventNames.dialogMinimize, minimize))
  unsubs.push(mq.on(EventNames.dialogMaximize, () => {
    if (resolvedOptions.value.maximizable) toggleMaximize()
  }))
  unsubs.push(mq.on(EventNames.dialogRestore, restore))
  unsubs.push(mq.on(EventNames.dialogClose, onClose))
})

const unsubs = []

// Cleanup
onUnmounted(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
  window.removeEventListener('resize', centerByMeasuredSize)
  document.removeEventListener('mousemove', onDragMove)
  document.removeEventListener('mouseup', onDragEnd)
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', onResizeEnd)
  for (const fn of unsubs) fn()
  unsubs.length = 0
  emit('closed')
})

// Expose methods for parent
function setTitle(newTitle) {
  title.value = newTitle
}

function getState() {
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
  /* 亮色默认值（dark/nord 在文件末尾成组覆写） */
  --dialog-overlay-bg: rgba(0, 0, 0, 0.3);
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background: var(--dialog-overlay-bg);
  pointer-events: auto;
}

.dialog-shell {
  /* 主题变量默认值（light）：一律引全局主题 token，var() 内不写兜底（口径见 20-gui §12.8）；
     .dialog-overlay 与 .dialog-shell 是兄弟节点，需各自声明（本规则内的变量同时被
     子节点 .dialog-minimized-bar 继承）。 */
  --dialog-bg: var(--panel-bg);
  --dialog-border: var(--border);
  --dialog-header-bg: var(--bg-primary);
  --dialog-btn-hover-bg: var(--bg-hover);
  --text-color: var(--text-primary);
  --dialog-radius: 8px;
  --dialog-shadow: 0 4px 20px rgba(0, 0, 0, 0.15);
  --dialog-header-padding: 12px 16px;
  --dialog-body-padding: 16px;
  --font-family: var(--font-sans);
  --font-size: 14px;
  display: flex;
  flex-direction: column;
  background: var(--dialog-bg);
  border: 1px solid var(--dialog-border);
  border-radius: var(--dialog-radius);
  box-shadow: var(--dialog-shadow);
  overflow: hidden;
  font-family: var(--font-family);
  font-size: var(--font-size);
  color: var(--text-color);
}

/* 视口安全上限（兜底）：即使 options.maxHeight 缺省/不支持 min()，弹窗也不会顶出窗口；
   仅内容区 .dialog-body 滚动，表头固定。maximized 态由 inline style 铺满，不受此限。 */
.dialog-shell:not(.dialog-state-maximized) {
  max-height: calc(100vh - 40px);
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--dialog-header-padding);
  background: var(--dialog-header-bg);
  border-bottom: 1px solid var(--dialog-border);
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
  background: var(--dialog-btn-hover-bg);
}

/* 危险实心填充：文字用主题最底层色（light = 纯白，与历史 #fff 一致；dark/nord = 深色） */
.dialog-btn-close:hover {
  background: var(--danger);
  color: var(--bg-secondary);
}

.dialog-body {
  flex: 1;
  overflow: auto;
  padding: var(--dialog-body-padding);
  min-height: 0;
}

.dialog-footer {
  padding: 12px 16px;
  border-top: 1px solid var(--dialog-border);
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
  background: var(--dialog-header-bg);
  border: 1px solid var(--dialog-border);
  border-radius: var(--dialog-radius);
  cursor: pointer;
  user-select: none;
}

.dialog-minimized-title {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 暗色主题支持：主题以 documentElement 的 data-theme 属性切换（见 Toolbar.setTheme），
   页面不存在 .dark class，故必须用属性选择器；取值引用既有 token，避免另造一套色。
   .dialog-overlay 与 .dialog-shell 是兄弟节点，需各自命中。 */
[data-theme="dark"] .dialog-shell,
[data-theme="dark"] .dialog-overlay,
[data-theme="nord"] .dialog-shell,
[data-theme="nord"] .dialog-overlay {
  --dialog-bg: var(--panel-bg);
  --dialog-border: var(--border);
  --dialog-header-bg: var(--bg-secondary);
  --dialog-overlay-bg: rgba(0, 0, 0, 0.5);
  --dialog-btn-hover-bg: var(--bg-hover);
  --text-color: var(--text-primary);
}
</style>
