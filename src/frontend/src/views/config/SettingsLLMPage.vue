<template>
  <div class="settings-page">
    <Tabs :tabs="tabs" v-model="activeTab" class="settings-tabs">
      <!-- 一览：provider 清单（新增 / 编辑 / 删除）。列表型页面 → 不加【保存】，增改走对话框 -->
      <template #list>
        <div class="page-body">
          <div class="config-toolbar-actions">
            <Button type="primary" v-mq:[EventNames.configAddLlm].click>
              <Icon name="plus" :size="14" /> {{ $t('config.addLLM') }}
            </Button>
          </div>
          <Table :columns="llmColumns" :data="llmRows" :empty-text="$t('config.llm.empty')" size="small">
            <template #action="{ row }">
              <Button text v-mq:[EventNames.configEditLlm].click="{ index: row._index }">{{ $t('common.edit') }}</Button>
              <Button text type="danger" v-mq:[EventNames.configDeleteLlm].click="{ index: row._index }">{{ $t('common.delete') }}</Button>
            </template>
          </Table>
        </div>
      </template>

      <!-- 默认模型：主对话 + 各子系统（全部下拉）。主对话 = defaultLLM（provider name）；
           各子系统 = llm.*（llmref，空 = 回落 defaultLLM）。表单型页面 → 编辑只改本地待保存态
           （显示「未保存」），点页签右上角【保存】才落库；保存即热生效（无需重启），生效粒度 = 下一个轮次。 -->
      <template #defaults>
        <div class="page-body">
          <div class="tab-toolbar">
            <p class="hint">{{ $t('config.llm.subsystemHint') }}</p>
            <span v-if="defaultsDirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
            <Button size="small" type="primary" :disabled="!defaultsDirty" :loading="savingDefaults" data-llm-save-defaults @click="saveDefaultsTab">{{ $t('common.save') }}</Button>
          </div>
          <div class="subsys-group">
            <div class="subsys-title">{{ $t('config.llm.defaultsTitle') }}</div>
            <div class="subsys-row">
              <label class="subsys-label">{{ $t('config.llm.mainDefault') }}</label>
              <div class="subsys-select">
                <Select
                  :model-value="mainValue"
                  :options="providerOptions"
                  :placeholder="mainPlaceholder"
                  placeholder-selectable
                  :aria-label="$t('config.llm.mainDefault')"
                  @update:model-value="onMainChange"
                />
              </div>
            </div>
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
      </template>
    </Tabs>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, h, defineAsyncComponent } from 'vue'
import { useI18n } from 'vue-i18n'
import { Table, Button, Tag, Select, Tabs, message, confirm } from '../../components/ui'
import { dialog } from '../../components/dialog'
import Icon from '../../components/icon/Icon.vue'
import { getUserConfig, saveUserConfig } from '../../api/config'
import { DEFAULT_LLM } from '../../config/defaults'
import { loadFailedText, savedText, saveFailedText, APPLY_INSTANT } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const EditLLMDialog = defineAsyncComponent(() => import('./EditLLMDialog.vue'))

const { t } = useI18n()

const llms = ref([])
// 默认模型页签：主对话（defaultLLM，provider name；'' = 未设置）与各子系统（llm.*，llmref）的
// 本地待保存态 + 已保存快照（用于「未保存」判定：本地态 ≠ 快照 → 有改动 → 保存按钮可用）。
// 编辑只改本地态，点页签右上角【保存】才落库（2026-10-06 统一口径，同 SettingsParamsPage）。
const mainValue = ref('')
const lastMainVal = ref('')
const subsystemLLM = ref({})
const lastSubVals = ref({})
const savingDefaults = ref(false)
const { dirty: defaultsDirty, markDirty: markDefaultsDirty, markSaved: markDefaultsSaved } = useUnsavedMark()

// 页签：一览（provider 清单）/ 默认模型（主对话 + 各子系统）。
const activeTab = ref('list')
const tabs = computed(() => [
  { label: t('config.llm.tabList'), name: 'list' },
  { label: t('config.llm.tabDefaults'), name: 'defaults' },
])

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

