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
           running（含塌缩为 running 的 detached）hover 切换 stop，非 running 仅状态图标 -->
      <span class="status-wrap" :class="{ 'has-action': isRunning }">
        <Icon
          v-if="statusMeta.icon"
          class="status-icon"
          :class="{ spinning: statusMeta.spin }"
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


    <div v-if="node.expanded && children.length > 0" class="node-children">
      <SessionTreeNode
        v-for="child in children"
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
import { computed, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@ckfw/components/icon/Icon.vue'
import { EventNames } from '../../events/event-names'
import { useTaskView } from '../../composables/useTaskView'
import { useTaskStatus } from '../../composables/useTaskStatus'
import { useSharedNow } from '../../composables/useSharedNow'
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

const { childrenOf } = useTaskView()

const { t } = useI18n()

// 该节点的直接子级：走 useTaskView 共享索引（O(N) 建一次，所有节点复用；
// 时间倒序，新任务排第一）——不再为每个节点扫全量 nodes。
const children = computed(() => childrenOf(props.node.node_id))

const status = computed(() => props.node.status || 'idle')
// 已关闭（`closed` = 逻辑删除标记，**非** state；42 §2 (126)）：只读展示，不显示停止/裁决等动作
const isClosed = computed(() => props.node.closed === true)
const isRunning = computed(() => !isClosed.value && status.value === 'running')
const isLLMRunning = computed(() => props.node.kind === 'llm' && isRunning.value)

// 展示态/图标：统一走 useTaskStatus（I-94）。isRunning 仍按 status 判定（塌缩口径），
// 故 detached 的 hover stop 既有逻辑不受影响；此处仅决定状态图标与 tooltip。
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

// llm 运行耗时：按 created_at 计算，1s 节拍来自**全局共享时钟**（useSharedNow）。
// 长会话性能：不再每个 running 节点各挂一个 1s 定时器；节点只按 isLLMRunning
// retain/release 引用计数，全应用仅 1 个 1s 定时器（无运行节点时不跑）。
const { now, retain, release } = useSharedNow()
let clockHeld = false
const elapsedSec = computed(() => {
  if (!isLLMRunning.value) return 0
  const base = props.node.created_at
  if (!base) return 0
  const start = new Date(base).getTime()
  if (isNaN(start)) return 0
  return Math.max(0, Math.floor((now.value - start) / 1000))
})

// 本地 held 标志保证同一节点重复 retain/release 不成对调用（不误停别人的时钟）。
function setClockHeld(on) {
  if (on && !clockHeld) {
    clockHeld = true
    retain()
  } else if (!on && clockHeld) {
    clockHeld = false
    release()
  }
}

watch(isLLMRunning, (on) => setClockHeld(!!on), { immediate: true })

function fmtElapsed(sec) {
  const n = Number(sec)
  if (!Number.isFinite(n)) return ''
  if (n < 60) return `${Math.floor(n)}s`
  const m = Math.floor(n / 60)
  return `${m}m ${Math.floor(n % 60)}s`
}

// 交互事件化：展开/折叠为消息（后端未来可据此监听会话树状态）。
// 节点对象归 useTaskView store 持有 → 折叠翻转由 store 订阅 session-tree-node-toggle 完成
// （本组件不直改 props.node.expanded；E-08）。
onUnmounted(() => {
  if (clockHeld) {
    clockHeld = false
    release()
  }
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
