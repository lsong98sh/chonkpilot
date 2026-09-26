<template>
  <div class="settings-page">
    <div class="page-body">
      <div class="config-toolbar-actions">
        <Button type="primary" v-mq:[EventNames.configAddMcp].click>
          <Icon name="plus" :size="14" /> {{ $t('config.addMCP') }}
        </Button>
        <template v-if="systemMCPs.length">
          <Select
            v-model="selectedBuiltin"
            class="builtin-select"
            :placeholder="$t('config.mcp.builtinPlaceholder')"
            :options="builtinOptions"
          />
          <Button :disabled="selectedBuiltin === ''" v-mq:[EventNames.configAddMcpBuiltin].click>
            {{ $t('config.mcp.addBuiltin') }}
          </Button>
        </template>
      </div>
      <!-- 两页状态互见：executor 级（tool_sandbox）显式开启数 + 一键跳转 -->
      <div class="cross-hint">
        <span>{{ $t('config.mcp.toolSandboxSummary', { n: toolSandboxOn }) }}</span>
        <Button text size="small" @click="gotoToolSandbox">{{ $t('config.mcp.gotoToolSandbox') }}</Button>
      </div>
      <!-- D：server 级空信任目录语义说明（= 不施加隔离，非「全部拒绝」；与 executor 级全拒区分） -->
      <div class="sandbox-note">{{ $t('config.mcp.sandboxEmptyHint') }}</div>
      <Table :columns="mcpColumns" :data="displayData" :empty-text="$t('config.mcp.empty')" size="small">
        <template #action="{ index }">
          <Button text v-mq:[EventNames.configToggleMcp].click="{ index }">
            {{ mcpServers[index] && mcpServers[index].enabled ? $t('config.mcp.disable') : $t('config.mcp.enable') }}
          </Button>
          <Button text v-mq:[EventNames.configEditMcp].click="{ index }">{{ $t('common.edit') }}</Button>
          <Button text type="danger" v-mq:[EventNames.configDeleteMcp].click="{ index }">{{ $t('common.delete') }}</Button>
        </template>
      </Table>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, h, defineAsyncComponent } from 'vue'
import { useI18n } from 'vue-i18n'
import { Table, Button, Select, message, confirm } from '../../components/ui'
import { dialog } from '../../components/dialog'
import Icon from '../../components/icon/Icon.vue'
import { getUserConfig, saveUserConfig, getSystemBuiltins } from '../../api/config'
import { DEFAULT_MCP } from '../../config/defaults'
import { countToolSandboxOn } from '../../utils/sandboxSummary'
import { loadFailedText } from '../../utils/settingsFeedback'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const EditMCPDialog = defineAsyncComponent(() => import('./EditMCPDialog.vue'))

const { t } = useI18n()

const mcpServers = ref([])
const systemMCPs = ref([])
const selectedBuiltin = ref('')
// executor 级（tool_sandbox）显式开启数 —— 供「两页状态互见」摘要（读 usr 同一次加载）。
const toolSandboxOn = ref(0)

// 「两页状态互见」跳转：既有 preview tab 通道（kind=settings-tool-sandbox），零新增消息面。
function gotoToolSandbox() {
  mq.emit(EventNames.previewTabOpen, { kind: 'settings-tool-sandbox' })
}

const mcpColumns = computed(() => [
  { label: '#', type: 'index', width: 40 },
  { label: t('config.mcp.name'), prop: 'name', minWidth: 120 },
  { label: 'URL', prop: 'url', minWidth: 200 },
  { label: t('config.mcp.description'), prop: 'description', minWidth: 140 },
  { label: t('config.mcp.isolate'), prop: '_isolate', width: 90, align: 'center' },
  { label: t('config.mcp.enabled'), prop: '_enabled', width: 60, align: 'center' },
  { label: t('config.table.operation'), type: 'action', width: 200, align: 'center' },
])

const displayData = computed(() =>
  mcpServers.value.map(s => ({
    ...s,
    _isolate: isolateLabel(s),
    _enabled: s.enabled ? t('dialog.yes') : t('dialog.no'),
  }))
)

