<!--
  自由布局形态 · 状态栏外壳（UILAYOUT-1 §7 P4①）。
  条目取 `layout.free` 文档的 `statusbar`（实时项 = 栏视图条目，裁决 2）。
-->
<template>
  <div class="statusbar ck-statusbar">
    <LfBarZones
      name="statusbar"
      :items="items"
      :extra-items="pluginItems"
      :background="bar.background"
      :zone-background="bar.zoneBackground"
      :default-left-width="420"
      :default-right-width="220"
      empty-text=""
    />
  </div>
</template>

<script setup>
import { computed } from 'vue'
import LfBarZones from '../layout-free/components/bar/LfBarZones.vue'
import { getLayoutEngine } from './index.js'
import { pluginBarItems } from '../../packages/plugin-ui/src/pluginBar.js'

const engine = getLayoutEngine()

const bar = computed(() =>
  engine ? engine.renderBar('statusbar') : { items: [], background: '', zoneBackground: {} },
)
const items = computed(() => bar.value.items ?? [])
// 插件状态栏条目（manifest ui.statusbar[]；不写入布局文档）
const pluginItems = computed(() => [
  ...pluginBarItems('left'),
  ...pluginBarItems('center'),
  ...pluginBarItems('right'),
])
</script>

<style scoped>
.ck-statusbar {
  height: var(--statusbar-height);
  background: var(--bg-secondary, #f5f5f5);
  border-top: 1px solid var(--border);
  font-size: 12px;
  color: var(--text-muted, #888);
  flex-shrink: 0;
  user-select: none;
}
</style>

<style>
.toolbar.ck-statusbar .bar-zone,
.ck-statusbar .bar-zone {
  -webkit-app-region: no-drag;
}
</style>
