<template>
  <LfViewHost :view-ref="viewRef" :pooled="false" />
</template>

<script setup>
import { computed } from 'vue'
import LfViewHost from '../views/LfViewHost.vue'

const props = defineProps({
  viewKey: { type: String, required: true },
  instanceKey: { type: String, default: null },
})

const viewRef = computed(() => ({ viewKey: props.viewKey, instanceKey: props.instanceKey }))
</script>
