<template>
  <div class="minimized-bar">
    <Tooltip content="Restore ChonkPilot" placement="right">
      <Button circle @click="$emit('restore')">
        <Icon name="chat-dot-square" />
      </Button>
    </Tooltip>
  </div>
</template>

<script setup>
import Icon from '../icon/Icon.vue'
import { Button, Tooltip } from '../ui'
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
