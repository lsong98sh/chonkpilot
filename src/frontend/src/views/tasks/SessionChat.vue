<template>
  <div class="panel-inner">
    <div class="panel-header">
      <Icon name="chat-dot-square" />
      <span>{{ $t('common.session_detail') }}</span>
      <template v-if="sessionId">
        <Tag size="small" type="info" class="session-id-tag">#{{ sessionId.slice(0, 8) }}</Tag>
      </template>
      <div class="header-spacer" />
      <!-- TASK-008：仅任务派生（tasks.* 有运行态快照）的子会话显示；普通聊天子会话无快照不显示 -->
      <Button
        v-if="runningTurn"
        size="small"
        type="danger"
        @click="stopSession"
      >
        {{ $t('taskView.stop_session') }}
      </Button>
    </div>
    <div class="session-chat">
      <!-- DSL 作业（DSL-3）：右侧面板 = 步骤表格 + preview，不是「任务详情/标题」 -->
      <DslStepsPanel v-if="dslJobId" :key="dslJobId" :job-id="dslJobId" />
      <TaskDetailView v-else-if="taskId" :task-id="taskId" />
      <div v-else-if="!sessionId" class="empty-prompt">
        <p>{{ $t('chat.select_subsession') }}</p>
      </div>
      <template v-else>
        <!-- 子会话只读对话：turn 导航按 CHAT-013-S06 / TASK-011-S03 置于**下侧**（组件 direction='bottom'；主 chat = 右） -->
        <MessageList :session-id="sessionId" :top-session-id="topSessionId" :viewer="true" direction="bottom" />
      </template>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import { storeToRefs } from 'pinia'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../../events/event-names'
import Icon from '@ckfw/components/icon/Icon.vue'
import { Button, Tag } from '@ckfw/components/ui/index.js'
import MessageList from '../chat/MessageList.vue'
import TaskDetailView from '../../components/task/TaskDetailView.vue'
import DslStepsPanel from '../dsl/DslStepsPanel.vue'
import { useTaskView } from '../../composables/useTaskView'
import { useDslView } from '../../composables/useDslView'
import { useSelectionStore } from '../../stores/selection'

// 任务区当前选中项 = **跨 view 共享状态**（唯一真相源 = selection store，UILAYOUT-1 §3.7 P2③）：
//   - `sessionId` ← `subsession-changed`；`taskId` ← `task-detail-open`（叠加态，选中子会话即清）。
// 本组件只读（storeToRefs 派生）——原组件内 sessionId / taskId 两个 ref 已删（双源已消除）；
// 写入 = 既有订阅者（stores/selectionSubscribers.js）。
const selection = useSelectionStore()
const { activeTask, activeSession } = storeToRefs(selection)
const sessionId = computed(() => activeTask.value.sessionId || null)
const taskId = computed(() => activeTask.value.taskId || null)

// 一树一库：任务详情 viewer 读的是**当前主会话的对话库**（子会话行与主会话同库）。
// `data-session-list` 只列主会话 → 子会话的 dialog_id 无从查得；这里只给主会话 id，
// dialog_id 由 MessageList 在加载时经 `sessionDialogOf` 现取（备忘可能在渲染后才就绪）。
const topSessionId = computed(() => activeSession.value.sessionId || '')

// DSL 作业：任务是 DSL 作业根 → 右侧改渲染步骤表格（DslStepsPanel）
const { isJobNode } = useDslView()
const dslJobId = computed(() => (taskId.value && isJobNode(taskId.value) ? taskId.value : ''))

// ── TaskView：会话树增量刷新（子会话切换时补全初始状态）──
const { refresh: refreshTasks, activeTasks, confirmTurnCancel } = useTaskView()

// TASK-008：当前子会话运行中的轮次。优先取带 turn_id 的任务（实时 tasks.* 事件携带）；
// data-tasktree-tasks 快照行无 turn_id → 冷启动回落 llm 节点，由 server 按 session
// 解析运行中轮次（onLLMCancel 的 turn 缺省回退）。只读响应式 tasks Map，不做 watch。
const runningTurn = computed(() => {
  if (!sessionId.value) return null
  const running = activeTasks(sessionId.value)
  return running.find((t) => !!t.turn_id) || running.find((t) => (t.kind || t.type) === 'llm') || null
})

// 终止会话 = 现有 llm-cancel{session,turn}：停当前轮并级联取消在飞工具（零新增 MQ 主题）。
function stopSession() {
  const t = runningTurn.value
  if (!t) return
  confirmTurnCancel({ sessionId: sessionId.value, turnId: t.turn_id || '' })
}

// 选中子会话（payload = session_id）：store 写入由订阅者承担，本组件只补任务快照。
function onSubSessionChanged({ session_id: newId }) {
  refreshTasks(newId || '', '', { initOnly: true })
}

onMounted(() => {
  mq.on(EventNames.subsessionChanged, onSubSessionChanged)
})

onUnmounted(() => {
  mq.off(EventNames.subsessionChanged, onSubSessionChanged)
})
</script>

<style scoped>
.session-chat {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.empty-prompt {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-muted);
  font-size: 13px;
}
.panel-inner {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.panel-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  min-height: 28px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.8px;
  color: var(--text-muted);
  text-transform: uppercase;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}
.header-spacer {
  flex: 1;
  min-width: 0;
}
.session-id-tag {
  font-weight: 500;
  text-transform: none;
  letter-spacing: 0;
}
</style>
