<template>
  <!-- 原语文件（*.type.md）编辑面板。
       v2（2026-10-14）：页签 = 契约分区一一对应（预览 / 基本信息 / 摘要 / 参数 / 参考 / 委托 / 正文），
       **首个默认页签 = 预览**；「基本信息」必填/默认键（name + 类型默认键）**恒显示且不可删**；
       正文含【AI 生成】+【AI 优化】+ 插入变量（**无「大纲」按钮**：默认正文 = 该类型大纲，新建时由后端 Template 预填）；描述【从正文提取】改走 LLM（zprompt-summarize）。
       保存走 SavePrimitive（后端按 v2 契约 H1 分区序列化）。 -->
  <div class="prim-panel" @input="clearAiApplied" @change="clearAiApplied">
    <!-- 顶部标题：类型标签 + 标题（可编辑，智能体模式由 AgentEditor 承载名称）+ 路径 + 未保存标记 -->
    <div class="prim-head">
      <Tag v-if="token" size="small" :type="tagType">{{ typeLabel }}</Tag>
      <Input v-if="!isAgent" v-model="form.title" class="prim-title" :placeholder="t('knowledgeList.title_ph')" />
      <span class="prim-path" :title="path">{{ path }}</span>
      <span v-if="dirty" class="prim-dirty">{{ t('common.unsaved') }}</span>
      <!-- 目标文件（当前编辑的契约 md）的系统处理：与导航右键同口径（gui.reveal / gui.open-with） -->
      <span class="prim-actions">
        <Button text size="small" :title="t('fileTree.reveal_in_explorer')" @click="handleReveal">
          <Icon name="folder-tree" :size="14" />
        </Button>
        <Button text size="small" :title="t('fileTree.open')" @click="handleOpen">
          <Icon name="new-window" :size="14" />
        </Button>
        <Button text size="small" :title="t('fileTree.open_with')" @click="handleOpenWith">
          <Icon name="link" :size="14" />
        </Button>
      </span>
    </div>

    <!-- 智能体原语（*.agent.md）渲染**智能体编辑器**（复用场景编辑的 AgentEditor），
         保存仍走原语保存通道（data-knowledge-save，见 handleSave）；其余类型走通用分区 Tabs。 -->
    <div v-if="isAgent" class="prim-agent-body">
      <AgentEditor
        :agent="agentModel"
        :kind="curLevel"
        :llm-options="llmOptions"
        :tool-groups="toolGroups"
        :all-tool-categories="allToolCategories"
        :skill-options="skillOptions"
        :resource-options="resourceOptions"
        :mcp-resources="mcpResources"
        :optimizing="optimizing"
        :show-copy="false"
        @update:agent="onAgentUpdate"
        @optimize="onAgentOptimize"
      />
    </div>

    <!-- 中间：Tabs（撑满） -->
    <Tabs v-else v-model="activeTab" :tabs="tabs" class="prim-tabs">
      <!-- 预览（第一个、默认）：直接渲染契约 md -->
      <template #preview>
        <div class="prim-tab-body">
          <MarkdownRender :content="previewMd" class="pf-md" />
        </div>
      </template>

      <!-- 基本信息：**按类型专用表单**（字段 = 该类型契约 meta 键，控件按值类型：枚举→Select、布尔→Switch、
           数字→number、文本→Input；name 由文件名决定，只读）。表单未覆盖的键（用户自建）落在下方「自定义键」KV 区，
           保存仍按 meta map 原样回写 → 往返保真。 -->
      <template #basic>
        <div class="prim-tab-body">
          <div class="pf-hint">{{ t('knowledgeList.meta_form_hint') }}</div>
          <!-- 资源可指向外部内容文件（meta `path`）：此时无「正文」，mimetype 按该文件后缀识别 -->
          <div v-if="token === 'resource'" class="pf-hint">{{ t('knowledgeList.resource_path_hint') }}</div>

          <div class="pf-fields">
            <div class="pf-field">
              <label class="pf-field-label">{{ t('knowledgeList.field_name') }}</label>
              <div class="pf-field-ctl">
                <Input :model-value="form.name" size="small" disabled />
                <span class="pf-field-hint">{{ t('knowledgeList.field_name_hint') }}</span>
              </div>
            </div>
            <div v-for="f in metaFields" :key="f.key" class="pf-field">
              <label class="pf-field-label" :title="f.key">{{ t('knowledgeList.' + f.labelKey) }}</label>
              <div class="pf-field-ctl">
                <Switch
                  v-if="f.type === 'bool'"
                  :model-value="metaFieldValue(f)"
                  @update:model-value="(v) => setMetaField(f, v)"
                />
                <Select
                  v-else-if="f.type === 'enum'"
                  :model-value="metaFieldValue(f)"
                  :options="metaFieldOptions(f)"
                  :placeholder="t('knowledgeList.field_unset')"
                  placeholder-selectable
                  @update:model-value="(v) => setMetaField(f, v)"
                />
                <Input
                  v-else
                  :model-value="metaFieldValue(f)"
                  :type="f.type === 'number' ? 'number' : 'text'"
                  size="small"
                  @update:model-value="(v) => setMetaField(f, v)"
                />
                <span v-if="f.hintKey" class="pf-field-hint">{{ t('knowledgeList.' + f.hintKey) }}</span>
              </div>
            </div>
            <div v-if="!metaFields.length" class="pf-hint">{{ t('knowledgeList.meta_no_fields') }}</div>
          </div>

          <!-- 自定义键：表单未覆盖的 meta 键（如用户自建），KV 维护；name 已在表单固定只读行 -->
          <div class="pf-custom">
            <div class="pf-hint">{{ t('knowledgeList.custom_keys_hint') }}</div>
            <div class="pf-meta-grid">
              <div class="pf-meta-head"><span>key</span><span>value</span><span></span></div>
              <div v-for="(row, i) in customRows" :key="i" class="pf-meta-row">
                <Input v-model="row.k" size="small" placeholder="key" :disabled="isLockedKey(row.k)" />
                <Input v-model="row.v" size="small" placeholder="value" />
                <Button v-if="!isLockedKey(row.k)" text size="small" @click="removeCustomRow(row)">✕</Button>
                <span v-else class="pf-lock" :title="t('knowledgeList.locked_key_hint')">🔒</span>
              </div>
              <Button text size="small" class="pf-add" @click="form.metaRows.push({ k: '', v: '' })">
                <Icon name="plus" :size="12" /> {{ t('knowledgeList.add_row') }}
              </Button>
            </div>
          </div>
        </div>
      </template>

      <!-- 摘要：LLM 从正文生成 + 变量插入 -->
      <template #summary>
        <div class="prim-tab-body">
          <div class="pf-desc-head">
            <Button size="small" text class="pf-extract" :loading="summarizing" :disabled="summarizing" @click="handleExtractDescription">
              {{ t('knowledgeList.extract_from_content') }}
            </Button>
            <PromptVariablesButton @insert="insertDescription" />
          </div>
          <Textarea
            ref="descRef"
            v-model="form.description"
            class="pf-area"
            :placeholder="t('knowledgeList.description_placeholder')"
          />
        </div>
      </template>

      <!-- 参数（tool / prompt）：JSON Schema 编辑器 -->
      <template #parameters>
        <div class="prim-tab-body">
          <div class="pf-hint">{{ t('knowledgeList.params_hint') }}</div>
          <JsonSchemaEditor
            v-if="loaded"
            :key="'jse-' + paramsVersion"
            v-model="form.parameters"
            class="pf-schema"
          />
        </div>
      </template>

      <!-- 参考（skill / agent）：skills / tools 名单（每行一个 name） -->
      <template #refs>
        <div class="prim-tab-body">
          <div class="pf-hint">{{ t('knowledgeList.refs_hint') }}</div>
          <label class="pf-ref-label">{{ t('knowledgeList.refs_skills') }}</label>
          <Textarea v-model="form.refsSkills" class="pf-ref-area" :placeholder="'debug\ncode-review'" />
          <label class="pf-ref-label">{{ t('knowledgeList.refs_tools') }}</label>
          <Textarea v-model="form.refsTools" class="pf-ref-area" :placeholder="'web_fetch\nfilesys_run'" />
          <label class="pf-ref-label">{{ t('knowledgeList.refs_resources') }}</label>
          <Textarea v-model="form.refsResources" class="pf-ref-area" :placeholder="'core-overview\nmcp://chonkpilot/resources/core-overview'" />
        </div>
      </template>

      <!-- 委托（agent） -->
      <template #delegate>
        <div class="prim-tab-body">
          <Textarea v-model="form.delegate" class="pf-area" :placeholder="t('knowledgeList.delegate_placeholder')" />
        </div>
      </template>

      <!-- 正文：AI 生成（按大纲产出）+ AI 优化（按大纲润色现有正文）+ 变量插入。
           默认正文 = 该类型大纲（新建时由后端 Template 预填），故不再有「大纲」按钮。 -->
      <template #content>
        <div class="prim-tab-body">
          <div class="pf-desc-head">
            <Button size="small" text :loading="generating" :disabled="generating" @click="openGenerate">
              <Icon name="magic-stick" :size="14" /> {{ t('knowledgeList.ai_generate') }}
            </Button>
            <Button size="small" text :loading="optimizingContent" :disabled="optimizingContent" @click="handleOptimizeContent">
              <Icon name="magic-stick" :size="14" /> {{ t('knowledgeList.ai_optimize') }}
            </Button>
            <PromptVariablesButton @insert="insertContent" />
          </div>
          <Textarea
            ref="contentRef"
            v-model="form.content"
            class="pf-area"
            :placeholder="t('knowledgeList.content_placeholder')"
          />
        </div>
      </template>
    </Tabs>

    <!-- 底部：保存 / 恢复（+ 恢复默认） -->
    <div class="prim-footer">
      <span v-if="saveError" class="pf-error">{{ saveError }}</span>
      <span v-if="aiFooterNote" class="pf-ai-note">{{ aiFooterNote }}</span>
      <Button
        v-if="upperLevels.length > 0"
        size="small"
        :disabled="!upperSource"
        :title="upperSource ? t('common.restore_default_hint') : t('common.restore_default_none')"
        @click="handleRestoreDefault"
      >{{ t('common.restore_default') }}</Button>
      <Button size="small" :loading="saving" @click="handleSave">{{ t('common.save') }}</Button>
      <Button size="small" @click="handleRestore">{{ t('common.restore') }}</Button>
    </div>

    <!-- AI 生成弹框 -->
    <div v-if="genOpen" class="pf-modal-overlay" @click.self="genOpen = false">
      <div class="pf-modal">
        <div class="pf-modal-head">{{ t('knowledgeList.ai_generate_title') }}</div>
        <div class="pf-modal-body">
          <div class="pf-hint">{{ t('knowledgeList.ai_generate_hint') }}</div>
          <Textarea v-model="genText" class="pf-gen-area" :placeholder="t('knowledgeList.ai_generate_input')" />
          <div v-if="aiError" class="pf-ai-error">{{ aiError }}</div>
        </div>
        <div class="pf-modal-footer">
          <Button size="small" @click="genOpen = false">{{ t('common.cancel') }}</Button>
          <Button size="small" type="primary" :loading="generating" :disabled="generating || !genText.trim()" @click="handleGenerate">{{ t('knowledgeList.ai_generate') }}</Button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { MarkdownRender } from '@ashlesss/markstream-vue'
