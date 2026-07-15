<template>
  <Transition name="fade">
    <div v-if="visible" class="error-banner" :type="type">
      <span class="error-text">{{ message }}</span>
      <el-button text size="small" @click="visible = false">
        <el-icon><Close /></el-icon>
      </el-button>
    </div>
  </Transition>
</template>

<script setup>
import { ref, watch, onUnmounted } from 'vue'
import { Close } from '@element-plus/icons-vue'

const props = defineProps({
  message: { type: String, default: '' },
  type: { type: String, default: 'error' },
  autoClose: { type: Number, default: 5000 },
})

const visible = ref(false)
let closeTimer = null

watch(() => props.message, (val) => {
  if (closeTimer) clearTimeout(closeTimer)
  if (val) {
    visible.value = true
    if (props.autoClose > 0) {
      closeTimer = setTimeout(() => { visible.value = false }, props.autoClose)
    }
  }
})

onUnmounted(() => {
  if (closeTimer) clearTimeout(closeTimer)
})
</script>

<style scoped>
.error-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-radius: 4px;
  margin: 8px;
  font-size: 13px;
}

.error-banner[type="error"] { background: rgba(243, 139, 168, 0.1); color: var(--danger); }
.error-banner[type="warning"] { background: rgba(249, 226, 175, 0.1); color: var(--warning); }
.error-banner[type="success"] { background: rgba(166, 227, 161, 0.1); color: var(--success); }

.fade-enter-active, .fade-leave-active { transition: opacity var(--transition-normal); }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
