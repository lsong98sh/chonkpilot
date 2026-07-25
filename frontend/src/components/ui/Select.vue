<template>
  <div class="b-select">
    <select
      class="b-select__native"
      :class="{ 'is-error': error, 'is-disabled': disabled }"
      :disabled="disabled"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option value="" disabled>{{ placeholder || '请选择' }}</option>
      <option v-for="opt in options" :key="opt.value" :value="opt.value">
        {{ opt.label }}
      </option>
    </select>
    <span class="b-select__arrow" />
  </div>
</template>
<script setup>
defineProps({
  modelValue: [String, Number],
  options: { type: Array, default: () => [] },
  placeholder: String,
  disabled: Boolean,
  error: Boolean
})
defineEmits(['update:modelValue'])
</script>

<style scoped>
.b-select {
  position: relative;
  width: 100%;
}
.b-select__native {
  width: 100%;
  padding: 5px 28px 5px 11px;
  border: 1px solid var(--border-color, #d9d9d9);
  border-radius: var(--border-radius, 4px);
  font-size: var(--font-size-sm, 13px);
  background: var(--bg-primary, #fff);
  color: var(--text-primary);
  outline: none;
  transition: border-color 0.2s;
  box-sizing: border-box;
  line-height: 1.5;
  font-family: inherit;
  cursor: pointer;
  appearance: none;
  -webkit-appearance: none;
}
.b-select__native:focus {
  border-color: var(--accent-color, #409eff);
}
.b-select__native.is-error {
  border-color: var(--danger-color, #f56c6c);
}
.b-select__native.is-disabled {
  opacity: 0.6;
  cursor: not-allowed;
  background: var(--bg-secondary, #f5f5f5);
}
.b-select__arrow {
  position: absolute;
  right: 8px;
  top: 50%;
  transform: translateY(-50%);
  width: 0;
  height: 0;
  border-left: 4px solid transparent;
  border-right: 4px solid transparent;
  border-top: 5px solid var(--text-muted, #999);
  pointer-events: none;
}
</style>