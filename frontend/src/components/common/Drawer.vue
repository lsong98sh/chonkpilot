<template>
  <el-drawer v-model="visible" :size="320" :title="title">
    <slot />
  </el-drawer>
</template>

<script setup>
import { ref } from 'vue'

defineProps({
  title: { type: String, default: '' },
})

const visible = ref(false)

function open() { visible.value = true }
function close() { visible.value = false }

defineExpose({ open, close })
</script>
