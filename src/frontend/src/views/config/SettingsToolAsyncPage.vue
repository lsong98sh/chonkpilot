<!--
  工具异步配置页（usr 级；设置菜单 → preview tab kind = settings-tool-async）

  需求（用户口径）：列出所有工具（`tools/list`）按 MCP 分组，逐工具设「仅异步 / 仅同步 /
  自动异步+超时 / 手动异步」（前端在 manual 工具消息上出现「转异步」图标，点击转）。

  读写面（**零新增 MQ 主题**）：
    - 工具清单 = 既有客户端能力面 `tools-list`（与 useToolAsyncMode.js / ScenarioEditDialog 同一消费点）
    - 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（api/config.js 的
      getUserConfig / saveUserConfig / resetUserKey）
  存储形态：usr 键 `tool_async`（自由键通道）= JSON 对象
    {"<工具暴露名>": {"mode":"always|never|auto|manual", "threshold":30, "hard_timeout":300}}
    - 未配置的工具：显示**契约现值**（`_meta.async` / `_meta.async-threshold` / `_meta.timeout`）并标
      「契约默认」，**不写库**；
    - 「恢复默认」= 删除该工具的键项（最后一项删除后整键删除，走 data-user-config-delete{id}）；
    - 未改动不进库（仅交互/失焦时按行写入）。
