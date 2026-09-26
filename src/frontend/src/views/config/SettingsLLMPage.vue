<template>
  <div class="settings-page">
    <div class="page-body">
      <div class="config-toolbar-actions">
        <Button type="primary" v-mq:[EventNames.configAddLlm].click>
          <Icon name="plus" :size="14" /> {{ $t('config.addLLM') }}
        </Button>
      </div>
      <Table :columns="llmColumns" :data="displayRows" :empty-text="$t('config.llm.empty')" size="small">
        <!-- 名称列：只读行（系统内置 / 系统默认）附标签；默认项附「默认」标记 -->
        <template #name="{ row }">
          <span>{{ row.name || $t('config.llm.systemDefaultName') }}</span>
          <Tag
            v-if="row._readonly"
            size="mini"
            type="info"
            class="readonly-tag"
            :title="$t('config.llm.builtinHint')"
          >{{ row._tag }}</Tag>
          <Tag v-if="row._isDefault" size="mini" type="success" class="default-tag">{{ $t('config.llm.isDefault') }}</Tag>
        </template>
        <!-- 操作列：只读行无编辑/删除（仅提示）；用户行可编辑/删除。默认项（含内置行）均可「设为默认」 -->
        <template #action="{ row }">
          <Button v-if="!row._isDefault" text @click="setDefaultLLM(row._value)">{{ $t('config.llm.setDefault') }}</Button>
          <template v-if="!row._readonly">
            <Button text v-mq:[EventNames.configEditLlm].click="{ index: row._index }">{{ $t('common.edit') }}</Button>
            <Button text type="danger" v-mq:[EventNames.configDeleteLlm].click="{ index: row._index }">{{ $t('common.delete') }}</Button>
          </template>
          <span v-else class="readonly-hint" :title="$t('config.llm.builtinHint')">{{ $t('config.llm.builtinHint') }}</span>
        </template>
      </Table>

      <!-- 子系统默认模型（SL-5，40-演进计划 §SL）：5 个子系统各自选择默认 LLM provider；
           值类型 llmref，空 = 跟随默认（defaultLLM）。保存即热生效（无需重启），生效粒度 = 下一个轮次。 -->
      <div class="subsys-group">
        <div class="subsys-title">{{ $t('config.llm.subsystemDefaults') }}</div>
        <p class="subsys-hint">{{ $t('config.llm.subsystemHint') }}</p>
        <div v-for="s in subsystems" :key="s.key" class="subsys-row">
          <label class="subsys-label">{{ $t(s.labelKey) }}</label>
          <div class="subsys-select">
            <Select
              :model-value="subsysDisplay(s.key)"
              :options="subsysOptions(s.key)"
              :placeholder="followLabel"
              placeholder-selectable
              :aria-label="$t(s.labelKey)"
              @update:model-value="(v) => onSubsysChange(s.key, v)"
            />
          </div>
          <Tag
            v-if="s.standby"
            size="mini"
            type="info"
            class="standby-tag"
            :title="$t('config.llm.standbyHint')"
          >{{ $t('config.llm.standby') }}</Tag>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, h, defineAsyncComponent } from 'vue'
import { useI18n } from 'vue-i18n'
import { Table, Button, Tag, Select, message, confirm } from '../../components/ui'
import { dialog } from '../../components/dialog'
import Icon from '../../components/icon/Icon.vue'
import { getUserConfig, saveUserConfig, getSystemBuiltins } from '../../api/config'
import { DEFAULT_LLM } from '../../config/defaults'
import { loadFailedText, savedText, saveFailedText, APPLY_INSTANT } from '../../utils/settingsFeedback'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const EditLLMDialog = defineAsyncComponent(() => import('./EditLLMDialog.vue'))

const { t } = useI18n()

const llms = ref([])
// 只读条目（gui.system.builtins 的 builtinLLMs 段）：**D-30（2026-09-22）后仅「系统默认
// （启动参数）」**（kind=default，取 exe flags -llm-base/-llm-model）——内置 provider（echo）
// 降为 router 内置兜底，不再作为可配置项列出。不入 usr llms 表，禁止编辑/删除。
// （kind=builtin 分支保留：契约形状仍允许内置项，见 61 §1 gui.system.builtins。）
const builtins = ref([])
// 默认 LLM（defaultLLM）：name 字符串（usr llms 记录名；'' = 系统默认（启动参数））；
// 旧记录为 int 索引（llms[v]，读侧兼容）。
const defaultLLM = ref(null)

