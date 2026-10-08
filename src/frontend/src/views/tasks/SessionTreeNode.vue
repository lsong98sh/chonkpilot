<template>
  <div class="session-tree-node">
    <div
      class="node-header"
      :class="{ active: node.node_id === activeId, closed: isClosed }"
      :style="{ paddingLeft: depth * 16 + 'px', background: highlighted ? 'var(--accent-bg)' : '' }"
      :title="headerTitle"
      :data-node-id="node.node_id"
    >
      <!-- 左1 展开箭头（有子级）或占位（叶子） -->
      <Icon
        v-if="children.length > 0"
        class="expand-icon"
        v-mq:[EventNames.sessionTreeNodeToggle].click.stop="{ node_id: node.node_id }"
        :size="12"
        :name="node.expanded ? 'arrow-down' : 'arrow-right'"
      />
      <span v-else class="leaf-spacer" />

      <!-- 左2 kind 图标：LLM=🧠 蓝 | tool/ask_user=工具箱 灰 -->
      <Icon :name="kindIcon" :size="13" :color="kindColor" />

      <!-- 中 标题：点击 → session 节点=会话内容（载荷 session_id）；task 节点=任务详情（载荷 task_id） -->
      <span
        class="node-title"
        v-mq:[selectEvent].click.stop="selectPayload"
      >{{ displayTitle }}</span>

      <!-- 折叠容器进度徽标（DSL-3）：LOOP/PARALLEL 显示「第 N 轮 / 共 M 轮」，
           节点本身不随迭代增长（树恒为静态语句）——徽标只刷新进度 -->
      <span v-if="progressLabel" class="node-progress" :title="progressLabel">{{ progressLabel }}</span>

      <!-- 已关闭标记（42 §2 (126)）：closed = 逻辑删除标记，节点只读展示（灰 + 「已关闭」标签），
           同列表内展示、级联子节点一并可见；不提供恢复/重开入口 -->
      <span v-if="isClosed" class="node-closed-tag">{{ $t('taskView.closed_label') }}</span>

      <!-- 右1 仅 kind=llm 且 running：思考中 + 运行耗时（停止统一走右侧 hover 切换，避免重复入口） -->
      <template v-if="isLLMRunning">
        <Icon name="think" :size="11" color="#1890ff" />
        <span class="llm-task-meta">{{ fmtElapsed(elapsedSec) }}</span>
      </template>

      <!-- 右2 状态/动作：状态图标（唯一映射见 useTaskStatus，I-94 补齐 8 态）；
           running（含塌缩为 running 的 detached/awaiting）hover 切换 stop，非 running 仅状态图标 -->
      <span class="status-wrap" :class="{ 'has-action': isRunning }">
        <Icon
          v-if="statusMeta.icon"
          class="status-icon"
          :class="{ spinning: statusMeta.spin, pulsing: statusMeta.pulse }"
          :color="statusMeta.color"
          :name="statusMeta.icon"
          :title="statusMeta.label ? $t(statusMeta.label) : ''"
        />

        <!-- hover 动作：仅 running → stop（task 节点强制级联 / session 节点三选）；无删除节点操作 -->
        <button
          v-if="isRunning"
          class="node-close node-stop hover-act"
          :title="$t('taskView.stop_task')"
          v-mq:[stopEvent].emit.stop="stopPayload"
        >
          <Icon name="stop" :size="11" />
        </button>
      </span>
    </div>

    <!-- 待裁决裁决条（I-103）：任务区**直接按 `awaiting` 渲染**，不依赖消息卡片
         （到超时点时工具仍在飞、无 `role=tool` 结果行 → 刷新/切会话后卡片不存在 →
         MessageList 的补挂路径恒 missing；此处为**无卡片也能裁决**的载体）。
         动作可用项按 `awaiting.options`（never=[wait,cancel] / manual=[detach,cancel]）决定；
         三个动作均复用既有事件，不新增 MQ 主题。 -->
    <div v-if="awaiting" class="node-awaiting">
      <span class="node-awaiting-text">{{ awaitingText }}</span>
      <span class="node-awaiting-actions">
        <button
          v-if="actions.wait"
          class="await-btn"
          :title="$t('chat.timeout_wait')"
          :disabled="arbitrating"
          @click.stop="onAwaitWait"
        >{{ $t('chat.timeout_wait') }}</button>
        <button
          v-if="actions.detach"
          class="await-btn"
          :title="$t('chat.timeout_detach')"
          :disabled="arbitrating"
          @click.stop="onAwaitDetach"
        >{{ $t('chat.timeout_detach') }}</button>
        <button
          v-if="actions.cancel"
          class="await-btn await-btn-danger"
          :title="$t('chat.tool_stop')"
          :disabled="arbitrating"
          @click.stop="onAwaitCancel"
        >{{ $t('chat.tool_stop') }}</button>
      </span>
    </div>

    <div v-if="node.expanded && children.length > 0" class="node-children">
      <SessionTreeNode
        v-for="child in childrenSorted"
        :key="child.node_id"
        :node="child"
        :depth="depth + 1"
        :active-id="activeId"
        :top-session="topSession"
        :highlight-id="highlightId"
      />
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, onUpdated, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { EventNames } from '../../events/event-names'
import { MsgTopics, FieldKeys, TaskBackgroundKeys } from '../../events/msgkeys'
import mq from '../../utils/mq'
import { message as uiMessage } from '../../components/ui'
import { useTaskView } from '../../composables/useTaskView'
import { useTaskStatus } from '../../composables/useTaskStatus'
import { resolveAwaiting, awaitingActionsFor } from '../../composables/useAwaitingArbitration'
import { isDslContainer, containerProgress, DSL_PARALLEL_KIND, DSL_JOB_KIND } from '../../utils/dslView'