const llmColumns = computed(() => [
  { label: '#', type: 'index', width: 40 },
  { label: t('config.llm.name'), prop: 'name', minWidth: 140 },
  { label: t('config.llm.model'), prop: 'model', minWidth: 120 },
  { label: t('config.llm.baseUrl'), prop: 'baseUrl', minWidth: 160 },
  { label: t('config.llm.maxToolIterations'), prop: 'maxToolIterations', width: 90 },
  { label: t('config.table.operation'), type: 'action', width: 180, align: 'center' },
])

// 当前主对话默认 LLM 的 provider 名（本地待保存态）：显式值原样；未设置 → null（用于
// 「跟随默认」文案与子系统展示归一；保存时以 mainValue 为准）。
const defaultLLMKey = computed(() => mainValue.value || null)

// llmref 取值归一（口径同 ChatPanel.resolveDefaultLLM）：旧 int 索引 → llms[v].name；
// name 字符串原样；其余（'' / 缺失 / -1 无可用 LLM）→ ''（= 跟随默认）。
function resolveRef(v) {
  if (typeof v === 'string') return v
  if (typeof v === 'number' && v >= 0 && v < llms.value.length) return llms.value[v].name || ''
  return ''
}

// 展示 / 判定归一：等于当前主对话默认 provider 的显式值统一按「跟随默认」（空串）计 ——
// 与下拉展示同口径（避免「选了与默认相同的 provider」被误判为有改动）。
function normSel(raw) {
  const name = resolveRef(raw)
  return name && name === defaultLLMKey.value ? '' : name
}

// 空选项文案（= 「跟随默认」）：含当前主对话默认 provider 名；未设置 → 「未设置」。
const followLabel = computed(() => t('config.llm.followDefault', {
  name: defaultLLMKey.value || t('config.llm.unset'),
}))

// 主对话默认：展示值 = provider name（'' = 未设置）；选项 = usr 已配置 provider（另补当前显式值防留白）。
const mainPlaceholder = computed(() => t('config.llm.mainDefaultUnset'))
const providerOptions = computed(() => {
  const names = llms.value.map(it => it.name).filter(Boolean)
  const cur = mainValue.value
  const opts = names.map(n => ({ value: n, label: n }))
  if (cur && !names.includes(cur)) opts.unshift({ value: cur, label: cur })
  return opts
})

// 下拉展示值：等于默认 provider 时统一显示为「跟随默认」（两者当前解析到同一 provider）；
// 显式指定其它 provider → 原样显示。空串 = 不选中任何 provider → 显示空选项文案。
function subsysDisplay(key) {
  return normSel(subsystemLLM.value[key])
}

// 选项 = usr 已配置的 provider 列表（name）；另补当前显式值，避免悬空引用/改名后下拉留白。
function subsysOptions(key) {
  const names = llms.value.map(it => it.name).filter(Boolean)
  const cur = resolveRef(subsystemLLM.value[key])
  const opts = names.map(n => ({ value: n, label: n }))
  if (cur && !names.includes(cur)) opts.unshift({ value: cur, label: cur })
  return opts
}

// refreshDefaultsDirty：由「本地待保存态 vs 已保存快照」重算 dirty（无 watch）；编辑 / 保存后显式调用。
function refreshDefaultsDirty() {
  const mainChanged = (mainValue.value || '') !== (lastMainVal.value || '')
  const subChanged = subsystems.some(s =>
    normSel(subsystemLLM.value[s.key]) !== normSel(lastSubVals.value[s.key]))
  ;(mainChanged || subChanged) ? markDefaultsDirty() : markDefaultsSaved()
}

// 编辑只改本地待保存态（不落库），dirty 据此重算。
function onMainChange(value) {
  mainValue.value = value
  refreshDefaultsDirty()
}
function onSubsysChange(key, value) {
  subsystemLLM.value = { ...subsystemLLM.value, [key]: value }
  refreshDefaultsDirty()
}

