<template>
  <!-- 容器节点：外面可以再套一圈「容器自己的自定义工具栏」（上下左右）+ 容器背景色 -->
  <div
    v-if="node.type === 'split'"
    class="node-split"
    :class="{ vertical: toolbarVertical }"
    :data-node-id="node.id"
    :style="node.background ? { background: node.background } : {}"
  >
    <LfBarStrip
      v-if="toolbarItems.length && toolbarPos === 'top'"
      :items="toolbarItems"
      position="top"
      :name="`node-${node.id}`"
      :background="toolbarDef?.background ?? ''"
      :zone-background="toolbarDef?.zoneBackground ?? {}"
      :default-side-width="120"
    />
    <LfBarStrip
      v-if="toolbarItems.length && toolbarPos === 'left'"
      :items="toolbarItems"
      position="left"
      :name="`node-${node.id}`"
      :background="toolbarDef?.background ?? ''"
      :zone-background="toolbarDef?.zoneBackground ?? {}"
      :default-side-width="100"
    />
    <div class="node-split-body">
      <LfLayoutSplit :node="node" :depth="depth" :design="design" :selected-id="selectedId" />
    </div>
    <LfBarStrip
      v-if="toolbarItems.length && toolbarPos === 'right'"
      :items="toolbarItems"
      position="right"
      :name="`node-${node.id}`"
      :background="toolbarDef?.background ?? ''"
      :zone-background="toolbarDef?.zoneBackground ?? {}"
      :default-side-width="100"
    />
    <LfBarStrip
      v-if="toolbarItems.length && toolbarPos === 'bottom'"
      :items="toolbarItems"
      position="bottom"
      :name="`node-${node.id}`"
      :background="toolbarDef?.background ?? ''"
      :zone-background="toolbarDef?.zoneBackground ?? {}"
      :default-side-width="120"
    />
  </div>

  <LfLayoutLeaf v-else :node="node" :depth="depth" :design="design" :selected-id="selectedId" />
</template>

<script setup>
import { computed } from 'vue'
import LfLayoutSplit from './LfLayoutSplit.vue'
import LfLayoutLeaf from './LfLayoutLeaf.vue'
import LfBarStrip from '../../components/bar/LfBarStrip.vue'
import { useConfigStore } from '../../stores/config'

const props = defineProps({
  /** 布局节点：容器(split) 或 区域(leaf) */
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  /** 设计态（编辑器预览） */
  design: { type: Boolean, default: false },
  /** 设计态下选中的节点 id */
  selectedId: { type: String, default: '' },
})

const config = useConfigStore()

/* ── 容器（父容器）也可以挂自定义工具栏 ── */
const toolbarDef = computed(() => {
  const id = props.node.toolbar?.toolbarId
  return id ? config.toolbarById(id) : null
})
const toolbarItems = computed(() => toolbarDef.value?.items ?? [])
const toolbarPos = computed(() => {
  const p = props.node.toolbar?.position
  return ['top', 'bottom', 'left', 'right'].includes(p) ? p : 'top'
})
const toolbarVertical = computed(
  () => toolbarItems.value.length > 0 && (toolbarPos.value === 'left' || toolbarPos.value === 'right'),
)
</script>

<style scoped>
.node-split {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
}

.node-split.vertical {
  flex-direction: row;
}

.node-split-body {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
}

.node-split-body > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}
</style>
