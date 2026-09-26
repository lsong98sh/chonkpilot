<template>
  <span
    class="b-switch"
    :class="{ 'is-checked': modelValue, 'is-disabled': disabled }"
    @click="!disabled && $emit('update:modelValue', !modelValue)"
  >
    <span class="b-switch__dot" />
  </span>
</template>
<script setup>
defineProps({
  modelValue: Boolean,
  disabled: Boolean
})
defineEmits(['update:modelValue'])
</script>

<style scoped>
.b-switch {
  position: relative;
  display: inline-flex;
  width: 36px;
  height: 20px;
  background: var(--border);
  border-radius: 10px;
  cursor: pointer;
  transition: background 0.2s;
  flex-shrink: 0;
}
.b-switch.is-checked {
  background: var(--accent);
}
.b-switch.is-disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.b-switch__dot {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  background: #fff;
  border-radius: 50%;
  transition: transform 0.2s;
  box-shadow: 0 1px 2px rgba(0,0,0,0.15);
}
.b-switch.is-checked .b-switch__dot {
  transform: translateX(16px);
}
</style>