<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">{{ $t('projectConfig.context') }}</span>
      <div class="tab-actions">
        <!-- ⑤ dirty 标记 + 保存中禁用重复提交（保存按钮仍为唯一提交入口，时机不变） -->
        <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" type="primary" :disabled="!dirty" :loading="saving" v-mq:[EventNames.contextSave].click>{{ $t('projectConfig.save') }}</Button>
      </div>
    </div>
    <form class="form-layout">
      <!-- 记忆沉淀配置已迁移（MEMORY-UI-1，2026-11-02）：见 memory 插件配置页
           「配置 → 插件维护 → 记忆沉淀 → 配置」。本页只保留上下文压缩配置。 -->
      <p class="form-description">{{ $t('projectConfig.context_memory_moved') }}</p>

      <!-- 上下文压缩配置 -->
      <section class="cfg-section">
        <h3 class="section-title">{{ $t('projectConfig.compress_section') }}</h3>
        <!-- 保留完整对话 = **本轮恒保留**（下限），更早的轮「累计完整态 token > M」或「已纳入轮数 >= N」
             的那一轮起全部进入**简化区**（口径 V/W；两条件均 0 = 不压缩；简化区 token ≥ T 才生成摘要） -->
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.keep_full_max_turns') }}</label>
          <Input v-model.number="keepFullMaxTurns" type="number" :min="0" :max="50" :step="1" @update:model-value="refreshDirty" />
          <!-- 口径 W：<0 = 非法（显式提示，不静默）；0 = 该条件不启用 -->
          <div class="field-hint" :class="{ 'hint-error': keepFullMaxTurnsInvalid }">
            {{ keepFullMaxTurnsInvalid ? $t('projectConfig.keep_full_max_turns_invalid') : $t('projectConfig.keep_full_max_turns_hint') }}
          </div>
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.keep_full_max_tokens') }}</label>
          <Input v-model.number="keepFullMaxTokens" type="number" :min="0" :max="1000000" :step="1000" @update:model-value="refreshDirty" />
          <!-- 口径 W：<0 = 非法（显式提示，不静默）；0 = 该条件不启用 -->
          <div class="field-hint" :class="{ 'hint-error': keepFullMaxTokensInvalid }">
            {{ keepFullMaxTokensInvalid ? $t('projectConfig.keep_full_max_tokens_invalid') : $t('projectConfig.keep_full_max_tokens_hint') }}
          </div>
        </div>
        <div class="form-item form-item-12">
          <label class="form-label">{{ $t('projectConfig.compress_threshold') }}</label>
          <Input v-model.number="compressTokenThreshold" type="number" :min="10000" :max="500000" :step="10000" @update:model-value="refreshDirty" />
          <div class="quick-thresholds">
            <Button
              v-for="b in QUICK_THRESHOLDS"
              :key="b.label"
              size="small"
              class="quick-threshold-btn"
              v-mq:[EventNames.contextQuickThreshold].click="{ value: b.value }"
            >{{ b.label }}</Button>
          </div>
        </div>
        <!-- 开关：压缩子会话（DSL-4，42 §2 (253)：默认关；关 = 子会话 turn 跳过压缩） -->
        <div class="form-item form-item-12">
          <div class="switch-row">
            <label class="form-label">{{ $t('projectConfig.compress_subsession') }}</label>
            <Switch :model-value="compressSubsession" @update:model-value="onCompressSubsessionChange" />
          </div>
          <div class="field-hint">{{ $t('projectConfig.compress_subsession_hint') }}</div>
        </div>
        <div class="form-item form-item-full">
          <div class="prompt-editor-header">
            <label class="form-label">{{ $t('projectConfig.summary_prompt_edit') }}</label>
            <!-- 编辑 = 弹框（A2：弹框内编辑 + 保存即关 + 弹框自带优化）；按钮样式与记忆分类行一致（text） -->
            <Button size="small" text @click="openSummaryEditor">{{ $t('common.edit') }}</Button>
          </div>
          <!-- 来源标注 + 取消覆盖入口（只读展示已移除 → 内容仅在编辑弹框内查看） -->
          <div class="switch-row summary-prompt-source">
            <span class="field-hint">{{ summaryOverride ? $t('projectConfig.summary_prompt_source_override') : $t('projectConfig.summary_prompt_source_inherit') }}</span>
            <Button size="small" text :disabled="!summaryOverride" @click="resetSummaryOverride">{{ $t('projectConfig.summary_prompt_reset') }}</Button>
          </div>
        </div>
        <!-- 压缩内容可查（数据来源 = 当前会话快照：data-snapshot-get，压缩产物唯一落点） -->
        <div class="form-item form-item-full">
          <div class="prompt-editor-header">
            <label class="form-label">{{ $t('memoryIO.records_title') }}</label>
            <div class="tab-actions">
              <Button size="small" :loading="recordsLoading" @click="loadCompressRecords">{{ $t('memoryIO.records_refresh') }}</Button>
            </div>
          </div>
          <div class="field-hint">{{ $t('memoryIO.records_note') }}</div>
          <ul class="compress-records" v-if="compressRecords.length > 0">
            <li v-for="(r, i) in compressRecords" :key="i" class="compress-record">
              <div class="record-meta">
                <span>{{ $t('memoryIO.records_range_value', { turn: r.turn }) }}</span>
                <span class="record-kept">{{ $t('memoryIO.records_kept', { count: r.kept }) }}</span>
                <Button size="small" text @click="toggleRecord(i)">{{ recordExpanded(i) ? $t('memoryIO.records_collapse') : $t('memoryIO.records_expand') }}</Button>
              </div>
              <pre class="record-text">{{ recordExpanded(i) ? r.text : brief(r.text) }}</pre>
            </li>
          </ul>
          <div v-else class="field-hint records-empty">{{ recordsEmptyText }}</div>
        </div>
        <p class="form-description">
          {{ $t('projectConfig.context_desc') }}<br>
          {{ $t('projectConfig.context_calc') }}<br>
          {{ $t('projectConfig.context_example') }}<br>
          {{ $t('projectConfig.context_async') }}
        </p>
        <!-- 压缩区末尾提示（2026-09-24 用户口径，zh-CN 逐字）：纯展示，无功能变更 -->
        <p class="form-description">
          {{ $t('projectConfig.context_tip_cache_billing') }}<br>
          {{ $t('projectConfig.context_tip_compress_llm') }}
        </p>
      </section>
    </form>
  </div>
