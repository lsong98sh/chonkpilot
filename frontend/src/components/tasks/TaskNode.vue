<template>
  <div class="task-node" :style="{ paddingLeft: depth * 20 + 'px' }">
    <div class="task-header">
      <Icon v-if="task.status === 'running'" class="is-loading" color="var(--accent)" name="loading" />
      <Icon v-else-if="task.status === 'completed'" color="var(--success)" name="success-filled" />
      <Icon v-else-if="task.status === 'failed'" color="var(--danger)" name="warning-filled" />
      <Icon v-else color="var(--text-muted)" name="circle-check" />
      <span class="task-name">{{ task.name }}</span>
    </div>
    <div class="task-progress" v-if="task.progress !== undefined">
      <div class="task-progress-bar-inner">
        <div class="task-progress-fill" :style="{ width: task.progress + '%' }"></div>
      </div>
    </div>
    <template v-if="task.children">
      <TaskNode
        v-for="child in task.children"
        :key="child.task_id"
        :task="child"
        :depth="depth + 1"
      />
    </template>
  </div>
</template>

<script setup>
import Icon from '../icon/Icon.vue'

defineProps({
  task: { type: Object, required: true },
  depth: { type: Number, default: 0 },
})
</script>

<style scoped>
.task-node {
  padding: 4px 8px;
}

.task-header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}

.task-name {
  color: var(--text-primary);
}

.task-progress {
  padding: 4px 0 4px 24px;
}

.task-progress-bar-inner {
  height: 4px;
  background: var(--bg-hover);
  border-radius: 2px;
  overflow: hidden;
}

.task-progress-fill {
  height: 100%;
  background: var(--accent);
  border-radius: 2px;
  transition: width 0.3s;
}
</style>