const props = defineProps({
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  activeId: { type: String, default: '' },
  topSession: { type: String, default: '' },
  // 一次性高亮目标（定位后 ~1.2s 淡出）：node_id 命中则该行高亮（走 --accent-bg token）。
  highlightId: { type: String, default: '' },
})

// 定位高亮：命中 highlightId 的行加高亮底色（`.node-header` 已有 background 过渡 → 清除即淡出）
const highlighted = computed(() => !!props.highlightId && props.node.node_id === props.highlightId)

const { nodes, nodeRuntime, clearAwaiting } = useTaskView()

const { t } = useI18n()

// 节点时间戳：created_at 优先；缺失时回退 node_id（rt-* unix nano）与 seq
function nodeTime(n) {
  const t = n.created_at ? new Date(n.created_at).getTime() : NaN
  if (Number.isFinite(t) && t > 0) return t
  const m = /rt-(\d{10,19})/.exec(n.node_id || '')
  if (m) return Math.floor(Number(m[1]) / 1e6) // unix nano → ms
  return n.seq || 0
}

// 该节点的直接子级（tasktree parent_node_id 索引；按时间倒序，新任务排第一）
const children = computed(() => {
  const out = []
  for (const [, n] of nodes) {
    if (n.parent_node_id === props.node.node_id) out.push(n)
  }
  out.sort((a, b) => nodeTime(b) - nodeTime(a))
  return out
})

// 每层统一按时间倒序展示（新任务/新会话在最前，含 llm 节点下的新子任务）
const childrenSorted = computed(() => children.value)

const status = computed(() => props.node.status || 'idle')
// 已关闭（`closed` = 逻辑删除标记，**非** state；42 §2 (126)）：只读展示，不显示停止/裁决等动作
const isClosed = computed(() => props.node.closed === true)
const isRunning = computed(() => !isClosed.value && status.value === 'running')
const isLLMRunning = computed(() => props.node.kind === 'llm' && isRunning.value)

// 展示态/图标：统一走 useTaskStatus（I-94）。isRunning 仍按 status 判定（塌缩口径），
// 故 detached/awaiting 的 hover stop 既有逻辑不受影响；此处仅决定状态图标与 tooltip。
const { resolveTaskState, stateMetaFor } = useTaskStatus()
const taskState = computed(() => resolveTaskState(props.node))
const statusMeta = computed(() => stateMetaFor(taskState.value))

