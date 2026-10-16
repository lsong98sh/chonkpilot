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
  <div class="toolbar ck-topbar" v-mq:[EventNames.windowToggle].dblclick>
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
import { getLayoutEngine } from './index.js'
import { installBarActions } from './barActions.js'

const engine = getLayoutEngine()

/** 条目取 live、背景取 draft（§3.6 编辑态预览语义） */
const bar = computed(() =>
  engine ? engine.renderBar('topToolbar') : { items: [], background: '', zoneBackground: {} },
)
const items = computed(() => bar.value.items ?? [])

let uninstall = null
onMounted(() => { uninstall = installBarActions() })
onUnmounted(() => { if (typeof uninstall === 'function') uninstall() })
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
/* 顶栏内所有交互元素退出拖拽区（否则点不动）；view 条目（搜索框等）同样处理 */
.toolbar.ck-topbar .bar-btn,
.toolbar.ck-topbar input,
.toolbar.ck-topbar button,
.toolbar.ck-topbar a,
.toolbar.ck-topbar .bar-view,
.toolbar.ck-topbar .bar-zone {
  -webkit-app-region: no-drag;
}
</style>