import Icon from '../../components/icon/Icon.vue'
import { Input, Textarea, Button, Tag, Select, Switch, message, confirm } from '../../components/ui'
import Tabs from '../../components/ui/Tabs.vue'
import JsonSchemaEditor from '../../components/editor/JsonSchemaEditor.vue'
import { readPrimitive, savePrimitive, getKnowledgeRoot } from '../../api/knowledge'
import { revealInExplorer, openWithDefault, openWithDialog } from '../../api/file'
import { optimizeAgentPrompt, summarizeContent, suggestConfig } from '../../api/config'
import { classifyError } from '../../utils/errorMessage'
import {
  primitiveTokenOf, primitiveTagType,
  PRIMITIVE_META_DEFAULTS, PRIMITIVE_OUTLINES, hasParamsTab, hasRefsTab, buildContractMd,
} from '../../utils/primitive'
import { metaFieldsOf, isSchemaKey, fieldValueOf, fieldToMetaOf } from '../../utils/metaForm'
import { filterToolsLoadPatch, normalizeTools } from '../../utils/agentToolFilter'
import { loadLlmOptions, loadToolGroups, loadSkillOptions, loadResourceOptions, loadMcpResources } from '../../utils/agentAssets'
import AgentEditor from '../scenario/AgentEditor.vue'
import PromptVariablesButton from '../../components/common/PromptVariablesButton.vue'
import { useVariableInsert } from '../../composables/usePromptVariables'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const props = defineProps({
  path: { type: String, required: true },
})

