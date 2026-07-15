<template>
  <div class="image-preview">
    <img :src="src" :alt="alt" @click="expanded = !expanded" />
  </div>
</template>

<script setup>
import { ref } from 'vue'

defineProps({
  src: { type: String, required: true },
  alt: { type: String, default: '' },
})

const expanded = ref(false)
</script>

<style scoped>
.image-preview img {
  max-width: 100%;
  border-radius: 4px;
  cursor: pointer;
  transition: transform var(--transition-normal);
}

.image-preview img:hover {
  transform: scale(1.02);
}
</style>
