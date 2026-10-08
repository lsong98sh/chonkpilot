<template>
  <div class="message-item" :class="[message.role, message.type]" :data-id="message.id">
    <!-- Avatar: only show for first message in a group (user-notify 除外，走 🔔 通知样式) -->
    <div v-if="showHeader && message.type !== 'user-notify' && (message.role === 'assistant' || message.role === 'user' || message.role === 'tool' || message.type === 'tool_pair')" class="speaker-line" :class="message.role === 'tool_pair' ? 'assistant' : message.role">
      <template v-if="message.role === 'assistant' || message.type === 'tool_pair'">
        <span class="speaker-icon assistant">
            <svg style="width: 1em; height: 1em; vertical-align: middle; fill: currentcolor; overflow: hidden;" viewBox="0 0 1024 1024" version="1.1" xmlns="http://www.w3.org/2000/svg">
              <path d="M209.408 76.8c-27.093333-1.408-52.821333 1.92-76.501333 11.776-21.888 9.130667-34.090667 28.586667-39.893334 45.184-5.845333 16.554667-7.552 32.981333-8.021333 50.261333-0.896 34.474667 4.096 72.746667 10.752 110.08 11.861333 66.432 26.538667 116.010667 30.293333 128.896-20.053333 42.666667-40.704 92.074667-40.704 150.4 0 109.866667 49.962667 203.989333 128.256 267.434667C291.84 904.277333 397.397333 938.666667 512 938.666667c114.901333 0 220.373333-35.669333 298.496-99.584C888.618667 775.168 938.666667 681.429333 938.666667 573.397333c0-55.04-18.261333-105.216-40.746667-150.613333 3.541333-12.8 17.706667-62.549333 28.842667-129.109333 6.229333-37.333333 10.794667-75.648 9.472-110.165334-0.64-17.28-2.474667-33.706667-8.490667-50.176-5.973333-16.512-18.346667-35.712-40.149333-44.757333-47.317333-19.712-102.314667-12.8-160.085334 5.76-50.474667 16.213333-100.693333 45.824-142.677333 85.845333C560.768 175.36 536.533333 170.666667 512 170.666667a42.666667 42.666667 0 0 0-0.085333 0c-24.448 0.085333-48.64 4.096-72.661334 8.32a374.869333 374.869333 0 0 0-144.938666-85.504c-29.312-9.045333-57.770667-15.232-84.906667-16.64z m-36.693333 91.136c15.232-4.181333 53.333333-6.186667 96.426666 7.168 46.250667 14.293333 94.592 41.472 125.696 76.373333a42.666667 42.666667 0 0 0 41.386667 13.269334A342.528 342.528 0 0 1 512 256h0.085333c24.746667 0 50.176 3.328 74.410667 9.685333a42.666667 42.666667 0 0 0 42.666667-12.842666c31.36-35.242667 79.104-62.72 124.416-77.226667 42.453333-13.653333 79.786667-11.904 94.72-7.68 1.109333 4.394667 2.346667 9.557333 2.688 18.816 0.938667 23.808-2.688 58.581333-8.405334 92.842667-11.434667 68.522667-30.293333 135.424-30.293333 135.424a42.666667 42.666667 0 0 0 3.413333 31.744c22.4 42.112 37.632 85.632 37.632 126.634666 0 82.346667-35.968 149.888-96.853333 199.68C695.637333 822.869333 609.152 853.333333 512 853.333333c-97.450667 0-183.978667-29.653333-244.650667-78.848C206.634667 725.333333 170.666667 658.133333 170.666667 573.44c0-41.941333 17.578667-84.906667 38.4-128.213333a42.666667 42.666667 0 0 0 2.517333-30.592s-19.626667-66.858667-31.829333-135.424c-6.101333-34.261333-10.069333-69.12-9.386667-92.928 0.213333-9.002667 1.408-13.909333 2.389333-18.346667z"/>
              <path d="M341.333333 554.666667a42.666667 42.666667 0 0 0-42.666666 42.666666v21.333334a42.666667 42.666667 0 0 0 42.666666 42.666666 42.666667 42.666667 0 0 0 42.666667-42.666666V597.333333a42.666667 42.666667 0 0 0-42.666667-42.666666zM682.666667 554.666667a42.666667 42.666667 0 0 0-42.666667 42.666666v21.333334a42.666667 42.666667 0 0 0 42.666667 42.666666 42.666667 42.666667 0 0 0 42.666666-42.666666V597.333333a42.666667 42.666667 0 0 0-42.666666-42.666666zM480 650.666667a42.666667 42.666667 0 0 0-30.165333 72.832l32 32a42.666667 42.666667 0 0 0 60.330666 0l32-32a42.666667 42.666667 0 0 0-30.165333-72.832z"/>
            </svg>
        </span>
        <span class="speaker-name">{{ $t('chat.speaker_chonk') }}</span>
      </template>
      <template v-else-if="message.role === 'tool'">
        <span class="speaker-icon tool">
          <Icon name="setting" :size="14" />
        </span>
        <span class="speaker-name tool-name">{{ $t('chat.speaker_tool') }}</span>
      </template>
      <template v-else>
        <span class="speaker-icon user">
          <Icon name="user" :size="14" />
        </span>
        <span class="speaker-name">{{ $t('chat.speaker_you') }}</span>
      </template>
    </div>

    <!-- Timestamp: show for every message (user-notify 在通知行内自显时间) -->
    <div v-if="message.type !== 'user-notify' && (message.role === 'assistant' || message.role === 'user' || message.role === 'tool' || message.type === 'tool_pair')" class="timestamp-line">
      <span class="speaker-time">{{ formattedTime }}</span>
    </div>

    <div v-if="message.type === 'reasoning'" class="collapsible-section reasoning-section">
      <div class="section-header" v-mq:[EventNames.msgToggleCollapse].click="{ id: message.id }">
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label">{{ $t('chat.section_thinking') }}</span>
        <span class="header-spacer" />
        <Tooltip :content="$t('common.copy')" placement="top" :show-after="600">
          <Icon name="copy-document" :size="13" class="copy-icon" :title="$t('common.copy')" v-mq:[EventNames.msgCopyText].click.stop="{ id: message.id }" />
        </Tooltip>
        <span v-if="loadingMore" class="loading-icon">{{ $t('common.loading') }}</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ localContent }}</pre>
        <div v-if="showMore" class="more-bar">
          <span class="more-link" v-mq:[EventNames.msgLoadMore].click.stop="{ id: message.id }">+ {{ $t('chat.more') }}</span>
        </div>
      </div>
    </div>

    <div v-else-if="message.type === 'tool_call'" class="collapsible-section toolcall-section">
      <div class="section-header" v-mq:[EventNames.msgToggleCollapse].click="{ id: message.id }">
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label">{{ $t('chat.section_tool_call') }}</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ message.content }}</pre>
      </div>
    </div>

    <div v-else-if="message.type === 'tool_result'" class="collapsible-section toolresult-section">
      <div class="section-header" v-mq:[EventNames.msgToggleCollapse].click="{ id: message.id }">
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label">{{ $t('chat.section_tool_result') }}</span>
      </div>
      <div v-if="!collapsed" class="section-body">
        <pre class="force-wrap">{{ message.content }}</pre>
      </div>
    </div>

    <div v-else-if="message.type === 'tool_pair'" class="collapsible-section toolpair-section">
      <div class="section-header" v-mq:[EventNames.msgToggleCollapse].click="{ id: message.id }">
        <Icon :name="collapsed ? 'arrow-right' : 'arrow-down'" :size="12" />
        <span class="section-label tool-name-label" :title="toolPairTitle">{{ toolPairTitle }}</span>
        <span v-if="message.status === 'pending'" class="status-badge pending">{{ $t('chat.status_pending') }}</span>
        <span v-else-if="message.status === 'failed'" class="status-badge failed">{{ $t('chat.status_failed') }}</span>
        <span v-else-if="message.status === 'done'" class="status-badge done">{{ $t('chat.status_done') }}</span>
        <span v-else-if="message.status === 'interrupted'" class="status-badge interrupted">{{ $t('chat.status_interrupted') }}</span>
        <span v-else-if="message.status === 'cancelled'" class="status-badge cancelled">{{ $t('chat.status_cancelled') }}</span>
        <!-- 工具 _meta.async（I-60）：随消息回带，本地即可判断该工具裁决能力（manual 可转异步） -->
        <span
          v-if="asyncMode"
          class="status-badge async"
          :class="{ manual: asyncMode === 'manual' }"
          :title="$t('chat.tool_async_mode', { mode: asyncMode })"
        >{{ asyncLabel }}</span>
        <!-- 「查看任务详情」（仅确有后台任务时显示，避免死点）：message.task_id（任务节点 id）
             或本卡「转后台」成功后返回的 task_id 二者其一为空时不渲染。
             点击 = ① 任务面板已收起则先打开（复用既有 tasks-toggle）→ ② 定位（任务节点走
             task-detail-open 打开右侧任务详情；子会话节点走 subsession-changed 在左树选中）。
             复合两步（先开面板再定位）无法用单条 v-mq 表达，且本行既有按钮（等待/停止）同为
             @click.stop + 内部 mq.emit 形态，故沿用之。 -->
        <Button
          v-if="hasBackgroundTask"
          size="mini"
          text
          class="task-detail-btn"
          :title="$t('chat.view_task_detail')"
          @click.stop="locateTaskDetail"
        >
          <Icon name="list" :size="13" />
        </Button>
        <!-- 转后台 / 转异步（**同一动作，单一入口**）：点击发前端内部事件 → mq.emit('task-background', {tool_call_id})。
             原「超时裁决条」的「转异步」按钮与工具行「转后台」箭头语义完全相同（都发 task-background{tool_call_id}），
             故合并为本按钮：运行中 manual 工具显示（canBackground）；裁决态（options 含 detach）亦复用本按钮，
             避免同动作双入口。title 按当前语义切换（运行中=转后台 / 裁决态=转异步）。 -->
        <button
          v-if="canDetach"
          class="background-btn"
          :title="detachTitle"
          :disabled="backgrounding"
          v-mq:[EventNames.toolBackground].click.stop="{ id: message.id }"
        >
          <Icon name="arrow-right" :size="12" />
        </button>
        <span class="header-spacer" />
        <!-- 等待完成（never 裁决，mcp-tools-wait）：原「超时裁决条」按钮**移入工具行**，仅图标 + 原生 title（无气泡载体）。
             事件与语义不变 = mq.emit(EventNames.toolsWait, {tool_call_id})；显示条件不变 = 裁决 options 含 wait。 -->
        <button
          v-if="hasWait"
          class="background-btn"
          :title="$t('chat.timeout_wait')"
          :disabled="arbitrating"
          @click.stop="arbitrationWait"
        >
          <Icon name="clock" :size="11" />
        </button>
        <!-- 停止（在飞工具取消）：原「超时裁决条」的取消按钮**移入工具行**，与复制图标同行横向排列（无气泡载体）。
             2026-09-18：改走统一取消入口 task-stop（arbitrationCancel → mq.emit('task-stop', {tool_call_id})，
             服务端反查归一 server 节点 id 后级联取消并经层 → sink → gateway 真打断）；不再直发已移除的
             gateway 取消方法面。显示条件不变 = 裁决态（message.arbitration 存在）；
             hover 仅作提示文字（title），不用气泡承载按钮本体。 -->
        <button
          v-if="message.arbitration"
          class="background-btn stop-btn"
          :title="$t('chat.tool_stop')"
          :disabled="arbitrating"
          @click.stop="arbitrationCancel"
        >
          <Icon name="stop" :size="11" />
        </button>
        <Tooltip :content="$t('common.copy')" placement="top" :show-after="600">
          <Icon name="copy-document" :size="13" class="copy-icon" :title="$t('common.copy')" v-mq:[EventNames.msgCopyTool].click.stop="{ id: message.id }" />
        </Tooltip>
        <span v-if="loadingMore" class="loading-icon">...</span>
      </div>
      <!-- 调用超时待裁决（mcp-tools-timeout）提示：原「裁决条」整条去除（不放气泡/浮层），
           三个动作（转异步 = 工具行箭头 / 等待完成 / 停止）全部移入上方工具行；
           此处仅保留等待态小字文案。显示条件不变 = 裁决态（message.arbitration 存在）；
           裁决后清除（arbitration=null）→ 提示与三个按钮同时隐藏，同一 tool_call_id 不再弹。 -->
      <div v-if="message.arbitration" class="arbitration-hint">{{ arbitrationText }}</div>
      <div v-if="!collapsed" class="section-body">
        <div class="brief-line">
          <span class="brief-text">{{ message.brief || message.simplified || message.tool }}</span>
          <span v-if="!showingFull" class="more-link" v-mq:[EventNames.msgShowFull].click.stop="{ id: message.id }">{{ moreLabel }}</span>
          <span v-if="loadingMore" class="loading-icon">{{ $t('common.loading') }}</span>
        </div>
        <template v-if="showingFull">
          <div v-if="!message.isOrphaned" class="pair-section">
            <div class="pair-sub-label">{{ $t('chat.arguments') }}</div>
            <pre class="force-wrap">{{ prettyArgs }}</pre>
          </div>
          <div class="pair-section">
            <!-- command/script 类工具：返回结果本身就是命令输出 → 标签显示"输出"而非"结果"，避免 output 与 result 重复 -->
            <div class="pair-sub-label">{{ isOutputTool ? $t('chat.output') : $t('chat.result') }}</div>
            <!-- 工具失败（结果 = `错误: <原始串>`，见 chonkpilot-llm/server/turn.go:548/583/614）：
                 先给人话原因（errorMessage 分类映射），原始串仍原样展示在下方 <pre>（可查/可复制）。 -->
            <div v-if="resultErrorText" class="pair-error-hint">{{ resultErrorText }}</div>
            <pre class="force-wrap">{{ localResult }}</pre>
          </div>
        </template>
      </div>
    </div>

    <div v-else-if="message.type === 'user-notify'" class="notify-row">
      <!-- 图标去重：历史消息 content 自带"🔔 "前缀（旧后端格式）时不再重复显示样式图标 -->
      <span v-if="!noticeHasBell" class="notify-icon">🔔</span>
      <div class="notify-body">
        <div class="message-content" v-html="renderedContent" />
        <div class="notify-time">{{ formattedTime }}</div>
      </div>
    </div>

    <div v-else-if="message.type === 'pending'" class="message-row">
      <div class="message-bubble pending-bubble">
        <span class="pending-spinner"></span>
        <span class="pending-status">{{ pendingStatusLabel }}</span>
      </div>
    </div>

    <!-- 轮次错误气泡（批 2 – 错误呈现面）：不再直出 `Error: <原始串>`，
         改为「人话文案（按 errorMessage 分类映射）+ 可展开的原始错误」（原始串不丢弃）。 -->
    <div v-else-if="message.type === 'error'" class="message-row">
      <div class="message-bubble error-bubble">
        <div class="error-text">{{ errorText }}</div>
        <div class="error-detail-bar">
          <span class="more-link" @click.stop="toggleErrorDetail">{{ $t('chat.error_detail_label') }}</span>
        </div>
        <pre v-if="errorDetailOpen" class="force-wrap error-detail-raw">{{ message.content }}</pre>
      </div>
      <div class="bubble-footer">
        <Icon name="copy-document" :size="14" class="copy-icon" :title="$t('common.copy')" v-mq:[EventNames.msgCopyText].click.stop="{ id: message.id }" />
      </div>
    </div>

    <div v-else class="message-row">
      <div class="message-bubble">
        <div class="message-content" v-html="renderedContent" />
      </div>
      <div class="bubble-footer">
        <Icon name="copy-document" :size="14" class="copy-icon" :title="$t('common.copy')" v-mq:[EventNames.msgCopyText].click.stop="{ id: message.id }" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { message as uiMessage, Button } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics, FieldKeys } from '../../events/msgkeys'
