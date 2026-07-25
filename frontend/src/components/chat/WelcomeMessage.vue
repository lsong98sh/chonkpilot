<template>
  <div class="welcome">
    <Icon name="chat-dot-round" :size="40" color="var(--accent)" />
    <h2>ChonkPilot</h2>
    <p>AI-powered code development assistant</p>
    <div class="suggestions">
      <Tag
        v-for="s in suggestions"
        :key="s"
        @click="$emit('suggest', s)"
        class="suggestion-tag"
      >
        {{ s }}
      </Tag>
    </div>
  </div>
</template>

<script setup>
import Icon from '../icon/Icon.vue'
import { Tag } from '../ui'

defineEmits(['suggest'])

const suggestions = [
  'Explain this code',
  'Find bugs',
  'Add comments',
  'Write tests',
]
</script>

<style scoped>
.welcome {
  text-align: center;
  padding: 40px 20px;
  color: var(--text-muted);
}

.welcome h2 {
  margin: 12px 0 4px;
  font-size: 18px;
  color: var(--text-primary);
}

.welcome p {
  font-size: 13px;
  margin-bottom: 20px;
}

.suggestions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  justify-content: center;
}

.suggestion-tag {
  cursor: pointer;
}
</style>