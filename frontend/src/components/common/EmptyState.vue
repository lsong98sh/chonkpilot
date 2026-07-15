<template>
  <div class="empty-state">
    <el-icon :size="36" color="var(--text-muted)"><FolderDelete /></el-icon>
    <p>{{ message }}</p>
  </div>
</template>

<script setup>
import { FolderDelete } from '@element-plus/icons-vue'

defineProps({
  message: { type: String, default: 'No data' },
})
</script>

<style scoped>
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px 20px;
  text-align: center;
  color: var(--text-muted);
  font-size: 13px;
}
</style>