import { useToolAsyncMode } from '../../composables/useToolAsyncMode'
import { useTaskView } from '../../composables/useTaskView'
import { getFileUrl } from '../../api/file'
import { classifyError } from '../../utils/errorMessage'

const { t } = useI18n()

const props = defineProps({
  message: { type: Object, required: true },
  showHeader: { type: Boolean, default: true },
  isActive: { type: Boolean, default: false },
  collapseKey: { type: Number, default: 0 },
  sessionId: { type: String, default: null },
  isLastMessage: { type: Boolean, default: false },
  // 来自 DB 的历史消息（loadMessages / loadMoreMessages）→ reasoning 默认折叠；
  // 流式实时追加的消息（无此标记）→ turn 活跃时展开、turn 结束时折叠。
  fromHistory: { type: Boolean, default: false },
})

// 清除裁决：message 由 MessageList 持有 → 子组件只发事件，不直改 props 内部属性（E-08）。
const emit = defineEmits(['clear-arbitration'])


// 折叠初始值：来自 DB 的历史消息（fromHistory，切换 session / 翻页加载）默认折叠；
// 当前会话实时流式消息（无 fromHistory）默认展开。
const collapsed = ref(props.message.type === 'reasoning' ? props.fromHistory : true)
const loadingMore = ref(false)
const contentComplete = ref(false)
const showingFull = ref(false)
const backgrounding = ref(false) // 「转后台」请求进行中（防重复点击）
const arbitrating = ref(false) // 超时裁决请求进行中（防重复点击）

