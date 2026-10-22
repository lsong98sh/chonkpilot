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
      :tabs="tabs"
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
                :maxlength="MAX_NAME"
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
            <!-- 「AI 编写」：输入角色 / 要点 → 走 LLM 流式链路按智能体提示词规范撰写（正文默认大纲由后端完成） -->
            <Button size="mini" text :disabled="optimizing || writing" @click="openAiWrite">
              <Icon name="magic-stick" size="14" /> {{ $t('scenario.ai_write') }}</Button>
          </div>
          <Textarea
            ref="promptRef"
            class="prompt-textarea"
            :modelValue="agent.prompt"
            @update:modelValue="updateField('prompt', $event)"
            :rows="12"
            :maxlength="MAX_TEXT"
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
          <div v-if="agent.filterTools" class="tool-tree">
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
                  :class="{ checked: agentTools.includes(tool.name), locked: isLockedTool(tool.name) }"
                >
                  <input
                    type="checkbox"
                    :checked="agentTools.includes(tool.name)"
                    :disabled="isLockedTool(tool.name)"
                    @change="toggleTool(tool.name)"
                  />
                  <span class="tool-name" :title="tool.name">{{ stripToolPrefix(tool.name, tool.server) }}</span>
                  <span v-if="tool.desc" class="tool-desc">{{ tool.desc }}</span>
                  <span v-if="isLockedTool(tool.name)" class="tool-locked-tag">{{ $t('scenario.skill_tools_locked') }}</span>
                </label>
              </div>
            </div>

            <!-- 技能关联工具中「不在现有工具分类里」的项：仍展示（避免「已选但不可见」），且恒为锁定勾选 -->
            <div v-if="orphanTools.length > 0" class="tool-category">
              <div class="tool-category-header">
                <span class="category-arrow expanded">▶</span>
                <span class="category-label">{{ $t('scenario.skill_tools_group') }}</span>
                <span class="category-count">({{ orphanTools.length }})</span>
              </div>
              <div class="tool-category-body">
                <label
                  v-for="tool in orphanTools"
                  :key="tool.name"
                  class="tool-item"
                  :class="{ checked: agentTools.includes(tool.name), locked: isLockedTool(tool.name) }"
                >
                  <input
                    type="checkbox"
                    :checked="agentTools.includes(tool.name)"
                    :disabled="isLockedTool(tool.name)"
                    @change="toggleTool(tool.name)"
                  />
                  <span class="tool-name" :title="tool.name">{{ tool.name }}</span>
                  <span v-if="isLockedTool(tool.name)" class="tool-locked-tag">{{ $t('scenario.skill_tools_locked') }}</span>
                </label>
              </div>
            </div>

            <div v-if="allToolCategories.length === 0 && orphanTools.length === 0" class="tool-empty">
              {{ $t('scenario.no_tools') }}
            </div>
          </div>
          <div v-else class="no-filter-hint">
            {{ $t('scenario.no_filter_hint') }}
          </div>
        </div>
      </template>

      <!-- Tab 4: 技能 —— 名单类引用（表格形式；弹窗按级别分组多选） -->
      <template #skills>
        <div class="tab-content">
          <div class="ref-table-toolbar">
            <span class="pf-hint">{{ $t('scenario.skills_hint') }}</span>
            <Button size="mini" text @click="openSkillsDialog">
              <Icon name="plus" size="12" /> {{ $t('scenario.add_item') }}</Button>
          </div>
          <div v-if="agentSkills.length > 0" class="ref-table">
            <div class="ref-table-head">
              <span class="rc-name">{{ $t('scenario.col_name') }}</span>
              <span class="rc-tag">{{ $t('scenario.col_level') }}</span>
              <span class="rc-action"></span>
            </div>
            <div v-for="name in agentSkills" :key="name" class="ref-table-row">
              <span class="rc-name" :title="name">{{ name }}</span>
              <span class="rc-tag"><Tag size="mini" type="info">{{ skillLevelLabel(name) }}</Tag></span>
              <span class="rc-action">
                <Button size="mini" text @click="removeSkill(name)">{{ $t('scenario.remove') }}</Button>
              </span>
            </div>
          </div>
          <div v-else class="tool-empty">{{ $t('scenario.no_selected_skills') }}</div>
        </div>
      </template>

      <!-- Tab 5: 资源 —— 名单类引用（本地 resource 原语 + MCP resources；表格形式；弹窗分组多选） -->
      <template #resources>
        <div class="tab-content">
          <div class="ref-table-toolbar">
            <span class="pf-hint">{{ $t('scenario.resources_hint') }}</span>
            <Button size="mini" text @click="openResourcesDialog">
              <Icon name="plus" size="12" /> {{ $t('scenario.add_item') }}</Button>
          </div>
          <div v-if="agentResources.length > 0" class="ref-table">
            <div class="ref-table-head">
              <span class="rc-name">{{ $t('scenario.col_name') }}</span>
              <span class="rc-tag">{{ $t('scenario.col_level') }}</span>
              <span class="rc-action"></span>
            </div>
            <div v-for="value in agentResources" :key="value" class="ref-table-row">
              <span class="rc-name" :title="value">{{ value }}</span>
              <span class="rc-tag"><Tag size="mini" type="info">{{ resourceLevelLabel(value) }}</Tag></span>
              <span class="rc-action">
                <Button size="mini" text @click="removeResource(value)">{{ $t('scenario.remove') }}</Button>
              </span>
            </div>
          </div>
          <div v-else class="tool-empty">{{ $t('scenario.no_selected_resources') }}</div>
        </div>
      </template>

      <!-- Tab 6: 委托（仅子 agent）——委派条件独立成页签编辑 -->
      <template #delegate>
        <div class="tab-content">
          <div class="pf-hint">{{ $t('scenario.delegate_hint') }}</div>
          <Textarea
            :modelValue="agent.delegateCond"
            @update:modelValue="updateField('delegateCond', $event)"
            :rows="8"
            :placeholder="$t('scenario.placeholder.delegateCond')"
          />
        </div>
      </template>
    </Tabs>

    <!-- 「AI 编写」弹框：输入 角色 / 要点 → LLM 流式写入提示词 -->
    <div v-if="aiWriteOpen" class="ae-modal-overlay" @click.self="aiWriteOpen = false">
      <div class="ae-modal">
        <div class="ae-modal-head">{{ $t('scenario.ai_write_title') }}</div>
        <div class="ae-modal-body">
          <div class="pf-hint">{{ $t('scenario.ai_write_hint') }}</div>
          <label class="form-label">{{ $t('scenario.ai_write_role') }}</label>
          <Input v-model="aiRole" :placeholder="$t('scenario.ai_write_role_ph')" />
          <label class="form-label">{{ $t('scenario.ai_write_points') }}</label>
          <Textarea v-model="aiPoints" class="ae-modal-area" :placeholder="$t('scenario.ai_write_points_ph')" />
        </div>
        <div class="ae-modal-footer">
          <Button size="small" @click="aiWriteOpen = false">{{ $t('common.cancel') }}</Button>
          <Button size="small" type="primary" :disabled="!aiPoints.trim()" @click="confirmAiWrite">{{ $t('scenario.ai_write') }}</Button>
        </div>
      </div>
    </div>

    <!-- 技能选择弹框：按级别分组，多选勾选 -->
    <div v-if="skillsDialogOpen" class="ae-modal-overlay" @click.self="skillsDialogOpen = false">
      <div class="ae-modal">
        <div class="ae-modal-head">{{ $t('scenario.select_skills_title') }}</div>
        <div class="ae-modal-body">
          <div v-if="skillGroups.length === 0" class="tool-empty">{{ $t('scenario.no_skills') }}</div>
          <div v-for="g in skillGroups" :key="g.name" class="tool-category">
            <div class="tool-category-header">
              <span class="category-label">{{ g.label }}</span>
              <span class="category-count">({{ g.items.length }})</span>
            </div>
            <div class="tool-category-body">
              <label
                v-for="sk in g.items"
                :key="sk.name"
                class="tool-item"
                :class="{ checked: pendingSkills.includes(sk.name) }"
              >
                <input
                  type="checkbox"
                  :checked="pendingSkills.includes(sk.name)"
                  @change="togglePendingSkill(sk.name)"
                />
                <span class="tool-name" :title="sk.name">{{ sk.name }}</span>
              </label>
            </div>
          </div>
        </div>
        <div class="ae-modal-footer">
          <Button size="small" @click="skillsDialogOpen = false">{{ $t('common.cancel') }}</Button>
          <Button size="small" type="primary" @click="confirmSkills">{{ $t('common.confirm') }}</Button>
        </div>
      </div>
    </div>

    <!-- 资源选择弹框：本地 resource 原语（按级别）+ MCP resources（按 server 分组），多选勾选 -->
    <div v-if="resourcesDialogOpen" class="ae-modal-overlay" @click.self="resourcesDialogOpen = false">
      <div class="ae-modal">
        <div class="ae-modal-head">{{ $t('scenario.select_resources_title') }}</div>
        <div class="ae-modal-body">
          <div class="pf-hint">{{ $t('scenario.resources_local') }}</div>
          <div v-if="resourceGroups.length === 0" class="tool-empty">{{ $t('scenario.no_resources') }}</div>
          <div v-for="g in resourceGroups" :key="'lv-' + g.name" class="tool-category">
            <div class="tool-category-header">
              <span class="category-label">{{ g.label }}</span>
              <span class="category-count">({{ g.items.length }})</span>
            </div>
            <div class="tool-category-body">
              <label
                v-for="rs in g.items"
                :key="rs.name"
                class="tool-item"
                :class="{ checked: pendingResources.includes(rs.name) }"
              >
                <input
                  type="checkbox"
                  :checked="pendingResources.includes(rs.name)"
                  @change="togglePendingResource(rs.name)"
                />
                <span class="tool-name" :title="rs.name">{{ rs.name }}</span>
              </label>
            </div>
          </div>

          <div class="pf-hint">{{ $t('scenario.resources_mcp') }}</div>
          <div v-if="mcpResources.length === 0" class="tool-empty">{{ $t('scenario.no_mcp_resources') }}</div>
          <div v-for="g in mcpResources" :key="'mcp-' + g.name" class="tool-category">
            <div class="tool-category-header">
              <span class="category-label">{{ g.label }}</span>
              <span class="category-count">({{ g.resources.length }})</span>
            </div>
            <div class="tool-category-body">
              <label
                v-for="rs in g.resources"
                :key="rs.uri"
                class="tool-item"
                :class="{ checked: pendingResources.includes(rs.uri) }"
              >
                <input
                  type="checkbox"
                  :checked="pendingResources.includes(rs.uri)"
                  @change="togglePendingResource(rs.uri)"
                />
                <span class="tool-name" :title="rs.uri">{{ rs.name || rs.uri }}</span>
                <span class="tool-desc" :title="rs.uri">{{ rs.uri }}</span>
              </label>
            </div>
          </div>
        </div>
        <div class="ae-modal-footer">
          <Button size="small" @click="resourcesDialogOpen = false">{{ $t('common.cancel') }}</Button>
          <Button size="small" type="primary" @click="confirmResources">{{ $t('common.confirm') }}</Button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { message, confirm } from '../../components/ui'
