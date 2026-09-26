<template>
  <span class="b-icon" :class="{ 'is-loading': name === 'loading' }" :style="{ fontSize: size + 'px', color: color }" v-html="iconSvg" />
</template>

<script setup>
import { computed } from 'vue'
import { icons } from './icons'

const props = defineProps({
  name: { type: String, required: true },
  size: { type: [Number, String], default: 16 },
  color: { type: String, default: 'currentColor' }
})

const iconSvg = computed(() => icons[props.name] || '')
</script>

<style scoped>
.b-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  line-height: 0;
}
.b-icon :deep(svg) {
  width: 1em;
  height: 1em;
  display: block;
}
.b-icon.is-loading :deep(svg) {
  animation: b-icon-spin 0.7s linear infinite;
}
@keyframes b-icon-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
