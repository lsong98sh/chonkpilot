<template>
  <!-- filetree 区多栈容器：头部「项目 / 知识库 / 项目记忆 / 会话 / 工具」切换（v-show 同显各树，切换不销毁状态）。
       本分段 = 知识库视图入口（toolbar「知识库」按钮 2026-09-16 已移除）；
       「项目记忆」= 按配置列出已启用记忆类别（第 4 模式，2026-09-26）；
       「会话」= 原会话抽屉内容迁入（P3-C1，2026-09-24）；
       「工具」= 工具契约从知识库分离出的独立页签（第 5 模式，2026-09-29），位于「会话」右侧，
       复用 KnowledgeTree（kinds=['tool']），编辑层级与知识库一致（系统/用户/项目三级）；
       知识库页签不再显示工具（仅 skill/prompt/resource）；
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
        :class="{ active: mode === 'memory' }"
        :title="t('fileTree.mode_memory')"
        v-mq:[EventNames.filetreeModeSelect].click="{ mode: 'memory' }"
      >{{ t('fileTree.mode_memory') }}</span>
      <span
        class="explorer-seg-btn"
        :class="{ active: mode === 'sessions' }"
        :title="t('fileTree.mode_sessions')"
        v-mq:[EventNames.filetreeModeSelect].click="{ mode: 'sessions' }"
      >{{ t('fileTree.mode_sessions') }}</span>
      <span
        class="explorer-seg-btn"
        :class="{ active: mode === 'tools' }"
        :title="t('fileTree.mode_tools')"
        v-mq:[EventNames.filetreeModeSelect].click="{ mode: 'tools' }"
      >{{ t('fileTree.mode_tools') }}</span>
      <span class="explorer-seg-spacer" />
      <Icon
        v-if="mode === 'knowledge' || mode === 'memory' || mode === 'tools'"
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
      <KnowledgeTree
        ref="kbTreeRef"
        scope="knowledge"
        :kinds="KB_KINDS"
        title-key="fileTree.mode_knowledge"
        empty-key="fileTree.kb_empty"
      />
    </div>
    <div v-show="mode === 'memory'" class="explorer-body">
      <MemoryPane ref="memTreeRef" />
    </div>
    <div v-show="mode === 'sessions'" class="explorer-body">
      <SessionsPane />
    </div>
    <div v-show="mode === 'tools'" class="explorer-body">
      <KnowledgeTree
        ref="toolsTreeRef"
        scope="tools"
        :kinds="TOOL_KINDS"
        title-key="fileTree.mode_tools"
        empty-key="fileTree.tools_empty"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import FileTree from './FileTree.vue'
import KnowledgeTree from './KnowledgeTree.vue'
import MemoryPane from './MemoryPane.vue'
import SessionsPane from '../sessions/SessionsPane.vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

defineOptions({ name: 'ExplorerPane' })

const { t } = useI18n()

// 知识库页签类型范围 = 技能/提示词/资源（工具已分离至独立「工具」页签）；工具页签 = 仅工具
const KB_KINDS = ['skill', 'prompt', 'resource']
const TOOL_KINDS = ['tool']

const mode = ref('project') // project | knowledge | memory | sessions | tools
const kbTreeRef = ref(null)
const memTreeRef = ref(null)
const toolsTreeRef = ref(null)
const _unsubs = []

function setMode(m) {
  if (m === 'knowledge' || m === 'project' || m === 'memory' || m === 'sessions' || m === 'tools') mode.value = m
}

onMounted(() => {
  _unsubs.push(mq.on(EventNames.filetreeModeToggle, () => {
    mode.value = mode.value === 'project' ? 'knowledge' : 'project'
  }))
  _unsubs.push(mq.on(EventNames.filetreeModeSelect, (d) => {
    if (d && d.mode) setMode(d.mode)
  }))
  _unsubs.push(mq.on(EventNames.filetreeModeRefresh, () => {
    // 刷新当前模式的面板（知识库树 / 工具树 / 项目记忆列表）
    if (mode.value === 'knowledge') kbTreeRef.value?.reload()
    else if (mode.value === 'tools') toolsTreeRef.value?.reload()
    else if (mode.value === 'memory') memTreeRef.value?.refresh()
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