const { t } = useI18n()

const token = computed(() => primitiveTokenOf(props.path))
const typeLabel = computed(() => t('fileTree.type_' + token.value))
const tagType = computed(() => primitiveTagType(token.value))
// 智能体原语（*.agent.md）→ 渲染智能体编辑器（复用 AgentEditor）。
const isAgent = computed(() => token.value === 'agent')

// 目标文件（当前编辑的契约 md）的系统处理：与导航右键同口径
//（gui.reveal 在资源管理器中显示 / gui.open-with 打开与打开方式）。
function handleReveal() { revealInExplorer(props.path) }
function handleOpen() { openWithDefault(props.path) }
function handleOpenWith() { openWithDialog(props.path) }

// 锁定键 = name + 该类型默认键（必须显示、不可删）。
const lockedKeys = computed(() => ['name', ...(PRIMITIVE_META_DEFAULTS[token.value] || [])])
function isLockedKey(k) {
  const key = String(k || '').trim()
  return key === '' ? false : lockedKeys.value.includes(key)
}
const outline = computed(() => PRIMITIVE_OUTLINES[token.value] || [])

const curTab = ref('preview')
// 资源 meta `path` 指向外部内容文件 → 无「正文」（内容取自该文件，mimetype 按后缀识别）。
const hasExternalFile = computed(() => token.value === 'resource' && String(metaMap().path || '').trim() !== '')
const tabs = computed(() => {
  const out = [
    { name: 'preview', label: t('knowledgeList.tab_preview') },
    { name: 'basic', label: t('knowledgeList.tab_basic') },
    { name: 'summary', label: t('knowledgeList.tab_summary') },
  ]
  if (hasParamsTab(token.value)) out.push({ name: 'parameters', label: t('knowledgeList.tab_parameters') })
  if (hasRefsTab(token.value)) out.push({ name: 'refs', label: t('knowledgeList.tab_refs') })
  if (token.value === 'agent') out.push({ name: 'delegate', label: t('knowledgeList.tab_delegate') })
  if (!hasExternalFile.value) out.push({ name: 'content', label: t('knowledgeList.tab_content') })
  return out
})
// 当前页签：若指向的页签已被隐藏（如资源加 `path` → 无「正文」），回落到首个页签。
const activeTab = computed({
  get: () => (tabs.value.some((x) => x.name === curTab.value) ? curTab.value : tabs.value[0]?.name || 'preview'),
  set: (v) => { curTab.value = v },
})

