<template>
  <div class="diff-preview">
    <div v-for="(line, i) in lines" :key="i" class="diff-line" :class="line.type">
      <span class="line-num">{{ i + 1 }}</span>
      <span class="line-content">{{ line.text }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  content: { type: String, default: '' },
})

const lines = computed(() => {
  return (props.content || '').split('\n').map(line => {
    let type = 'normal'
    if (line.startsWith('+')) type = 'added'
    else if (line.startsWith('-')) type = 'removed'
    else if (line.startsWith('@@')) type = 'header'
    return { text: line, type }
  })
})
</script>

<style scoped>
.diff-preview {
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.5;
  background: var(--bg-primary);
}

.diff-line {
  display: flex;
  padding: 0 8px;
}

.line-num {
  width: 40px;
  text-align: right;
  padding-right: 12px;
  color: var(--text-muted);
  flex-shrink: 0;
}

.line-content {
  white-space: pre;
}

.added { background: rgba(166, 227, 161, 0.1); }
.removed { background: rgba(243, 139, 168, 0.1); }
.header { color: var(--accent); }
</style>
