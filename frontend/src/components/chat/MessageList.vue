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
    <!-- Floating scroll buttons -->
    <el-tooltip content="Scroll to top" placement="right">
      <el-button v-if="!isAtTop" size="small" circle class="scroll-float-btn scroll-float-top" @click="scrollTop">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <line x1="12" y1="20" x2="12" y2="7"/>
          <polyline points="5,14 12,7 19,14"/>
          <line x1="4" y1="12" x2="20" y2="12"/>
        </svg>
      </el-button>
    </el-tooltip>
    <el-tooltip content="Scroll to bottom" placement="left">
      <el-button v-if="!isAtBottom" size="small" circle class="scroll-float-btn scroll-float-bottom" @click="scrollBottom">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <line x1="12" y1="4" x2="12" y2="17"/>
          <polyline points="5,10 12,17 19,10"/>
          <line x1="4" y1="12" x2="20" y2="12"/>
        </svg>
      </el-button>
    </el-tooltip>
  </div>
</template>

<script setup>
import { ref, onMounted, watch, nextTick } from 'vue'
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
const isAtTop = ref(true)
const isAtBottom = ref(true)
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
  isAtTop.value = el.scrollTop <= 1
  const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_THRESHOLD
  isAtBottom.value = atBottom
  autoScroll.value = atBottom
}

function scrollTop() {
  listRef.value?.scrollTo({ top: 0, behavior: 'smooth' })
}

function scrollBottom() {
  listRef.value?.scrollTo({ top: listRef.value.scrollHeight, behavior: 'smooth' })
}

defineExpose({ scrollTop, scrollBottom })

onMounted(() => onScroll())</script>

<style scoped>
.message-list {
  flex: 1;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  position: relative;
}
.scroll-float-top {
  position: sticky;
  left: 0;
  bottom: 8px;
  align-self: flex-start;
  pointer-events: auto;
  opacity: 0.7;
  background: var(--bg-surface, #fff);
  border: 1px solid var(--border);
  z-index: 10;
}
.scroll-float-bottom {
  position: sticky;
  left: 0;
  bottom: 8px;
  align-self: flex-end;
  pointer-events: auto;
  opacity: 0.7;
  background: var(--bg-surface, #fff);
  border: 1px solid var(--border);
  z-index: 10;
}
.scroll-float-top:hover,
.scroll-float-bottom:hover {
  opacity: 1;
}
</style>
