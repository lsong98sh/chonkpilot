<template>
  <div class="dialog-content">
    <div class="toolbar-actions">
      <Button size="small" type="primary" v-mq:[EventNames.scenarioAdd].click>
        <Icon name="plus" :size="14" /> {{ $t('scenario.add') }}
      </Button>
    </div>
    <Table :columns="columns" :data="rows" :empty-text="$t('scenario.empty')">
      <template #action="{ row }">
        <!-- 三级场景（app / user / project）均可编辑/删除（app 级自 2026-09-26 起可编辑，出厂内容由 embed 提供） -->
        <Button text size="small" v-mq:[EventNames.scenarioEditRow].click="{ row }">
          {{ $t('common.edit') }}
        </Button>
        <Button text size="small" type="danger" v-mq:[EventNames.scenarioDeleteRow].click="{ row }">{{ $t('common.delete') }}</Button>
      </template>
    </Table>
  </div>
</template>

<script setup>
import { h, ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { message, confirm } from '../../components/ui'
import { Button, Table } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { dialog } from '../../components/dialog'
import ScenarioEditDialog from './ScenarioEditDialog.vue'
import { getScenarioList, deleteScenario } from '../../api/scenario'
import { onDataRefresh } from '../../utils/dataClient'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()

const emit = defineEmits(['changed'])

const _unsubs = []

const scenarios = ref([])

const columns = computed(() => [
  { label: t('scenario.fields.name'), prop: '_name', minWidth: 160 },
  { label: t('scenario.fields.description'), prop: 'description', minWidth: 200 },
  { label: t('scenario.fields.level'), prop: '_level', width: 90 },
  { label: t('scenario.actions'), type: 'action', width: 200, align: 'center' },
])

const LEVEL_LABEL = { app: 'scenario.level.app', user: 'scenario.level.user', project: 'scenario.level.project' }

// 合并列表显示层：仅在**同名跨级并存**时给名字加 -系统/-用户/-项目 后缀（不改目录名）。
const rows = computed(() => {
  const counts = {}
  for (const s of scenarios.value) counts[s.id] = (counts[s.id] || 0) + 1
  return scenarios.value.map(s => {
    const suffix = counts[s.id] > 1 ? ' -' + t(LEVEL_LABEL[s.level] || 'scenario.level.user') : ''
    return { ...s, _name: (s.name || s.id) + suffix, _level: t(LEVEL_LABEL[s.level] || 'scenario.level.user') }
  })
})

async function loadList() {
  try {
    const res = await getScenarioList()
    scenarios.value = res.scenarios || []
  } catch (e) {
    scenarios.value = []
  }
}

function openEditDialog(scenario) {
  const isNew = !scenario
  const handle = dialog.show(h(ScenarioEditDialog, {
    scenario,
    isNew,
    onDone: () => {
      loadList()
      emit('changed')
      // 立即广播刷新：ChatPanel 场景选择列表 / 其他订阅方同步（CodeView changed
      // 的 scenario-reload 在离开 tab 时才触发，保存后需即时可见）。
      mq.emit(EventNames.scenarioReload, {})
      handle.close()
    },
    // 取消：关闭弹窗、不落库（底部「取消」按钮）
    onCancel: () => handle.close(),
  }), {
    title: isNew ? t('scenario.add') : t('scenario.edit'),
    width: 900,
    height: 640,
    bodyClass: 'scenario-edit-dialog-body',
    closable: true,
  })
}

function handleAdd() {
  openEditDialog(null)
}

function handleEdit(row) {
  openEditDialog(row)
}

async function handleDelete(row) {
  try {
    await confirm(t('scenario.delete_confirm', { name: row.name }), t('dialog.confirm_title'))
    await deleteScenario(row.id, row.level)
    message.success(t('scenario.deleted'))
    await loadList()
    emit('changed')
    mq.emit(EventNames.scenarioReload, {})
  } catch (e) {
    if (e !== 'cancel') message.error(t('scenario.delete_failed') + ': ' + (e.message || e))
  }
}

onMounted(() => {
  loadList()
  // data-scenario-refresh：save/delete 后 server 广播，自动刷新列表（20-gui）
  _unsubs.push(onDataRefresh('scenario', loadList))
  // 按钮事件化：v-mq 触发 → 本地执行
  _unsubs.push(mq.on(EventNames.scenarioAdd, handleAdd))
  _unsubs.push(mq.on(EventNames.scenarioEditRow, ({ row }) => {
    if (row) handleEdit(row)
  }))
  _unsubs.push(mq.on(EventNames.scenarioDeleteRow, ({ row }) => {
    if (row) handleDelete(row)
  }))
})

onUnmounted(() => _unsubs.forEach(fn => fn()))
</script>

<style>
.dialog-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}
.toolbar-actions {
  margin-bottom: 12px;
  flex-shrink: 0;
}
</style>
