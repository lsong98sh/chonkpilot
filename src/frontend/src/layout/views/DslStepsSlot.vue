<!--
  区块适配壳：`dsl-steps`（UILAYOUT-1 §3.4）。
  `DslStepsPanel` 的 `jobId` 是必填 props → 未配置 params.jobId 时渲染占位（不挂载真实面板，
  避免"空 jobId 面板"渲染出无意义内容）。
-->
<template>
  <DslStepsPanel v-if="jobId" :job-id="jobId" />
  <div v-else class="lf-empty">{{ $t('layoutFree.view.dslStepsNoJob') }}</div>
</template>

<script setup>
import { computed } from 'vue'
import DslStepsPanel from '../../views/dsl/DslStepsPanel.vue'

const props = defineProps({
  /** 区域配置里的 `view.params`（容器注入） */
  params: { type: Object, default: () => ({}) },
})

const jobId = computed(() => String(props.params?.jobId || ''))
</script>
