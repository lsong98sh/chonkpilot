<template>
  <input
    class="b-input"
    :class="{ 'is-error': error, 'is-disabled': disabled }"
    :disabled="disabled"
    :readonly="readonly"
    :value="modelValue"
    :placeholder="placeholder"
    :type="type"
    :maxlength="maxlength"
    :minlength="minlength"
    :pattern="pattern"
    :required="required"
    :min="min"
    :max="max"
    :step="step"
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
  error: Boolean,
  // INPUTVAL-1：原样透传原生约束（**不设全局默认上限**——长文本与短名称上限不同）。
  maxlength: { type: [Number, String], default: undefined },
  minlength: { type: [Number, String], default: undefined },
  pattern: { type: String, default: undefined },
  required: { type: Boolean, default: false },
  min: { type: [Number, String], default: undefined },
  max: { type: [Number, String], default: undefined },
  step: { type: [Number, String], default: undefined }
})
defineEmits(['update:modelValue', 'focus', 'blur', 'change'])
</script>

<style scoped>
.b-input {
  width: 100%;
  padding: 5px 11px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  font-size: var(--font-size-sm);
  background: var(--bg-primary, #fff);
  color: var(--text-primary);
  outline: none;
  transition: border-color 0.2s;
  box-sizing: border-box;
  line-height: 1.5;
  font-family: inherit;
}
.b-input:focus {
  border-color: var(--accent);
}
.b-input.is-error {
  border-color: var(--danger);
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
