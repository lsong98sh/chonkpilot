# 前端拖拽改变尺寸

[meta]
uri=mcp://chonkpilot/resources/ui/frontend-drag-resize
mimetype=text/markdown

[description]
前端（Vue 3）拖拽改变面板尺寸的实现模式：handle 捕获 mousedown → window mousemove 计算增量并 clamp → mouseup 收尾。含完整 Resizer.vue 组件示例。

[content]
# 拖拽 Resizer（Vue 3）

## 设计要点（项目 Resizer 规范）

1. **handle 用真实 `<div>`**（不用 `::after`），占布局空间 `width/height: 4px`
2. `background: transparent`，hover 变 `var(--accent)`
3. `@mousedown` 用**闭包捕获 startX/startW**，不使用全局 ctx
4. `window mousemove` 设 `{ capture: true }`，**clamp 到阈值**
5. `window mouseup` 移除 listener，恢复 `cursor`/`userSelect`
6. 每次 `e.preventDefault() + e.stopPropagation()`
7. 被 resize 的容器加 `flex-shrink: 0`（防 flex 挤压吃掉尺寸）

## 组件示例

```vue
<template>
  <div
    class="resizer"
    :class="direction"
    @mousedown.prevent.stop="onMousedown"
  />
</template>

<script setup>
import { onBeforeUnmount } from 'vue'

const props = defineProps({
  direction: { type: String, default: 'h' }, // 'h'=水平改宽 'v'=垂直改高
  size:     { type: Number, required: true }, // 当前尺寸（父组件状态）
  min:      { type: Number, default: 100 },
  max:      { type: Number, default: 3000 },
})
const emit = defineEmits(['resize', 'resize-end'])

let dragging = false
let startPos = 0   // 起始鼠标坐标
let startSize = 0  // 起始尺寸
let onMove = null
let onUp = null

function onMousedown(e) {
  dragging = true
  startPos = props.direction === 'h' ? e.clientX : e.clientY
  startSize = props.size
  // 拖拽期间全局禁用文本选择 + 十字光标
  document.body.style.cursor = props.direction === 'h' ? 'col-resize' : 'row-resize'
  document.body.style.userSelect = 'none'

  onMove = (ev) => {
    if (!dragging) return
    ev.preventDefault()
    ev.stopPropagation()
    const pos = props.direction === 'h' ? ev.clientX : ev.clientY
    const delta = pos - startPos
    let next = startSize + delta
    if (next < props.min) next = props.min
    if (next > props.max) next = props.max
    emit('resize', next)
  }
  onUp = () => {
    if (!dragging) return
    dragging = false
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    window.removeEventListener('mousemove', onMove, true)
    window.removeEventListener('mouseup', onUp, true)
    emit('resize-end')
  }
  // capture: true 保证 handle 移出后仍收到事件；mouseup 在 window 上必收
  window.addEventListener('mousemove', onMove, { capture: true })
  window.addEventListener('mouseup', onUp, true)
}

onBeforeUnmount(() => {
  if (onMove) window.removeEventListener('mousemove', onMove, true)
  if (onUp) window.removeEventListener('mouseup', onUp, true)
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
})
</script>

<style scoped>
.resizer {
  flex-shrink: 0;
  background: transparent;
  transition: background 0.15s;
}
.resizer.h { width: 4px; cursor: col-resize; }
.resizer.v { height: 4px; cursor: row-resize; }
.resizer:hover { background: var(--accent); }
</style>
```

## 用法（父组件）

```vue
<div class="layout">
  <aside :style="{ width: sidebarWidth + 'px' }" class="pane">
    <!-- 面板内容 -->
  </aside>
  <Resizer direction="h" :size="sidebarWidth" @resize="v => sidebarWidth = v" />
  <main class="pane main"><!-- 主区，无需 flex-shrink:0，被挤的是 aside --></main>
</div>

<style>
.layout { display: flex; }
.pane { flex-shrink: 0; }
.pane.main { flex: 1; flex-shrink: 1; }
</style>
```

## 要点

- `:style` 数值必须显式加 `'px'`（`sidebarWidth + 'px'`），否则浏览器忽略
- 水平方向用 `clientX`、垂直用 `clientY`，delta 加到**起始尺寸**（不是当前值）避免累积漂移
- clamp 在 handle 内做，父组件只存状态
- 尺寸持久化：`resize-end` 时写 localStorage / 配置（重启恢复）
- 多 handle（filetree/preview/chat/tasktree 分隔）各自独立实例，互不干扰
