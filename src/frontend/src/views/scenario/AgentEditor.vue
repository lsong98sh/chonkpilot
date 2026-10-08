<template>
  <div class="agent-editor">
    <!-- Header -->
    <div class="agent-editor-header">
      <div class="agent-editor-header-left">
        <span class="agent-editor-name">{{ agent.name || $t('scenario.unnamed') }}</span>
        <Tag :type="agent.isMain ? 'success' : 'info'" size="mini">
          {{ agent.isMain ? $t('scenario.main_agent_name') : $t('scenario.sub_agents') }}
        </Tag>
        <Tag v-if="agent.roleTag" type="warning" size="mini">
          {{ agent.roleTag }}
        </Tag>
      </div>
      <div class="agent-editor-header-actions">
        <Button v-if="!agent.isMain && showCopy" size="mini" text v-mq:[EventNames.agentCopy].click>
          <Icon name="copy-document" size="14" /> {{ $t('scenario.copy_agent') }}</Button>
      </div>
    </div>

    <!-- Tabs -->
    <Tabs
      class="agent-editor-tabs"
      :tabs="TABS"
      v-model="currentTab"
    >
      <!-- Tab 1: 基本信息 -->
      <template #basic>
        <div class="tab-content">
          <div class="form-layout">
            <div class="form-item form-item-full">
              <label class="form-label">{{ $t('scenario.fields.name') }}</label>
              <Input
                :modelValue="agent.name"
                @update:modelValue="updateField('name', $event)"
                :placeholder="$t('scenario.fields.name')"
              />
            </div>
            <div class="form-item form-item-full">
              <label class="form-label">{{ $t('scenario.fields.roleTag') }}</label>
              <Input
                :modelValue="agent.roleTag"
                @update:modelValue="updateField('roleTag', $event)"
                :placeholder="$t('scenario.placeholder.roleTag')"
              />
            </div>
            <div class="form-item form-item-full">
              <label class="form-label">{{ $t('scenario.fields.llm') }}</label>
              <Select
                :modelValue="agent.llmRef"
                @update:modelValue="updateField('llmRef', $event)"
                :options="llmOptions"
                :placeholder="$t('scenario.placeholder.llm')"
              />
            </div>
            <div class="form-item form-item-full">
              <label class="form-label">{{ $t('scenario.fields.description') }}</label>
              <Input
                :modelValue="agent.description"
                @update:modelValue="updateField('description', $event)"
                :placeholder="$t('scenario.fields.description')"
              />
            </div>
            <div v-if="!agent.isMain" class="form-item form-item-full">
              <label class="form-label">{{ $t('scenario.fields.delegateCond') }}</label>
              <Textarea
                :modelValue="agent.delegateCond"
                @update:modelValue="updateField('delegateCond', $event)"
                :rows="3"
                :placeholder="$t('scenario.placeholder.delegateCond')"
              />
            </div>
          </div>
        </div>
      </template>

      <!-- Tab 2: 提示词 -->
      <template #prompt>
        <div class="tab-content tab-prompt">
          <div class="prompt-toolbar">
            <PromptVariablesButton :disabled="optimizing" @insert="insertVariable" />
            <Button size="mini" text v-mq:[EventNames.agentOptimize].click :loading="optimizing" :disabled="optimizing">
              <Icon name="magic-stick" size="14" /> {{ $t('scenario.optimize_prompt') }}</Button>
          </div>
          <Textarea
            ref="promptRef"
            class="prompt-textarea"
            :modelValue="agent.prompt"
            @update:modelValue="updateField('prompt', $event)"
            :rows="12"
            :placeholder="$t('scenario.placeholder.systemPrompt')"
          />
        </div>
      </template>

      <!-- Tab 3: 工具 -->
      <template #tools>
        <div class="tab-content">
          <!-- filterTools checkbox -->
          <label class="filter-tools-checkbox">
            <input
              type="checkbox"
              :checked="agent.filterTools"
              @change="toggleFilterTools"
            />
            <span>{{ $t('scenario.enable_filter_tools') }}</span>
          </label>

          <!-- Tool tree (only when filterTools is true) -->
          <div v-if="agent.filterTools && allToolCategories.length > 0" class="tool-tree">
            <div
              v-for="cat in allToolCategories"
              :key="cat.name"
              class="tool-category"
            >
              <div
                class="tool-category-header"
                v-mq:[EventNames.agentToggleCategory].click="{ name: cat.name }"
              >
                <span class="category-arrow" :class="{ expanded: expandedCategories.has(cat.name) }">▶</span>
                <span class="category-label">{{ cat.label || cat.name }}</span>
                <span class="category-count">({{ cat.tools.length }})</span>
              </div>
              <div v-if="expandedCategories.has(cat.name)" class="tool-category-body">
                <label
                  v-for="tool in cat.tools"
                  :key="tool.name"
                  class="tool-item"
                  :class="{ checked: agentTools.includes(tool.name) }"
                >
                  <input
                    type="checkbox"
                    :checked="agentTools.includes(tool.name)"
                    @change="toggleTool(tool.name)"
                  />
                  <span class="tool-name" :title="tool.name">{{ stripToolPrefix(tool.name, tool.server) }}</span>
                  <span v-if="tool.desc" class="tool-desc">{{ tool.desc }}</span>
                </label>
              </div>
            </div>
          </div>
          <div v-else-if="agent.filterTools && allToolCategories.length === 0" class="tool-empty">
            {{ $t('scenario.no_tools') }}
          </div>
          <div v-if="!agent.filterTools" class="no-filter-hint">
            {{ $t('scenario.no_filter_hint') }}
          </div>
        </div>
      </template>
    </Tabs>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { message } from '../../components/ui'