// 超时裁决（mcp-tools-timeout）：信息由 MessageList 挂载到 message.arbitration。
// 注：裁决条本体已去除，动作全部移入工具行（转异步=工具行箭头 / 等待完成 / 停止）。
const hasDetach = computed(() => {
  const opts = (props.message.arbitration && props.message.arbitration.options) || []
  return opts.includes('detach')
})
const hasWait = computed(() => {
  const opts = (props.message.arbitration && props.message.arbitration.options) || []
  return opts.includes('wait')
})
// 等待态文案（原裁决条文字）：工具名 + 已等待时长（timeout_s 秒；≥60s 显示 m/s），
// 现降级为工具行下方小字提示（无气泡/浮层载体）。
const arbitrationText = computed(() => {
  const a = props.message.arbitration || {}
  const n = Number(a.timeout_s)
  let seconds
  if (!Number.isFinite(n)) seconds = ''
  else if (n < 60) seconds = Math.floor(n) + 's'
  else seconds = Math.floor(n / 60) + 'm ' + Math.floor(n % 60) + 's'
  return t('chat.timeout_arbitrate', { tool: a.tool || props.message.tool || 'tool', seconds })
})

// tool_pair 转后台（G-17）：仅 async=manual 工具（同步运行但任务化、可解绑）显示按钮；
// auto 超阈值自动转、always 已转、never 不可转 → 均无手动入口。
const { isManualTool } = useToolAsyncMode()

