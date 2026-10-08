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
import { ref, computed, onMounted, onUnmounted } from 'vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import Icon from '../../components/icon/Icon.vue'
import { Tag } from '../../components/ui'
import MessageList from '../chat/MessageList.vue'
import TaskDetailView from '../../components/task/TaskDetailView.vue'
import DslStepsPanel from '../dsl/DslStepsPanel.vue'
import { useTaskView } from '../../composables/useTaskView'
import { useDslView } from '../../composables/useDslView'

const sessionId = ref(null)
// 任务详情模式：点击会话树任务行后右侧显示任务详情（非会话消息）
const taskId = ref(null)

// DSL 作业：任务是 DSL 作业根 → 右侧改渲染步骤表格（DslStepsPanel）
const { isJobNode } = useDslView()
const dslJobId = computed(() => (taskId.value && isJobNode(taskId.value) ? taskId.value : ''))

// ── TaskView：会话树增量刷新（子会话切换时补全初始状态）──
const { refresh: refreshTasks } = useTaskView()

function onSubSessionChanged({ session_id: newId }) {
  sessionId.value = newId || null
  taskId.value = null // 返回会话模式：清掉任务详情
  refreshTasks(sessionId.value || '', '', { initOnly: true })
}

function onTaskDetailOpen({ task_id }) {
  taskId.value = task_id || null
}

onMounted(() => {
  mq.on(EventNames.subsessionChanged, onSubSessionChanged)
  mq.on(EventNames.taskDetailOpen, onTaskDetailOpen)
})

onUnmounted(() => {
  mq.off(EventNames.subsessionChanged, onSubSessionChanged)
  mq.off(EventNames.taskDetailOpen, onTaskDetailOpen)
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
