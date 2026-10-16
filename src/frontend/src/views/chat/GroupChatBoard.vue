<template>
  <!-- G1 · 群聊窗口 = **只读展示板**（群聊方案 §4 / §12 步骤 6；D11/D14/D16/D29/D31/D47/D48）：
       布局 = **左**（参与者头像区 + 结论消息流）｜**右**（选中 agent 的子会话内容，只读）；
       人**不能发言**（不渲染输入框）；记录可回看。
       群聊本体**不跑 LLM、不碰工具**（D11）—— 判定与执行都在各参与者的 `work_session` 里。 -->
  <div class="group-board">
    <!-- 头部：会话名 + 状态 + 「继续」（到顶/关窗续聊）+「关闭」（暂停/终止二选一，D37） -->
    <div class="gb-header">
      <Icon name="group" :size="14" />
      <span class="gb-title" :title="sessionId">{{ title || $t('chat.group_chat_title') }}</span>
      <!-- G5 · 收敛点亮（§7 / D10/D52）：`subchat-state{lifecycle=ready}`（主 agent 判「已收敛」）→
           点亮「可以结束了」；用户拍板结束 → 复用既有 `subchat-finish`（走「关闭窗口」→「终止」，D37）。 -->
      <span class="gb-state" :class="{ lit: lifecycle === SUBCHAT_READY }">{{
        lifecycle === SUBCHAT_READY ? $t('chat.subchat_ready_hint') : $t(subChatStateKey(lifecycle))
      }}</span>
      <span class="gb-readonly">{{ $t('chat.group_chat_readonly') }}</span>
      <div class="gb-spacer" />
      <button
        v-if="lifecycle !== SUBCHAT_FINISHED"
        type="button"
        class="gb-btn"
        :disabled="busy || continuing"
        :title="$t('chat.group_chat_continue_hint')"
        @click="onContinue"
      >{{ $t('chat.subchat_continue') }}</button>
      <button type="button" class="gb-btn gb-btn-close" @click="onCloseClick">
        {{ $t('chat.group_chat_close') }}
      </button>
    </div>

    <div class="gb-main">
      <!-- 左：参与者头像区（实时状态）+ 结论消息流 -->
      <div class="gb-left">
        <div class="gb-avatars">
          <button
            v-for="p in participants"
            :key="p.agent"
            type="button"
            class="gb-avatar"
            :class="[avatarClass(p.agent), { active: p.agent === selectedAgent }]"
            :title="p.agent + (p.role ? ' · ' + p.role : '')"
            @click="selectAgent(p.agent)"
          >
            <span class="gb-avatar-ring" />
            <span class="gb-avatar-name">{{ agentDisplayName(p.agent) }}</span>
            <span class="gb-avatar-mark">✓</span>
          </button>
        </div>
        <div class="gb-stream">
          <div v-if="board.length === 0" class="gb-empty">{{ $t('chat.group_chat_board_empty') }}</div>
          <div
            v-for="(m, i) in board"
            :key="m.message_id || i"
            class="gb-msg"
            :class="{
              left: isLeftAligned(m, mainAgent),
              right: !isLeftAligned(m, mainAgent) && !!m.sender_agent,
              notice: !m.sender_agent,
            }"
            @click="m.sender_agent && selectAgent(m.sender_agent)"
          >
            <div v-if="m.sender_agent" class="gb-msg-head">
              <span class="gb-msg-agent">{{ agentDisplayName(m.sender_agent) }}</span>
              <span v-if="mentionText(m)" class="gb-msg-mentions">{{ mentionText(m) }}</span>
            </div>
            <div class="gb-msg-body">{{ m.content }}</div>
          </div>
        </div>
      </div>

      <!-- 右：选中 agent 的**自己的聊天记录**（`work_session` 历史；运行中实时 / 已结束只读，D48） -->
      <div class="gb-right">
        <template v-if="selectedAgent">
          <div class="gb-right-head">
            <span class="gb-right-name">{{ agentDisplayName(selectedAgent) }}</span>
            <span class="gb-right-id">#{{ workSessionId.slice(0, 8) }}</span>
          </div>
          <MessageList
            :key="workSessionId"
            :session-id="workSessionId"
            :viewer="true"
            direction="bottom"
          />
        </template>
        <div v-else class="gb-right-empty">{{ $t('chat.group_chat_pick_agent') }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, h, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics, FieldKeys, SubchatParticipantStateKeys, SubchatStateKeys } from '../../events/msgkeys.js'
