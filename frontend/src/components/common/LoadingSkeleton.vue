<template>
  <div class="loading-skeleton">
    <div v-for="i in rows" :key="i" class="skeleton-row" :style="{ width: randomWidth() }" />
  </div>
</template>

<script setup>
defineProps({
  rows: { type: Number, default: 3 },
})

function randomWidth() {
  return (40 + Math.random() * 60) + '%'
}
</script>

<style scoped>
.loading-skeleton {
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.skeleton-row {
  height: 12px;
  background: var(--bg-hover);
  border-radius: 4px;
  animation: pulse 1.5s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 0.8; }
}
</style>