const loaded = ref(false)
const saving = ref(false)
const saveError = ref('')
const summarizing = ref(false)
const generating = ref(false)
const optimizingContent = ref(false)
const genOpen = ref(false)
const genText = ref('')
// AI 生成整份契约（AICFG-1）：回填表单**待保存态**；记录被 AI 回填的字段（「AI 建议」提示）。
const aiApplied = ref([])
const aiNotes = ref('')
const aiError = ref('')
const pristine = ref(null) // 最近一次读取/保存后的 form JSON（dirty 基准 + 恢复目标）
// JSON Schema 编辑器重挂载标记：每次装载/恢复表单后自增（父侧 :key 变化 → 控件按新文本重建）
const paramsVersion = ref(0)

// 「恢复默认」：用户级/项目级原语可一键回填**上一级**同名原语。
const levelRoots = ref({}) // { app, user, project, prjusr } → 各级 capability 根（绝对路径）
const upperSource = ref(null) // { level, doc }：最近可回填的上一级原语（无则 null → 按钮禁用）

const form = reactive({
  name: '', title: '', metaRows: [],
  description: '', parameters: '',
  refsSkills: '', refsTools: '', refsResources: '', delegate: '', content: '',
})

// 文件名（去 `.<token>.md`）→ 原语名（name）。
const nameFromPath = computed(() => {
  const base = String(props.path || '').replace(/\\/g, '/').split('/').pop() || ''
  return base.replace(new RegExp('\\.' + token.value + '\\.md$', 'i'), '').replace(/\.md$/i, '')
})

// 变量插入（OP-12）：描述 / 正文各自的光标处插入（Textarea.$el 即 textarea）。
const descRef = ref(null)
const contentRef = ref(null)
const { insert: insertDescription } = useVariableInsert({
  getEl: () => descRef.value?.$el || null,
  getValue: () => form.description,
  setValue: (v) => { form.description = v },
})
const { insert: insertContent } = useVariableInsert({
  getEl: () => contentRef.value?.$el || null,
  getValue: () => form.content,
  setValue: (v) => { form.content = v },
})

const dirty = computed(() => {
  if (!pristine.value) return false
  return JSON.stringify(buildFormForCompare()) !== pristine.value
})

// ── 智能体模式（*.agent.md）──────────────────────────────────────
const llmOptions = ref([])
const toolGroups = ref([])
const allToolCategories = ref([])
const skillOptions = ref([])
const resourceOptions = ref([])
const mcpResources = ref([])
const optimizing = ref(false)
let optimizeAbort = null

function metaMap() {
  const m = {}
  for (const r of form.metaRows) if (r.k.trim()) m[r.k.trim()] = r.v
  return m
}

// ── 「基本信息」按类型表单（字段定义见 utils/metaForm.js）──────────
const metaFields = computed(() => metaFieldsOf(token.value))

// 表单未覆盖的键（用户自建 / 未来新增键）→「自定义键」KV 区（保留 metaRows 行引用 → 直接 v-model 写回）。
const customRows = computed(() => form.metaRows.filter((r) => !isSchemaKey(token.value, r.k)))

function metaFieldValue(f) {
  return fieldValueOf(f, metaMap())
}

// 枚举选项 → Select 的 [{ value, label }]（label 走 i18n）。
function metaFieldOptions(f) {
  return (f.options || []).map((o) => ({ value: o.value, label: t('knowledgeList.' + o.labelKey) }))
}

