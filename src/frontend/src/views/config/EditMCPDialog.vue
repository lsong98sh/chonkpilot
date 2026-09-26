<template>
  <div class="mcp-dialog">
    <Tabs v-model="activeTab" :tabs="tabItems" class="mcp-tabs">
      <!-- 基本信息：连接点与通用元数据 -->
      <template #basic>
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
            <label class="form-label">
              <span>{{ $t('config.mcp.transport') }}</span>
              <Tooltip :content="$t('config.mcp.transportHint')" placement="top">
                <Icon name="help" :size="13" class="label-help" />
              </Tooltip>
            </label>
            <Select v-model="transport" :options="transportOptions" />
            <!-- stdio 时 url 字段隐藏，但已填值会被忽略 → 就地提示（仅此时出现） -->
            <div v-if="urlIgnoredHint" class="form-hint">{{ urlIgnoredHint }}</div>
          </div>
          <!-- 连接字段从属 transport：http/sse → url；stdio → runtime + args（auto 两者皆可） -->
          <div v-if="showUrl" class="form-item form-item-full">
            <label class="form-label">{{ $t('config.mcp.serverUrl') }}</label>
            <Input v-model="localData.url" :placeholder="$t('config.mcp.urlPlaceholder')" />
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.namespace') }}</label>
            <Input v-model="localData.namespace" :placeholder="$t('config.mcp.namespacePlaceholder')" />
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.timeout') }}</label>
            <Input type="number" v-model.number="localData.timeout" min="0" step="1" :placeholder="$t('config.mcp.timeoutPlaceholder')" />
          </div>
          <div class="form-item form-item-full">
            <label class="form-label">{{ $t('config.mcp.description') }}</label>
            <Textarea v-model="localData.description" :rows="2" :placeholder="$t('config.mcp.descriptionPlaceholder')" />
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.headers') }}</label>
            <Textarea v-model="headersText" :rows="3" :placeholder="$t('config.mcp.headersPlaceholder')" />
          </div>
        </div>
      </template>
      <!-- 运行信息：子进程/连接行为 -->
      <template #runtime>
        <div class="form-layout">
          <div v-if="showSpawn" class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.runtime') }}</label>
            <Input v-model="localData.runtime" :placeholder="$t('config.mcp.runtimePlaceholder')" />
          </div>
          <div v-if="showSpawn" class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.args') }}</label>
            <Textarea v-model="argsText" :rows="3" :placeholder="$t('config.mcp.argsPlaceholder')" />
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.env') }}</label>
            <Textarea v-model="envText" :rows="3" :placeholder="$t('config.mcp.envPlaceholder')" />
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.cwd') }}</label>
            <Input v-model="localData.cwd" :placeholder="$t('config.mcp.cwdPlaceholder')" />
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.hotTools') }}</label>
            <div class="hot-tools-row">
              <span class="hot-tools-summary" :title="hotToolsTitle">{{ hotToolsSummary }}</span>
              <Button size="small" data-hot-tools-set @click="openHotTools">{{ $t('config.mcp.hotToolsSet') }}</Button>
            </div>
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">{{ $t('config.mcp.isolate') }}</label>
            <Switch v-model="isolateToggle" />
            <div class="form-hint">{{ isolateHint }}</div>
          </div>
          <div class="form-item form-item-12">
            <label class="form-label">
              <span>{{ $t('config.mcp.sandbox') }}</span>
              <Tooltip :content="sandboxTooltip" placement="top">
                <Icon name="help" :size="13" class="label-help" />
              </Tooltip>
            </label>
            <Switch v-model="sandboxToggle" :disabled="!sandboxApplicable" />
          </div>
        </div>
      </template>
    </Tabs>
    <div class="dialog-footer" style="width:100%">
      <Button v-mq:[EventNames.editMcpCancel].click>{{ $t('common.cancel') }}</Button>
      <Button type="primary" v-mq:[EventNames.editMcpSave].click>{{ $t('common.save') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { h, reactive, ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Input, Textarea, Select, Switch, Tabs, Tooltip, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { dialog } from '../../components/dialog'
import SetMCPHotToolsDialog from './SetMCPHotToolsDialog.vue'
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

// ── 页签：仅显示分组，不改变保存载荷（handleSave 一次性提交两页全部字段）──
const activeTab = ref('basic')
const tabItems = computed(() => [
  { name: 'basic', label: t('config.mcp.tabBasic') },
  { name: 'runtime', label: t('config.mcp.tabRuntime') },
])

// ── 表单友好字段：env/headers 以「每行 K=V」文本编辑；
//    transport 用 'auto' 哨兵表示「留空」（避免与 Select 的 disabled 占位 option 冲突），
//    保存时统一还原为规范结构（[]string "K=V" / map / []string）与空串。
//    hot_tools 不再用文本框：改为「高频工具」行（摘要 + 设置按钮）→ 独立弹窗勾选，
//    结果写回本 ref，随 handleSave 一次性提交（弹窗不改「保存才落库」的时机）。
const envText = ref(listToText(props.initialData.env, '\n'))
const headersText = ref(headersToText(props.initialData.headers))
const hotTools = ref(normalizeHotTools(props.initialData.hot_tools))
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
//   http/sse → 仅 url；stdio → 仅 runtime + args；auto（留空）→ 两者皆可。
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

// ── 沙箱（sandbox，三态）──
// 仅对 stdio 的 **spawn 子进程** 生效：http/sse 不落地子进程，agentbox 无从注入 → 控件禁用。
// 传输归一与 gateway TransportName 一致：显式 transport 优先；缺省/auto：有 url → http，否则 stdio。
const sandboxTransportName = computed(() => {
  if (transport.value === 'stdio' || transport.value === 'http' || transport.value === 'sse') return transport.value
  return (localData.url || '').trim() ? 'http' : 'stdio'
})
const sandboxApplicable = computed(() => sandboxTransportName.value === 'stdio')
// 三态：未显式拨动 → 缺键（不隔离）；拨动即显式 true/false（与 isolate 同口径）。
const sandboxExplicit = ref(props.initialData.sandbox === true || props.initialData.sandbox === false)
const sandboxValue = ref(props.initialData.sandbox === true)
const sandboxToggle = computed({
  get: () => (sandboxExplicit.value ? sandboxValue.value : false),
  set: (v) => { sandboxExplicit.value = true; sandboxValue.value = !!v },
})
const sandboxTooltip = computed(() =>
  sandboxApplicable.value ? t('config.mcp.sandboxHint') : t('config.mcp.sandboxStdioOnly')
)

// ── 高频工具（hot_tools）：行摘要 + 独立「设置」弹窗 ──────────────
// hot_tools 语义（gateway registry.isHot）：`"*"` = 该 server 全部 hot；否则为**原名**列表。
const hotToolsSummary = computed(() => {
  const list = hotTools.value
  if (!list.length) return t('config.mcp.hotToolsNone')
  if (list.includes('*')) return t('config.mcp.hotToolsAll')
  return t('config.mcp.hotToolsCount', { n: list.length })
})
const hotToolsTitle = computed(() => (hotTools.value.length ? hotTools.value.join(', ') : ''))

// 打开「设置高频工具」弹窗（按 server 别名列出工具勾选）→ 确定后回填 ref（仍待 handleSave 落库）。
// 数据源/写库原名转换见 SetMCPHotToolsDialog.vue；本弹窗只做草稿收集。
function openHotTools() {
  const handle = dialog.show(h(SetMCPHotToolsDialog, {
    serverName: (localData.name || '').trim(),
    hotTools: [...hotTools.value],
    onConfirm: (list) => { hotTools.value = Array.isArray(list) ? list : []; handle.close() },
    onCancel: () => handle.close(),
  }), {
    title: t('config.mcp.hotToolsTitle'),
    width: 480,
    height: 520,
    bodyClass: 'mcp-hot-tools-dialog-body',
    minimizable: false,
    closable: true,
  })
}

// env：数组 ↔ 每行一条文本。
function listToText(list, sep) {
  return Array.isArray(list) ? list.join(sep) : ''
}
function textToList(text) {
  return String(text || '').split(/[\n,]/).map(s => s.trim()).filter(Boolean)
}
// hot_tools：归一为字符串数组（过滤空值；保留 "*" 与原名）。
function normalizeHotTools(list) {
  return Array.isArray(list) ? list.map(s => String(s).trim()).filter(Boolean) : []
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
// 两页签字段一次性提交（页签仅为显示分组）。
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
  localData.namespace = (localData.namespace || '').trim()
  localData.cwd = (localData.cwd || '').trim()
  localData.timeout = Number(localData.timeout) || 0
  // transport 缺省留空：gateway 只认 http/sse/stdio，空值按 command/url 推断
  localData.transport = transport.value === 'auto' ? '' : transport.value
  localData.env = textToList(envText.value)
  localData.headers = textToHeaders(headersText.value)
  // hot_tools：由「设置」弹窗回填（含 "*" = 全部 hot 或原名列表），此处随主对话框保存提交。
  localData.hot_tools = [...hotTools.value]
  // isolate：显式拨动过开关才写库（true/false）；否则删键 = 未设置 → gateway 按 transport 推断
  // （stdio → 隔离、http/sse → 共享），保持旧配置兼容。
  if (isolateExplicit.value) localData.isolate = !!isolateValue.value
  else delete localData.isolate
  // sandbox：同口径三态（未拨动 = 缺键 = 不隔离）。
  if (sandboxExplicit.value) localData.sandbox = !!sandboxValue.value
  else delete localData.sandbox
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
.mcp-dialog {
  display: flex;
  flex-direction: column;
}
/* Tabs 默认 height:100% + 内层 overflow:hidden 会裁掉超出内容；此处放开，
   交由弹窗 .dialog-body 统一滚动（页签头固定、内容整体滚动）。 */
.mcp-dialog .mcp-tabs {
  height: auto;
}
.mcp-tabs :deep(.b-tabs-body) {
  overflow: visible;
}
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
  width: 100%;
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
.label-help {
  color: var(--text-muted);
  cursor: help;
}
.form-hint {
  font-size: 12px;
  line-height: 1.4;
  color: var(--text-muted);
}
/* 「高频工具」行：摘要文字 + 设置按钮（同一行，按钮右对齐） */
.hot-tools-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-height: 28px;
}
.hot-tools-summary {
  font-size: 13px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