import { Input, Textarea, Button, Select, Tag, Tabs } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import PromptVariablesButton from '../../components/common/PromptVariablesButton.vue'
import mq from '../../utils/mq'
import { stripToolPrefix } from '../../utils/toolSource'
import { filterToolsTogglePatch, normalizeTools } from '../../utils/agentToolFilter'
import { EventNames } from '../../events/event-names'
import { useVariableInsert } from '../../composables/usePromptVariables'

const { t } = useI18n()

const props = defineProps({
  agent: {
    type: Object,
    required: true,
  },
  llmOptions: {
    type: Array,
    default: () => [],
  },
  toolGroups: {
    type: Array,
    default: () => [],
  },
  allToolCategories: {
    type: Array,
    default: () => [],
  },
  // 提示词优化进行中（由父组件透传）：优化按钮 loading + 禁用（防重入）
  optimizing: {
    type: Boolean,
    default: false,
  },
  // 是否显示「复制」按钮（场景子 agent = 复制为新 agent；独立「智能体原语」编辑 = 不适用 → false）
  showCopy: {
    type: Boolean,
    default: true,
  },
})

const emit = defineEmits(['update:agent', 'copy', 'optimize'])

const currentTab = ref('basic')
const expandedCategories = ref(new Set())
// 提示词变量插入（OP-12）：把 {{...}} 插入到提示词编辑框光标处（Textarea.$el 即 textarea）。
const promptRef = ref(null)
const { insert: insertVariable } = useVariableInsert({
  getEl: () => promptRef.value?.$el || null,
  getValue: () => props.agent.prompt || '',
  setValue: (v) => updateField('prompt', v),
})

const TABS = [
  { name: 'basic', label: t('scenario.tab_basic') },
  { name: 'prompt', label: t('scenario.tab_prompt') },
  { name: 'tools', label: t('scenario.tab_tools') },
]

const agentTools = computed(() => normalizeTools(props.agent.tools))

function updateField(field, value) {
  emit('update:agent', { ...props.agent, [field]: value })
}

// 工具过滤开关（G-45 ①，2026-09-25 拍板 A）：`filterTools` 不持久化，后端判据 =
// 「tools 非空 = 白名单，空 = 不限制」→ **关闭时须同时清空 tools**（否则开关已关而数组
// 残留，后端仍静默限制）。打开时不清空（保留既有选择）。语义与补丁见 utils/agentToolFilter.js。
function toggleFilterTools() {
  const patch = filterToolsTogglePatch(props.agent)
  emit('update:agent', { ...props.agent, ...patch })
  // Expand all categories when enabling filter tools
  if (patch.filterTools) {
    expandedCategories.value = new Set(props.allToolCategories.map(c => c.name))
  }
}

