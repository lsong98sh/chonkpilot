<template>
  <div class="html-preview">
    <iframe :srcdoc="content" sandbox="allow-scripts" />
  </div>
</template>

<script setup>
defineProps({
  content: { type: String, default: '' },
})
</script>

<style scoped>
.html-preview {
  height: 100%;
}

.html-preview iframe {
  width: 100%;
  height: 100%;
  border: none;
  background: white;
}
</style>