</template>

<script setup>
import { ref, computed, h, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input, Button, Switch, message } from '@ckfw/components/ui/index.js'
import { dialog } from '@ckfw/components/dialog/index.js'
import TextEditDialog from '../../components/common/TextEditDialog.vue'
import { getAllConfig, setConfigs, getPrompt, setPrompt } from '../../api/config'
import { readPrimitive } from '../../api/knowledge'
import { getActiveSessionID } from '../../api/session'
import dataClient, { dataRequest } from '../../utils/dataClient'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../../events/event-names'
import { saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'
import { usePrjConfigRefresh } from '../../composables/usePrjConfigRefresh'

const { t } = useI18n()

// ⑤ 手动保存：dirty 由「本地态 JSON 快照 vs 已保存态快照」比对得出（无改动 → 【保存】禁用）；
// 数值项 / 开关只改本地态，点【保存】才落库（弹窗编辑仍即时落库，见下）。
const { dirty, markDirty, markSaved } = useUnsavedMark()
const saving = ref(false)

// 快速阈值按钮（FP L212）：64K/128K/256K/512K/1M
const QUICK_THRESHOLDS = [
  { label: '64K', value: 64 * 1024 },
  { label: '128K', value: 128 * 1024 },
  { label: '256K', value: 256 * 1024 },
  { label: '512K', value: 512 * 1024 },
  { label: '1M', value: 1024 * 1024 },
]

// 系统默认（与 compress 插件 DefaultOptions 一致：
// keep_full_max_turns=10 / keep_full_max_tokens=24000 / compress_token_threshold=20000）
const keepFullMaxTurns = ref(10)
const keepFullMaxTokens = ref(24000)
const compressTokenThreshold = ref(20000)
// 子会话治理开关（DSL-4，42 §2 (253)，默认关闭）：
//   - compress.subsession「压缩子会话」：关 = 子会话 turn 跳过压缩。
// 落库走既有 setConfigs（prj 键），保存即生效（后端插件按 session.parent_id 分流）。
const compressSubsession = ref(false)
// 口径 W（2026-09-25）：`< 0` = 非法值（前端显式提示，不静默；后端按「不启用」处理并留日志）；
// `0` = 该条件不启用（只用另一条件）；**两者均为 0 = 不压缩**（保留全量、不生成摘要）。
const keepFullMaxTurnsInvalid = computed(() => Number(keepFullMaxTurns.value) < 0)
const keepFullMaxTokensInvalid = computed(() => Number(keepFullMaxTokens.value) < 0)
const hasIllegalBounds = computed(() => keepFullMaxTurnsInvalid.value || keepFullMaxTokensInvalid.value)

// warnIllegalBounds 保存/落库前提示非法值（不阻断保存：后端按不启用处理，但用户必须知情）。
function warnIllegalBounds() {
  if (hasIllegalBounds.value) {
    message.warning(t('projectConfig.keep_full_max_illegal_warning'))
  }
}
const summarizePrompt = ref('')
// 总结提示词来源：true = 项目级覆盖（capability/system/summary.md 存在），
// false = 继承（后端 load 回落链：项目文件 → 用户文件 → 系统文件 → 旧 prj config → embed 内置）。
const summaryOverride = ref(false)
// 加载时的有效值快照：页保存按钮据「未做覆盖且内容未改」提示"未做覆盖（沿用继承值）"。
const summaryLoadedValue = ref('')
const unsubs = []

// ── 压缩内容可查（data-snapshot-get）──
// 压缩回写快照的摘要前缀（唯一来源 = `chonkpilot-agent/session` 的 `SummaryPrefix`，
// 经 `src/lib/llm/server/compress.go` 使用）——压缩产物唯一落点 = 会话快照（system 消息），
// 前端据此识别「压缩记录」。
const COMPRESS_MARK = '[已压缩早前对话] '
// COMPRESS_EVENT = 压缩产物（会话快照）可能变化的时机。压缩本身是**内部方法面**
// （2026-10-31 起 `session-compress` 已从消息面移除），故改用每轮终态事件 `session-complete`
// 重读压缩记录（压缩写回快照发生在轮末之后，下一轮终态时必然可见）。
const COMPRESS_EVENT = EventNames.sessionComplete
const RECORD_BRIEF_CHARS = 200
const compressRecords = ref([])
const recordsLoading = ref(false)
const expandedRecords = ref({})
// 无活动会话（未取到 session_id）→ 空态给专门文案（不谎称「无压缩记录」）
const hasActiveSession = ref(false)

// ── 手动保存的 dirty 判定（无 watch：由各变更点显式调用 refreshDirty 重算）──
// 仅将「点【保存】才落库」的项纳入快照：压缩三项数值 + 压缩子会话开关；
// 弹窗编辑（总结提示词）即时落库，不进快照。
let savedState = ''
function currentState() {
  return JSON.stringify({
    turns: Number(keepFullMaxTurns.value) || 0,
    tokens: Number(keepFullMaxTokens.value) || 0,
    threshold: Number(compressTokenThreshold.value) || 0,
    compSub: !!compressSubsession.value,
  })
}
// refreshDirty 重算 dirty：与已保存态一致 → 清除标记；否则标记「未保存」。
function refreshDirty() {
  if (currentState() === savedState) markSaved()
  else markDirty()
}

// readNum 读数字项：键存在且可解析（含 0 / 负数 → 该项「不启用」）→ 采用；缺失/非法 → 回落默认。
function readNum(map, key, fallback) {
  const raw = map[key]
  if (raw === undefined || raw === null || raw === '') return fallback
  const n = parseInt(raw)
  return Number.isFinite(n) ? n : fallback
}

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    // 保留完整对话：**本轮恒保留**（下限），更早的轮超 N 轮或累计完整态 token 超 M 的那一轮起
    // 进入**简化区**（`keep_full_max_turns`、`keep_full_max_tokens`）；0 = 该条件不启用、
    // 两者均 0 = 不压缩（口径 V/W）；简化区摘要阈值 `compress_token_threshold`（作用域 = 简化区）。
    keepFullMaxTurns.value = readNum(c, 'keep_full_max_turns', 10)
    keepFullMaxTokens.value = readNum(c, 'keep_full_max_tokens', 24000)
    compressTokenThreshold.value = readNum(c, 'compress_token_threshold', 20000)
    // 子会话治理开关（prj 键；缺失/非 'true' → 关闭）
    compressSubsession.value = c['compress.subsession'] === 'true'
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('projectConfig.context'), e))
  }
  await loadSummaryPrompt()
  // 载入完成 = 已保存态：重置快照并清除 dirty（无改动 → 【保存】禁用）
  savedState = currentState()
  markSaved()
}

