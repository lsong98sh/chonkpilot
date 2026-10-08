<template>
  <template v-if="!sessionId">
    <div class="empty-prompt">{{ $t('chat.select_session') }}</div>
  </template>
  <template v-else>
    <div class="message-body" :class="'dir-' + direction">
      <!-- turn 导航（CHAT-013，2026-09-24；方向可配见 [42 §2 (154)]）：**与内容分开的专用列/行**、
           **不随内容滚动**、总数最多 10 个（>10 → 每圈 2 轮，覆盖不存在轮次的圈置灰）；
           点击滚动到该轮开始，悬停显示该圈覆盖轮次的用户消息。方向由组件参数 `direction`
           （top/bottom/left/right，**代码可配、无用户配置项**）决定：默认 right（主 chat / 纯对话
           窗口），子会话 = bottom；切换方向时 flex 方向 / 尺寸轴 / 弹层方向 / 边框方向同步切换（CSS）。
           原「向上/向下箭头」浮窗按钮已按用户口径移除。 -->
      <div v-if="turnStarts.length >= MIN_NAV_TURNS" class="turn-nav" @mouseleave="navHover = null">
        <div
          v-for="(c, i) in navCircles"
          :key="i"
          class="turn-dot"
          :class="{ active: currentCircle === i, disabled: c.disabled }"
          @click="c.disabled || scrollToTurn(c.turnIdx)"
          @mouseenter="c.disabled || showNavHover($event, c)"
        />
        <div v-if="navHover" class="turn-nav-pop" :style="navPopStyle">
          <div
            v-for="(t, i) in navHover.circle.turns"
            :key="i"
            class="turn-nav-item"
            :title="navHover.circle.msgs[i] ? (navHover.circle.msgs[i].content || '') : ''"
            @click="scrollToTurn(t)"
          >{{ navHover.circle.previews[i] }}</div>
        </div>
      </div>
      <div
        class="message-list"
        ref="listRef"
        :class="{ 'is-scroll-paused': !autoScroll }"
        @scroll="onScroll"
        @wheel="onWheel"
      >
        <!-- Top indicator: shown when near top -->
        <div v-if="isAtTop && messages.length > 0" class="top-indicator">
          <template v-if="loadingMore">
            <span class="load-more-spinner"></span> {{ $t('common.loading') }}
          </template>
          <template v-else-if="!hasMore">
            {{ $t('chat.reached_top') }}
          </template>
        </div>

        <div v-if="messages.length === 0 && !pending" class="empty-inside">{{ $t('chat.no_messages') }}</div>
        <MessageItem
          v-for="(msg, i) in messages"
          :key="msg.id"
          :message="msg"
          :session-id="sessionId"
          :show-header="i === 0 || messages[i-1].role !== msg.role"
          :is-active="turnActive"
          :is-last-message="i === messages.length - 1"
          :from-history="!!msg.fromHistory"
          @clear-arbitration="onClearArbitration"
        />
        <!-- 「加载中」占位（独立 pending 状态，不在 messages 内）→ 历史全量替换不影响 -->
        <MessageItem
          v-if="pending"
          :key="'pending'"
          :message="pendingMessage"
          :session-id="sessionId"
          :show-header="pendingShowHeader"
          :is-active="turnActive"
          :is-last-message="true"
          :from-history="false"
        />
        <!-- 底部操作区已移至 ChatPanel 输入区上方 -->
      </div>
    </div>
  </template>
</template>

