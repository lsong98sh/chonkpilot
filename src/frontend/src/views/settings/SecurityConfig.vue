<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">{{ $t('security.title') }}</span>
      <div class="tab-actions">
        <!-- ⑤ dirty 标记（仅显示，不改行编辑即保存的时机） -->
        <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" type="primary" v-mq:[EventNames.securityAdd].click>
          <Icon name="plus" /> {{ $t('security.add') }}
        </Button>
      </div>
    </div>
    <p class="security-hint">{{ $t('security.not_enforced_hint') }}</p>
    <div class="table-wrap">
      <table class="b-table-inline">
        <thead>
          <tr>
            <th style="width:40px">#</th>
            <th style="min-width:300px">{{ $t('security.trust_dir') }}</th>
            <th style="width:80px;text-align:center">{{ $t('security.read_write') }}</th>
            <th style="width:70px;text-align:center">{{ $t('security.operation') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="entries.length === 0">
            <td colspan="4" class="b-table-empty">{{ $t('security.empty') }}</td>
          </tr>
          <tr v-for="(row, rowIndex) in entries" :key="rowIndex">
            <td style="width:40px">{{ rowIndex + 1 }}</td>
            <td style="min-width:300px">
              <div class="security-dir-row">
                <Input v-model="entries[rowIndex].dir" :placeholder="$t('security.dir_placeholder')" @change="save" />
                <Button size="small" v-mq:[EventNames.securitySelectDir].click="{ index: rowIndex }">...</Button>
              </div>
            </td>
            <td style="width:80px;text-align:center">
              <label class="b-checkbox">
                <input type="checkbox" v-model="entries[rowIndex].writable" @change="save" />
              </label>
            </td>
            <td style="width:70px;text-align:center">
              <Button text size="small" type="danger" v-mq:[EventNames.securityDelete].click="{ index: rowIndex }">{{ $t('security.delete') }}</Button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { Input, Button } from '../../components/ui'
import { getProjectSecurity, saveProjectSecurity } from '../../api/config'
import { useDirPicker } from '../../composables/useDirPicker'
import { onDataRefresh } from '../../utils/dataClient'
import { message, confirm } from '../../components/ui'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()

// ⑤ dirty 可视标记（仅显示，不改行编辑即保存的时机）
const { dirty, markDirty, markSaved } = useUnsavedMark()

// 目录选择统一入口：GUI/native → gui.dir.open-dialog；browser → GET /dirs 只读选择器
const { pickDir } = useDirPicker()

const props = defineProps({
  maxHeight: { type: Number, default: 600 },
})

const entries = ref([])
const _unsubs = []
// 已移除条目的持久化键：随下一次 save 落库（删除 = data-prj-security-delete）
let _removedKeys = []

async function load() {
  try {
    const res = await getProjectSecurity()
    entries.value = res.entries || []
  } catch (e) {
    entries.value = []
    // ④ 加载失败须用户可见（不再静默清空）
    message.error(loadFailedText(t, t('security.title'), e))
  }
}

// save：逐条 upsert（一条目录 = 一个 persist key）+ 删除已移除条目；成功后清空待删键。
// 信任目录由 agentbox 在下次 tools/call spawn 时读取 → 保存即生效（onPrjSecurityRefresh 热生效）。
async function save() {
  markDirty()
  try {
    await saveProjectSecurity(entries.value, _removedKeys)
    _removedKeys = []
    markSaved()
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

function addEntry() {
  entries.value.push({ _key: '', dir: '', writable: false })
  save()
}

// 选择信任目录：GUI/native 走系统目录选择框（失败回退手输）；browser 走 GET /dirs 选择器
// （只读，限本 instance work_dir 子树；拉取失败在选择器内明确提示）。
async function selectDir(index) {
  try {
    const path = await pickDir()
    if (path) {
      entries.value[index].dir = path
      save()
    }
  } catch (_) {
    const path = prompt(t('security.dir_path_prompt'))
    if (path) {
      entries.value[index].dir = path
      save()
    }
  }
}

async function deleteEntry(index) {
  try {
    await confirm(t('security.delete_confirm'))
  } catch { return }
  const removed = entries.value.splice(index, 1)[0]
  if (removed && removed._key) _removedKeys.push(removed._key)
  save()
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
/* 安全策略提示（agentbox 已在执行层强制执行，见 14-安全域-agentbox） */
.security-hint {
  margin: 0 0 8px;
  padding: 6px 10px;
  border-radius: 4px;
  background: var(--warning-bg);
  border: 1px solid var(--warning-border);
  color: var(--text-secondary, #6b5b1e);
  font-size: var(--font-size-sm);
  flex-shrink: 0;
}
.table-wrap {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}
.b-table-inline {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-size-sm);
}
.b-table-inline thead {
  position: sticky;
  top: 0;
  z-index: 1;
}
.b-table-inline th {
  padding: 8px 12px;
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
  border-bottom: 1px solid var(--border, #dee2e6);
  background: var(--bg-secondary, #fff);
  color: var(--text-secondary, #495057);
  user-select: none;
}
.b-table-inline td {
  padding: 7px 12px;
  border-bottom: 1px solid var(--border, #dee2e6);
  color: var(--text-primary, #212529);
  line-height: 1.4;
}

/* 操作列按钮：足够大的点击区域 + 留白，避免"只能点到文字" */
.b-table-inline td .b-btn {
  min-width: 52px;
  margin: 2px;
}
.b-table-inline tbody tr:hover {
  background: var(--bg-hover, #e9ecef);
}
.b-table-empty {
  text-align: center;
  padding: 24px 12px;
  color: var(--text-muted, #6c757d);
  font-size: var(--font-size-sm);
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