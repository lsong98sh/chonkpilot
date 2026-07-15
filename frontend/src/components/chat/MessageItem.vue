<template>
  <div class="message-item" :class="[message.role, message.type]">
    <!-- Speaker line: left to right -->
    <div v-if="showHeader && (message.role === 'assistant' || message.role === 'user' || message.role === 'tool' || message.type === 'tool_pair')" class="speaker-line" :class="message.role === 'tool_pair' ? 'assistant' : message.role">
      <!-- Assistant: icon → name → time -->
      <template v-if="message.role === 'assistant' || message.type === 'tool_pair'">
        <span class="speaker-icon assistant">
          <el-icon :size="14"><MagicStick /></el-icon>
        </span>
        <span class="speaker-name">ChonkPilot</span>
        <span class="speaker-time">{{ formattedTime }}</span>
      </template>
      <!-- Tool: time → name → icon (right-aligned) -->
      <template v-else-if="message.role === 'tool'">
        <span class="speaker-time">{{ formattedTime }}</span>
        <span class="speaker-name tool-name">Tool</span>
        <span class="speaker-icon tool">
          <el-icon :size="14"><Setting /></el-icon>
        </span>
      </template>
      <!-- User: time → name → icon (right-aligned) -->
      <template v-else>
        <span class="speaker-time">{{ formattedTime }}</span>
        <span class="speaker-name">You</span>
        <span class="speaker-icon user">
          <el-icon :size="14"><User /></el-icon>
        </span>
      </template>
    </div>

    <!-- Collapsible sections for reasoning / tool_call / tool_result -->
    <div v-if="message.type === 'reasoning'" class="collapsible-section reasoning-section">
      <div class="section-header" @click="toggleCollapse">
        <el-icon :size="12"><component :is="collapsed ? 'ArrowRight' : 'ArrowDown'" /></el-icon>
        <span class="section-label">Thinking</span>
        <span v-if="loadingMore" class="loading-icon">loading...</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ localContent }}</pre>
        <div v-if="showMore" class="more-bar">
          <span class="more-link" @click.stop="loadMore">+ more</span>
        </div>
      </div>
    </div>

    <div v-else-if="message.type === 'tool_call'" class="collapsible-section toolcall-section">
      <div class="section-header" @click="toggleCollapse">
        <el-icon :size="12"><component :is="collapsed ? 'ArrowRight' : 'ArrowDown'" /></el-icon>
        <span class="section-label">Tool Call</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ message.content }}</pre>
      </div>
    </div>

    <div v-else-if="message.type === 'tool_result'" class="collapsible-section toolresult-section">
      <div class="section-header" @click="toggleCollapse">
        <el-icon :size="12"><component :is="collapsed ? 'ArrowRight' : 'ArrowDown'" /></el-icon>
        <span class="section-label">Tool Result</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ message.content }}</pre>
      </div>
    </div>

    <!-- Tool pair (tool_call + tool_result combined) -->
    <div v-else-if="message.type === 'tool_pair'" class="collapsible-section toolpair-section">
      <div class="section-header" @click="toggleCollapse">
        <el-icon :size="12"><component :is="collapsed ? 'ArrowRight' : 'ArrowDown'" /></el-icon>
        <span class="section-label tool-name-label">{{ message.tool || 'tool' }}</span>
        <span v-if="message.status === 'pending'" class="status-badge pending">pending</span>
        <span v-else-if="message.status === 'failed'" class="status-badge failed">failed</span>
        <span v-else-if="message.status === 'done'" class="status-badge done">done</span>
        <span v-if="loadingMore" class="loading-icon">...</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <div class="pair-section">
          <div class="pair-sub-label">Arguments</div>
          <pre class="force-wrap">{{ prettyArgs }}</pre>
        </div>
        <div class="pair-section">
          <div class="pair-sub-label">Result</div>
          <template v-if="showMore">
            <!-- Brief mode: show one-line summary + raw data button -->
            <pre class="force-wrap">{{ message.result_simplified || 'ok' }}</pre>
            <div class="more-bar">
              <span class="more-link" @click.stop="loadMore">[raw data]</span>
              <span v-if="loadingMore" class="loading-icon">loading...</span>
            </div>
          </template>
          <template v-else-if="localResult">
            <!-- Full result available (non-brief or loaded) -->
            <pre class="force-wrap">{{ localResult }}</pre>
          </template>
        </div>
      </div>
    </div>

    <!-- Regular text content (assistant reply or user message) -->
    <div v-else class="message-bubble">
      <div class="message-content" v-html="renderedContent" />
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { User, MagicStick, Setting, ArrowRight, ArrowDown } from '@element-plus/icons-vue'

