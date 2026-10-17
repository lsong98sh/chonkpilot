<!--
  自由布局形态 · 顶部工具栏外壳（UILAYOUT-1 §7 P4①）。

  职责：把机制层的「栏」渲染成宿主顶栏 —— 品牌（静态块，不入配置）+ `LfBarZones`
  （条目取 `layout.free` 文档的 `topToolbar`）+ **窗口控件固定右端**（`#right-extra` 插槽，
  不进配置：删掉就无法操作窗口，§4.2）。

  交互：条目点击由机制层 `v-mq` 直接发（`button` → 既有主题 / `menu` → 菜单树 / `view` → 内嵌组件）；
  宿主侧对「发布 MQ 动作」到既有主题的处理在 `layout/barActions.js`（本组件挂载时安装）。
  `-webkit-app-region: drag` 提供窗口拖拽区；交互子元素一律 `no-drag`。
-->
<template>
  <div class="toolbar ck-topbar" v-mq:[EventNames.windowToggle].dblclick @mousedown="onDragStart">
    <div class="ck-brand">Chonk Pilot</div>
    <div class="ck-zones">
      <LfBarZones
        name="topToolbar"
        :items="items"
        :background="bar.background"
        :zone-background="bar.zoneBackground"
        :default-left-width="150"
        :default-right-width="760"
        :max-side-width="1400"
        empty-text=""
      >
        <template #right-extra>
          <WindowControls />
        </template>
      </LfBarZones>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import LfBarZones from '../layout-free/components/bar/LfBarZones.vue'
import WindowControls from './bar-views/WindowControls.vue'
import { EventNames } from '../events/event-names'
import { GuiWindowResizeKeys } from '../events/msgkeys'
import mq from '../utils/mq'
import { isBrowserForm } from '../utils/runtimeForm'
import { getLayoutEngine } from './index.js'
import { installBarActions } from './barActions.js'

const engine = getLayoutEngine()

// 顶栏拖拽移动（frameless 主窗口）：WebView2 的 `app-region` 非客户区在本项目
// `WM_NCCALCSIZE=0` 的自绘窗口上实测不触发（settings9 enabled=true 仍无效）→
// 与边缘 resize 同链路：非交互区 mousedown → gui.window.resize{hit:'caption'} →
// 宿主 `WM_NCLBUTTONDOWN(HTCAPTION)` 进入系统移动模态循环（61 §1）。
const NO_DRAG = 'button, input, textarea, select, a, .bar-item-shell, .bar-view, .split-resizer'
let dragState = null
function onDragStart(e) {
  if (e.button !== 0 || isBrowserForm()) return
  const t = e.target
  if (t && t.closest && t.closest(NO_DRAG)) return
  // 不立即进入移动循环：先等鼠标位移超过阈值（4px）再发起 → 保留单击/双击
  // （顶栏 dblclick = 最大化/还原；若 mousedown 即 StartResize，系统移动循环会吞掉双击）。
  dragState = { x: e.screenX, y: e.screenY }
  window.addEventListener('mousemove', onDragMove, true)
  window.addEventListener('mouseup', onDragEnd, { capture: true, once: true })
}
function onDragMove(e) {
  if (!dragState) return
  const dx = e.screenX - dragState.x
  const dy = e.screenY - dragState.y
  if (dx * dx + dy * dy < 16) return
  cleanupDrag()
  mq.emit(EventNames.guiWindowResize, { [GuiWindowResizeKeys.hit]: 'caption' })
}
function onDragEnd() {
  cleanupDrag()
}
function cleanupDrag() {
  dragState = null
  window.removeEventListener('mousemove', onDragMove, true)
  window.removeEventListener('mouseup', onDragEnd, true)
}

/** 条目取 live、背景取 draft（§3.6 编辑态预览语义） */
const bar = computed(() =>
  engine ? engine.renderBar('topToolbar') : { items: [], background: '', zoneBackground: {} },
)
const items = computed(() => bar.value.items ?? [])

let uninstall = null
onMounted(() => { uninstall = installBarActions() })
onUnmounted(() => {
  if (typeof uninstall === 'function') uninstall()
  cleanupDrag()
})
</script>

<style scoped>
.ck-topbar {
  height: var(--toolbar-height);
  display: flex;
  align-items: center;
  padding: 0 0 0 8px;
  background: var(--toolbar-bg);
  border-bottom: 1px solid var(--border);
  user-select: none;
  /* frameless 拖拽区 */
  -webkit-app-region: drag;
}
.ck-brand {
  flex: none;
  font-size: 14px;
  font-weight: 700;
  color: var(--accent);
  letter-spacing: 0.5px;
  white-space: nowrap;
  padding: 0 10px 0 4px;
  margin-right: 4px;
  border-right: 1px solid var(--border);
  height: 100%;
  display: flex;
  align-items: center;
}
.ck-zones {
  flex: 1;
  min-width: 0;
  height: 100%;
}
</style>

<style>
/* 拖拽区**下沉到实际命中的子容器**：WebView2 的非客户区（app-region）按鼠标命中元素
   自身的计算样式判定（不是仅看父级矩形）；顶栏整条被 `.ck-brand` / `.bar-zones` /
   `.bar-zone` 铺满 → 只给 `.ck-topbar` 设 drag 时命中的是子元素 → 没有拖拽区（move 不生效）。
   故这些容器自身也标 drag（`app-region` 标准属性 + `-webkit-app-region` 兼容别名）。 */
.toolbar.ck-topbar,
.toolbar.ck-topbar .ck-brand,
.toolbar.ck-topbar .bar-zones,
.toolbar.ck-topbar .bar-zone {
  app-region: drag;
  -webkit-app-region: drag;
}

/* 顶栏内**交互元素**退出拖拽区（否则点不动）；view 条目（搜索框等）同样处理。
   条目外壳（.bar-item-shell）/控件/三区之间的分隔条一律 no-drag。 */
.toolbar.ck-topbar .bar-item-shell,
.toolbar.ck-topbar .bar-btn,
.toolbar.ck-topbar input,
.toolbar.ck-topbar button,
.toolbar.ck-topbar a,
.toolbar.ck-topbar .bar-view,
.toolbar.ck-topbar .split-resizer {
  app-region: no-drag;
  -webkit-app-region: no-drag;
}
</style>