// isolateLabel 列表展示「按项目隔离」状态：显式设置 → 是/否；未设置 → 推断值 + 标注（自动）。
// 推断口径与 gateway（ServerEntry.IsolateEnabled）一致：stdio → 隔离、http/sse → 共享；
// transport 留空按连接点推断（仅 runtime → stdio）。
function isolateLabel(s) {
  const explicit = s.isolate === true || s.isolate === false
  const t0 = (s.transport || '').trim().toLowerCase()
  const inferred = t0 === 'stdio' ? true
    : (t0 === 'http' || t0 === 'sse' ? false : !!(s.runtime || '').trim() && !(s.url || '').trim())
  const on = explicit ? !!s.isolate : inferred
  const text = on ? t('dialog.yes') : t('dialog.no')
  return explicit ? text : text + t('config.mcp.isolateAutoSuffix')
}

// ── 沙箱（sandbox，三态）的编辑入口在 EditMCPDialog「运行信息」页签（仅 stdio 可开）；
//    本列表页只保留跨页摘要与空信任目录语义说明，不再提供行内拨动。
const builtinOptions = computed(() =>
  systemMCPs.value.map((s, i) => ({ label: s.name || ('#' + (i + 1)), value: String(i) }))
)

async function loadConfig() {
  try {
    const res = await getUserConfig()
    const uc = res.config || res
    mcpServers.value = Array.isArray(uc.mcpServers) ? uc.mcpServers : []
    // 同一次加载顺带取 executor 级沙箱开启数（两页状态互见摘要）
    toolSandboxOn.value = countToolSandboxOn(uc.tool_sandbox)
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console；成功路径不动）
    console.warn('[SettingsMCP] load user config failed:', e)
    message.error(loadFailedText(t, t('config.page.mcp'), e))
  }
}

async function loadSystem() {
  try {
    const b = await getSystemBuiltins()
    systemMCPs.value = b.mcpServers
  } catch (_) {
    systemMCPs.value = []
  }
}

// 列表类即落盘（CFG-015-S01）
async function saveNow(tip) {
  try {
    await saveUserConfig({ mcpServers: mcpServers.value })
    if (tip) message.success(tip)
    return true
  } catch (e) {
    message.error(t('config.save_failed') + ': ' + (e.message || ''))
    return false
  }
}

function openEditor(data, index) {
  const handle = dialog.show(h(EditMCPDialog, {
    initialData: { ...data },
    editIndex: index,
    onSave: async (d, idx) => {
      if (idx === -1) mcpServers.value.push(d)
      else mcpServers.value[idx] = d
      handle.close()
      await saveNow(t('config.mcp.saved'))
    },
    onCancel: () => handle.close(),
  }), { title: t('config.mcp.editTitle'), width: 520, height: 640, minimizable: false, closable: true })
}

function addMCP() { openEditor({ ...DEFAULT_MCP }, -1) }

// 从系统内置项添加（默认 disabled，可再编辑）
function addBuiltin() {
  const idx = parseInt(selectedBuiltin.value)
  const src = systemMCPs.value[idx]
  if (!src) return
  mcpServers.value.push({ ...DEFAULT_MCP, ...src, enabled: false })
  selectedBuiltin.value = ''
  saveNow(t('config.mcp.saved'))
}

function editMCP(index) { openEditor({ ...mcpServers.value[index] }, index) }

async function deleteMCP(index) {
  try { await confirm(t('config.mcp.confirmDelete')) } catch { return }
  mcpServers.value.splice(index, 1)
  await saveNow(t('config.mcp.saved'))
}

async function toggleMCP(index) {
  const s = mcpServers.value[index]
  if (!s) return
  s.enabled = !s.enabled
  await saveNow()
}

const unsubs = []
onMounted(() => {
  loadConfig()
  loadSystem()
  unsubs.push(mq.on(EventNames.configAddMcp, addMCP))
  unsubs.push(mq.on(EventNames.configAddMcpBuiltin, addBuiltin))
  unsubs.push(mq.on(EventNames.configEditMcp, ({ index }) => editMCP(index)))
  unsubs.push(mq.on(EventNames.configDeleteMcp, ({ index }) => deleteMCP(index)))
  unsubs.push(mq.on(EventNames.configToggleMcp, ({ index }) => toggleMCP(index)))
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
}
.config-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.builtin-select { width: 220px; }
.cross-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 8px;
}
.sandbox-note {
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 8px;
}
</style>
