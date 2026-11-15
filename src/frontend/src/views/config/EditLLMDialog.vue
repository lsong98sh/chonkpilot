<template>
  <div class="edit-dialog">
    <!-- 页签区（唯一滚动容器；底栏按钮区固定在其外，不随内容滚动）。
         @input/@change 兜底清除「AI 建议」标记：AI 回填是程序化写值（不触发原生事件），
         用户一旦手填/改选即触发原生事件冒泡 → 标记清除（零 watch，见项目规则）。 -->
    <div class="edit-scroll" @input="clearAiMarks" @change="clearAiMarks">
      <Tabs v-model="activeTab" :tabs="tabItems" class="llm-tabs">
        <!-- 基本：常用连接与生成参数（严格 6 行 × 2 列，从左到右、从上到下） -->
        <template #basic>
          <div class="form-layout">
            <!-- ① 名称* | 协议类型* -->
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.llm.name') }}<span class="required-mark">*</span><span v-if="aiMark('name')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
              <Input v-model="localData.name" :maxlength="MAX_NAME" required :placeholder="$t('config.llm.namePlaceholder')" />
            </div>
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.llm.protocol') }}<span class="required-mark">*</span><span v-if="aiMark('protocol')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
              <Select v-model="localData.protocol" :options="protocolOptions" />
            </div>
            <!-- ② 接口地址* | 模型* -->
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.llm.baseUrl') }}<span class="required-mark">*</span><span v-if="aiMark('baseUrl')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
              <Input v-model="localData.baseUrl" :maxlength="MAX_URL" :placeholder="$t('config.llm.baseUrlPlaceholder')" />
            </div>
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.llm.model') }}<span class="required-mark">*</span><span v-if="aiMark('model')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
              <Input v-model="localData.model" :maxlength="MAX_TITLE" :placeholder="$t('config.llm.modelPlaceholder')" />
            </div>
            <!-- ③ API 密钥* | 上下文窗口大小* -->
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.llm.apiKey') }}<span class="required-mark">*</span></label>
              <Input v-model="localData.apiKey" type="password" :maxlength="MAX_KEY" />
            </div>
            <div class="form-item form-item-12">
              <div class="label-with-presets">
                <label class="form-label">
                  <span>{{ $t('config.llm.maxContextToken') }}</span><span class="required-mark">*</span>
                  <Tooltip :content="$t('config.llm.maxContextTokenHint')" placement="top">
                    <Icon name="help" :size="13" class="label-help" />
                  </Tooltip>
                  <span v-if="aiMark('maxContextToken')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span>
                </label>
                <span class="token-presets">
                  <Button
                    v-for="p in tokenPresets"
                    :key="'ctx-' + p.value"
                    text size="mini"
                    @click="setToken('maxContextToken', p.value)"
                  >{{ p.label }}</Button>
                </span>
              </div>
              <Input type="number" v-model.number="localData.maxContextToken" min="0" :max="CTX_TOKEN_MAX" step="1" style="width:100%" />
            </div>
            <!-- ④ 最大输出 Token | 最大工具迭代 -->
            <div class="form-item form-item-12">
              <div class="label-with-presets">
                <label class="form-label">{{ $t('config.llm.maxOutputToken') }}<span v-if="aiMark('maxOutputToken')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
                <span class="token-presets">
                  <Button
                    v-for="p in tokenPresets"
                    :key="'out-' + p.value"
                    text size="mini"
                    @click="setToken('maxOutputToken', p.value)"
                  >{{ p.label }}</Button>
                </span>
              </div>
              <Input type="number" v-model.number="localData.maxOutputToken" :min="OUT_TOKEN_MIN" :max="OUT_TOKEN_MAX" step="256" style="width:100%" />
            </div>
            <div class="form-item form-item-12">
              <label class="form-label">
                <span>{{ $t('config.llm.maxToolIterations') }}</span>
                <Tooltip :content="$t('config.llm.maxToolIterationsHint')" placement="top">
                  <Icon name="help" :size="13" class="label-help" />
                </Tooltip>
                <span v-if="aiMark('maxToolIterations')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span>
              </label>
              <Input type="number" v-model.number="localData.maxToolIterations" min="0" :max="TOOL_ITER_MAX" step="1" style="width:100%" />
            </div>
            <!-- ⑤ 推理能力 | 识图能力（档位下拉；值 5/4/3/2/1/0，0 = 不具备） -->
            <div class="form-item form-item-12">
              <label class="form-label">
                <span>{{ $t('config.llm.capabilityReasoning') }}</span>
                <Tooltip :content="$t('config.llm.capabilityReasoningHint')" placement="top">
                  <Icon name="help" :size="13" class="label-help" />
                </Tooltip>
                <span v-if="aiMark('capabilities')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span>
              </label>
              <Select
                v-model="localData.capability.reasoning"
                :options="capabilityOptions(localData.capability.reasoning)"
              />
              <span v-if="reasoningUnavailable" class="cap-zero-hint">{{ $t('config.llm.capabilityZeroHint') }}</span>
            </div>
            <div class="form-item form-item-12">
              <label class="form-label">
                <span>{{ $t('config.llm.capabilityVision') }}</span>
                <Tooltip :content="$t('config.llm.capabilityVisionHint')" placement="top">
                  <Icon name="help" :size="13" class="label-help" />
                </Tooltip>
                <span v-if="aiMark('capabilities')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span>
              </label>
              <Select
                v-model="localData.capability.vision"
                :options="capabilityOptions(localData.capability.vision)"
              />
              <span v-if="visionUnavailable" class="cap-zero-hint">{{ $t('config.llm.capabilityZeroHint') }}</span>
            </div>
            <!-- ⑥ 思考模式 | 温度 -->
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.llm.thinking') }}<span v-if="aiMark('reasoningEffort') || aiMark('thinking')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
              <Select v-model="localData.reasoningEffort" :options="reasoningOptions" :placeholder="$t('config.llm.reasoningPlaceholder')" />
            </div>
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.llm.temperature') }}<span v-if="aiMark('temperature')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
              <div class="temp-row">
                <input type="range" v-model.number="localData.temperature" min="0" max="2" step="0.1" style="width:100%" />
                <span class="temp-value">{{ localData.temperature }}</span>
              </div>
            </div>
          </div>
        </template>

        <!-- 附加参数：请求体额外字段（K=V 行编辑；值可为字符串 / 数字 / 布尔 / JSON 对象） -->
        <template #extra>
          <div class="kv-tab">
            <p class="kv-hint">{{ $t('config.llm.extraParamsHint') }}</p>
            <KeyValueEditor
              v-model="extraParamsText"
              mode="kv"
              :key-placeholder="$t('config.llm.kvKey')"
              :value-placeholder="$t('config.llm.extraParamsPlaceholder')"
              :add-label="$t('config.llm.kvAdd')"
              :delete-label="$t('config.llm.kvDelete')"
            />
          </div>
        </template>

        <!-- 头信息：出站 HTTP 头（K=V 行编辑；同名头优先于默认鉴权头） -->
        <template #headers>
          <div class="kv-tab">
            <p class="kv-hint">{{ $t('config.llm.headersHint') }}</p>
            <KeyValueEditor
              v-model="headersText"
              mode="kv"
              :key-placeholder="$t('config.llm.kvKey')"
              :value-placeholder="$t('config.llm.headersPlaceholder')"
              :add-label="$t('config.llm.kvAdd')"
              :delete-label="$t('config.llm.kvDelete')"
            />
          </div>
        </template>
      </Tabs>
    </div>

    <!-- 就地状态区（不随页签滚动；AI 注记/错误 + 测试连接结果） -->
    <div v-if="aiError || aiNotes || testing || testResult" class="status-area">
      <div v-if="aiError" class="ai-fill-error">{{ aiError }}</div>
      <div v-else-if="aiNotes" class="ai-fill-note">{{ $t('config.llm.aiFillNote', { notes: aiNotes }) }}</div>
      <div v-if="testing || testResult" class="test-conn">
        <span v-if="testing" class="test-conn-pending">{{ $t('config.llm.testing') }}</span>
        <template v-else-if="testResult && testResult.ok">
          <span class="test-conn-ok">{{ testOkText }}</span>
        </template>
        <template v-else>
          <span class="test-conn-fail">{{ testFailText }}</span>
          <span v-if="testDetail" class="test-conn-more" @click="testDetailOpen = !testDetailOpen">{{ $t('chat.error_detail_label') }}</span>
          <pre v-if="testDetailOpen" class="test-conn-raw">{{ testDetail }}</pre>
        </template>
      </div>
    </div>

    <!-- 底部按钮区（固定在滚动区之外，不随内容滚动）：左侧「AI 填充 + 测试连接」，右侧「取消 / 保存」 -->
    <div class="edit-footer">
      <div class="footer-left">
        <Button :disabled="aiLoading" @click="openAiFill">{{ $t('config.llm.aiFill') }}</Button>
        <Button
          :loading="testing"
          :disabled="!canTest"
          :title="canTest ? '' : $t('config.llm.testInvalid')"
          @click="testConnection"
        >{{ $t('config.llm.testConnection') }}</Button>
      </div>
      <Button v-mq:[EventNames.editLlmCancel].click>{{ $t('common.cancel') }}</Button>
      <Button type="primary" v-mq:[EventNames.editLlmSave].click>{{ $t('common.save') }}</Button>
    </div>

    <!-- AI 填充对话框：自然语言 → 结构化字段建议 → **只回填待保存态**（不落库）。
         后端经点分相对主题 config.suggest（MsgTopics.configSuggest）；apiKey 绝不代填。 -->
    <div v-if="aiDialogOpen" class="ai-modal-mask" @click.self="closeAiFill">
      <div class="ai-modal">
        <div class="ai-modal-title">{{ $t('config.llm.aiFillDialogTitle') }}</div>
        <Textarea v-model="aiText" :rows="5" :placeholder="$t('config.llm.aiFillPlaceholder')" />
        <div class="ai-fill-hint">{{ $t('config.llm.aiFillHint') }}</div>
        <div v-if="aiError" class="ai-fill-error">{{ aiError }}</div>
        <div class="ai-modal-actions">
          <Button :disabled="aiLoading" @click="closeAiFill">{{ $t('common.cancel') }}</Button>
          <Button type="primary" :loading="aiLoading" :disabled="aiLoading || !aiText.trim()" @click="generateSuggest">{{ $t('config.llm.aiFillConfirm') }}</Button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Input, Textarea, Select, Tabs, Tooltip, KeyValueEditor, message } from '@ckfw/components/ui/index.js'
