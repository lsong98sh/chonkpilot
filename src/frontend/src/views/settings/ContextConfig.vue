<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">{{ $t('projectConfig.context') }}</span>
      <div class="tab-actions">
        <!-- ⑤ dirty 标记 + 保存中禁用重复提交（保存按钮仍为唯一提交入口，时机不变） -->
        <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" type="primary" :disabled="!dirty" :loading="saving" v-mq:[EventNames.contextSave].click>{{ $t('projectConfig.save') }}</Button>
      </div>
    </div>
    <form class="form-layout">
      <!-- 第一块：记忆配置（「记忆库」与「用户偏好」为同级开关，各自下辖子项；类别来自 data-memory-list） -->
      <section class="cfg-section">
        <h3 class="section-title">{{ $t('projectConfig.memory_section') }}</h3>

        <!-- 开关一：记忆库（关闭时其下子项统一禁用） -->
        <div class="form-item form-item-12">
          <div class="switch-row">
            <label class="form-label">{{ $t('projectConfig.memory_enabled') }}</label>
            <Switch :model-value="memoryEnabled" @update:model-value="onMemoryEnabledChange" />
          </div>
          <div class="field-hint">{{ $t('projectConfig.memory_enabled_hint') }}</div>
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.memory_min_turn_tokens') }}</label>
          <Input v-model.number="memoryMinTurnTokens" type="number" :min="0" :step="50" :disabled="!memoryEnabled" @update:model-value="refreshDirty" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.memory_category_max_tokens') }}</label>
          <Input v-model.number="memoryCategoryMaxTokens" type="number" :min="0" :step="100" :disabled="!memoryEnabled" @update:model-value="refreshDirty" />
          <div class="field-hint">{{ $t('projectConfig.memory_category_max_tokens_hint') }}</div>
        </div>
        <!-- 手动沉淀（memory.flush）：显式触发一次，不受最小轮次 Token 阈值限制 -->
        <div class="form-item form-item-full">
          <div class="switch-row">
            <label class="form-label">{{ $t('memoryIO.flush_section') }}</label>
            <Button size="small" :loading="flushing" :disabled="!memoryEnabled || flushing" @click="flushMemory">{{ $t('memoryIO.flush') }}</Button>
          </div>
          <div class="field-hint">{{ $t('memoryIO.flush_hint') }}</div>
        </div>
        <div class="form-item form-item-full" v-if="memoryEnabled">
          <!-- 类别表头：新增类别入口（新增后出现在清单并可编辑内容） -->
          <div class="mem-table-head">
            <span class="form-label">{{ $t('projectConfig.memory_col_category') }}</span>
            <Button size="small" :disabled="!memoryEnabled" @click="addCategory">{{ $t('projectConfig.memory_add_category') }}</Button>
          </div>
          <Table
            :columns="memoryColumns"
            :data="projectCategories"
            :empty-text="$t('projectConfig.memory_empty')"
            :row-class="memoryRowClass"
            size="small"
          >
            <template #category="{ row }">
              <span>{{ row.category }}</span>
              <span v-if="isOverThreshold(row)" class="over-hint">{{ $t('projectConfig.memory_over_hint') }}</span>
            </template>
            <template #level="{ row }">{{ levelText(row.level) }}</template>
            <template #enabled="{ row }">
              <Switch :model-value="categoryEnabled(row)" :disabled="!memoryEnabled" @update:model-value="v => onCategoryToggle(row, v)" />
            </template>
            <template #action="{ row }">
              <div class="mem-ops">
                <Button size="small" text :disabled="!memoryEnabled" @click="openPromptEditor(row)">{{ $t('projectConfig.memory_edit_prompt') }}</Button>
                <Button size="small" text :disabled="!memoryEnabled" @click="openContentEditor(row)">{{ $t('projectConfig.memory_edit_content') }}</Button>
                <Button size="small" text :disabled="!memoryEnabled" @click="clearCategory(row)">{{ $t('memoryIO.clear') }}</Button>
                <Button v-if="isCustomCategory(row)" size="small" text :disabled="!memoryEnabled" @click="deleteCategory(row)">{{ $t('common.delete') }}</Button>
              </div>
            </template>
          </Table>
        </div>

        <!-- 开关二：用户偏好（与记忆库同级；子项 = 预估 Token + 内容编辑）——行保留（关闭时灰显），
             内容行随类别清单渲染（关闭态不取清单 → 不渲染） -->
        <div class="form-item form-item-12">
          <div class="switch-row">
            <label class="form-label">{{ $t('projectConfig.memory_user_pref') }}</label>
            <Switch :model-value="userPrefEnabled" :disabled="!memoryEnabled" @update:model-value="onUserPrefChange" />
          </div>
          <div class="field-hint">{{ $t('projectConfig.memory_user_pref_hint') }}</div>
        </div>
        <div class="form-item form-item-12" v-if="userPref">
          <label class="form-label">{{ $t('projectConfig.memory_col_tokens') }}</label>
          <div class="switch-row">
            <span class="mem-token" :class="{ 'is-over': isOverThreshold(userPref) }">{{ userPref.tokens }}</span>
            <span v-if="isOverThreshold(userPref)" class="over-hint">{{ $t('projectConfig.memory_over_hint') }}</span>
            <Button size="small" text :disabled="!memoryEnabled" @click="openPromptEditor(userPref)">{{ $t('projectConfig.memory_edit_prompt') }}</Button>
            <Button size="small" text :disabled="!memoryEnabled" @click="openContentEditor(userPref)">{{ $t('projectConfig.memory_edit_content') }}</Button>
            <Button size="small" text :disabled="!memoryEnabled" @click="clearCategory(userPref)">{{ $t('memoryIO.clear') }}</Button>
          </div>
        </div>

        <!-- 记忆区底部：记忆总 token 数（各启用类别 tokens 之和）——**只读展示**。
             可点击的主入口（分类列表 → 内容编辑弹框）已迁至窗口状态栏底部（StatusBar），
             此处不再重复入口。 -->
        <div class="form-item form-item-full mem-total-row" v-if="memoryEnabled">
          <span class="mem-total">
            <span class="mem-total-label">{{ $t('projectConfig.memory_total_tokens') }}</span>
            <span class="mem-total-value">{{ memoryTotalTokens }}</span>
          </span>
          <div class="field-hint">{{ $t('projectConfig.memory_total_hint') }}</div>
        </div>
      </section>

      <!-- 第二块：上下文压缩配置 -->
      <section class="cfg-section">
        <h3 class="section-title">{{ $t('projectConfig.compress_section') }}</h3>
        <!-- 保留完整对话 = **本轮恒保留**（下限），更早的轮「累计完整态 token > M」或「已纳入轮数 >= N」
             的那一轮起全部进入**简化区**（口径 V/W；两条件均 0 = 不压缩；简化区 token ≥ T 才生成摘要） -->
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.keep_full_max_turns') }}</label>
          <Input v-model.number="keepFullMaxTurns" type="number" :min="0" :max="50" :step="1" @update:model-value="refreshDirty" />
          <!-- 口径 W：<0 = 非法（显式提示，不静默）；0 = 该条件不启用 -->
          <div class="field-hint" :class="{ 'hint-error': keepFullMaxTurnsInvalid }">
            {{ keepFullMaxTurnsInvalid ? $t('projectConfig.keep_full_max_turns_invalid') : $t('projectConfig.keep_full_max_turns_hint') }}
          </div>
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.keep_full_max_tokens') }}</label>
          <Input v-model.number="keepFullMaxTokens" type="number" :min="0" :max="1000000" :step="1000" @update:model-value="refreshDirty" />
          <!-- 口径 W：<0 = 非法（显式提示，不静默）；0 = 该条件不启用 -->
          <div class="field-hint" :class="{ 'hint-error': keepFullMaxTokensInvalid }">
            {{ keepFullMaxTokensInvalid ? $t('projectConfig.keep_full_max_tokens_invalid') : $t('projectConfig.keep_full_max_tokens_hint') }}
          </div>
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.compress_threshold') }}</label>
          <Input v-model.number="compressTokenThreshold" type="number" :min="10000" :max="500000" :step="10000" @update:model-value="refreshDirty" />
          <div class="quick-thresholds">
            <Button
              v-for="b in QUICK_THRESHOLDS"
              :key="b.label"
              size="small"
              class="quick-threshold-btn"
              v-mq:[EventNames.contextQuickThreshold].click="{ value: b.value }"
            >{{ b.label }}</Button>
          </div>
        </div>
        <div class="form-item form-item-full">
          <div class="prompt-editor-header">
            <label class="form-label">{{ $t('projectConfig.summary_prompt_edit') }}</label>
            <!-- 编辑 = 弹框（A2：弹框内编辑 + 保存即关 + 弹框自带优化）；按钮样式与记忆分类行一致（text） -->
            <Button size="small" text @click="openSummaryEditor">{{ $t('common.edit') }}</Button>
          </div>
          <!-- 来源标注 + 取消覆盖入口（只读展示已移除 → 内容仅在编辑弹框内查看） -->
          <div class="switch-row summary-prompt-source">
            <span class="field-hint">{{ summaryOverride ? $t('projectConfig.summary_prompt_source_override') : $t('projectConfig.summary_prompt_source_inherit') }}</span>
            <Button size="small" text :disabled="!summaryOverride" @click="resetSummaryOverride">{{ $t('projectConfig.summary_prompt_reset') }}</Button>
          </div>
        </div>
        <!-- 压缩内容可查（数据来源 = 当前会话快照：data-snapshot-get，压缩产物唯一落点） -->
        <div class="form-item form-item-full">
          <div class="prompt-editor-header">
            <label class="form-label">{{ $t('memoryIO.records_title') }}</label>
            <div class="tab-actions">
              <Button size="small" :loading="recordsLoading" @click="loadCompressRecords">{{ $t('memoryIO.records_refresh') }}</Button>
            </div>
          </div>
          <div class="field-hint">{{ $t('memoryIO.records_note') }}</div>
          <ul class="compress-records" v-if="compressRecords.length > 0">
            <li v-for="(r, i) in compressRecords" :key="i" class="compress-record">
              <div class="record-meta">
                <span>{{ $t('memoryIO.records_range_value', { turn: r.turn }) }}</span>
                <span class="record-kept">{{ $t('memoryIO.records_kept', { count: r.kept }) }}</span>
                <Button size="small" text @click="toggleRecord(i)">{{ recordExpanded(i) ? $t('memoryIO.records_collapse') : $t('memoryIO.records_expand') }}</Button>
              </div>
              <pre class="record-text">{{ recordExpanded(i) ? r.text : brief(r.text) }}</pre>
            </li>
          </ul>
          <div v-else class="field-hint records-empty">{{ recordsEmptyText }}</div>
        </div>
        <p class="form-description">
          {{ $t('projectConfig.context_desc') }}<br>
          {{ $t('projectConfig.context_calc') }}<br>
          {{ $t('projectConfig.context_example') }}<br>
          {{ $t('projectConfig.context_async') }}
        </p>
        <!-- 压缩区末尾提示（2026-09-24 用户口径，zh-CN 逐字）：纯展示，无功能变更 -->
        <p class="form-description">
          {{ $t('projectConfig.context_tip_cache_billing') }}<br>
          {{ $t('projectConfig.context_tip_compress_llm') }}
        </p>
      </section>
    </form>
  </div>