function setMetaField(f, v) {
  setMeta(f.key, fieldToMetaOf(f, v))
}

function removeCustomRow(row) {
  const i = form.metaRows.indexOf(row)
  if (i >= 0) form.metaRows.splice(i, 1)
}

// setMeta 写/删一条 meta：值空 → 非锁定键删行、锁定键（name + 类型默认键）留行清值（键不可删、值可为空）。
function setMeta(k, v) {
  const key = String(k || '').trim()
  if (!key) return
  const idx = form.metaRows.findIndex(r => r.k.trim() === key)
  const empty = v === '' || v === null || v === undefined
  if (empty) {
    if (idx >= 0 && !isLockedKey(key)) form.metaRows.splice(idx, 1)
    else if (idx >= 0) form.metaRows[idx].v = ''
    return
  }
  if (idx >= 0) form.metaRows[idx].v = String(v)
  else form.metaRows.push({ k: key, v: String(v) })
}

const agentFilterTools = ref(false)

// agentModel：契约文档形态 → AgentEditor 的 agent 模型（基本信息 roletag/ismain/llm；
// 参考 skills/tools/resources 落「# 参考」分区；委托落「# 委托」分区）。
const agentModel = computed(() => {
  const meta = metaMap()
  const tools = splitRefs(form.refsTools)
  return {
    name: form.title,
    description: form.description,
    roleTag: meta.roletag || '',
    isMain: String(meta.ismain || '').toLowerCase() === 'true',
    prompt: form.content,
    tools,
    skills: splitRefs(form.refsSkills),
    resources: splitRefs(form.refsResources),
    filterTools: agentFilterTools.value,
    llmRef: meta.llm || '',
    delegateCond: form.delegate,
  }
})

// onAgentUpdate：AgentEditor 编辑回写 → 契约文档 form（保存仍走 handleSave / data-knowledge-save）。
function onAgentUpdate(a) {
  if (!a) return
  form.title = a.name || ''
  form.description = a.description || ''
  form.content = a.prompt || ''
  setMeta('roletag', a.roleTag)
  setMeta('ismain', a.isMain ? 'true' : '')
  setMeta('llm', a.llmRef)
  form.refsTools = normalizeTools(a.tools).join('\n')
  form.refsSkills = normalizeTools(a.skills).join('\n')
  form.refsResources = normalizeTools(a.resources).join('\n')
  form.delegate = a.delegateCond || ''
  agentFilterTools.value = !!a.filterTools
}

// 「优化提示词」：复用既有优化链路（gui.prompt-optimise，流式回显）。
function onAgentOptimize() {
  if (optimizing.value) return
  if (!(form.content || '').trim()) {
    message.warning(t('common.input_required'))
    return
  }
  optimizing.value = true
  optimizeAbort = optimizeAgentPrompt(
    {
      title: t('scenario.optimize_title', { name: form.title || t('scenario.unnamed') }),
      useCase: t('scenario.optimize_use_case'),
      prompt: form.content,
    },
    (chunk) => { form.content = (form.content || '') + chunk },
    (prompt) => {
      optimizing.value = false
      if (prompt) form.content = prompt
    },
    (err) => {
      optimizing.value = false
      message.error(t('common.optimize_failed') + ': ' + err)
    },
  )
}

function normPath(p) {
  return String(p || '').replace(/\\/g, '/').replace(/\/+$/, '')
}

const curLevel = computed(() => {
  const p = normPath(props.path)
  if (!p) return ''
  for (const lv of ['prjusr', 'project', 'user', 'app']) {
    const r = normPath(levelRoots.value[lv])
    if (r && (p === r || p.startsWith(r + '/'))) return lv
  }
  return ''
})
const upperLevels = computed(() => {
  if (curLevel.value === 'project') return ['user', 'app']
  if (curLevel.value === 'user') return ['app']
  return []
})

// refsText → 名单数组（按行、去空、去重）。
function splitRefs(text) {
  const seen = new Set()
  const out = []
  for (const ln of String(text || '').split('\n')) {
    const v = ln.trim()
    if (v && !seen.has(v)) { seen.add(v); out.push(v) }
  }
  return out
}

// 当前表单 → v2 契约文档（保存 + 预览共用）。
function currentDoc() {
  const meta = metaMap()
  delete meta.name
  delete meta.title
  const refs = {}
  const skills = splitRefs(form.refsSkills)
  const tools = splitRefs(form.refsTools)
  const resources = splitRefs(form.refsResources)
  if (skills.length) refs.skills = skills
  if (tools.length) refs.tools = tools
  if (resources.length) refs.resources = resources
  return {
    name: form.name,
    title: form.title,
    meta,
    description: form.description,
    parameters: form.parameters,
    refs,
    delegate: form.delegate,
    content: form.content,
  }
}