// 保存（页签右上角【保存】）：把「本地态 ≠ 已保存快照」的项合并为一次写库（走既有 saveUserConfig）；
// 成功后更新快照 + 给一次成功反馈、失败走 saveFailedText；保存中短路防重复提交。
// 保存即热生效（SL-C9）：数据层与消费方每次现读 → 无需重启；生效粒度 = 下一个轮次（当前轮不改）。
async function saveDefaultsTab() {
  if (!defaultsDirty.value || savingDefaults.value) return
  savingDefaults.value = true
  try {
    const patch = {}
    if ((mainValue.value || '') !== (lastMainVal.value || '')) patch.defaultLLM = mainValue.value
    for (const s of subsystems) {
      if (normSel(subsystemLLM.value[s.key]) !== normSel(lastSubVals.value[s.key])) {
        patch[s.key] = subsystemLLM.value[s.key] || '' // '' = 跟随默认（显式清空，回落 defaultLLM）
      }
    }
    if (Object.keys(patch).length === 0) return
    await saveUserConfig(patch)
    if (Object.prototype.hasOwnProperty.call(patch, 'defaultLLM')) lastMainVal.value = mainValue.value
    for (const s of subsystems) {
      if (Object.prototype.hasOwnProperty.call(patch, s.key)) {
        lastSubVals.value = { ...lastSubVals.value, [s.key]: subsystemLLM.value[s.key] }
      }
    }
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    savingDefaults.value = false
    refreshDefaultsDirty()
  }
}

// 一览行 = usr llms（带 _index 供编辑/删除）。
const llmRows = computed(() => llms.value.map((it, i) => ({ ...it, _index: i })))

async function loadConfig() {
  try {
    const res = await getUserConfig()
    const uc = res.config || res
    llms.value = Array.isArray(uc.llms) ? uc.llms : []
    const main = resolveRef(uc.defaultLLM === undefined ? null : uc.defaultLLM)
    mainValue.value = main
    lastMainVal.value = main
    const sub = {}
    for (const s of subsystems) sub[s.key] = uc[s.key] === undefined ? '' : uc[s.key]
    subsystemLLM.value = sub
    lastSubVals.value = { ...sub }
    refreshDefaultsDirty()
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console；成功路径不动）
    console.warn('[SettingsLLM] load user config failed:', e)
    message.error(loadFailedText(t, t('config.page.llm'), e))
  }
}

// 列表类即落盘（CFG-015-S01）：仅保存 llms（defaultLLM / llm.* 由「默认模型」页签单独写入，不经此路径）
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
  }), { title: t('config.llm.editTitle'), width: 640, height: 620, bodyClass: 'form-dialog-body', minimizable: false, closable: true })
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
.settings-tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
/* 统一骨架：页签头部固定 + 正文区 flex:1/min-height:0/overflow:auto（正文整体滚一次）；
   滚动交给各页签的 .page-body 承担（两轴）→ 头部页签固定不滚、横向滚动条贴正文区底部。 */
.settings-tabs :deep(.b-tabs-body) {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  padding: 0;
}
.page-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 8px 0;
  display: flex;
  flex-direction: column;
}
/* provider 清单表不再自建横向滚动容器 → 溢出交给 .page-body（2026-09-27 用户口径） */
.page-body :deep(.b-table-wrapper) {
  overflow-x: visible;
}
.config-toolbar-actions { margin-bottom: 12px; }
/* 默认模型页签工具条：说明（左）+「未保存」标记 +【保存】（右上角） */
.tab-toolbar { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.hint { flex: 1; margin: 0; font-size: 12px; color: var(--fg-secondary); line-height: 1.6; }
.unsaved-mark { color: var(--warning, #e6a23c); }
/* 默认模型分组（主对话 + 各子系统） */
.subsys-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.subsys-title { font-size: 14px; font-weight: 600; color: var(--text-primary); }
.subsys-row { display: flex; align-items: center; gap: 10px; }
.subsys-label { width: 200px; flex-shrink: 0; font-size: 13px; font-weight: 500; color: var(--text-primary); }
.subsys-select { flex: 1; max-width: 420px; }
.standby-tag { flex-shrink: 0; }
</style>
