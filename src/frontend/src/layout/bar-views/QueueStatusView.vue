<!--
  栏视图条目 `queue-status`（UILAYOUT-1 §7 P4① / 裁决 2）：记忆沉淀 / 上下文压缩**队列状态**（I-128）。
  从 `views/statusbar/StatusBar.vue` 平移（实时值 → 栏视图条目）；数据源 = 既有通知面 tool-notify。
  空闲不渲染；完成态保持数秒后自动隐藏（`useQueueStatus` 内部定时）。
-->
<template>
  <div v-if="memoryQueue || compressQueue" class="sb-section sb-queue">
    <span v-if="memoryQueue" class="sb-q" :class="'is-' + memoryQueue.state">{{ queueText('memory', memoryQueue.state) }}</span>
    <span v-if="compressQueue" class="sb-q" :class="'is-' + compressQueue.state">{{ queueText('compress', compressQueue.state) }}</span>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { EventNames } from '../../events/event-names'
import { useQueueStatus } from '../../composables/useQueueStatus'
import mq from '../../utils/mq'

const { t } = useI18n()
const { memoryQueue, compressQueue, handleNotice: handleQueueNotice } = useQueueStatus()

function queueText(kind, state) {
  return t(`statusBar.queue_${kind}_${state}`)
}

const _unsubs = []
onMounted(() => {
  _unsubs.push(mq.on(EventNames.toolNotify, handleQueueNotice))
})
onUnmounted(() => { _unsubs.forEach((fn) => fn()); _unsubs.length = 0 })
</script>

<style scoped>
.sb-section {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 4px;
  border-radius: 3px;
  height: 20px;
  -webkit-app-region: no-drag;
}
.sb-queue {
  gap: 8px;
  color: var(--text-secondary);
  white-space: nowrap;
  flex-shrink: 0;
}
.sb-q {
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
}
.sb-q.is-queued { color: var(--fg-disabled); }
.sb-q.is-running { color: var(--fg-secondary); }
.sb-q.is-done { color: var(--text-primary); }
</style>