<script setup>
import { ref, computed, onMounted, onUpdated, nextTick, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import MessageItem from './MessageItem.vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { createSessionMessages } from '../../utils/sessionMessages'
import { publishLLMStart, publishLLMContinue } from '../../api/chat'
import { sendQueue } from '../../composables/useSendQueue'
import { useTaskView } from '../../composables/useTaskView'

const props = defineProps({
  sessionId: { type: String, default: null },
  // 只读查看模式（子会话详情面板）：挂载即常驻订阅流事件，按 session_id 过滤。
  // 主聊天（ChatPanel）不传此标志，仅在发消息后订阅流事件。
  viewer: { type: Boolean, default: false },
  // turn 导航方向（CHAT-013-S06 / [42 §2 (154)]）：**代码可配**（组件参数），**无用户配置项**。
  // 由父组件显式传入：主 chat（ChatPanel）与纯对话窗口 = right（默认）；子会话（SessionChat）= bottom。
  direction: {
    type: String,
    default: 'right',
    validator: (v) => ['top', 'bottom', 'left', 'right'].includes(v),
  },
})

// 主会话 cancel（§〇.7）：全部级联，无弹框
// tasks/taskMap：切会话/刷新时查询任务快照（data-tasktree-tasks），补挂待裁决（awaiting）裁决条
const { confirmTurnCancel, refresh: refreshTasks, tasks: taskMap, clearAwaiting } = useTaskView()
const { t } = useI18n() // retryText 的"继续"文案（autoContinue 自动续写用）

// ── Internal message state ──
const {
  messages, turns, turnActive,
  hasMore, loadingMore, loadingMessages,
  pending, pendingStatus,
  handleToken, handleReceive, handleDone, handleError,
  loadMessages, loadMoreMessages, teardown: resetMessages,
  showPending, setPendingStatus, hidePending,
} = createSessionMessages()

// 「加载中」占位行（独立 pending 状态 → **不进 messages**：loadMessages 整体替换历史
// 不会把它冲掉，取消按钮/加载态在历史回填后仍正常）。复用 MessageItem 渲染，
// 保留 `.message-item.pending` / `.pending-bubble` 既有结构（DOM 契约不变）。
const pendingMessage = computed(() => ({
  id: 'pending',
  role: 'assistant',
  type: 'pending',
  pending: true,
  pendingStatus: pendingStatus.value,
  createdAt: new Date().toISOString(),
}))
// 占位行是否显示说话人（与 messages 内联渲染同口径：首条或上一条非 assistant）
const pendingShowHeader = computed(() => {
  const arr = messages.value
  return arr.length === 0 || arr[arr.length - 1].role !== 'assistant'
})

// ── Scroll state ──
const listRef = ref(null)
const autoScroll = ref(true)
const isAtTop = ref(true)
// 继续按钮：最后 turn 未完成（失败/取消/中断/重试耗尽）时显示，仅主 chat
const showContinue = ref(false)
// 空回复提示（S21）：llm-complete status=completed 但本 turn 无任何 assistant 输出
const emptyReplyHint = ref(false)
// 本 turn 是否已产生任何 assistant 输出（文本/推理/工具调用/工具结果）：
// completed 且无输出 = 空回复 → 实时提示"继续"（重发原用户消息）
let turnHadOutput = false
// 本 turn 是否为后端通知轮次（llm-started notify=true）：通知轮次空回复不提示"继续"（避免误发用户消息）
let notifyTurn = false
// 最后一条中断（interrupted，abort）的 tool_pair：底部"重试"按钮的目标（重跑该工具）
const lastInterruptedPair = computed(() => {
  for (let i = messages.value.length - 1; i >= 0; i--) {
    const m = messages.value[i]
    if (m.type === 'tool_pair' && m.status === 'interrupted') return m
  }
  return null
})

// ── turn 导航（圆圈，FP CHAT-013「turn 导航」）──
// 轮次开始 = 用户消息（排除通知）；最多 10 圈，>10 时每圈覆盖 2 轮。
// （「加载中」占位已移出 messages，不再是 messages 里的待排除项。）
const MAX_NAV_CIRCLES = 10
// turn 导航显示阈值（P1-7）：导航列/行为**固定占宽/占高**（竖向 22px 全高、横向 22px 全宽，
// 且带一条分隔边框）。轮次 < 3 时该固定占位换不来可感知收益（1–2 轮内容通常一屏内可见），
// 反而白占 22px + 一条分线 → 隐藏（不占宽、不画 border）。≥ 3 轮才值得导航。
const MIN_NAV_TURNS = 3
const currentCircle = ref(-1)
const navHover = ref(null) // { top, left, circle }
// 导航列/行方向（组件参数）：left/right = 竖向一列；top/bottom = 横向一行。
const isSideways = computed(() => props.direction === 'left' || props.direction === 'right')
// 弹层锚点：竖向列用 top（与该圈垂直对齐）；横向行用 left（与该圈水平对齐）。
// 弹出方向（向左/向右/向上/向下）由 CSS 按 direction 决定，见 <style> 的 .message-body.dir-* 段。
const navPopStyle = computed(() => {
  const h = navHover.value
  if (!h) return null
  return isSideways.value ? { top: h.top + 'px' } : { left: h.left + 'px' }
})

const turnStarts = computed(() => {
  const out = []
  for (let i = 0; i < messages.value.length; i++) {
    const m = messages.value[i]
    if (m.role === 'user' && m.type !== 'user-notify') {
      out.push({ index: i, msg: m })
    }
  }
  return out
})

function previewTurnText(m) {
  const s = (m.content || '').replace(/\s+/g, ' ').trim()
  return s.length > 20 ? s.slice(0, 20) + '...' : s
}

// 圈映射：turn ≤10 → 1 圈/turn；>10 → 固定 10 圈，每圈 2 轮（末圈可能 1 轮）；
// 覆盖的轮次全部不存在 → 置灰禁用（点击无响应、悬停不弹）。
const navCircles = computed(() => {
  const N = turnStarts.value.length
  const circles = []
  if (N === 0) return circles
  const push = (turnIdxs) => {
    const valid = turnIdxs.filter(t => t < N)
    if (valid.length === 0) {
      circles.push({ turnIdx: -1, turns: [], msgs: [], previews: [], disabled: true })
      return
    }
    circles.push({
      turnIdx: valid[0],
      turns: valid,
      msgs: valid.map(t => turnStarts.value[t].msg),
      previews: valid.map(t => previewTurnText(turnStarts.value[t].msg)),
      disabled: false,
    })
  }
  if (N <= MAX_NAV_CIRCLES) {
    for (let i = 0; i < N; i++) push([i])
  } else {
    for (let c = 0; c < MAX_NAV_CIRCLES; c++) push([c * 2, c * 2 + 1])
  }
  return circles
})

// 当前圈：滚动时根据 turn 位置（该轮首条消息元素顶 vs 滚动顶），当前圈高亮。
function updateCurrentCircle() {
  const el = listRef.value
  const starts = turnStarts.value
  if (!el || starts.length === 0) {
    currentCircle.value = -1
    return
  }
  const items = el.querySelectorAll('.message-item')
  let cur = -1
  for (let i = 0; i < starts.length; i++) {
    const node = items[starts[i].index]
    if (!node) continue
    if (node.offsetTop <= el.scrollTop + 4) cur = i
    else break
  }
  if (cur === -1) cur = 0
  const circles = navCircles.value
  let circle = -1
  for (let i = 0; i < circles.length; i++) {
    if (!circles[i].disabled && circles[i].turns.includes(cur)) {
      circle = i
      break
    }
  }
  currentCircle.value = circle
}

// 点击圈：滚动到该圈所代表 turn 的开始处。
function scrollToTurn(turnIdx) {
  const el = listRef.value
  const starts = turnStarts.value
  if (!el || !starts[turnIdx]) return
  const items = el.querySelectorAll('.message-item')
  const node = items[starts[turnIdx].index]
  if (node) el.scrollTop = Math.max(0, node.offsetTop - 8)
}

// 悬停圈：记录圈位置（offsetTop/offsetLeft 均相对 .turn-nav 定位锚点）+ 内容。
// 竖向列锚 top、横向行锚 left（见 navPopStyle），故两者都记录。
function showNavHover(e, circle) {
  navHover.value = { top: e.currentTarget.offsetTop, left: e.currentTarget.offsetLeft, circle }
}
const SCROLL_THRESHOLD = 20
const LOAD_MORE_THRESHOLD = 150  // px from top to trigger loadMore
let toppedOut = false  // true = already triggered loadMore at top, reset on scroll-down

// ── Turn / loading state ──
const isLoading = ref(false)
const currentTurnId = ref(null)
// 最近一轮 turn id：同轮次继续（continue）复用它，不新开轮次。
// 实时 turn 由 doSend 记录；重启/切换后回退已加载 turns 列表末项（见 continueTurnId）。
const lastTurnId = ref(null)
// 最近一次发送的参数（同轮次继续时沿用，保证场景 system_prompt / 模型选择不丢）。
let lastSendParams = { llm: '', think: 'on', effort: 'high', scenarioId: '' }
const turnUnsubs = []
// 常驻流订阅（onMounted 注册，组件销毁清理）：脚本注入 llm-start 不经 doSend，
// 流事件（llm-receive/llm-complete/llm-token）仍按 session+turn 过滤渲染。
const persistentUnsubs = []

// 当前 LLM（待发送队列按 LLM 分桶；ChatPanel initSession/selectLLM 时经 chat-select-llm 广播）
const currentLlm = ref('')
// 本 turn 是否以"取消/出错"结束（§3.5.3）：executorDone 后弹框询问待发消息发送/清除
const lastTurnCanceled = ref(false)
// 本 turn 是否用户主动取消（取消 ≠ 断链/中断：取消不自动续写，断链才自动续写）
let userCanceled = false
// 本 turn 的 LLM 错误是否可自动重试（分类来源：llm-complete.retryable 优先，S21；
// 缺省回退旧 llm-error.retryable）。false 如 API Key 401 → 只手动重试；每轮发送时复位 true
let lastTurnRetryable = true

// ── Track turnActive transitions for auto-scroll ──
const prevTurnActive = ref(false)

// ── Auto-scroll on content updates ──
onUpdated(() => {
  const ta = turnActive.value
  // When turn becomes active, re-enable auto-scroll
  if (ta && !prevTurnActive.value) {
    autoScroll.value = true
  }
  prevTurnActive.value = ta
  updateCurrentCircle()
  // 卡片渲染后补挂早到的待决裁决（mcp-tools-timeout 是 fire-and-forget，可能先于卡片到达）
  flushPendingArbitration()

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
  autoScroll.value = atBottom // 贴底 = 自动跟随；离底 = 暂停（`.message-list.is-scroll-paused`）
  updateCurrentCircle()

  // Load more: trigger when near top, but only once per scroll-up cycle.
  if (el.scrollTop <= LOAD_MORE_THRESHOLD) {
    tryLoadMore(el)
  } else {
    toppedOut = false // 离开顶部区域后复位，允许下次滚到顶再次加载
  }
}

// wheel 兜底：scrollTop 已到 0 时继续向上滚动浏览器不再派发 scroll 事件，
// 直接捕获 wheel 触发加载，否则滚顶一次后就再也加载不到更早的消息。
function onWheel(e) {
  if (e.deltaY >= 0) return
  const el = listRef.value
  if (!el || el.scrollTop > LOAD_MORE_THRESHOLD) return
  tryLoadMore(el)
}

function tryLoadMore(el) {
  if (!el) el = listRef.value
  if (!el) return
  // 初始加载进行中不触发 loadMore（否则 hasMore 临时为 true + beforeTurnId 为空，
  // 会重复抓取最新批次前置导致消息重复显示）
  if (loadingMessages.value) return
  if (!toppedOut && hasMore.value && !loadingMore.value) {
    toppedOut = true
    const prevHeight = el.scrollHeight
    loadMoreMessages().then(() => {
      nextTick(() => {
        nextTick(() => {
          if (listRef.value) {
            listRef.value.scrollTop = listRef.value.scrollHeight - prevHeight
          }
        })
      })
    })
  }
}

function scrollTop() {
  listRef.value?.scrollTo({ top: 0, behavior: 'smooth' })
}

function scrollBottom() {
  listRef.value?.scrollTo({ top: listRef.value.scrollHeight, behavior: 'smooth' })
}

// 滚动事件按会话隔离：主 LLM（ChatPanel）与子 LLM（SessionChat viewer）是
// 独立的 MessageList 实例，全局 msg-scroll-top/bottom 事件必须只作用于
// 发起点击的那个聊天；按钮通过 v-mq 携带本会话 session_id，这里做过滤，
// 否则点击一个聊天所有实例会同时滚动。
function isForThisSession(d) {
  const sid = activeSessionId.value || props.sessionId
  if (!sid || !d) return true
  return d.session_id === sid
}

// ── 继续按钮：turn 未完成判定 + 双开返回恢复订阅 ──

// 未写完的 finish_reason（LLM 未自然结束）：length 超长截断 / insufficient_system_resource
// 资源不足中断 / interrupted turn 级失败（断链、取消、错误，无终态 finish_reason）。
const UNFINISHED_REASONS = ['length', 'insufficient_system_resource', 'interrupted']

// 启发式判定"最后 turn 未完成"：
//  1) 权威信号：最后 turn 的 finish_reason ∈ UNFINISHED_REASONS
//     （由后端 turn 终结时落库；应用重启后仍可靠）
//  2) 兜底：末尾非通知消息不是"带内容的 assistant 回复"（失败/手动取消/中断）
function lastTurnIncomplete(msgs) {
  const turnsArr = turns.value
  const lastTurn = turnsArr.length ? turnsArr[turnsArr.length - 1] : null
  if (lastTurn && UNFINISHED_REASONS.includes(lastTurn.finish_reason)) {
    return true
  }
  let last = null
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i].type === 'user-notify') continue
    last = msgs[i]
    break
  }
  if (!last) return false
  return !(last.role === 'assistant' && last.content)
}

