<template>
  <span
    v-if="count > 0"
    class="b-badge"
    :class="[`b-badge--${type}`]"
    :style="badgeStyle"
  >{{ displayCount }}</span>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  count: { type: [Number, String], default: 0 },
  color: { type: String, default: '' },
  type: { type: String, default: 'danger' },
})

// 数字过大时显示 99+，避免角标溢出
const displayCount = computed(() => {
  const n = Number(props.count)
  if (Number.isFinite(n) && n > 99) return '99+'
  return String(n)
})

// 允许通过 color 自定义背景色（默认红色 danger）
const badgeStyle = computed(() => props.color ? { background: props.color } : null)
</script>

<style scoped>
.b-badge {
  position: absolute;
  top: -4px;
  right: -6px;
  min-width: 14px;
  height: 14px;
  padding: 0 3px;
  border-radius: 7px;
  /* 危险实心填充：文字用主题最底层色（light = 纯白，与历史 #fff 一致；dark/nord = 深色） */
  background: var(--danger);
  color: var(--bg-secondary);
  font-size: 10px;
  line-height: 14px;
  text-align: center;
  pointer-events: none;
  box-sizing: border-box;
  z-index: 1;
}
</style>