-->
<template>
  <div class="settings-page">
    <div class="page-body">
      <div class="tool-toolbar">
        <span class="hint">{{ $t('config.toolAsync.pageHint') }}</span>
        <Button size="small" :loading="loading" @click="reload">
          <Icon name="refresh" :size="13" /> {{ $t('config.page.redetect') }}
        </Button>
      </div>

      <div v-if="!groups.length" class="tool-empty">{{ $t('config.toolAsync.empty') }}</div>

      <div v-for="g in groups" :key="g.key" class="tool-group">
        <div class="group-title">{{ g.key }}<span class="group-count">（{{ g.tools.length }}）</span></div>

        <div v-for="row in g.tools" :key="row.name" class="tool-item" :data-tool="row.name">
          <div class="tool-row">
            <div class="tool-name" :title="row.name">
              <span class="mono">{{ stripToolPrefix(row.name, row.server) }}</span>
              <span class="tool-desc">{{ row.description }}</span>
            </div>

            <div class="tool-mode" :title="modeHint(row)">
              <Select
                :model-value="row.mode"
                :options="modeOptions"
                :aria-label="$t('config.toolAsync.mode') + ': ' + row.name"
                @update:model-value="(v) => onModeChange(row, v)"
              />
            </div>

            <!-- 阈值：仅 auto / manual 有意义（条件显示） -->
            <div v-if="isThresholdMode(row)" class="tool-threshold">
              <Input
                v-model="row.threshold"
                :aria-label="$t('config.toolAsync.threshold')"
                :placeholder="row.cThreshold === '' ? $t('config.toolAsync.thresholdPlaceholder') : String(row.cThreshold)"
                @blur="() => onNumberBlur(row, 'threshold')"
              />
            </div>

            <div class="tool-meta">
              <span class="badge" :class="row.userSet ? 'badge-own' : 'badge-sys'">
                {{ row.userSet ? $t('config.toolAsync.sourceUser') : $t('config.toolAsync.sourceContract') }}
              </span>
              <span class="contract">{{ $t('config.toolAsync.contractNow') }}：{{ contractText(row) }}</span>
            </div>

            <div class="tool-actions">
              <Button text :aria-label="$t('config.toolAsync.advanced') + ': ' + row.name" @click="toggleAdvanced(row)">
                {{ advancedOpen(row) ? '▾ ' : '▸ ' }}{{ $t('config.toolAsync.advanced') }}
              </Button>
              <Button text type="danger" :disabled="!row.userSet" @click="restoreDefault(row)">
                {{ $t('config.toolAsync.restoreDefault') }}
              </Button>
            </div>
          </div>

          <!-- 高级（默认折叠）：执行硬上限 hard_timeout -->
          <div v-if="advancedOpen(row)" class="advanced-body">
            <label class="adv-label">{{ $t('config.toolAsync.hardTimeout') }}</label>
            <div class="adv-input">
              <Input
                v-model="row.hardTimeout"
                :disabled="row.dirNode === true"
                :aria-label="$t('config.toolAsync.hardTimeout')"
                :placeholder="row.cTimeout === '' ? $t('config.toolAsync.hardTimeoutPlaceholder') : String(row.cTimeout)"
                @blur="() => onNumberBlur(row, 'hard_timeout')"
              />
            </div>
            <!-- I-109 边界：dir 节点工具 hard_timeout 不适用（mode/阈值仍适用） -->
            <span v-if="row.dirNode === true" class="adv-na" :title="$t('config.toolAsync.hardTimeoutNotApplicable')">
              {{ $t('config.toolAsync.naBadge') }}：{{ $t('config.toolAsync.hardTimeoutNotApplicable') }}
            </span>
            <span v-else class="adv-hint">{{ $t('config.toolAsync.advancedHint') }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input, Button, Select, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { getUserConfig, saveUserConfig, resetUserKey } from '../../api/config'
import { isDirNode, stripToolPrefix } from '../../utils/toolSource'
import { loadFailedText } from '../../utils/settingsFeedback'
import mq from '../../utils/mq'

const { t } = useI18n()

// usr 配置键（自由键通道）：值是 JSON 对象字符串（persist 侧自由键按字符串原样存取）。
const CFG_KEY = 'tool_async'
// 契约默认异步阈值兜底（spec 72 参考值；契约 `async-threshold` 缺省 = 契约 `timeout`）
const FALLBACK_THRESHOLD = 30

const loading = ref(false)
const groups = ref([]) // [{ key, tools: [row] }]
const userMap = ref({}) // 工具名 → { mode, threshold, hard_timeout }
const openSet = ref(new Set()) // 展开高级的工具名（模板中读 ref.value）
let saveSeq = 0

const modeOptions = computed(() => [
  { label: t('config.toolAsync.modeAlways'), value: 'always' },
  { label: t('config.toolAsync.modeNever'), value: 'never' },
  { label: t('config.toolAsync.modeAuto'), value: 'auto' },
  { label: t('config.toolAsync.modeManual'), value: 'manual' },
])

// 契约现值：tools-list 每项 `_meta`（25-mcp-server § 透出 hot/category/async/async-threshold/timeout）
function metaNum(v) {
  const n = Number(v)
  return Number.isFinite(n) && n > 0 ? n : ''
}

function isThresholdMode(row) {
  return row.mode === 'auto' || row.mode === 'manual'
}

// 四档语义说明（行内 hover 提示，文案 = config.toolAsync.desc*）
function modeHint(row) {
  const key = { always: 'descAlways', never: 'descNever', auto: 'descAuto', manual: 'descManual' }[row.mode]
  return key ? t('config.toolAsync.' + key) : ''
}

function advancedOpen(row) {
  return openSet.value.has(row.name)
}

function toggleAdvanced(row) {
  const s = new Set(openSet.value)
  if (s.has(row.name)) s.delete(row.name)
  else s.add(row.name)
  openSet.value = s
}

function contractText(row) {
  const parts = [row.cMode]
  if (row.cThreshold !== '') parts.push(row.cThreshold + 's')
  if (row.cTimeout !== '') parts.push('timeout ' + row.cTimeout + 's')
  return parts.join(' · ')
}

function defaultThreshold(row) {
  return row.cThreshold !== '' ? row.cThreshold : (row.cTimeout !== '' ? row.cTimeout : FALLBACK_THRESHOLD)
}

// 解析 usr `tool_async`：允许对象形态与 JSON 字符串形态（自由键按字符串回读）。
function parseToolAsync(v) {
  let obj = v
  if (typeof obj === 'string') {
    try { obj = JSON.parse(obj) } catch (_) { return {} }
  }
  return obj && typeof obj === 'object' && !Array.isArray(obj) ? obj : {}
}

// 用户配置 → 行生效值（未配置的行保持契约现值，仅标「契约默认」）
function applyUserConfig() {
  for (const g of groups.value) {
    for (const row of g.tools) {
      const u = userMap.value[row.name]
      if (u && typeof u === 'object') {
        row.userSet = true
        row.mode = ['always', 'never', 'auto', 'manual'].includes(u.mode) ? u.mode : row.cMode
        row.threshold = u.threshold !== undefined && u.threshold !== null ? String(u.threshold) : defaultThreshold(row)
        row.hardTimeout = u.hard_timeout !== undefined && u.hard_timeout !== null ? String(u.hard_timeout) : ''
      } else {
        row.userSet = false
        row.mode = row.cMode
        row.threshold = String(defaultThreshold(row))
        row.hardTimeout = ''
      }
    }
  }
}

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
        userSet: false,
      })
    }
    groups.value = [...byServer.entries()].map(([key, list]) => ({ key, tools: list }))
    applyUserConfig()
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
    userMap.value = parseToolAsync(uc[CFG_KEY])
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console；与 loadTools 用不同 item，避免两条同文案刷屏）
    console.warn('[SettingsToolAsync] load user config failed:', e)
    message.error(loadFailedText(t, t('config.toolAsync.sourceUser'), e))
    userMap.value = {}
  }
  applyUserConfig()
}

async function reload() {
  await loadUserConfig()
  await loadTools()
}

// 未改动不进库：仅把「发生了变化的行」写回 usr `tool_async`。
// 注意 saveUserConfig 是**增量合并**（persist_userconfig.go saveUserConfig），只写本键不影响其它配置。
async function persist(okTip) {
  const seq = ++saveSeq
  try {
    await saveUserConfig({ [CFG_KEY]: userMap.value })
    if (seq === saveSeq && okTip) message.success(okTip)
    return true
  } catch (e) {
    if (seq === saveSeq) message.error(t('config.save_failed') + ': ' + (e.message || ''))
    return false
  }
}