// 加载/切换会话后评估"继续"按钮。
// busy 状态本地维护（20-gui）：发 llm-start 置 busy（doSend 置 isLoading）、
// 收 llm-complete 置 idle（cleanupAndFinish）；GetLLMStatus 调用已移除，双开恢复
// 由 llm-complete 事件驱动（流事件按 session 过滤，后端会话后台继续跑）。
function assessContinueState() {
  const sid = activeSessionId.value || props.sessionId
  if (props.viewer || !sid || isLoading.value) {
    // isLoading 时（如队列续发刚启动）不评估，避免误显"继续"
    showContinue.value = false
    return
  }
  showContinue.value = lastTurnIncomplete(messages.value)
}

// 同轮次继续的目标 turn id：优先内存记录的最近一轮（实时 turn，doSend 写入），
// 回退已加载 turns 列表末项（重启恢复后由 DB 历史带回）。为空 = 由后端按 session 解析。
function continueTurnId() {
  if (lastTurnId.value) return lastTurnId.value
  const arr = turns.value
  return arr.length ? (arr[arr.length - 1].turn_id || '') : ''
}

// 点击"继续/重试"：同轮次继续（不新开 turn）。
function sendContinue() {
  if (isLoading.value || !props.sessionId) return
  showContinue.value = false
  emptyReplyHint.value = false
  sendSameTurnContinue()
}

// 同轮次继续出口（手动按钮 + 空回复提示 + 断链自动续写共用）：
// 复用最近一轮 turn id 发 llm-start{continue:true}，注入的 user 消息由 server 标
// Kind=continue（非新轮边界，同一轮次）；发送文本按 retryText()：已收到部分输出 → "继续"
// （续写），首字前失败 → 重发原用户消息。UI 行为（气泡/loading）与普通发送一致
// （同轮次继续无 turn-start 广播，气泡同样以「落库回执」为渲染时点，见 doSend）。
async function sendSameTurnContinue() {
  const text = retryText()
  turnHadOutput = false
  notifyTurn = false
  // 续写即新一轮动作（会话进行中）→ 收起「继续」/空回复提示（A7）
  showContinue.value = false
  emptyReplyHint.value = false
  // 可重试分类复位（S21）：每个 turn 起点重新按本轮 llm-complete/llm-error 判定
  lastTurnRetryable = true
  isLoading.value = true
  showPending('processing')
  startPendingProgress()
  const sid = props.sessionId
  const at = messages.value.length // user 气泡插入点（发送时刻的消息末尾）
  const { turn, ack } = publishLLMContinue(
    sid,
    text,
    continueTurnId(),
    lastSendParams.llm || currentLlm.value || '',
    lastSendParams.think || 'on',
    lastSendParams.effort || 'high',
    lastSendParams.scenarioId || '',
  )
  if (turn) currentTurnId.value = turn
  // 落库回执到达后再渲染 user 气泡（不再乐观插入）；等待期间已切会话 → 不渲染
  await ack
  if (activeSessionId.value && activeSessionId.value !== sid) return
  pushUserBubble(text, at)
}

// ── 截断/断链续写（llm-complete status=incomplete / error）──
// LLM 回复未写完时自动发"继续"，最多 MAX 次防死循环；超限或用户手动取消/出错后转手动按钮。
// 仅主 chat（viewer 只读不自动）。
const MAX_AUTO_CONTINUE = 3
const autoContinueCount = ref(0)
let autoContinueTimer = null

// 手动/自动重试的实际发送文本：
//   已收到部分内容（最后一条是 assistant 输出）→ "继续"（方式 B：续写，避免重发重复）
//   首字前失败（无 assistant 输出）→ 重发原用户消息（方式 A：重发上下文）
function retryText() {
  let last = null
  for (let i = messages.value.length - 1; i >= 0; i--) {
    const m = messages.value[i]
    if (m.type === 'user-notify') continue
    last = m
    break
  }
  if (last && last.role === 'assistant' && (last.content || last.type === 'tool_pair' || last.type === 'reasoning')) {
    return t('common.continue')
  }
  for (let i = messages.value.length - 1; i >= 0; i--) {
    const m = messages.value[i]
    if (m.role === 'user' && m.type !== 'user-notify') {
      return m.content || t('common.continue')
    }
  }
  return t('common.continue')
}

function scheduleAutoContinue() {
  clearTimeout(autoContinueTimer)
  autoContinueTimer = setTimeout(() => {
    if (isLoading.value || !props.sessionId) return
    // 断链/截断自动续写同样走同轮次继续（不新开 turn）
    sendSameTurnContinue()
  }, 400)
}

