<!--
  工具配置页（usr 级；设置菜单 → preview tab kind = settings-tool-async）

  需求（用户口径，2026-09-26 改版；2026-09-28 增「涉及文件变动」；2026-11-14 四模式重构）：
    - 明细用**表格**（Table 组件）呈现：工具 / 异步模式 / 转异步阈值 / 执行硬上限 /
      **涉及文件变动**（打点开关）；工具名列 min-width 200px；仍按 MCP（`_meta.server`）分组。
    - **手动保存**：模式选择 / 数值输入 / 开关只改本地待保存态（显示「未保存」），点页头【保存】一次性
      提交本页全部变更（usr 键 `tool_async`，走既有 `data-user-config-save`，**零消息面变更**）；
      **无改动时保存按钮禁用**。
    - **不再显示默认配置信息**：契约默认信息改由「恢复默认」按钮的 tooltip 承载。
    - **四模式（2026-11-14）**：sync = 仅同步（阻塞等待结果）；async = 仅异步（立即返回 task_id）；
      auto = 自动异步（到阈值自动转后台，不重跑）；manual = 手动异步（到阈值交用户裁决）。
    - **转异步阈值**：仅 auto / manual 可填写（到该秒数转后台 / 交用户裁决）；sync / async 显示「—」
      且不可填写。留空 = 缺省；0 / -1 = 不设点（永远等待，用户可取消）。
    - **执行硬上限**：所有模式可填写；**>0 = 到该秒数直接取消/终止**；<=0 / 留空 = 不处理
      （不写库，回落契约 / 全局）。原「超时动作」「超时自动取消」两列已删除（字段退役）。
    - **「涉及文件变动」列**：每行一个开关 + 派生「打点 / 不打点」标记。缺省**优先读契约声明
      `_meta.touch_files`**（tool.md `touch_files: true|false`），无声明才回落按来源启发式
      （self 内置仅 filesys_run / script_run 涉及；dir 节点 / 第三方 / 无法判定 = 涉及，保守）。
      与缺省一致的项**不写库**（保持配置干净）。
    - 生效点 = `plugin-history` 的前置打点钩子（`history-pre-tool-hook`）：标「不打点」的工具直接放行、
      不打检查点（省 8–9 次 git 进程）；轮末补点仍在（保证总有产像），标错只会让检查点粒度变粗，
      不丢安全。

  读写面（**零新增 MQ 主题**）：
    - 工具清单 = 既有客户端能力面 `tools-list`（与 useToolAsyncMode.js / ScenarioEditDialog 同一消费点）
    - 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（api/config.js 的
      getUserConfig / saveUserConfig / resetUserKey）
  存储形态：usr 键 `tool_async`（自由键通道）= JSON 对象
    {"<工具暴露名>": {"mode":"sync|async|auto|manual", "threshold":30, "hard_timeout":300,
                      "touch_files":true|false}}
    - 未配置的工具：显示**契约现值**（`_meta.async` / `_meta.async-threshold` / `_meta.timeout`），**不写库**；
    - threshold：仅 auto / manual 写入；
    - hard_timeout：仅 **>0** 才写库（<=0 / 留空 = 不处理 → 不写该字段）；
    - touch_files：与缺省（优先 `_meta.touch_files`，无声明回落 `utils/toolTouchFiles.js`）一致 → 不写该字段；
    - 旧「超时动作 / 超时自动取消」字段已退役（读入忽略，不再写）。
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
          <!-- 工具名：展示剥来源前缀（<节点名>_），:title / data-tool = 完整暴露名（重名可区分） -->
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
                :disabled="row.policyLock"
                @update:model-value="(v) => onModeChange(row, v)"
              />
            </div>
          </template>

          <!-- 转异步阈值：仅 auto / manual 有意义（其它档以「—」呈现） -->
          <template #threshold="{ row }">
            <div class="cell-threshold">
              <Input
                v-if="isThresholdMode(row)"
                :model-value="row.threshold"
                :aria-label="$t('config.toolAsync.transferThreshold')"
                :placeholder="row.cThreshold === '' ? $t('config.toolAsync.transferThresholdPlaceholder') : String(row.cThreshold)"
                @update:model-value="(v) => onNumberInput(row, 'threshold', v)"
                @blur="() => onNumberBlur(row, 'threshold')"
              />
              <span v-else class="cell-dash">—</span>
            </div>
          </template>

          <!-- 执行硬上限 hard_timeout：所有模式可编辑。
               占位符「回落全局」= 该工具**未设置**执行硬上限（契约未声明 `_meta.timeout`），
               执行侧回落全局默认（CallTimeout / timeout_sec）。
               >0 = 到该秒数直接取消/终止；<=0 / 留空 = 不处理（不写库）。 -->
          <template #timeout="{ row }">
            <div class="cell-timeout">
              <Input
                :model-value="row.hardTimeout"
                :aria-label="$t('config.toolAsync.hardTimeout')"
                :placeholder="row.cTimeout === '' ? $t('config.toolAsync.hardTimeoutPlaceholder') : String(row.cTimeout)"
                @update:model-value="(v) => onNumberInput(row, 'hard_timeout', v)"
                @blur="() => onNumberBlur(row, 'hard_timeout')"
              />
            </div>
          </template>

          <!-- 涉及文件变动：开关 + 派生「打点 / 不打点」标记。
               缺省优先读契约 `_meta.touch_files`；与缺省一致 → 不写库；
               关闭 = 不涉及 → plugin-history 前置钩子直接放行、不打点。 -->
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

          <!-- 恢复默认：契约默认信息改由本按钮 tooltip 承载 -->
          <template #action="{ row }">
            <Tooltip :content="contractTooltip(row)" :data-contract="contractTooltip(row)">
              <Button text type="danger" data-restore :disabled="!isUserSet(row)" @click="restoreDefault(row)">
                {{ $t('config.toolAsync.restoreDefault') }}
              </Button>
            </Tooltip>
          </template>

          <!-- 表头 ? 说明 -->
          <template #header-mode>
            <span class="th-help">
              {{ $t('config.toolAsync.mode') }}
              <Tooltip :content="$t('config.toolAsync.modeHint')"><Icon name="help" :size="12" /></Tooltip>
            </span>
          </template>
          <template #header-threshold>
            <span class="th-help">
              {{ $t('config.toolAsync.transferThreshold') }}
              <Tooltip :content="$t('config.toolAsync.transferThresholdHint')"><Icon name="help" :size="12" /></Tooltip>
            </span>
          </template>
          <template #header-timeout>
            <span class="th-help">
              {{ $t('config.toolAsync.hardTimeout') }}
              <Tooltip :content="$t('config.toolAsync.hardTimeoutHint')"><Icon name="help" :size="12" /></Tooltip>
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
import { Input, Button, Select, Switch, Table, Tooltip, message } from '@ckfw/components/ui/index.js'
import Icon from '@ckfw/components/icon/Icon.vue'
import { getUserConfig, saveUserConfig, resetUserKey } from '../../api/config'
import { stripToolPrefix } from '../../utils/toolSource'
import { defaultTouchFiles, effectiveTouchFiles, touchFilesOverride } from '../../utils/toolTouchFiles'
import { loadFailedText, saveFailedText } from '../../utils/settingsFeedback'
import mq from '@ckfw/utils/mq.js'
import { MsgClientTopics } from '../../events/msgkeys.js'

