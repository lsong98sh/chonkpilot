<template>
  <div class="message-item" :class="[message.role, message.type]">
    <div style="display:none" class="debug-info" :data-role="message.role" :data-type="message.type" :data-showheader="showHeader"></div>
    <!-- Avatar: only show for first message in a group -->
    <div v-if="showHeader && (message.role === 'assistant' || message.role === 'user' || message.role === 'tool' || message.type === 'tool_pair')" class="speaker-line" :class="message.role === 'tool_pair' ? 'assistant' : message.role">
      <template v-if="message.role === 'assistant' || message.type === 'tool_pair'">
        <span class="speaker-icon assistant">
            <svg style="width: 1em; height: 1em; vertical-align: middle; fill: currentcolor; overflow: hidden;" viewBox="0 0 1024 1024" version="1.1" xmlns="http://www.w3.org/2000/svg">
              <path d="M209.408 76.8c-27.093333-1.408-52.821333 1.92-76.501333 11.776-21.888 9.130667-34.090667 28.586667-39.893334 45.184-5.845333 16.554667-7.552 32.981333-8.021333 50.261333-0.896 34.474667 4.096 72.746667 10.752 110.08 11.861333 66.432 26.538667 116.010667 30.293333 128.896-20.053333 42.666667-40.704 92.074667-40.704 150.4 0 109.866667 49.962667 203.989333 128.256 267.434667C291.84 904.277333 397.397333 938.666667 512 938.666667c114.901333 0 220.373333-35.669333 298.496-99.584C888.618667 775.168 938.666667 681.429333 938.666667 573.397333c0-55.04-18.261333-105.216-40.746667-150.613333 3.541333-12.8 17.706667-62.549333 28.842667-129.109333 6.229333-37.333333 10.794667-75.648 9.472-110.165334-0.64-17.28-2.474667-33.706667-8.490667-50.176-5.973333-16.512-18.346667-35.712-40.149333-44.757333-47.317333-19.712-102.314667-12.8-160.085334 5.76-50.474667 16.213333-100.693333 45.824-142.677333 85.845333C560.768 175.36 536.533333 170.666667 512 170.666667a42.666667 42.666667 0 0 0-0.085333 0c-24.448 0.085333-48.64 4.096-72.661334 8.32a374.869333 374.869333 0 0 0-144.938666-85.504c-29.312-9.045333-57.770667-15.232-84.906667-16.64z m-36.693333 91.136c15.232-4.181333 53.333333-6.186667 96.426666 7.168 46.250667 14.293333 94.592 41.472 125.696 76.373333a42.666667 42.666667 0 0 0 41.386667 13.269334A342.528 342.528 0 0 1 512 256h0.085333c24.746667 0 50.176 3.328 74.410667 9.685333a42.666667 42.666667 0 0 0 42.666667-12.842666c31.36-35.242667 79.104-62.72 124.416-77.226667 42.453333-13.653333 79.786667-11.904 94.72-7.68 1.109333 4.394667 2.346667 9.557333 2.688 18.816 0.938667 23.808-2.688 58.581333-8.405334 92.842667-11.434667 68.522667-30.293333 135.424-30.293333 135.424a42.666667 42.666667 0 0 0 3.413333 31.744c22.4 42.112 37.632 85.632 37.632 126.634666 0 82.346667-35.968 149.888-96.853333 199.68C695.637333 822.869333 609.152 853.333333 512 853.333333c-97.450667 0-183.978667-29.653333-244.650667-78.848C206.634667 725.333333 170.666667 658.133333 170.666667 573.44c0-41.941333 17.578667-84.906667 38.4-128.213333a42.666667 42.666667 0 0 0 2.517333-30.592s-19.626667-66.858667-31.829333-135.424c-6.101333-34.261333-10.069333-69.12-9.386667-92.928 0.213333-9.002667 1.408-13.909333 2.389333-18.346667z"/>
              <path d="M341.333333 554.666667a42.666667 42.666667 0 0 0-42.666666 42.666666v21.333334a42.666667 42.666667 0 0 0 42.666666 42.666666 42.666667 42.666667 0 0 0 42.666667-42.666666V597.333333a42.666667 42.666667 0 0 0-42.666667-42.666666zM682.666667 554.666667a42.666667 42.666667 0 0 0-42.666667 42.666666v21.333334a42.666667 42.666667 0 0 0 42.666667 42.666666 42.666667 42.666667 0 0 0 42.666666-42.666666V597.333333a42.666667 42.666667 0 0 0-42.666666-42.666666zM480 650.666667a42.666667 42.666667 0 0 0-30.165333 72.832l32 32a42.666667 42.666667 0 0 0 60.330666 0l32-32a42.666667 42.666667 0 0 0-30.165333-72.832z"/>
            </svg>
        </span>
        <span class="speaker-name">ChonkPilot</span>
      </template>
      <template v-else-if="message.role === 'tool'">
        <span class="speaker-icon tool">
          <Icon name="setting" :size="14" />
        </span>
        <span class="speaker-name tool-name">Tool</span>
      </template>
      <template v-else>
        <span class="speaker-icon user">
          <Icon name="user" :size="14" />
        </span>
        <span class="speaker-name">You</span>
      </template>
    </div>

    <!-- Timestamp: show for every message -->
    <div v-if="message.role === 'assistant' || message.role === 'user' || message.role === 'tool' || message.type === 'tool_pair'" class="timestamp-line">
      <span class="speaker-time">{{ formattedTime }}</span>
    </div>

    <div v-if="message.type === 'reasoning'" class="collapsible-section reasoning-section">
      <div class="section-header" @click="toggleCollapse">
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label">Thinking</span>
        <span class="header-spacer" />
        <Tooltip content="Copy" placement="top" :show-after="600">
          <Icon name="copy-document" :size="13" class="copy-icon" title="Copy" @click.stop="copyTextContent" />
        </Tooltip>
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
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label">Tool Call</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ message.content }}</pre>
      </div>
    </div>

    <div v-else-if="message.type === 'tool_result'" class="collapsible-section toolresult-section">
      <div class="section-header" @click="toggleCollapse">
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label">Tool Result</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ message.content }}</pre>
      </div>
    </div>

    <div v-else-if="message.type === 'tool_pair'" class="collapsible-section toolpair-section">
      <div class="section-header" @click="toggleCollapse">
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label tool-name-label">{{ message.tool || 'tool' }}</span>
        <span v-if="message.status === 'pending'" class="status-badge pending">pending</span>
        <span v-else-if="message.status === 'failed'" class="status-badge failed">failed</span>
        <span v-else-if="message.status === 'done'" class="status-badge done">done</span>
        <span class="header-spacer" />
        <Tooltip content="Copy" placement="top" :show-after="600">
          <Icon name="copy-document" :size="13" class="copy-icon" title="Copy" @click.stop="copyToolContent" />
        </Tooltip>
        <span v-if="loadingMore" class="loading-icon">...</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <div class="brief-line">
          <span class="brief-text">{{ message.brief || message.simplified || message.tool }}</span>
          <span v-if="!showingFull" class="more-link" @click.stop="showFull">{{ moreLabel }}</span>
          <span v-if="loadingMore" class="loading-icon">loading...</span>
        </div>
        <template v-if="showingFull">
          <div v-if="!message.isOrphaned" class="pair-section">
            <div class="pair-sub-label">Arguments</div>
            <pre class="force-wrap">{{ prettyArgs }}</pre>
          </div>
          <div class="pair-section">
            <div class="pair-sub-label">Result</div>
            <pre class="force-wrap">{{ localResult }}</pre>
          </div>
        </template>
      </div>
    </div>

    <div v-else class="message-row">
      <div class="message-bubble">
        <div class="message-content" v-html="renderedContent" />
      </div>
      <div class="bubble-footer">
        <Icon name="copy-document" :size="14" class="copy-icon" title="Copy" @click.stop="copyTextContent" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onUpdated, onUnmounted } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { message as uiMessage } from '../ui'
