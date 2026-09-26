<template>
  <div class="edit-dialog-body">
    <!-- Top toolbar -->
    <div class="edit-toolbar">
      <div class="edit-title-input">
        <Input
          v-model="form.name"
          :placeholder="$t('scenario.placeholder.scene_name')"
          class="title-input"
        />
        <Input
          v-if="isNew"
          v-model="form.id"
          :placeholder="$t('scenario.fields.dirName')"
          class="dir-input"
        />
      </div>
      <div class="toolbar-right">
        <Button size="small" type="primary" v-mq:[EventNames.scenarioSave].click :loading="saving">{{ $t('common.save') }}</Button>
      </div>
    </div>

    <!-- Left-right split -->
    <div class="edit-split">
      <!-- Left: Agent list -->
      <div class="agent-list-panel">
        <!-- Agent list header -->
        <div class="agent-list-header">
          <span>{{ $t('scenario.agents') }} ({{ agents.length }})</span>
          <Button size="mini" v-mq:[EventNames.scenarioAddSubAgent].click>
            <Icon name="plus" :size="12" /> {{ $t('scenario.add_agent') }}
          </Button>
        </div>

        <div class="agent-list-body">
          <!-- Main agent group -->
          <div v-if="mainAgent" class="agent-group">
            <div class="agent-group-title">{{ $t('scenario.main_agent') }}</div>
            <div
              class="agent-list-item"
              :class="{ active: agents.indexOf(mainAgent) === selectedAgentIdx }"
              v-mq:[EventNames.scenarioSelectMain].click
            >
              <span class="al-name">{{ mainAgent.name || $t('scenario.main_agent') }}</span>
              <span class="al-badge main">main</span>
              <span class="al-remove disabled">×</span>
            </div>
          </div>

          <!-- Sub agents group -->
          <div v-if="subAgents.length > 0" class="agent-group">
            <div class="agent-group-title">{{ $t('scenario.sub_agents') }}</div>
            <div
              v-for="agent in subAgents"
              :key="agent.id || agent._key"
              class="agent-list-item"
              :class="{ active: agents.indexOf(agent) === selectedAgentIdx }"
              v-mq:[EventNames.scenarioSelectAgent].click="{ agent }"
            >
              <span class="al-name">{{ agent.name || $t('scenario.unnamed') }}</span>
              <span v-if="agent.roleTag" class="al-badge sub">{{ agent.roleTag }}</span>
              <span class="al-remove" v-mq:[EventNames.scenarioDeleteAgent].click.stop="{ agent }">×</span>
            </div>
          </div>

          <!-- Empty state -->
          <div v-if="agents.length === 0" class="agent-list-empty">
            <p>{{ $t('scenario.no_agent_hint') }}</p>
          </div>
        </div>

        <div class="agent-list-footer">
          <span class="footer-hint">{{ $t('scenario.cannot_delete_main') }}</span>
        </div>
      </div>

      <!-- Right: Agent editor ｜「组合后系统提示词」只读预览（页签，25 §3 三层） -->
      <div class="agent-editor-panel">
        <Tabs class="scenario-right-tabs" :tabs="rightTabs" v-model="rightTab">
          <template #agent>
            <div v-if="selectedAgent" class="agent-editor-wrapper">
              <AgentEditor
                :key="selectedAgentIdx"
                :agent="normalizedSelectedAgent"
                :llm-options="llmOptions"
                :tool-groups="toolGroups"
                :all-tool-categories="allToolCategories"
                @update:agent="updateAgentField"
                @copy="handleCopy"
                @optimize="handleOptimize"
              />
            </div>
            <div v-else class="empty-state">
              <div class="empty-icon">⎔</div>
              <p>{{ $t('scenario.select_agent_hint') }}</p>
            </div>
          </template>
          <template #combined>
            <CombinedPromptPreview
              :description="form.description"
              :agents="agents"
              :agent="selectedAgent"
            />
          </template>
        </Tabs>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { message, confirm } from '../../components/ui'
import { Input, Button, Tabs } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import AgentEditor from './AgentEditor.vue'
import CombinedPromptPreview from './CombinedPromptPreview.vue'
import { getUserConfig } from '../../api/config'
import { saveScenario } from '../../api/scenario'
import { filterToolsLoadPatch, normalizeTools } from '../../utils/agentToolFilter'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()

