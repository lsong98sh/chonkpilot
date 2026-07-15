<template>
  <div class="task-node" :style="{ paddingLeft: depth * 20 + 'px' }">
    <div class="task-header">
      <el-icon v-if="task.status === 'running'" class="is-loading" color="var(--accent)">
        <Loading />
      </el-icon>
      <el-icon v-else-if="task.status === 'completed'" color="var(--success)"> 
        <SuccessFilled />
      </el-icon>
      <el-icon v-else-if="task.status === 'failed'" color="var(--danger)">
        <WarningFilled />
      </el-icon>
      <el-icon v-else color="var(--text-muted)">
        <CircleCheck />
      </el-icon>
      <span class="task-name">{{ task.name }}</span>
    </div>
    <div class="task-progress" v-if="task.progress !== undefined">
      <el-progress
        :percentage="task.progress"
        :stroke-width="4"
        :show-text="false"
      />
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
</style>