</template>

<script setup>
import { ref, computed, h, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input, Button, Switch, Table, message, confirm, promptInput } from '../../components/ui'
import { dialog } from '../../components/dialog'
import TextEditDialog from '../../components/common/TextEditDialog.vue'
import { getAllConfig, setConfig, setConfigs, getPrompt, setPrompt, getUserConfig, saveUserConfig, deleteConfig, resetUserKey } from '../../api/config'
import { readPrimitive } from '../../api/knowledge'
import { getActiveSessionID } from '../../api/session'
import dataClient, { onDataRefresh, dataRequest } from '../../utils/dataClient'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'
import { useMemoryCategories, DEFAULT_MEMORY_PROMPT } from '../../composables/useMemoryCategories'
import { usePrjConfigRefresh } from '../../composables/usePrjConfigRefresh'

const { t } = useI18n()

// ⑤ 手动保存：dirty 由「本地态 JSON 快照 vs 已保存态快照」比对得出（无改动 → 【保存】禁用）；
// 数值项 / 开关只改本地态，点【保存】才落库（弹窗编辑仍即时落库，见下）。
const { dirty, markDirty, markSaved } = useUnsavedMark()
const saving = ref(false)

// 快速阈值按钮（FP L212）：64K/128K/256K/512K/1M
const QUICK_THRESHOLDS = [
  { label: '64K', value: 64 * 1024 },
  { label: '128K', value: 128 * 1024 },
  { label: '256K', value: 256 * 1024 },
  { label: '512K', value: 512 * 1024 },
  { label: '1M', value: 1024 * 1024 },
]

