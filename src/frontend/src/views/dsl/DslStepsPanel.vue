<template>
  <div class="dsl-panel">
    <!-- 作业头：标题 + 状态 + 容器进度 + $RETURN（两态） -->
    <div class="dsl-header">
      <Icon name="list" :size="13" color="var(--accent)" />
      <span class="dsl-title">{{ jobTitle }}</span>
      <Icon
        v-if="statusMeta.icon"
        class="dsl-status-icon"
        :class="{ spinning: statusMeta.spin, pulsing: statusMeta.pulse }"
        :size="13"
        :color="statusMeta.color"
        :name="statusMeta.icon"
        :title="statusMeta.label ? $t(statusMeta.label) : ''"
      />
      <div class="dsl-header-spacer" />
      <span v-if="steps.length > 0" class="dsl-count">{{ steps.length }}</span>
    </div>

    <!-- $RETURN 结果通道（[42 §2 (249)]）：inline 文本 / file 文件名 + 大小；无 → 不渲染 -->
    <div v-if="ret" class="dsl-return">
      <span class="dsl-return-label">{{ $t('taskView.dsl_return_label') }}</span>
      <template v-if="ret.kind === 'file'">
        <Icon name="document" :size="11" color="var(--text-muted)" />
        <span class="dsl-return-file">{{ $t('taskView.dsl_return_file', { name: ret.name, size: fmtSize(ret.size) }) }}</span>
      </template>
      <pre v-else class="dsl-return-text">{{ ret.text }}</pre>
    </div>

    <!-- 步骤表格：列 No / 状态 / 目的 / 耗时 / 时间 + 操作列（查看 / 新窗口 / 取消） -->
    <div class="dsl-table-wrap">
      <Table :columns="columns" :data="steps" :empty-text="$t('taskView.dsl_no_steps')">
        <template #status="{ row }">
          <span class="dsl-cell-status">
            <Icon
              v-if="rowMeta(row).icon"
              :size="12"
              :color="rowMeta(row).color"
              :class="{ spinning: rowMeta(row).spin }"
              :name="rowMeta(row).icon"
            />
            <span class="dsl-cell-status-text">{{ rowMeta(row).label ? $t(rowMeta(row).label) : row.status }}</span>
          </span>
        </template>
        <template #elapsed="{ row }">{{ fmtElapsedMs(row.elapsedMs) }}</template>
        <template #time="{ row }">{{ fmtTime(row.createdAt) }}</template>
        <template #purpose="{ row }">
          <span class="dsl-cell-purpose" :title="row.purpose">{{ row.purpose }}</span>
        </template>
        <template #action="{ row }">
          <Button size="mini" @click.stop="selectRow(row)">
            {{ $t('taskView.dsl_action_view') }}
          </Button>
          <Button size="mini" :disabled="!row.sessionId" @click.stop="openWindow(row)">
            {{ $t('taskView.dsl_action_new_window') }}
          </Button>
          <Button
            v-if="isJobRunning"
            size="mini"
            type="danger"
            :loading="cancelling"
            :title="$t('taskView.dsl_action_cancel_title')"
            @click.stop="onCancelJob"
          >
            {{ $t('taskView.dsl_action_cancel') }}
          </Button>
        </template>
      </Table>
    </div>

    <!-- preview 区：只读显示选中步骤（轮次）的完整 chat 内容（无 chat 输入） -->
    <div class="dsl-preview">
      <div class="dsl-preview-head">
        <Icon name="chat-dot-square" :size="12" />
        <span>{{ $t('taskView.dsl_preview_title') }}</span>
        <Tag v-if="selectedStep" size="small" type="info">No.{{ selectedStep.no }}</Tag>
      </div>
      <div class="dsl-preview-body">
        <MessageList
          v-if="selectedStep && selectedStep.sessionId"
          :key="selectedStep.sessionId"
          :session-id="selectedStep.sessionId"
          :viewer="true"
          direction="bottom"
        />
        <div v-else class="dsl-preview-empty">
          {{ selectedStep ? $t('taskView.dsl_no_session') : $t('taskView.dsl_preview_empty') }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { Button, Table, Tag } from '../../components/ui'
import MessageList from '../chat/MessageList.vue'
import { useTaskView } from '../../composables/useTaskView'
import { useTaskStatus } from '../../composables/useTaskStatus'
import { useDslView } from '../../composables/useDslView'
import { useChatWindows } from '../../composables/useChatWindows'
import { fmtElapsedMs, fmtTime } from '../../utils/dslView'

const props = defineProps({
  /** DSL 作业根节点 id（= 任务树节点 task_id）。 */
  jobId: { type: String, required: true },
})

const { t } = useI18n()
const { getNode } = useTaskView()
const { stepsOf, returnOf, cancelJob } = useDslView()
const { resolveTaskState, stateMetaFor } = useTaskStatus()
// 新窗口复用主 chat 的多窗口机制（gui.window.open-chat，幂等：已开即激活）
const { openSessionChatWindow, refreshWindows } = useChatWindows()
onMounted(() => { refreshWindows() })

// 作业节点 / 步骤行 / $RETURN 两态（响应式读取 tasktree 快照）
const jobNode = computed(() => (props.jobId ? getNode(props.jobId) : null))
const steps = computed(() => stepsOf(props.jobId))
const ret = computed(() => returnOf(props.jobId))

const jobTitle = computed(() => (jobNode.value && jobNode.value.title) || props.jobId || '')

const jobState = computed(() => (jobNode.value ? resolveTaskState(jobNode.value) : 'idle'))
const statusMeta = computed(() => stateMetaFor(jobState.value))
// 取消 = 作业级（单一动作）；仅作业运行中可取消（终态不可取消）
const isJobRunning = computed(() => jobState.value === 'running')
const cancelling = ref(false)

// 表格列（列名走 i18n；操作列由 #action 具名插槽渲染）
const columns = computed(() => [
  { prop: 'no', label: t('taskView.dsl_col_no'), width: 52, align: 'right' },
  { prop: 'status', label: t('taskView.dsl_col_status'), width: 92 },
  { prop: 'purpose', label: t('taskView.dsl_col_purpose') },
  { prop: 'elapsed', label: t('taskView.dsl_col_elapsed'), width: 76 },
  { prop: 'time', label: t('taskView.dsl_col_time'), width: 84 },
  { type: 'action', label: t('taskView.dsl_col_actions'), width: 200 },
])

// 步骤状态 → 图标/配色/文案：复用任务树唯一映射（未知态回落 idle = 不渲染图标，回落原状态文本）
function rowMeta(row) {
  return stateMetaFor(resolveTaskState({ status: row.status }))
}

// 查看：选中该行 → preview 区只读显示该轮 chat（同一动作也用于行点击）
const selectedNo = ref(null)
const selectedStep = computed(() => steps.value.find((s) => s.no === selectedNo.value) || null)
function selectRow(row) {
  selectedNo.value = row.no
}

// 新窗口：复用主 chat 多窗口（`gui.window.open-chat {session_id}`，幂等：已开即激活该对话窗口）。
function openWindow(row) {
  if (!row.sessionId) return
  openSessionChatWindow(row.sessionId).catch((e) => {
    console.warn('[DslStepsPanel] gui.window.open-chat failed:', e)
  })
}

// 作业级取消（作业终止；服务端按子树级联）。防重复点击。
function onCancelJob() {
  if (cancelling.value) return
  cancelling.value = true
  cancelJob(props.jobId)
  // 终态由 tasks.* 事件驱动（isJobRunning 转 false → 按钮消失）；短延时复位防连点
  setTimeout(() => { cancelling.value = false }, 600)
}

// 字节数格式化（$RETURN file 两态的 size）
function fmtSize(n) {
  const v = Number(n)
  if (!Number.isFinite(v) || v < 0) return ''
  if (v < 1024) return `${v} B`
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`
  return `${(v / (1024 * 1024)).toFixed(1)} MB`
}
</script>

<style scoped>
.dsl-panel {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.dsl-header {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 8px;
  min-height: 28px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.dsl-title {
  font-weight: 600;
  font-size: 13px;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dsl-status-icon {
  flex-shrink: 0;
}
.dsl-status-icon.spinning {
  animation: dsl-spin 0.8s linear infinite;
}
.dsl-status-icon.pulsing {
  animation: dsl-pulse 1s ease-in-out infinite;
}
@keyframes dsl-spin {
  to { transform: rotate(360deg); }
}
@keyframes dsl-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
.dsl-header-spacer {
  flex: 1;
  min-width: 0;
}
.dsl-count {
  flex-shrink: 0;
  font-size: 10px;
  color: var(--text-muted);
  font-family: var(--font-mono, monospace);
}
.dsl-return {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 4px 8px;
  font-size: 12px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}
.dsl-return-label {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.dsl-return-file {
  color: var(--text-secondary);
  font-family: var(--font-mono, monospace);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dsl-return-text {
  margin: 0;
  flex-basis: 100%;
  max-height: 160px;
  overflow: auto;
  font-family: var(--font-mono, monospace);
  font-size: 11px;
  line-height: 1.5;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border-radius: 4px;
  padding: 6px 8px;
  white-space: pre-wrap;
  word-break: break-all;
}
.dsl-table-wrap {
  flex-shrink: 0;
  max-height: 55%;
  overflow: auto;
}
.dsl-cell-status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.dsl-cell-status-text {
  color: var(--text-secondary);
  font-size: 12px;
}
.dsl-cell-purpose {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dsl-preview {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-top: 1px solid var(--border);
}
.dsl-preview-head {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--text-muted);
  text-transform: uppercase;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}
.dsl-preview-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.dsl-preview-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-muted);
  font-size: 13px;
}
</style>
