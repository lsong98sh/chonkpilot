<template>
  <div class="ask-center">
    <header class="ask-center-head">
      <span class="ask-center-title">{{ $t('chat.ask_center_title') }}</span>
      <span class="ask-center-count">{{ $t('chat.ask_center_count', { count: items.length }) }}</span>
    </header>

    <div class="ask-center-body">
      <!-- 左列：待办一览（workdir 末级目录名 + 会话摘要 + 时间/状态） -->
      <aside class="ask-list">
        <button
          v-for="item in items"
          :key="item.ask_id"
          class="ask-list-item"
          :class="{ active: item.ask_id === selectedId, expired: isExpired(item) }"
          :data-ask-id="item.ask_id"
          @click="selectAsk(item.ask_id)"
        >
          <span class="ask-list-top">
            <span class="ask-list-dir">{{ dirName(item.work_dir) }}</span>
            <span v-if="isExpired(item)" class="ask-list-badge">{{ $t('chat.ask_center_expired') }}</span>
            <span class="ask-list-time">{{ timeLabel(item) }}</span>
          </span>
          <span class="ask-list-summary">{{ summaryOf(item) }}</span>
          <span class="ask-list-question">{{ item.question }}</span>
        </button>
        <div v-if="items.length === 0" class="ask-list-empty">{{ $t('chat.ask_center_empty') }}</div>
      </aside>

      <!-- 右侧：选中项详情（复用 ask 卡片：选项/推荐/自定义/跳过/取消/聊一聊/发送）；
           「聊一聊」= 内嵌聊天面板覆盖 ask 区（加载源会话既有内容，可讨论；完成后解除）。 -->
      <main class="ask-detail">
        <template v-if="current">
          <AskChatPanel
            v-if="chatMode"
            :key="'chat-' + current.ask_id"
            :ask="current"
            :resolving="chatResolving"
            @back="closeChat"
            @done="onChatDone"
          />
          <template v-else>
            <div class="ask-detail-meta">
              <span>{{ $t('chat.ask_center_session') }}: {{ summaryOf(current) }}</span>
              <span v-if="current.work_dir">{{ $t('chat.ask_center_workdir') }}: {{ dirName(current.work_dir) }}</span>
              <span>{{ $t('chat.ask_center_received') }}: {{ timeLabel(current) }}</span>
            </div>
            <AskUserContent
              :key="current.ask_id"
              :uid="current.ask_id"
              :question="current.question"
              :options="current.options"
              :custom="current.custom"
              :multi="current.multi"
              :recommended="current.recommended"
              :context="current.context"
              :sub-session-id="current.sub_session_id"
              :session-id="current.session"
              :deadline="deadlineMs(current.expires_at)"
              :no-timeout="deadlineMs(current.expires_at) === 0"
              :can-chat="true"
              @answer="onAnswer"
              @skip="onSkip"
              @cancel="onCancel"
              @chat="onChat"
            />
          </template>
        </template>
        <div v-else class="ask-detail-empty">{{ $t('chat.ask_center_select') }}</div>
      </main>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { message } from '@ckfw/components/ui/index.js'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../../events/event-names'
import { getSession, seedDialog } from '../../api/session'
import { sendAskReply } from '../../utils/askUserManager'
import AskUserContent from '../chat/AskUserContent.vue'
import AskChatPanel from './AskChatPanel.vue'

const { t } = useI18n()

const items = ref([])
const selectedId = ref('')
const titles = ref({}) // session_id → 标题（懒加载；失败回落会话 id 前缀）
const chatMode = ref(false) // 右侧是否处于内嵌聊天面板（「聊一聊」）
const chatResolving = ref(false)
const unsubs = []

const current = computed(() => items.value.find((it) => it.ask_id === selectedId.value) || null)