// SUMMARY_PROMPT_PATH 项目级总结提示词文件（相对知识库根 → 归属项目级；
// 与 persist 侧 summaryPromptFile 同路径：<workdir>/.chonkpilot/capability/system/summary.md）。
const SUMMARY_PROMPT_PATH = 'system/summary.md'

// loadSummaryPrompt 读总结提示词（有效值 + 来源）：有效值走 data-prompt-load（后端回落链）；
// 来源经知识库读项目级文件判定——读得到 = 项目级覆盖，读不到 = 继承系统级/内置默认。
async function loadSummaryPrompt() {
  try {
    const sp = await getPrompt('summary_prompt')
    summarizePrompt.value = sp.value || ''
    summaryLoadedValue.value = summarizePrompt.value
  } catch (e) {
    message.error(loadFailedText(t, t('projectConfig.summary_prompt_edit'), e))
  }
  try {
    await readPrimitive(SUMMARY_PROMPT_PATH)
    summaryOverride.value = true
  } catch (e) {
    summaryOverride.value = false // 项目级文件不存在（或读取失败）→ 继承
  }
}

// resetSummaryOverride 取消项目级覆盖：删除项目级文件（data-prompt-delete 既有面）→ 回落继承值。
// 属「即时落库」动作（不由页面【保存】提交）→ 只重算 dirty，不清除页面未保存改动。
async function resetSummaryOverride() {
  try {
    await dataClient.remove('prompt', 'summary_prompt')
    await loadSummaryPrompt()
    refreshDirty()
    message.success(t('projectConfig.summary_prompt_reset_done'))
  } catch (e) {
    message.error(t('projectConfig.prompt_save_failed') + ': ' + (e.message || ''))
  }
}