// ── Session change handling (event-driven, no watch on props) ──
// Tracks the last session id this component has processed to avoid duplicate
// reloads when ChatPanel (the prop source) updates before we receive the event.
const activeSessionId = ref(null)

async function onSessionChanged({ session_id }) {
  const newId = session_id || null
  if (newId === activeSessionId.value) return
  // 切换会话：取消挂起的自动续写并重置计数（自动续写仅针对当前会话连续截断）
  clearTimeout(autoContinueTimer)
  autoContinueCount.value = 0
  activeSessionId.value = newId
  // 空回复/输出标记随会话重置（S21）
  turnHadOutput = false
  notifyTurn = false
  emptyReplyHint.value = false
  lastTurnRetryable = true
  // 最近一轮 turn 记录随会话重置：继续时改用已加载 turns 列表末项（DB 历史）
  lastTurnId.value = null
  // 双开：切换会话不取消旧 turn（后端 LLM 域按 session 独立，后台继续跑）。
  // 仅解绑本地订阅并清理状态；旧会话 turn 事件不再显示，消息仍落库。
  // viewer（子会话详情）常驻订阅，切换仅换数据源。
  if (!props.viewer) {
    cleanupAndFinish()
  }
  resetMessages()
  toppedOut = false

  if (newId) {
    await loadMessages(newId)
    await assessContinueState()
    // 切会话恢复：消息落库后按任务列表 awaiting 补挂超时裁决条（I-57）
    await restoreAwaitingArbitration(newId)
  } else {
    showContinue.value = false
  }
}

function cleanupAndFinish() {
  turnUnsubs.forEach(fn => { try { fn() } catch (e) { console.error('[MessageList] turnUnsubs cleanup error:', e) } })
  turnUnsubs.length = 0
  isLoading.value = false
  currentTurnId.value = null
  clearPendingTimers()
  handleDone()
}

// ── 常驻流事件订阅（llm-receive 流增量 / llm-complete 终态 / 旧协议 llm-token）──
// 主 chat 与 viewer 均常驻（onMounted 注册，组件销毁清理），交互与脚本注入
// （直接 mq.emit llm-start，不经 doSend）都按 session+turn 过滤渲染。
// server 协议（21-llm-server）：llm-receive{type:text/reason/tool-call} 一个主题
// 承载全部流内容，llm-complete 为唯一终态（complete/incomplete/error/interrupted）。
function setupPersistentStreamSubs() {
  const isForThisTurn = (d) => {
    const sid = activeSessionId.value || props.sessionId
    if (sid && d.session && d.session !== sid) return false
    if (sid && d.session_id && d.session_id !== sid) return false
    if (d.turn && currentTurnId.value && d.turn !== currentTurnId.value) return false
    if (d.turn_id && currentTurnId.value && d.turn_id !== currentTurnId.value) return false
    return true
  }

  // llm-receive 流增量（text/reason/tool-call 拆分在 sessionMessages.handleReceive）。
  // 注：llm-receive 是桥映射后的**客户端 type**（桥 mqTypeMap：session-receive → llm-receive），
  // 非 61 契约主题，唯一事实源 = EventNames.llmReceive（与 Go 侧 messages.MsgLLMReceive 同值）。
  const onReceive = (d) => {
    if (!isForThisTurn(d)) return
    // 真实内容到达 → 标记本 turn 有输出（空回复判定 S21 用）
    if (d.type === 'text' || d.type === 'reason' || d.type === 'tool-call') {
      turnHadOutput = true
    }
    handleReceive(d)
  }
  persistentUnsubs.push(mq.on(EventNames.llmReceive, onReceive))

  // llm-complete：唯一终态（正常/错误/取消都收敛到这里）。
  const onComplete = (d) => {
    if (!isForThisTurn(d)) return
    handleDone()
  }
  persistentUnsubs.push(mq.on(EventNames.llmComplete, onComplete))

  // 旧协议 llm-token（C12 脚本注入流事件断言过滤用；按 session+turn 过滤后追加）
  const onLegacyToken = (d) => {
    if (!isForThisTurn(d)) return
    if (d.type === 'text' && d.content) handleToken({ type: 'text', content: d.content })
  }
  persistentUnsubs.push(mq.on(EventNames.llmToken, onLegacyToken))
}

// ── Handle user message sending (from ChatPanel via mq) ──
// 【发送】只在 LLM 空闲时可用（busy 时 UI 无发送按钮）→ 直接 doSend，不入队；
// busy 兜底（防御）→ 入队，等 turn 完全结束后由 drainQueue 发送。
function onMessageSend(data) {
  if (data.sessionId !== props.sessionId) return
  if (!data.text?.trim()) return
  if (isLoading.value) {
    sendQueue.enqueue({ type: 'message', llm: data.llm || currentLlm.value, payload: data })
    return
  }
  doSend({ ...data, type: 'message', llm: data.llm || currentLlm.value })
}

// ── Handle queue (from ChatPanel via mq) ──
// 【入队】仅前端操作：文本放入待发队列（不写 DB、不启动 turn、不显示气泡），
// LLM 结束后由 drainQueue 发送（docs/spec/00-overview/01-端到端数据流.md §5.3）。
function onMessageQueue(data) {
  if (data.sessionId !== props.sessionId) return
  if (!data.text?.trim()) return
  sendQueue.enqueue({ type: 'message', llm: data.llm || currentLlm.value, payload: data })
}

// ── 发送队列出口：busy（本 LLM turn 进行中）时跳过，等 executorDone 后由调用方再次触发 ──
// 队列按 LLM 分桶：取当前 LLM 的待发内容（队列只承载用户消息，附录 A）。
function drainQueue() {
  if (isLoading.value) return
  const batch = sendQueue.shiftBatch(currentLlm.value)
  if (!batch) return
  doSend(batch)
}

// 真正发送一批（message 单条）。user 气泡只在"真正发送"时显示（入队不显示，§3.5.2）；
// 且**不再乐观插入**——等「落库回执」（llm-start 经桥同步受理、server 落库 session/turn
// 后的 /publish 应答）到达才渲染（T5 方案 B，避免被 loadMessages 的历史全量替换冲掉）。
// server 协议（阶段三）：turn 前端自分配 uuid（publishLLMStart 生成），发布 llm-start
// 事件（桥拆 llm-start + llm-send{text-user}）；turn 已知即可立即订阅流事件
// （llm-receive/llm-complete 按 session+turn 过滤，规避旧事件竞态 s4）。
// notify 批次已归后端（server 空闲自动触发通知轮次），前端不再处理。
async function doSend(batch) {
  // 每轮重置输出/通知标记（S21 空回复判定按 turn 隔离，非会话级）
  turnHadOutput = false
  notifyTurn = false
  // 新一轮开始（会话进行中）→ 不显示「继续」/空回复提示（A7：正常回复后/进行中都不显示）
  showContinue.value = false
  emptyReplyHint.value = false
  // 可重试分类复位（S21）：每轮起点按本轮 llm-complete.retryable 重新判定，不带上一轮残留
  lastTurnRetryable = true
  isLoading.value = true
  // 发送后立即显示「加载中」占位 + 进度（处理上下文 → 发送 → 思考中）；
  // 占位为**独立状态**（不在 messages 内）→ 历史回填不会冲掉它；定时器兜底推进，
  // 首个真实内容到达时收起。
  showPending('processing')
  startPendingProgress()
  const sid = props.sessionId
  const at = messages.value.length // user 气泡插入点（发送时刻的消息末尾）
  const { turn: turnId, ack } = publishLLMStart(
    sid,
    batch.text,
    batch.llm || '',
    batch.thinkFlag || 'on',
    batch.effortLevel || 'high',
    batch.scenarioId,
  )
  // 记录本轮发送参数，供「同轮次继续」复用（场景/模型/思考档不丢）
  lastSendParams = {
    llm: batch.llm || currentLlm.value || '',
    think: batch.thinkFlag || 'on',
    effort: batch.effortLevel || 'high',
    scenarioId: batch.scenarioId || '',
  }
  // turn 前端自分配：无需等后端 ack 即可过滤流事件（常驻订阅已注册）。
  if (turnId) {
    currentTurnId.value = turnId
    lastTurnId.value = turnId
  }
  // 落库回执到达后再渲染 user 气泡；等待期间已切会话 → 不渲染
  await ack
  if (activeSessionId.value && activeSessionId.value !== sid) return
  if (batch.type === 'message') pushUserBubble(batch.text, at)
}

