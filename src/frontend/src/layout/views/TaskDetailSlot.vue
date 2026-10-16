<!--
  区块适配壳：`task-detail`（UILAYOUT-1 §3.4）。
  容器把区域配置的 `view.params` 传进来（`LfViewHost` 的 `params` prop），这里转成
  `TaskDetailView` 需要的 props（`taskId`）。机制层不认识任何宿主区块，故转发壳放适配层。
-->
<template>
  <TaskDetailView :task-id="taskId" />
</template>

<script setup>
import { computed } from 'vue'
import TaskDetailView from '../../components/task/TaskDetailView.vue'

const props = defineProps({
  /** 区域配置里的 `view.params`（容器注入） */
  params: { type: Object, default: () => ({}) },
})

const taskId = computed(() => String(props.params?.taskId || ''))
</script>
