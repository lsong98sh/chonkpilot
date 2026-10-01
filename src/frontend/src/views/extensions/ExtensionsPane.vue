<template>
  <!-- 「扩展」页（P3，2026-10-01）：filetree 区分段「扩展」的落地视图。
       顶部 = 5 个子 tab（知识 resources / 技能 skills / 工具 tools / 命令 prompts / 智能体 agents）；
       右侧 = 【级别】选择（popup 下拉，替代原 KnowledgeTree 内联级别按钮组）。
       内部按子 tab 渲染 KnowledgeTree（复用，kinds 过滤）；**级别状态由本页统一持有并下发给子树**
       （KnowledgeTree 仅经 props.level 读取，不再自持级别）。
       MQ 零新增：子 tab 复用 filetree-mode-select（payload 增可选 ext 字段），
       级别复用 kb-level-select（payload {kind}）。 -->
  <div class="extensions-pane">
    <div class="ext-subtabs">
      <span
        v-for="tab in SUBTABS"
        :key="tab.key"
        class="ext-subtab"
        :class="{ active: subTab === tab.key }"
        :title="t(tab.label)"
        v-mq:[EventNames.filetreeModeSelect].click="{ mode: 'extensions', ext: tab.key }"
      >{{ t(tab.label) }}</span>
      <span class="ext-subtabs-spacer" />
      <Popover placement="bottom-end" :width="120">
        <template #reference>
          <span class="ext-level-btn" :title="t('fileTree.level_label')">
            {{ t('fileTree.level_label') }}：{{ t('fileTree.kb_level_' + level) }}
          </span>
        </template>
        <div
          v-for="lv in LEVELS"
          :key="lv.kind"
          class="ext-level-item"
          :class="{ active: level === lv.kind }"
          v-mq:[EventNames.kbLevelSelect].click="{ kind: lv.kind }"
        >{{ t(lv.label) }}</div>
      </Popover>
    </div>
    <div class="ext-body">
      <KnowledgeTree
        :key="subTab + '-' + level"
        ref="treeRef"
        :scope="subTab"
        :kinds="activeTab.kinds"
        :level="level"
        :title-key="activeTab.label"
        :empty-key="activeTab.empty"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import KnowledgeTree from '../filetree/KnowledgeTree.vue'
import { Popover } from '../../components/ui'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

defineOptions({ name: 'ExtensionsPane' })

const { t } = useI18n()

// 5 子 tab：知识(resources) / 技能(skills) / 工具(tools) / 命令(prompts) / 智能体(agents)
// kinds = 该子 tab 的类型范围（交 KnowledgeTree 过滤）；label = 标题/子 tab 文案；empty = 空态文案。
const SUBTABS = [
  { key: 'resource', label: 'fileTree.ext_tab_resource', empty: 'fileTree.kb_empty', kinds: ['resource'] },
  { key: 'skill', label: 'fileTree.ext_tab_skill', empty: 'fileTree.kb_empty', kinds: ['skill'] },
  { key: 'tool', label: 'fileTree.ext_tab_tool', empty: 'fileTree.kb_empty', kinds: ['tool'] },
  { key: 'prompt', label: 'fileTree.ext_tab_prompt', empty: 'fileTree.kb_empty', kinds: ['prompt'] },
  { key: 'agent', label: 'fileTree.ext_tab_agent', empty: 'fileTree.kb_empty', kinds: ['agent'] },
]

// 四级级别（系统/用户/项目/项目私有）
const LEVELS = [
  { kind: 'app', label: 'fileTree.kb_level_app' },
  { kind: 'user', label: 'fileTree.kb_level_user' },
  { kind: 'project', label: 'fileTree.kb_level_project' },
  { kind: 'prjusr', label: 'fileTree.kb_level_prjusr' },
]

const subTab = ref(SUBTABS[0].key)
const level = ref('app')
const treeRef = ref(null)
const _unsubs = []

const activeTab = computed(() => SUBTABS.find(tb => tb.key === subTab.value) || SUBTABS[0])

function setSubTab(key) {
  if (SUBTABS.some(tb => tb.key === key)) subTab.value = key
}

function setLevel(kind) {
  if (LEVELS.some(lv => lv.kind === kind)) level.value = kind
}

onMounted(() => {
  // 子 tab 复用 filetree-mode-select（payload 增可选 ext 字段；仅扩展页受理 ext）
  _unsubs.push(mq.on(EventNames.filetreeModeSelect, (d) => {
    if (d && d.ext) setSubTab(d.ext)
  }))
  // 级别选择复用 kb-level-select（{kind}）
  _unsubs.push(mq.on(EventNames.kbLevelSelect, ({ kind }) => {
    if (kind) setLevel(kind)
  }))
})

onUnmounted(() => {
  for (const fn of _unsubs) fn()
  _unsubs.length = 0
})

defineExpose({
  reload: () => treeRef.value?.reload(),
  get level() { return level.value },
  get subTab() { return subTab.value },
})
</script>

<style scoped>
.extensions-pane {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: var(--bg-primary);
}
.ext-subtabs {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px 6px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
}
.ext-subtab {
  padding: 2px 8px;
  font-size: 12px;
  line-height: 18px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-secondary);
  user-select: none;
}
.ext-subtab:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.05));
}
.ext-subtab.active {
  background: var(--accent-bg);
  color: var(--accent, #409eff);
  font-weight: 600;
}
.ext-subtabs-spacer {
  flex: 1;
}
.ext-level-btn {
  font-size: 12px;
  line-height: 18px;
  padding: 2px 8px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-secondary);
  user-select: none;
  border: 1px solid var(--border);
}
.ext-level-btn:hover {
  color: var(--accent);
  border-color: var(--accent);
}
.ext-level-item {
  padding: 6px 12px;
  border-radius: 4px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  white-space: nowrap;
}
.ext-level-item:hover {
  background: var(--bg-hover, #f0f0f0);
}
.ext-level-item.active {
  color: var(--accent);
  font-weight: 600;
}
.ext-body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
