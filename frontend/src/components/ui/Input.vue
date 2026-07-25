<template>
  <input
    class="b-input"
    :class="{ 'is-error': error, 'is-disabled': disabled }"
    :disabled="disabled"
    :readonly="readonly"
    :value="modelValue"
    :placeholder="placeholder"
    :type="type"
    @input="$emit('update:modelValue', $event.target.value)"
    @focus="$emit('focus', $event)"
    @blur="$emit('blur', $event)"
    @change="$emit('change', $event)"
  />
</template>
<script setup>
defineProps({
  modelValue: [String, Number],
  placeholder: String,
  type: { type: String, default: 'text' },
  disabled: Boolean,
  readonly: Boolean,
  error: Boolean
})
defineEmits(['update:modelValue', 'focus', 'blur', 'change'])
</script>

<style scoped>
.b-input {
  width: 100%;
  padding: 5px 11px;
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
}
.b-input:focus {
  border-color: var(--accent-color, #409eff);
}
.b-input.is-error {
  border-color: var(--danger-color, #f56c6c);
}
.b-input.is-disabled {
  opacity: 0.6;
  cursor: not-allowed;
  background: var(--bg-secondary, #f5f5f5);
}
.b-input::placeholder {
  color: var(--text-muted, #999);
}
</style>