// 工具 _meta.async（I-60）：随消息（DB 历史 / llm-receive 实时）回带，本地优先判断裁决能力。
const asyncMode = computed(() => (props.message._meta && props.message._meta.async) || '')
const asyncLabel = computed(() => (asyncMode.value === 'manual' ? t('chat.tool_async_manual') : asyncMode.value))

// tool_pair 运行中且可转后台：manual 工具 + 有 LLM tool-call id 且未结束（pending/running）。
// 判定以消息本地 _meta.async 为主（I-60），无 _meta 时回落全局 tools-list 缓存。
const canBackground = computed(() => {
  const s = props.message.status
  const localManual = !!props.message._meta && props.message._meta.async === 'manual'
  return (s === 'pending' || s === 'running') && !!props.message.tool_call_id &&
    (localManual || isManualTool(props.message.tool))
})

// 工具行「转后台 / 转异步」箭头：两个既有判定取并集（**不放宽也不收紧**）——
// 运行中 manual 工具（canBackground）∪ 裁决态含 detach（hasDetach）；二者动作同为
// task-background{tool_call_id}，故合并为单一入口。
const canDetach = computed(() => canBackground.value || (!!props.message.arbitration && hasDetach.value))
// 箭头提示语：裁决态 = 转异步（原裁决条按钮文案）；运行中 = 转后台。
const detachTitle = computed(() => (props.message.arbitration ? t('chat.timeout_detach') : t('chat.tool_background')))

// ── 工具卡「查看任务详情」入口 ────────────────────────────────────
// 显示判据（避免死点）：该工具调用**确有后台任务** = 消息自带 task_id（DB 落库的任务节点 id，
// 与 tasktree 节点主键同源）或本卡「转后台」成功后服务端返回的 task_id（转后台前二者皆空 →
// 纯同步工具不显示）。task_id 与节点主键同源，故可经 useTaskView 反查节点类型（会话/任务）。
const detachedTaskId = ref('')
const { getNode } = useTaskView()
const taskDetailId = computed(() => props.message.task_id || detachedTaskId.value)
const hasBackgroundTask = computed(() => !!taskDetailId.value)