const { t } = useI18n()

// usr 配置键（自由键通道）：值是 JSON 对象字符串（persist 侧自由键按字符串原样存取）。
const CFG_KEY = 'tool_async'
// 契约默认异步阈值兜底（spec 72 参考值；契约 `async-threshold` 缺省 = 30）
const FALLBACK_THRESHOLD = 30
// 四档模式（新模型：sync/async/auto/manual；旧值 never→sync / always→async 仅读取兼容）
const MODES = ['sync', 'async', 'auto', 'manual']

function normalizeUiMode(m) {
  const v = String(m || '').toLowerCase()
  if (v === 'never') return 'sync'
  if (v === 'always') return 'async'
  return MODES.includes(v) ? v : 'auto'
}

const loading = ref(false)
const saving = ref(false)
const groups = ref([]) // [{ key, tools: [row] }]
const savedMap = ref({}) // 上次落库态（usr `tool_async`）
const workMap = ref({}) // 本地待保存态（改模式/数值只动它；点【保存】才落库）

const modeOptions = computed(() => [
  { label: t('config.toolAsync.modeSync'), value: 'sync' },
  { label: t('config.toolAsync.modeAsync'), value: 'async' },
  { label: t('config.toolAsync.modeAuto'), value: 'auto' },
  { label: t('config.toolAsync.modeManual'), value: 'manual' },
])

const columns = computed(() => [
  { label: t('config.toolAsync.tool'), prop: 'name', minWidth: 200 },
  { label: t('config.toolAsync.mode'), prop: 'mode', width: 150 },
  { label: t('config.toolAsync.transferThreshold'), prop: 'threshold', width: 140 },
  { label: t('config.toolAsync.hardTimeout'), prop: 'timeout', width: 140 },
  { label: t('config.toolAsync.touchFiles'), prop: 'touch', width: 150 },
  { label: t('config.table.operation'), type: 'action', width: 130, align: 'center' },
])