import Icon from '@ckfw/components/icon/Icon.vue'
import { DEFAULT_LLM_PROTOCOL, RESPONSES_LLM_PROTOCOL, ANTHROPIC_LLM_PROTOCOL } from '../../config/defaults'
import { classifyError } from '../../utils/errorMessage'
import { suggestConfig } from '../../api/config'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../../events/event-names'
// INPUTVAL-1：字段基线（与后端 validate.go 同源口径）。
import {
  MAX_NAME, MAX_TITLE, MAX_URL, MAX_KEY, OUT_TOKEN_MIN, OUT_TOKEN_MAX,
  CTX_TOKEN_MAX, TOOL_ITER_MAX, TEMP_MAX, trimStr, tooLong, isHttpUrl,
  LLM_CAPABILITY_MIN, LLM_CAPABILITY_MAX, isValidCapability,
  normalizeCapability, deriveCapabilities, capabilityFromLegacyCaps,
} from '../../utils/fieldRules'

const { t } = useI18n()

const props = defineProps({
  initialData: { type: Object, required: true },
  editIndex: { type: Number, default: -1 },
})

const emit = defineEmits(['save', 'cancel'])

const _unsubs = []

// 页签：仅显示分组，不改变保存载荷（handleSave 一次性提交三页全部字段）。
const activeTab = ref('basic')
const tabItems = computed(() => [
  { name: 'basic', label: t('config.llm.tabBasic') },
  { name: 'extra', label: t('config.llm.tabExtra') },
  { name: 'headers', label: t('config.llm.tabHeaders') },
])

