<!--
  自由布局容器（UILAYOUT-1 §2 装配 / §3.2 大组件 API）：宿主唯一渲染点。
  宿主只传数据（布局树 / 设计态 / 选中节点），组件只渲染、交互全走 mq（`v-mq`）。
-->
<template>
  <LfFreeLayoutContainer
    v-if="engine"
    :layout="renderLayout"
    :preview="editing"
    :selected-id="selectedNodeId"
  />
</template>

<script setup>
import { computed } from 'vue'
import LfFreeLayoutContainer from '../layout-free/components/layout/LfFreeLayoutContainer.vue'
import { getLayoutEngine } from './index.js'

const engine = getLayoutEngine()

/** 渲染树：结构取 `live`（保存后生效）、节点背景色取 `draft`（改一下立刻可见）；设计态为真 */
const renderLayout = computed(() => (engine ? engine.renderLayout : null))
const editing = computed(() => (engine ? engine.editing : false))
const selectedNodeId = computed(() => (engine ? engine.selectedNodeId : ''))
</script>