// 契约现值：tools-list 每项 `_meta`（透出 hot/category/async/async-threshold/timeout/touch_files）
// 键缺失/空/非数字 → ''（未设置，回落）；数字（含 0 / -1）→ 数值。
function metaNum(v) {
  if (v === undefined || v === null || v === '') return ''
  const n = Number(v)
  return Number.isFinite(n) ? n : ''
}

// 「转异步阈值」合法输入：正数 = 秒数；0 / -1 = 不设点（永远等待，用户可取消）；其余 → 非法。
function isLimitValue(v) {
  if (v === undefined || v === null || String(v).trim() === '') return false
  const n = Number(v)
  return Number.isFinite(n) && (n >= 0 || n === -1)
}

// 转异步阈值仅 auto / manual 有意义。
function isThresholdMode(row) {
  return row.mode === 'auto' || row.mode === 'manual'
}

// 配置键：工具配置按当前暴露名精确索引（内置 self 节点 = 原名），不做历史前缀回退。
function cfgKeyOf(name) {
  return String(name || '')
}

// 阈值缺省显示：契约 async-threshold 优先，否则 30（执行硬上限不再参与阈值缺省）。
function defaultThreshold(row) {
  return row.cThreshold !== '' ? row.cThreshold : FALLBACK_THRESHOLD
}

function modeLabel(m) {
  const key = { sync: 'modeSync', async: 'modeAsync', auto: 'modeAuto', manual: 'modeManual' }[normalizeUiMode(m)]
  return key ? t('config.toolAsync.' + key) : m
}

