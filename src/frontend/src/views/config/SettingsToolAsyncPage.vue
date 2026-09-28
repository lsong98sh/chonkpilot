<!--
  工具配置页（原名「工具异步配置」；usr 级；设置菜单 → preview tab kind = settings-tool-async）

  需求（用户口径，2026-09-26 改版；2026-09-28 增「涉及文件变动」）：
    - 明细用**表格**（Table 组件）呈现：工具 / 模式 / 阈值 / 超时 / **涉及文件变动**；工具名列
      min-width 200px；仍按 MCP（`_meta.server`）分组（`.tool-group` + `.group-title`）。
    - **手动保存**：模式选择 / 数值输入 / 开关只改本地待保存态（显示「未保存」），点页头【保存】一次性
      提交本页全部变更（usr 键 `tool_async`，走既有 `data-user-config-save`，**零消息面变更**）；
      **无改动时保存按钮禁用**。
    - **不再显示默认配置信息**：原「契约默认/用户配置」徽标与「契约现值」文本移除；契约默认信息
      改由「恢复默认」按钮的 tooltip 承载。
    - **去「高级」按钮**：`hard_timeout`（执行硬上限）常显为表格「超时」列；dir 节点行该列禁用 +
      标「不适用」。
    - **超时/阈值取值口径（2026-09-27）**：未设置（留空）= 回落全局/契约默认；**0 / -1 = 无上限**
      （永远等待，用户可取消）；正数 = 该秒数为硬上限。
    - **「涉及文件变动」列（2026-09-28）**：每行一个开关 + 派生「打点 / 不打点」标记。缺省由工具来源
      给出（self 内置仅 `filesys_run` / `script_run` 涉及 = 打点；其余内置不涉及 = 不打点；dir 节点 /
      第三方 / 无法判定 = 涉及 = 打点，保守）；与缺省一致的项**不写库**（保持配置干净）。
      生效点 = `plugin-history` 的前置打点钩子（`history-pre-tool-hook`）：标「不打点」的工具直接放行、
      不打检查点（省 8–9 次 git 进程）；轮末补点仍在（保证总有产像），标错只会让检查点粒度变粗，
      不丢安全。
    - 模式 / 阈值 / 超时 / 涉及文件变动 四列表头各带 `?` + Tooltip 说明。

  读写面（**零新增 MQ 主题**）：
    - 工具清单 = 既有客户端能力面 `tools-list`（与 useToolAsyncMode.js / ScenarioEditDialog 同一消费点）
    - 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（api/config.js 的
      getUserConfig / saveUserConfig / resetUserKey）
  存储形态：usr 键 `tool_async`（自由键通道）= JSON 对象
    {"<工具暴露名>": {"mode":"always|never|auto|manual", "threshold":30, "hard_timeout":300,
                      "touch_files":true|false}}
    - 未配置的工具：显示**契约现值**（`_meta.async` / `_meta.async-threshold` / `_meta.timeout`），**不写库**；
    - `touch_files`：与缺省（`utils/toolTouchFiles.js` `defaultTouchFiles`）一致 → 不写该字段；
    - 「恢复默认」= 从待保存态删该工具键项（点【保存】落库；最后一项删除后整键删除，走
      `data-user-config-delete{id}`）；
    - 与契约现值一致的行不写入（保持配置干净）。