// 子系统默认模型（SL-5，40-演进计划 §SL · 64-配置项一览 §3）：5 个 usr 键，值类型 llmref
// （provider name；'' = 回落 defaultLLM，数据层读侧已补）。
// analysis / decision = **预留位**（后端未实现、暂无消费方）→ 标「备用」，仍保持可配置（不加禁用）。
const subsystems = [
  { key: 'llm.promptOptimise', labelKey: 'config.llm.promptOptimise' },
  { key: 'llm.memory', labelKey: 'config.llm.memory' },
  { key: 'llm.compress', labelKey: 'config.llm.compress' },
  { key: 'llm.analysis', labelKey: 'config.llm.analysis', standby: true },
  { key: 'llm.decision', labelKey: 'config.llm.decision', standby: true },
]
// 各子系统键的已读值（原始形态：provider name / 旧 int 索引 / ''）；写回即落盘（热生效）。
const subsystemLLM = ref({})

const llmColumns = computed(() => [
  { label: '#', type: 'index', width: 40 },
  { label: t('config.llm.name'), prop: 'name', minWidth: 140 },
  { label: t('config.llm.model'), prop: 'model', minWidth: 120 },
  { label: t('config.llm.baseUrl'), prop: 'baseUrl', minWidth: 160 },
  { label: t('config.llm.maxToolIterations'), prop: 'maxToolIterations', width: 90 },
  { label: t('config.table.operation'), type: 'action', width: 280, align: 'center' },
])

// 当前默认 LLM 的标识（=「设为默认」写入 defaultLLM 的值）：
// 字符串原样（provider name / '' = 系统默认（启动参数））；旧 int 索引 → llms[idx].name（兼容）；
// 无可用 LLM（persist 补 -1）→ null（不标默认）。
const defaultLLMKey = computed(() => {
  const v = defaultLLM.value
  if (typeof v === 'string') return v
  if (typeof v === 'number' && v >= 0 && v < llms.value.length) return llms.value[v].name || ''
  return null
})

// ── 子系统默认模型（SL-5）────────────────────────────────────
// llmref 取值归一（口径同 ChatPanel.resolveDefaultLLM）：旧 int 索引 → llms[v].name；
// name 字符串原样；其余（'' / 缺失 / -1 无可用 LLM）→ ''（= 跟随默认）。
function resolveRef(v) {
  if (typeof v === 'string') return v
  if (typeof v === 'number' && v >= 0 && v < llms.value.length) return llms.value[v].name || ''
  return ''
}

// 空选项文案（= 「跟随默认」）：含当前默认 provider 名；无可用 LLM / 显式系统默认 → 「系统默认」。
const followLabel = computed(() => t('config.llm.followDefault', {
  name: defaultLLMKey.value || t('config.llm.systemDefaultName'),
}))

// 下拉展示值：数据层读侧会把「未配置 / 空串」补成 defaultLLM 的 provider name（SL-1），
// 故值等于默认 provider 时统一显示为「跟随默认」（两者当前解析到同一 provider）；
// 显式指定其它 provider → 原样显示。空串 = 不选中任何 provider → 显示空选项文案。
function subsysDisplay(key) {
  const name = resolveRef(subsystemLLM.value[key])
  return name && name === defaultLLMKey.value ? '' : name
}

// 选项 = usr 已配置的 provider 列表（name）；另补当前显式值，避免悬空引用/改名后下拉留白。
function subsysOptions(key) {
  const names = llms.value.map(it => it.name).filter(Boolean)
  const cur = resolveRef(subsystemLLM.value[key])
  const opts = names.map(n => ({ value: n, label: n }))
  if (cur && !names.includes(cur)) opts.unshift({ value: cur, label: cur })
  return opts
}

// 保存即热生效（SL-C9）：数据层与消费方每次现读 → 无需重启；生效粒度 = 下一个轮次
// （当前轮不改）——故文案只承诺「已保存并即时生效（无需重启）」。
async function onSubsysChange(key, value) {
  const prev = subsystemLLM.value[key]
  subsystemLLM.value = { ...subsystemLLM.value, [key]: value }
  try {
    await saveUserConfig({ [key]: value }) // '' = 跟随默认（显式清空，回落 defaultLLM）
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    subsystemLLM.value = { ...subsystemLLM.value, [key]: prev }
    message.error(saveFailedText(t, e))
  }
}

// displayRows = 顶部只读行（系统内置 / 系统默认）+ 用户 llms：
// 用户行带 _index（llms 数组下标，供编辑/删除）；只读行 _readonly 无 _index。
// _value = 「设为默认」写入 defaultLLM 的值（用户行/内置 provider = name；系统默认行 = ''）；
// _isDefault = 当前默认项（与 defaultLLMKey 同源，旧 int 索引记录也能正确标出）。
const displayRows = computed(() => {
  const key = defaultLLMKey.value
  return [
    ...builtins.value.map(b => {
      const value = b.kind === 'builtin' ? (b.name || '') : ''
      return {
        name: b.name || '',
        model: b.model || '',
        baseUrl: b.baseUrl || '',
        maxToolIterations: '',
        _readonly: true,
        _tag: b.kind === 'builtin' ? t('config.llm.builtinTag') : t('config.llm.systemDefaultTag'),
        _value: value,
        _isDefault: key !== null && key === value,
      }
    }),
    ...llms.value.map((it, i) => ({
      ...it,
      _readonly: false,
      _index: i,
      _value: it.name || '',
      _isDefault: key !== null && key === (it.name || ''),
    })),
  ]
})