import Icon from '../icon/Icon.vue'
import bridge from '../../utils/bridge'

const props = defineProps({
  message: { type: Object, required: true },
  showHeader: { type: Boolean, default: true },
  isActive: { type: Boolean, default: false },
  collapseKey: { type: Number, default: 0 },
  sessionId: { type: String, default: null },
})


const collapsed = ref(true)
const loadingMore = ref(false)
const contentComplete = ref(false)
const showingFull = ref(false)

const localResult = computed(() => props.message.result || '')
const localContent = computed(() => props.message.content || '')

const showMore = computed(() => props.message.has_more && !contentComplete.value)

const moreLabel = computed(() => {
  const bytes = props.message.content_size
  if (!bytes || bytes <= 0) return '[more]'
  let label
  if (bytes < 1024) label = bytes + 'B'
  else {
    const kb = bytes / 1024
    if (kb < 1024) label = (kb >= 10 ? Math.round(kb) : Math.round(kb * 10) / 10) + 'KB'
    else label = (kb / 1024 >= 10 ? Math.round(kb / 1024) : Math.round(kb / 1024 * 10) / 10) + 'MB'
  }
  return '[more: ' + label + ']'
})

// isActive-driven collapse handled in onUpdated + initial
const prevIsActive = ref(props.isActive)

// collapseKey-driven collapse replaced by bridge event
function onCollapseReasoning() {
  if (props.message.type === 'reasoning') {
    collapsed.value = true
  }
}



onUpdated(() => {
  if (props.message.type === 'reasoning') {
    if (props.isActive && !prevIsActive.value) {
      collapsed.value = false
    } else if (!props.isActive && prevIsActive.value) {
      collapsed.value = true
    }
    prevIsActive.value = props.isActive
  }
})

let _unsubCollapse = null
// Need to subscribe in a separate setup — use a wrapper
const _setupBridge = () => {
  _unsubCollapse = bridge.on('message:collapse-reasoning', onCollapseReasoning)
}
_setupBridge()