import { dialog } from '../../components/dialog'
import { message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import MessageList from './MessageList.vue'
import GroupChatCloseDialog from './GroupChatCloseDialog.vue'
import { getSession } from '../../api/session'
import { finishSubChat } from '../../composables/useSubChats'
import { loadGroupBoard, loadGroupParticipants, continueGroupChat } from '../../composables/useGroupChat'
import {
  SUBCHAT_FINISHED,
  SUBCHAT_READY,
  subChatStateKey,
} from '../../utils/subchat.js'
import {
  PARTICIPANT_THINKING,
  agentDisplayName,
  groupWorkSessionId,
  isLeftAligned,
} from '../../utils/groupchat.js'

const props = defineProps({
  /** 本窗口绑定的群聊会话（URL `session-id`；由 ChatOnlyView 传入，固定不变）。 */
  sessionId: { type: String, required: true },
})

const { t } = useI18n()

// ── 会话元信息（标题 / 主 agent / lifecycle）────────────────────────────
// 主 agent（`sessions.agent`，由 runGroupChatOpen 落）= 群聊里的「用户」角色（D46/D47）→ 发言靠左。
const title = ref('')
const mainAgent = ref('')
const lifecycle = ref('')

async function loadMeta() {
  try {
    const s = await getSession(props.sessionId)
    title.value = (s && s.title) || ''
    mainAgent.value = (s && s.agent) || ''
    lifecycle.value = (s && s.lifecycle) || ''
  } catch (e) {
    console.warn('[GroupChatBoard] load session meta failed:', e)
  }
}

// ── 参与者名单（按 seq 升序 = 依次叫号的固定顺序，D3）──────────────────
const participants = ref([])

async function loadParts() {
  try {
    participants.value = await loadGroupParticipants(props.sessionId)
  } catch (e) {
    console.warn('[GroupChatBoard] load participants failed:', e)
  }
}

// ── 结论消息流（展示板；`sender_agent` / `mentions` 随消息视图回带，61 §3.2）────
const board = ref([])

async function loadBoard() {
  try {
    board.value = await loadGroupBoard(props.sessionId)
  } catch (e) {
    console.warn('[GroupChatBoard] load board failed:', e)
  }
}

// ── 头像实时状态（`subchat-participant-state`，61 §4.3 / D16）────────────
//   thinking = 本轮被叫到、正在判断/干活（小图标转）；idle = 转停且无发言（无关）；
//   spoken = 本轮产出了结论。
const states = ref({})

/** 头像状态 class（无状态 → 中性）。 */
function avatarClass(agent) {
  const s = states.value[agent]
  if (s === PARTICIPANT_THINKING) return 'thinking'
  if (s === 'spoken') return 'spoken'
  if (s === 'idle') return 'idle'
  return ''
}

/** 有参与者处于 thinking → 调度器在跑（「继续」防误触，D50 续期只在停下的线程上做）。 */
const busy = computed(() => participants.value.some((p) => states.value[p.agent] === PARTICIPANT_THINKING))

// ── 右侧分栏：选中 agent 的 `work_session`（不另开窗口、不占窗口数，D29）────
// id 由**后端确定性公式**推导（见 utils/groupchat.js 头注）——不新增任何消息。
const selectedAgent = ref('')
const workSessionId = computed(() => (selectedAgent.value ? groupWorkSessionId(props.sessionId, selectedAgent.value) : ''))

function selectAgent(agent) {
  if (!agent) return
  selectedAgent.value = agent
}

/** 发言里的 `@` 名单展示（`mentions` 随消息回带；`@全体` 归一为「全体」）。 */
function mentionText(m) {
  const list = m && Array.isArray(m.mentions) ? m.mentions : []
  if (list.length === 0) return ''
  return list.map((a) => '@' + agentDisplayName(a)).join(' ')
}

// ── 结论上屏的**刷新触发** ─────────────────────────────────────────────
// `subchat-participant-state{state:spoken}` 由调度器在**追加展示板消息之前**发出
// （groupchat_sched.go: emitParticipantState → appendBoard）→ 收到即（稍后）重读展示板。
// 单条 turn 承载全部上屏消息，无专门下行事件 → 两段延时兜底（写入经总线落库，异步）。
let boardTimer = null
let boardTimer2 = null

function scheduleBoardReload() {
  clearTimeout(boardTimer)
  clearTimeout(boardTimer2)
  boardTimer = setTimeout(loadBoard, 500)
  boardTimer2 = setTimeout(loadBoard, 1500)
}

// ── 动作：继续（subchat-continue，61 §4.2）/ 关闭（暂停·终止二选一，§15.4 D37）──
const continuing = ref(false)

async function onContinue() {
  if (continuing.value || busy.value) return
  continuing.value = true
  try {
    const ok = await continueGroupChat(props.sessionId)
    if (!ok) { message.error(t('chat.group_chat_continue_failed')); return }
    await loadMeta()
    scheduleBoardReload()
  } finally {
    continuing.value = false
  }
}

/** 关闭本窗口（宿主 `gui.window.status{command:'close'}`；关闭后 `instance-exit` → 调度即停）。 */
function closeWindow() {
  mq.emit(EventNames.guiWindowStatus, { command: 'close' })
}

const closing = ref(false)

/** 关窗 → 弹「暂停 / 终止」二选一（D37）：暂停 = 仅关窗（lifecycle 不变）；终止 = `subchat-finish`。 */
function onCloseClick() {
  let handle = null
  const close = () => { try { handle && handle.close() } catch (_) { /* noop */ } }
  const choose = (kind) => {
    if (!kind) { close(); return }          // 取消
    if (closing.value) return
    closing.value = true
    if (kind === 'terminate') {
      finishSubChat(props.sessionId).then((ok) => {
        closing.value = false
        if (!ok) { message.error(t('chat.subchat_finish_failed')); return }
        close()
        closeWindow()
      })
      return
    }
    // 暂停：不发任何消息 → 关窗即停机（lifecycle 保持原值，可随时重开「继续」，D38/N25）
    closing.value = false
    close()
    closeWindow()
  }
  handle = dialog.show(h(GroupChatCloseDialog, { onChoose: choose }), {
    title: t('chat.group_chat_close_title'),
    width: 460,
    height: 220,
    closable: true,
  })
}

// ── 订阅（组件级；随窗口卸载清理）────────────────────────────────────
const unsubs = []

function subscribe() {
  unsubs.push(mq.on(MsgTopics.subchatParticipantState, (d) => {
    if (!d || d[SubchatParticipantStateKeys.session_id] !== props.sessionId) return
    const agent = d[SubchatParticipantStateKeys.agent]
    if (!agent) return
    states.value = { ...states.value, [agent]: d[SubchatParticipantStateKeys.state] || '' }
    if (d[SubchatParticipantStateKeys.state] === 'spoken') scheduleBoardReload()
  }))
  // 本群聊 lifecycle 变更（ready 点亮 / finished；到顶终审 → finished）→ 头部状态跟随。
  unsubs.push(mq.on(MsgTopics.subchatState, (d) => {
    if (!d || d[SubchatStateKeys.session_id] !== props.sessionId) return
    lifecycle.value = d[SubchatStateKeys.lifecycle] || ''
    scheduleBoardReload()
  }))
}

onMounted(() => {
  subscribe()
  loadMeta()
  loadParts()
  loadBoard()
})

onUnmounted(() => {
  unsubs.forEach((fn) => fn())
  unsubs.length = 0
  clearTimeout(boardTimer)
  clearTimeout(boardTimer2)
})
</script>

<style scoped>
.group-board {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.gb-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  min-height: 28px;
  font-size: 12px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
}
.gb-title {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 40%;
}
.gb-state,
.gb-readonly {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
/* G5 · 收敛点亮「可以结束了」（§7 / D10） */
.gb-state.lit {
  color: var(--accent);
  font-weight: 600;
}
.gb-spacer {
  flex: 1;
  min-width: 0;
}
.gb-btn {
  flex-shrink: 0;
  padding: 2px 12px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 12px;
  cursor: pointer;
}
.gb-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.gb-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.gb-main {
  flex: 1;
  min-height: 0;
  display: flex;
}
.gb-left {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.gb-avatars {
  flex-shrink: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 8px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-secondary);
}
.gb-avatar {
  position: relative;
  width: 40px;
  height: 40px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: var(--bg-hover);
  color: var(--text-primary);
  font-size: 10px;
  line-height: 1.1;
  cursor: pointer;
  overflow: hidden;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  word-break: break-all;
}
.gb-avatar.active {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.gb-avatar-ring {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  border: 2px solid transparent;
  pointer-events: none;
}
.gb-avatar.thinking .gb-avatar-ring {
  border-color: color-mix(in srgb, var(--accent) 30%, transparent);
  border-top-color: var(--accent);
  animation: gb-spin 0.8s linear infinite;
}
.gb-avatar.spoken .gb-avatar-ring {
  border-color: var(--accent);
}
.gb-avatar.idle .gb-avatar-ring {
  border-color: var(--border);
}
.gb-avatar-mark {
  position: absolute;
  right: -2px;
  bottom: -2px;
  display: none;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--accent);
  color: var(--bg-secondary);
  font-size: 9px;
  line-height: 14px;
  text-align: center;
}
.gb-avatar.spoken .gb-avatar-mark {
  display: block;
}
@keyframes gb-spin {
  to { transform: rotate(360deg); }
}
.gb-stream {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.gb-empty {
  color: var(--text-muted);
  font-size: 13px;
  text-align: center;
  margin-top: 12px;
}
.gb-msg {
  max-width: 76%;
  padding: 6px 10px;
  border-radius: var(--border-radius);
  background: var(--bg-surface);
  border: 1px solid var(--border);
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
/* 主 agent ≡ 「用户」（D47）→ 靠左（同真人用户消息位置）；其余参与者靠右 */
.gb-msg.left {
  align-self: flex-start;
}
.gb-msg.right {
  align-self: flex-end;
}
.gb-msg.notice {
  align-self: center;
  max-width: 90%;
  background: transparent;
  border-style: dashed;
  color: var(--text-muted);
  font-size: 12px;
}
.gb-msg-head {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 2px;
  font-size: 11px;
  color: var(--text-muted);
}
.gb-msg-agent {
  font-weight: 600;
}
.gb-msg-mentions {
  color: var(--accent);
}
.gb-right {
  flex-shrink: 0;
  width: 42%;
  min-width: 240px;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--border);
}
.gb-right-head {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  min-height: 26px;
  font-size: 12px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
}
.gb-right-id {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-muted);
}
.gb-right-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 12px;
  font-size: 12px;
  color: var(--text-muted);
  text-align: center;
}
</style>
