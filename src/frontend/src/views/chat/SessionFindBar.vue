<!--
  会话内查找条（CHAT-028，2026-11-01）：会话视图**消息区上方**独立一行（`flex-shrink:0`，
  不占消息滚动区），由 ChatPanel 放在 `<MessageList>` 之前。
  纯对话窗口（chatOnly）绑定的固定参数会话**没有标题条** → 由本组件渲染一个「放大镜启动器」
  （`show-launcher`），保证任何窗口形态都能唤起查找。
  状态逻辑在 `composables/useSessionSearch.js`（禁止 watch）；本组件只做展示 + 事件上抛。
-->
<template>
  <div ref="rootRef" class="session-find">
    <!-- 关闭态启动器：仅标题条被隐藏（chatOnly + 固定参数会话）时出现 -->
    <div v-if="!open && showLauncher" class="session-find-launcher">
      <button
        type="button"
        class="session-find-icon-btn"
        :title="$t('chatSearch.placeholder')"
        @click="onLauncherClick"
      >
        <Icon name="search" :size="13" />
      </button>
    </div>
    <div v-else class="session-find-bar">
      <Icon name="search" :size="13" class="session-find-prefix" />
      <input
        ref="inputRef"
        class="session-find-input"
        type="text"
        spellcheck="false"
        :placeholder="$t('chatSearch.placeholder')"
        :value="query"
        @input="$emit('input', $event.target.value)"
        @keydown.enter.prevent="$emit('next')"
        @keydown.down.prevent="$emit('next')"
        @keydown.up.prevent="$emit('prev')"
        @keydown.esc.stop.prevent="$emit('close')"
      />
      <span v-if="loading" class="session-find-status">{{ $t('chatSearch.loading') }}</span>
      <span v-else-if="errorHint" class="session-find-status is-error">{{ $t(errorHint) }}</span>
      <span v-else-if="total > 0" class="session-find-status">{{ positionText }}</span>
      <span v-else-if="showNoResult" class="session-find-status">{{ $t('chatSearch.no_result') }}</span>
      <button
        type="button"
        class="session-find-icon-btn"
        :title="$t('chatSearch.prev')"
        :disabled="total === 0"
        @click="$emit('prev')"
      >↑</button>
      <button
        type="button"
        class="session-find-icon-btn"
        :title="$t('chatSearch.next')"
        :disabled="total === 0"
        @click="$emit('next')"
      >↓</button>
      <button
        type="button"
        class="session-find-icon-btn"
        :title="$t('chatSearch.close')"
        @click="$emit('close')"
      >✕</button>
    </div>
    <div v-if="open && olderHint" class="session-find-hint">{{ $t('chatSearch.older_hint') }}</div>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, ref } from 'vue'
import { Icon } from '@ckfw/components/icon/index.js'

const props = defineProps({
  /** 查找条是否展开（由 ChatPanel 持有，Esc/✕ → emit('close') 收回）。 */
  open: { type: Boolean, default: false },
  /** 关闭态是否渲染启动器（标题条被隐藏的窗口形态需要它作入口）。 */
  showLauncher: { type: Boolean, default: false },
  query: { type: String, default: '' },
  total: { type: Number, default: 0 },
  index: { type: Number, default: -1 },
  loading: { type: Boolean, default: false },
  searched: { type: Boolean, default: false },
  errorHint: { type: String, default: '' },
  olderHint: { type: Boolean, default: false },
})

const emit = defineEmits(['input', 'prev', 'next', 'close', 'open'])

const rootRef = ref(null)

const hasQuery = computed(() => !!(props.query || '').trim())
// 「查完无命中」：收到应答之后才提示，避免防抖等待期闪现「无匹配」
const showNoResult = computed(() => props.searched && !props.loading && !props.errorHint && hasQuery.value && props.total === 0)
const positionText = computed(() => `${props.index >= 0 ? props.index + 1 : 0}/${props.total}`)

function focusInput() {
  nextTick(() => {
    const el = rootRef.value && rootRef.value.querySelector('.session-find-input')
    if (el) {
      el.focus()
      el.select?.()
    }
  })
}

function onLauncherClick() {
  emit('open')
  focusInput()
}

// 由 ChatPanel 的标题条按钮打开 → 本组件新挂载（v-if）→ 挂载即聚焦
onMounted(() => {
  if (props.open) focusInput()
})
</script>

<style scoped>
.session-find {
  flex-shrink: 0;
}

/* ── 关闭态启动器（标题条被隐藏的窗口形态）── */
.session-find-launcher {
  display: flex;
  align-items: center;
  padding: 2px 8px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
}

/* ── 查找条（独立一行，不占消息滚动区）── */
.session-find-bar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-surface);
  font-size: 12px;
  color: var(--text-secondary);
}

.session-find-prefix {
  flex-shrink: 0;
  color: var(--text-muted);
}

.session-find-input {
  flex: 1;
  min-width: 0;
  padding: 3px 8px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  background: var(--bg-primary, #fff);
  color: var(--text-primary);
  font-size: 12px;
  line-height: 1.5;
  font-family: inherit;
  outline: none;
}
.session-find-input:focus {
  border-color: var(--accent);
}
.session-find-input::placeholder {
  color: var(--text-muted);
}

.session-find-status {
  flex-shrink: 0;
  padding: 0 4px;
  font-size: 11px;
  color: var(--text-muted);
  white-space: nowrap;
}
.session-find-status.is-error {
  color: var(--warning, #e6a23c);
}

.session-find-icon-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 4px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}
.session-find-icon-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--accent);
}
.session-find-icon-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.session-find-hint {
  padding: 3px 12px;
  border-bottom: 1px solid var(--border);
  background: var(--warning-bg);
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 1.4;
}
</style>