const previewMd = computed(() => buildContractMd(currentDoc()))

function buildFormForCompare() {
  return { ...currentDoc() }
}

function fillForm(doc) {
  aiApplied.value = [] // 装载/恢复后清「AI 建议」提示（待保存态被整体重写）
  aiNotes.value = ''
  const d = doc || {}
  form.name = d.name || nameFromPath.value
  form.title = d.title || ''
  const meta = d.meta || {}
  const rows = []
  for (const k of lockedKeys.value) {
    rows.push({ k, v: k === 'name' ? form.name : String(meta[k] ?? '') })
  }
  for (const k of Object.keys(meta)) {
    if (!lockedKeys.value.includes(k)) rows.push({ k, v: String(meta[k] ?? '') })
  }
  form.metaRows = rows
  form.description = d.description || ''
  form.parameters = d.parameters || ''
  const refs = d.refs || {}
  form.refsSkills = (refs.skills || []).join('\n')
  form.refsTools = (refs.tools || []).join('\n')
  form.refsResources = (refs.resources || []).join('\n')
  form.delegate = d.delegate || ''
  form.content = d.content || ''
  paramsVersion.value += 1
  agentFilterTools.value = filterToolsLoadPatch({ tools: refs.tools }).filterTools
}

function applyDoc(doc) {
  fillForm(doc)
  pristine.value = JSON.stringify(buildFormForCompare())
}

async function load() {
  try {
    const res = await readPrimitive(props.path)
    const doc = (res && res.doc) || {}
    applyDoc(doc)
    loaded.value = true
    saveError.value = ''
  } catch (e) {
    loaded.value = true
    saveError.value = (e && e.message) || t('knowledgeList.load_failed')
  }
}

async function loadLevelRoots() {
  const out = {}
  for (const k of ['prjusr', 'project', 'user', 'app']) {
    try {
      const r = await getKnowledgeRoot(k)
      out[k] = (r && r.root) || ''
    } catch (_) {
      out[k] = ''
    }
  }
  levelRoots.value = out
}

async function resolveUpperSource() {
  upperSource.value = null
  const levels = upperLevels.value
  const p = normPath(props.path)
  const root = normPath(levelRoots.value[curLevel.value])
  if (levels.length === 0 || !root || !p.startsWith(root + '/')) return
  const rel = p.slice(root.length + 1)
  for (const lv of levels) {
    const r = normPath(levelRoots.value[lv])
    if (!r) continue
    try {
      const res = await readPrimitive(`${r}/${rel}`)
      if (res && res.doc) {
        upperSource.value = { level: lv, doc: res.doc }
        return
      }
    } catch (_) { /* 该级无同名原语 → 继续下一级 */ }
  }
}

function handleRestoreDefault() {
  const src = upperSource.value
  if (!src) {
    message.info(t('common.restore_default_none'))
    return
  }
  fillForm(src.doc || {})
  saveError.value = ''
  message.info(t('common.restore_default_done', { level: t('fileTree.kb_level_' + src.level) }))
}

// 「从正文提取」摘要：走 LLM（zprompt-summarize）；描述非空时先确认再覆盖。
async function handleExtractDescription() {
  if (!(form.content || '').trim()) {
    message.warning(t('knowledgeList.extract_no_content'))
    return
  }
  if ((form.description || '').trim()) {
    try {
      await confirm(t('knowledgeList.extract_confirm'), t('knowledgeList.extract_from_content'))
    } catch (_) {
      return
    }
  }
  summarizing.value = true
  summarizeContent(
    { kind: token.value, title: form.title, content: form.content },
    (summary) => {
      summarizing.value = false
      if (summary) form.description = summary
    },
    (err) => {
      summarizing.value = false
      message.error(t('knowledgeList.summarize_failed') + ': ' + err)
    },
  )
}

function openGenerate() {
  genText.value = ''
  aiError.value = ''
  genOpen.value = true
}

// 用户手填即清「AI 建议」提示（由根节点 @input/@change 原生事件冒泡触发；零 watch）。
function clearAiApplied() {
  if (aiApplied.value.length) aiApplied.value = []
  if (aiNotes.value) aiNotes.value = ''
}

// 底部「AI 建议」提示：已回填字段清单 +（可选）AI 注记。
const aiFooterNote = computed(() => {
  const parts = []
  if (aiApplied.value.length) parts.push(t('knowledgeList.ai_applied_note', { fields: aiApplied.value.join(', ') }))
  if (aiNotes.value) parts.push(aiNotes.value)
  return parts.join(' · ')
})

