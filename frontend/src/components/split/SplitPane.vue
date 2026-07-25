<template>
  <div
    v-if="visible"
    class="split-pane"
    :class="{
      'is-flex': flex,
      'is-fixed': !flex,
    }"
    :style="paneStyle"
  >
    <div v-if="$slots.header" class="split-pane-header">
      <slot name="header" />
    </div>
    <div class="split-pane-body">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, onMounted, ref } from 'vue'

export type PaneDirection = 'horizontal' | 'vertical'

const props = withDefaults(defineProps<{
  size?: number
  min?: number
  max?: number
  resizable?: boolean
  visible?: boolean
  flex?: boolean
}>(), {
  size: 200,
  min: 100,
  max: 2000,
  resizable: true,
  visible: true,
  flex: false,
})

const paneRef = ref<HTMLElement | null>(null)

// Register with parent SplitPanel if available (provide/inject registration)
const registerPane = inject<(id: string, instance: {
  ref: typeof paneRef
  props: typeof props
  el: typeof paneRef
}) => void>('registerPane', null)

onMounted(() => {
  if (registerPane) {
    const id = `pane-${Math.random().toString(36).slice(2, 9)}`
    registerPane(id, {
      ref: paneRef,
      props: props as any,
      el: paneRef,
    })
  }
})

const paneStyle = computed(() => {
  if (props.flex) {
    return {
      flex: '1',
      minWidth: 0,
      minHeight: 0,
    }
  }
  return {
    flex: '0 0 auto',
    width: `${props.size}px` as const,
    minWidth: `${props.min}px` as const,
    maxWidth: `${props.max}px` as const,
  }
})
</script>

<style scoped>
.split-pane {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  position: relative;
}

.split-pane.is-flex {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.split-pane.is-fixed {
  flex-shrink: 0;
}

.split-pane-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  min-height: 28px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.8px;
  color: var(--text-muted);
  text-transform: uppercase;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}

.split-pane-body {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}
</style>
