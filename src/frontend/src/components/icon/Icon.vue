<template>
  <span
    class="b-icon"
    :class="{ 'is-loading': isSpinning, 'is-missing': isMissing }"
    :style="style"
    :title="isMissing ? `未定义图标：${name}` : undefined"
    v-html="content"
  />
</template>

<script setup>
import { computed } from 'vue'
import { icons } from './icons'

const props = defineProps({
  name: { type: String, default: '' },
  size: { type: [Number, String], default: 16 },
  color: { type: String, default: 'currentColor' },
  /** 未定义图标名的兜底字符；传 '' 则什么都不渲染 */
  fallback: { type: String, default: '·' }
})

const warned = new Set()

const iconSvg = computed(() => {
  if (!props.name) return ''
  const hit = icons[props.name]
  if (!hit && !warned.has(props.name)) {
    warned.add(props.name)
    console.warn(`[Icon] 未定义的图标名：${props.name}（请补到 components/icon/icons.js）`)
  }
  return hit || ''
})

const isMissing = computed(() => !!props.name && !iconSvg.value)
const content = computed(() => iconSvg.value || (isMissing.value ? props.fallback : ''))
const isSpinning = computed(() => props.name === 'loading')
const style = computed(() => ({ fontSize: props.size + 'px', color: props.color }))
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
.b-icon.is-missing {
  line-height: 1;
  opacity: 0.45;
}
@keyframes b-icon-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