// 把 config.suggest 回填到**待保存态**（只改 form；落库仍走 handleSave → data-knowledge-save）。
// 只认 tool kind 白名单字段：title/description/parameters/content/meta/refs/delegate。
function applyContractSuggest(fields) {
  const applied = []
  if (typeof fields.title === 'string') { form.title = fields.title; applied.push('title') }
  if (typeof fields.description === 'string') { form.description = fields.description; applied.push('description') }
  if (typeof fields.parameters === 'string') {
    form.parameters = fields.parameters
    paramsVersion.value += 1 // 触发 JsonSchemaEditor 按新文本重建
    applied.push('parameters')
  }
  if (typeof fields.content === 'string') { form.content = fields.content; applied.push('content') }
  if (fields.meta && typeof fields.meta === 'object' && !Array.isArray(fields.meta)) {
    for (const [k, v] of Object.entries(fields.meta)) setMeta(k, v === null || v === undefined ? '' : String(v))
    applied.push('meta')
  }
  const refs = fields.refs
  if (refs && typeof refs === 'object' && !Array.isArray(refs)) {
    if (Array.isArray(refs.skills)) form.refsSkills = splitRefs(refs.skills.join('\n')).join('\n')
    if (Array.isArray(refs.tools)) {
      form.refsTools = splitRefs(refs.tools.join('\n')).join('\n')
      agentFilterTools.value = filterToolsLoadPatch({ tools: refs.tools }).filterTools
    }
    if (Array.isArray(refs.resources)) form.refsResources = splitRefs(refs.resources.join('\n')).join('\n')
    if (refs.skills || refs.tools || refs.resources) applied.push('refs')
  }
  if (typeof fields.delegate === 'string') { form.delegate = fields.delegate; applied.push('delegate') }
  aiApplied.value = applied
}

// 「AI 生成」整份契约（AICFG-1）：弹框输入需求 → config.suggest（kind=tool）→ 回填表单**待保存态**
// （title/description/parameters/content/meta/refs/delegate）；落库仍由【保存】走 data-knowledge-save。
async function handleGenerate() {
  const text = genText.value.trim()
  if (!text) {
    message.warning(t('common.input_required'))
    return
  }
  if (generating.value) return
  if ((form.content || '').trim() || (form.title || '').trim() || (form.description || '').trim()) {
    try {
      await confirm(t('knowledgeList.ai_generate_confirm'), t('knowledgeList.ai_generate'))
    } catch (_) {
      return
    }
  }
  generating.value = true
  aiError.value = ''
  aiNotes.value = ''
  try {
    const res = await suggestConfig('tool', text, {
      title: form.title, description: form.description,
      parameters: form.parameters, content: form.content,
    })
    applyContractSuggest(res.fields || {})
    aiNotes.value = res.notes || ''
    genOpen.value = false
    message.success(t('knowledgeList.ai_generate_done'))
  } catch (e) {
    const msg = (e && e.message) || String(e)
    if (e && e.kind === 'invalid') {
      aiError.value = t('knowledgeList.ai_generate_input_required')
    } else {
      const cls = classifyError(msg)
      aiError.value = t(cls.key, cls.params)
    }
  } finally {
    generating.value = false
  }
}

// 「AI 优化」正文：按「类型 + 大纲」润色/补全**现有正文**（复用 prompt-optimise 流式链路）。
// 与「AI 生成」同链路，差别在输入 = 现有正文（而非用户要点）。
async function handleOptimizeContent() {
  if (!(form.content || '').trim()) {
    message.warning(t('knowledgeList.optimize_no_content'))
    return
  }
  if (optimizingContent.value || generating.value) return
  optimizingContent.value = true
  const useCase = '契约类型：' + token.value + '\n正文大纲：\n' + outline.value.join('\n')
  const original = form.content
  form.content = ''
  optimizeAbort = optimizeAgentPrompt(
    { title: form.title || form.name, useCase, prompt: original },
    (chunk) => { form.content = (form.content || '') + chunk },
    (p) => { optimizingContent.value = false; if (p) form.content = p },
    (err) => { optimizingContent.value = false; message.error(t('common.optimize_failed') + ': ' + err) },
  )
}

async function handleSave() {
  if (!props.path) return
  saving.value = true
  saveError.value = ''
  try {
    await savePrimitive(props.path, currentDoc())
    message.success(t('common.saved'))
    await load()
  } catch (e) {
    saveError.value = (e && e.message) || t('knowledgeList.save_failed')
    message.error(t('knowledgeList.save_failed'))
  } finally {
    saving.value = false
  }
}

function handleRestore() {
  if (!pristine.value) return
  try {
    const snap = JSON.parse(pristine.value)
    fillForm(snap)
    saveError.value = ''
    message.info(t('common.restored'))
  } catch (e) {
    message.error(t('knowledgeList.restore_failed'))
  }
}