// ── 待发送占位气泡进度定时器 ──
// 后端无独立事件标注各阶段，用时间推进兜底（覆盖即可）：
//   处理上下文（0s）→ 发送（+1.5s）→ 思考中（+4s）
// 首个真实内容到达 → 占位结束（定时器仅兜底推进）。
let pendingTimerSending = null
let pendingTimerThinking = null
function startPendingProgress() {
  clearPendingTimers()
  pendingTimerSending = setTimeout(() => setPendingStatus('sending'), 1500)
  pendingTimerThinking = setTimeout(() => setPendingStatus('thinking'), 4000)
}
function clearPendingTimers() {
  if (pendingTimerSending) { clearTimeout(pendingTimerSending); pendingTimerSending = null }
  if (pendingTimerThinking) { clearTimeout(pendingTimerThinking); pendingTimerThinking = null }
}

// 裁决清除：MessageItem 发事件回传消息对象（消息归本组件持有）→ 置空 arbitration（E-08）。
function onClearArbitration(msg) {
  if (msg) msg.arbitration = null
}

// user 气泡（send / drain 后的 message / 同轮次继续）：落库回执后渲染。
// at = 发送时刻的消息末尾下标：即使等待回执期间流式内容先到，也按该下标插入，
// 保持「用户消息在 assistant 输出之前」的时序（下标失效时回落追加）。
function pushUserBubble(text, at) {
  const idx = (typeof at === 'number' && at >= 0 && at <= messages.value.length)
    ? at : messages.value.length
  messages.value.splice(idx, 0, {
    role: 'user',
    content: text,
    type: 'text',
    id: Date.now().toString() + Math.random().toString(36).slice(2, 6),
    createdAt: new Date().toISOString(),
  })
  autoScroll.value = true
  nextTick(() => scrollToBottom())
}

// ── Handle cancel (from ChatPanel via mq) ──
// 主会话 cancel（20-gui）：发 llm-cancel{session,turn} 消息，无弹框。
// 标记 lastTurnCanceled：llm-complete 后对队列做简化处理（§3.5.3）——
// 待发消息写回 textarea，由用户点击发送。
// turn-id 缺失兜底（llm-started 缺失/后到，busy 但 currentTurnId 未设置）：
// 仍按 session 发取消，server 经 LLM 域解析运行中 turn。
function onMessageCancel() {
  const turnId = currentTurnId.value
  const sid = activeSessionId.value || props.sessionId
  if (!turnId && !sid) return
  lastTurnCanceled.value = true
  userCanceled = true
  confirmTurnCancel({ turnId, sessionId: sid })
}

// ── canceled 队列简化（§3.5.3 / §3.4.1）：当前 LLM 桶的待发消息写回 textarea ──
function handleCanceledQueue() {
  const texts = sendQueue.takeTexts(currentLlm.value)
  if (texts.length === 0) return
  mq.emit(EventNames.chatQueueRestore, { text: texts.join('\n') })
}

// ── 队列项操作（InputBox popover）：撤回（删除 + 写回 textarea）/ 删除 ──
function onQueueAction({ id, action }) {
  const item = sendQueue.takeById(id)
  if (!item) return
  if (action === 'recall') {
    const text = item.payload?.text || ''
    if (text && text.trim()) mq.emit(EventNames.chatQueueRestore, { text })
  }
}

// ── 异步任务完成通知（后端 sendCompletionNotice 推送 tool-notify / notice=completion）──
// DB 已写入"🔔 异步任务已完成"消息（role=user, type=user-notify，同一 message_id）。
// 本视图只会在会话切换时重载——这里即时追加该通知消息，使用户在命令完成后立刻看到。
// 去重依据：事件携带的真实 message_id（DB id 形如 msg-notify-*），重复事件不重复追加（§三 / §六B6）。
//
// 域化（阶段三）：通知轮次由后端 LLM 域空闲时自动触发（notify 归后端，
// 监听 tool-notify 入队 → 空闲发内部 llm-start），前端只负责显示，不入队。
// auto_notice=true（自动轮次内派生的任务）与 viewer（子会话只读详情）照旧仅显示。
function onCompletionNotice(d) {
  // completion：异步任务完成通知；recover：LLM 断链恢复（「继续」气泡）。
  if (!d || (d.notice !== 'completion' && d.notice !== 'recover')) return
  const cur = activeSessionId.value || props.sessionId
  if (cur && d.session_id && d.session_id !== cur) return
  const text = d.message || d.text || ''
  if (!text) return
  const nid = d.message_id || ('ntf_' + (d.task_id || d.turn_id || Date.now()))
  if (messages.value.some(m => m.id === nid)) return
  messages.value.push({
    id: nid,
    role: 'user',
    type: 'user-notify',
    content: text,
    createdAt: new Date().toISOString(),
  })
  autoScroll.value = true
  nextTick(() => scrollToBottom())
}

// ── LLM 域事件（server 协议）：llm-complete（唯一终态）──
// busy/idle 由 LLM 域状态驱动（非本地猜测）：
//   llm-complete{status: complete/incomplete/error/interrupted} → 清理订阅 + 空闲 + drain。
// 注：旧 llm-start.reply 无发布方（桥 compat.go：llm-started 现为 server-starting 的服务就绪
// ack，非每轮受理 ack）→ 原 onLlmStarted 死分支已删除（I-45）；占位进度由 startPendingProgress
// 定时器 + 首个流内容推进。

// llm-error（旧兼容主题，桥由 llm-complete{status:error} 兼发）：分类**兜底来源**——
// 本轮 llm-complete 已带真实 retryable 时以其为准（见 onLlmComplete），此订阅仅承接
// 「llm-complete 缺 retryable 字段」（旧载荷/手工注入事件）与诊断。
// 不可自动重试（如 API Key 401）→ 本次 turn 终结后不自动续写，只显示"继续"按钮（手动重试）。
function onLlmError(d) {
  const sid = activeSessionId.value || props.sessionId
  if (sid && d.session_id && d.session_id !== sid) return
  if (d.retryable === false) lastTurnRetryable = false
}