-->
<template>
  <div class="settings-page">
    <div class="page-body">
      <div class="tool-toolbar">
        <span class="hint">{{ $t('config.toolAsync.pageHint') }}</span>
        <span v-if="dirty" class="unsaved-mark" data-async-unsaved>{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" :loading="loading" @click="reload">
          <Icon name="refresh" :size="13" /> {{ $t('config.page.redetect') }}
        </Button>
        <Button
          type="primary"
          size="small"
          data-async-save
          :disabled="!dirty"
          :loading="saving"
          @click="onSave"
        >{{ $t('common.save') }}</Button>
      </div>

      <div v-if="!groups.length" class="tool-empty">{{ $t('config.toolAsync.empty') }}</div>

      <div v-for="g in groups" :key="g.key" class="tool-group">
        <div class="group-title">{{ g.key }}<span class="group-count">（{{ g.tools.length }}）</span></div>

        <Table :columns="columns" :data="g.tools" size="small">
          <!-- 工具名：展示剥前缀（网关加的 self_/<节点名>_），:title / data-tool = 完整暴露名（重名可区分） -->
          <template #name="{ row }">
            <div class="tool-name" :title="row.name" :data-tool="row.name">
              <span class="mono">{{ stripToolPrefix(row.name, row.server) }}</span>
              <span class="tool-desc">{{ row.description }}</span>
            </div>
          </template>

          <template #mode="{ row }">
            <div class="cell-mode">
              <Select
                :model-value="row.mode"
                :options="modeOptions"
                :aria-label="$t('config.toolAsync.mode') + ': ' + row.name"
                @update:model-value="(v) => onModeChange(row, v)"
              />
            </div>
          </template>

          <!-- 阈值：仅 auto / manual 有意义（其它档以「—」呈现） -->
          <template #threshold="{ row }">
            <div class="cell-threshold">
              <Input
                v-if="isThresholdMode(row)"
                :model-value="row.threshold"
                :aria-label="$t('config.toolAsync.threshold')"
                :placeholder="row.cThreshold === '' ? $t('config.toolAsync.thresholdPlaceholder') : String(row.cThreshold)"
                @update:model-value="(v) => onNumberInput(row, 'threshold', v)"
                @blur="() => onNumberBlur(row, 'threshold')"
              />
              <span v-else class="cell-dash">—</span>
            </div>
          </template>

          <!-- 执行硬上限 hard_timeout：常显（原「高级」折叠已去掉）。
               占位符「回落全局」= 该工具**未设置**执行硬上限（契约未声明 `_meta.timeout`），
               执行侧回落全局默认（CallTimeout / timeout_sec）。
               显示 `0` / `-1` = 该工具**显式无上限**（永远等待，用户可取消）。 -->
          <template #timeout="{ row }">
            <div class="cell-timeout">
              <Input
                :model-value="row.hardTimeout"
                :disabled="row.dirNode === true"
                :aria-label="$t('config.toolAsync.hardTimeout')"
                :placeholder="row.cTimeout === '' ? $t('config.toolAsync.hardTimeoutPlaceholder') : String(row.cTimeout)"
                @update:model-value="(v) => onNumberInput(row, 'hard_timeout', v)"
                @blur="() => onNumberBlur(row, 'hard_timeout')"
              />
              <!-- I-109 边界：dir 节点工具 hard_timeout 不适用（mode / 阈值仍适用） -->
              <span
                v-if="row.dirNode === true"
                class="na-hint"
                :title="$t('config.toolAsync.hardTimeoutNotApplicable')"
              >{{ $t('config.toolAsync.naBadge') }}</span>
            </div>
          </template>

          <!-- 涉及文件变动（2026-09-28）：开关 + 派生「打点 / 不打点」标记。
               与缺省一致 → 不写库；关闭 = 不涉及 → plugin-history 前置钩子直接放行、不打点。 -->
          <template #touch="{ row }">
            <div class="cell-touch">
              <Switch
                :model-value="row.touch"
                :aria-label="$t('config.toolAsync.touchFiles') + ': ' + row.name"
                @update:model-value="(v) => onTouchChange(row, v)"
              />
              <span
                class="touch-badge"
                :class="row.touch ? 'is-checkpoint' : 'is-skip'"
                :title="$t('config.toolAsync.touchFilesHint')"
              >{{ row.touch ? $t('config.toolAsync.checkpointOn') : $t('config.toolAsync.checkpointOff') }}</span>
            </div>
          </template>

          <!-- 恢复默认：契约默认信息（原行内「契约现值」）改由本按钮 tooltip 承载 -->
          <template #action="{ row }">
            <Tooltip :content="contractTooltip(row)" :data-contract="contractTooltip(row)">
              <Button text type="danger" data-restore :disabled="!isUserSet(row)" @click="restoreDefault(row)">
                {{ $t('config.toolAsync.restoreDefault') }}
              </Button>
            </Tooltip>
          </template>

          <!-- 表头 ? 说明（复用既有 i18n 文案） -->
          <template #header-mode>
            <span class="th-help">
              {{ $t('config.toolAsync.mode') }}
              <Tooltip :content="$t('config.toolAsync.modeHint')"><Icon name="help" :size="12" /></Tooltip>
            </span>
          </template>
          <template #header-threshold>
            <span class="th-help">
              {{ $t('config.toolAsync.threshold') }}
              <Tooltip :content="$t('config.toolAsync.thresholdHint')"><Icon name="help" :size="12" /></Tooltip>
            </span>
          </template>
          <template #header-timeout>
            <span class="th-help">
              {{ $t('config.toolAsync.hardTimeout') }}
              <Tooltip :content="$t('config.toolAsync.advancedHint')"><Icon name="help" :size="12" /></Tooltip>
            </span>
          </template>
          <template #header-touch>
            <span class="th-help">
              {{ $t('config.toolAsync.touchFiles') }}
              <Tooltip :content="$t('config.toolAsync.touchFilesHint')"><Icon name="help" :size="12" /></Tooltip>
            </span>
          </template>
        </Table>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input, Button, Select, Switch, Table, Tooltip, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { getUserConfig, saveUserConfig, resetUserKey } from '../../api/config'
import { isDirNode, stripToolPrefix } from '../../utils/toolSource'
import { defaultTouchFiles, effectiveTouchFiles, touchFilesOverride } from '../../utils/toolTouchFiles'
import { loadFailedText, saveFailedText } from '../../utils/settingsFeedback'
import mq from '../../utils/mq'

const { t } = useI18n()

// usr 配置键（自由键通道）：值是 JSON 对象字符串（persist 侧自由键按字符串原样存取）。
const CFG_KEY = 'tool_async'
// 契约默认异步阈值兜底（spec 72 参考值；契约 `async-threshold` 缺省 = 契约 `timeout`）
const FALLBACK_THRESHOLD = 30
// 四档模式（顺序即选择器顺序）
const MODES = ['always', 'never', 'auto', 'manual']

const loading = ref(false)
const saving = ref(false)
const groups = ref([]) // [{ key, tools: [row] }]
const savedMap = ref({}) // 上次落库态（usr `tool_async`）
const workMap = ref({}) // 本地待保存态（改模式/数值只动它；点【保存】才落库）

const modeOptions = computed(() => [
  { label: t('config.toolAsync.modeAlways'), value: 'always' },
  { label: t('config.toolAsync.modeNever'), value: 'never' },
  { label: t('config.toolAsync.modeAuto'), value: 'auto' },
  { label: t('config.toolAsync.modeManual'), value: 'manual' },
])

const columns = computed(() => [
  { label: t('config.toolAsync.tool'), prop: 'name', minWidth: 200 },
  { label: t('config.toolAsync.mode'), prop: 'mode', width: 150 },
  { label: t('config.toolAsync.threshold'), prop: 'threshold', width: 120 },
  { label: t('config.toolAsync.hardTimeout'), prop: 'timeout', width: 140 },
  { label: t('config.toolAsync.touchFiles'), prop: 'touch', width: 150 },
  { label: t('config.table.operation'), type: 'action', width: 130, align: 'center' },
])

// 契约现值：tools-list 每项 `_meta`（25-mcp-server § 透出 hot/category/async/async-threshold/timeout）
// 键缺失/空/非数字 → ''（未设置，回落）；数字（含 **0 / -1 = 无上限**）→ 数值。
function metaNum(v) {
  if (v === undefined || v === null || v === '') return ''
  const n = Number(v)
  return Number.isFinite(n) ? n : ''
}

// 「阈值 / 硬上限」合法输入：正数 = 秒数；0 / -1 = **无上限**（永远等待，用户可取消）；其余 → 非法。
function isLimitValue(v) {
  if (v === undefined || v === null || String(v).trim() === '') return false
  const n = Number(v)
  return Number.isFinite(n) && (n >= 0 || n === -1)
}

function isThresholdMode(row) {
  return row.mode === 'auto' || row.mode === 'manual'
}

function defaultThreshold(row) {
  return row.cThreshold !== '' ? row.cThreshold : (row.cTimeout !== '' ? row.cTimeout : FALLBACK_THRESHOLD)
}

function modeLabel(m) {
  const key = { always: 'modeAlways', never: 'modeNever', auto: 'modeAuto', manual: 'modeManual' }[m]
  return key ? t('config.toolAsync.' + key) : m
}

// 「恢复默认」tooltip：说明该工具的契约默认（原行内「契约现值」文本改由此承载）
function contractTooltip(row) {
  return t('config.toolAsync.contractTooltip', {
    mode: modeLabel(row.cMode),
    threshold: row.cThreshold === '' ? '—' : (row.cThreshold + 's'),
    timeout: row.cTimeout === '' ? '—' : (row.cTimeout + 's'),
  })
}

// 解析 usr `tool_async`：允许对象形态与 JSON 字符串形态（自由键按字符串回读）。
function parseToolAsync(v) {
  let obj = v
  if (typeof obj === 'string') {
    try { obj = JSON.parse(obj) } catch (_) { return {} }
  }
  return obj && typeof obj === 'object' && !Array.isArray(obj) ? obj : {}
}

// 生效项是否等于契约现值（等于 → 视为未改动，不进库）
function sameAsContract(row, e) {
  if (e.mode !== row.cMode) return false
  const cur = row.cThreshold === '' ? '' : String(row.cThreshold)
  const t0 = e.threshold === undefined ? '' : String(e.threshold)
  if (t0 !== cur) return false
  if (e.hard_timeout !== undefined) return false
  // touch_files 仅在**显式偏离缺省**时才写入 → 出现即视为已配置（不算「等于契约现值」）
  if (e.touch_files !== undefined) return false
  return true
}

// 行 → usr 键项（阈值仅 auto/manual 计入；dir 节点不写 hard_timeout；touch_files 仅偏离缺省时写入）。
// 数值：正数 / 0 / -1 均为合法（0/-1 = 无上限），落库保留原值。
function buildEntry(row) {
  const e = { mode: row.mode }
  if (isThresholdMode(row)) {
    const n = Number(row.threshold)
    e.threshold = isLimitValue(row.threshold) ? n : defaultThreshold(row)
  }
  // I-109：dir 节点工具的 hard_timeout 不生效 → 不写库（避免「配了不生效」的误导）
  if (!row.dirNode && row.hardTimeout !== '' && row.hardTimeout !== null) {
    if (isLimitValue(row.hardTimeout)) e.hard_timeout = Number(row.hardTimeout)
  }
  // 涉及文件变动：与缺省一致 → 不写（保持配置干净）
  const tf = touchFilesOverride(row.touch, row.name, row.server)
  if (tf !== undefined) e.touch_files = tf
  return e
}

// 待保存态 → 行显示值（workMap 优先，其次已落库态，最后契约现值）
function resetRowFromMaps(row) {
  const u = workMap.value[row.name] || savedMap.value[row.name]
  if (u && typeof u === 'object') {
    row.mode = MODES.includes(u.mode) ? u.mode : row.cMode
    row.threshold = u.threshold !== undefined && u.threshold !== null ? String(u.threshold) : String(defaultThreshold(row))
    row.hardTimeout = u.hard_timeout !== undefined && u.hard_timeout !== null ? String(u.hard_timeout) : ''
    row.touch = effectiveTouchFiles(u, row.name, row.server)
  } else {
    row.mode = row.cMode
    row.threshold = String(defaultThreshold(row))
    row.hardTimeout = ''
    row.touch = defaultTouchFiles(row.name, row.server)
  }
}

function applyRows() {
  for (const g of groups.value) for (const row of g.tools) resetRowFromMaps(row)
}

// 编辑一行 → 只改本地待保存态（不落库）：与契约一致 → 从待保存态删除该行。
function syncRow(row) {
  const next = { ...workMap.value }
  const e = buildEntry(row)
  if (sameAsContract(row, e)) delete next[row.name]
  else next[row.name] = e
  workMap.value = next
}

function isUserSet(row) {
  return Object.prototype.hasOwnProperty.call(workMap.value, row.name)
}

// 待保存态是否与已落库态不同（无改动 → 保存按钮禁用）
function canon(e) {
  if (!e || typeof e !== 'object') return ''
  return JSON.stringify({ mode: e.mode, threshold: e.threshold, hard_timeout: e.hard_timeout, touch_files: e.touch_files })
}
const dirty = computed(() => {
  const a = workMap.value
  const b = savedMap.value
  const keys = new Set([...Object.keys(a), ...Object.keys(b)])
  for (const k of keys) {
    if (canon(a[k]) !== canon(b[k])) return true
  }
  return false
})

async function loadTools() {
  loading.value = true
  try {
    // 分组口径与既有 tools-list 消费点一致（ScenarioEditDialog.loadToolGroups）：
    // `_meta.server.alias || _meta.server.node || '全局'`。
    const env = await mq.emit('tools-list', {})
    const res = env && env.backend && env.backend.result
    const tools = res && Array.isArray(res.tools) ? res.tools : []
    const byServer = new Map()
    for (const tl of tools) {
      const meta = (tl && tl._meta) || {}
      const srv = meta.server || {}
      const key = srv.alias || srv.node || t('config.toolAsync.ungrouped')
      if (!byServer.has(key)) byServer.set(key, [])
      byServer.get(key).push({
        name: tl.name,
        description: tl.description || '',
        // 展示用：网关为暴露名加的前缀（self_/dir_ 等）剥掉后显示（`row.name` 仍作配置键）
        server: srv,
        // I-109 边界：dir 节点工具的 hard_timeout 未接线（gateway 下发暴露名、mcp-server 按契约名
        // 查 → 不达）；mode/threshold 对全部工具均适用。无 `_meta.server` → false（不标注）。
        dirNode: isDirNode(meta),
        cMode: meta.async || 'auto',
        cThreshold: metaNum(meta['async-threshold']),
        cTimeout: metaNum(meta.timeout),
        mode: meta.async || 'auto',
        threshold: '',
        hardTimeout: '',
        // 涉及文件变动：缺省由工具来源派生（self 内置仅 filesys_run/script_run 涉及）；
        // 已落库/待保存值由 applyRows → resetRowFromMaps 覆盖。
        touch: defaultTouchFiles(tl.name, srv),
      })
    }
    groups.value = [...byServer.entries()].map(([key, list]) => ({ key, tools: list }))
    applyRows()
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console；成功路径不动）
    console.warn('[SettingsToolAsync] load tools failed:', e)
    message.error(loadFailedText(t, t('config.page.toolAsync'), e))
    groups.value = []
  } finally {
    loading.value = false
  }
}

async function loadUserConfig() {
  try {
    const res = await getUserConfig()
    const uc = res.config || res
    const parsed = parseToolAsync(uc[CFG_KEY])
    savedMap.value = { ...parsed }
    workMap.value = { ...parsed }
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console；与 loadTools 用不同 item，避免两条同文案刷屏）
    console.warn('[SettingsToolAsync] load user config failed:', e)
    message.error(loadFailedText(t, t('config.toolAsync.sourceUser'), e))
    savedMap.value = {}
    workMap.value = {}
  }
  applyRows()
}

async function reload() {
  await loadUserConfig()
  await loadTools()
}

// 手动保存：一次性提交本页全部待保存变更（无改动 → 不写库）。
// 注意 saveUserConfig 是**增量合并**（persist_userconfig.go saveUserConfig），只写本键不影响其它配置。
async function onSave() {
  if (!dirty.value) return
  saving.value = true
  try {
    if (Object.keys(workMap.value).length === 0) await resetUserKey(CFG_KEY)
    else await saveUserConfig({ [CFG_KEY]: workMap.value })
    savedMap.value = { ...workMap.value }
    message.success(t('config.toolAsync.saved'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

function onModeChange(row, v) {
  if (!v || v === row.mode) return
  row.mode = v
  if (isThresholdMode(row) && !row.threshold) row.threshold = String(defaultThreshold(row))
  syncRow(row)
}

// 涉及文件变动开关：只改本地待保存态（与缺省一致 → 该字段不落库）
function onTouchChange(row, v) {
  if (v === row.touch) return
  row.touch = v
  syncRow(row)
}

// 数值输入：只改本地待保存态（不校验、不落库）
function onNumberInput(row, field, v) {
  if (field === 'threshold') row.threshold = v
  else row.hardTimeout = v
  syncRow(row)
}

// 数值失焦：① 非法/空（threshold）→ 显示值回落（savedMap / 契约现值）；
// ② hard_timeout 清空 = 未设置硬上限（回落全局）；③ 合法（含 **0 / -1 = 无上限**）→ 同步待保存态。
function onNumberBlur(row, field) {
  // I-109：dir 节点工具的 hard_timeout 不适用（输入已禁用，此处兜底）
  if (field === 'hard_timeout' && row.dirNode === true) return
  const raw = field === 'threshold' ? row.threshold : row.hardTimeout
  const legal = isLimitValue(raw)
  const emptyHard = field === 'hard_timeout' && (raw === '' || raw === null)
  if (!legal && !emptyHard) {
    const u = savedMap.value[row.name]
    if (field === 'threshold') {
      row.threshold = u && u.threshold != null ? String(u.threshold) : String(defaultThreshold(row))
    } else {
      row.hardTimeout = u && u.hard_timeout != null ? String(u.hard_timeout) : ''
    }
  }
  syncRow(row)
}

// 恢复默认 = 从待保存态删除该工具键项（点【保存】落库；最后一项 → 整键删除）。
// 行显示明确回落契约现值（不读 savedMap，否则会显示「已落库的旧值」而非契约默认）。
function restoreDefault(row) {
  const next = { ...workMap.value }
  delete next[row.name]
  workMap.value = next
  row.mode = row.cMode
  row.threshold = String(defaultThreshold(row))
  row.hardTimeout = ''
  row.touch = defaultTouchFiles(row.name, row.server)
}

onMounted(reload)
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
  /* 两轴滚动都由页面自身承担（2026-09-27 用户口径）：滚动条始终贴在 **preview 区**边缘 ——
     纵向滚内容；需要横向时在**底部**即出现横向滚动条，而不是滚到表格底部才见到它。 */
  overflow: auto;
  padding: 8px 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
/* 表格不再自建横向滚动容器（否则其滚动条贴表格底边、要滚到底才可见）→ 交给 .page-body */
.page-body :deep(.b-table-wrapper) {
  overflow-x: visible;
}
.tool-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}
.hint {
  font-size: 12px;
  color: var(--fg-secondary);
  flex: 1;
}
.unsaved-mark {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--warning, #e6a23c);
}
.tool-empty {
  font-size: 13px;
  color: var(--text-muted);
  padding: 12px 4px;
}
.tool-group {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 6px 10px 10px;
  /* 卡片随表格内容变宽（否则表格会戳出卡片边框）→ 溢出部分由 .page-body 横向滚动 */
  min-width: max-content;
}
.group-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-secondary);
  padding: 4px 0 8px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 8px;
}
.group-count {
  font-weight: 400;
  color: var(--text-muted);
}
.tool-name {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.mono {
  font-family: var(--font-mono, Consolas, monospace);
  font-size: 12px;
  color: var(--text-primary);
  word-break: break-all;
}
.tool-desc {
  font-size: 11px;
  color: var(--text-muted);
  /* 工具内容介绍：**按 200px 宽度截断**（不是 200 个字），2026-09-27 用户口径 */
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cell-mode { width: 130px; }
.cell-threshold { width: 90px; }
.cell-timeout {
  display: flex;
  align-items: center;
  gap: 6px;
}
.cell-timeout .b-input {
  width: 90px;
  flex-shrink: 0;
}
.cell-dash {
  color: var(--text-muted);
}
/* 涉及文件变动：开关 + 派生「打点 / 不打点」标记 */
.cell-touch {
  display: flex;
  align-items: center;
  gap: 8px;
}
.touch-badge {
  font-size: 11px;
  white-space: nowrap;
  cursor: help;
}
.touch-badge.is-checkpoint { color: var(--success); }
.touch-badge.is-skip { color: var(--text-muted); }
.na-hint {
  font-size: 11px;
  color: var(--warning, #e6a23c);
  white-space: nowrap;
  cursor: help;
}
.th-help {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.th-help .b-tooltip {
  color: var(--text-muted);
}
</style>
