<template>
  <div class="task-tree">
    <TaskNode
      v-for="task in tasks"
      :key="task.task_id"
      :task="task"
      :depth="0"
    />
  </div>
</template>

<script setup>
import TaskNode from './TaskNode.vue'

defineProps({
  tasks: { type: Array, required: true },
})
</script>

<style scoped>
.task-tree {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
</style>
