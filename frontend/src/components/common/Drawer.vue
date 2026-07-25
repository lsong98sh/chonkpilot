<template>
  <div class="drawer-shell" v-if="visible">
    <div class="drawer-overlay" @click="close" />
    <div class="drawer-content" :style="{ width: size + 'px' }">
      <div class="drawer-header">
        <span class="drawer-title">{{ title }}</span>
        <Button text @click="close">
          <Icon name="close" />
        </Button>
      </div>
      <div class="drawer-body">
        <slot />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { Button } from '../ui'
import Icon from '../icon/Icon.vue'

defineProps({
  title: { type: String, default: '' },
  size: { type: Number, default: 320 },
})

const visible = ref(false)

function open() { visible.value = true }
function close() { visible.value = false }

defineExpose({ open, close })
</script>

<style scoped>
.drawer-shell {
  position: fixed;
  top: 0;
  right: 0;
  height: 100%;
  z-index: 1000;
}
.drawer-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0,0,0,0.3);
}
.drawer-content {
  position: fixed;
  top: 0;
  right: 0;
  height: 100%;
  background: var(--bg-primary);
  border-left: 1px solid var(--border);
  box-shadow: -2px 0 8px rgba(0,0,0,0.1);
  display: flex;
  flex-direction: column;
}
.drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.drawer-title {
  font-weight: 600;
  font-size: 15px;
}
.drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}
</style>