// 「恢复默认」tooltip：说明该工具的契约默认
function contractTooltip(row) {
  return t('config.toolAsync.contractTooltip', {
    mode: modeLabel(row.cMode),
    threshold: row.cThreshold === '' ? '—' : (row.cThreshold + 's'),
    timeout: row.cTimeout === '' ? '—' : (row.cTimeout + 's'),
    touch: row.touchDefault ? t('config.toolAsync.checkpointOn') : t('config.toolAsync.checkpointOff'),
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
  if (normalizeUiMode(e.mode) !== normalizeUiMode(row.cMode)) return false
  if (isThresholdMode(row)) {
    const cur = row.cThreshold === '' ? Number(defaultThreshold(row)) : Number(row.cThreshold)
    const val = e.threshold !== undefined ? Number(e.threshold) : Number(defaultThreshold(row))
    if (!Number.isFinite(cur) || !Number.isFinite(val) || cur !== val) return false
  } else if (e.threshold !== undefined) {
    return false
  }
  // 执行硬上限：仅在 >0 时写入；等于契约 timeout 视为未改动
  if (e.hard_timeout !== undefined) {
    if (row.cTimeout === '' || Number(row.cTimeout) !== e.hard_timeout) return false
  }
  // touch_files 仅在**显式偏离缺省**时才写入 → 出现即视为已配置（不算「等于契约现值」）
  if (e.touch_files !== undefined) return false
  return true
}

// 行 → usr 键项：
//   - mode 恒写；
//   - threshold 仅 auto / manual 计入（正数 / 0 / -1 均合法）；
//   - hard_timeout 仅 **>0** 计入（<=0 / 留空 = 不处理 → 不写）；
//   - touch_files 仅偏离缺省时写入。
function buildEntry(row) {
  const e = { mode: row.mode }
  if (isThresholdMode(row)) {
    const n = Number(row.threshold)
    e.threshold = isLimitValue(row.threshold) ? n : Number(defaultThreshold(row))
  }
  if (row.hardTimeout !== '' && row.hardTimeout !== null) {
    const n = Number(row.hardTimeout)
    if (Number.isFinite(n) && n > 0) e.hard_timeout = n
  }
  // 涉及文件变动：与缺省一致 → 不写（保持配置干净）
  const tf = touchFilesOverride(row.touch, row.name, row.server, row.meta)
  if (tf !== undefined) e.touch_files = tf
  return e
}

// 待保存态 → 行显示值（workMap 优先，其次已落库态，最后契约现值）
function resetRowFromMaps(row) {
  row.cfgKey = cfgKeyOf(row.name) // 配置键 = 当前暴露名
  const u = workMap.value[row.cfgKey] || savedMap.value[row.cfgKey]
  if (u && typeof u === 'object') {
    row.mode = u.mode ? normalizeUiMode(u.mode) : row.cMode
    row.threshold = u.threshold !== undefined && u.threshold !== null ? String(u.threshold) : String(defaultThreshold(row))
    row.hardTimeout = u.hard_timeout !== undefined && u.hard_timeout !== null && Number(u.hard_timeout) > 0
      ? String(u.hard_timeout) : ''
    row.touch = effectiveTouchFiles(u, row.name, row.server, row.meta)
  } else {
    row.mode = row.cMode
    row.threshold = String(defaultThreshold(row))
    row.hardTimeout = ''
    row.touch = row.touchDefault
  }
}

function applyRows() {
  for (const g of groups.value) for (const row of g.tools) resetRowFromMaps(row)
}

// 编辑一行 → 只改本地待保存态（不落库）：与契约一致 → 从待保存态删除该行。
function syncRow(row) {
  const next = { ...workMap.value }
  const e = buildEntry(row)
  if (sameAsContract(row, e)) delete next[row.cfgKey || row.name]
  else next[row.cfgKey || row.name] = e
  workMap.value = next
}

function isUserSet(row) {
  return Object.prototype.hasOwnProperty.call(workMap.value, row.cfgKey || row.name)
}

// 待保存态是否与已落库态不同（无改动 → 保存按钮禁用）
function canon(e) {
  if (!e || typeof e !== 'object') return ''
  return JSON.stringify({ mode: e.mode, threshold: e.threshold,
    hard_timeout: e.hard_timeout, touch_files: e.touch_files })
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
    const env = await mq.emit(MsgClientTopics.toolsList, {})
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
        // 展示用：dir/第三方暴露名的来源前缀剥掉后显示（`row.name` 仍作配置键）
        server: srv,
        // 契约 `_meta`（打点缺省优先读 touch_files 声明）
        meta,
        policyLock: meta.policy_lock === true,
        cMode: normalizeUiMode(meta.async || 'auto'),
        cThreshold: metaNum(meta['async-threshold']),
        cTimeout: metaNum(meta.timeout),
        // 涉及文件变动缺省：契约 `_meta.touch_files` 优先，无声明回落来源启发式
        touchDefault: defaultTouchFiles(tl.name, srv, meta),
        mode: normalizeUiMode(meta.async || 'auto'),
        threshold: '',
        hardTimeout: '',
        // 已落库/待保存值由 applyRows → resetRowFromMaps 覆盖。
        touch: defaultTouchFiles(tl.name, srv, meta),
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
  row.mode = normalizeUiMode(v)
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

// 数值失焦：
//   ① 阈值：非法 → 提示并回落（已落库值 / 缺省）；空 → 缺省显示；0 / -1 合法（不设点）。
//   ② 执行硬上限：>0 → 生效；<=0 / 空 → 清显示（不写库，回落契约/全局）；非法 → 提示并回落。
function onNumberBlur(row, field) {
  if (field === 'threshold') {
    const raw = row.threshold
    const u = savedMap.value[row.cfgKey || row.name]
    if (raw === '' || raw === null || raw === undefined) {
      row.threshold = String(defaultThreshold(row))
    } else if (!isLimitValue(raw)) {
      // INPUTVAL-1 P2-1：非法值不再静默回落（明确提示；回落已落库值 / 默认值）。
      message.warning(t('fieldRules.invalidNumber', {
        field: t('config.toolAsync.transferThreshold'), min: -1, max: '-',
      }))
      row.threshold = u && u.threshold != null ? String(u.threshold) : String(defaultThreshold(row))
    }
    syncRow(row)
    return
  }
  const raw = row.hardTimeout
  const u = savedMap.value[row.cfgKey || row.name]
  if (raw === '' || raw === null || raw === undefined) {
    row.hardTimeout = ''
  } else {
    const n = Number(raw)
    if (Number.isFinite(n) && n > 0) {
      // 合法（生效）
    } else if (Number.isFinite(n)) {
      row.hardTimeout = '' // <=0 = 不处理（不写库）
    } else {
      message.warning(t('fieldRules.invalidNumber', {
        field: t('config.toolAsync.hardTimeout'), min: 1, max: '-',
      }))
      row.hardTimeout = u && u.hard_timeout != null && Number(u.hard_timeout) > 0 ? String(u.hard_timeout) : ''
    }
  }
  syncRow(row)
}

// 恢复默认 = 从待保存态删除该工具键项（点【保存】落库；最后一项 → 整键删除）。
// 行显示明确回落契约现值（不读 savedMap）。
function restoreDefault(row) {
  const next = { ...workMap.value }
  delete next[row.cfgKey || row.name]
  workMap.value = next
  row.mode = row.cMode
  row.threshold = String(defaultThreshold(row))
  row.hardTimeout = ''
  row.touch = row.touchDefault
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
.cell-threshold { width: 110px; }
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
.th-help {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.th-help .b-tooltip {
  color: var(--text-muted);
}
</style>