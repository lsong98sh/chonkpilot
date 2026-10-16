<template>
  <!-- 设计态兜底（§3.11）：`designable:false` 的区块**不挂载真实组件**（设计态取数会污染真实状态），
       只渲染标题 + 骨架占位。 -->
  <div v-if="designSkeleton" class="v-root">
    <div class="v-head">
      <LfIcon class="v-icon" name="square-dashed" :size="13" />
      <span class="v-title">{{ title }}</span>
    </div>
    <div class="v-body lf-empty">{{ tr('container.designSkeleton') }}</div>
  </div>
  <component
    :is="comp"
    v-else-if="comp"
    :instance-key="viewRef.instanceKey"
    :params="viewRef.params"
    :label="viewRef.label"
    :design="design"
  />
  <div v-else class="v-root view-missing">
    <div class="v-head">
      <LfIcon class="v-icon" name="circle-alert" :size="13" />
      <span class="v-title">{{ viewRef.label || viewRef.viewKey }}</span>
    </div>
    <div class="v-body lf-empty">{{ tr('container.viewMissing', { key: viewRef.viewKey }) }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { resolveViewComponent, tr } from '../deps.js'
import { getView } from '../config/views'

const props = defineProps({
  /** 视图引用 { viewKey, instanceKey, label?, tabs?, params? } */
  viewRef: { type: Object, required: true },
  /** 设计态：view 走只读渲染（design 模式），看得到效果但无法操作 */
  design: { type: Boolean, default: false },
})

const comp = computed(() => resolveViewComponent(props.viewRef.viewKey))
const def = computed(() => getView(props.viewRef.viewKey))
const title = computed(() => def.value?.label || props.viewRef.label || props.viewRef.viewKey)

/** 设计态 + 该区块声明「设计态不可挂载」→ 只渲染骨架（§3.11 设计态兜底） */
const designSkeleton = computed(() => props.design === true && def.value?.designable === false && !props.viewRef?.tabs)
</script>

<style scoped>
.view-missing {
  border: 1px dashed var(--lf-border-strong);
}
</style>
