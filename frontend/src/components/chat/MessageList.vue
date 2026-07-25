<template>
  <div class="message-list" ref="listRef" @scroll="onScroll">
    <!-- Top indicator: shown when near top -->
    <div v-if="isAtTop && messages.length > 0" class="top-indicator">
      <template v-if="loadingMore">
        <span class="load-more-spinner"></span> 加载中...
      </template>
      <template v-else-if="!hasMore">
        到顶了
      </template>
    </div>

    <WelcomeMessage v-if="messages.length === 0" />
    <MessageItem
      v-for="(msg, i) in messages"
      :key="msg.id"
      :message="msg"
      :session-id="sessionId"
      :show-header="i === 0 || messages[i-1].role !== msg.role"
      :is-active="turnActive"
     
    />
    <Button v-if="!isAtTop" circle class="scroll-float-btn scroll-float-top" @click="scrollTop" title="Scroll to top">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <line x1="12" y1="20" x2="12" y2="7"/>
        <polyline points="5,14 12,7 19,14"/>
        <line x1="4" y1="12" x2="20" y2="12"/>
      </svg>
    </Button>
    <Button v-if="!isAtBottom" circle class="scroll-float-btn scroll-float-bottom" @click="scrollBottom" title="Scroll to bottom">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <line x1="12" y1="4" x2="12" y2="17"/>
        <polyline points="5,10 12,17 19,10"/>
        <line x1="4" y1="12" x2="20" y2="12"/>
      </svg>
    </Button>
  </div>
</template>

<script setup>
import { ref, onMounted, onUpdated, nextTick, onUnmounted } from 'vue'
import WelcomeMessage from './WelcomeMessage.vue'
import MessageItem from './MessageItem.vue'
import { Button } from '../ui'
import bridge from '../../utils/bridge'

const props = defineProps({
  messages: { type: Array, default: () => [] },
  turnActive: { type: Boolean, default: false },
  sessionId: { type: String, default: null },
  hasMore: { type: Boolean, default: true },
  loadingMore: { type: Boolean, default: false },
})

const emit = defineEmits(['loadMore'])

const listRef = ref(null)
const autoScroll = ref(true)
const isAtTop = ref(true)
const isAtBottom = ref(true)
const SCROLL_THRESHOLD = 20
const LOAD_MORE_THRESHOLD = 150  // px from top to trigger loadMore
let toppedOut = false  // true = already triggered loadMore at top, reset on scroll-down

// ── Replace all 5 watches with onUpdated + EventBus ──
// Track previous value for detecting turnActive transitions
const prevTurnActive = ref(false)

function doScroll() {
  nextTick(() => scrollToBottom())
}

function onCollapseReasoning() {
  autoScroll.value = true
  nextTick(async () => {
    await nextTick()
    scrollToBottom()
  })
}

onUpdated(() => {
  const ta = props.turnActive
  // When turn becomes active, re-enable auto-scroll
  if (ta && !prevTurnActive.value) {
    autoScroll.value = true
  }
  prevTurnActive.value = ta

  // Auto-scroll on any content change
  if (!autoScroll.value) return
  if (!ta) {
    // Turn just ended: double nextTick for render
    nextTick(async () => {
      await nextTick()
      scrollToBottom()
    })
  } else {
    nextTick(() => scrollToBottom())
  }
})

let _unsubCollapse = null
onMounted(() => {
  _unsubCollapse = bridge.on('message:collapse-reasoning', onCollapseReasoning)
})
onUnmounted(() => {
  if (_unsubCollapse) _unsubCollapse()
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

  // Load more: trigger when near top, but only once per scroll-up cycle.
  // toppedOut stays true until user scrolls down past threshold.
  if (el.scrollTop <= LOAD_MORE_THRESHOLD) {
    if (!toppedOut && props.hasMore && !props.loadingMore) {
      toppedOut = true
      const prevHeight = el.scrollHeight
      emit('loadMore')
      // Restore scroll position after messages are prepended
      nextTick(() => {
        nextTick(() => {
          if (listRef.value) {
            listRef.value.scrollTop = listRef.value.scrollHeight - prevHeight
          }
        })
      })
    }
  }
}

function scrollTop() {
  listRef.value?.scrollTo({ top: 0, behavior: 'smooth' })
}

function scrollBottom() {
  listRef.value?.scrollTo({ top: listRef.value.scrollHeight, behavior: 'smooth' })
}

defineExpose({ scrollTop, scrollBottom })

onMounted(() => onScroll())
</script>

<style scoped>
.top-indicator {
  text-align: center;
  padding: 8px;
  color: var(--text-muted, #888);
  font-size: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
}
.load-more-spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid var(--border, #ddd);
  border-top-color: var(--accent, #007bff);
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}
@keyframes spin {
  to { transform: rotate(360deg); }
}
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