const reasoningOptions = [
  { label: t('config.llm.reasoningLow'), value: 'low' },
  { label: t('config.llm.reasoningHigh'), value: 'high' },
  { label: t('config.llm.reasoningMax'), value: 'max' },
]

// 协议类型：openai = OpenAI 兼容（Chat Completions）；responses = OpenAI Responses API；
// anthropic = Anthropic /v1/messages（router 三个适配器均支持）。
const protocolOptions = [
  { label: t('config.llm.protocolOpenAI'), value: DEFAULT_LLM_PROTOCOL },
  { label: t('config.llm.protocolResponses'), value: RESPONSES_LLM_PROTOCOL },
  { label: t('config.llm.protocolAnthropic'), value: ANTHROPIC_LLM_PROTOCOL },
]
const KNOWN_PROTOCOLS = [DEFAULT_LLM_PROTOCOL, RESPONSES_LLM_PROTOCOL, ANTHROPIC_LLM_PROTOCOL]

const localData = reactive({ ...props.initialData })
// 旧记录无 protocol（或为空）→ 回落 openai，保证下拉有选中项、保存后字段完整
if (!localData.protocol) localData.protocol = DEFAULT_LLM_PROTOCOL
// 能力双维度（D-65）：旧记录只有 `capabilities` 声明式数组 → 按「具备 = 中位档 3.0」映射为
// {reasoning, vision}（与后端读侧同口径）；完全未声明 → {3, 0}（不把存量记录判成「不具备」）。
if (!localData.capability || typeof localData.capability !== 'object') {
  localData.capability = capabilityFromLegacyCaps(
    Array.isArray(localData.capabilities) ? localData.capabilities : null)
}
localData.capability = normalizeCapability(localData.capability)
// 旧记录无 capabilities → 空数组（保存时按能力双维度重新派生）
if (!Array.isArray(localData.capabilities)) localData.capabilities = []