// 打开任务面板（若已收起）+ 定位到该任务/子会话：
//   ① 已收起 → 先发既有 tasks-toggle 打开（与顶部「任务」开关同一语义，不改 store/MainLayout 内部状态）；
//   ② 面板挂载后再定位 —— 子会话节点（kind=llm）→ 左树选中该会话；任务节点 → 右侧任务详情。
//   面板已打开时 SessionChat / SessionTree 已订阅，直接发定位事件即可。
async function locateTaskDetail() {
  const tid = taskDetailId.value
  if (!tid) return
  if (!document.querySelector('.session-chat')) {
    mq.emit(EventNames.tasksToggle)
    await nextTick()
  }
  const node = getNode(tid)
  if (node && node.node_type === 'session' && node.session_id) {
    mq.emit(EventNames.subsessionChanged, { session_id: node.session_id })
  } else {
    mq.emit(EventNames.taskDetailOpen, { task_id: tid })
  }
}

// localResult/localContent were read-only computeds assigned elsewhere → Vue3
// silently drops the writes. Back them with override refs: read props until a
// full result/content is loaded, then keep the loaded value.
const localResultOverride = ref(null)
const localContentOverride = ref(null)

const localResult = computed({
  get: () => localResultOverride.value !== null ? localResultOverride.value : (props.message.result || ''),
  set: (v) => { localResultOverride.value = v },
})
const localContent = computed({
  get: () => localContentOverride.value !== null ? localContentOverride.value : (props.message.content || ''),
  set: (v) => { localContentOverride.value = v },
})

const showMore = computed(() => props.message.has_more && !contentComplete.value)

// tool_pair 标题：优先显示 LLM 必填的 purpose 调用理由（附工具名），历史消息无 purpose 时回退工具名。
const toolPairTitle = computed(() => {
  const p = props.message.purpose || ''
  if (p) return p + '（' + (props.message.tool || 'tool') + '）'
  return props.message.tool || 'tool'
})

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

// ── 错误人话化（批 2 – 错误呈现面）──
// 轮次错误气泡：errorKey/errorParams 由 sessionMessages.handleError 按 errorMessage 分类映射写入；
// 原始错误串保留在 message.content（折叠可查、可复制）。未识别类别（error_unknown）默认展开，
// 让用户直接看到原始串（此时通用文案信息量最低，原串才是唯一线索）。
const errorDetailOpen = ref(props.message.errorKey === 'chat.error_unknown')
const errorText = computed(() => {
  if (!props.message.errorKey) return props.message.content || '' // 兼容无分类的旧形态
  return t(props.message.errorKey, props.message.errorParams || {})
})
function toggleErrorDetail() {
  errorDetailOpen.value = !errorDetailOpen.value
}

// 工具失败结果（`错误: <原始串>`）的人话原因：仅识别出类别时叠加一行提示；
// 未识别（error_unknown）→ 不叠加（工具自身的中文 Output 往往已可读，原串照常展示在下方）。
const resultErrorText = computed(() => {
  if (props.message.status !== 'failed' && props.message.result_success !== false) return ''
  const cls = classifyError(props.message.result || '')
  if (!cls.detail || cls.key === 'chat.error_unknown') return ''
  return t(cls.key, cls.params)
})

// 当前会话 reasoning 保持展开，仅手动折叠；历史消息（fromHistory）默认折叠，只响应手动点击。
function toggleCollapse() {
  collapsed.value = !collapsed.value
}

// 列表项交互事件化：模板 v-mq 触发 → 本地按 message.id 分发到本实例
// （多实例列表：只有 id 匹配的实例执行），同时经 /publish 通知后端。
const _msgUnsubs = []
onMounted(() => {
  _msgUnsubs.push(mq.on(EventNames.msgToggleCollapse, ({ id }) => {
    if (id === props.message.id) toggleCollapse()
  }))
  _msgUnsubs.push(mq.on(EventNames.msgCopyText, ({ id }) => {
    if (id === props.message.id) copyTextContent()
  }))
  _msgUnsubs.push(mq.on(EventNames.msgCopyTool, ({ id }) => {
    if (id === props.message.id) copyToolContent()
  }))
  _msgUnsubs.push(mq.on(EventNames.msgLoadMore, ({ id }) => {
    if (id === props.message.id) loadMore()
  }))
  _msgUnsubs.push(mq.on(EventNames.msgShowFull, ({ id }) => {
    if (id === props.message.id) showFull()
  }))
  _msgUnsubs.push(mq.on(EventNames.toolBackground, ({ id }) => {
    if (id === props.message.id) backgroundTool()
  }))
})
onUnmounted(() => _msgUnsubs.forEach(fn => fn()))

// 统一内容拉取：动态 import api/session 并按 key 拉取完整内容。
// 四段重复（showFull/loadMore/copyTextContent/copyToolContent）收敛于此。
async function fetchFullContent(key) {
  if (!key || !key.includes(':') || !props.sessionId) return null
  try {
    const { getMessageContent } = await import('../../api/session')
    const res = await getMessageContent(props.sessionId, [key])
    return res?.[key] || null
  } catch (e) {
    console.warn('[MessageItem] Failed to load content:', e)
    return null
  }
}

function toolCallKey() {
  return 'tool_call:' + props.message.tool_call_id
}