// 系统默认（与 compress 插件 DefaultOptions 一致：
// keep_full_max_turns=10 / keep_full_max_tokens=24000 / compress_token_threshold=20000）
const keepFullMaxTurns = ref(10)
const keepFullMaxTokens = ref(24000)
const compressTokenThreshold = ref(20000)
// 口径 W（2026-09-25）：`< 0` = 非法值（前端显式提示，不静默；后端按「不启用」处理并留日志）；
// `0` = 该条件不启用（只用另一条件）；**两者均为 0 = 不压缩**（保留全量、不生成摘要）。
const keepFullMaxTurnsInvalid = computed(() => Number(keepFullMaxTurns.value) < 0)
const keepFullMaxTokensInvalid = computed(() => Number(keepFullMaxTokens.value) < 0)
const hasIllegalBounds = computed(() => keepFullMaxTurnsInvalid.value || keepFullMaxTokensInvalid.value)

// warnIllegalBounds 保存/落库前提示非法值（不阻断保存：后端按不启用处理，但用户必须知情）。
function warnIllegalBounds() {
  if (hasIllegalBounds.value) {
    message.warning(t('projectConfig.keep_full_max_illegal_warning'))
  }
}
const summarizePrompt = ref('')
// 总结提示词来源：true = 项目级覆盖（capability/knowledge/prompts/summary.prompt.md 存在），
// false = 继承系统级/内置默认（后端 load 回落链：项目文件 → 系统文件 → 旧 prj config → 内置默认）。
const summaryOverride = ref(false)
// 加载时的有效值快照：页保存按钮据「未做覆盖且内容未改」提示"未做覆盖（沿用继承值）"。
const summaryLoadedValue = ref('')
const unsubs = []

// ── 记忆配置 ──
// 用户偏好 = 唯一用户级类别（名 = 后端 persist.MemoryUserCategory，落 ~/.chonkpilot/用户偏好.md）
const USER_PREF_CATEGORY = '用户偏好'
// 预置项目级类别（与后端 persist.memoryCategorySpecs 一致；预置不可删除，仅可清空内容）
const PRESET_CATEGORIES = ['项目概要', '共同库', '开发规范', '构建发布规则', '接口库', '测试规范', '典型参照', '用户决策']
// 记忆类别共享状态（与状态栏「记忆总 token 数」入口同源）：开关/清单/总量 + 内容编辑弹框
const {
  enabled: memoryEnabled,
  list: memoryList,
  total: memoryTotalTokens,
  setEnabled: setMemoryEnabled,
  load: loadMemoryCategories,
  openContentEditor,
  closeEditorFor,
} = useMemoryCategories()
const memoryMinTurnTokens = ref(200)
const memoryCategoryMaxTokens = ref(2000)
// 用户偏好开关（存储键 memory.category.用户偏好，走既有 prj-config 读写；缺失 → 默认启用）
const userPrefEnabled = ref(true)
// 项目配置平铺 map（类别开关 memory.category.<类别名> 缺失 → 默认启用）
const cfgMap = ref({})
// 类别沉淀提示词：项目级 8 类落 prj `memory.prompt.<类别名>`（在 cfgMap 内）；
// 用户偏好（唯一用户级）落 usr 自由键 `memory_prompts`（JSON 对象字符串，见 userPrefPrompts）。
// 键前缀 / 键名与 Go 侧同字面量（chonkpilot-plugin-memory/memory.go memoryPromptPrefix / userMemoryPromptsKey）。
const MEMORY_PROMPT_PREFIX = 'memory.prompt.'
const USER_MEMORY_PROMPTS_KEY = 'memory_prompts'
// usr `memory_prompts` 解析后的 map（类别名 → 提示词全文；未配置 → 空对象）
const userPrefPrompts = ref({})

// parsePromptMap 解析 usr `memory_prompts`（JSON 对象字符串）：非法 JSON / 数组 / 非对象 → 空对象。
function parsePromptMap(raw) {
  if (raw && typeof raw === 'object' && !Array.isArray(raw)) return raw
  if (typeof raw === 'string' && raw.trim() !== '') {
    try {
      const m = JSON.parse(raw)
      if (m && typeof m === 'object' && !Array.isArray(m)) return m
    } catch (_) { /* 非法 JSON → 视为未配置（回落内置默认） */ }
  }
  return {}
}

// loadUserPrefPrompts 读 usr 自由键（data-user-config-load，既有面）：用户偏好类别的自定义提示词。
async function loadUserPrefPrompts() {
  try {
    const res = await getUserConfig()
    const cfg = res.config || {}
    userPrefPrompts.value = parsePromptMap(cfg[USER_MEMORY_PROMPTS_KEY])
  } catch (e) {
    message.error(loadFailedText(t, t('projectConfig.memory_edit_prompt'), e))
  }
}