import { Input, Textarea, Button, Select, Tag, Tabs } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import PromptVariablesButton from '../../components/common/PromptVariablesButton.vue'
import mq from '../../utils/mq'
import { optimizeAgentPrompt } from '../../api/config'
import { loadSkillTools } from '../../utils/agentAssets'
import { stripToolPrefix } from '../../utils/toolSource'
import { filterToolsTogglePatch, normalizeTools } from '../../utils/agentToolFilter'
import { EventNames } from '../../events/event-names'
import { useVariableInsert } from '../../composables/usePromptVariables'
// INPUTVAL-1：Agent 名 ≤64 字符；系统提示词 ≤256 KB 字符。
import { MAX_NAME, MAX_TEXT } from '../../utils/fieldRules'

const { t } = useI18n()

const props = defineProps({
  agent: {
    type: Object,
    required: true,
  },
  // 当前编辑级别（app|user|project|prjusr）：用于读取 skill 契约（skill → tools 映射）时的级别可见矩阵。
  kind: {
    type: String,
    default: '',
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
  // 可选 skill 名单（已按级别过滤；每项 { name, level, label }）——「技能」页签用。
  skillOptions: {
    type: Array,
    default: () => [],
  },
  // 本地 resource 原语名单（每项 { name, level, label }）——「资源」页签用。
  resourceOptions: {
    type: Array,
    default: () => [],
  },
  // MCP resources 分组（[{ name, label, resources:[{ value, name, uri, desc }] }]）——「资源」页签用。
  mcpResources: {
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

// 页签：基础 / 提示词 / 工具 / 技能 / 资源；非主 agent 追加「委托」（委派条件独立页签）。
const tabs = computed(() => {
  const out = [
    { name: 'basic', label: t('scenario.tab_basic') },
    { name: 'prompt', label: t('scenario.tab_prompt') },
    { name: 'tools', label: t('scenario.tab_tools') },
    { name: 'skills', label: t('scenario.tab_skills') },
    { name: 'resources', label: t('scenario.tab_resources') },
  ]
  if (!props.agent.isMain) out.push({ name: 'delegate', label: t('scenario.tab_delegate') })
  return out
})

const agentTools = computed(() => normalizeTools(props.agent.tools))
const agentSkills = computed(() => normalizeTools(props.agent.skills))
const agentResources = computed(() => normalizeTools(props.agent.resources))

function updateField(field, value) {
  emit('update:agent', { ...props.agent, [field]: value })
}

// ── 技能 / 资源 选择弹框（表格 + 多选）─────────────────────────────
// 分组：按级别（app|user|project|prjusr）排序展示（级别标签取选项自带 label）。
const LEVEL_ORDER = ['app', 'user', 'project', 'prjusr']
function groupByLevel(items) {
  const byLv = new Map()
  for (const it of items || []) {
    const lv = it.level || 'app'
    if (!byLv.has(lv)) byLv.set(lv, { name: lv, label: it.label || lv, items: [] })
    byLv.get(lv).items.push(it)
  }
  return [...byLv.values()].sort((a, b) => LEVEL_ORDER.indexOf(a.name) - LEVEL_ORDER.indexOf(b.name))
}
const skillGroups = computed(() => groupByLevel(props.skillOptions))
const resourceGroups = computed(() => groupByLevel(props.resourceOptions))

// 已选项的级别标签查找：本地资源按 name，MCP 资源按 uri（分组标签）。
const skillLevelMap = computed(() => {
  const m = {}
  for (const o of props.skillOptions) m[o.name] = o.label
  return m
})
const resourceLevelMap = computed(() => {
  const m = {}
  for (const o of props.resourceOptions) m[o.name] = o.label
  for (const g of props.mcpResources) for (const r of g.resources || []) m[r.uri] = g.label
  return m
})
function skillLevelLabel(name) { return skillLevelMap.value[name] || '-' }
function resourceLevelLabel(value) { return resourceLevelMap.value[value] || '-' }
function toggleItem(list, value) {
  return list.includes(value) ? list.filter((x) => x !== value) : [...list, value]
}

// 技能：弹窗确认 → 重算 skill→tools 映射，并在启用过滤时把关联工具并入白名单（同一处 emit）。
const skillsDialogOpen = ref(false)
const pendingSkills = ref([])
function openSkillsDialog() {
  pendingSkills.value = [...agentSkills.value]
  skillsDialogOpen.value = true
}
function togglePendingSkill(name) { pendingSkills.value = toggleItem(pendingSkills.value, name) }
async function confirmSkills() {
  const skills = [...new Set(pendingSkills.value)]
  skillsDialogOpen.value = false
  await applySkills(skills)
}
// removeSkill：从已选技能移除 → 重新计算映射（被移除技能的工具随之解锁、可自由取消）。
async function removeSkill(name) {
  await applySkills(agentSkills.value.filter((s) => s !== name))
}
// applySkills：读技能契约得 skill→tools 映射 → 回写 skills（+ 启用过滤时并入关联工具）。
async function applySkills(skills) {
  const map = await loadSkillTools(props.kind, skills)
  skillToolsMap.value = map
  const patch = { skills }
  if (props.agent.filterTools) patch.tools = mergeLockedTools(agentTools.value, skills, map)
  emit('update:agent', { ...props.agent, ...patch })
}

// 资源：弹窗确认 → 写入 agent.resources（本地 name / MCP uri 同一数组）。
const resourcesDialogOpen = ref(false)
const pendingResources = ref([])
function openResourcesDialog() {
  pendingResources.value = [...agentResources.value]
  resourcesDialogOpen.value = true
}
function togglePendingResource(value) { pendingResources.value = toggleItem(pendingResources.value, value) }
function confirmResources() {
  resourcesDialogOpen.value = false
  updateField('resources', [...new Set(pendingResources.value)])
}
function removeResource(value) {
  updateField('resources', agentResources.value.filter((x) => x !== value))
}

// ── 技能关联工具：默认勾选且不可取消 ───────────────────────────────
// skill → tools 映射（读技能契约 refs.tools）；锁定集合 = 已选技能关联的工具并集。
const skillToolsMap = ref({})
const lockedTools = computed(() => {
  const out = new Set()
  for (const sk of agentSkills.value) {
    const list = skillToolsMap.value[sk]
    if (list) for (const tn of list) out.add(tn)
  }
  return out
})
function isLockedTool(name) { return lockedTools.value.has(name) }
// 合并：当前勾选 ∪ 各已选技能关联工具（顺序稳定、去重）。
function mergeLockedTools(current, skills, map) {
  const out = [...current]
  const has = new Set(current)
  for (const sk of skills) {
    for (const tn of map[sk] || []) {
      if (!has.has(tn)) { has.add(tn); out.push(tn) }
    }
  }
  return out
}

// 工具树里不存在的「已选 / 已锁定」工具：仍展示（避免「已选但不可见」），列于「技能关联工具」分组。
const knownToolNames = computed(() => {
  const s = new Set()
  for (const cat of props.allToolCategories) for (const tl of cat.tools || []) s.add(tl.name)
  return s
})
const orphanTools = computed(() => {
  const out = []
  for (const name of new Set([...agentTools.value, ...lockedTools.value])) {
    if (name && !knownToolNames.value.has(name)) out.push({ name })
  }
  return out
})

// 工具过滤开关（G-45 ①，2026-09-25 拍板 A）：`filterTools` 不持久化，后端判据 =
// 「tools 非空 = 白名单，空 = 不限制」→ **关闭时须同时清空 tools**（否则开关已关而数组
// 残留，后端仍静默限制）。打开时不清空（保留既有选择），并**并入技能关联工具**（默认勾选）。
// 语义与补丁见 utils/agentToolFilter.js。
function toggleFilterTools() {
  const patch = filterToolsTogglePatch(props.agent)
  const next = { ...props.agent, ...patch }
  if (patch.filterTools) {
    next.tools = mergeLockedTools(agentTools.value, agentSkills.value, skillToolsMap.value)
  }
  emit('update:agent', next)
  // Expand all categories when enabling filter tools
  if (patch.filterTools) {
    expandedCategories.value = new Set(props.allToolCategories.map(c => c.name))
  }
}

// 按钮事件化：v-mq 触发 → 本地执行
const _unsubs = []
onMounted(async () => {
  _unsubs.push(mq.on(EventNames.agentCopy, handleCopy))
  _unsubs.push(mq.on(EventNames.agentOptimize, optimizePrompt))
  _unsubs.push(mq.on(EventNames.agentToggleCategory, ({ name }) => {
    if (name) toggleCategory(name)
  }))
  // 载入既有 skills 的 skill→tools 映射；若已启用过滤 → 幂等并入关联工具（自动勾选）。
  if (agentSkills.value.length > 0) {
    const map = await loadSkillTools(props.kind, agentSkills.value)
    skillToolsMap.value = map
    if (props.agent.filterTools) {
      const merged = mergeLockedTools(agentTools.value, agentSkills.value, map)
      if (merged.length !== agentTools.value.length) updateField('tools', merged)
    }
  }
})
onUnmounted(() => {
  _unsubs.forEach(fn => fn())
  // AI 编写流式中关闭 → 中止订阅，避免残留 onToken 写回已卸载组件。
  if (writeAbort) { writeAbort(); writeAbort = null }
})

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
  if (isLockedTool(toolName)) return // 由技能关联，不可取消
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

// ── 「AI 编写」提示词（复用 LLM 流式链路 gui.prompt-optimise）──────────
// 弹框输入「角色 / 要点」→ useCase 带智能体提示词规范 → 流式写入 agent.prompt
// （提示词默认正文 = 大纲，已由后端完成，前端不再提供「大纲」按钮）。
const aiWriteOpen = ref(false)
const aiRole = ref('')
const aiPoints = ref('')
const writing = ref(false)
let writeAbort = null
// 流式累积缓冲（本地变量，不依赖 props 回环，避免异步回填丢 token）。
let writeBuf = ''

function openAiWrite() {
  aiRole.value = ''
  aiPoints.value = ''
  aiWriteOpen.value = true
}

async function confirmAiWrite() {
  if (!aiPoints.value.trim()) {
    message.warning(t('common.input_required'))
    return
  }
  if ((props.agent.prompt || '').trim()) {
    try {
      await confirm(t('scenario.ai_write_confirm'), t('scenario.ai_write_title'))
    } catch (_) {
      return
    }
  }
  aiWriteOpen.value = false
  writing.value = true
  writeBuf = ''
  const useCase = t('scenario.ai_write_use_case') + '\n' + t('scenario.ai_write_role') + ': ' + (aiRole.value.trim() || '-')
  const prompt = aiPoints.value
  updateField('prompt', '')
  writeAbort = optimizeAgentPrompt(
    { title: props.agent.name || t('scenario.unnamed'), useCase, prompt },
    (chunk) => { writeBuf += chunk; updateField('prompt', writeBuf) },
    (p) => { writing.value = false; if (p) { writeBuf = p; updateField('prompt', p) } },
    (err) => { writing.value = false; message.error(t('common.optimize_failed') + ': ' + err) },
  )
}
</script>

<style scoped>
.agent-editor {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  /* 相对定位：弹框 overlay 以本面板为定位锚（inset:0） */
  position: relative;
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

.pf-hint {
  font-size: 11px;
  color: var(--text-muted, #999);
  opacity: 0.8;
  flex-shrink: 0;
}

.pf-ref-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--text-primary);
  flex-shrink: 0;
  margin-top: 4px;
}

/* ── 技能 / 资源 表格（name | 级别标签 | 移除）── */
.ref-table-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-shrink: 0;
}

.ref-table {
  display: flex;
  flex-direction: column;
  font-size: 12px;
}

.ref-table-head,
.ref-table-row {
  display: grid;
  grid-template-columns: 1fr auto auto;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
}

.ref-table-head {
  font-size: 11px;
  font-weight: 600;
  color: var(--text-muted, #999);
  border-bottom: 1px solid var(--border, #dee2e6);
}

.ref-table-row {
  border-radius: 4px;
  transition: background 0.1s;
}

.ref-table-row:hover {
  background: var(--bg-hover, #f5f5f5);
}

.rc-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono, monospace);
  color: var(--text-primary);
}

.rc-tag,
.rc-action {
  flex-shrink: 0;
}

/* 技能关联工具提示标签 */
.tool-locked-tag {
  font-size: 10px;
  color: var(--warning, #e6a23c);
  flex-shrink: 0;
  margin-left: auto;
}

.tool-item.locked {
  cursor: default;
}

/* ── 弹框（AI 编写 / 技能选择 / 资源选择）：照抄 PrimitivePanel 的 pf-modal 范式 ── */
.ae-modal-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.35);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 20;
}

.ae-modal {
  width: min(560px, calc(100% - 40px));
  max-height: calc(100% - 40px);
  display: flex;
  flex-direction: column;
  background: var(--bg-primary, #fff);
  border: 1px solid var(--border, #dee2e6);
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.2);
}

.ae-modal-head {
  padding: 10px 14px;
  font-size: 14px;
  font-weight: 600;
  border-bottom: 1px solid var(--border, #dee2e6);
  flex-shrink: 0;
}

.ae-modal-body {
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.ae-modal-area {
  min-height: 140px;
  resize: vertical;
  font-size: 13px;
}

.ae-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 10px 14px;
  border-top: 1px solid var(--border, #dee2e6);
  flex-shrink: 0;
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