onUnmounted(() => {
  if (_unsubCollapse) _unsubCollapse()
})

function toggleCollapse() {
  collapsed.value = !collapsed.value
}

async function showFull() {
  if (showingFull.value) return
  if (!localResult.value && props.message.has_more && props.sessionId) {
    if (loadingMore.value) return
    loadingMore.value = true
    try {
      const { getMessageContent } = await import('../../api/session')
      const key = 'tool_call:' + props.message.tool_call_id
      if (!key.includes(':')) return
      const res = await getMessageContent(props.sessionId, [key])
      const full = res?.[key]
      if (full) {
        localResult.value = full
      }
      contentComplete.value = true
    } catch (e) {
      console.warn('[MessageItem] Failed to load content:', e)
      contentComplete.value = true
    } finally {
      loadingMore.value = false
    }
  }
  showingFull.value = true
}

async function loadMore() {
  if (loadingMore.value || !props.sessionId) return
  loadingMore.value = true
  try {
    const { getMessageContent } = await import('../../api/session')
    const key = 'message:' + (props.message.id || props.message.message_id || '')
    if (!key.includes(':')) return
    const res = await getMessageContent(props.sessionId, [key])
    const full = res?.[key]
    if (full) {
      localContent.value = full
    }
    contentComplete.value = true
  } catch (e) {
    console.warn('[MessageItem] Failed to load content:', e)
    contentComplete.value = true
  } finally {
    loadingMore.value = false
  }
}

async function copyTextContent() {
  if (!localContent.value && props.message.has_more && props.sessionId) {
    loadingMore.value = true
    try {
      const { getMessageContent } = await import('../../api/session')
      const key = 'message:' + (props.message.id || props.message.message_id || '')
      if (key.includes(':')) {
        const res = await getMessageContent(props.sessionId, [key])
        const full = res?.[key]
        if (full) localContent.value = full
      }
    } catch (e) { console.error('[MessageItem] getMessageContent error:', e) }
    loadingMore.value = false
  }
  const text = localContent.value || props.message.content || ''
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    uiMessage.success('Copied')
  } catch {
    uiMessage.warning('Copy failed')
  }
}

async function copyToolContent() {
  let argsText = ''
  let resultText = localResult.value || ''

  if (props.message.arguments) {
    try {
      const parsed = typeof props.message.arguments === 'string'
        ? JSON.parse(props.message.arguments)
        : props.message.arguments
      argsText = JSON.stringify(parsed, null, 2)
    } catch {
      argsText = props.message.arguments
    }
  }

  if (!resultText && props.message.has_more && props.sessionId) {
    loadingMore.value = true
    try {
      const { getMessageContent } = await import('../../api/session')
      const key = 'tool_call:' + props.message.tool_call_id
      if (key.includes(':')) {
        const res = await getMessageContent(props.sessionId, [key])
        const full = res?.[key]
        if (full) {
          resultText = full
          localResult.value = full
        }
      }
    } catch (e) { console.error('[MessageItem] getMessageContent error:', e) }
    loadingMore.value = false
  }

  const text = argsText
    ? 'Arguments:\n' + argsText + '\n\nResult:\n' + (resultText || '')
    : resultText || ''
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    uiMessage.success('Copied')
  } catch {
    uiMessage.warning('Copy failed')
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

.message-item.user,
.message-item.tool {
  align-items: flex-end;
}

.message-item.assistant {
  align-items: flex-start;
}

.speaker-line {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 0;
  font-size: 12px;
}

.speaker-line.assistant {
  flex-direction: row;
}

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

.timestamp-line {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 4px;
  opacity: 0.7;
}

.speaker-time {
  font-size: 11px;
  color: var(--text-muted);
}

.message-row {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.message-row .bubble-footer {
  display: flex;
  align-items: center;
  padding-top: 0;
  opacity: 0.4;
  transition: opacity 0.15s;
}

.assistant .message-row .bubble-footer {
  align-self: flex-start;
}

.user .message-row .bubble-footer {
  align-self: flex-end;
}

.message-item:hover .message-row .bubble-footer {
  opacity: 1;
}

.message-bubble {
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.5;
}

.assistant.text .message-bubble {
  background: var(--bg-secondary);
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
  color: black;
  word-break: break-all;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.message-content :deep(p) {
  word-break: break-word;
  overflow-wrap: anywhere;
}

.message-content :deep(ol),
.message-content :deep(ul) {
  padding-left: 1em;
}

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

.header-spacer {
  flex: 1;
}

.copy-icon {
  cursor: pointer;
  color: var(--text-muted);
  transition: color 0.15s;
}
.copy-icon:hover {
  color: var(--accent);
}

.section-body {
  padding: 6px 8px 8px 20px;
  background: var(--bg-tertiary);
  border-radius: 0 0 4px 4px;
}

.brief-line {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  line-height: 1.4;
}
.brief-text {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
}

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