// customPromptOf 取该类别的**自定义**提示词（未自定义 → 空串 = 将回落内置默认）。
function customPromptOf(c) {
  if (!c || !c.category) return ''
  const v = c.level === 'user'
    ? userPrefPrompts.value[c.category]
    : cfgMap.value[MEMORY_PROMPT_PREFIX + c.category]
  return typeof v === 'string' ? v : ''
}

// writeUserPrefPrompts 整表写 usr 自由键 memory_prompts（JSON 对象字符串）；全空 → 删键（回落缺省）。
async function writeUserPrefPrompts(map) {
  const next = {}
  for (const [k, v] of Object.entries(map || {})) {
    if (String(v == null ? '' : v).trim() !== '') next[k] = v
  }
  if (Object.keys(next).length === 0) {
    await resetUserKey(USER_MEMORY_PROMPTS_KEY)
    return
  }
  await saveUserConfig({ [USER_MEMORY_PROMPTS_KEY]: JSON.stringify(next) })
}

// clearMemoryPrompt 清除类别自定义提示词 → 回落内置默认（项目级删 prj 键 / 用户偏好移除 map 项）。
async function clearMemoryPrompt(c) {
  if (c.level === 'user') {
    const next = { ...userPrefPrompts.value }
    delete next[c.category]
    await writeUserPrefPrompts(next)
    userPrefPrompts.value = next
    return
  }
  const key = MEMORY_PROMPT_PREFIX + c.category
  await deleteConfig(key)
  const next = { ...cfgMap.value }
  delete next[key]
  cfgMap.value = next
}

// saveMemoryPrompt 保存类别沉淀提示词：空白 / 与内置默认相同 → 清键（回落内置默认），否则落库。
// 与总结提示词「与继承值相同则不覆盖」同口径：避免把内置默认固化为永久覆盖。
async function saveMemoryPrompt(c, text) {
  const val = String(text == null ? '' : text)
  if (val.trim() === '' || val.trim() === DEFAULT_MEMORY_PROMPT) {
    await clearMemoryPrompt(c)
    return
  }
  if (c.level === 'user') {
    const next = { ...userPrefPrompts.value, [c.category]: val }
    await writeUserPrefPrompts(next)
    userPrefPrompts.value = next
    return
  }
  const key = MEMORY_PROMPT_PREFIX + c.category
  await setConfig(key, val)
  cfgMap.value = { ...cfgMap.value, [key]: val }
}

// openPromptEditor 打开该类别「沉淀提示词」编辑弹框（复用 TextEditDialog）：
// 未自定义 → 回填内置默认 + 来源提示「当前为内置默认」；已自定义 → 回填自定义值 + 「恢复默认」入口。
function openPromptEditor(c) {
  if (!c || !c.category) return
  const custom = customPromptOf(c)
  const isCustom = custom.trim() !== ''
  const handle = dialog.show(h(TextEditDialog, {
    content: isCustom ? custom : DEFAULT_MEMORY_PROMPT,
    placeholder: t('projectConfig.memory_prompt_placeholder'),
    hint: isCustom ? t('projectConfig.memory_prompt_source_custom') : t('projectConfig.memory_prompt_source_default'),
    reset: isCustom ? {
      label: t('projectConfig.memory_prompt_reset'),
      // 恢复默认 = 清键回落内置默认 → 关闭弹框（与保存同口径：父组件负责落库与关闭）
      onClick: async () => {
        await clearMemoryPrompt(c)
        message.success(t('projectConfig.memory_prompt_reset_done'))
        handle.close()
      },
    } : null,
    optimize: {
      title: t('projectConfig.memory_prompt_optimize_title', { name: c.category }),
      useCase: t('projectConfig.memory_prompt_optimize_use_case'),
      recover: true,
    },
    onSave: async (text) => {
      await saveMemoryPrompt(c, text)
      message.success(t('projectConfig.saved'))
      handle.close()
    },
    onCancel: () => handle.close(),
  }), {
    title: t('projectConfig.memory_prompt_edit') + ' - ' + c.category,
    width: 760,
    height: 620,
    bodyClass: 'text-edit-dialog-body',
    closable: true,
  })
}

// ── 手动沉淀（memory.flush）+ 压缩内容可查（data-snapshot-get）──
// FLUSH_TOPIC = 点分相对主题（桥/服务端「点分直通总线」分支受理；memory 插件订阅应答）；
// 后端 Go 侧同字面量：chonkpilot-plugin-memory/memory.go:flushSubject。
const FLUSH_TOPIC = 'memory.flush'
// 手动沉淀可能跨多次 llm-simple（各类别并行）→ 给足等待（>插件侧单次 LLM 超时 60s）
const FLUSH_TIMEOUT = 120000
// 压缩插件回写快照的摘要前缀（chonkpilot-plugin-compress/compress.go:DoCompress）——
// 压缩产物唯一落点 = 会话快照（system 消息），前端据此识别「压缩记录」。
const COMPRESS_MARK = '[已压缩早前对话] '
// COMPRESS_EVENT = 总线 session-compress 的前端 type（61-消息一览 §6 映射：session-compress
// → llm-compress，桥/入口同表 mqTypeMap）；压缩发生（写回快照）后据此重读压缩记录。
const COMPRESS_EVENT = 'llm-compress'
const RECORD_BRIEF_CHARS = 200
const flushing = ref(false)
const compressRecords = ref([])
const recordsLoading = ref(false)
const expandedRecords = ref({})
// 无活动会话（未取到 session_id）→ 空态给专门文案（不谎称「无压缩记录」）
const hasActiveSession = ref(false)

// 用户级类别（用户偏好）与项目级类别分离：前者作为与「记忆库」同级的开关，不入项目类别表
const userPref = computed(() => memoryList.value.find(c => c.level === 'user') || null)
const projectCategories = computed(() => memoryList.value.filter(c => c.level !== 'user'))

function levelText(level) {
  if (level === 'user') return t('projectConfig.memory_level_user')
  return t('projectConfig.memory_level_project')
}

// 超阈值 → 该行标红提示（只提醒、不阻断）
function isOverThreshold(c) {
  const th = Number(memoryCategoryMaxTokens.value) || 0
  return th > 0 && Number(c.tokens) > th
}

