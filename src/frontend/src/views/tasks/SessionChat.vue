<template>
  <div class="panel-inner">
    <div class="panel-header">
      <Icon name="chat-dot-square" />
      <span>{{ $t('common.session_detail') }}</span>
      <template v-if="sessionId">
        <Tag size="small" type="info" class="session-id-tag">#{{ sessionId.slice(0, 8) }}</Tag>
      </template>
      <div class="header-spacer" />
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
        <MessageList :session-id="sessionId" :viewer="true" direction="bottom" />
      </template>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import { storeToRefs } from 'pinia'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import Icon from '../../components/icon/Icon.vue'
import { Tag } from '../../components/ui'
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
const { activeTask } = storeToRefs(selection)
const sessionId = computed(() => activeTask.value.sessionId || null)
const taskId = computed(() => activeTask.value.taskId || null)

// DSL 作业：任务是 DSL 作业根 → 右侧改渲染步骤表格（DslStepsPanel）
const { isJobNode } = useDslView()
const dslJobId = computed(() => (taskId.value && isJobNode(taskId.value) ? taskId.value : ''))

// ── TaskView：会话树增量刷新（子会话切换时补全初始状态）──
const { refresh: refreshTasks } = useTaskView()

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