const props = defineProps({
  message: { type: Object, required: true },
  showHeader: { type: Boolean, default: true },
  isActive: { type: Boolean, default: false },
  collapseKey: { type: Number, default: 0 },
  sessionId: { type: String, default: null },
})

const collapsed = ref(true)
const loadingMore = ref(false)     // loading indicator for "more" button
const contentComplete = ref(false) // full content has been loaded via "more"

// Local content — from prop (streamed) or preview (brief) initially
const localResult = ref(props.message.result || '')
const localContent = ref(props.message.content || '')

// Show "more" link when there is additional content not yet loaded
const showMore = computed(() => props.message.has_more && !contentComplete.value)

watch(() => props.isActive, (val) => {
  if (val && props.message.type === 'reasoning') {
    collapsed.value = false
  } else if (!val && props.message.type === 'reasoning') {
    collapsed.value = true
  }
}, { immediate: true })

watch(() => props.collapseKey, () => {
  if (props.message.type === 'reasoning') {
    collapsed.value = true
  }
})

// Watch for live streaming content updates (handleToken mutates .content / .result in place)
// Using deep:true so property mutations on the same object are detected
watch(() => props.message, (m) => {
  if (!m) return
  localContent.value = m.content || ''
  localResult.value = m.result || ''
}, { immediate: true, deep: true })

function toggleCollapse() {
  collapsed.value = !collapsed.value
}

async function loadMore() {
  if (loadingMore.value || !props.sessionId) return
  loadingMore.value = true
  try {
    const { getMessageContent } = await import('../../api/session')
    const key = props.message.type === 'tool_pair'
      ? 'tool_call:' + props.message.tool_call_id
      : 'message:' + (props.message.id || props.message.message_id || '')
    if (!key.includes(':')) return
    const res = await getMessageContent(props.sessionId, [key])
    const full = res?.[key]
    if (full) {
      if (props.message.type === 'tool_pair') localResult.value = full
      else localContent.value = full
    }
    contentComplete.value = true
  } catch (e) {
    console.warn('[MessageItem] Failed to load content:', e)
    // On failure, still mark complete so the "more" link goes away
    contentComplete.value = true
  } finally {
    loadingMore.value = false
  }
}

const renderedContent = computed(() => {
  const content = localContent.value || props.message.content || ''
  return DOMPurify.sanitize(marked(content || ''))
})

const prettyArgs = computed(() => {
  const args = props.message.arguments
  if (!args) return ''
  try {
    const parsed = typeof args === 'string' ? JSON.parse(args) : args
    return JSON.stringify(parsed, null, 2)
  } catch (_) {
    return args
  }
})

const formattedTime = computed(() => {
  const date = new Date(props.message.createdAt)
  if (isNaN(date.getTime())) return ''
  const now = new Date()
  const isToday = date.getFullYear() === now.getFullYear()
    && date.getMonth() === now.getMonth()
    && date.getDate() === now.getDate()
  const hours = date.getHours().toString().padStart(2, '0')
  const mins = date.getMinutes().toString().padStart(2, '0')
  if (isToday) {
    return `${hours}:${mins}`
  } else {
    const month = (date.getMonth() + 1).toString().padStart(2, '0')
    const day = date.getDate().toString().padStart(2, '0')
    return `${month}-${day} ${hours}:${mins}`
  }
})
</script>

<style scoped>
.message-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

/* User and tool messages right-aligned */
.message-item.user,
.message-item.tool {
  align-items: flex-end;
}

/* Assistant messages left-aligned */
.message-item.assistant {
  align-items: flex-start;
}

/* Speaker line: timestamp → icon → name */
.speaker-line {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 0;
  font-size: 12px;
}