const kindIcon = computed(() => {
  switch (props.node.kind) {
    case 'llm':
      return 'icon-llm'
    case DSL_JOB_KIND:
      return 'list'
    case DSL_PARALLEL_KIND:
      return 'collection'
    default:
      if (isDslContainer(props.node)) return 'refresh' // LOOP
      return 'icon-other-tool'
  }
})

const kindColor = computed(() => {
  switch (props.node.kind) {
    case 'llm':
      return '#1890ff'
    case DSL_JOB_KIND:
    case DSL_PARALLEL_KIND:
      return '#722ed1'
    default:
      if (isDslContainer(props.node)) return '#722ed1'
      return '#8c8c8c'
  }
})

// 折叠容器进度徽标文案（LOOP/PARALLEL）：「第 N 轮 / 共 M 轮」；total 未知 → 「第 N 轮」。
// 非容器 / 后端字段缺失 → 空串（不渲染）。
const progressLabel = computed(() => {
  const p = containerProgress(props.node)
  if (!p) return ''
  const current = p.current == null ? 0 : p.current
  if (p.total == null) return t('taskView.dsl_progress_open', { current })
  return t('taskView.dsl_progress', { current, total: p.total })
})

const displayTitle = computed(() => props.node.title || '#' + (props.node.node_id || '?').slice(0, 8))

const headerTitle = computed(() => {
  const n = props.node
  const parts = []
  if (n.kind) parts.push(n.kind)
  if (n.title) parts.push(n.title)
  if (n.task_id && n.task_id !== n.node_id) parts.push(n.task_id)
  return parts.join(' · ')
})

// 点击行为：session 节点 → 会话内容（载荷 session_id）；task 节点 → 任务详情（载荷 task_id）
const selectEvent = computed(() =>
  props.node.node_type === 'session' ? EventNames.subsessionChanged : EventNames.taskDetailOpen)
// 节点主键 = task_id；session 节点的会话身份在 session_id 字段（subsession-changed 载荷仍为 session_id）
const selectPayload = computed(() =>
  props.node.node_type === 'session'
    ? { session_id: props.node.session_id || '' }
    : { task_id: props.node.node_id })

// hover stop 事件：task 节点 → 直接停止（服务端恒按子树级联，§〇.7 无弹框）；
// session（llm）节点 → 弹框三选（与 llm-stop 同一入口）
const stopEvent = computed(() =>
  props.node.node_type === 'task' ? EventNames.taskStop : EventNames.taskStopChoice)

// stop payload：task 节点 = {task_id}；session 节点 = {node_id}
const stopPayload = computed(() => {
  if (props.node.node_type !== 'task') return { node_id: props.node.node_id }
  return { task_id: props.node.node_id }
})

// ── 待裁决裁决条（I-103，见模板注释）──
// 运行态快照（data-tasktree-tasks → tasks）承载 awaiting{options,timeout_s}（I-99）。
const runtime = computed(() => nodeRuntime(props.node.node_id))
// 仅层展示态 == awaiting 且带非空 options 时渲染；明细优先取运行态、回落节点自身字段。
// 已关闭节点只读展示 → 不渲染裁决条（无操作入口，42 §2 (126)）。
const awaiting = computed(() =>
  isClosed.value ? null : resolveAwaiting(taskState.value, props.node, runtime.value))
// LLM tool-call id（task-background / mcp-tools-wait 的定位键）：节点行 → 运行态快照。
const toolCallId = computed(() =>
  props.node.tool_call_id || (runtime.value && runtime.value.tool_call_id) || '')
// 取消用的 server 任务节点 id（task-stop 直取，与任务树 ▍ 停止同一 id 空间）。
const nodeTaskId = computed(() => props.node.task_id || props.node.node_id)
const actions = computed(() => awaitingActionsFor(awaiting.value, !!toolCallId.value))
const arbitrating = ref(false) // 裁决请求进行中（防重复点击）

// 等待态提示：工具名 + 已等待时长（timeout_s 秒；≥60s 显示 m/s），与工具行同口径。
const awaitingText = computed(() => {
  const n = Number(awaiting.value && awaiting.value.timeout_s)
  let seconds
  if (!Number.isFinite(n)) seconds = ''
  else if (n < 60) seconds = Math.floor(n) + 's'
  else seconds = Math.floor(n / 60) + 'm ' + Math.floor(n % 60) + 's'
  return t('chat.timeout_arbitrate', { tool: props.node.title || 'tool', seconds })
})