function categoryEnabled(c) {
  const v = cfgMap.value['memory.category.' + c.category]
  return v === undefined || v === '' ? true : v === 'true'
}

// 记忆类别表列配置（自研 Table）：类别 / 级别 / 预估 Token / 启用 / 操作。
const memoryColumns = computed(() => [
  { label: t('projectConfig.memory_col_category'), prop: 'category', minWidth: 160 },
  { label: t('projectConfig.memory_col_level'), prop: 'level', width: 90 },
  { label: t('projectConfig.memory_col_tokens'), prop: 'tokens', width: 110 },
  { label: t('projectConfig.memory_col_enabled'), prop: 'enabled', width: 70, align: 'center' },
  { label: t('common.operation'), type: 'action' },
])

// 超阈值行 → 标红（沿用原 .is-over 行 class 语义）
function memoryRowClass(row) {
  return isOverThreshold(row) ? 'is-over' : ''
}

// ── 手动保存的 dirty 判定（无 watch：由各变更点显式调用 refreshDirty 重算）──
// 仅将「点【保存】才落库」的项纳入快照：压缩三项数值 + 记忆库开关/阈值 + 类别开关/用户偏好；
// 弹窗编辑（内容 / 提示词 / 总结提示词）即时落库，不进快照。
let savedState = ''
function currentState() {
  const cats = {}
  for (const c of projectCategories.value) cats[c.category] = String(categoryEnabled(c))
  cats[USER_PREF_CATEGORY] = String(userPrefEnabled.value)
  return JSON.stringify({
    turns: Number(keepFullMaxTurns.value) || 0,
    tokens: Number(keepFullMaxTokens.value) || 0,
    threshold: Number(compressTokenThreshold.value) || 0,
    enabled: !!memoryEnabled.value,
    minTurn: Number(memoryMinTurnTokens.value) || 0,
    catMax: Number(memoryCategoryMaxTokens.value) || 0,
    cats,
  })
}
// refreshDirty 重算 dirty：与已保存态一致 → 清除标记；否则标记「未保存」。
function refreshDirty() {
  if (currentState() === savedState) markSaved()
  else markDirty()
}

// readNum 读数字项：键存在且可解析（含 0 / 负数 → 该项「不启用」）→ 采用；缺失/非法 → 回落默认。
function readNum(map, key, fallback) {
  const raw = map[key]
  if (raw === undefined || raw === null || raw === '') return fallback
  const n = parseInt(raw)
  return Number.isFinite(n) ? n : fallback
}

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    cfgMap.value = c
    // 保留完整对话：**本轮恒保留**（下限），更早的轮超 N 轮或累计完整态 token 超 M 的那一轮起
    // 进入**简化区**（`keep_full_max_turns`，缺失回落旧键 `keep_full_turns` 读时兼容；
    // `keep_full_max_tokens`）；0 = 该条件不启用、两者均 0 = 不压缩（口径 V/W）；
    // 简化区摘要阈值 `compress_token_threshold`（作用域 = 简化区）。
    keepFullMaxTurns.value = readNum(c, 'keep_full_max_turns', readNum(c, 'keep_full_turns', 10))
    keepFullMaxTokens.value = readNum(c, 'keep_full_max_tokens', 24000)
    compressTokenThreshold.value = readNum(c, 'compress_token_threshold', 20000)
    setMemoryEnabled(c['memory.enabled'] === 'true')
    if (c['memory.min-turn-tokens'] !== undefined) memoryMinTurnTokens.value = parseInt(c['memory.min-turn-tokens']) || 0
    if (c['memory.category-max-tokens'] !== undefined) memoryCategoryMaxTokens.value = parseInt(c['memory.category-max-tokens']) || 2000
    userPrefEnabled.value = userPrefEnabledFromMap()
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('projectConfig.context'), e))
  }
  await loadUserPrefPrompts()
  await loadSummaryPrompt()
  // 记忆库关闭 → 不取类别清单（关闭态页面不展示类别区，且后端 list 会落盘预置文件）
  await reloadMemoryCategories()
  // 载入完成 = 已保存态：重置快照并清除 dirty（无改动 → 【保存】禁用）
  savedState = currentState()
  markSaved()
}

// reloadMemoryCategories 取记忆类别清单（共享 composable：关闭态不发 data-memory-list，
// 后端不再落盘预置文件）；加载失败 → 用户可见提示（与既有口径一致）。
async function reloadMemoryCategories() {
  try {
    await loadMemoryCategories()
  } catch (e) {
    message.error(loadFailedText(t, t('projectConfig.memory_section'), e))
  }
}

// SUMMARY_PROMPT_PATH 项目级总结提示词文件（相对知识库根 → 归属项目级；
// 与 persist 侧 summaryPromptFile 同路径：<workdir>/.chonkpilot/capability/knowledge/prompts/summary.prompt.md）。
const SUMMARY_PROMPT_PATH = 'knowledge/prompts/summary.prompt.md'

// loadSummaryPrompt 读总结提示词（有效值 + 来源）：有效值走 data-prompt-load（后端回落链）；
// 来源经知识库读项目级文件判定——读得到 = 项目级覆盖，读不到 = 继承系统级/内置默认。
async function loadSummaryPrompt() {
  try {
    const sp = await getPrompt('summary_prompt')
    summarizePrompt.value = sp.value || ''
    summaryLoadedValue.value = summarizePrompt.value
  } catch (e) {
    message.error(loadFailedText(t, t('projectConfig.summary_prompt_edit'), e))
  }
  try {
    await readPrimitive(SUMMARY_PROMPT_PATH)
    summaryOverride.value = true
  } catch (e) {
    summaryOverride.value = false // 项目级文件不存在（或读取失败）→ 继承
  }
}