/** 时间值归一：server `expires_at` = RFC3339 字符串；测试夹具 = unix ms 数字。两者都接受。 */
function deadlineMs(v) {
  if (!v) return 0
  if (typeof v === 'number') return Number.isFinite(v) && v > 0 ? v : 0
  const n = Number(v)
  if (Number.isFinite(n) && n > 0) return n
  const parsed = Date.parse(v)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

function isExpired(item) {
  const dl = deadlineMs(item.expires_at)
  return dl > 0 && Date.now() > dl
}

function dirName(p) {
  return String(p || '').replace(/[\\/]+$/, '').split(/[\\/]/).filter(Boolean).pop() || '—'
}

function timeLabel(item) {
  const ms = Number(item.received_at) || 0
  if (!ms) return '—'
  const d = new Date(ms)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

function summaryOf(item) {
  const sid = item.session || ''
  const title = item.session_title || (sid && titles.value[sid]) || ''
  if (title) return title
  return sid ? `#${sid.slice(0, 8)}` : t('chat.ask_center_no_session')
}

/** 归一 ask 载荷（兼容 gui.ask.list 的扁平项与 ask-center-add 的 {ask, work_dir, received_at}）。 */
function normalize(raw) {
  const a = raw && typeof raw.ask === 'object' && raw.ask !== null ? raw.ask : (raw || {})
  const options = Array.isArray(a.options) ? a.options : []
  return {
    ask_id: a.ask_id || '',
    question: a.question || '',
    options,
    custom: options.length === 0 ? true : !!a.custom,
    multi: !!a.multi,
    recommended: Array.isArray(a.recommended) ? a.recommended : [],
    session: a.session || a.session_id || '',
    sub_session_id: a.sub_session_id || '',
    turn: a.turn || a.turn_id || '',
    dialog_id: a.dialog_id || '',
    instance_id: a.instance_id || '',
    owner_instance: a.owner_instance || '',
    session_title: a.session_title || '',
    context: a.context || '',
    expires_at: a.expires_at || 0,
    work_dir: (raw && raw.work_dir) || a.work_dir || '',
    received_at: Number((raw && raw.received_at) || a.received_at) || 0,
    seq: Number((raw && raw.seq) || a.seq) || 0,
  }
}

async function loadTitles() {
  const seen = new Set()
  for (const it of items.value) {
    const sid = it.session
    if (it.session_title) {
      if (sid && titles.value[sid] !== it.session_title) {
        titles.value = { ...titles.value, [sid]: it.session_title }
      }
      continue
    }
    if (!sid || seen.has(sid) || titles.value[sid]) continue
    seen.add(sid)
    try {
      if (it.dialog_id) seedDialog(sid, it.dialog_id)
      const data = await getSession(sid)
      const title = String((data && data.title) || '').trim()
      if (title && title !== sid) titles.value = { ...titles.value, [sid]: title }
    } catch (e) {
      // 会话已删除 / 尚未落库：保持会话 id 前缀（不阻断待办展示）。
    }
  }
}

function upsert(item) {
  if (!item.ask_id) return
  const idx = items.value.findIndex((it) => it.ask_id === item.ask_id)
  if (idx >= 0) {
    items.value.splice(idx, 1, item)
  } else {
    items.value.push(item)
  }
  if (!selectedId.value) selectedId.value = items.value[0].ask_id
  loadTitles()
}

function removeItem(askId) {
  items.value = items.value.filter((it) => it.ask_id !== askId)
  if (selectedId.value === askId) {
    selectedId.value = items.value[0]?.ask_id || ''
    chatMode.value = false
    chatResolving.value = false
  }
}

/** 从宿主进程级缓存 + 后端权威快照拉回（挂载 / 重载 / 窗口重开；按 ask_id 去重合并）。 */
async function load() {
  const tasks = [
    (async () => {
      try {
        const env = await mq.emit(EventNames.askCenterSubscribe, {}, { timeout: 5000 })
        const result = env && env.backend && env.backend.result
        const list = result && Array.isArray(result.asks) ? result.asks : []
        for (const raw of list) upsert(normalize(raw))
      } catch (e) {
        console.warn('[AskCenter] ask-center-subscribe failed:', e)
      }
    })(),
    (async () => {
      try {
        const env = await mq.emit(EventNames.guiAskList, {}, { timeout: 5000 })
        const result = env && env.backend && env.backend.result
        const list = result && Array.isArray(result.asks) ? result.asks : []
        for (const raw of list) upsert(normalize(raw))
      } catch (e) {
        console.warn('[AskCenter] gui.ask.list failed:', e)
      }
    })(),
  ]
  await Promise.allSettled(tasks)
}

/** 作答/取消后同时通知宿主摘除（幂等）并从本地列表移除。 */
function finishItem(item, answer) {
  sendAskReply(item, answer)
  mq.emit(EventNames.guiAskRemove, { ask_id: item.ask_id })
  removeItem(item.ask_id)
}

function onAnswer(answer) {
  if (!current.value) return
  finishItem(current.value, answer)
}

function onCancel() {
  if (!current.value) return
  finishItem(current.value, t('chat.ask_user_cancel_answer'))
}

/** 跳过 = 暂挂队尾（不回答、不移除；与弹窗「跳过」语义一致）。 */
function onSkip() {
  const it = current.value
  if (!it) return
  const idx = items.value.findIndex((x) => x.ask_id === it.ask_id)
  if (idx >= 0) items.value.push(items.value.splice(idx, 1)[0])
  selectedId.value = items.value[0]?.ask_id || ''
}

/** 选中待办：切走 = 关闭聊天面板（**不解除**，无后端动作）。 */
function selectAsk(askId) {
  if (askId === selectedId.value && !chatMode.value) return
  chatMode.value = false
  chatResolving.value = false
  selectedId.value = askId
}

/** 关闭聊天面板（返回回复界面）：ask 保持未解除，可再次聊一聊或直接回答。 */
function closeChat() {
  chatMode.value = false
  chatResolving.value = false
}

/**
 * 聊一聊：进入内嵌聊天面板（加载源会话既有内容，可讨论）。
 * 不发送 defer、不解除 ask；只有「完成」或直接回答才解除。
 */
async function onChat() {
  const it = current.value
  if (!it) return
  const sid = it.session || ''
  if (!sid) {
    message.error(t('chat.ask_user_chat_no_session'))
    return
  }
  let exists = !!it.session_title
  if (!exists) {
    try {
      if (it.dialog_id) seedDialog(sid, it.dialog_id)
      exists = !!(await getSession(sid))
    } catch (e) {
      exists = false
    }
  }
  if (!exists) {
    message.error(t('chat.ask_user_chat_session_missing', { id: sid.slice(0, 8) }))
    return
  }
  chatMode.value = true
}

/**
 * 「完成」：解除提问（AskUser S4）——后端只更新 tool-result + 任务节点 done + 摘除待办，
 * **不向 LLM 发消息**；前端同时摘除宿主缓存与本地项。
 */
async function onChatDone() {
  const it = current.value
  if (!it || chatResolving.value) return
  chatResolving.value = true
  try {
    const env = await mq.emit(EventNames.askResolve, { ask_id: it.ask_id }, { timeout: 8000 })
    const result = env && env.backend && env.backend.result
    if (result && result.ok === false) {
      message.error(t('chat.ask_resolve_failed'))
      return
    }
    mq.emit(EventNames.guiAskRemove, { ask_id: it.ask_id })
    removeItem(it.ask_id)
    chatMode.value = false
  } catch (e) {
    console.warn('[AskCenter] ask-resolve failed:', e)
    message.error(t('chat.ask_resolve_failed'))
  } finally {
    chatResolving.value = false
  }
}

onMounted(() => {
  // 先订阅（新增/摘除），再拉快照：两者之间到达的 ask 由快照 upsert 去重合并。
  unsubs.push(mq.on(EventNames.askCenterAdd, (ev) => {
    if (ev && ev.ask) upsert(normalize(ev))
  }))
  unsubs.push(mq.on(EventNames.askCenterRemove, (ev) => {
    if (ev && ev.ask_id) removeItem(ev.ask_id)
  }))
  load()
})

onUnmounted(() => {
  // 退订（窗口卸载/关闭）：后端唯一分发不再向已关闭窗口推送；幂等。
  mq.emit(EventNames.askCenterUnsubscribe, {}).catch(() => {})
  unsubs.forEach((fn) => fn())
  unsubs.length = 0
})
</script>

<style scoped>
.ask-center {
  height: 100vh;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.ask-center-head {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-secondary, var(--bg-primary));
}

.ask-center-title {
  font-size: 15px;
  font-weight: 600;
}

.ask-center-count {
  font-size: 12px;
  color: var(--text-muted);
}

.ask-center-body {
  flex: 1;
  min-height: 0;
  display: flex;
}

.ask-list {
  width: 320px;
  flex-shrink: 0;
  overflow-y: auto;
  border-right: 1px solid var(--border);
  padding: 8px;
  box-sizing: border-box;
}

.ask-list-item {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 4px;
  text-align: left;
  padding: 10px 12px;
  margin-bottom: 6px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
}

.ask-list-item:hover {
  border-color: var(--accent);
}

.ask-list-item.active {
  border-color: var(--accent);
  background: var(--accent-bg, #ecf5ff);
}

.ask-list-item.expired {
  opacity: 0.75;
}

.ask-list-top {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
}

.ask-list-dir {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ask-list-time {
  margin-left: auto;
  font-size: 11px;
  color: var(--text-muted);
  flex-shrink: 0;
}

.ask-list-badge {
  font-size: 10px;
  padding: 0 5px;
  border-radius: 3px;
  background: var(--warning-bg);
  color: var(--warning);
  border: 1px solid var(--warning-border);
  flex-shrink: 0;
}

.ask-list-summary {
  font-size: 12px;
  color: var(--text-secondary, #606266);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ask-list-question {
  font-size: 12px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ask-list-empty,
.ask-detail-empty {
  padding: 24px 12px;
  font-size: 13px;
  color: var(--text-muted);
  text-align: center;
}

.ask-detail {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 16px 20px;
  box-sizing: border-box;
}

.ask-detail-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  padding-bottom: 12px;
  margin-bottom: 12px;
  border-bottom: 1px solid var(--border);
  font-size: 12px;
  color: var(--text-muted);
}
</style>