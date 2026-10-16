<template>
  <div class="free-layout" :class="{ 'is-preview': preview }">
    <LfLayoutNodeRenderer :node="root" :depth="0" :design="preview" :selected-id="selectedId" />
  </div>
</template>

<script setup>
import { computed, provide, toRef } from 'vue'
import LfLayoutNodeRenderer from './LfLayoutNodeRenderer.vue'
import { LAYOUT_CONTEXT } from '../../composables/layout-context'

const props = defineProps({
  /** 要渲染的布局树 */
  layout: { type: Object, default: null },
  /** 设计态：只读渲染 view + 显示区域标题 + 区域可点选可右键 */
  preview: { type: Boolean, default: false },
  /** 当前选中节点 id（设计态高亮） */
  selectedId: { type: String, default: '' },
})

/** provide 一份设计态标记，供 view 内部 useDesign() 读取 */
provide(LAYOUT_CONTEXT, toRef(props, 'preview'))

const root = computed(() => props.layout)
</script>

<style scoped>
.free-layout {
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  display: flex;
  overflow: hidden;
}

.free-layout > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.free-layout.is-preview {
  background: #eef1f6;
  padding: 6px;
  border-radius: 8px;
}

.free-layout.is-preview > * {
  border-radius: 6px;
  overflow: hidden;
}
</style>
