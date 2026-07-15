<template>
  <div class="message-list" ref="listRef" @scroll="onScroll">
    <WelcomeMessage v-if="messages.length === 0" />
    <MessageItem
      v-for="(msg, i) in messages"
      :key="msg.id"
      :message="msg"
      :session-id="sessionId"
      :show-header="i === 0 || messages[i-1].role !== msg.role"
      :is-active="turnActive"
      :collapse-key="collapseReasoning"
    />
  </div>
</template>

<script setup>
import { ref, watch, nextTick } from 'vue'
import WelcomeMessage from './WelcomeMessage.vue'
import MessageItem from './MessageItem.vue'

const props = defineProps({
  messages: { type: Array, default: () => [] },
  turnActive: { type: Boolean, default: false },
  collapseReasoning: { type: Number, default: 0 }, // bump to signal reasoning collapse
  sessionId: { type: String, default: null },
})

const listRef = ref(null)
const autoScroll = ref(true)
const SCROLL_THRESHOLD = 20 // px from bottom to consider "at bottom"

// When a new turn starts, re-enable auto-scroll
watch(() => props.turnActive, (val) => {
  if (val) {
    autoScroll.value = true
    nextTick(() => scrollToBottom())
  }
})

// When messages change, auto-scroll if enabled
watch(() => props.messages.length, async () => {
  if (!autoScroll.value) return
  await nextTick()
  scrollToBottom()
})

// Also watch content changes (streaming) to auto-scroll
watch(() => {
  const m = props.messages
  if (m.length === 0) return ''
  return m[m.length - 1].content
}, async () => {
  if (!autoScroll.value) return
  await nextTick()
  scrollToBottom()
})

// When turn ends (streaming done), collapse thinking then scroll
watch(() => props.turnActive, async (val) => {
  if (!val) {
    await nextTick()
    await nextTick()
    scrollToBottom()
  }
})

// When reasoning collapses (text reply starts), re-scroll
watch(() => props.collapseReasoning, async () => {
  await nextTick()
  await nextTick()
  scrollToBottom()
})

function scrollToBottom() {
  if (listRef.value) {
    listRef.value.scrollTop = listRef.value.scrollHeight
  }
}

function onScroll() {
  const el = listRef.value
  if (!el) return
  const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_THRESHOLD
  autoScroll.value = atBottom
}

function scrollTop() {
  listRef.value?.scrollTo({ top: 0, behavior: 'smooth' })
}

function scrollBottom() {
  listRef.value?.scrollTo({ top: listRef.value.scrollHeight, behavior: 'smooth' })
}

defineExpose({ scrollTop, scrollBottom })</script>

<style scoped>
.message-list {
  flex: 1;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
</style>