// ── 工具重试（interrupted 的 tool_pair，S2 abort 场景）──
// 按钮 @click 即时计算载荷并 publish tool-retry（主流程）；本 on 订阅仅本地乐观
// 置回 running（列表即时反馈，用本地 lastInterruptedPair，不依赖 payload 的 id 字段）。
function onRetryToolClick() {
  const pair = lastInterruptedPair.value
  const sid = activeSessionId.value || props.sessionId
  if (!pair || !sid) return
  // msg-ref §4.2：tool-retry {session, turn} → {ok, task_id?}。turn = 该工具所属 turn id
  // （DB 历史消息带 turn_id；实时气泡无 turn_id 时回退当前轮 currentTurnId）。
  mq.emit(EventNames.toolRetry, {
    session: sid,
    turn: pair.turn_id || currentTurnId.value || '',
  })
}
function onToolRetry(d) {
  const sid = activeSessionId.value || props.sessionId
  if (!sid) return
  const es = d.session || d.session_id
  if (es && es !== sid) return
  const pair = lastInterruptedPair.value
  if (!pair) return
  if (d.turn && pair.turn_id && d.turn !== pair.turn_id) return
  pair.status = 'running'
}

// tool-pair 终态事件（compat：server 任务节点 tasks.started/updated/done → tool-pair）→
// 更新列表中对应 tool_pair 的状态/结果。匹配键：server 任务载荷自带 tool_call_id
// （call_*，与消息气泡 tool_call_id 同源）优先；无 call id 的旧形态事件回退
// tool_id/task_id（tk-* 任务节点 id → 与气泡 DB 落库 task_id 同值）。
function onToolPairUpdate(d) {
  const sid = activeSessionId.value || props.sessionId
  if (sid && d.session_id && d.session_id !== sid) return
  const callId = d.tool_call_id || ''
  const tid = d.tool_id || d.task_id || ''
  if (!callId && !tid) return
  const msg = messages.value.find((m) => {
    if (m.type !== 'tool_pair') return false
    if (callId) return m.tool_call_id === callId || m.id === 'tp_' + callId
    return m.tool_call_id === tid || m.task_id === tid || m.id === 'tp_' + tid
  })
  if (!msg) return
  if (d.status) msg.status = d.status
  if (d.result && !msg.result) msg.result = d.result
}

// 调用超时待裁决（mcp-tools-timeout，后端 → 前端）：按 tool_call_id（回退 task_id）
// 匹配当前 tool_pair 卡片，挂载裁决信息（options/timeout_s/tool/task_id）供 MessageItem
// 渲染裁决条。同一调用只弹一次（timeoutShown 去重，含裁决后再次到达）。
//
// 该事件是 fire-and-forget（41 I-57）：可能早于目标卡片渲染（超时很短 / 消息未落库），
// 或用户刷新/切会话后事件已丢失 → 裁决条不出现、任务永远 running。故未命中卡片时**不丢弃**，
// 先入待决缓存 pendingArbitration，待卡片加载/渲染（flush）或任务列表自愈（restore）时补挂。
const timeoutShown = new Set()
// key（tool_call_id 优先，回退 task_id）→ { arb, callId, tid }：事件早到/会话重载未命中卡片时暂存
const pendingArbitration = new Map()

// 按调用 id 在已渲染消息中定位 tool_pair 卡片（与 tool-pair 终态事件同一匹配规则）。
function findToolPairMsg(callId, tid) {
  return messages.value.find((m) => {
    if (m.type !== 'tool_pair') return false
    if (callId) return m.tool_call_id === callId || m.id === 'tp_' + callId
    return m.tool_call_id === tid || m.task_id === tid || m.id === 'tp_' + tid
  })
}

// 尝试把裁决挂到卡片：'attached' 已挂载 / 'terminal' 卡片已终态（裁决无意义）/ 'missing' 卡片未渲染。
function tryAttachArbitration(arb, callId, tid) {
  const msg = findToolPairMsg(callId, tid)
  if (!msg) return 'missing'
  const s = msg.status
  if (s && s !== 'pending' && s !== 'running') return 'terminal'
  if (!arb.tool) arb.tool = msg.tool || ''
  msg.arbitration = arb
  // 去重（I-103）：卡片已渲染裁决条 → 任务区节点条让位（「卡片存在时沿用卡片，卡片不存在时才由
  // 节点渲染」）。按 task_id 清本地 awaiting（缺 task_id 时回退调用 id，键不匹配则无操作）。
  clearAwaiting(msg.task_id || tid || callId)
  return 'attached'
}

// 把待决缓存中已能命中的项补挂到卡片（消息加载 / 卡片渲染后调用）。
function flushPendingArbitration() {
  if (pendingArbitration.size === 0) return
  for (const [key, item] of pendingArbitration) {
    if (timeoutShown.has(key)) { pendingArbitration.delete(key); continue }
    const r = tryAttachArbitration(item.arb, item.callId, item.tid)
    if (r === 'attached' || r === 'terminal') {
      timeoutShown.add(key)
      pendingArbitration.delete(key)
    }
  }
}

function onToolTimeout(d) {
  if (!d) return
  const callId = d.tool_call_id || ''
  const tid = d.task_id || ''
  if (!callId && !tid) return
  const key = callId || tid
  if (timeoutShown.has(key)) return
  const arb = {
    options: Array.isArray(d.options) ? d.options : [],
    timeout_s: d.timeout_s,
    tool: d.tool || '',
    task_id: tid,
  }
  const r = tryAttachArbitration(arb, callId, tid)
  if (r === 'attached' || r === 'terminal') { timeoutShown.add(key); return }
  // 卡片尚未渲染 → 入待决缓存，等卡片出现（flush）或任务列表自愈（restore）时补挂
  pendingArbitration.set(key, { arb, callId, tid })
}

// 切会话 / 刷新恢复（I-57 自愈）：查询任务列表，对处于 awaiting（待裁决）的任务补挂裁决条；
// 卡片未渲染则先入待决缓存。沿用既有前端通道（useTaskView.refresh → data-tasktree-tasks，
// 载荷 {session_id, top_session}，返回 {list:[...]}，不新造主题）。
// awaiting 契约（后端并行实现）：{"reason":"timeout","timeout_s":30,"options":[...]}。
async function restoreAwaitingArbitration(sessionId) {
  if (sessionId) {
    try {
      await refreshTasks(sessionId, '')
      for (const t of taskMap.values()) {
        if (!t || !t.awaiting) continue
        const callId = t.tool_call_id || ''
        const tid = t.task_id || ''
        if (!callId && !tid) continue
        const key = callId || tid
        if (timeoutShown.has(key)) continue
        const cached = pendingArbitration.get(key)
        const arb = cached ? cached.arb : {
          options: Array.isArray(t.awaiting.options) ? t.awaiting.options : [],
          timeout_s: t.awaiting.timeout_s,
          tool: t.tool || t.tool_name || '',
          task_id: tid,
        }
        const r = tryAttachArbitration(arb, callId, tid)
        if (r === 'attached' || r === 'terminal') {
          timeoutShown.add(key)
          pendingArbitration.delete(key)
        } else {
          pendingArbitration.set(key, { arb, callId, tid })
        }
      }
    } catch (e) {
      console.warn('[MessageList] restoreAwaitingArbitration failed:', e)
    }
  }
  // 补挂其余缓存项（卡片已渲染但事件早到）
  flushPendingArbitration()
}

