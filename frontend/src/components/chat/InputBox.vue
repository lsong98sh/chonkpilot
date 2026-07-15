<template>
  <div class="input-box">
    <el-input
      ref="inputRef"
      v-model="text"
      type="textarea"
      :autosize="{ minRows: 2, maxRows: 12 }"
      placeholder="Ask ChonkPilot... (Enter to send on last empty line)"
      @keydown.enter="handleKeydown"
      resize="none"
    />
    <div class="input-actions">
      <div class="input-actions-left">
        <slot name="controls" />
      </div>
      <div class="input-actions-right">
        <el-button
          v-if="loading"
          type="danger"
          size="small"
          @click="$emit('cancel')"
        >
          Cancel
        </el-button>
        <el-button
          v-else
          type="primary"
          size="small"
          :disabled="!text.trim()"
          @click="handleSend"
          :icon="'Promotion'"
        >
          Send
        </el-button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const props = defineProps({
  loading: { type: Boolean, default: false },
})

const emit = defineEmits(['send', 'cancel'])
const text = ref('')
const inputRef = ref(null)

function handleSend() {
  if (text.value.trim() && !props.loading) {
    emit('send', text.value)
    text.value = ''
  }
}

function handleKeydown(e) {
  // Any modifier key → let Enter insert newline
  if (e.shiftKey || e.ctrlKey || e.altKey || e.metaKey) return

  const val = text.value
  const cursorPos = e.target.selectionStart ?? val.length

  // Find the line boundaries at cursor
  const lineStart = val.lastIndexOf('\n', cursorPos - 1) + 1
  const lineEnd = val.indexOf('\n', cursorPos)
  const lineEndPos = lineEnd === -1 ? val.length : lineEnd

  // Send only when: cursor on last line AND that line is empty/whitespace-only
  const onLastLine = lineEnd === -1
  const currentLine = val.substring(lineStart, lineEndPos)
  const lineEmpty = currentLine.trim() === ''

  if (onLastLine && lineEmpty) {
    e.preventDefault()
    handleSend()
    return
  }

  // Otherwise: let Enter insert newline (don't prevent)
}
</script>

<style scoped>
.input-box {
  padding: 8px;
  border-top: 1px solid var(--border);
}

.input-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
}

.input-actions-left {
  display: flex;
  align-items: center;
  gap: 6px;
}

.input-actions-right {
  display: flex;
  align-items: center;
}
</style>