function messageKey() {
  return 'message:' + (props.message.id || props.message.message_id || '')
}

async function showFull() {
  if (showingFull.value) return
  if (!localResult.value && props.message.has_more && props.sessionId) {
    if (loadingMore.value) return
    loadingMore.value = true
    try {
      const full = await fetchFullContent(toolCallKey())
      if (full) localResult.value = full
    } finally {
      contentComplete.value = true
      loadingMore.value = false
    }
  }
  showingFull.value = true
}

async function loadMore() {
  if (loadingMore.value || !props.sessionId) return
  loadingMore.value = true
  try {
    const full = await fetchFullContent(messageKey())
    if (full) localContent.value = full
  } finally {
    contentComplete.value = true
    loadingMore.value = false
  }
}

async function copyTextContent() {
  if (!localContent.value && props.message.has_more && props.sessionId) {
    loadingMore.value = true
    try {
      const full = await fetchFullContent(messageKey())
      if (full) localContent.value = full
    } finally {
      loadingMore.value = false
    }
  }
  // 错误气泡：原始串为空（终态未带 message/code）时回落人话文案，保证「复制」仍可用
  const text = localContent.value || props.message.content || errorText.value || ''
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    uiMessage.success(t('chat.copied'))
  } catch {
    uiMessage.warning(t('chat.copy_failed'))
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
      const full = await fetchFullContent(toolCallKey())
      if (full) {
        resultText = full
        localResult.value = full
      }
    } finally {
      loadingMore.value = false
    }
  }

  const text = argsText
    ? 'Arguments:\n' + argsText + '\n\nResult:\n' + (resultText || '')
    : resultText || ''
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    uiMessage.success(t('chat.copied'))
  } catch {
    uiMessage.warning(t('chat.copy_failed'))
  }
}

// 「转后台」：发 task-background{tool_call_id}（前端 → server → gateway tools/background），
// 成功后轻提示；失败（未命中/已终态/已解绑）给错误提示。
// 裁决态（超时待裁决）下本动作 = 原裁决条「转异步」→ 成功后同时清除裁决（提示文案与裁决按钮一并隐藏）。
async function backgroundTool() {
  const toolCallId = props.message.tool_call_id
  if (!toolCallId || backgrounding.value) return
  backgrounding.value = true
  try {
    const res = await mq.emit(MsgTopics.taskBackground, { [FieldKeys.tool_call_id]: toolCallId })
    const backend = res && res.backend
    const taskId = backend && backend.result && backend.result.task_id
    if (!backend || !backend.ok || !taskId) {
      const errText = (backend && backend.errors && backend.errors[0]) || 'no task_id'
      uiMessage.error(t('chat.tool_background_failed', { error: errText }))
      return
    }
    // 记下服务端返回的任务节点 id → 本卡「查看任务详情」图标随即可用（转后台前该卡无 task_id）
    detachedTaskId.value = taskId
    uiMessage.success(t('chat.tool_background_ok'))
    if (props.message.arbitration) hideArbitration()
  } finally {
    backgrounding.value = false
  }
}

// ── 超时裁决动作（mcp-tools-timeout）──
// 三个动作均在工具行：「转异步」复用工具行箭头（task-background，见 backgroundTool）、
// 「等待完成」发 mcp-tools-wait、「停止」走统一取消入口 task-stop。
// 裁决后清除 message.arbitration → 提示文案与工具行裁决按钮同时隐藏；MessageList 侧 timeoutShown 保证不再弹。
function hideArbitration() {
  // 交由持有方（MessageList）清除 message.arbitration（子组件不直改 props 内部属性；E-08）。
  emit('clear-arbitration', props.message)
}

// 等待完成（never）：发 mcp-tools-wait{tool_call_id}，撤销超时、继续等待原同步调用返回。
function arbitrationWait() {
  if (arbitrating.value) return
  const toolCallId = props.message.tool_call_id
  if (!toolCallId) return
  mq.emit(EventNames.toolsWait, { tool_call_id: toolCallId })
  hideArbitration()
}

// 取消（manual / never 共用）：统一走 task-stop（**不再直发已移除的 gateway 取消方法面**）。
// 裁决态气泡只有 LLM tool_call_id（message.arbitration.task_id / message.task_id
// 均为 **gateway 执行 id**，与 server 任务节点 id 不同源）→ 发 {tool_call_id}
// （instance_id 由 mq 自动注入）；服务端按 tool_call_id 反查 server 任务节点 → 层判定可取消
// 集合 → 层 → sink → gateway 真打断在飞调用。失败给出明确提示（不再静默）。
async function arbitrationCancel() {
  if (arbitrating.value) return
  const callId = props.message.tool_call_id || ''
  if (!callId) {
    uiMessage.error(t('chat.timeout_cancel_failed'))
    return
  }
  arbitrating.value = true
  try {
    const res = await mq.emit(EventNames.taskStop, { tool_call_id: callId })
    const backend = res && res.backend
    const errs = backend && Array.isArray(backend.errors) ? backend.errors : []
    if (errs.length > 0 || (backend && backend.ok === false)) {
      uiMessage.error(t('chat.tool_stop_failed', { error: errs[0] || 'no ack' }))
      return
    }
    hideArbitration()
  } finally {
    arbitrating.value = false
  }
}

const renderedContent = computed(() => {
  const content = localContent.value || props.message.content || ''
  return DOMPurify.sanitize(withLocalImageUrls(marked(content || '')))
})