// resetSummaryOverride 取消项目级覆盖：删除项目级文件（data-prompt-delete 既有面）→ 回落继承值。
// 属「即时落库」动作（不由页面【保存】提交）→ 只重算 dirty，不清除页面未保存改动。
async function resetSummaryOverride() {
  try {
    await dataClient.remove('prompt', 'summary_prompt')
    await loadSummaryPrompt()
    refreshDirty()
    message.success(t('projectConfig.summary_prompt_reset_done'))
  } catch (e) {
    message.error(t('projectConfig.prompt_save_failed') + ': ' + (e.message || ''))
  }
}

// 用户偏好开关取值（缺 key → 默认启用，与后端 categoryEnabled 口径一致）
function userPrefEnabledFromMap() {
  const v = cfgMap.value['memory.category.' + USER_PREF_CATEGORY]
  return v === undefined || v === '' ? true : v === 'true'
}

// ── 本地态变更（不落库；点【保存】才提交）：各变更点只改本地值 + 重算 dirty ──

// 记忆库总开关：由关到开 → 取类别清单（此前关闭态未取；只读，不影响落库时机）。
function onMemoryEnabledChange(v) {
  setMemoryEnabled(v)
  reloadMemoryCategories().then(refreshDirty)
}

// 单类别开关：只改本地 cfgMap（memory.category.<类别名> 键，项目级/用户偏好同口径）。
function setCategoryEnabled(category, v) {
  const key = 'memory.category.' + category
  cfgMap.value = { ...cfgMap.value, [key]: String(v) }
  refreshDirty()
}

function onCategoryToggle(c, v) {
  setCategoryEnabled(c.category, v)
}

// 用户偏好开关（键与项目类别同构）
function onUserPrefChange(v) {
  userPrefEnabled.value = v
  refreshDirty()
}

// ── 类别内容编辑（弹框）：读内容 / 保存 / 分类列表 / 总量汇总均为共享 composable
//（useMemoryCategories）实现，与状态栏「记忆总 token 数」入口同源（A4 / P1 迁移）。 ──

// 自定义类别 = 项目级且非预置（仅此类可删除；预置仅可清空内容）
function isCustomCategory(c) {
  return c.level !== 'user' && !PRESET_CATEGORIES.includes(c.category)
}

// 新增类别：输入名称 → data-memory-save（空内容）建类别 → 重载清单并打开内容编辑弹框
async function addCategory() {
  let name
  try {
    name = await promptInput(t('projectConfig.memory_new_category_placeholder'))
  } catch { return }
  if (name === null || name === undefined) return
  name = String(name).trim()
  if (!name) {
    message.warning(t('projectConfig.memory_category_required'))
    return
  }
  try {
    await dataClient.save('memory', { category: name, content: '' })
    message.success(t('projectConfig.saved'))
    await reloadMemoryCategories()
    const created = memoryList.value.find(c => c.category === name)
    if (created) openContentEditor(created)
  } catch (e) {
    message.error(t('projectConfig.save_failed') + ': ' + (e.message || ''))
  }
}

// 删除自定义类别：二次确认 → data-memory-delete 移除清单项 + 内容文件
async function deleteCategory(c) {
  try {
    await confirm(t('projectConfig.memory_delete_confirm', { name: c.category }), t('common.confirm_delete_title'))
  } catch { return }
  try {
    await dataRequest('memory', 'delete', { data: { category: c.category } })
    message.success(t('projectConfig.deleted'))
    closeEditorFor(c.category) // 内容编辑弹框若正开着该类 → 一并关闭（类别已不存在）
    await reloadMemoryCategories()
  } catch (e) {
    message.error(t('projectConfig.save_failed') + ': ' + (e.message || ''))
  }
}

// 清空类别内容（≠ 删除类别）：二次确认 → 复用既有 data-memory-save 写空串（后端已支持）
// → **后端确认后**重新读回清单（不乐观清空）；该类的内容编辑弹框若开着 → 关闭（内容已变，避免回写陈旧草稿）。
async function clearCategory(c) {
  if (!c || !c.category) return
  try {
    await confirm(t('memoryIO.clear_confirm', { name: c.category }), t('memoryIO.clear_confirm_title'))
  } catch { return }
  try {
    await dataClient.save('memory', { category: c.category, content: '' })
    // 后端确认成功后才更新 UI：重读清单（类别保留、tokens 归零）
    await reloadMemoryCategories()
    closeEditorFor(c.category)
    message.success(t('memoryIO.clear_ok', { name: c.category }))
  } catch (e) {
    message.error(t('memoryIO.clear_fail', { error: e.message || String(e) }))
  }
}

// 立即沉淀（memory.flush）：显式触发一次，同步等结果（进行中 → 失败/部分失败可见提示）；
// **成功静默**（A3：记忆沉淀成功不弹消息），成功后重读类别清单（tokens 变化 = 写入已落盘）。
async function flushMemory() {
  if (flushing.value) return
  flushing.value = true
  try {
    let sessionID = ''
    try {
      const r = await getActiveSessionID()
      sessionID = (r && r.session_id) || ''
    } catch (e) {
      message.error(t('memoryIO.flush_fail', { error: e.message || String(e) }))
      return
    }
    if (!sessionID) {
      message.warning(t('memoryIO.flush_no_session'))
      return
    }
    const env = await mq.emit(FLUSH_TOPIC, { session: sessionID }, { timeout: FLUSH_TIMEOUT })
    const backend = env && env.backend
    const res = (backend && backend.result) || null
    const reason = (backend && backend.errors && backend.errors[0]) || (res && res.reason) || ''
    if (!backend || !res || res.ok !== true) {
      message.error(t('memoryIO.flush_fail', { error: reason || t('config.feedback.loadFailedUnknown', { item: t('memoryIO.flush') }) }))
      return
    }
    const saved = Array.isArray(res.saved) ? res.saved : []
    const failed = Array.isArray(res.failed) ? res.failed : []
    if (res.enabled === 0) {
      message.warning(t('memoryIO.flush_no_category'))
    } else if (failed.length > 0) {
      message.warning(t('memoryIO.flush_partial', { count: saved.length, failed: failed.length }))
    }
    // else：全部成功 → 静默（A3，用户口径 2026-09-24：记忆成功不显示消息）
    await reloadMemoryCategories()
  } finally {
    flushing.value = false
  }
}

