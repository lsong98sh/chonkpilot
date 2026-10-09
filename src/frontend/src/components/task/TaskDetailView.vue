<template>
  <div class="task-detail">
    <template v-if="task || node">
      <div class="td-header">
        <span
          class="td-back"
          :title="$t('taskView.back_to_session')"
          v-mq:[EventNames.subsessionChanged].emit="{ session_id: backSessionId }"
        >
          <Icon name="arrow-left" :size="11" />
        </span>
        <Icon :name="iconName" :size="13" :color="iconColor" />
        <span class="td-status" :class="statusClass"></span>
        <span class="td-title">{{ displayName }}</span>
        <span v-if="isClosed" class="td-closed-tag">{{ $t('taskView.closed_label') }}</span>
        <span class="td-meta">
          <span v-if="task?.pid" class="td-pid">PID {{ task.pid }}</span>
          <span v-if="task?.elapsed != null" class="td-elapsed">{{ fmtElapsed(task.elapsed) }}</span>
        </span>
      </div>

      <div class="td-body">
        <!-- 工具节点：参数为主（输出非重点） -->
        <div v-if="task?.error" class="td-error">{{ task.error }}</div>
        <div v-if="hasArgs" class="td-args">
          <div class="td-section-title">{{ $t('taskView.args') }}</div>
          <pre class="td-args-pre">{{ argsText }}</pre>
        </div>
        <div v-if="task?.output" class="td-output" ref="outRef">{{ task.output }}</div>
        <div v-else-if="!hasArgs" class="td-note">{{ $t('taskView.no_output') }}</div>
      </div>

      <div class="td-footer">
        <!-- 已关闭节点（42 §2 (126)）：只读展示，不提供停止/关闭/恢复等动作 -->
        <span v-if="isClosed" class="td-closed-hint">{{ $t('taskView.closed_readonly_hint') }}</span>
        <template v-else>
          <Button
            v-if="isRunning"
            size="small"
            type="danger"
            v-mq:[EventNames.taskStop].emit="stopPayload"
          >
            {{ $t('taskView.stop_task') }}
          </Button>
          <Button v-else size="small" v-mq:[EventNames.taskClose].emit="{ node_id: nodeId }">
            {{ $t('taskView.close_task') }}
          </Button>
        </template>
      </div>
    </template>
    <div v-else class="td-empty">
      <p>{{ $t('taskView.task_not_found') }}</p>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../icon/Icon.vue'
import { Button } from '../ui'
import { EventNames } from '../../events/event-names'
import { useTaskView } from '../../composables/useTaskView'
import mq from '../../utils/mq'

defineOptions({ name: 'TaskDetailView' })

const { t } = useI18n()

const props = defineProps({
  taskId: { type: String, default: '' },
})

const { tasks, getNode } = useTaskView()

// 双源：tasktree 节点（层级/kind，事实源）+ TaskInfo 运行态（输出/进程/elapsed）
// server tasks.* 载荷字段映射：kind 替代旧 type（21-llm-server）
const node = computed(() => (props.taskId ? getNode(props.taskId) : null))
const task = computed(() => (props.taskId ? tasks.get(props.taskId) : null))

const nodeId = computed(() => node.value?.node_id || props.taskId)

const kind = computed(() => node.value?.kind || task.value?.kind || task.value?.type || 'tool')
// 已关闭（`closed` = 逻辑删除标记，非 state；42 §2 (126)）：只读展示 → 不显示停止/关闭动作
const isClosed = computed(() => node.value?.closed === true || task.value?.closed === true)
const isRunning = computed(
  () => !isClosed.value
    && (node.value?.status === 'running' || task.value?.status === 'running'))

const backSessionId = computed(() => node.value?.session_id || task.value?.session_id || '')

const hasArgs = computed(() => {
  const a = task.value?.args
  return !!a && typeof a === 'object' && Object.keys(a).length > 0
})

const argsText = computed(() => {
  if (!hasArgs.value) return ''
  try {
    const s = JSON.stringify(task.value.args, null, 2)
    // 截断后缀走 i18n（taskView.truncated_suffix），避免 zh 界面混英文（E-44）
    return s.length > 4000 ? s.slice(0, 4000) + t('taskView.truncated_suffix') : s
  } catch (e) {
    return String(task.value.args)
  }
})

const iconName = computed(() => {
  switch (kind.value) {
    case 'llm':
      return 'think'
    default:
      return 'tool'
  }
})

const iconColor = computed(() => {
  switch (kind.value) {
    case 'llm':
      return '#1890ff'
    default:
      return '#8c8c8c'
  }
})