function onLlmComplete(d) {
  const sid = activeSessionId.value || props.sessionId
  if (sid && d.session && d.session !== sid) return
  if (sid && d.session_id && d.session_id !== sid) return
  if (d.turn && currentTurnId.value && d.turn !== currentTurnId.value) return
  if (d.turn_id && currentTurnId.value && d.turn_id !== currentTurnId.value) return
  // server 协议状态枚举映射（20-gui）：
  //   complete↔completed（正常完成）；incomplete（length 截断）；error（错误，错误信息随
  //   message/code 携带，无独立 llm-error 主题）；interrupted↔cancelled（用户取消/中断）。
  const wasIncomplete = d.status === 'incomplete'
  const wasError = d.status === 'error'
  const wasInterrupted = d.status === 'interrupted' || d.status === 'cancelled'
  // 回复未写完（finish_reason 权威信号：length 超长截断 / insufficient_system_resource
  // 资源不足中断 / interrupted turn 级失败）
  const unfinished = wasIncomplete || wasInterrupted || UNFINISHED_REASONS.includes(d.finish_reason)
  // 空回复（S21）：turn 无任何 assistant 输出 → 实时提示"继续"（重发原用户消息）。
  // server 定稿协议下空回复收敛为 llm-complete{status:error, code:EMPTY_REPLY}
  //（61-消息一览：错误随 llm-complete 携带；空回复也走 error 终态），此处一并识别。
  // 通知轮次（notify=true，后端自动发起）不提示，避免误发用户消息。
  const isEmptyReplyErr = d.status === 'error' && d.code === 'EMPTY_REPLY'
  // 可重试分类（S21 修复要点）：**优先读 llm-complete.retryable**（server 定稿真实分类，
  // 桥原样透传）——该字段在本次终态里即可得，不受桥"兼发旧 llm-error（在 llm-complete之后）"
  // 的时序影响；字段缺省（旧载荷/手工注入事件，如 run_chat_flow CF6）→ 回退 llm-error 记录值。
  const retryable = (typeof d.retryable === 'boolean') ? d.retryable : lastTurnRetryable
  lastTurnRetryable = retryable
  // 自动续写场景：回复未写完，或 error 但该错误**可自动重试**（超时/网络/5xx），
  // 且非用户主动取消；viewer（子会话只读）不自动。
  // 不可自动重试的错误（如 API Key 401、空回复）→ 不自动续写，显示错误气泡/空回复提示 +"继续"。
  const autoRetry = (unfinished || (wasError && retryable)) && !isEmptyReplyErr && !userCanceled && !props.viewer
  const emptyReply = !turnHadOutput && !notifyTurn && (d.status === 'complete' || isEmptyReplyErr)
  if (wasError && !isEmptyReplyErr) {
    if (autoRetry) {
      // 断链/中断自动续写：静默收起「加载中」占位（不展示 Error 气泡，续写自然衔接）
      hidePending()
    } else {
      // 启动失败 / 用户取消：标记 canceled 语义（队列简化 + 错误展示）。
      // 传整个终态事件（message + code）：错误气泡按 errorMessage 分类映射出人话文案
      // （message 为空时也能按 code 归类，如 TOOL_LOOP_LIMIT / DB_ERROR）。
      lastTurnCanceled.value = true
      handleError({ message: d.message, code: d.code })
    }
  }
  cleanupAndFinish()
  if (lastTurnCanceled.value) {
    lastTurnCanceled.value = false
    handleCanceledQueue()
  } else {
    drainQueue() // turn 完全结束（LLM 域终结）：发送队列中等待的用户消息
  }
  if (autoRetry) {
    userCanceled = false
    autoContinueCount.value++
    if (autoContinueCount.value < MAX_AUTO_CONTINUE) {
      scheduleAutoContinue()
    } else {
      // 连续断链/截断达到上限：转手动"继续"按钮
      showContinue.value = true
    }
    return
  }
  userCanceled = false
  autoContinueCount.value = 0
  // 空回复（S21）：complete 且无任何 assistant 输出 → 实时提示 +"继续"（重发原消息）
  if (emptyReply) {
    emptyReplyHint.value = true
    showContinue.value = true
  } else if (!wasError && !wasInterrupted) {
    // 正常回复完成（有输出的 complete）：**不需要「继续」** → 收起（A7：正常回复后不显示；
    // 此前只置位不收起，会把上一轮失败留下的按钮一直挂着）
    showContinue.value = false
  }
  // 失败/取消（非自动续写）→ 评估显示"继续"；正常完成（complete）无需评估，直接不显示
  if (wasError || wasInterrupted) {
    assessContinueState()
    // 不可恢复错误（如 401 API Key，lastTurnRetryable=false）→ 直接显示「继续」供手动重试
    // （Error 气泡带 content 会让 lastTurnIncomplete 启发式误判为"有输出"，需强制置位）。
    if (wasError && !autoRetry) showContinue.value = true
  }
}

// ── Lifecycle ──
let unsubMessageSend = null
let unsubMessageQueue = null
let unsubMessageCancel = null
let unsubChatSelectLlm = null
let unsubSessionChanged = null
let unsubScrollTop = null
let unsubScrollBottom = null
let unsubToolNotify = null
let unsubLlmComplete = null
let unsubLlmError = null
let unsubToolRetry = null
let unsubToolPairUpdate = null
let unsubToolTimeout = null
let unsubQueueAction = null
let unsubChatContinue = null

onMounted(async () => {
  setupPersistentStreamSubs()
  unsubMessageSend = mq.on(EventNames.messageSend, onMessageSend)
  // 入队（LLM 忙碌时）：InputBox 入队按钮 → ChatPanel → mq.emit(message-queue) → 本订阅入队
  unsubMessageQueue = mq.on(EventNames.messageQueue, onMessageQueue)
  unsubMessageCancel = mq.on(EventNames.messageCancel, onMessageCancel)
  // 当前 LLM 跟随 ChatPanel（initSession / 选择 LLM）：待发送队列按 LLM 分桶
  unsubChatSelectLlm = mq.on(EventNames.chatSelectLlm, ({ name }) => {
    currentLlm.value = name || ''
  })
  // LLM 域事件（server 协议）：llm-complete 终结（drain）
  unsubLlmComplete = mq.on(EventNames.llmComplete, onLlmComplete)
  // llm-error：分类兜底（llm-complete.retryable 优先；此订阅承接旧载荷/手工注入事件）
  unsubLlmError = mq.on(EventNames.llmError, onLlmError)
  // 工具重试（interrupted → 直接重跑）+ tool-pair 终态更新
  unsubToolRetry = mq.on(EventNames.toolRetry, onToolRetry)
  unsubToolPairUpdate = mq.on(EventNames.toolPair, onToolPairUpdate)
  // 调用超时待裁决（mcp-tools-timeout）：挂载裁决条到对应 tool_pair 卡片
  unsubToolTimeout = mq.on(EventNames.toolTimeout, onToolTimeout)
  // 队列项操作（InputBox popover）：撤回 / 删除（§3.4.1）
  unsubQueueAction = mq.on(EventNames.chatQueueAction, onQueueAction)
  // 滚动主题（msg-scroll-top/bottom）保留为**程序化入口**（仅本会话实例响应；主 LLM / 子 LLM
  // 各自滚动）——原「向上/向下箭头」浮窗按钮已按用户口径移除（P3-C1，2026-09-24）。
  unsubScrollTop = mq.on(EventNames.msgScrollTop, (d) => { if (isForThisSession(d)) scrollTop() })
  unsubScrollBottom = mq.on(EventNames.msgScrollBottom, (d) => { if (isForThisSession(d)) scrollBottom() })
  unsubToolNotify = mq.on(EventNames.toolNotify, onCompletionNotice)
  // 继续按钮：v-mq 触发 → 发送"继续"
  unsubChatContinue = mq.on(EventNames.chatContinue, sendContinue)

  if (props.viewer) {
    // 子会话详情（只读查看）：跟随 subsession-changed 切换会话，流订阅已常驻。
    unsubSessionChanged = mq.on(EventNames.subsessionChanged, onSessionChanged)
  } else {
    unsubSessionChanged = mq.on(EventNames.sessionChanged, onSessionChanged)
  }

  // If already have sessionId on mount, load messages + 评估"继续"
  if (props.sessionId) {
    activeSessionId.value = props.sessionId
    await loadMessages(props.sessionId)
    await assessContinueState()
    // 刷新恢复：挂载后按任务列表 awaiting 补挂超时裁决条（I-57）
    await restoreAwaitingArbitration(props.sessionId)
  }

  onScroll()
})

