<template>
  <div class="b-select">
    <select
      class="b-select__native"
      :class="{ 'is-error': error, 'is-disabled': disabled }"
      :disabled="disabled"
      :value="modelValue"
      :aria-label="ariaLabel"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option value="" :disabled="!placeholderSelectable">{{ placeholder || $t('common.select_placeholder') }}</option>
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
  // ariaLabel：显式透传到原生 <select>（默认 attrs 落在根 div 上，原生 select 拿不到 aria-label）
  ariaLabel: String,
  disabled: Boolean,
  error: Boolean,
  // placeholderSelectable：占位项（value=""）**可被选中**（默认 false = 仅占位、不可选，行为不变）。
  // 用途：空串本身即一种合法取值（如 llmref「跟随默认」——空 = 回落 defaultLLM，见 40-演进计划 §SL）。
  placeholderSelectable: Boolean
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
  cursor: pointer;
  appearance: none;
  -webkit-appearance: none;
}
.b-select__native:focus {
  border-color: var(--accent);
}
.b-select__native.is-error {
  border-color: var(--danger);
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