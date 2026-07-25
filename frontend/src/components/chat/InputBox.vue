<template>
  <div class="input-box">
    <textarea
      ref="inputRef"
      v-model="text"
      class="b-textarea b-textarea--autosize"
      placeholder="Ask ChonkPilot... (Enter to send on last empty line)"
      @keydown.enter="handleKeydown"
      @input="autoResize"
      :style="{ resize: 'none' }"
    />
    <div class="input-actions">
      <div class="input-actions-left">
        <slot name="controls" />
      </div>
      <div class="input-actions-right">
        <Button
          v-if="loading"
          type="danger"
          @click="$emit('cancel')"
        >
          Cancel
        </Button>
        <Button
          v-else
          type="primary"
          :disabled="!text.trim()"
          @click="handleSend"
        >
          Send
        </Button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, nextTick, onMounted, onUnmounted } from 'vue'
import { Button } from '../ui'

const props = defineProps({
  loading: { type: Boolean, default: false },
})

const emit = defineEmits(['send', 'cancel'])
const text = ref('')
const inputRef = ref(null)


function autoResize() {
  const el = inputRef.value
  if (!el) return
  el.style.height = 'auto'
  const minH = 2 * 20
  const maxH = 12 * 20
  const scrollH = el.scrollHeight
  el.style.height = Math.min(Math.max(scrollH, minH), maxH) + 'px'
  if (scrollH > maxH) {
    el.style.overflowY = 'auto'
  } else {
    el.style.overflowY = 'hidden'
  }
}

function handleSend() {
  if (text.value.trim() && !props.loading) {
    emit('send', text.value)
    text.value = ''
  }
}

function handleKeydown(e) {
  if (e.shiftKey || e.ctrlKey || e.altKey || e.metaKey) return

  const val = text.value
  const cursorPos = e.target.selectionStart ?? val.length

  const lineStart = val.lastIndexOf('\n', cursorPos - 1) + 1
  const lineEnd = val.indexOf('\n', cursorPos)
  const lineEndPos = lineEnd === -1 ? val.length : lineEnd

  const onLastLine = lineEnd === -1
  const currentLine = val.substring(lineStart, lineEndPos)
  const lineEmpty = currentLine.trim() === ''

  if (onLastLine && lineEmpty) {
    e.preventDefault()
    handleSend()
    return
  }
}

function insertAtCursor(insertText) {
  const el = inputRef.value
  if (!el) {
    text.value += insertText
    return
  }
  const start = el.selectionStart ?? text.value.length
  const end = el.selectionEnd ?? start
  text.value = text.value.slice(0, start) + insertText + text.value.slice(end)
  const newPos = start + insertText.length
  nextTick(() => {
    try {
      el.setSelectionRange(newPos, newPos)
      el.focus()
    } catch (e) { console.error('[InputBox] setSelectionRange error:', e) }
  })
}

function onInsertText(e) {
  if (e.detail?.text) {
    insertAtCursor(e.detail.text)
  }
}

onMounted(() => {
  window.addEventListener('chat:insert-text', onInsertText)
  nextTick(() => autoResize())
})

onUnmounted(() => {
  window.removeEventListener('chat:insert-text', onInsertText)
})
</script>

<style scoped>
.input-box {
  padding: 8px;
  border-top: 1px solid var(--border);
}

.b-textarea--autosize {
  width: 100%;
  padding: 5px 11px;
  border: 1px solid var(--border-color, #d9d9d9);
  border-radius: var(--border-radius, 4px);
  font-size: var(--font-size-sm, 13px);
  background: var(--bg-primary, #fff);
  color: var(--text-primary);
  outline: none;
  transition: border-color 0.2s;
  box-sizing: border-box;
  line-height: 20px;
  font-family: inherit;
  min-height: 40px;
  max-height: 240px;
}
.b-textarea--autosize:focus {
  border-color: var(--accent-color, #409eff);
}
.b-textarea--autosize::placeholder {
  color: var(--text-muted, #999);
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