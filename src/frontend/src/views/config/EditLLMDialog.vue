<template>
  <div class="edit-dialog">
    <!-- 表单区（唯一滚动容器；底栏按钮区固定在其外，不随内容滚动）。
         @input/@change 兜底清除「AI 建议」标记：AI 回填是程序化写值（不触发原生事件），
         用户一旦手填/改选即触发原生事件冒泡 → 标记清除（零 watch，见项目规则）。 -->
    <div class="edit-scroll" @input="clearAiMarks" @change="clearAiMarks">
      <!-- AI 填充（AICFG-1）：自然语言 → 结构化字段建议 → **只回填待保存态**（不落库）。
           后端经点分相对主题 config.suggest（MsgTopics.configSuggest）；apiKey 绝不代填。 -->
      <div class="ai-fill">
        <div class="ai-fill-row">
          <Textarea
            v-model="aiText"
            class="ai-fill-input"
            :rows="2"
            :placeholder="$t('config.llm.aiFillPlaceholder')"
          />
          <Button
            size="small"
            :loading="aiLoading"
            :disabled="aiLoading || !aiText.trim()"
            @click="generateSuggest"
          >{{ $t('config.llm.aiFill') }}</Button>
        </div>
        <div class="ai-fill-hint">{{ $t('config.llm.aiFillHint') }}</div>
        <div v-if="aiError" class="ai-fill-error">{{ aiError }}</div>
        <div v-else-if="aiNotes" class="ai-fill-note">{{ $t('config.llm.aiFillNote', { notes: aiNotes }) }}</div>
      </div>
      <div class="form-layout">
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.name') }}<span v-if="aiMark('name')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
          <Input v-model="localData.name" :placeholder="$t('config.llm.namePlaceholder')" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.model') }}<span v-if="aiMark('model')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
          <Input v-model="localData.model" :placeholder="$t('config.llm.modelPlaceholder')" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.protocol') }}<span v-if="aiMark('protocol')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
          <Select v-model="localData.protocol" :options="protocolOptions" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.baseUrl') }}<span v-if="aiMark('baseUrl')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
          <Input v-model="localData.baseUrl" :placeholder="$t('config.llm.baseUrlPlaceholder')" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.apiKey') }}</label>
          <Input v-model="localData.apiKey" type="password" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.temperature') }}<span v-if="aiMark('temperature')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
          <input type="range" v-model.number="localData.temperature" min="0" max="2" step="0.1" style="width:100%" />
          <span class="temp-value">{{ localData.temperature }}</span>
        </div>
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
          <Input type="number" v-model.number="localData.maxOutputToken" min="256" max="1000000" step="256" style="width:100%" />
        </div>
        <div class="form-item form-item-12">
          <div class="label-with-presets">
            <label class="form-label">
              <span>{{ $t('config.llm.maxContextToken') }}</span>
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
          <Input type="number" v-model.number="localData.maxContextToken" min="0" max="1000000000" step="1" style="width:100%" />
        </div>
        <div class="form-item form-item-12">
          <div class="label-switch-row">
            <label class="form-label">{{ $t('config.llm.thinking') }}<span v-if="aiMark('thinking')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span></label>
            <Switch v-model="localData.thinking" />
          </div>
          <Select v-model="localData.reasoningEffort" :options="reasoningOptions" :placeholder="$t('config.llm.reasoningPlaceholder')" :disabled="!localData.thinking" />
        </div>
        <!-- 模型能力（多选）：声明该 provider 支持的能力；「图形」未勾选 → 聊天窗口禁用截图 -->
        <div class="form-item form-item-12">
          <label class="form-label">
            <span>{{ $t('config.llm.capabilities') }}</span>
            <Tooltip :content="$t('config.llm.capabilitiesHint')" placement="top">
              <Icon name="help" :size="13" class="label-help" />
            </Tooltip>
            <span v-if="aiMark('capabilities')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span>
          </label>
          <div class="cap-row">
            <label class="b-checkbox">
              <input type="checkbox" value="reasoning" v-model="localData.capabilities" />
              <span>{{ $t('config.llm.capReasoning') }}</span>
            </label>
            <label class="b-checkbox">
              <input type="checkbox" value="vision" v-model="localData.capabilities" />
              <span>{{ $t('config.llm.capVision') }}</span>
            </label>
          </div>
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">
            <span>{{ $t('config.llm.maxToolIterations') }}</span>
            <Tooltip :content="$t('config.llm.maxToolIterationsHint')" placement="top">
              <Icon name="help" :size="13" class="label-help" />
            </Tooltip>
            <span v-if="aiMark('maxToolIterations')" class="ai-badge">{{ $t('config.llm.aiSuggested') }}</span>
          </label>
          <Input type="number" v-model.number="localData.maxToolIterations" min="0" max="2000" step="1" style="width:100%" />
        </div>
        <!-- 测试连接（批 3 ⑮）：用**当前表单里的配置**真实探活一次（只读探测：不落库、不改生效配置）。
             结果就地展示：成功 = 延迟 + 回显模型名；失败 = 人话文案（复用 errorMessage 分类映射）+
             可展开的原始详情（后端已脱敏，不含 API Key 明文）。 -->
        <div v-if="testing || testResult" class="form-item form-item-full test-conn">
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
    </div>

    <!-- 底部按钮区（固定在滚动区之外，不随内容滚动）：左侧「测试连接」，右侧「取消 / 保存」 -->
    <div class="edit-footer">
      <Button
        class="test-conn-btn"
        :loading="testing"
        :disabled="!canTest"
        :title="canTest ? '' : $t('config.llm.testInvalid')"
        @click="testConnection"
      >{{ $t('config.llm.testConnection') }}</Button>
      <Button v-mq:[EventNames.editLlmCancel].click>{{ $t('common.cancel') }}</Button>
      <Button type="primary" v-mq:[EventNames.editLlmSave].click>{{ $t('common.save') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Input, Textarea, Select, Switch, Tooltip } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { DEFAULT_LLM_PROTOCOL, RESPONSES_LLM_PROTOCOL } from '../../config/defaults'
import { classifyError } from '../../utils/errorMessage'
import { suggestConfig } from '../../api/config'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()

const props = defineProps({
  initialData: { type: Object, required: true },
  editIndex: { type: Number, default: -1 },
})

const emit = defineEmits(['save', 'cancel'])

const _unsubs = []

const reasoningOptions = [
  { label: t('config.llm.reasoningHigh'), value: 'high' },
  { label: t('config.llm.reasoningMax'), value: 'max' },
]

// 协议类型：openai = OpenAI 兼容（Chat Completions）；responses = OpenAI Responses API（含 DeepSeek）
const protocolOptions = [
  { label: t('config.llm.protocolOpenAI'), value: DEFAULT_LLM_PROTOCOL },
  { label: t('config.llm.protocolResponses'), value: RESPONSES_LLM_PROTOCOL },
]

const localData = reactive({ ...props.initialData })
// 旧记录无 protocol（或为空）→ 回落 openai，保证下拉有选中项、保存后字段完整
if (!localData.protocol) localData.protocol = DEFAULT_LLM_PROTOCOL
// 旧记录无 capabilities → 空数组（checkbox 多选绑定要求数组）
if (!Array.isArray(localData.capabilities)) localData.capabilities = []

// Token 快捷值（十进制 K/M）：点按钮即把对应字段设为该值（供「最大输出 Token」「上下文窗口」共用）
const tokenPresets = [
  { label: '128K', value: 128000 },
  { label: '256K', value: 256000 },
  { label: '512K', value: 512000 },
  { label: '1M', value: 1000000 },
]
function setToken(field, value) { localData[field] = value }

function handleSave() {
  emit('save', { ...localData }, props.editIndex)
  aiFields.value = [] // 保存后清「AI 建议」标记
}

// ── AI 填充（AICFG-1）：自然语言 → 结构化字段建议（config.suggest）──────────────
// 只回填**待保存态**、标「AI 建议」；**apiKey 绝不代填**（不在白名单，后端亦丢弃）。
// 回填后由用户点既有【测试连接】验证，再点【保存】落库（本组件不写任何配置）。
// 白名单（§4）：可填 + 仅建议值字段（apiKey 不在内）。
const AI_LLM_FIELDS = [
  'name', 'model', 'protocol', 'baseUrl',
  'maxContextToken', 'maxOutputToken', 'thinking', 'reasoningEffort',
  'capabilities', 'temperature', 'maxToolIterations',
]

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

// 只把白名单字段写入待保存态（apiKey 与未知字段一律不动）。
function applySuggest(fields) {
  const applied = []
  for (const k of AI_LLM_FIELDS) {
    if (!(k in fields)) continue
    if (k === 'capabilities') {
      localData.capabilities = Array.isArray(fields.capabilities)
        ? fields.capabilities.filter((v) => typeof v === 'string')
        : []
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
  if (aiLoading.value || !text) return
  aiLoading.value = true
  aiError.value = ''
  aiNotes.value = ''
  try {
    const current = {}
    for (const k of AI_LLM_FIELDS) current[k] = localData[k]
    const res = await suggestConfig('llm', text, current)
    applySuggest(res.fields || {})
    aiNotes.value = res.notes || ''
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
/* 弹窗内容体：顶栏 + 滚动表单区 + 底栏（bodyClass=form-dialog-body，body 去内距/不外溢） */
.edit-dialog {
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
.edit-footer {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
}
/* 「测试连接」贴底栏左侧（与右侧「取消 / 保存」拉开） */
.test-conn-btn {
  margin-right: auto;
}
.temp-value {
  font-size: 12px;
  color: var(--fg-secondary);
  margin-left: 4px;
}
/* AI 填充（AICFG-1）：自然语言输入 + 【AI 填充】按钮 + 提示/结果行 */
.ai-fill {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}
.ai-fill-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.ai-fill-input {
  flex: 1;
  min-width: 0;
}
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
}
.form-item {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  width: 100%;
}
.form-item-12 { width: calc(50% - 6px); }
.form-item-13 { width: calc(33.33% - 8px); }
.form-item-23 { width: calc(66.67% - 4px); }
.form-item-14 { width: calc(25% - 9px); }
.form-item-34 { width: calc(75% - 3px); }
.form-item-full { width: 100%; }
.form-label {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
/* Label 行 + Token 快捷值按钮（右对齐）；思考模式标签 + 开关同行（开关靠右） */
.label-with-presets,
.label-switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}
.token-presets {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}
.token-presets :deep(.b-btn) { padding: 2px 6px; min-height: 20px; font-size: 11px; }
/* 模型能力（多选 checkbox） */
.cap-row { display: flex; align-items: center; gap: 16px; }
.b-checkbox {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
}
/* 测试连接：结果行在表单区内（底栏按钮触发的就地结果） */
.form-item.test-conn {
  flex-direction: row;
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
</style>