// 等待完成（never）：发既有 mcp-tools-wait{tool_call_id}（撤销超时、继续等原调用返回）。
function onAwaitWait() {
  if (arbitrating.value) return
  const callId = toolCallId.value
  if (!callId) return
  mq.emit(EventNames.toolsWait, { [FieldKeys.tool_call_id]: callId })
  clearAwaiting(nodeTaskId.value)
}

// 转后台（manual）：发既有 task-background{tool_call_id}（与工具行「转后台」同一动作/入口）。
async function onAwaitDetach() {
  if (arbitrating.value) return
  const callId = toolCallId.value
  if (!callId) return
  arbitrating.value = true
  try {
    const res = await mq.emit(MsgTopics.taskBackground, { [FieldKeys.tool_call_id]: callId })
    const backend = res && res.backend
    const bgTaskId = backend && backend.result && backend.result[TaskBackgroundKeys.task_id]
    if (!backend || !backend.ok || !bgTaskId) {
      const errText = (backend && backend.errors && backend.errors[0]) || 'no task_id'
      uiMessage.error(t('chat.tool_background_failed', { error: errText }))
      return
    }
    clearAwaiting(nodeTaskId.value)
    uiMessage.success(t('chat.tool_background_ok'))
  } finally {
    arbitrating.value = false
  }
}

// 取消：统一走既有 task-stop{task_id}（与任务树 ▍ 停止同一入口；服务端恒按子树级联）。
async function onAwaitCancel() {
  if (arbitrating.value) return
  const tid = nodeTaskId.value
  if (!tid) return
  arbitrating.value = true
  try {
    const res = await mq.emit(EventNames.taskStop, { task_id: tid })
    const backend = res && res.backend
    const errs = backend && Array.isArray(backend.errors) ? backend.errors : []
    if (errs.length > 0 || (backend && backend.ok === false)) {
      uiMessage.error(t('chat.tool_stop_failed', { error: errs[0] || 'no ack' }))
      return
    }
    clearAwaiting(tid)
  } finally {
    arbitrating.value = false
  }
}

// llm 运行耗时：按 created_at 计算（1s 节拍刷新）。
// 禁止 watch props 派生量（项目硬规则）→ 计时器改由 onMounted / onUpdated 依 isLLMRunning
// 挂/卸：isLLMRunning 用于模板 v-if，其变化必触发重渲染 → onUpdated 同步计时器（与 watch
// 等价，但无隐式 props 依赖）。
const now = ref(Date.now())
let timer = null
const elapsedSec = computed(() => {
  if (!isLLMRunning.value) return 0
  const base = props.node.created_at
  if (!base) return 0
  const start = new Date(base).getTime()
  if (isNaN(start)) return 0
  return Math.max(0, Math.floor((now.value - start) / 1000))
})

// 依 isLLMRunning 挂/卸 1s 节拍计时器（幂等：无变化不动作）。
function syncElapsedTimer() {
  if (isLLMRunning.value && !timer) {
    timer = setInterval(() => { now.value = Date.now() }, 1000)
  } else if (!isLLMRunning.value && timer) {
    clearInterval(timer)
    timer = null
  }
}

function fmtElapsed(sec) {
  const n = Number(sec)
  if (!Number.isFinite(n)) return ''
  if (n < 60) return `${Math.floor(n)}s`
  const m = Math.floor(n / 60)
  return `${m}m ${Math.floor(n % 60)}s`
}

// 交互事件化：展开/折叠为消息（后端未来可据此监听会话树状态）
let unsubToggle = null

onMounted(() => {
  syncElapsedTimer()
  unsubToggle = mq.on(EventNames.sessionTreeNodeToggle, ({ node_id }) => {
    if (node_id === props.node.node_id) props.node.expanded = !props.node.expanded
  })
})

// isLLMRunning 变化 → 组件重渲染 → onUpdated 同步计时器（替代 watch props 派生量）。
onUpdated(syncElapsedTimer)

onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (unsubToggle) unsubToggle()
})
</script>

