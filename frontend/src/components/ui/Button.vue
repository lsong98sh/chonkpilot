<template>
  <button
    class="b-btn"
    :class="[
      `b-btn--${type}`,
      `b-btn--${size}`,
      { 'is-text': text, 'is-circle': circle, 'is-loading': loading, 'is-disabled': disabled }
    ]"
    :disabled="disabled || loading"
    :type="nativeType"
    @click="!disabled && !loading && $emit('click', $event)"
  >
    <span v-if="loading" class="b-btn-spinner" />
    <slot />
  </button>
</template>

<script setup>
defineProps({
  size: { type: String, default: 'small' },
  type: { type: String, default: 'default' },
  nativeType: { type: String, default: 'button' },
  text: Boolean,
  circle: Boolean,
  loading: Boolean,
  disabled: Boolean,
})
defineEmits(['click'])
</script>

<style scoped>
.b-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  border: 1px solid transparent;
  border-radius: var(--border-radius, 4px);
  cursor: pointer;
  font-size: var(--font-size-sm, 13px);
  line-height: 1;
  white-space: nowrap;
  transition: all 0.12s;
  box-sizing: border-box;
  font-family: inherit;
  outline: none;
}
.b-btn--small { padding: 5px 10px; min-height: 28px; }
.b-btn--mini { padding: 3px 6px; min-height: 24px; font-size: 12px; }
.b-btn--medium { padding: 8px 16px; min-height: 32px; }

.b-btn--default { background: var(--bg-secondary, #f5f5f5); border-color: var(--border, #d9d9d9); color: var(--text-primary); }
.b-btn--primary { background: var(--accent, #409eff); color: #fff; }
.b-btn--info { background: var(--bg-hover, #e6e6e6); color: var(--text-primary); }
.b-btn--danger { background: var(--danger, #f56c6c); color: #fff; }
.b-btn--warning { background: var(--warning, #e6a23c); color: #fff; }
.b-btn--success { background: var(--success, #67c23a); color: #fff; }

.b-btn.is-text { background: transparent; border-color: transparent; color: var(--accent); }
.b-btn.is-text:hover { background: var(--accent-bg, #ecf5ff); }
.b-btn.is-circle { border-radius: 50%; padding: 0; width: 28px; height: 28px; }

.b-btn:hover { opacity: 0.85; }
.b-btn.is-disabled { opacity: 0.5; cursor: not-allowed; }

.b-btn-spinner {
  width: 14px; height: 14px;
  border: 2px solid currentColor;
  border-top-color: transparent;
  border-radius: 50%;
  animation: b-spin 0.7s linear infinite;
  flex-shrink: 0;
}
@keyframes b-spin { to { transform: rotate(360deg); } }
</style>