// 档位 = 0 → 不具备（就地提示，避免误以为是普通档位）
const reasoningUnavailable = computed(() => Number(localData.capability.reasoning) === 0)
const visionUnavailable = computed(() => Number(localData.capability.vision) === 0)

// 能力档位下拉：5/4/3/2/1/0（0 = 不具备）。旧记录可能是一位小数（如 3.5），
// 不在档位表内 → 追加「当前值」选项，避免下拉显示空白（保存时仍按原值保留）。
const CAPABILITY_TIERS = [5, 4, 3, 2, 1, 0]
const CAPABILITY_KEYS = {
  5: 'capabilityExcellent',
  4: 'capabilityGood',
  3: 'capabilityAverage',
  2: 'capabilityLow',
  1: 'capabilityWeak',
  0: 'capabilityNone',
}
function capabilityOptions(current) {
  const opts = CAPABILITY_TIERS.map(v => ({
    value: v,
    label: t('config.llm.' + CAPABILITY_KEYS[v]),
  }))
  const n = Number(current)
  if (Number.isFinite(n) && !CAPABILITY_TIERS.includes(n)) {
    opts.unshift({ value: n, label: t('config.llm.capabilityCurrent', { value: n }) })
  }
  return opts
}

// Token 快捷值（十进制 K/M）：点按钮即把对应字段设为该值（供「最大输出 Token」「上下文窗口」共用）
const tokenPresets = [
  { label: '128K', value: 128000 },
  { label: '256K', value: 256000 },
  { label: '384K', value: 384000 },
  { label: '512K', value: 512000 },
  { label: '1M', value: 1000000 },
]
function setToken(field, value) { localData[field] = value }