/* Assistant: left aligned (icon → name → time) */
.speaker-line.assistant {
  flex-direction: row;
}

/* User: right aligned (time → name → icon) */
.speaker-line.user {
  flex-direction: row;
  justify-content: flex-end;
}

.speaker-icon {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.speaker-icon.assistant {
  background: var(--accent);
  color: var(--bg-primary);
}

.speaker-icon.user {
  background: var(--success);
  color: var(--bg-primary);
}

.speaker-icon.tool {
  background: var(--warning);
  color: var(--bg-primary);
}

.speaker-name {
  font-weight: 600;
  color: var(--text-primary);
  font-size: 12px;
}

.tool-name {
  color: var(--warning);
}

.speaker-time {
  color: var(--text-muted);
  font-size: 11px;
}

/* Regular bubble (text type) */
.message-bubble {
  max-width: 100%;
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.5;
}

.assistant.text .message-bubble {
  background: var(--bg-primary);
  color: var(--text-primary);
}

.user.text .message-bubble {
  background: var(--accent);
  color: var(--bg-primary);
}

.message-content :deep(pre) {
  background: var(--bg-tertiary);
  padding: 8px;
  border-radius: 4px;
  overflow-x: auto;
  margin: 8px 0;
  font-family: var(--font-mono);
  font-size: 12px;
}

.message-content :deep(code) {
  word-break: break-all;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.message-content :deep(p) {
  word-break: break-word;
  overflow-wrap: anywhere;
}

/* Markdown tables: ensure visible borders on any background */
.message-content :deep(table) {
  border-collapse: collapse;
  width: 100%;
  margin: 8px 0;
}
.message-content :deep(th),
.message-content :deep(td) {
  border: 1px solid var(--text-muted);
  padding: 6px 10px;
  text-align: left;
}
.message-content :deep(th) {
  background: var(--bg-tertiary);
  font-weight: 600;
}

/* Collapsible sections */
.collapsible-section {
  border-radius: 6px;
  overflow: hidden;
  font-size: 13px;
  line-height: 1.5;
  max-width: 100%;
}

.section-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
  user-select: none;
  border-radius: 4px;
}

.section-header:hover {
  opacity: 0.8;
}

.reasoning-section .section-header {
  background: rgba(255, 193, 7, 0.12);
  color: #b8860b;
}

.toolcall-section .section-header {
  background: rgba(33, 150, 243, 0.12);
  color: #1565c0;
}

.toolresult-section .section-header {
  background: rgba(76, 175, 80, 0.12);
  color: #2e7d32;
}

.toolpair-section .section-header {
  background: rgba(156, 39, 176, 0.10);
  color: #7b1fa2;
}

.tool-name-label {
  font-weight: 700;
}

.status-badge {
  font-size: 10px;
  font-weight: 600;
  padding: 0 5px;
  border-radius: 3px;
  margin-left: 4px;
  line-height: 16px;
}

.status-badge.pending {
  background: rgba(255, 152, 0, 0.20);
  color: #e65100;
}

.status-badge.done {
  background: rgba(76, 175, 80, 0.20);
  color: #2e7d32;
}

.status-badge.failed {
  background: rgba(244, 67, 54, 0.20);
  color: #c62828;
}

.pair-section {
  margin-bottom: 6px;
}

.pair-section:last-child {
  margin-bottom: 0;
}

.pair-sub-label {
  font-size: 11px;
  font-weight: 600;
  color: var(--text-muted);
  margin-bottom: 2px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.section-label {
  font-size: 12px;
}

.section-body {
  padding: 6px 8px 8px 20px;
  background: var(--bg-tertiary);
  border-radius: 0 0 4px 4px;
}

/* Force-wrap: break long strings without spaces */
.force-wrap {
  margin: 0;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.4;
  word-break: break-all;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.more-bar {
  margin-top: 6px;
  display: flex;
  align-items: center;
}

.more-link {
  font-size: 11px;
  color: var(--accent);
  cursor: pointer;
  user-select: none;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 3px;
  background: var(--bg-secondary);
  transition: opacity 0.15s;
}

.more-link:hover {
  opacity: 0.7;
}

.loading-icon {
  font-size: 11px;
  color: var(--text-muted);
  margin-left: 4px;
}
</style>
