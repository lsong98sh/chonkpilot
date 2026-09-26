<template>
  <div class="empty-state">
    <Icon name="folder-delete" :size="36" color="var(--text-muted)" />
    <p>{{ message }}</p>
  </div>
</template>

<script setup>
import Icon from '../icon/Icon.vue'

defineProps({
  message: { type: String, default: '' },
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