// ── 附加参数 / 头信息：对象 ↔ 每行 K=V 文本（KeyValueEditor 行编辑）──
// 附加参数值允许字符串 / 数字 / 布尔（JSON 标量）；其它（数组 / 对象 / null）按原样字符串保存，
// 避免与数据层校验（拒绝 null / 数组 / 嵌套对象）冲突。
function paramValueText(v) {
  if (typeof v === 'string') return v
  if (v === null || v === undefined) return ''
  try { return JSON.stringify(v) } catch { return String(v) }
}
function paramsToText(obj) {
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return ''
  return Object.keys(obj).map(k => `${k}=${paramValueText(obj[k])}`).join('\n')
}
function parseParamValue(raw) {
  const s = String(raw == null ? '' : raw).trim()
  if (s === '') return ''
  try {
    const v = JSON.parse(s)
    if (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') return v
  } catch { /* 非 JSON → 按普通字符串 */ }
  return s
}
function textToParams(text) {
  const out = {}
  for (const line of String(text || '').split('\n').map(s => s.trim()).filter(Boolean)) {
    const i = line.indexOf('=')
    if (i <= 0) continue
    const key = line.slice(0, i).trim()
    if (!key) continue
    out[key] = parseParamValue(line.slice(i + 1))
  }
  return out
}
function headersToText(h) {
  if (!h || typeof h !== 'object' || Array.isArray(h)) return ''
  return Object.keys(h).map(k => `${k}=${h[k]}`).join('\n')
}
function textToHeaders(text) {
  const m = {}
  for (const line of String(text || '').split('\n').map(s => s.trim()).filter(Boolean)) {
    const i = line.indexOf('=')
    if (i > 0) m[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return m
}
const extraParamsText = ref(paramsToText(props.initialData.extraParams))
const headersText = ref(headersToText(props.initialData.headers))

// 字段级错误提示（INPUTVAL-1 P2-1：明确提示，替代 blur 静默回落）。
function warn(rule, params) {
  message.warning(t(`fieldRules.${rule}`, params))
}

// 保存前校验（前端体验层；后端 UserConfigSet 复验并拒绝非法值）。
// 基本页 6 个必填项（名称 / 协议 / 接口地址 / 模型 / API 密钥 / 上下文窗口）+ 其余数值/选项校验。
function handleSave() {
  const name = trimStr(localData.name)
  if (!name) return warn('required', { field: t('config.llm.name') })
  if (tooLong(name, MAX_NAME)) return warn('tooLong', { field: t('config.llm.name'), max: MAX_NAME })

  const protocol = trimStr(localData.protocol)
  if (!protocol) return warn('required', { field: t('config.llm.protocol') })
  if (!KNOWN_PROTOCOLS.includes(protocol)) {
    return message.warning(t('config.llm.invalidOption', { field: t('config.llm.protocol') }))
  }

  const baseUrl = trimStr(localData.baseUrl)
  if (!baseUrl) return warn('required', { field: t('config.llm.baseUrl') })
  if (!isHttpUrl(baseUrl)) return warn('invalidUrl', { field: t('config.llm.baseUrl') })

  const model = trimStr(localData.model)
  if (!model) return warn('required', { field: t('config.llm.model') })
  if (tooLong(model, MAX_TITLE)) return warn('tooLong', { field: t('config.llm.model'), max: MAX_TITLE })

  const apiKey = trimStr(localData.apiKey)
  if (!apiKey) return warn('required', { field: t('config.llm.apiKey') })
  if (tooLong(apiKey, MAX_KEY)) return warn('tooLong', { field: t('config.llm.apiKey'), max: MAX_KEY })

  const hasCtx = localData.maxContextToken !== '' && localData.maxContextToken !== null && localData.maxContextToken !== undefined
  if (!hasCtx) return warn('required', { field: t('config.llm.maxContextToken') })
  const ctxTok = Number(localData.maxContextToken)
  if (!Number.isInteger(ctxTok) || ctxTok < 0 || ctxTok > CTX_TOKEN_MAX) {
    return warn('invalidNumber', { field: t('config.llm.maxContextToken'), min: 0, max: CTX_TOKEN_MAX })
  }

  const temp = Number(localData.temperature)
  if (!Number.isFinite(temp) || temp < 0 || temp > TEMP_MAX) {
    return warn('invalidNumber', { field: t('config.llm.temperature'), min: 0, max: TEMP_MAX })
  }
  const outTok = Number(localData.maxOutputToken)
  if (localData.maxOutputToken !== '' && (!Number.isInteger(outTok) || outTok < OUT_TOKEN_MIN || outTok > OUT_TOKEN_MAX)) {
    return warn('invalidNumber', { field: t('config.llm.maxOutputToken'), min: OUT_TOKEN_MIN, max: OUT_TOKEN_MAX })
  }
  const iter = Number(localData.maxToolIterations)
  if (localData.maxToolIterations !== '' && (!Number.isInteger(iter) || iter < 0 || iter > TOOL_ITER_MAX)) {
    return warn('invalidNumber', { field: t('config.llm.maxToolIterations'), min: 0, max: TOOL_ITER_MAX })
  }

  const effort = trimStr(localData.reasoningEffort)
  if (effort && !['low', 'high', 'max'].includes(effort)) {
    return message.warning(t('config.llm.invalidOption', { field: t('config.llm.thinking') }))
  }

  // 能力双维度（D-65）：推理 / 识图各 0~5；保存时派生旧 capabilities（截图门控等旧读侧契约）。
  if (!isValidCapability(localData.capability.reasoning)) {
    return warn('invalidNumber', {
      field: t('config.llm.capabilityReasoning'), min: LLM_CAPABILITY_MIN, max: LLM_CAPABILITY_MAX,
    })
  }
  if (!isValidCapability(localData.capability.vision)) {
    return warn('invalidNumber', {
      field: t('config.llm.capabilityVision'), min: LLM_CAPABILITY_MIN, max: LLM_CAPABILITY_MAX,
    })
  }

  localData.name = name
  localData.protocol = protocol
  localData.model = model
  localData.baseUrl = baseUrl
  localData.apiKey = apiKey
  localData.capability = normalizeCapability(localData.capability)
  localData.capabilities = deriveCapabilities(localData.capability)
  // 思考模式：下拉选值（low/high/max）非空 = 开启思考（兼容后端 resolveThinkEffort 的 thinking 键语义）；
  // 未选 → 保留原 thinking 值（缺省 true），不移除该字段（ChatPanel 等处仍读）。
  if (effort) localData.thinking = true
  else if (typeof localData.thinking !== 'boolean') localData.thinking = true
  localData.reasoningEffort = effort
  // 附加参数 / 头信息：K=V 文本 → 对象（缺失 = 空对象）。
  localData.extraParams = textToParams(extraParamsText.value)
  localData.headers = textToHeaders(headersText.value)
  emit('save', { ...localData }, props.editIndex)
  aiFields.value = [] // 保存后清「AI 建议」标记
}

// ── AI 填充（AICFG-1）：自然语言 → 结构化字段建议（config.suggest）──────────────
// 只回填**待保存态**、标「AI 建议」；**apiKey 绝不代填**（不在白名单，后端亦丢弃）。
// 回填后由用户点既有【测试连接】验证，再点【保存】落库（本组件不写任何配置）。
// 白名单（§4）：可填 + 仅建议值字段（apiKey / headers 不在内）。
const AI_LLM_FIELDS = [
  'name', 'model', 'protocol', 'baseUrl',
  'maxContextToken', 'maxOutputToken', 'thinking', 'reasoningEffort',
  'capabilities', 'temperature', 'maxToolIterations', 'extraParams',
]

const aiDialogOpen = ref(false)
const aiText = ref('')
const aiLoading = ref(false)
const aiFields = ref([]) // 被 AI 回填的字段键（用于「AI 建议」标记）
const aiError = ref('')
const aiNotes = ref('')

function aiMark(key) { return aiFields.value.includes(key) }
// 用户手填/改选即清除标记（由 .edit-scroll 的 @input/@change 原生事件冒泡触发；零 watch）。
function clearAiMarks() {
  if (aiFields.value.length) aiFields.value = []
}

function openAiFill() {
  aiError.value = ''
  aiDialogOpen.value = true
}
function closeAiFill() {
  if (!aiLoading.value) aiDialogOpen.value = false
}

// 只把白名单字段写入待保存态（apiKey 与未知字段一律不动）。
function applySuggest(fields) {
  const applied = []
  for (const k of AI_LLM_FIELDS) {
    if (!(k in fields)) continue
    if (k === 'capabilities') {
      const caps = Array.isArray(fields.capabilities)
        ? fields.capabilities.filter((v) => typeof v === 'string')
        : []
      localData.capabilities = caps
      // 旧声明式能力 → 双维度（具备 = 中位档 3.0）：AI 只给出 capabilities 时同步档位，避免保存后
      // 按 capability 派生把 AI 建议覆盖掉。
      localData.capability = capabilityFromLegacyCaps(caps)
    } else if (k === 'extraParams') {
      if (!fields.extraParams || typeof fields.extraParams !== 'object' || Array.isArray(fields.extraParams)) continue
      extraParamsText.value = paramsToText(fields.extraParams)
    } else {
      localData[k] = fields[k]
    }
    applied.push(k)
  }
  aiFields.value = applied
}

// 生成建议：current 上传白名单字段（apiKey 不上送，后端二次过滤）供增量补全。
async function generateSuggest() {
  const text = (aiText.value || '').trim()
  if (aiLoading.value) return
  aiError.value = ''
  aiNotes.value = ''
  if (!text) {
    aiError.value = t('config.llm.aiFillInvalid')
    return
  }
  aiLoading.value = true
  try {
    const current = {}
    for (const k of AI_LLM_FIELDS) current[k] = localData[k]
    current.extraParams = textToParams(extraParamsText.value)
    const res = await suggestConfig('llm', text, current)
    applySuggest(res.fields || {})
    aiNotes.value = res.notes || ''
    if (!aiError.value) {
      aiDialogOpen.value = false
      activeTab.value = 'basic'
    }
  } catch (e) {
    const msg = (e && e.message) || String(e)
    if (e && e.kind === 'invalid') {
      aiError.value = t('config.llm.aiFillInvalid')
    } else {
      const cls = classifyError(msg)
      aiError.value = t(cls.key, cls.params)
    }
  } finally {
    aiLoading.value = false
  }
}

// ── 测试连接（批 3 ⑮）：用当前表单配置**只读探活**一次 ──────────────────────
// 主题 = **点分相对主题**（61-消息一览 §1「点分相对主题直通总线服务」）：桥 / 服务端上行入口对
// 点分主题原样注入总线（不经单字方法白名单），服务端订阅点见
// chonkpilot-llm/server/llm_testconn.go（SubjectLLMTestConnection，主题字面量须一致）。
const TEST_CONN_TOPIC = 'llm.test-connection'

const testing = ref(false)
const testResult = ref(null)
const testDetailOpen = ref(false)

// 必填项（baseUrl / model）齐备才可探活——与后端 invalid 判定同口径（表单未填 → 按钮禁用）
const canTest = computed(() => !!(localData.baseUrl || '').trim() && !!(localData.model || '').trim())

// 成功文案：延迟 + 回显模型名（上游未回显模型名时省略该段）
const testOkText = computed(() => {
  const r = testResult.value
  if (!r || !r.ok) return ''
  return r.model_echo
    ? t('config.llm.testOkModel', { model: r.model_echo, latency: r.latency_ms })
    : t('config.llm.testOk', { latency: r.latency_ms })
})

// 失败文案：入参类单独给文案；其余复用批 2 的 errorMessage 分类映射（不另造一套口径）
const testFailText = computed(() => {
  const r = testResult.value
  if (!r || r.ok) return ''
  if (r.error && r.error.kind === 'invalid') return t('config.llm.testInvalid')
  const cls = classifyError(r.error ? r.error.message : '')
  return t(cls.key, cls.params)
})

// 原始详情（不丢弃、可展开核对；后端已脱敏 → 不含 API Key 明文）
const testDetail = computed(() => (testResult.value && testResult.value.error && testResult.value.error.message) || '')

// 探活：发**临时配置**（不保存、不改当前生效 provider）→ 就地展示结果；进行中禁止重复点击
async function testConnection() {
  if (testing.value || !canTest.value) return
  testing.value = true
  testResult.value = null
  testDetailOpen.value = false
  try {
    const env = await mq.emit(TEST_CONN_TOPIC, {
      baseUrl: localData.baseUrl.trim(),
      model: localData.model.trim(),
      apiKey: localData.apiKey || '',
      protocol: localData.protocol || DEFAULT_LLM_PROTOCOL,
    })
    const backend = env && env.backend
    // 后端无应答（桥/服务端未接）→ 明确失败，不假成功；原始原因原样保留（可展开核对）
    testResult.value = (backend && backend.result) || {
      ok: false,
      error: {
        message: (backend && backend.errors && backend.errors[0]) || TEST_CONN_TOPIC + ': backend unreachable',
      },
    }
  } catch (e) {
    testResult.value = { ok: false, error: { message: (e && e.message) || String(e) } }
  } finally {
    testing.value = false
  }
}

// 交互事件化：v-mq 触发 → 本地执行（弹窗为单实例）
onMounted(() => {
  _unsubs.push(mq.on(EventNames.editLlmSave, handleSave))
  _unsubs.push(mq.on(EventNames.editLlmCancel, () => emit('cancel')))
})

onUnmounted(() => _unsubs.forEach(fn => fn()))
</script>

<style scoped>
/* 弹窗内容体：顶栏 + 滚动页签区 + 底栏（bodyClass=form-dialog-body，body 去内距/不外溢） */
.edit-dialog {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.edit-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px;
}
/* Tabs 默认 height:100% + 内层 overflow:hidden 会裁掉超出内容；此处放开，
   交由 .edit-scroll 统一滚动（页签头固定、内容整体滚动）。 */
.llm-tabs {
  height: auto;
}
.llm-tabs :deep(.b-tabs-body) {
  overflow: visible;
  padding-top: 0.5em;
}
.edit-footer {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
}
/* 左侧「AI 填充 + 测试连接」同区，与右侧「取消 / 保存」拉开 */
.footer-left {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-right: auto;
}
/* 就地状态区（AI 注记 / 错误 + 测试连接结果；固定在底栏上方） */
.status-area {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 16px;
  border-top: 1px solid var(--border);
}
/* 必填标记 */
.required-mark {
  color: color-mix(in srgb, var(--danger) 75%, var(--text-primary));
  font-weight: 600;
}
.temp-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.temp-value {
  font-size: 12px;
  color: var(--fg-secondary);
  margin-left: 4px;
  flex-shrink: 0;
}
/* 能力值 = 0（不具备，不参与 auto 档位调度）的就地提示 */
.cap-zero-hint {
  font-size: 12px;
  /* --warning 作为正文色在 light 底上仅 ~2.2:1；与正文色混合后三主题都可读
     （light 压深为琥珀色 / dark·nord 提亮），仍保留「告警」色相。 */
  color: color-mix(in srgb, var(--warning, #e6a23c) 55%, var(--text-primary));
}
/* 附加参数 / 头信息页签 */
.kv-tab {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.kv-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-muted);
}
/* AI 填充状态行 */
.ai-fill-hint,
.ai-fill-note,
.ai-fill-error {
  font-size: 12px;
  line-height: 1.4;
}
.ai-fill-hint { color: var(--text-muted); }
.ai-fill-note { color: var(--accent); }
.ai-fill-error { color: color-mix(in srgb, var(--danger) 70%, var(--text-primary)); }
/* 「AI 建议」标记（被 AI 回填的字段；用户手填即清除） */
.ai-badge {
  margin-left: 6px;
  padding: 0 5px;
  font-size: 10px;
  font-weight: 400;
  line-height: 16px;
  color: var(--accent);
  border: 1px solid currentColor;
  border-radius: 3px;
}
/* label 旁的 ? 术语说明（Tooltip 入口） */
.label-help {
  color: var(--text-muted);
  cursor: help;
}
.form-layout {
  display: flex;
  flex-wrap: wrap;
  /* 列间距须与 form-item-* 的半宽偏移（6px/8px/9px…）匹配：12px 保证「一行两列」精确合排 */
  gap: 12px;
  width: 100%;
}
.form-item {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  width: 100%;
}
.form-item-12 { width: calc(50% - 6px); }
.form-item-full { width: 100%; }
.form-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
/* Label 行 + Token 快捷值按钮（右对齐） */
.label-with-presets {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 4px 6px;
}
.token-presets {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}
.token-presets :deep(.b-btn) { padding: 2px 6px; min-height: 20px; font-size: 11px; }
/* 测试连接：结果行（底栏按钮触发的就地结果） */
.test-conn {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 12px;
}
.test-conn-pending { color: var(--text-muted); }
.test-conn-ok { color: var(--success); }
.test-conn-fail { color: color-mix(in srgb, var(--danger) 70%, var(--text-primary)); }
.test-conn-more { color: var(--accent); cursor: pointer; user-select: none; }
.test-conn-raw {
  width: 100%;
  margin: 4px 0 0;
  padding: 6px 8px;
  font-size: 11px;
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--bg-tertiary);
  color: var(--text-secondary);
  border-radius: var(--border-radius);
}
/* AI 填充对话框（覆盖在当前编辑弹窗内；点击遮罩取消） */
.ai-modal-mask {
  position: absolute;
  inset: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(0, 0, 0, 0.38);
}
.ai-modal {
  width: 100%;
  max-width: 520px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  background: var(--bg-primary, var(--bg-secondary));
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.28);
}
.ai-modal-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}
.ai-modal-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}
</style>