// 用户消息里的附件标记 `![名](本地绝对路径)` → /show/ 同源 URL，使气泡内直接显示缩略图
// （原样渲染时 Windows 绝对路径会被当作相对 URL 而加载失败）；http(s) 等其余 src 不动。
function withLocalImageUrls(html) {
  return html.replace(/(<img\b[^>]*?\ssrc=")([^"]+)(")/g, (m, pre, src, post) => {
    if (/^https?:/i.test(src) || !/^[A-Za-z]:[\\/]/.test(src)) return m
    return pre + getFileUrl(src) + post
  })
}

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

// 命令/脚本类工具：返回结果本身就是命令输出 → 展开区"结果"标签改用"输出"
const OUTPUT_TOOLS = new Set(['command_execute', 'python_script_execute', 'script'])
const isOutputTool = computed(() => {
  const tool = (props.message.tool || '').toLowerCase()
  return OUTPUT_TOOLS.has(tool) || tool.endsWith('_script')
})

// 通知去重：历史消息 content 自带"🔔 "前缀时不显示样式图标
const noticeHasBell = computed(() => (props.message.content || '').trimStart().startsWith('🔔'))

// 待发送占位气泡的进度文案（处理上下文 → 发送 → 思考中）
const pendingStatusLabel = computed(() => {
  const key = {
    processing: 'chat.pending_processing',
    sending: 'chat.pending_sending',
    thinking: 'chat.pending_thinking',
  }[props.message.pendingStatus] || 'chat.pending_processing'
  return t(key)
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
  /* 内容列最大宽度 + 居中（P0-1）：原先无 max-width → 1280 宽（尤其纯对话窗口）下正文铺满整行、
     行长过长、留白失衡。列宽 = min(760px, 100%)（窄屏仍占满），列内用户/助手/工具左右对齐语义不变。 */
  width: 100%;
  max-width: min(760px, 100%);
  margin: 0 auto;
}

.message-item.user,
.message-item.tool {
  align-items: flex-end;
}

.message-item.assistant {
  align-items: flex-start;
}

/* user-notify（🔔 完成通知）：role=user 但走通知样式，靠左展示，覆盖 .user 的右对齐 */
.message-item.user-notify {
  align-items: flex-start;
}

.notify-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  max-width: 100%;
  padding: 8px 12px;
  border-radius: 6px;
  background: rgba(255, 193, 7, 0.10);
  border-left: 3px solid #f0ad4e;
}

.notify-icon {
  font-size: 14px;
  line-height: 1.5;
  flex-shrink: 0;
}

.notify-body {
  min-width: 0;
}

.notify-body .message-content {
  font-size: 13px;
  line-height: 1.5;
  color: var(--text-primary);
  word-break: break-word;
  overflow-wrap: anywhere;
}

