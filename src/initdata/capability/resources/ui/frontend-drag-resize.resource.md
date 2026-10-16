# 基本信息
```yaml
name: frontend-drag-resize
title: 前端拖拽改变尺寸
```

# 摘要
前端（Vue 3）拖拽改变面板尺寸的实现模式（**方案 A：视觉 / 命中解耦**）：本体恒 1px 发丝线、命中区为 5px 透明伪元素；handle 捕获 mousedown → window mousemove 计算增量并 clamp → mouseup 收尾。含完整 Resizer 组件示例。

# 正文
# 拖拽 Resizer（Vue 3）

## 设计要点（项目 Resizer 规范，方案 A：视觉/命中解耦）

1. **本体 = 真实 `<div>`**（`.split-resizer`），占布局空间 = `var(--split-hairline)`（1px）：水平 `width: var(--split-hairline)` + `cursor: col-resize`；垂直 `height: var(--split-hairline)` + `cursor: row-resize`
2. 本体常态 `background: var(--border)`（发丝线取**边框色**，非 `transparent`）
3. **命中区 = `::after` 绝对定位伪元素**：常态 `background: transparent`（**不占布局、不改 1px 视觉**），向两侧各外溢 `(hit − hairline)/2 ≈ 2px`，命中跨度 = `var(--split-hit)`（5px）
4. **hover / 拖拽激活（`.is-active`）只给 `::after` 填 `var(--accent)`**；**本体恒 1px、绝不加宽**（拖动瞬间不重排、不抖动）
5. `@mousedown` 用**闭包捕获 startX/startW**（`.stop.prevent` 阻冒泡 + 默认行为），不使用全局 ctx
6. `window mousemove` 设 `{ capture: true }`，**clamp 到阈值**后直改 DOM；`window mouseup` 移除 listener，恢复 `cursor`/`userSelect`
7. 拖拽数学走 pane 的 `getBoundingClientRect()`；被 resize 的 pane 加 `flex-shrink: 0`（防 flex 挤压吃掉尺寸）

## 组件示例

```vue
<template>
  <div
    class="split-resizer"
    :class="[direction, { 'is-active': dragging }]"
    @mousedown.stop.prevent="onMousedown"
  />
</template>

<script setup>
import { ref, onBeforeUnmount } from 'vue'

const props = defineProps({
  direction: { type: String, default: 'h' }, // 'h'=水平改宽 'v'=垂直改高
  size:     { type: Number, required: true }, // 当前尺寸（父组件状态）
  min:      { type: Number, default: 100 },
  max:      { type: Number, default: 3000 },
})
const emit = defineEmits(['resize', 'resize-end'])

const dragging = ref(false) // 仅用于切换 :class 的 is-active，不参与布局
let startPos = 0   // 起始鼠标坐标
let startSize = 0  // 起始尺寸
let onMove = null
let onUp = null

function onMousedown(e) {
  dragging.value = true
  startPos = props.direction === 'h' ? e.clientX : e.clientY
  startSize = props.size
  // 拖拽期间全局禁用文本选择 + 十字光标
  document.body.style.cursor = props.direction === 'h' ? 'col-resize' : 'row-resize'
  document.body.style.userSelect = 'none'

  onMove = (ev) => {
    if (!dragging.value) return
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
    if (!dragging.value) return
    dragging.value = false
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
/* 方案 A：本体恒 1px 发丝线（--border），命中区为 ::after 伪元素（不占布局，向两侧各外溢 ≈2px） */
.split-resizer {
  position: relative;
  flex-shrink: 0;
  background: var(--border);
}
.split-resizer.h { width: var(--split-hairline); cursor: col-resize; }
.split-resizer.v { height: var(--split-hairline); cursor: row-resize; }
.split-resizer::after {
  content: '';
  position: absolute;
  background: transparent;
}
.split-resizer.h::after {
  top: 0; bottom: 0;
  left: calc((var(--split-hairline) - var(--split-hit)) / 2);
  right: calc((var(--split-hairline) - var(--split-hit)) / 2);
}
.split-resizer.v::after {
  left: 0; right: 0;
  top: calc((var(--split-hairline) - var(--split-hit)) / 2);
  bottom: calc((var(--split-hairline) - var(--split-hit)) / 2);
}
/* hover / 拖拽激活只染命中区，本体恒 1px、绝不加宽 */
.split-resizer:hover::after,
.split-resizer.is-active::after { background: var(--accent); }
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

> `--split-hairline`（1px）与 `--split-hit`（5px）为 `variables.css` `:root` 提供的 token；旧 `--split-gap` 已移除。

## 要点

- `:style` 数值必须显式加 `'px'`（`sidebarWidth + 'px'`），否则浏览器忽略
- 水平方向用 `clientX`、垂直用 `clientY`，delta 加到**起始尺寸**（不是当前值）避免累积漂移
- clamp 在 handle 内做，父组件只存状态
- 尺寸持久化：`resize-end` 时写 localStorage / 配置（重启恢复）
- 多 handle（filetree/preview/chat/tasktree 分隔）各自独立实例，互不干扰
