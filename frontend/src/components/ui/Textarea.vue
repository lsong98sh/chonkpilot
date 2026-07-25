<template>
  <textarea
    class="b-textarea"
    :class="{ 'is-error': error, 'is-disabled': disabled }"
    :disabled="disabled"
    :readonly="readonly"
    :value="modelValue"
    :placeholder="placeholder"
    :rows="rows"
    @input="$emit('update:modelValue', $event.target.value)"
  ></textarea>
</template>
<script setup>
defineProps({
  modelValue: [String, Number],
  placeholder: String,
  disabled: Boolean,
  readonly: Boolean,
  error: Boolean,
  rows: { type: Number, default: 4 }
})
defineEmits(['update:modelValue'])
</script>

<style scoped>
.b-textarea {
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
  resize: vertical;
}
.b-textarea:focus {
  border-color: var(--accent-color, #409eff);
}
.b-textarea.is-error {
  border-color: var(--danger-color, #f56c6c);
}
.b-textarea.is-disabled {
  opacity: 0.6;
  cursor: not-allowed;
  background: var(--bg-secondary, #f5f5f5);
}
.b-textarea::placeholder {
  color: var(--text-muted, #999);
}
</style>