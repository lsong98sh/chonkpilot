<template>
  <!-- F3/F4/F6/F7 · 子聊天状态条 + 子聊天窗口「结束」（单聊 §3.4.5 / §3.7 / §3.8）：
       - 位置 = 对话窗口**输入框上方**，常驻（父会话窗口：列该会话的未结束子聊天）；
       - 默认**折叠为一条**占位条「N 个子聊天进行中」（淡色，走主题 token），点击展开 →
         **最多显示 15 条 + 超出可滚动**；每条 = 「正在 <purpose>」+「继续」+「强制结束」；
       - 子聊天窗口内：「结束」常驻，`lifecycle=ready` 时**点亮**（`subchat-state` 驱动）。 -->
  <div v-if="showSelfFinish || unfinished.length > 0" class="subchat-bar">
    <!-- ① 子聊天窗口内：常驻「结束」（ready 点亮）；finished → 整体不渲染（F9 只读） -->
    <div v-if="showSelfFinish" class="subchat-self">
      <span class="subchat-self-hint">{{ selfReady ? $t('chat.subchat_ready_hint') : '' }}</span>
      <button
        type="button"
        class="subchat-finish-btn"
        :class="{ lit: selfReady }"
        :disabled="finishing === sessionId"
        @click="onFinishSelf"
      >{{ $t('chat.subchat_finish') }}</button>
    </div>

    <!-- ② 父会话窗口内：「进行中子聊天」状态条（默认折叠为一条） -->
    <div v-if="unfinished.length > 0" class="subchat-ongoing">
      <button type="button" class="subchat-summary" @click="expanded = !expanded">
        <Icon :name="expanded ? 'arrow-down' : 'arrow-right'" :size="12" />
        <span class="subchat-summary-text">{{ $t('chat.subchat_ongoing', { n: unfinished.length }) }}</span>
      </button>
      <div v-if="expanded" class="subchat-list" :style="{ maxHeight: listMaxHeight }">
        <div
          v-for="it in unfinished"
          :key="it[FieldKeys.session_id]"
          class="subchat-row"
        >
          <!-- 单聊 / 群聊**图标区分**（群聊 §4 D41）；群聊额外带 **agent(s)** 信息 -->
          <Icon :name="isGroupChatKind(it[FieldKeys.kind]) ? 'group' : 'chat-dot-round'" :size="12" class="subchat-row-icon" />
          <span class="subchat-row-name" :class="{ ready: it[FieldKeys.lifecycle] === SUBCHAT_READY }">
            {{ subChatStatusText(it, $t) }}
          </span>
          <span v-if="groupAgentsOf(it)" class="subchat-row-agents" :title="groupAgentsOf(it)">{{ groupAgentsOf(it) }}</span>
          <button type="button" class="subchat-row-btn" @click="onContinue(it)">
            {{ $t('chat.subchat_continue') }}
          </button>
          <button
            type="button"
            class="subchat-row-btn subchat-row-btn-danger"
            :disabled="finishing === it[FieldKeys.session_id]"
            @click="onForceFinish(it)"
          >{{ $t('chat.subchat_force_finish') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics, FieldKeys } from '../../events/msgkeys.js'
import { message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import {
  SUBCHAT_MAX_VISIBLE,
  SUBCHAT_READY,
  isSubChatKind,
  isSubChatUnfinished,
  subChatStatusText,
} from '../../utils/subchat.js'
import { agentDisplayName, isGroupChatKind } from '../../utils/groupchat.js'
import { finishSubChat, listSubChats, openSubChatWindow } from '../../composables/useSubChats.js'
import { loadGroupParticipants } from '../../composables/useGroupChat.js'

const props = defineProps({
  /** 本窗口绑定 / 当前会话（父会话过滤依据；§3.4.5「按发起它的那个会话显示」）。 */
  sessionId: { type: String, default: '' },
  /** 本会话 `kind`（`sub_chat`/`group_chat` → 渲染常驻「结束」；F1/F3②）。 */
  kind: { type: String, default: '' },
  /** 本会话 `lifecycle`（`ready` → 点亮「结束」；`finished` → 不渲染，F9）。 */
  lifecycle: { type: String, default: '' },
})

/** 子聊天窗口内点「结束」→ 由父（ChatPanel）发 `subchat-finish` 并自关窗口（F5，执行对象恒为来源窗口）。 */
const emit = defineEmits(['finish', 'child-closed'])

const { t } = useI18n()

// 子聊天窗口内「结束」常驻：kind 属子聊天体系且**非终态**（finished 只读 → 无按钮）。
const isSelf = computed(() => isSubChatKind(props.kind))
const showSelfFinish = computed(() => isSelf.value && props.lifecycle !== 'finished')
const selfReady = computed(() => props.lifecycle === SUBCHAT_READY)

/** 该父会话的子聊天（含 finished；§3.4.6 会话列表与状态条共用同一取数，此处自行过滤）。 */
const items = ref([])
const expanded = ref(false)
const finishing = ref('')

// 群聊条目带 **agent(s)** 信息（群聊 §4 D41）：名册经 `data-session-group-participants-load`
// 取（useGroupChat 内进程级缓存 → 状态条多次刷新零额外往返）。单聊条目不带。
const groupAgents = ref({}) // session_id → "A / B"

function groupAgentsOf(it) {
  return groupAgents.value[(it && it[FieldKeys.session_id]) || ''] || ''
}

async function loadGroupAgentHints(list) {
  const groups = (list || []).filter((it) => isGroupChatKind(it[FieldKeys.kind]))
  if (groups.length === 0) return
  const out = { ...groupAgents.value }
  await Promise.all(groups.map(async (it) => {
    const sid = it[FieldKeys.session_id]
    try {
      const parts = await loadGroupParticipants(sid)
      out[sid] = parts.map((p) => agentDisplayName(p.agent)).join(' / ')
    } catch (_) {
      out[sid] = ''
    }
  }))
  groupAgents.value = out
}

/** 状态条范围（§3.4.5）：`parent_id == 本会话` 且 `lifecycle ∈ {chatting, ready}`（天然排除自身）。 */
const unfinished = computed(() => items.value.filter(isSubChatUnfinished))

/** 展开列表可视高度上限 = 15 条（§10 Q5 / D40：最多显示 15 条 + 超出可滚动）。 */
const listMaxHeight = computed(() => (SUBCHAT_MAX_VISIBLE * 22) + 'px')

let unsubs = []

async function refresh() {
  const pid = props.sessionId
  if (!pid) { items.value = []; return }
  try {
    items.value = await listSubChats(pid)
    await loadGroupAgentHints(items.value)
  } catch (e) {
    console.warn('[SubChatBar] subchat-list failed:', e)
  }
}

/** 「继续」= `gui.window.open-chat`（未开建窗 / 已开前置，幂等）；撞窗口上限 → 明确提示（E5；
    群聊为**独立额度** → 群聊专属文案，群聊 §9 D28/D30）。 */
async function onContinue(item) {
  const ok = await openSubChatWindow(item[FieldKeys.session_id])
  if (!ok) {
    message.warning(t(isGroupChatKind(item[FieldKeys.kind])
      ? 'chat.group_chat_window_limit'
      : 'chat.chat_window_limit'))
  }
}

/** 「强制结束」= 同一条 `subchat-finish`（与窗口内「结束」同消息，§3.5 D31）；**不打开窗口**、不做二次确认。 */
async function onForceFinish(item) {
  const sid = item[FieldKeys.session_id]
  if (!sid || finishing.value) return
  finishing.value = sid
  try {
    const ok = await finishSubChat(sid)
    if (!ok) { message.error(t('chat.subchat_finish_failed')); return }
    await refresh() // finished 不再列出（状态条移除）
  } finally {
    finishing.value = ''
  }
}

/** 子聊天窗口内「结束」：交给父组件收口（发消息 + 自关窗口 + 回切）。 */
function onFinishSelf() {
  if (!props.sessionId || finishing.value) return
  emit('finish', props.sessionId)
}

// F6/F7 刷新触发：`subchat-opened` / `subchat-state`（本会话为父）/ `gui.window.closed`（F4）/
// `data-session-title-changed`（本会话标题）。**零 watch**（项目前端硬约束）。
function subscribe() {
  unsubs.push(mq.on(MsgTopics.subchatOpened, (d) => {
    if (d && d[FieldKeys.parent_session_id] === props.sessionId) refresh()
  }))
  unsubs.push(mq.on(MsgTopics.subchatState, (d) => {
    if (d && d[FieldKeys.parent_session_id] === props.sessionId) refresh()
  }))
  unsubs.push(mq.on(EventNames.guiWindowClosed, async (d) => {
    // F4：关窗 = 用户暂离（`lifecycle` 不变、不上报服务端）→ 仅刷新状态条。
    await refresh()
    const sid = d && d[FieldKeys.session_id]
    if (!sid) return
    // F5：被关闭会话属本会话的子聊天（状态条列表内）→ 交父组件「回切原 session」。
    const hit = items.value.find((it) => it[FieldKeys.session_id] === sid)
    if (hit) emit('child-closed', hit)
  }))
  unsubs.push(mq.on(EventNames.sessionTitleChanged, (d) => {
    if (d && d[FieldKeys.session_id] === props.sessionId) refresh()
  }))
}

onMounted(() => {
  subscribe()
  refresh()
})

onUnmounted(() => {
  unsubs.forEach((fn) => fn())
  unsubs.length = 0
})
</script>

<style scoped>
/* 状态条：淡色（走主题 token，§10 Q5「淡粉/淡蓝」）以区别于常规 UI。 */
.subchat-bar {
  --subchat-bg: var(--accent-bg);
  --subchat-border: color-mix(in srgb, var(--accent) 30%, transparent);
  flex-shrink: 0;
  background: var(--subchat-bg);
  border-top: 1px solid var(--subchat-border);
}
.subchat-self {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
}
.subchat-self-hint {
  flex: 1;
  min-width: 0;
  font-size: 11px;
  color: var(--text-secondary);
}
.subchat-finish-btn {
  flex-shrink: 0;
  padding: 2px 12px;
  border: 1px solid var(--subchat-border);
  border-radius: 12px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 12px;
  cursor: pointer;
}
/* 点亮态（§3.7：子 agent 自报 → ready → 高亮；非终态，用户继续聊会回到 chatting） */
.subchat-finish-btn.lit {
  border-color: var(--accent);
  color: var(--accent);
  font-weight: 600;
}
.subchat-finish-btn.lit:hover {
  background: var(--accent);
  color: var(--bg-secondary);
}
.subchat-summary {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  padding: 3px 8px;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  font-size: 11px;
  text-align: left;
  cursor: pointer;
}
.subchat-summary:hover {
  color: var(--accent);
}
.subchat-summary-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 展开：最多显示 15 条（行高由 `listMaxHeight` 约束），超出可滚动（§10 Q5 / D40）。 */
.subchat-list {
  overflow-y: auto;
  padding: 0 8px 4px;
}
.subchat-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 22px;
}
.subchat-row-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  color: var(--text-secondary);
}
/* 单聊 / 群聊图标区分（群聊 §4 D41）+ 群聊 agent(s) 提示 */
.subchat-row-icon {
  flex-shrink: 0;
  color: var(--text-muted);
}
.subchat-row-agents {
  flex-shrink: 0;
  max-width: 30%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 10px;
  color: var(--text-muted);
}
.subchat-row-name.ready {
  color: var(--accent);
  font-weight: 600;
}
.subchat-row-btn {
  flex-shrink: 0;
  padding: 0 8px;
  border: 1px solid var(--subchat-border);
  border-radius: 10px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 18px;
  cursor: pointer;
}
.subchat-row-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.subchat-row-btn-danger:hover {
  border-color: var(--danger);
  color: var(--danger);
}
.subchat-row-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