<style scoped>
.node-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: background var(--transition-fast);
  font-size: 13px;
}
.node-header:hover {
  background: var(--bg-hover);
}
.node-header.active {
  background: var(--bg-hover);
  border-left-color: var(--accent);
}
.expand-icon {
  flex-shrink: 0;
  cursor: pointer;
  color: var(--text-muted);
  width: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.leaf-spacer {
  display: inline-block;
  width: 16px;
  flex-shrink: 0;
}
.status-icon {
  flex-shrink: 0;
  font-size: 14px;
}
.status-icon.spinning {
  animation: spin 0.8s linear infinite;
}
/* awaiting（待用户裁决）：提示性脉冲，与 running 的转圈区分 */
.status-icon.pulsing {
  animation: status-pulse 1s ease-in-out infinite;
}
/* 状态/动作位：常态显示状态图标；仅 running 节点（has-action）行 hover 时切换为 stop 按钮 */
.status-wrap {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
}
.status-wrap .hover-act {
  display: none;
}
.node-header:hover .status-wrap.has-action .status-icon {
  display: none;
}
.node-header:hover .status-wrap.has-action .hover-act {
  display: inline-flex;
}
@keyframes spin {
  to { transform: rotate(360deg); }
}
@keyframes status-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
.node-title {
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}
.node-children {
  padding-left: 16px;
}
/* 已关闭节点（逻辑删除标记，42 §2 (126)）：灰色次级样式 + 「已关闭」标签，只读展示 */
.node-header.closed .node-title {
  color: var(--text-muted);
  font-weight: 400;
}
.node-header.closed .status-icon {
  opacity: 0.6;
}
.node-closed-tag {
  flex-shrink: 0;
  font-size: 10px;
  line-height: 1;
  padding: 2px 5px;
  border-radius: 4px;
  color: var(--text-muted);
  background: var(--bg-hover);
  border: 1px solid var(--border);
}
/* 折叠容器进度徽标（DSL-3 LOOP/PARALLEL）：只刷进度、不新增节点 */
.node-progress {
  flex-shrink: 0;
  font-size: 10px;
  line-height: 1;
  padding: 2px 5px;
  border-radius: 4px;
  color: var(--accent);
  background: var(--accent-bg);
  font-family: var(--font-mono, monospace);
  white-space: nowrap;
}
/* 待裁决裁决条（I-103）：任务区直接按 awaiting 渲染，无消息卡也能裁决 */
.node-awaiting {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 4px 8px 4px 36px;
  font-size: 12px;
  color: var(--warning);
  background: rgba(230, 162, 60, 0.1);
  border-left: 3px solid var(--warning);
}
.node-awaiting-text {
  flex: 1 1 140px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-awaiting-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}
.await-btn {
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-size: 11px;
  line-height: 1;
  padding: 3px 8px;
  cursor: pointer;
}
.await-btn:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accent);
}
.await-btn:disabled {
  opacity: 0.6;
  cursor: default;
}
.await-btn-danger {
  /* 危险语义走主题 token（原硬编码 #ff4d4f：11px 文字在按钮底 --bg-primary 上仅 3.10:1）。
     --danger 直用仅 4.30:1 → 向 --text-primary 混合 30% 压深（light 6.49 / dark 8.08 / nord 6.88，见 20-gui §12.8） */
  color: color-mix(in srgb, var(--danger) 70%, var(--text-primary));
  border-color: color-mix(in srgb, var(--danger) 40%, transparent);
}
.await-btn-danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--danger) 12%, transparent);
  color: color-mix(in srgb, var(--danger) 70%, var(--text-primary));
}
.llm-task-meta {
  flex-shrink: 0;
  font-size: 10px;
  color: var(--accent);
  font-family: var(--font-mono, monospace);
}
.node-close {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0;
}
.node-close:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.node-stop {
  /* 停止图标（11px 非文本图形，WCAG 1.4.11 ≥3:1）：原硬编码 #ff4d4f → 主题 --danger
     （light 4.30 / dark 7.08 / nord 5.65，见 20-gui §12.8） */
  color: var(--danger);
}
.node-stop:hover {
  background: color-mix(in srgb, var(--danger) 12%, transparent);
  color: var(--danger);
}
</style>
