<template>
  <!-- filetree 区双栈容器：头部「项目 / 知识库 / 会话」切换（v-show 同显三树，切换不销毁状态）。
       本分段 = 知识库视图入口（toolbar「知识库」按钮 2026-09-16 已移除）；
       「会话」= 原会话抽屉内容迁入（P3-C1，2026-09-24）；
       filetreeModeToggle 订阅保留为休眠态（无 UI 入口触发，供 mq 直发/回归用例驱动）。 -->
  <div class="explorer-pane">
    <div class="explorer-seg">
      <span
        class="explorer-seg-btn"
        :class="{ active: mode === 'project' }"
        :title="t('fileTree.mode_project')"
        v-mq:[EventNames.filetreeModeSelect].click="{ mode: 'project' }"
      >{{ t('fileTree.mode_project') }}</span>
      <span
        class="explorer-seg-btn"
        :class="{ active: mode === 'knowledge' }"
        :title="t('fileTree.mode_knowledge')"
        v-mq:[EventNames.filetreeModeSelect].click="{ mode: 'knowledge' }"
      >{{ t('fileTree.mode_knowledge') }}</span>
      <span
        class="explorer-seg-btn"
        :class="{ active: mode === 'sessions' }"
        :title="t('fileTree.mode_sessions')"
        v-mq:[EventNames.filetreeModeSelect].click="{ mode: 'sessions' }"
      >{{ t('fileTree.mode_sessions') }}</span>
      <span class="explorer-seg-spacer" />
      <Icon
        v-if="mode === 'knowledge'"
        name="refresh"
        class="explorer-seg-icon"
        :title="t('common.refresh')"
        v-mq:[EventNames.filetreeModeRefresh].click
      />
    </div>
    <div v-show="mode === 'project'" class="explorer-body">
      <FileTree />
    </div>
    <div v-show="mode === 'knowledge'" class="explorer-body">
      <KnowledgeTree ref="kbTreeRef" />
    </div>
    <div v-show="mode === 'sessions'" class="explorer-body">
      <SessionsPane />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import FileTree from './FileTree.vue'
import KnowledgeTree from './KnowledgeTree.vue'
import SessionsPane from '../sessions/SessionsPane.vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

defineOptions({ name: 'ExplorerPane' })

const { t } = useI18n()

const mode = ref('project') // project | knowledge | sessions
const kbTreeRef = ref(null)
const _unsubs = []

function setMode(m) {
  if (m === 'knowledge' || m === 'project' || m === 'sessions') mode.value = m
}

onMounted(() => {
  _unsubs.push(mq.on(EventNames.filetreeModeToggle, () => {
    mode.value = mode.value === 'project' ? 'knowledge' : 'project'
  }))
  _unsubs.push(mq.on(EventNames.filetreeModeSelect, (d) => {
    if (d && d.mode) setMode(d.mode)
  }))
  _unsubs.push(mq.on(EventNames.filetreeModeRefresh, () => {
    kbTreeRef.value?.reload()
  }))
})

onUnmounted(() => {
  for (const fn of _unsubs) fn()
  _unsubs.length = 0
})
</script>

<style scoped>
.explorer-pane {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: var(--bg-primary);
}
.explorer-seg {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px 6px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
}
.explorer-seg-btn {
  padding: 2px 10px;
  font-size: 12px;
  line-height: 18px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-secondary);
  user-select: none;
}
.explorer-seg-btn:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.05));
}
.explorer-seg-btn.active {
  background: var(--accent-bg);
  color: var(--accent, #409eff);
  font-weight: 600;
}
.explorer-seg-spacer {
  flex: 1;
}
.explorer-seg-icon {
  cursor: pointer;
  opacity: 0.6;
  padding: 2px;
}
.explorer-seg-icon:hover {
  opacity: 1;
}
.explorer-body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
