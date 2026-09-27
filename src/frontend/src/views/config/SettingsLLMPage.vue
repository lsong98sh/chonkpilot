<template>
  <div class="settings-page">
    <Tabs :tabs="tabs" v-model="activeTab" class="settings-tabs">
      <!-- 一览：provider 清单（新增 / 编辑 / 删除） -->
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
           各子系统 = llm.*（llmref，空 = 回落 defaultLLM）。保存即热生效（无需重启），生效粒度 = 下一个轮次。 -->
      <template #defaults>
        <div class="page-body">
          <div class="subsys-group">
            <div class="subsys-title">{{ $t('config.llm.defaultsTitle') }}</div>
            <p class="subsys-hint">{{ $t('config.llm.subsystemHint') }}</p>
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
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const EditLLMDialog = defineAsyncComponent(() => import('./EditLLMDialog.vue'))

const { t } = useI18n()

const llms = ref([])
// 默认 LLM（defaultLLM）：name 字符串（usr llms 记录名）；旧记录为 int 索引（llms[v]，读侧兼容）。
const defaultLLM = ref(null)
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
// 各子系统键的已读值（原始形态：provider name / 旧 int 索引 / ''）；写回即落盘（热生效）。
const subsystemLLM = ref({})

const llmColumns = computed(() => [
  { label: '#', type: 'index', width: 40 },
  { label: t('config.llm.name'), prop: 'name', minWidth: 140 },
  { label: t('config.llm.model'), prop: 'model', minWidth: 120 },
  { label: t('config.llm.baseUrl'), prop: 'baseUrl', minWidth: 160 },
  { label: t('config.llm.maxToolIterations'), prop: 'maxToolIterations', width: 90 },
  { label: t('config.table.operation'), type: 'action', width: 180, align: 'center' },
])

// 当前主对话默认 LLM 的 provider 名：字符串原样；旧 int 索引 → llms[idx].name（兼容）；
// 未设置 / 无可用 LLM（persist 补 -1）→ null。
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

// 空选项文案（= 「跟随默认」）：含当前主对话默认 provider 名；未设置 → 「未设置」。
const followLabel = computed(() => t('config.llm.followDefault', {
  name: defaultLLMKey.value || t('config.llm.unset'),
}))

// ── 主对话默认 LLM（defaultLLM）──────────────────────────────
// 展示值 = provider name（'' = 未设置）；选项 = usr 已配置 provider（另补当前显式值防留白）。
const mainValue = computed(() => resolveRef(defaultLLM.value))
const mainPlaceholder = computed(() => t('config.llm.mainDefaultUnset'))
const providerOptions = computed(() => {
  const names = llms.value.map(it => it.name).filter(Boolean)
  const cur = mainValue.value
  const opts = names.map(n => ({ value: n, label: n }))
  if (cur && !names.includes(cur)) opts.unshift({ value: cur, label: cur })
  return opts
})

// 主对话默认：写 usr defaultLLM = provider name（保存即热生效，粒度 = 下一个轮次）。
async function onMainChange(value) {
  const prev = defaultLLM.value
  defaultLLM.value = value
  try {
    await saveUserConfig({ defaultLLM: value })
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    defaultLLM.value = prev
    message.error(saveFailedText(t, e))
  }
}

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

// 一览行 = usr llms（带 _index 供编辑/删除）。
const llmRows = computed(() => llms.value.map((it, i) => ({ ...it, _index: i })))

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

// 列表类即落盘（CFG-015-S01）：仅保存 llms（defaultLLM 由「默认模型」页签单独写入 name，不经此路径）
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
/* 默认模型分组（主对话 + 各子系统） */
.subsys-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.subsys-title { font-size: 14px; font-weight: 600; color: var(--text-primary); }
.subsys-hint { margin: 0; font-size: 12px; color: var(--fg-secondary); line-height: 1.6; }
.subsys-row { display: flex; align-items: center; gap: 10px; }
.subsys-label { width: 200px; flex-shrink: 0; font-size: 13px; font-weight: 500; color: var(--text-primary); }
.subsys-select { flex: 1; max-width: 420px; }
.standby-tag { flex-shrink: 0; }
</style>
