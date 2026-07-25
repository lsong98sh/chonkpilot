<template>
  <div class="b-tabs">
    <div class="b-tabs-header">
      <span
        v-for="tab in tabs"
        :key="tab.name"
        class="b-tabs-item"
        :class="{ active: tab.name === currentValue }"
        @click="$emit('update:modelValue', tab.name)"
      >
        {{ tab.label }}
      </span>
    </div>
    <div class="b-tabs-body">
      <slot :name="currentValue" />
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  tabs: { type: Array, required: true },
  modelValue: { type: String, default: '' },
})
defineEmits(['update:modelValue'])

const currentValue = computed(() => props.modelValue || props.tabs?.[0]?.name || '')
</script>

<style scoped>
.b-tabs {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.b-tabs-header {
  display: flex;
  align-items: center;
  gap: 0;
  border-bottom: 1px solid var(--border, #dee2e6);
  flex-shrink: 0;
  user-select: none;
}

.b-tabs-item {
  position: relative;
  padding: 8px 16px;
  font-size: var(--font-size-sm, 13px);
  color: var(--text-secondary, #495057);
  cursor: pointer;
  transition: color 0.15s;
  white-space: nowrap;
}

.b-tabs-item:hover {
  color: var(--text-primary, #212529);
}

.b-tabs-item.active {
  color: var(--accent, #4361ee);
  font-weight: 500;
}

.b-tabs-item.active::after {
  content: '';
  position: absolute;
  bottom: -1px;
  left: 0;
  right: 0;
  height: 2px;
  background: var(--accent, #4361ee);
  border-radius: 1px 1px 0 0;
}

.b-tabs-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
