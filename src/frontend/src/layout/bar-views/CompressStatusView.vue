<!--
  栏视图条目 `compress-status`（STB-1）：上下文压缩**长任务状态 view**。
  数据源 = 既有通知面 tool-notify（`useQueueStatus`，零新增主题）。
  空闲仍显示（中性色），排队 / 进行中 / 完成 / 失败给状态文案；
  点击 → 统一命令 `open-view{status-compress}`（落 preview 详情页）。
-->
<template>
  <div
    class="sb-section sb-task"
    :class="'is-' + model.tone"
    data-task="compress"
    :title="model.title"
    @click="open"
  >
    <Icon name="document" :size="12" class="sb-task-icon" />
    <span class="sb-task-state">{{ model.text }}</span>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { useQueueStatus } from '../../composables/useQueueStatus'
import { taskEntry } from '../../utils/taskStatus'
import { openStatusDetail } from '../statusDetail.js'
import { EventNames } from '../../events/event-names'
import mq from '../../utils/mq'

const { t } = useI18n()
const { compressTask, handleNotice } = useQueueStatus()

const model = computed(() => taskEntry({ kind: 'compress', task: compressTask.value, enabled: true, t }))

function open() {
  openStatusDetail('compress')
}

const _unsubs = []
onMounted(() => {
  _unsubs.push(mq.on(EventNames.toolNotify, (d) => handleNotice(d, 'compress')))
})
onUnmounted(() => {
  _unsubs.forEach((fn) => fn())
  _unsubs.length = 0
})
</script>

<style scoped>
.sb-section {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  padding: 0 4px;
  border-radius: 3px;
  height: 20px;
  white-space: nowrap;
  flex-shrink: 0;
  -webkit-app-region: no-drag;
}
.sb-section:hover {
  background: var(--bg-hover, #e8e8e8);
}
.sb-task-icon {
  flex-shrink: 0;
}
.sb-task-state {
  font-size: 12px;
}
.sb-task.is-disabled {
  color: var(--fg-disabled);
}
.sb-task.is-progress {
  color: var(--fg-secondary);
}
.sb-task.is-ready {
  color: var(--text-primary);
}
.sb-task.is-error {
  color: var(--fg-important);
}
</style>
