<template>
  <span class="b-tag" :class="[`b-tag--${type}`, `b-tag--${size}`]">
    <slot />
  </span>
</template>

<script setup>
defineProps({
  type: { type: String, default: 'info' },
  size: { type: String, default: 'small' },
})
</script>

<style scoped>
.b-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--border-radius, 4px);
  font-size: var(--font-size-sm, 13px);
  line-height: 1.4;
  white-space: nowrap;
  box-sizing: border-box;
}

.b-tag--small {
  padding: 1px 6px;
  font-size: 12px;
}
.b-tag--mini {
  padding: 0 4px;
  font-size: 11px;
}

.b-tag--info {
  background: var(--bg-hover, #e9ecef);
  color: var(--text-secondary, #495057);
}
.b-tag--danger {
  background: color-mix(in srgb, var(--danger, #dc3545) 15%, transparent);
  color: var(--danger, #dc3545);
}
.b-tag--success {
  background: color-mix(in srgb, var(--success, #2d9f4e) 15%, transparent);
  color: var(--success, #2d9f4e);
}
.b-tag--warning {
  background: color-mix(in srgb, var(--warning, #e8a317) 15%, transparent);
  color: var(--warning, #e8a317);
}
</style>