async function loadConfig() {
  try {
    const res = await getUserConfig()
    const uc = res.config || res
    llms.value = Array.isArray(uc.llms) ? uc.llms : []
    defaultLLM.value = uc.defaultLLM === undefined ? null : uc.defaultLLM
    const sub = {}
    for (const s of subsystems) sub[s.key] = uc[s.key] === undefined ? '' : uc[s.key]
    subsystemLLM.value = sub
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console；成功路径不动）
    console.warn('[SettingsLLM] load user config failed:', e)
    message.error(loadFailedText(t, t('config.page.llm'), e))
  }
}

// 「设为默认」：defaultLLM = provider name（usr llms 记录名）；「系统默认（启动参数）」行写空串
// （= 不指定 provider → 回落 exe 启动参数）。name 稳定，不受 llms 增删移位影响。
async function setDefaultLLM(value) {
  try {
    await saveUserConfig({ defaultLLM: value })
    defaultLLM.value = value
    message.success(t('config.llm.defaultSet'))
  } catch (e) {
    message.error(t('config.save_failed') + ': ' + (e.message || ''))
  }
}

// 只读条目（gui.system.builtins）：系统内置 / 系统默认，加载失败 → 空（不阻塞用户 llms 编辑）
async function loadBuiltins() {
  try {
    const b = await getSystemBuiltins()
    builtins.value = b.builtinLLMs
  } catch (e) {
    // ④ 只读条目加载失败须用户可见（非阻塞：用户 llms 仍可编辑 → 轻提示，不刷屏）
    console.warn('[SettingsLLM] load system builtins failed:', e)
    message.warning(loadFailedText(t, t('config.llm.builtinTag'), e))
    builtins.value = []
  }
}

// 列表类即落盘（CFG-015-S01）：仅保存 llms（defaultLLM 由「设为默认」单独写入 name，不经此路径；
// 只读条目不入库）
async function saveNow(tip) {
  try {
    await saveUserConfig({ llms: llms.value })
    if (tip) message.success(tip)
    return true
  } catch (e) {
    message.error(t('config.save_failed') + ': ' + (e.message || ''))
    return false
  }
}

function openEditor(data, index) {
  const handle = dialog.show(h(EditLLMDialog, {
    initialData: { ...data },
    editIndex: index,
    onSave: async (d, idx) => {
      if (idx === -1) llms.value.push(d)
      else llms.value[idx] = d
      handle.close()
      await saveNow(t('config.llm.saved'))
    },
    onCancel: () => handle.close(),
  }), { title: t('config.llm.editTitle'), width: 640, height: 560, minimizable: false, closable: true })
}

function addLLM() { openEditor({ ...DEFAULT_LLM }, -1) }

function editLLM(index) { openEditor({ ...llms.value[index] }, index) }

async function deleteLLM(index) {
  if (llms.value.length <= 1) {
    message.warning(t('config.llm.atLeastOne'))
    return
  }
  try { await confirm(t('config.llm.confirmDelete')) } catch { return }
  llms.value.splice(index, 1)
  await saveNow(t('config.llm.saved'))
}

const unsubs = []
onMounted(() => {
  loadConfig()
  loadBuiltins()
  unsubs.push(mq.on(EventNames.configAddLlm, addLLM))
  unsubs.push(mq.on(EventNames.configEditLlm, ({ index }) => editLLM(index)))
  unsubs.push(mq.on(EventNames.configDeleteLlm, ({ index }) => deleteLLM(index)))
})
onUnmounted(() => unsubs.forEach(fn => fn()))
</script>

<style scoped>
.settings-page {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.page-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px 0;
  display: flex;
  flex-direction: column;
}
.config-toolbar-actions { margin-bottom: 12px; }
.readonly-tag { margin-left: 6px; }
.default-tag { margin-left: 6px; }
.readonly-hint { font-size: 12px; color: var(--text-muted, #6c757d); }
/* 子系统默认模型分组（SL-5） */
.subsys-group {
  margin-top: 20px;
  padding-top: 12px;
  border-top: 1px solid var(--border, #dee2e6);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.subsys-title { font-size: 13px; font-weight: 600; color: var(--text-primary); }
.subsys-hint { margin: 0; font-size: 12px; color: var(--text-muted, #6c757d); line-height: 1.6; }
.subsys-row { display: flex; align-items: center; gap: 10px; }
.subsys-label { width: 200px; flex-shrink: 0; font-size: 13px; font-weight: 500; color: var(--text-primary); }
.subsys-select { flex: 1; max-width: 420px; }
.standby-tag { flex-shrink: 0; }
</style>
