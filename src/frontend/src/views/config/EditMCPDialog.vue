<template>
  <div class="form-layout">
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.name') }}</label>
      <Input v-model="localData.name" :placeholder="$t('config.mcp.namePlaceholder')" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.enabled') }}</label>
      <Switch v-model="localData.enabled" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.transport') }}</label>
      <Select v-model="transport" :options="transportOptions" />
      <div class="form-hint">{{ $t('config.mcp.transportHint') }}</div>
      <div v-if="urlIgnoredHint" class="form-hint">{{ urlIgnoredHint }}</div>
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.category') }}</label>
      <Input v-model="localData.category" :placeholder="$t('config.mcp.categoryPlaceholder')" />
    </div>
    <!-- 连接字段从属 transport：http/sse → url 显示在 transport 之下；stdio → runtime + args（auto 两者皆可） -->
    <div v-if="showUrl" class="form-item form-item-full">
      <label class="form-label">{{ $t('config.mcp.serverUrl') }}</label>
      <Input v-model="localData.url" :placeholder="$t('config.mcp.urlPlaceholder')" />
    </div>
    <div v-if="showSpawn" class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.runtime') }}</label>
      <Input v-model="localData.runtime" :placeholder="$t('config.mcp.runtimePlaceholder')" />
    </div>
    <div v-if="showSpawn" class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.args') }}</label>
      <Textarea v-model="argsText" :rows="3" :placeholder="$t('config.mcp.argsPlaceholder')" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.namespace') }}</label>
      <Input v-model="localData.namespace" :placeholder="$t('config.mcp.namespacePlaceholder')" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.cwd') }}</label>
      <Input v-model="localData.cwd" :placeholder="$t('config.mcp.cwdPlaceholder')" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.isolate') }}</label>
      <Switch v-model="isolateToggle" />
      <div class="form-hint">{{ isolateHint }}</div>
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.timeout') }}</label>
      <Input type="number" v-model.number="localData.timeout" min="0" step="1" :placeholder="$t('config.mcp.timeoutPlaceholder')" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.hotTools') }}</label>
      <Input v-model="hotToolsText" :placeholder="$t('config.mcp.hotToolsPlaceholder')" />
    </div>
    <div class="form-item form-item-full">
      <label class="form-label">{{ $t('config.mcp.description') }}</label>
      <Textarea v-model="localData.description" :rows="2" :placeholder="$t('config.mcp.descriptionPlaceholder')" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.env') }}</label>
      <Textarea v-model="envText" :rows="3" :placeholder="$t('config.mcp.envPlaceholder')" />
    </div>
    <div class="form-item form-item-12">
      <label class="form-label">{{ $t('config.mcp.headers') }}</label>
      <Textarea v-model="headersText" :rows="3" :placeholder="$t('config.mcp.headersPlaceholder')" />
    </div>
    <div class="dialog-footer" style="width:100%">
      <Button v-mq:[EventNames.editMcpCancel].click>{{ $t('common.cancel') }}</Button>
      <Button type="primary" v-mq:[EventNames.editMcpSave].click>{{ $t('common.save') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Input, Textarea, Select, Switch, message } from '../../components/ui'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()

const props = defineProps({
  initialData: { type: Object, required: true },
  editIndex: { type: Number, default: -1 },
})

const emit = defineEmits(['save', 'cancel'])

const localData = reactive({ ...props.initialData })
const _unsubs = []

// ── 表单友好字段：env/headers 以「每行 K=V」文本编辑、hot_tools 以逗号分隔文本编辑；
//    transport 用 'auto' 哨兵表示「留空」（避免与 Select 的 disabled 占位 option 冲突），
//    保存时统一还原为规范结构（[]string "K=V" / map / []string）与空串。
const envText = ref(listToText(props.initialData.env, '\n'))
const headersText = ref(headersToText(props.initialData.headers))
const hotToolsText = ref(listToText(props.initialData.hot_tools, ', '))
const transport = ref(transportToForm(props.initialData.transport))
// args：每行一个参数（**逐个**作为 exec 参数，不做 shell/引号解析；行内空格保留）。
const argsText = ref(normalizeArgs(props.initialData.args).join('\n'))

const transportOptions = computed(() => [
  { label: t('config.mcp.transportAuto'), value: 'auto' },
  { label: 'stdio', value: 'stdio' },
  { label: 'http', value: 'http' },
  { label: 'sse', value: 'sse' },
])

// 连接字段从属 transport（transport 为主选择）：
//   http/sse → 仅 url（显示在 transport 之下）；stdio → 仅 runtime + args；auto（留空）→ 两者皆可。
const showUrl = computed(() => transport.value !== 'stdio')
const showSpawn = computed(() => transport.value !== 'http' && transport.value !== 'sse')

// stdio 只走 runtime 拉起子进程：此时已填的 url 会被忽略（仅提示，不拦截保存）。
const urlIgnoredHint = computed(() =>
  transport.value === 'stdio' && (localData.url || '').trim() ? t('config.mcp.urlIgnoredHint') : ''
)

// GUI 不做 transport 语义判断，只做「存/取」：'' 与旧值 'direct' 均回落为 'auto' 展示。
function transportToForm(v) {
  const s = (v || '').trim()
  return s === '' || s.toLowerCase() === 'direct' ? 'auto' : s
}

// ── 按项目隔离（isolate，三态）──
// 取值：null/undefined = 未设置（gateway 按 transport 推断：stdio → 隔离、http/sse → 共享）；
// true/false = 显式设置（以配置为准）。表单用开关 + "是否已显式设置" 两态表达三态：
// 未显式设置时，开关显示按当前 transport 推断的缺省值；用户一旦拨动即视为显式设置并写库。
const isolateExplicit = ref(props.initialData.isolate === true || props.initialData.isolate === false)
const isolateValue = ref(props.initialData.isolate === true)

// inferIsolate 按 transport 推断缺省值：stdio 只有单管道（不隔离会互相阻塞）→ true；
// http/sse 可并发多连接 → false；auto（留空）按连接点推断（仅 runtime → stdio）。
function inferIsolate(d) {
  const t = transportToForm(d && d.transport)
  if (t === 'stdio') return true
  if (t === 'http' || t === 'sse') return false
  return !!((d && d.runtime) || '').trim() && !((d && d.url) || '').trim()
}

// 开关：未显式设置时随 transport 变化显示推断值（computed，无 watch）。
const isolateToggle = computed({
  get: () => (isolateExplicit.value ? isolateValue.value : inferIsolate({ transport: transport.value, runtime: localData.runtime, url: localData.url })),
  set: (v) => { isolateExplicit.value = true; isolateValue.value = !!v },
})

const isolateHint = computed(() => {
  const auto = !isolateExplicit.value
  return auto ? t('config.mcp.isolateAuto') + ' — ' + t('config.mcp.isolateHint') : t('config.mcp.isolateHint')
})

// env / hot_tools：数组 ↔ 文本（env 每行一条、hot_tools 逗号分隔）。
function listToText(list, sep) {
  return Array.isArray(list) ? list.join(sep) : ''
}
function textToList(text) {
  return String(text || '').split(/[\n,]/).map(s => s.trim()).filter(Boolean)
}
// args：数组 ↔ 每行一条（仅去除首尾空白，保留行内空格；不按逗号切分——参数本身可含逗号）。
function normalizeArgs(list) {
  return Array.isArray(list) ? list.map(s => String(s)) : []
}
function textToArgs(text) {
  return String(text || '').split('\n').map(s => s.trim()).filter(Boolean)
}
// headers：map ↔ 每行 "K=V" 文本。
function headersToText(h) {
  if (!h || typeof h !== 'object') return ''
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

// 保存：name 必填 + 连接点必须匹配 transport（stdio → runtime；http/sse → url；auto → 二者其一）。
function handleSave() {
  const name = (localData.name || '').trim()
  if (!name) {
    message.warning(t('config.mcp.nameRequired'))
    return
  }
  if (!/^[a-zA-Z][a-zA-Z0-9_]*$/.test(name)) {
    message.warning(t('config.mcp.nameFormat'))
    return
  }
  const runtime = (localData.runtime || '').trim()
  const url = (localData.url || '').trim()
  const connectorOK = transport.value === 'stdio'
    ? !!runtime
    : (transport.value === 'http' || transport.value === 'sse' ? !!url : (!!runtime || !!url))
  if (!connectorOK) {
    message.warning(t('config.mcp.connectorRequired'))
    return
  }
  localData.name = name
  localData.runtime = runtime
  localData.args = textToArgs(argsText.value)
  localData.url = url
  localData.category = (localData.category || '').trim()
  localData.namespace = (localData.namespace || '').trim()
  localData.cwd = (localData.cwd || '').trim()
  localData.timeout = Number(localData.timeout) || 0
  // transport 缺省留空：gateway 只认 http/sse/stdio，空值按 command/url 推断
  localData.transport = transport.value === 'auto' ? '' : transport.value
  localData.env = textToList(envText.value)
  localData.headers = textToHeaders(headersText.value)
  localData.hot_tools = textToList(hotToolsText.value)
  // isolate：显式拨动过开关才写库（true/false）；否则删键 = 未设置 → gateway 按 transport 推断
  // （stdio → 隔离、http/sse → 共享），保持旧配置兼容。
  if (isolateExplicit.value) localData.isolate = !!isolateValue.value
  else delete localData.isolate
  emit('save', { ...localData }, props.editIndex)
}

// 交互事件化：v-mq 触发 → 本地执行（弹窗为单实例）
onMounted(() => {
  _unsubs.push(mq.on(EventNames.editMcpSave, handleSave))
  _unsubs.push(mq.on(EventNames.editMcpCancel, () => emit('cancel')))
})

onUnmounted(() => _unsubs.forEach(fn => fn()))
</script>

<style scoped>
.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
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
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
.form-hint {
  font-size: 12px;
  line-height: 1.4;
  color: var(--text-muted);
}
</style>