function onFileChanged(data) {
  if (!data || Array.isArray(data.children)) return
  const p = data && (data.path || data.file)
  if (!p || String(p).replace(/\\/g, '/') !== props.path) return
  const op = data.operation || data.op
  if (op === 'remove' || op === 'deleted') return
  if (dirty.value) return
  load()
}

const _unsubs = []
onMounted(async () => {
  await load()
  _unsubs.push(mq.on(EventNames.fileChanged, onFileChanged))
  await loadLevelRoots()
  await resolveUpperSource()
  if (isAgent.value) {
    const [lo, tg, so, ro, mr] = await Promise.all([
      loadLlmOptions(t),
      loadToolGroups(curLevel.value, t),
      loadSkillOptions(curLevel.value, t),
      loadResourceOptions(curLevel.value, t),
      loadMcpResources(t),
    ])
    llmOptions.value = lo
    toolGroups.value = tg
    allToolCategories.value = tg
    skillOptions.value = so
    resourceOptions.value = ro
    mcpResources.value = mr
  }
})
onUnmounted(() => {
  for (const fn of _unsubs) fn()
  _unsubs.length = 0
  if (optimizeAbort) { optimizeAbort(); optimizeAbort = null }
})
</script>

<style scoped>
.prim-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  position: relative;
}
.prim-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--border);
  background: var(--bg-secondary);
}
.prim-title {
  width: 240px;
  flex-shrink: 0;
}
.prim-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text-muted);
  font-family: var(--font-mono, Consolas, monospace);
}
.prim-dirty {
  font-size: 11px;
  color: var(--warning, #e6a23c);
}
.prim-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}
.prim-tabs {
  flex: 1;
  min-height: 0;
}
.prim-agent-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.prim-tab-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.prim-tab-body .pf-area {
  flex: 1;
  min-height: 0;
  resize: none;
  font-size: 13px;
  line-height: 1.5;
}
.pf-desc-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.pf-schema {
  min-height: 0;
}
.pf-md {
  font-size: 13px;
  line-height: 1.6;
}
.pf-meta-grid {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 860px;
}
/* 「基本信息」按类型表单：标签列 + 控件列（控件 + 说明） */
.pf-fields {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-width: 860px;
}
.pf-field {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.pf-field-label {
  flex: 0 0 120px;
  padding-top: 5px;
  font-size: 12px;
  color: var(--text-primary);
}
.pf-field-ctl {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}
.pf-field-ctl > .b-input { flex: 1 1 auto; min-width: 0; }
.pf-field-ctl > .b-select { flex: 0 0 200px; }
.pf-field-hint {
  flex: 0 1 auto;
  font-size: 11px;
  color: var(--text-muted);
  opacity: 0.75;
}
.pf-custom {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.pf-meta-head, .pf-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.pf-meta-head span { flex: 1; font-size: 12px; color: var(--text-muted); }
.pf-meta-head span:last-child { flex: 0 0 28px; }
.pf-meta-row > :first-child { flex: 1; }
.pf-meta-row > :nth-child(2) { flex: 1.4; }
.pf-meta-row > :last-child { flex: 0 0 28px; }
.pf-lock {
  text-align: center;
  font-size: 12px;
  opacity: 0.7;
}
.pf-add {
  align-self: flex-start;
}
.pf-hint {
  font-size: 11px;
  color: var(--text-muted);
  opacity: 0.75;
}
.pf-ref-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--text-primary);
  flex-shrink: 0;
}
.pf-ref-area {
  min-height: 96px;
  resize: vertical;
  font-size: 13px;
  font-family: var(--font-mono, Consolas, monospace);
}
.prim-footer {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-top: 1px solid var(--border);
  flex-shrink: 0;
}
.pf-error {
  flex: 1;
  font-size: 12px;
  color: var(--danger, #f56c6c);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* AI 生成整份契约（AICFG-1）：底部「AI 建议」提示 + 弹框内错误行 */
.pf-ai-note {
  flex: 1;
  font-size: 12px;
  color: var(--accent);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pf-ai-error {
  font-size: 12px;
  line-height: 1.4;
  color: color-mix(in srgb, var(--danger) 70%, var(--text-primary));
}
.pf-modal-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.35);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 20;
}
.pf-modal {
  width: min(560px, calc(100% - 40px));
  max-height: calc(100% - 40px);
  display: flex;
  flex-direction: column;
  background: var(--bg-primary, #fff);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.2);
}
.pf-modal-head {
  padding: 10px 14px;
  font-size: 14px;
  font-weight: 600;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.pf-modal-body {
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  min-height: 0;
}
.pf-gen-area {
  min-height: 140px;
  resize: vertical;
  font-size: 13px;
}
.pf-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 10px 14px;
  border-top: 1px solid var(--border);
  flex-shrink: 0;
}
</style>