function userEntry(row) {
  const e = { mode: row.mode }
  if (isThresholdMode(row)) {
    const n = Number(row.threshold)
    e.threshold = Number.isFinite(n) && n > 0 ? n : defaultThreshold(row)
  }
  // I-109：dir 节点工具的 hard_timeout 不生效 → 不写库（避免「配了不生效」的误导）
  if (!row.dirNode && row.hardTimeout !== '' && row.hardTimeout !== null) {
    const h = Number(row.hardTimeout)
    if (Number.isFinite(h) && h > 0) e.hard_timeout = h
  }
  return e
}

async function onModeChange(row, v) {
  if (!v || v === row.mode) return
  row.mode = v
  row.userSet = true
  if (isThresholdMode(row) && !row.threshold) row.threshold = String(defaultThreshold(row))
  userMap.value = { ...userMap.value, [row.name]: userEntry(row) }
  await persist(t('config.toolAsync.saved'))
}

// 生效项是否等于契约现值（等于 → 视为未改动，不进库）
function sameAsContract(row, e) {
  if (e.mode !== row.cMode) return false
  const cur = row.cThreshold === '' ? '' : String(row.cThreshold)
  const t0 = e.threshold === undefined ? '' : String(e.threshold)
  if (t0 !== cur) return false
  return e.hard_timeout === undefined
}

// 数值项失焦：① 非法/空（threshold）→ 不写库、显示值回落当前有效值；
// ② hard_timeout 清空 = 不设硬上限（继承契约）；③ 与已存项/契约现值一致 → 不进库。
async function onNumberBlur(row, field) {
  // I-109：dir 节点工具的 hard_timeout 不适用（输入已禁用，此处兜底）
  if (field === 'hard_timeout' && row.dirNode === true) return
  const raw = field === 'threshold' ? row.threshold : row.hardTimeout
  const n = Number(raw)
  const legal = raw !== '' && raw !== null && Number.isFinite(n) && n > 0
  const emptyHard = field === 'hard_timeout' && (raw === '' || raw === null)
  if (!legal && !emptyHard) {
    const u = userMap.value[row.name]
    if (field === 'threshold') {
      row.threshold = u && u.threshold != null ? String(u.threshold) : String(defaultThreshold(row))
    } else {
      row.hardTimeout = u && u.hard_timeout != null ? String(u.hard_timeout) : ''
    }
    return
  }
  const nextEntry = userEntry(row)
  const curEntry = userMap.value[row.name]
  if (curEntry && JSON.stringify(curEntry) === JSON.stringify(nextEntry)) return
  if (!curEntry && sameAsContract(row, nextEntry)) return
  row.userSet = true
  userMap.value = { ...userMap.value, [row.name]: nextEntry }
  await persist(t('config.toolAsync.saved'))
}

// 恢复默认 = 删除该工具的键项；最后一项删除后整键删除（data-user-config-delete{id:key}），
// 不留下空对象配置。
async function restoreDefault(row) {
  const next = { ...userMap.value }
  delete next[row.name]
  userMap.value = next
  try {
    if (Object.keys(next).length === 0) await resetUserKey(CFG_KEY)
    else await persist()
    row.userSet = false
    row.mode = row.cMode
    row.threshold = String(defaultThreshold(row))
    row.hardTimeout = ''
    message.success(t('config.toolAsync.restored'))
  } catch (e) {
    message.error(t('config.save_failed') + ': ' + (e.message || ''))
  }
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
  overflow-y: auto;
  padding: 8px 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.tool-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}
.hint {
  font-size: 12px;
  color: var(--text-muted);
  flex: 1;
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
}
.group-title {
  font-size: 12px;
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
.tool-item + .tool-item {
  border-top: 1px dashed var(--border);
}
.tool-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
}
.tool-name {
  flex: 1;
  min-width: 0;
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
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tool-mode {
  width: 130px;
  flex-shrink: 0;
}
.tool-threshold {
  width: 90px;
  flex-shrink: 0;
}
.tool-meta {
  width: 240px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.badge {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  align-self: flex-start;
}
.badge-own {
  background: var(--accent, #409eff);
  color: #fff;
}
.badge-sys {
  background: var(--bg-hover, #eee);
  color: var(--text-muted);
}
.contract {
  font-size: 11px;
  color: var(--text-muted);
}
.tool-actions {
  width: 170px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 4px;
  justify-content: flex-end;
}
.advanced-body {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0 8px 12px;
}
.adv-label {
  font-size: 12px;
  color: var(--text-secondary);
  width: 110px;
  flex-shrink: 0;
}
.adv-input {
  width: 90px;
  flex-shrink: 0;
}
.adv-hint {
  font-size: 11px;
  color: var(--text-muted);
}
.adv-na {
  font-size: 11px;
  color: var(--warning, #e6a23c);
}
</style>
