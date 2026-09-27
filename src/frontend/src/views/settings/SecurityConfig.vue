<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">{{ $t('security.title') }}</span>
      <div class="tab-actions">
        <!-- ⑤ dirty 标记（与手动保存按钮联动） -->
        <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" v-mq:[EventNames.securityAdd].click>
          <Icon name="plus" /> {{ $t('security.add') }}
        </Button>
        <Button
          size="small"
          type="primary"
          data-security-save
          :disabled="!dirty"
          :loading="saving"
          @click="save"
        >{{ $t('common.save') }}</Button>
      </div>
    </div>
    <p class="security-hint">{{ $t('security.not_enforced_hint') }}</p>
    <div class="table-wrap">
      <Table :columns="securityColumns" :data="entries" :empty-text="$t('security.empty')" size="small">
        <template #dir="{ index }">
          <div class="security-dir-row">
            <Input v-model="entries[index].dir" :placeholder="$t('security.dir_placeholder')" @change="syncDirty" />
            <Button size="small" v-mq:[EventNames.securitySelectDir].click="{ index }">...</Button>
          </div>
        </template>
        <template #writable="{ index }">
          <label class="b-checkbox">
            <input type="checkbox" v-model="entries[index].writable" @change="syncDirty" />
          </label>
        </template>
        <template #action="{ index }">
          <Button text size="small" type="danger" v-mq:[EventNames.securityDelete].click="{ index }">{{ $t('security.delete') }}</Button>
        </template>
      </Table>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { Input, Button, Table } from '../../components/ui'
import { getProjectSecurity, saveProjectSecurity } from '../../api/config'
import { useDirPicker } from '../../composables/useDirPicker'
import { onDataRefresh } from '../../utils/dataClient'
import { message, confirm } from '../../components/ui'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()

// 表格列配置（信任目录表）：与既有原生表逐列一致（# / 信任目录 / 读写 / 操作）。
const securityColumns = computed(() => [
  { label: '#', type: 'index', width: 40 },
  { label: t('security.trust_dir'), prop: 'dir', minWidth: 300 },
  { label: t('security.read_write'), prop: 'writable', width: 80, align: 'center' },
  { label: t('security.operation'), type: 'action', width: 70, align: 'center' },
])

// ⑤ dirty 可视标记（与手动保存联动：编辑/增删只改本地态 → dirty；保存成功 → 清除）
const { dirty, markDirty, markSaved } = useUnsavedMark()

// 目录选择统一入口：GUI/native → gui.dir.open-dialog；browser → GET /dirs 只读选择器
const { pickDir } = useDirPicker()

const props = defineProps({
  maxHeight: { type: Number, default: 600 },
})

const entries = ref([])
const saving = ref(false)
const _unsubs = []
// 已移除条目的持久化键：随下一次 save 落库（删除 = data-prj-security-delete）
let _removedKeys = []
// 上次落库/加载时的条目快照：用于「无改动」判定（dirty 精确、可回退）
let _savedSnapshot = ''

// 当前条目状态的可比较快照（忽略行号，含待删键标记）
function snapshot() {
  return JSON.stringify(entries.value.map((e) => ({ key: e._key || '', dir: e.dir || '', writable: !!e.writable })))
}

// 编辑/增删后重算 dirty：与上次快照一致（且无待删键）→ 视为无改动。
function syncDirty() {
  if (_removedKeys.length === 0 && snapshot() === _savedSnapshot) markSaved()
  else markDirty()
}

async function load() {
  try {
    const res = await getProjectSecurity()
    entries.value = res.entries || []
    _removedKeys = []
    _savedSnapshot = snapshot()
    markSaved()
  } catch (e) {
    entries.value = []
    // ④ 加载失败须用户可见（不再静默清空）
    message.error(loadFailedText(t, t('security.title'), e))
  }
}

// save（手动）：逐条 upsert（一条目录 = 一个 persist key）+ 删除已移除条目；成功后清空待删键。
// 信任目录由 agentbox 在下次 tools/call spawn 时读取 → 保存即生效（onPrjSecurityRefresh 热生效）。
async function save() {
  if (!dirty.value || saving.value) return
  saving.value = true
  try {
    await saveProjectSecurity(entries.value, _removedKeys)
    _removedKeys = []
    _savedSnapshot = snapshot()
    markSaved()
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 新增条目：只改本地态（点【保存】才落库）
function addEntry() {
  entries.value.push({ _key: '', dir: '', writable: false })
  syncDirty()
}

// 选择信任目录：GUI/native 走系统目录选择框（失败回退手输）；browser 走 GET /dirs 选择器
// （只读，限本 instance work_dir 子树；拉取失败在选择器内明确提示）。
async function selectDir(index) {
  try {
    const path = await pickDir()
    if (path) {
      entries.value[index].dir = path
      syncDirty()
    }
  } catch (_) {
    const path = prompt(t('security.dir_path_prompt'))
    if (path) {
      entries.value[index].dir = path
      syncDirty()
    }
  }
}

// 删除条目：只改本地态（记下待删键，点【保存】才落库）
async function deleteEntry(index) {
  try {
    await confirm(t('security.delete_confirm'))
  } catch { return }
  const removed = entries.value.splice(index, 1)[0]
  if (removed && removed._key) _removedKeys.push(removed._key)
  syncDirty()
}

onMounted(() => {
  load()
  // data-prj-security-refresh：保存后 server 广播，自动刷新列表（20-gui）
  _unsubs.push(onDataRefresh('prj-security', load))
  // 交互事件化：v-mq 触发 → 本地执行
  _unsubs.push(mq.on(EventNames.securityAdd, addEntry))
  _unsubs.push(mq.on(EventNames.securitySelectDir, ({ index }) => { if (index !== undefined) selectDir(index) }))
  _unsubs.push(mq.on(EventNames.securityDelete, ({ index }) => { if (index !== undefined) deleteEntry(index) }))
})

onUnmounted(() => _unsubs.forEach(fn => fn()))
</script>

<style scoped>
.list-container {
  height: 100%;
  display: flex;
  flex-direction: column;
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
/* 安全策略提示（agentbox 已在执行层强制执行，见 14-安全域-agentbox）：说明文字 12px + --fg-secondary */
.security-hint {
  margin: 0 0 8px;
  padding: 6px 10px;
  border-radius: 4px;
  background: var(--warning-bg);
  border: 1px solid var(--warning-border);
  color: var(--fg-secondary);
  font-size: 12px;
  flex-shrink: 0;
}
.table-wrap {
  flex: 1;
  min-height: 0;
  /* 两轴滚动由正文承担（2026-09-27 用户口径，与工具异步页同范式）：横向滚动条贴正文区底部 */
  overflow: auto;
}
/* 信任目录表不再自建横向滚动容器 → 溢出交给 .table-wrap（表头吸顶保持） */
.table-wrap :deep(.b-table-wrapper) {
  overflow-x: visible;
}
/* 自研 Table：表头吸顶（原 .b-table-inline thead sticky 等价）+ 操作列点击区域 */
.table-wrap :deep(.b-table-th) {
  position: sticky;
  top: 0;
  z-index: 1;
}
.table-wrap :deep(.b-table-td .b-btn) {
  min-width: 52px;
  margin: 2px;
}
.security-dir-row {
  display: flex;
  gap: 4px;
  align-items: center;
}
.security-dir-row .b-input {
  flex: 1;
}
.b-checkbox {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  font-size: 13px;
}
.b-checkbox input[type="checkbox"] {
  accent-color: var(--accent, #409eff);
}
</style>