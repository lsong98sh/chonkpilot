<template>
  <div class="edit-dialog">
    <!-- 表单区（唯一滚动容器；底栏按钮区固定在其外，不随内容滚动） -->
    <div class="edit-scroll">
      <div class="form-layout">
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.name') }}</label>
          <Input v-model="localData.name" :placeholder="$t('config.llm.namePlaceholder')" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.model') }}</label>
          <Input v-model="localData.model" :placeholder="$t('config.llm.modelPlaceholder')" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.protocol') }}</label>
          <Select v-model="localData.protocol" :options="protocolOptions" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.baseUrl') }}</label>
          <Input v-model="localData.baseUrl" :placeholder="$t('config.llm.baseUrlPlaceholder')" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.apiKey') }}</label>
          <Input v-model="localData.apiKey" type="password" />
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('config.llm.temperature') }}</label>
          <input type="range" v-model.number="localData.temperature" min="0" max="2" step="0.1" style="width:100%" />
          <span class="temp-value">{{ localData.temperature }}</span>
        </div>
        <div class="form-item form-item-12">
          <div class="label-with-presets">
            <label class="form-label">{{ $t('config.llm.maxOutputToken') }}</label>
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
            <label class="form-label">{{ $t('config.llm.thinking') }}</label>
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
import { Button, Input, Select, Switch, Tooltip } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { DEFAULT_LLM_PROTOCOL, RESPONSES_LLM_PROTOCOL } from '../../config/defaults'
import { classifyError } from '../../utils/errorMessage'
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