// 压缩记录空态文案：无活动会话（未取到 session）与「有会话但无压缩」区分开，不谎报。
const recordsEmptyText = computed(() => (
  hasActiveSession.value ? t('memoryIO.records_empty') : t('memoryIO.records_no_session')
))

// 摘要截断（长文折叠）：超长只展示前 N 字符 + 省略号，展开看全文（与大文本既有呈现同风格）。
function brief(text) {
  const s = String(text || '')
  return s.length > RECORD_BRIEF_CHARS ? s.slice(0, RECORD_BRIEF_CHARS) + '…' : s
}

function recordExpanded(i) {
  return expandedRecords.value[i] === true
}

function toggleRecord(i) {
  expandedRecords.value = { ...expandedRecords.value, [i]: !recordExpanded(i) }
}

// loadCompressRecords 读「压缩了什么」：数据来源 = **当前活动会话的会话快照**
// （data-snapshot-get 既有面；压缩产物唯一落点 = sessions 表 history/snapshot_turn）。
// 记录 = 快照 history 中带压缩标记的 system 消息（摘要原文）+ 快照轮次（触发范围）
// + 保留段条数；快照**不存压缩时间** → 不展示时间（不臆造）。
async function loadCompressRecords() {
  recordsLoading.value = true
  try {
    let sessionID = ''
    try {
      const r = await getActiveSessionID()
      sessionID = (r && r.session_id) || ''
    } catch (e) {
      hasActiveSession.value = false
      compressRecords.value = []
      message.error(t('memoryIO.records_load_failed', { error: e.message || String(e) }))
      return
    }
    hasActiveSession.value = !!sessionID
    if (!sessionID) {
      compressRecords.value = []
      return
    }
    const res = await dataRequest('snapshot', 'get', { data: { session_id: sessionID } })
    const snap = (res && res.snapshot) || null
    const history = (snap && Array.isArray(snap.history)) ? snap.history : []
    const out = []
    for (const m of history) {
      const content = m && typeof m.content === 'string' ? m.content : ''
      if (m && m.role === 'system' && content.startsWith(COMPRESS_MARK)) {
        out.push({
          text: content.slice(COMPRESS_MARK.length),
          turn: (snap && snap.snapshot_turn) || '',
          // 保留段 = 快照中压缩摘要之外的消息条数（压缩后快照 = 摘要 + 保留段）
          kept: Math.max(history.length - 1, 0),
        })
      }
    }
    compressRecords.value = out
    expandedRecords.value = {}
  } catch (e) {
    compressRecords.value = []
    message.error(t('memoryIO.records_load_failed', { error: e.message || String(e) }))
  } finally {
    recordsLoading.value = false
  }
}

