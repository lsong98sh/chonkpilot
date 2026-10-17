<!--
  窗口边缘 resize 命中层（WIN-1 / G-59，2026-10-17）。

  桌面单体主窗口 = frameless（`src/lib/gui/window.go`：WM_NCCALCSIZE=0），WebView2 子窗
  （Chrome_RenderWidgetHostHWND）铺满整个客户区 → WM_NCHITTEST 恒 HTCLIENT，**系统层没有
  任何 resize 命中区**。故在此渲染最外圈 8 条透明命中条（4 边 6px / 4 角 12px）：
  hover 出 resize 光标，mousedown 发 gui.window.resize{hit} → 宿主 StartResize(HT*) 进入
  系统原生 resize 模态循环（设计见 `src/lib/go-webview2/FORK.md` P4）。

  仅 GUI/桌面形态渲染；browser 形态无宿主（isBrowserForm() = true → 不渲染）。
-->
<template>
  <div v-if="resizeEnabled" class="win-resize-layer" aria-hidden="true">
    <div
      v-for="hit in hits"
      :key="hit"
      class="win-resize-handle"
      :class="`win-resize-${hit}`"
      @mousedown.prevent="onHit(hit)"
    ></div>
  </div>
</template>

<script setup>
import mq from '../../utils/mq'
import { isBrowserForm } from '../../utils/runtimeForm'
import { EventNames } from '../../events/event-names'
import { GuiWindowResizeKeys } from '../../events/msgkeys'

// 8 个方位：4 边在前、4 角在后（同一层叠顺序下后者绘制在上，角落优先命中）。
const hits = ['left', 'right', 'top', 'bottom', 'topleft', 'topright', 'bottomleft', 'bottomright']

// 仅 GUI/桌面形态有宿主窗口；browser 形态无 resize 能力 → 不渲染。
const resizeEnabled = !isBrowserForm()

// fire-and-forget：61 §1 本主题「返回 = 无（本地事件）」，宿主随后进入系统原生 resize 模态循环。
function onHit(hit) {
  mq.emit(EventNames.guiWindowResize, { [GuiWindowResizeKeys.hit]: hit })
}
</script>

<style scoped>
.win-resize-layer {
  position: fixed;
  inset: 0;
  /* 高于全部布局容器 / 弹层；仅最外圈命中条可见（不拦内容区交互） */
  z-index: 2147483000;
  pointer-events: none;
}

.win-resize-handle {
  position: fixed;
  pointer-events: auto;
  /* 命中条只在最外圈：退出标题栏拖拽区，mousedown 才能到本组件 */
  -webkit-app-region: no-drag;
}

/* 4 边：6px；两端各让出 12px 给角 */
.win-resize-left,
.win-resize-right {
  top: 12px;
  bottom: 12px;
  width: 6px;
}
.win-resize-top,
.win-resize-bottom {
  left: 12px;
  right: 12px;
  height: 6px;
}
.win-resize-left { left: 0; cursor: ew-resize; }
.win-resize-right { right: 0; cursor: ew-resize; }
.win-resize-top { top: 0; cursor: ns-resize; }
.win-resize-bottom { bottom: 0; cursor: ns-resize; }

/* 4 角：12px × 12px */
.win-resize-topleft,
.win-resize-topright,
.win-resize-bottomleft,
.win-resize-bottomright {
  width: 12px;
  height: 12px;
}
.win-resize-topleft { left: 0; top: 0; cursor: nwse-resize; }
.win-resize-topright { right: 0; top: 0; cursor: nesw-resize; }
.win-resize-bottomleft { left: 0; bottom: 0; cursor: nesw-resize; }
.win-resize-bottomright { right: 0; bottom: 0; cursor: nwse-resize; }
</style>
