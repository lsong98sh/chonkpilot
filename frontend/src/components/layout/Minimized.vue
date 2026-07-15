<template>
  <div class="minimized-bar">
    <el-tooltip content="Restore ChonkPilot" placement="right">
      <el-button :icon="ChatDotSquare" circle @click="$emit('restore')" />
    </el-tooltip>
  </div>
</template>

<script setup>
import { ChatDotSquare } from '@element-plus/icons-vue'
defineEmits(['restore'])
</script>

<style scoped>
.minimized-bar {
  position: fixed;
  bottom: 20px;
  right: 20px;
  z-index: 9999;
}
</style>