// 保存按钮：提交本页全部设置（记忆 + 压缩 + 总结提示词）；保存中禁用重复提交
async function handleSave() {
  if (saving.value) return
  saving.value = true
  try {
    warnIllegalBounds()
    // 本页全部 prj 键（压缩三项 + 记忆库开关/阈值 + 类别开关）**一次批量写** → 后端整批一次
    // 广播 data-prj-config-refresh（61 §3.1），不再逐键 N 条。
    const entries = {
      keep_full_max_turns: String(keepFullMaxTurns.value),
      keep_full_max_tokens: String(keepFullMaxTokens.value),
      compress_token_threshold: String(compressTokenThreshold.value),
      'memory.enabled': String(memoryEnabled.value),
      'memory.min-turn-tokens': String(memoryMinTurnTokens.value),
      'memory.category-max-tokens': String(memoryCategoryMaxTokens.value),
    }
    if (memoryEnabled.value) {
      // 记忆库关闭时类别开关不可见 → 不提交类别键（避免写入用户未见过的值）
      for (const c of projectCategories.value) {
        entries['memory.category.' + c.category] = String(categoryEnabled(c))
      }
      entries['memory.category.' + USER_PREF_CATEGORY] = String(userPrefEnabled.value)
    }
    await setConfigs(entries)
    // 总结提示词：内容与"继承值"相同（未覆盖 + 未改动）→ 后端不写项目级文件（保持继承），
    // 只提示"未做覆盖"，避免把有效值固化成项目级副本、永久遮蔽系统级后续更新。
    // 属 **prompt 域**（非 prj-config）→ 单独一次调用（不同域，各自广播）。
    const inheritKept = !summaryOverride.value && summarizePrompt.value === summaryLoadedValue.value
    await setPrompt('summary_prompt', summarizePrompt.value)
    await loadSummaryPrompt()
    // 保存成功 → 以当前本地态为新「已保存态」并清除 dirty（无改动 → 【保存】禁用）
    savedState = currentState()
    markSaved()
    message.success(inheritKept ? t('projectConfig.summary_prompt_inherit_kept') : t('projectConfig.saved'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 总结提示词编辑弹框（A2：弹框内编辑 + 保存即关 + 弹框自带优化；优化前快照在弹框内可还原）
function openSummaryEditor() {
  const handle = dialog.show(h(TextEditDialog, {
    content: summarizePrompt.value,
    placeholder: t('projectConfig.summary_prompt_placeholder'),
    optimize: {
      title: t('projectConfig.summary_prompt_optimize_title'),
      useCase: t('projectConfig.summary_prompt_optimize_use_case'),
      recover: true, // 弹框内提供「恢复优化」（优化前内容回填，仍需点保存才落库）
    },
    onSave: async (text) => {
      // 弹窗编辑 = 即时落库（用户口径「弹出编辑的，直接保存」）→ 不走页面级 dirty；
      // 与继承值相同（未覆盖 + 未改动）→ 后端不写项目级文件（保持继承）。
      const inheritKept = !summaryOverride.value && text === summaryLoadedValue.value
      await setPrompt('summary_prompt', text)
      await loadSummaryPrompt() // 刷新有效值与来源标注（优化/覆盖后为项目级覆盖）
      refreshDirty() // 只重算 dirty（不清除页面其它未保存改动）
      message.success(inheritKept ? t('projectConfig.summary_prompt_inherit_kept') : t('projectConfig.saved'))
      handle.close()
    },
    // 弹框「取消」：TextEditDialog emit('cancel') → 关闭弹框（不落库）
    onCancel: () => handle.close(),
  }), {
    title: t('projectConfig.summary_prompt_edit'),
    width: 760,
    height: 620,
    bodyClass: 'text-edit-dialog-body',
    closable: true,
  })
}

// 快速阈值按钮：只改本地值（点【保存】才落库）
function handleQuickThreshold({ value }) {
  compressTokenThreshold.value = value
  refreshDirty()
}

onMounted(() => {
  loadConfig()
  // 压缩记录：进页读一次当前会话快照（空态亦展示）
  loadCompressRecords()
  // data-prj-config-refresh：配置变更后 server 广播，自动重载（20-gui；统一机制 usePrjConfigRefresh，I-138）。
  // handleSave 一次批量写（setConfigs）→ 后端整批广播 1 条（含 ids 全组键）；统一机制**按键过滤**
  // （仅本页关注键）+ **合并突发广播为 1 次重载**（读最终快照）+ **保存期间（saving）跳过**，避免
  // 早到的广播让 loadConfig 读到「尚含旧值」的中间快照，把本地**未提交**的开关/数值冲回旧值
  // （实测缺陷：记忆库「开 → 关 → 点保存」被冲回「开」→ 其下子项随之解禁、落库值也错成 true）。
  unsubs.push(usePrjConfigRefresh({
    keys: ['keep_full_max_turns', 'keep_full_turns', 'keep_full_max_tokens',
      'compress_token_threshold', 'memory.enabled', 'memory.min-turn-tokens', 'memory.category-max-tokens'],
    prefixes: ['memory.category.', 'memory.prompt.'],
    reload: loadConfig,
    isSaving: () => saving.value,
  }))
  // data-user-config-refresh：用户偏好沉淀提示词（usr 自由键 memory_prompts）变更后重载（20-gui）
  unsubs.push(onDataRefresh('user-config', loadUserPrefPrompts))
  // data-memory-refresh：记忆沉淀写回后刷新类别 token（20-gui；关闭态不发 list）
  unsubs.push(onDataRefresh('memory', reloadMemoryCategories))
  // llm-compress（= 总线 session-compress 的前端 type，20-gui §6 映射）：任一轮次压缩发生
  // → 重读压缩记录（压缩产物写回快照后即时可见）
  unsubs.push(mq.on(COMPRESS_EVENT, loadCompressRecords))
  unsubs.push(mq.on(EventNames.contextSave, handleSave))
  unsubs.push(mq.on(EventNames.contextQuickThreshold, handleQuickThreshold))
})

onUnmounted(() => {
  unsubs.forEach(fn => fn())
})
</script>

<style scoped>
.list-container {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.tab-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  gap: 8px;
  flex-shrink: 0;
}
.tab-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}
.tab-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}
/* ⑤ dirty 标记（仅显示） */
.unsaved-mark {
  font-size: 12px;
  color: var(--warning, #e6a23c);
  white-space: nowrap;
}
/* 长表单正文整体滚一次（2026-09-27 用户口径）：正文区承担两轴滚动、头部工具条固定不滚；
   其中「压缩内容」记录列表（.compress-records）自身内滚，不受此影响。 */
.form-layout {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
}
/* 记忆类别表不再自建横向滚动容器（与工具异步页同范式）→ 溢出交给 .form-layout */
.form-layout :deep(.b-table-wrapper) {
  overflow-x: visible;
}
.cfg-section {
  width: 100%;
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--border, #dee2e6);
}
.cfg-section:last-child {
  border-bottom: none;
}
.section-title {
  width: 100%;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
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
.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
.form-description {
  width: 100%;
  font-size: 12px;
  line-height: 1.7;
  color: var(--fg-secondary);
}
.field-hint {
  font-size: 12px;
  color: var(--fg-secondary);
  line-height: 1.6;
}
/* 非法值提示（口径 W：<0 = 非法，显式标红、不静默） */
.field-hint.hint-error {
  color: var(--danger, #f56c6c);
}
.switch-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
/* 总结提示词：来源标注（左）+「恢复默认」（右）同行分列 */
.summary-prompt-source {
  justify-content: space-between;
}
.quick-thresholds {
  display: flex;
  gap: 6px;
  margin-top: 4px;
}
.quick-threshold-btn {
  min-width: 48px;
}
.prompt-editor-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.mem-table-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
/* 超阈值行标红（自研 Table 行级 class） */
:deep(.b-table tbody tr.is-over td) {
  color: var(--danger, #f56c6c);
}
/* 操作列：多个文字按钮并排不换行 */
.mem-ops {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-wrap: nowrap;
}
.over-hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--danger, #f56c6c);
}
.mem-token {
  font-size: 13px;
  color: var(--text-primary);
}
.mem-token.is-over {
  color: var(--danger, #f56c6c);
}
/* 记忆区底部：记忆总 token 数（只读展示；可点击主入口在状态栏底部） */
.mem-total-row {
  gap: 0.3em;
}
.mem-total {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  align-self: flex-start;
  padding: 2px 8px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  font-size: 13px;
  color: var(--text-secondary);
}
.mem-total-value {
  font-weight: 600;
  color: var(--text-primary);
}
/* 压缩记录（压缩内容可查）：列表 + 截断/展开全文；列表自身内滚（固定高度上限） */
.compress-records {
  list-style: none;
  margin: 4px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 320px;
  overflow-y: auto;
}
.compress-record {
  border: 1px solid var(--border, #dee2e6);
  border-radius: 4px;
  padding: 6px 8px;
}
.record-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-secondary);
}
.record-kept {
  color: var(--text-muted);
}
.record-text {
  margin: 4px 0 0;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-primary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 260px;
  overflow: auto;
}
.records-empty {
  padding: 4px 0;
}
</style>