function toggleCategory(name) {
  const set = new Set(expandedCategories.value)
  if (set.has(name)) {
    set.delete(name)
  } else {
    set.add(name)
  }
  expandedCategories.value = set
}

function toggleTool(toolName) {
  const current = agentTools.value
  const tools = current.includes(toolName)
    ? current.filter((t) => t !== toolName)
    : [...current, toolName]
  updateField('tools', tools)
}

function optimizePrompt() {
  emit('optimize', props.agent)
}

function handleCopy() {
  emit('copy', props.agent)
}

// 按钮事件化：v-mq 触发 → 本地执行
const _unsubs = []
onMounted(() => {
  _unsubs.push(mq.on(EventNames.agentCopy, handleCopy))
  _unsubs.push(mq.on(EventNames.agentOptimize, optimizePrompt))
  _unsubs.push(mq.on(EventNames.agentToggleCategory, ({ name }) => {
    if (name) toggleCategory(name)
  }))
})
onUnmounted(() => _unsubs.forEach(fn => fn()))
</script>

<style scoped>
.agent-editor {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.agent-editor-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border, #dee2e6);
  flex-shrink: 0;
}

.agent-editor-header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.agent-editor-name {
  font-weight: 600;
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 200px;
}

.agent-editor-header-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.agent-editor-tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.tab-content {
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow-y: auto;
  flex: 1;
}

.tab-prompt {
  position: relative;
}

.form-layout {
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
}

.form-item {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  width: 100%;
}

.form-item-12 {
  width: calc(50% - 6px);
}

.form-item-full {
  width: 100%;
}

.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}

.prompt-toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.prompt-textarea {
  flex: 1;
  min-height: 0;
}

.prompt-textarea :deep(textarea) {
  font-family: var(--font-mono, 'Cascadia Code', 'JetBrains Mono', monospace);
  resize: none;
  min-height: 200px;
}

/* Tool tree */
.filter-tools-checkbox {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  padding: 4px 0 8px;
  font-size: 12px;
  font-weight: 500;
  user-select: none;
  flex-shrink: 0;
}

.filter-tools-checkbox input[type="checkbox"] {
  flex-shrink: 0;
}

.no-filter-hint {
  font-size: 11px;
  color: var(--text-muted, #999);
  padding: 20px 0;
  text-align: center;
}

.tool-tree {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.tool-category {
  margin-bottom: 1px;
}

.tool-category-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  cursor: pointer;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 600;
  user-select: none;
  transition: background 0.1s;
}

.tool-category-header:hover {
  background: var(--bg-hover, #f5f5f5);
}

.category-arrow {
  font-size: 10px;
  transition: transform 0.15s;
  color: var(--text-muted, #999);
  flex-shrink: 0;
}

.category-arrow.expanded {
  transform: rotate(90deg);
}

.category-label {
  color: var(--text-primary, #303133);
}

.category-count {
  font-size: 11px;
  color: var(--text-muted, #999);
  font-weight: 400;
}

.tool-category-body {
  padding-left: 20px;
}

.tools-list {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.tool-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  border-radius: 4px;
  cursor: pointer;
  transition: background 0.1s;
  user-select: none;
}

.tool-item:hover {
  background: var(--bg-hover, #f5f5f5);
}

.tool-item.checked {
  background: var(--accent-bg, #ecf5ff);
}

.tool-item input[type="checkbox"] {
  flex-shrink: 0;
}

.tool-name {
  font-family: var(--font-mono, monospace);
  font-size: 12px;
  font-weight: 500;
  color: var(--text-primary);
  min-width: 120px;
}

.tool-desc {
  font-size: 11px;
  color: var(--text-muted, #999);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-empty {
  font-size: 12px;
  color: var(--text-muted, #999);
  text-align: center;
  padding: 20px;
}
</style>