<!--
  上下文压缩详情页（STB-1，preview 功能页 `status-compress`）。
  数据源 = 既有通知面 tool-notify（`useQueueStatus`，零新增主题）：当前状态 / 最近一次任务 / 真实状态时间线。
  没有队列条目数据 → **不伪造队列**，只展示收到过的 notice 时间线。
-->
<template>
  <TaskDetailLayout
    detail="compress"
    title-key="statusBar.detail.compressTitle"
    icon="document"
    :tone="tone"
    :state-text="stateText"
    :time="stampText(task && task.at)"
    :settings-tab="'context'"
    :error="task && task.state === 'error' ? task.error : ''"
  >
    <section class="td-block">
      <h4 class="td-h4">{{ $t('statusBar.detail.lastTask') }}</h4>
      <dl v-if="last" class="td-kv">
        <dt>{{ $t('statusBar.detail.state') }}</dt>
        <dd><span class="td-badge" :class="'is-' + lastTone">{{ stateTextOf(last.state) }}</span></dd>
        <dt>{{ $t('statusBar.detail.updatedAt') }}</dt>
        <dd>{{ stampText(last.at) || '—' }}</dd>
        <dt>{{ $t('statusBar.detail.startedAt') }}</dt>
        <dd>{{ stampText(last.startedAt) || '—' }}</dd>
        <dt v-if="last.session">{{ $t('statusBar.detail.session') }}</dt>
        <dd v-if="last.session">{{ last.session }}</dd>
        <dt v-if="last.turn">{{ $t('statusBar.detail.turn') }}</dt>
        <dd v-if="last.turn">{{ last.turn }}</dd>
        <dt v-if="last.error">{{ $t('statusBar.detail.error') }}</dt>
        <dd v-if="last.error" class="td-error-text">{{ last.error }}</dd>
      </dl>
      <div v-else class="td-empty">{{ $t('statusBar.detail.noRecord') }}</div>
    </section>

    <section class="td-block">
      <h4 class="td-h4">{{ $t('statusBar.detail.timeline') }}</h4>
      <ul v-if="timeline.length" class="td-timeline">
        <li v-for="(e, i) in timeline" :key="i" class="td-tl-item">
          <span class="td-time">{{ stampText(e.at) }}</span>
          <span class="td-badge" :class="'is-' + toneOf(e.state)">{{ stateTextOf(e.state) }}</span>
          <span v-if="e.message" class="td-tl-msg">{{ e.message }}</span>
        </li>
      </ul>
      <div v-else class="td-empty">{{ $t('statusBar.detail.noTimeline') }}</div>
    </section>
  </TaskDetailLayout>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import TaskDetailLayout from './TaskDetailLayout.vue'
import { useQueueStatus } from '../../composables/useQueueStatus'
import { stampText, taskStateKey, taskTone } from '../../utils/taskStatus'

const { t } = useI18n()
const { compressTask, compressLast, compressTimeline } = useQueueStatus()

const task = compressTask
const last = compressLast
const timeline = compressTimeline

const tone = computed(() => taskTone('compress', task.value, true))
const lastTone = computed(() => taskTone('compress', last.value, true))
const stateText = computed(() => t(taskStateKey('compress', task.value, true)))

function stateTextOf(state) {
  return t(taskStateKey('compress', { state }, true))
}
function toneOf(state) {
  return taskTone('compress', { state }, true)
}
</script>

<style scoped>
.td-block {
  margin-bottom: 14px;
}
.td-h4 {
  margin: 0 0 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
}
.td-kv {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 12px;
  margin: 0;
}
.td-kv dt {
  color: var(--text-muted);
}
.td-kv dd {
  margin: 0;
  word-break: break-word;
}
.td-timeline {
  list-style: none;
  margin: 0;
  padding: 0;
}
.td-tl-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
  border-bottom: 1px dashed var(--border);
}
.td-time {
  color: var(--text-muted);
  white-space: nowrap;
}
.td-tl-msg {
  color: var(--text-secondary);
  word-break: break-word;
}
.td-empty {
  color: var(--fg-disabled);
}
.td-badge {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  white-space: nowrap;
}
.td-badge.is-disabled { color: var(--fg-disabled); background: var(--bg-secondary); }
.td-badge.is-progress { color: var(--fg-secondary); background: var(--bg-secondary); }
.td-badge.is-ready { color: var(--text-primary); background: var(--bg-secondary); }
.td-badge.is-error { color: var(--fg-important); background: var(--bg-secondary); }
.td-error-text {
  color: var(--fg-important);
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