onUnmounted(() => {
  if (unsubMessageSend) unsubMessageSend()
  if (unsubMessageQueue) unsubMessageQueue()
  if (unsubMessageCancel) unsubMessageCancel()
  if (unsubChatSelectLlm) unsubChatSelectLlm()
  if (unsubSessionChanged) unsubSessionChanged()
  if (unsubScrollTop) unsubScrollTop()
  if (unsubScrollBottom) unsubScrollBottom()
  if (unsubToolNotify) unsubToolNotify()
  if (unsubLlmComplete) unsubLlmComplete()
  if (unsubLlmError) unsubLlmError()
  if (unsubToolRetry) unsubToolRetry()
  if (unsubToolPairUpdate) unsubToolPairUpdate()
  if (unsubToolTimeout) unsubToolTimeout()
  if (unsubQueueAction) unsubQueueAction()
  if (unsubChatContinue) unsubChatContinue()
  persistentUnsubs.forEach(fn => { try { fn() } catch (e) { console.error('[MessageList] persistentUnsubs cleanup error:', e) } })
  persistentUnsubs.length = 0
  clearTimeout(autoContinueTimer)
  clearPendingTimers()
  cleanupAndFinish()
  resetMessages()
})

defineExpose({ scrollTop, scrollBottom, isLoading, showContinue, lastInterruptedPair, emptyReplyHint, onRetryToolClick, activeSessionId, currentTurnId })
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
/* 对话区：turn 导航专用列/行 + 滚动内容区（导航与内容分开，导航不随内容滚动）。
   方向由组件参数 `direction` 决定（CHAT-013-S06 / [42 §2 (154)]，代码可配、无用户配置项）——
   每条 .dir-* 同步切换 4 处：flex 方向 / 尺寸轴 / 弹层弹出方向 / 边框方向。 */
.message-body {
  flex: 1;
  min-height: 0;
  display: flex;
}
.message-list {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden; /* 防止长 purpose/宽内容撑出水平滚动条 */
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  position: relative;
}
/* turn 导航：独立于滚动内容（不滚动、不随内容移动），最多 10 个圆圈；居中，
   悬停弹出该圈覆盖轮次的用户消息一览。位置/尺寸轴/边框由 .dir-* 修饰。 */
.turn-nav {
  position: relative;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  z-index: 15;
  /* 底色走主题 token（P1-7）：原 rgba(0,0,0,0.02) 在 dark/nord 下不随主题（违反浮层 token 约定）。
     color-mix(var(--text-primary) 2%, transparent) 与既有浮层写法一致，三主题自动适配。 */
  background: color-mix(in srgb, var(--text-primary) 2%, transparent);
}
/* 左列：flex 横排 + 导航列在左（尺寸轴 = width）；边框朝内容（右）；弹层向右弹 */
.message-body.dir-left { flex-direction: row; }
.message-body.dir-left .turn-nav {
  order: 1;
  flex-direction: column;
  width: 22px;
  padding: 8px 0;
  border-right: 1px solid var(--border, #dee2e6);
}
.message-body.dir-left .message-list { order: 2; }
.message-body.dir-left .turn-nav-pop { left: calc(100% + 8px); }
/* 右列（默认：主 chat / 纯对话窗口）：flex 横排 + 导航列在右；边框朝内容（左）；弹层向左弹 */
.message-body.dir-right { flex-direction: row; }
.message-body.dir-right .turn-nav {
  order: 2;
  flex-direction: column;
  width: 22px;
  padding: 8px 0;
  border-left: 1px solid var(--border, #dee2e6);
}
.message-body.dir-right .message-list { order: 1; }
.message-body.dir-right .turn-nav-pop { right: calc(100% + 8px); }
/* 上行：flex 竖排 + 导航行在上（尺寸轴 = height）；边框朝内容（下）；弹层向下弹 */
.message-body.dir-top { flex-direction: column; }
.message-body.dir-top .turn-nav {
  order: 1;
  flex-direction: row;
  height: 22px;
  padding: 0 8px;
  border-bottom: 1px solid var(--border, #dee2e6);
}
.message-body.dir-top .message-list { order: 2; min-height: 0; }
.message-body.dir-top .turn-nav-pop { top: calc(100% + 8px); }
/* 下行（子会话）：flex 竖排 + 导航行在下；边框朝内容（上）；弹层向上弹 */
.message-body.dir-bottom { flex-direction: column; }
.message-body.dir-bottom .turn-nav {
  order: 2;
  flex-direction: row;
  height: 22px;
  padding: 0 8px;
  border-top: 1px solid var(--border, #dee2e6);
}
.message-body.dir-bottom .message-list { order: 1; min-height: 0; }
.message-body.dir-bottom .turn-nav-pop { bottom: calc(100% + 8px); }
.turn-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 1.5px solid var(--border, #bbb);
  background: var(--bg-surface, #fff);
  cursor: pointer;
  transition: all 0.15s;
  box-sizing: border-box;
  flex-shrink: 0;
}
.turn-dot:hover {
  border-color: var(--accent, #409eff);
}
.turn-dot.active {
  border-color: var(--accent, #409eff);
  background: var(--accent, #409eff);
  box-shadow: 0 0 0 1px var(--accent, #409eff);
}
.turn-dot.disabled {
  opacity: 0.25;
  cursor: default;
}
.turn-dot.disabled:hover {
  border-color: var(--border, #bbb);
}
/* 弹层：绝对定位锚定 .turn-nav（position: relative）；弹出方向由 .dir-* 决定，
   对齐用内联 top（竖向列）/ left（横向行）见 navPopStyle。 */
.turn-nav-pop {
  position: absolute;
  min-width: 160px;
  max-width: 240px;
  background: var(--bg-secondary, #fff);
  border: 1px solid var(--border, #ddd);
  border-radius: 6px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  padding: 4px;
  z-index: 30;
}
.turn-nav-item {
  padding: 4px 8px;
  font-size: 12px;
  color: var(--text-primary);
  cursor: pointer;
  border-radius: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.turn-nav-item:hover {
  background: var(--bg-hover, #eee);
}
/* 底部操作区已移至 ChatPanel 输入区上方 */
.empty-prompt,
.empty-inside {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-muted);
  font-size: 13px;
}
</style>