const props = defineProps({
  scenario: {
    type: Object,
    default: null,
  },
  isNew: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['done'])

const _unsubs = []

const saving = ref(false)
const form = ref({ name: '', description: '' })
const agents = ref([])
const selectedAgentIdx = ref(-1)
let nextKey = 1

const llmOptions = ref([])
const toolGroups = ref([])
const allToolCategories = ref([])

// 右侧页签：agent 编辑 ｜「组合后系统提示词」只读预览（25 §3 三层拼接结果）
const rightTab = ref('agent')
const rightTabs = computed(() => [
  { name: 'agent', label: t('scenario.agents') },
  { name: 'combined', label: t('scenario.preview.tab') },
])

const mainAgent = computed(() => agents.value.find(a => a.isMain))
const subAgents = computed(() => agents.value.filter(a => !a.isMain))
const selectedAgent = computed(() => {
  if (selectedAgentIdx.value >= 0 && selectedAgentIdx.value < agents.value.length) {
    return agents.value[selectedAgentIdx.value]
  }
  return null
})

/**
 * Normalize tools string → array for AgentEditor consumption.
 * The backend stores tools as an array in agents_json, but keep this tolerant
 * of legacy JSON-string values.
 */
const normalizedSelectedAgent = computed(() => {
  const agent = selectedAgent.value
  if (!agent) return null
  return { ...agent, tools: normalizeTools(agent.tools) }
})

// 注（2026-09-26，用户口径）：**场景不提供「恢复默认 / 回填上一级」** ——
// 场景 id 全局唯一（不允许跨级同名，[42 §2 (175)]），故「上一级同名场景」不存在，
// 该入口已整体摘除（原 upperLevels/canRestoreDefault/upperSource/resolveUpperSource/
// handleRestoreDefault + `api/scenario.loadScenario` 一并移除）。见 [37 SCEN-008]。
async function loadAgents() {
  // D7: agents come from scenario.agents (agents_json) — no separate
  // scenario_agents table anymore. Pure frontend array, saved atomically.
  // G-45 ⑤c 载入路径 ①：filterTools 不持久化 → 按 tools 反推回填（tools 非空 = 后端白名单）
  // 并归一 tools 为数组，使勾选框显示态与后端限制行为一致（消除「显示为关但后端仍限制」）。
  const list = props.scenario?.agents || []
  agents.value = list.map(a => ({
    ...a,
    ...filterToolsLoadPatch(a),
    _key: `k-${nextKey++}`,
  }))
  if (agents.value.length > 0) {
    const mainIdx = agents.value.findIndex(a => a.isMain)
    selectedAgentIdx.value = mainIdx >= 0 ? mainIdx : 0
  } else {
    // No agents found — create a default main agent
    createDefaultMainAgent()
  }
}

/**
 * 同场景内 agent 重名预检（2026-09-26 用户裁决，42 §2 (175)）：name 去空白 + 大小写不敏感
 * 比对；命中返回重复名，无重名返回空串。与后端 capfs 校验同口径（后端为权威拦截）。
 */
function findDuplicateAgentName(list) {
  const seen = new Set()
  for (const a of list || []) {
    const name = (a && a.name ? String(a.name) : '').trim()
    if (!name) continue
    const key = name.toLowerCase()
    if (seen.has(key)) return name
    seen.add(key)
  }
  return ''
}

/** Save scenario (with agents embedded) in one atomic SaveScenario call (D7) */
async function handleSave() {
  if (!form.value.name.trim()) {
    message.warning(t('scenario.placeholder.name'))
    return
  }
  // 新建：目录名（= 场景 id/key）必填；未填则按名称 slug 推导（v6 场景 = prompts 下的目录）
  if (props.isNew) {
    let id = (form.value.id || '').trim()
    if (!id) id = slugify(form.value.name)
    if (!id) {
      message.warning(t('scenario.dir_name_required'))
      return
    }
    if (!/^[A-Za-z0-9_-]+$/.test(id)) {
      message.warning(t('scenario.dir_name_format'))
      return
    }
    form.value.id = id
  }
  // 同场景内 agent 重名预检（2026-09-26 用户裁决，42 §2 (175)）：与后端 capfs 校验同口径
  // （trim + 大小写不敏感）；命中即以 i18n 文案显式提示，不发请求。后端仍为权威拦截（兜底）。
  const dupName = findDuplicateAgentName(agents.value)
  if (dupName) {
    message.warning(t('scenario.agent_duplicate', { name: dupName }))
    return
  }
  saving.value = true
  try {
    // Strip frontend-only keys (_key / id) before persisting.
    const payloadAgents = agents.value.map(({ _key, id, ...rest }) => rest)
    // data-scenario-save：有 id 更新 / 无 id 新建（20-gui），reply {ok, id}。
    const res = await saveScenario({ ...form.value, agents: payloadAgents })
    if (res && res.id) {
      form.value = { ...form.value, id: res.id }
    }
    message.success(props.isNew ? t('scenario.created') : t('scenario.updated'))
    emit('done')
  } catch (e) {
    message.error(t('scenario.save_failed') + ': ' + (e.message || e))
  } finally {
    saving.value = false
  }
}

function addSubAgent() {
  const newAgent = {
    name: t('scenario.new_agent'),
    description: '',
    roleTag: '',
    prompt: '',
    tools: [],
    llmRef: '',
    delegateCond: '',
    isMain: false,
    _key: `new-${nextKey++}`,
  }
  agents.value.push(newAgent)
  selectedAgentIdx.value = agents.value.length - 1
  message.success(t('scenario.agent_added'))
}

async function deleteAgent(agent) {
  if (agent.isMain) {
    message.warning(t('scenario.cannot_delete_main'))
    return
  }
  try {
    await confirm(t('scenario.delete_agent_confirm', { name: agent.name }), t('common.confirm'))
    agents.value = agents.value.filter(a => a !== agent)
    if (selectedAgentIdx.value >= agents.value.length) {
      selectedAgentIdx.value = agents.value.length - 1
    }
    message.success(t('scenario.agent_deleted'))
  } catch (e) {
    if (e !== 'cancel') message.error(t('scenario.delete_failed') + ': ' + (e.message || e))
  }
}

function updateAgentField(updatedAgent) {
  if (selectedAgentIdx.value >= 0 && selectedAgentIdx.value < agents.value.length) {
    const agent = agents.value[selectedAgentIdx.value]
    agents.value[selectedAgentIdx.value] = {
      ...updatedAgent,
      _key: agent._key,
    }
  }
}

function handleCopy(agent) {
  const copy = {
    ...agent,
    name: agent.name + ' ' + t('scenario.copy_suffix'),
    isMain: false,
    _key: `new-${nextKey++}`,
  }
  agents.value.push(copy)
  selectedAgentIdx.value = agents.value.length - 1
  message.success(t('scenario.agent_copied'))
}

function handleOptimize() {
  message.info(t('scenario.optimize_not_implemented'))
}

onMounted(async () => {
  // 交互事件化：v-mq 触发 → 本地执行
  _unsubs.push(mq.on(EventNames.scenarioSave, handleSave))
  _unsubs.push(mq.on(EventNames.scenarioAddSubAgent, addSubAgent))
  _unsubs.push(mq.on(EventNames.scenarioSelectMain, () => {
    selectedAgentIdx.value = agents.value.indexOf(mainAgent.value)
  }))
  _unsubs.push(mq.on(EventNames.scenarioSelectAgent, ({ agent }) => {
    if (agent) selectedAgentIdx.value = agents.value.indexOf(agent)
  }))
  _unsubs.push(mq.on(EventNames.scenarioDeleteAgent, ({ agent }) => {
    if (agent) deleteAgent(agent)
  }))

  // Load LLM options and available tools
  await Promise.all([loadLLMOptions(), loadToolGroups()])

  if (props.scenario) {
    form.value = { ...props.scenario }
    if (props.scenario.id) {
      await loadAgents(props.scenario.id)
    } else {
      // Existing scenario but no ID — create default main agent
      createDefaultMainAgent()
    }
  } else {
    form.value = { name: '', description: '', level: 'user' }
    createDefaultMainAgent()
  }
})

onUnmounted(() => _unsubs.forEach(fn => fn()))

async function loadLLMOptions() {
  try {
    const uc = await getUserConfig()
    const llms = uc?.config?.llms || []
    llmOptions.value = [
      { label: t('scenario.default_label'), value: '' },
      ...llms.map(llm => ({ label: `${llm.name} (${llm.model})`, value: llm.name })),
    ]
  } catch (e) {
    llmOptions.value = [{ label: t('scenario.default_label'), value: '' }]
  }
}

async function loadToolGroups() {
  const groups = []

  // 工具分组：读**运行时能力面**（T-31）——前端经桥客户端主题 tools-list →
  // 相对主题 mcp-tools-list（bridge.go frontMethodSubjects；桥自动注入 instance_id）。
  // 结果 {resultType, tools, ttlMs}，每项 {name, scope, description, inputSchema,
  // _meta:{hot, category, server}, node}；按 _meta.server.alias 分组展示。
  // scope 过滤已在桥侧完成（仅全局 + 当前实例，见 bridge.go filterCapabilityScope），
  // 前端直接渲染返回的全部工具。
  try {
    const env = await mq.emit('tools-list', {})
    const res = env && env.backend && env.backend.result
    const tools = res && Array.isArray(res.tools) ? res.tools : []

    const byServer = new Map()
    for (const tl of tools) {
      const meta = tl._meta || {}
      const srv = meta.server || {}
      const groupKey = srv.alias || srv.node || '全局'
      if (!byServer.has(groupKey)) byServer.set(groupKey, [])
      byServer.get(groupKey).push({
        name: tl.name,
        desc: (meta.hot ? '[hot] ' : '') + (tl.description || ''),
        // 展示用：网关为暴露名加的前缀（self_/dir_ 等）剥掉后显示（`name` 仍作配置键 / 勾选键）
        server: srv,
      })
    }
    for (const [key, list] of byServer) {
      groups.push({ name: key, label: key, tools: list })
    }
  } catch (_) {}

  toolGroups.value = groups
  allToolCategories.value = groups
}

// slugify 名称 → 合法目录名（小写字母/数字/下划线/连字符；其余归一为连字符）
function slugify(name) {
  return String(name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
}

function createDefaultMainAgent() {
  const mainAgent = {
    name: t('scenario.main_agent'),
    description: '',
    roleTag: 'main',
    prompt: '',
    tools: [],
    llmRef: '',
    delegateCond: '',
    isMain: true,
    _key: `new-${nextKey++}`,
  }
  agents.value = [mainAgent]
  selectedAgentIdx.value = 0
}
</script>

<style scoped>
.edit-dialog-body {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}

/* ── Top toolbar ── */
.edit-toolbar {
  display: flex;
  align-items: center;
  gap: 1em;
  padding: 8px 16px;
  border-bottom: 1px solid var(--border, #dcdfe6);
  flex-shrink: 0;
}

.edit-title-input {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.title-input :deep(input) {
  font-weight: 600;
  font-size: 14px;
  border: none;
  background: transparent;
  padding: 4px 8px;
}

.dir-input {
  flex: 0 0 200px;
}

.title-input :deep(input):focus {
  border: 1px solid var(--accent, #4361ee);
  background: var(--bg-primary, #fff);
  border-radius: 4px;
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

/* ── Left-right split ── */
.edit-split {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
}

/* ── Left: Agent list panel ── */
.agent-list-panel {
  width: 260px;
  min-width: 200px;
  max-width: 320px;
  border-right: 1px solid var(--border, #eee);
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary, #fafafa);
  flex-shrink: 0;
}

/* Agent list header */
.agent-list-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border, #eee);
  font-size: 11px;
  font-weight: 700;
  color: var(--text-muted, #999);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  flex-shrink: 0;
}

.agent-list-body {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}

.agent-group-title {
  padding: 4px 12px 2px;
  font-size: 10px;
  font-weight: 700;
  color: var(--text-muted, #bbb);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.agent-list-item {
  display: flex;
  align-items: center;
  padding: 6px 12px;
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: all 0.1s;
  gap: 6px;
}

.agent-list-item:hover {
  background: var(--bg-hover, #f0f0f0);
}

.agent-list-item.active {
  background: var(--accent-bg, #e6f0ff);
  border-left-color: var(--accent, #409eff);
}

.al-name {
  flex: 1;
  font-size: 12px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.al-badge {
  font-size: 9px;
  padding: 1px 5px;
  border-radius: 6px;
  flex-shrink: 0;
}

/* 标签色改用主题 token（原硬编码浅蓝 #f0f5ff/#2f54eb、#e6f7ff/#1890ff 在 dark/nord 下
   是近白底「亮块」，与暗色整体不协调）：main = 强调色底 + 强调色字，sub = 主题面底 + 次级字。 */
.al-badge.main {
  background: var(--accent-bg);
  color: var(--accent);
}

.al-badge.sub {
  background: var(--bg-surface);
  color: var(--text-secondary);
}

.al-remove {
  border: none;
  background: transparent;
  color: var(--text-muted, #ccc);
  cursor: pointer;
  font-size: 13px;
  padding: 0 2px;
  visibility: hidden;
  line-height: 1;
}

.agent-list-item:hover .al-remove:not(.disabled) {
  visibility: visible;
}

.al-remove:hover:not(.disabled) {
  color: var(--danger);
}

.al-remove.disabled {
  visibility: hidden !important;
  cursor: not-allowed;
}

.agent-list-empty {
  padding: 20px 12px;
  text-align: center;
  color: var(--text-muted, #bbb);
  font-size: 11px;
}

.agent-list-empty p {
  margin: 0;
}

.agent-list-footer {
  padding: 6px 12px;
  border-top: 1px solid var(--border, #eee);
  flex-shrink: 0;
}

.footer-hint {
  font-size: 10px;
  color: var(--text-muted, #bbb);
}

/* ── Right: Agent editor panel ── */
.agent-editor-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
  height: 100%;
}

/* 右侧页签（agent 编辑 / 组合后系统提示词预览）：撑满右栏，页签体可滚动 */
.scenario-right-tabs {
  flex: 1;
  min-height: 0;
}

.agent-editor-wrapper {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: var(--text-muted, #bbb);
  font-size: 12px;
  gap: 6px;
}

.empty-icon {
  font-size: 32px;
  opacity: 0.3;
}

.empty-state p {
  margin: 0;
}
</style>
