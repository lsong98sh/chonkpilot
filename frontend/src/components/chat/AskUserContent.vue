<template>
  <div class="ask-user-content">
    <div v-if="subSessionId" class="session-tag">
      Session: #{{ subSessionId.slice(0, 8) }}
    </div>
    <div class="question-content">{{ question }}</div>

    <div v-if="options.length > 0" class="options-list">
      <button
        v-for="(opt, idx) in options"
        :key="idx"
        class="option-btn"
        :class="{ selected: selectedOption === opt }"
        @click="selectOption(opt)"
      >{{ opt }}</button>
    </div>

    <div v-if="custom" class="custom-input">
      <textarea
        v-model="customAnswer"
        :rows="3"
        placeholder="Type your custom answer..."
        :disabled="selectedOption !== null"
      ></textarea>
    </div>

    <div class="footer">
      <button
        class="submit-btn"
        :disabled="!canSubmit"
        @click="submitAnswer"
      >Send Answer</button>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'

const props = defineProps({
  question: String,
  options: { type: Array, default: () => [] },
  custom: { type: Boolean, default: false },
  subSessionId: { type: String, default: '' },
})

const emit = defineEmits(['answer'])

const selectedOption = ref(null)
const customAnswer = ref('')

const canSubmit = computed(() => {
  return !!selectedOption.value || (props.custom && customAnswer.value.trim())
})

function selectOption(opt) {
  selectedOption.value = opt
  customAnswer.value = ''
}

function submitAnswer() {
  const answer = selectedOption.value || customAnswer.value.trim()
  if (!answer) return
  emit('answer', answer)
}
</script>

<style scoped>
.ask-user-content {
  padding: 4px 0;
}

.question-content {
  font-size: 15px;
  line-height: 1.6;
  color: var(--text-primary);
  margin-bottom: 20px;
  white-space: pre-wrap;
}

.session-tag {
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 10px;
}

.options-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 16px;
}

.option-btn {
  width: 100%;
  text-align: left;
  padding: 10px 16px;
  border: 1px solid var(--border-color, #d9d9d9);
  border-radius: 4px;
  background: var(--bg-primary, #fff);
  cursor: pointer;
  font-size: 14px;
  color: var(--text-primary);
  transition: border-color 0.15s, background 0.15s;
}

.option-btn:hover {
  border-color: var(--accent-color, #409eff);
}

.option-btn.selected {
  border-color: var(--accent-color, #409eff);
  background: var(--accent-bg, #ecf5ff);
  color: var(--accent-color, #409eff);
}

.custom-input textarea {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--border-color, #d9d9d9);
  border-radius: 4px;
  font-size: 14px;
  resize: vertical;
  background: var(--bg-primary, #fff);
  color: var(--text-primary);
  box-sizing: border-box;
  font-family: inherit;
}

.custom-input textarea:disabled {
  opacity: 0.5;
}

.footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.submit-btn {
  padding: 8px 20px;
  background: var(--accent-color, #409eff);
  color: #fff;
  border: none;
  border-radius: 4px;
  font-size: 14px;
  cursor: pointer;
  transition: opacity 0.15s;
}

.submit-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