// ── 本地态变更（不落库；点【保存】才提交）：各变更点只改本地值 + 重算 dirty ──

// 子会话治理开关（DSL-4）：只改本地态，点【保存】才落库（走既有 setConfigs）。
function onCompressSubsessionChange(v) {
  compressSubsession.value = v
  refreshDirty()
}

// 压缩记录空态文案：无活动会话（未取到 session）与「有会话但无压缩」区分开，不谎报。
const recordsEmptyText = computed(() => (
  hasActiveSession.value ? t('memoryIO.records_empty') : t('memoryIO.records_no_session')
))

// 摘要截断（长文折叠）：超长只展示前 N 字符 + 省略号，展开看全文（与大文本既有呈现同风格）。
function brief(text) {
  const s = String(text || '')
  return s.length > RECORD_BRIEF_CHARS ? s.slice(0, RECORD_BRIEF_CHARS) + '…' : s
}

function recordExpanded(i) {
  return expandedRecords.value[i] === true
}

function toggleRecord(i) {
  expandedRecords.value = { ...expandedRecords.value, [i]: !recordExpanded(i) }
}

// loadCompressRecords 读「压缩了什么」：数据来源 = **当前活动会话的会话快照**
// （data-snapshot-get 既有面；压缩产物唯一落点 = sessions 表 history/snapshot_turn）。
// 记录 = 快照 history 中带压缩标记的 system 消息（摘要原文）+ 快照轮次（触发范围）
// + 保留段条数；快照**不存压缩时间** → 不展示时间（不臆造）。
async function loadCompressRecords() {
  recordsLoading.value = true
  try {
    let sessionID = ''
    try {
      const r = await getActiveSessionID()
      sessionID = (r && r.session_id) || ''
    } catch (e) {
      hasActiveSession.value = false
      compressRecords.value = []
      message.error(t('memoryIO.records_load_failed', { error: e.message || String(e) }))
      return
    }
    hasActiveSession.value = !!sessionID
    if (!sessionID) {
      compressRecords.value = []
      return
    }
    const res = await dataRequest('snapshot', 'get', { data: { session_id: sessionID } })
    const snap = (res && res.snapshot) || null
    const history = (snap && Array.isArray(snap.history)) ? snap.history : []
    const out = []
    for (const m of history) {
      const content = m && typeof m.content === 'string' ? m.content : ''
      if (m && m.role === 'system' && content.startsWith(COMPRESS_MARK)) {
        out.push({
          text: content.slice(COMPRESS_MARK.length),
          turn: (snap && snap.snapshot_turn) || '',
          // 保留段 = 快照中压缩摘要之外的消息条数（压缩后快照 = 摘要 + 保留段）
          kept: Math.max(history.length - 1, 0),
        })
      }
    }
    compressRecords.value = out
    expandedRecords.value = {}
  } catch (e) {
    compressRecords.value = []
    message.error(t('memoryIO.records_load_failed', { error: e.message || String(e) }))
  } finally {
    recordsLoading.value = false
  }
}