.notify-time {
  margin-top: 2px;
  font-size: 11px;
  color: var(--text-muted);
  opacity: 0.7;
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

/* 待发送占位气泡：进度文案 + 旋转指示 */
.pending-bubble {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  background: var(--bg-secondary);
  color: var(--text-muted);
}
.pending-spinner {
  width: 12px;
  height: 12px;
  border: 2px solid var(--border, #ddd);
  border-top-color: var(--accent, #409eff);
  border-radius: 50%;
  animation: pending-spin 0.7s linear infinite;
  flex-shrink: 0;
}
@keyframes pending-spin {
  to { transform: rotate(360deg); }
}
.pending-status {
  font-size: 12px;
}

/* ── 错误人话化（批 2 – 错误呈现面）──
   文字/底色走主题 token（同 .td-error 口径：--danger 70% + --text-primary 混色、12% 淡底），
   无硬编码色值；原始错误块用面板色，与正文区分。 */
.error-bubble {
  background: color-mix(in srgb, var(--danger) 12%, var(--bg-tertiary));
  border-left: 3px solid var(--danger);
}

.error-text {
  color: color-mix(in srgb, var(--danger) 70%, var(--text-primary));
  word-break: break-word;
  overflow-wrap: anywhere;
}

.error-detail-bar {
  margin-top: 4px;
}

.error-detail-raw {
  margin-top: 4px;
  padding: 6px 8px;
  border-radius: 4px;
  background: var(--bg-tertiary);
  color: var(--text-secondary);
}

/* 工具卡失败结果上方的人话原因（原始结果仍在下方 <pre> 内原样展示） */
.pair-error-hint {
  margin-bottom: 4px;
  font-size: 12px;
  color: color-mix(in srgb, var(--danger) 70%, var(--text-primary));
  word-break: break-word;
  overflow-wrap: anywhere;
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

/* 行内代码：随主题 token（原硬编码 black 落在 dark/nord 气泡 #181825 上仅 1.2:1 不可读）。
   底色用 --bg-tertiary + 前景用 --text-primary：三主题对比度 12.97 / 13.01 / 7.49:1（见 20-gui §12.8），
   且同时适配 assistant 气泡（--bg-secondary）与 user 气泡（--accent）两种底色。 */
.message-content :deep(code) {
  color: var(--text-primary);
  background: var(--bg-tertiary);
  padding: 0 3px;
  border-radius: 3px;
  word-break: break-all;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

/* 代码块内 code 不叠加行内底色/内距（pre 已提供） */
.message-content :deep(pre code) {
  background: transparent;
  padding: 0;
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
  /* 表格网格线用 --border（--text-muted 提亮后作边框在暗色下过重，见 20-gui §12.8） */
  border: 1px solid var(--border);
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
  /* 卡头底色/文字走主题 token（原硬编码浅色底 + 深色字在 dark/nord 上不可读）；
     四种卡片仅以左侧色条区分语义（色条为装饰块，不承载文字对比度） */
  background: var(--bg-tertiary);
  color: var(--text-secondary);
  border-left: 3px solid var(--border);
}

.section-header:hover {
  opacity: 0.8;
}

.reasoning-section .section-header {
  border-left-color: var(--warning);
}

.toolcall-section .section-header {
  border-left-color: var(--accent);
}

.toolresult-section .section-header {
  border-left-color: var(--success);
}

.toolpair-section .section-header {
  border-left-color: var(--accent-hover);
}

.tool-name-label {
  font-weight: 700;
  min-width: 0; /* 允许 flex 收缩，长 purpose 标题单行省略，不撑开横向布局 */
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex-shrink: 1;
}

.status-badge {
  font-size: 10px;
  font-weight: 600;
  padding: 0 5px;
  border-radius: 3px;
  margin-left: 4px;
  line-height: 16px;
  /* 徽标文字统一用主题前景色，语义只由底色承载（color-mix 随主题 token 走）：
     原硬编码浅色字（#e65100/#2e7d32/#c62828…）在 dark/nord 卡头（--bg-tertiary）上对比不足 */
  color: var(--text-primary);
}

.status-badge.pending {
  background: color-mix(in srgb, var(--warning) 22%, var(--bg-tertiary));
}

.status-badge.done {
  background: color-mix(in srgb, var(--success) 22%, var(--bg-tertiary));
}

.status-badge.failed {
  background: color-mix(in srgb, var(--danger) 22%, var(--bg-tertiary));
}

.status-badge.interrupted {
  background: color-mix(in srgb, var(--text-secondary) 18%, var(--bg-tertiary));
}

.status-badge.cancelled {
  background: color-mix(in srgb, var(--text-secondary) 12%, var(--bg-tertiary));
}

/* 工具 _meta.async 徽标（I-60）：manual 高亮（可转异步） */
.status-badge.async {
  background: color-mix(in srgb, var(--accent) 22%, var(--bg-tertiary));
}
.status-badge.async.manual {
  background: color-mix(in srgb, var(--accent-hover) 22%, var(--bg-tertiary));
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

/* 工具行内联图标按钮（转后台/转异步、等待完成、停止）：hover 高亮 */
.background-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  flex-shrink: 0;
}
.background-btn:hover {
  background: var(--bg-secondary);
  color: var(--accent);
}
.background-btn:disabled {
  opacity: 0.5;
  cursor: default;
}

/* 工具行「查看任务详情」图标按钮（Button size="mini"）：压到与同行内联按钮（.background-btn 16×16）
   等高，避免撑高工具卡头；文字色 / 悬停底沿用 Button 的 .b-btn.is-text（--accent / --accent-bg token）。 */
.task-detail-btn.b-btn {
  min-height: 16px;
  height: 16px;
  padding: 0 2px;
}
/* 「停止」按钮（在飞工具取消）：复用 .background-btn 的尺寸/间距/hover/禁用态（16×16、radius 3、透明底），
   仅 hover 转危险色以示"停止"语义；与复制图标同一行（工具行 tool_pair section-header）。 */
.stop-btn:hover {
  /* 危险语义走主题 token（原硬编码 #ff4d4f：light 的 --bg-primary 上仅 3.10:1、
     叠 12% 同色淡底后 2.42:1）；图标属非文本图形，按 WCAG 1.4.11 取 ≥3:1，
     --danger + 12% 同色淡底三主题均达标（light 3.24 / dark 6.72 / nord 3.27，见 20-gui §12.8）。 */
  background: color-mix(in srgb, var(--danger) 12%, transparent);
  color: var(--danger);
}
.stop-btn:disabled:hover {
  background: transparent;
  color: var(--text-muted);
}

/* 超时裁决提示（mcp-tools-timeout）：原内联裁决条已整条去除，此处仅为工具行下方的小字文案
   （无气泡/浮层/背景块），与工具行内联按钮（.stop-btn 等）同处一个卡片。 */
.arbitration-hint {
  padding: 0 8px 4px 20px;
  font-size: 11px;
  line-height: 1.5;
  /* 告警语义走主题 token（原硬编码 #e65100：三主题在 --bg-primary 上 3.60/4.33/3.30，均 <4.5）。
     light 的 --warning（#e8a317）本身仅 2.06:1 → 向 --text-primary 压深 55%（#8e6a1f，4.71:1）；
     dark/nord 的 --warning 已达标（12.91 / 8.00），下方覆写回原色以保住琥珀语义（见 20-gui §12.8）。 */
  color: color-mix(in srgb, var(--warning) 55%, var(--text-primary));
  word-break: break-word;
  overflow-wrap: anywhere;
}

/* 暗色主题 --warning 直接可用，勿压深（压深后偏白、丢失"告警"色相） */
[data-theme="dark"] .arbitration-hint,
[data-theme="nord"] .arbitration-hint {
  color: var(--warning);
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