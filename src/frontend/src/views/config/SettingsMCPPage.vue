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
            {{ mcpList[index] && mcpList[index].enabled ? $t('config.mcp.disable') : $t('config.mcp.enable') }}
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
import { getUserConfig, getSystemBuiltins, listMcpServers, saveMcpServer, deleteMcpServer } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { DEFAULT_MCP } from '../../config/defaults'
import { countToolSandboxOn } from '../../utils/sandboxSummary'
import { loadFailedText } from '../../utils/settingsFeedback'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const EditMCPDialog = defineAsyncComponent(() => import('./EditMCPDialog.vue'))

const { t } = useI18n()

const mcpList = ref([])
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
  { label: t('config.mcp.level'), prop: '_level', width: 90, align: 'center' },
  { label: 'URL', prop: 'url', minWidth: 200 },
  { label: t('config.mcp.description'), prop: 'description', minWidth: 140 },
  { label: t('config.mcp.isolate'), prop: '_isolate', width: 90, align: 'center' },
  { label: t('config.mcp.enabled'), prop: '_enabled', width: 60, align: 'center' },
  { label: t('config.table.operation'), type: 'action', width: 200, align: 'center' },
])

const displayData = computed(() =>
  mcpList.value.map(s => ({
    ...s,
    _level: levelLabel(s.level),
    _isolate: isolateLabel(s),
    _enabled: s.enabled ? t('dialog.yes') : t('dialog.no'),
  }))
)

// levelLabel 级别展示名（app/user/project/prjusr → scenario.level.* 文案；缺省按 user）。
function levelLabel(level) {
  const k = level || 'user'
  return t('scenario.level.' + k)
}

// isolateLabel 列表展示「按实例隔离」状态：显式设置 → 是/否；未设置 → 推断值 + 标注（自动）。
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
    // MCP server 列表 = 新数据层 mcp 域（四级文件化合并视图；同名最具体级优先）
    mcpList.value = await listMcpServers()
    // executor 级沙箱开启数仍取 usr 配置（两页状态互见摘要）
    const uc = (await getUserConfig()).config || {}
    toolSandboxOn.value = countToolSandboxOn(uc.tool_sandbox)
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console；成功路径不动）
    console.warn('[SettingsMCP] load mcp servers failed:', e)
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

// 保存单条（新数据层 mcp 域：按 server.level 落文件；改名/移级 → 传 oldName/oldLevel 先删旧文件）
async function saveOne(server, oldName, oldLevel, tip) {
  try {
    await saveMcpServer(server, oldName, oldLevel)
    if (tip) message.success(tip)
    await loadConfig()
    return true
  } catch (e) {
    message.error(t('config.save_failed') + ': ' + (e.message || ''))
    return false
  }
}

function openEditor(data, index) {
  const orig = index >= 0 ? mcpList.value[index] : null
  const handle = dialog.show(h(EditMCPDialog, {
    initialData: { ...data },
    editIndex: index,
    onSave: async (d) => {
      handle.close()
      await saveOne(d, orig && orig.name, orig && orig.level, t('config.mcp.saved'))
    },
    onCancel: () => handle.close(),
  }), { title: t('config.mcp.editTitle'), width: 520, height: 640, bodyClass: 'form-dialog-body', minimizable: false, closable: true })
}

function addMCP() { openEditor({ ...DEFAULT_MCP }, -1) }

// 从系统内置项添加（默认 disabled，可再编辑）
function addBuiltin() {
  const idx = parseInt(selectedBuiltin.value)
  const src = systemMCPs.value[idx]
  if (!src) return
  selectedBuiltin.value = ''
  saveOne({ ...DEFAULT_MCP, ...src, enabled: false }, '', '', t('config.mcp.saved'))
}

function editMCP(index) { openEditor({ ...mcpList.value[index] }, index) }

async function deleteMCP(index) {
  try { await confirm(t('config.mcp.confirmDelete')) } catch { return }
  const s = mcpList.value[index]
  if (!s) return
  try {
    // 按名删（level 空 = 删最具体级副本，与列表所示一致）
    await deleteMcpServer(s.name)
    message.success(t('config.mcp.saved'))
    await loadConfig()
  } catch (e) {
    message.error(t('config.save_failed') + ': ' + (e.message || ''))
  }
}

async function toggleMCP(index) {
  const s = mcpList.value[index]
  if (!s) return
  await saveOne({ ...s, enabled: !s.enabled }, '', '')
}

const unsubs = []
onMounted(() => {
  loadConfig()
  loadSystem()
  unsubs.push(onDataRefresh('mcp', loadConfig)) // 四级文件配置 save/delete 后即时刷新
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