// 保存按钮：提交本页全部设置（压缩 + 总结提示词）；保存中禁用重复提交
async function handleSave() {
  if (saving.value) return
  saving.value = true
  try {
    warnIllegalBounds()
    // 本页全部 prj 键（压缩三项 + 子会话开关）**一次批量写** → 后端整批一次
    // 广播 data-prj-config-refresh（61 §3.1），不再逐键 N 条。
    const entries = {
      keep_full_max_turns: String(keepFullMaxTurns.value),
      keep_full_max_tokens: String(keepFullMaxTokens.value),
      compress_token_threshold: String(compressTokenThreshold.value),
      // 子会话治理开关（DSL-4，42 §2 (253)；prj 键，默认关闭）——与压缩同域，随本页一次批量写。
      'compress.subsession': String(compressSubsession.value),
    }
    await setConfigs(entries)
    // 总结提示词：内容与"继承值"相同（未覆盖 + 未改动）→ 后端不写项目级文件（保持继承），
    // 只提示"未做覆盖"，避免把有效值固化成项目级副本、永久遮蔽系统级后续更新。
    // 属 **prompt 域**（非 prj-config）→ 单独一次调用（不同域，各自广播）。
    const inheritKept = !summaryOverride.value && summarizePrompt.value === summaryLoadedValue.value
    await setPrompt('summary_prompt', summarizePrompt.value)
    await loadSummaryPrompt()
    // 保存成功 → 以当前本地态为新「已保存态」并清除 dirty（无改动 → 【保存】禁用）
    savedState = currentState()
    markSaved()
    message.success(inheritKept ? t('projectConfig.summary_prompt_inherit_kept') : t('projectConfig.saved'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 总结提示词编辑弹框（A2：弹框内编辑 + 保存即关 + 弹框自带优化；优化前快照在弹框内可还原）
function openSummaryEditor() {
  const handle = dialog.show(h(TextEditDialog, {
    content: summarizePrompt.value,
    placeholder: t('projectConfig.summary_prompt_placeholder'),
    variables: true,
    optimize: {
      title: t('projectConfig.summary_prompt_optimize_title'),
      useCase: t('projectConfig.summary_prompt_optimize_use_case'),
      recover: true, // 弹框内提供「恢复优化」（优化前内容回填，仍需点保存才落库）
    },
    onSave: async (text) => {
      // 弹窗编辑 = 即时落库（用户口径「弹出编辑的，直接保存」）→ 不走页面级 dirty；
      // 与继承值相同（未覆盖 + 未改动）→ 后端不写项目级文件（保持继承）。
      const inheritKept = !summaryOverride.value && text === summaryLoadedValue.value
      await setPrompt('summary_prompt', text)
      await loadSummaryPrompt() // 刷新有效值与来源标注（优化/覆盖后为项目级覆盖）
      refreshDirty() // 只重算 dirty（不清除页面其它未保存改动）
      message.success(inheritKept ? t('projectConfig.summary_prompt_inherit_kept') : t('projectConfig.saved'))
      handle.close()
    },
    // 弹框「取消」：TextEditDialog emit('cancel') → 关闭弹框（不落库）
    onCancel: () => handle.close(),
  }), {
    title: t('projectConfig.summary_prompt_edit'),
    width: 760,
    height: 620,
    bodyClass: 'text-edit-dialog-body',
    closable: true,
  })
}

// 快速阈值按钮：只改本地值（点【保存】才落库）
function handleQuickThreshold({ value }) {
  compressTokenThreshold.value = value
  refreshDirty()
}

onMounted(() => {
  loadConfig()
  // 压缩记录：进页读一次当前会话快照（空态亦展示）
  loadCompressRecords()
  // data-prj-config-refresh：配置变更后 server 广播，自动重载（20-gui；统一机制 usePrjConfigRefresh，I-138）。
  // handleSave 一次批量写（setConfigs）→ 后端整批广播 1 条（含 ids 全组键）；统一机制**按键过滤**
  // （仅本页关注键）+ **合并突发广播为 1 次重载**（读最终快照）+ **保存期间（saving）跳过**，避免
  // 早到的广播让 loadConfig 读到「尚含旧值」的中间快照，把本地**未提交**的数值冲回旧值。
  unsubs.push(usePrjConfigRefresh({
    keys: ['keep_full_max_turns', 'keep_full_max_tokens',
      'compress_token_threshold', 'compress.subsession'],
    reload: loadConfig,
    isSaving: () => saving.value,
  }))
  // session-complete（轮末终态）：压缩写回快照后据此重读压缩记录（压缩为内部方法面，
  // 2026-10-31 起无独立总线事件）。
  unsubs.push(mq.on(COMPRESS_EVENT, loadCompressRecords))
  unsubs.push(mq.on(EventNames.contextSave, handleSave))
  unsubs.push(mq.on(EventNames.contextQuickThreshold, handleQuickThreshold))
})

onUnmounted(() => {
  unsubs.forEach(fn => fn())
})
</script>

<style scoped>
.list-container {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
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
/* 长表单正文整体滚一次（2026-09-27 用户口径）：正文区承担两轴滚动、头部工具条固定不滚；
   其中「压缩内容」记录列表（.compress-records）自身内滚，不受此影响。 */
.form-layout {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
}
/* 记忆类别表不再自建横向滚动容器（与工具异步页同范式）→ 溢出交给 .form-layout */
.form-layout :deep(.b-table-wrapper) {
  overflow-x: visible;
}
.cfg-section {
  width: 100%;
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--border, #dee2e6);
}
.cfg-section:last-child {
  border-bottom: none;
}
.section-title {
  width: 100%;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}
.form-item {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  width: 100%;
}
.form-item-12 {
  width: calc(50% - 6px);
}
.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
.form-description {
  width: 100%;
  font-size: 12px;
  line-height: 1.7;
  color: var(--fg-secondary);
}
.field-hint {
  font-size: 12px;
  color: var(--fg-secondary);
  line-height: 1.6;
}
/* 非法值提示（口径 W：<0 = 非法，显式标红、不静默） */
.field-hint.hint-error {
  color: var(--danger, #f56c6c);
}
.switch-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
/* 总结提示词：来源标注（左）+「恢复默认」（右）同行分列 */
.summary-prompt-source {
  justify-content: space-between;
}
.quick-thresholds {
  display: flex;
  gap: 6px;
  margin-top: 4px;
}
.quick-threshold-btn {
  min-width: 48px;
}
.prompt-editor-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
/* 压缩记录（压缩内容可查）：列表 + 截断/展开全文；列表自身内滚（固定高度上限） */
.compress-records {
  list-style: none;
  margin: 4px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 320px;
  overflow-y: auto;
}
.compress-record {
  border: 1px solid var(--border, #dee2e6);
  border-radius: 4px;
  padding: 6px 8px;
}
.record-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-secondary);
}
.record-kept {
  color: var(--text-muted);
}
.record-text {
  margin: 4px 0 0;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-primary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 260px;
  overflow: auto;
}
.records-empty {
  padding: 4px 0;
}
</style>