const displayName = computed(() =>
  node.value?.title || task.value?.purpose || task.value?.name || task.value?.tool_name || props.taskId || '')

const status = computed(() => node.value?.status || task.value?.status || 'idle')

const statusClass = computed(() => {
  switch (status.value) {
    case 'running':
      return 'is-running'
    case 'done':
      return 'is-done'
    case 'error':
      return 'is-error'
    default:
      return 'is-stopped'
  }
})

// 终止：服务端恒按子树级联（§〇.7）
const stopPayload = computed(() => ({ task_id: nodeId.value }))

const outRef = ref(null)

// 运行中输出自动跟随尾部（tasks.updated 事件每秒更新 output；事件驱动替代 watch store）
let unsubTaskUpdated = null
onMounted(() => {
  unsubTaskUpdated = mq.on(EventNames.taskUpdated, (payload) => {
    if (!payload || payload.task_id !== props.taskId) return
    nextTick(() => {
      if (outRef.value && (payload.status || payload.state) === 'running') {
        outRef.value.scrollTop = outRef.value.scrollHeight
      }
    })
  })
})
onUnmounted(() => {
  if (unsubTaskUpdated) unsubTaskUpdated()
})

function fmtElapsed(sec) {
  const n = Number(sec)
  if (!Number.isFinite(n)) return ''
  if (n < 60) return `${Math.floor(n)}s`
  const m = Math.floor(n / 60)
  return `${m}m ${Math.floor(n % 60)}s`
}
</script>

<style scoped>
.task-detail {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.td-header {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 3px 8px;
  font-size: 12px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.td-back {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 4px;
  color: var(--text-muted);
  cursor: pointer;
}
.td-back:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.td-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
  color: var(--text-primary);
}
.td-status {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.td-status.is-running {
  background: var(--accent);
  animation: td-pulse 1s ease-in-out infinite;
}
.td-status.is-done {
  /* 状态圆点（7px 装饰）走主题 token：原硬编码 #52c41a / #f5222d / #bfbfbf
     在 dark/nord 下不随主题（与 .td-error 同批口径，见 20-gui §12.8） */
  background: var(--success);
}
.td-status.is-error {
  background: var(--danger);
}
.td-status.is-stopped {
  background: var(--text-muted);
}
@keyframes td-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}
.td-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  font-size: 10px;
  color: var(--text-muted);
  font-family: var(--font-mono, monospace);
}
.td-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.td-error {
  font-size: 12px;
  /* 危险语义走主题 token（原硬编码 #f5222d；nord 的 --danger 已是提亮过的 #f0959e）。
     纯 --danger 作 12px 文字在浅色底/浅色淡底上仅 3.8~3.9:1 → 向 --text-primary 混合 30%
     压深（light 5.9:1 / dark 6.5:1 / nord 4.6:1，见 20-gui §12.8），底色仍用 --danger 淡底示意 */
  color: color-mix(in srgb, var(--danger) 70%, var(--text-primary));
  background: color-mix(in srgb, var(--danger) 12%, transparent);
  border-radius: 4px;
  padding: 6px 8px;
  white-space: pre-wrap;
}
.td-output {
  flex: 1;
  min-height: 80px;
  overflow-y: auto;
  font-family: var(--font-mono, monospace);
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-primary);
  background: var(--bg-tertiary);
  border-radius: 4px;
  padding: 8px;
  white-space: pre-wrap;
  word-break: break-all;
}
.td-note {
  font-size: 12px;
  color: var(--text-muted);
  padding: 6px 2px;
}
.td-section-title {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 4px;
}
.td-args-pre {
  margin: 0;
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  line-height: 1.5;
  color: var(--text-secondary);
  background: var(--bg-tertiary);
  border-radius: 4px;
  padding: 8px;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 240px;
  overflow-y: auto;
}
.td-footer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-top: 1px solid var(--border);
  flex-shrink: 0;
}
/* 已关闭（逻辑删除标记，42 §2 (126)）：灰色「已关闭」标签 + 只读提示（无动作按钮） */
.td-closed-tag {
  flex-shrink: 0;
  font-size: 10px;
  line-height: 1;
  padding: 2px 5px;
  border-radius: 4px;
  color: var(--text-muted);
  background: var(--bg-hover);
  border: 1px solid var(--border);
}
.td-closed-hint {
  font-size: 12px;
  color: var(--text-muted);
}
.td-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-muted);
  font-size: 13px;
}
</style>
