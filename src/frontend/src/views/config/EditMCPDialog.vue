<template>
  <div class="mcp-dialog">
    <!-- 页签区（唯一滚动容器；底栏按钮区固定在其外，不随内容滚动） -->
    <div class="edit-scroll">
      <Tabs v-model="activeTab" :tabs="tabItems" class="mcp-tabs">
        <!-- 基本信息：连接点与通用元数据（「启用」已移至底栏，与保存按钮同一行） -->
        <template #basic>
          <div class="form-layout">
            <div class="form-item form-item-12">
              <label class="form-label">{{ $t('config.mcp.name') }}</label>
              <Input v-model="localData.name" :placeholder="$t('config.mcp.namePlaceholder')" />
            </div>
            <!-- 级别（四级文件化配置：app/user/project/prjusr；保存落对应级 `capability/mcps/<名>.json`） -->
            <div class="form-item form-item-12">
              <label class="form-label">
                <span>{{ $t('config.mcp.level') }}</span>
                <Tooltip :content="$t('config.mcp.levelHint')" placement="top">
                  <Icon name="help" :size="13" class="label-help" />
                </Tooltip>
              </label>
              <Select v-model="level" :options="levelOptions" />
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
            <!-- 请求头：K=V 行编辑（增删行），占一整行 -->
            <div class="form-item form-item-full">
              <label class="form-label">{{ $t('config.mcp.headers') }}</label>
              <KeyValueEditor
                v-model="headersText"
                mode="kv"
                :key-placeholder="$t('config.mcp.kvKey')"
                :value-placeholder="$t('config.mcp.headersPlaceholder')"
                :add-label="$t('config.mcp.kvAdd')"
                :delete-label="$t('config.mcp.kvDelete')"
              />
            </div>
          </div>
        </template>
        <!-- 运行信息：子进程/连接行为 -->
        <template #runtime>
          <div class="form-layout">
            <!-- 「运行时」与「工作目录」同一行；运行时隐藏（http/sse）时工作目录占整行 -->
            <div v-if="showSpawn" class="form-item form-item-12">
              <label class="form-label">{{ $t('config.mcp.runtime') }}</label>
              <Input v-model="localData.runtime" :placeholder="$t('config.mcp.runtimePlaceholder')" />
            </div>
            <div class="form-item" :class="showSpawn ? 'form-item-12' : 'form-item-full'">
              <label class="form-label">{{ $t('config.mcp.cwd') }}</label>
              <Input v-model="localData.cwd" :placeholder="$t('config.mcp.cwdPlaceholder')" />
            </div>
            <!-- 两个隔离开关：紧跟「运行时|工作目录」行，同排两列 -->
            <div class="form-item form-item-12">
              <label class="form-label">
                <span>{{ $t('config.mcp.isolate') }}</span>
                <Tooltip :content="isolateHint" placement="top">
                  <Icon name="help" :size="13" class="label-help" />
                </Tooltip>
              </label>
              <Switch v-model="isolateToggle" />
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
            <!-- 启动参数：每行一个参数（字符串数组，非 KV）→ list 行编辑 -->
            <div class="form-item form-item-full">
              <label class="form-label">{{ $t('config.mcp.args') }}</label>
              <KeyValueEditor
                v-model="argsText"
                mode="list"
                :value-placeholder="$t('config.mcp.argsPlaceholder')"
                :add-label="$t('config.mcp.kvAdd')"
                :delete-label="$t('config.mcp.kvDelete')"
              />
            </div>
            <!-- 环境变量：K=V 行编辑 -->
            <div class="form-item form-item-full">
              <label class="form-label">{{ $t('config.mcp.env') }}</label>
              <KeyValueEditor
                v-model="envText"
                mode="kv"
                :key-placeholder="$t('config.mcp.kvKey')"
                :value-placeholder="$t('config.mcp.envPlaceholder')"
                :add-label="$t('config.mcp.kvAdd')"
                :delete-label="$t('config.mcp.kvDelete')"
              />
            </div>
          </div>
        </template>
        <!-- 工具：高频工具勾选（数据源 = 既有 tools-list，写库原名，随【保存】落库） -->
        <template #tools>
          <div class="tools-tab" :data-loaded="toolsLoaded ? '1' : '0'" :data-loading="toolsLoading ? '1' : '0'">
            <Button size="small" data-load-tools :loading="toolsLoading" @click="loadTools">
              <Icon name="refresh" :size="13" /> {{ $t('config.mcp.loadTools') }}
            </Button>
            <div v-if="!toolsLoaded" class="tools-state">{{ $t('config.mcp.loadToolsHint') }}</div>
            <div v-else-if="toolsLoading" class="tools-state">{{ $t('config.mcp.hotToolsLoading') }}</div>
            <div v-else-if="!tools.length" class="tools-state">{{ $t('config.mcp.hotToolsEmpty') }}</div>
            <template v-else>
              <label class="hot-all">
                <input type="checkbox" class="hot-all-cb" data-hot-all :checked="allHot" @change="toggleAll" />
                <span>{{ $t('config.mcp.hotToolsAllLabel') }}</span>
              </label>
              <div class="hot-tools-list">
                <label v-for="row in tools" :key="row.name" class="hot-tool-item" :data-tool="row.name">
                  <input
                    type="checkbox"
                    class="hot-tool-cb"
                    :checked="allHot || selectedSet.has(row.orig)"
                    :disabled="allHot"
                    @change="toggleOne(row)"
                  />
                  <span class="hot-tool-name" :title="row.name">{{ row.orig }}</span>
                  <span v-if="row.description" class="hot-tool-desc" :title="row.description">{{ row.description }}</span>
                </label>
              </div>
            </template>
          </div>
        </template>
      </Tabs>
    </div>

    <!-- 底部按钮区（固定在滚动区之外，不随内容滚动）：右对齐「启用 / 取消 / 保存」 -->
    <div class="edit-footer">
      <div class="footer-enabled">
        <label class="form-label">{{ $t('config.mcp.enabled') }}</label>
        <Switch v-model="localData.enabled" />
      </div>
      <Button v-mq:[EventNames.editMcpCancel].click>{{ $t('common.cancel') }}</Button>
      <Button type="primary" v-mq:[EventNames.editMcpSave].click>{{ $t('common.save') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Input, Textarea, Select, Switch, Tabs, Tooltip, KeyValueEditor, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { stripToolPrefix } from '../../utils/toolSource'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgClientTopics } from '../../events/msgkeys.js'

const { t } = useI18n()

const props = defineProps({
  initialData: { type: Object, required: true },
  editIndex: { type: Number, default: -1 },
})

const emit = defineEmits(['save', 'cancel'])

const localData = reactive({ ...props.initialData })
const _unsubs = []

// ── 页签：仅显示分组，不改变保存载荷（handleSave 一次性提交三页全部字段）──
const activeTab = ref('basic')
const tabItems = computed(() => [
  { name: 'basic', label: t('config.mcp.tabBasic') },
  { name: 'runtime', label: t('config.mcp.tabRuntime') },
  { name: 'tools', label: t('config.mcp.tabTools') },
])

// ── 表单友好字段：env/headers 以「每行 K=V」文本编辑（KeyValueEditor 行编辑）；
//    transport 用 'auto' 哨兵表示「留空」（避免与 Select 的 disabled 占位 option 冲突），
//    保存时统一还原为规范结构（[]string "K=V" / map / []string）与空串。
//    hot_tools 不再用文本框：改为「工具」页签勾选，结果写回本 ref，随 handleSave 一次性提交。
const envText = ref(listToText(props.initialData.env, '\n'))
const headersText = ref(headersToText(props.initialData.headers))
const hotTools = ref(normalizeHotTools(props.initialData.hot_tools))
const transport = ref(transportToForm(props.initialData.transport))
// 级别（四级文件化）：app / user / project / prjusr；缺省 user（既有 usr mcps 语义）。
const level = ref(normalizeLevel(props.initialData.level))

// 级别选项（文案复用 fileTree.kb_level_*）。
const levelOptions = computed(() =>
  ['app', 'user', 'project', 'prjusr'].map(k => ({ label: t('fileTree.kb_level_' + k), value: k }))
)

// normalizeLevel 归一 level 取值（未知/空 → user）。
function normalizeLevel(v) {
  const s = (v || '').trim().toLowerCase()
  return ['app', 'user', 'project', 'prjusr'].includes(s) ? s : 'user'
}
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

// ── 按实例隔离（isolate，三态；gateway 隔离键 = (instance_id, work_dir)）──
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

// 说明文案（改为 label 右侧 ? Tooltip 承载）：未显式设置时前缀「自动（未设置…）」。
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

// ── 高频工具（hot_tools）：工具页签勾选（数据源 = 既有 tools-list）──────────
// hot_tools 语义（gateway registry.isHot）：`"*"` = 该 server 全部 hot；否则为**原名**列表。
// 勾选结果只改本地草稿（hotTools ref），仍随主对话框【保存】落库（时机不变）。
const toolsLoaded = ref(false)
const toolsLoading = ref(false)
// [{ name: 暴露名（data-tool/title）, orig: 原名（展示 + 写库值）, description }]
const tools = ref([])
const allHot = computed(() => hotTools.value.includes('*'))
const selectedSet = computed(() => new Set(hotTools.value.filter((n) => n && n !== '*')))

// 点【加载工具】才读取该 server 的工具清单（数据源/写库原名转换口径与旧独立弹窗一致）。
async function loadTools() {
  toolsLoading.value = true
  try {
    // tools-list：客户端能力面 type（schema clientTopic，桥映射 mcp-tools-list），常量见 MsgClientTopics。
    const env = await mq.emit(MsgClientTopics.toolsList, {})
    const res = env && env.backend && env.backend.result
    const list = res && Array.isArray(res.tools) ? res.tools : []
    const name = (localData.name || '').trim()
    const rows = []
    if (name) {
      for (const tl of list) {
        const meta = (tl && tl._meta) || {}
        const srv = meta.server || {}
        // 按别名归属当前 server（标识 = 该记录 name，匹配 _meta.server.alias 或 node）
        if (srv.alias !== name && srv.node !== name) continue
        // 暴露名 → 原名（写库值）：默认前缀 <节点 ID>_ 可直接剥离；自定义 Namespace 时
        // 前缀不匹配 → stripToolPrefix 原样返回（此时网关 isHot 亦无法命中，宁缺勿错）。
        rows.push({
          name: tl.name,
          orig: stripToolPrefix(tl.name, srv),
          description: tl.description || '',
        })
      }
    }
    tools.value = rows
    toolsLoaded.value = true
  } catch (e) {
    console.warn('[EditMCPDialog] load tools failed:', e)
    tools.value = []
    toolsLoaded.value = true
  } finally {
    toolsLoading.value = false
  }
}

function toggleAll() {
  hotTools.value = allHot.value ? [] : ['*']
}

function toggleOne(row) {
  if (allHot.value) return
  const next = new Set(selectedSet.value)
  if (next.has(row.orig)) next.delete(row.orig)
  else next.add(row.orig)
  // 按已加载工具顺序归一（丢弃陈旧/未知项），写库值恒为原名。
  hotTools.value = tools.value.filter((r) => next.has(r.orig)).map((r) => r.orig)
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
// 三页签字段一次性提交（页签仅为显示分组）。
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
  // hot_tools：由「工具」页签勾选（含 "*" = 全部 hot 或原名列表），此处随主对话框保存提交。
  localData.hot_tools = [...hotTools.value]
  // isolate：显式拨动过开关才写库（true/false）；否则删键 = 未设置 → gateway 按 transport 推断
  // （stdio → 隔离、http/sse → 共享），保持旧配置兼容。
  if (isolateExplicit.value) localData.isolate = !!isolateValue.value
  else delete localData.isolate
  // sandbox：同口径三态（未拨动 = 缺键 = 不隔离）。
  if (sandboxExplicit.value) localData.sandbox = !!sandboxValue.value
  else delete localData.sandbox
  // 级别：保存落对应级 `capability/mcps/<名>.json`（app/user/project/prjusr）
  localData.level = level.value
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
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
/* 滚动页签区 / 固定底部按钮区（bodyClass=form-dialog-body，body 去内距） */
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
/* 底栏「启用」：label + Switch 同行，**固定在最左侧**（2026-09-27 用户口径），
   「取消 / 保存」保持右对齐。 */
.footer-enabled {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-right: auto;
}
/* Tabs 默认 height:100% + 内层 overflow:hidden 会裁掉超出内容；此处放开，
   交由 .edit-scroll 统一滚动（页签头固定、内容整体滚动）。
   页签内容整体与页签分割线留 0.5em 间距（2026-09-27 用户口径）。 */
.mcp-dialog .mcp-tabs {
  height: auto;
}
.mcp-tabs :deep(.b-tabs-body) {
  overflow: visible;
  padding-top: 0.5em;
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
  color: var(--fg-secondary);
}
/* 「工具」页签：顶部【加载工具】按钮 + 状态/清单 */
.tools-tab {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 100%;
}
.tools-state {
  padding: 16px 0;
  font-size: 12px;
  color: var(--fg-secondary);
}
.hot-all {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
  padding: 6px 0;
  cursor: pointer;
  border-bottom: 1px solid var(--border);
}
.hot-tools-list {
  display: flex;
  flex-direction: column;
}
.hot-tool-item {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 5px 0;
  font-size: 13px;
  cursor: pointer;
}
.hot-tool-item:hover {
  background: var(--bg-hover);
}
.hot-tool-name {
  color: var(--text-primary);
  flex-shrink: 0;
}
.hot-tool-desc {
  color: var(--fg-secondary);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
