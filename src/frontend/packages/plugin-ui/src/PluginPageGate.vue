<template>
  <!-- 插件页承载门（G-107）：视图（entry）未注册时不挂 PluginPageHost —— 否则宿主 onMounted
       的 check() 在 entry='' 下落 idle 态，视图后到也无人重跑 → 页面永久空白。
       视图就绪后才挂载（entry 翻转触发本组件重渲染 → v-if 建立子树 → 宿主重新走完整就绪链路）；
       entry 变更（插件升级重注册）经 :key 强制重挂，避免沿用旧 src。 -->
  <PluginPageHost
    v-if="view && view.entry"
    :key="view.entry"
    :plugin-id="pluginId"
    :page-id="pageId"
    :entry="view.entry"
    :title="view.title || ''"
  />
  <div v-else class="ppg-starting">
    <p>{{ $t('layoutFree.container.viewStarting') }}</p>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import PluginPageHost from './PluginPageHost.vue'
import { pluginView } from './usePlugins.js'

const props = defineProps({
  pluginId: { type: String, required: true },
  pageId: { type: String, required: true },
})

const view = computed(() => pluginView(props.pluginId, props.pageId))
</script>

<style scoped>
/* 与 PluginPageHost 的 .pph-state 同口径：居中、muted、只用主题 token（不得硬编码颜色） */
.ppg-starting {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: center;
  color: var(--text-muted);
  font-size: 13px;
}
.ppg-starting p {
  margin: 0;
}
</style>
