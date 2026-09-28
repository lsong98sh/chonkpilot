<template>
  <div class="history-root">
    <div class="history-tab-content">
      <form class="form-layout">
        <!-- ① 启用开关 -->
        <div class="form-item form-item-full">
          <div class="history-toggle">
            <label class="form-label">{{ $t('historyConfig.enable') }}</label>
            <Switch v-model="historyEnabled" :disabled="!gitAvailable" @update:model-value="handleChange" />
            <!-- ⑤ dirty 标记（仅显示） -->
            <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
          </div>
          <div v-if="!gitAvailable" class="history-reminder history-warn">{{ $t(noGitHint) }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_default_off') }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_checkpoint') }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_perf') }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_retention') }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_no_commit') }}</div>
        </div>

        <hr class="b-divider" />

        <!-- ② 保留策略（保留个数 / 保留天数） -->
        <div class="form-item form-item-full">
          <label class="form-label">{{ $t('historyConfig.retention_label') }}</label>
          <div class="history-num-row">
            <div class="history-num">
              <span class="history-num-label">{{ $t('historyConfig.keep_label') }}</span>
              <Input
                v-model="checkpointKeep"
                type="number"
                class="history-num-input"
                :error="!!keepError"
                @update:model-value="markDirty"
              />
            </div>
            <div class="history-num">
              <span class="history-num-label">{{ $t('historyConfig.ttl_label') }}</span>
              <Input
                v-model="checkpointTtlDays"
                type="number"
                class="history-num-input"
                :error="!!ttlError"
                @update:model-value="markDirty"
              />
            </div>
          </div>
          <div class="history-reminder">{{ $t('historyConfig.retention_hint') }}</div>
          <div v-if="keepError" class="history-error">{{ keepError }}</div>
          <div v-if="ttlError" class="history-error">{{ ttlError }}</div>
          <div class="history-actions">
            <Button size="small" type="primary" :loading="saving" @click="handleRetentionSave">
              {{ $t('projectConfig.save') }}
            </Button>
          </div>
        </div>

        <hr class="b-divider" />

        <!-- ③ 检查点时间轴（只读） -->
        <div class="form-item form-item-full">
          <label class="form-label">{{ $t('historyConfig.timeline_title') }}</label>

          <!-- 顶部状态条（history.status） -->
          <div class="tl-status" data-history-status>
            <span class="tl-status-item">
              <span class="tl-status-key">{{ $t('historyConfig.status_mode') }}</span>
              <span class="tl-status-val" :class="modeClass">{{ modeText }}</span>
            </span>
            <span class="tl-status-item">
              <span class="tl-status-key">{{ $t('historyConfig.status_count') }}</span>
              <span class="tl-status-val">{{ statusCount }}</span>
            </span>
            <span class="tl-status-item">
              <span class="tl-status-key">{{ $t('historyConfig.status_bytes') }}</span>
              <span class="tl-status-val">{{ bytesText }}</span>
            </span>
            <span class="tl-status-item">
              <span class="tl-status-key">{{ $t('historyConfig.status_last') }}</span>
              <span class="tl-status-val">{{ lastTimeText }}</span>
            </span>
            <span class="tl-status-item">
              <span class="tl-status-key">{{ $t('historyConfig.status_last_duration') }}</span>
              <span class="tl-status-val">{{ lastDurationText }}</span>
            </span>
            <span class="tl-status-item">
              <span class="tl-status-key">{{ $t('historyConfig.status_fail') }}</span>
              <span class="tl-status-val">{{ failCount }}</span>
            </span>
            <span v-if="statusDirty" class="tl-status-item tl-status-dirty">
              {{ $t('historyConfig.status_dirty') }}
            </span>
          </div>
          <div v-if="lastError" class="tl-status-error">
            {{ $t('historyConfig.status_last_error') }}：{{ lastError }}
          </div>

          <!-- 列表（history.timeline，最新在前，-1 为最新一步） -->
          <Table
            :columns="timelineColumns"
            :data="timeline"
            :empty-text="timelineEmptyText"
            data-history-timeline
          >
            <template #n="{ index }">
              <span class="tl-n">{{ relativeNumber(index) }}</span>
            </template>
            <template #ts="{ row }">
              <span>{{ formatTime(row.ts) || '—' }}</span>
            </template>
            <template #delta="{ row }">
              <span class="tl-add">+{{ row.added || 0 }}</span>
              <span class="tl-del">−{{ row.removed || 0 }}</span>
            </template>
          </Table>

          <div class="history-actions">
            <Button
              size="small"
              type="danger"
              data-history-clear
              :disabled="clearing || timeline.length === 0"
              @click="handleClear"
            >
              {{ $t('historyConfig.clear') }}
            </Button>
          </div>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Switch, Input, Button, Table } from '../../components/ui'
import { message, confirm } from '../../components/ui'
import { getAllConfig, setConfig, getVCSInfo } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { validatePositiveInt, positiveIntErrorText } from '../../utils/settingsValidation'
import { useUnsavedMark } from '../../composables/useUnsavedMark'
import {
  DEFAULT_CHECKPOINT_KEEP, DEFAULT_CHECKPOINT_TTL_DAYS,
  parseStatus, parseTimeline, relativeNumber, formatBytes, formatTime,
  statusMode, statusEnabled, retentionValue,
} from '../../utils/historyTimeline'

const { t } = useI18n()

// ⑤ dirty 可视标记（仅显示，不改开启即保存的时机）
const { dirty, markDirty, markSaved } = useUnsavedMark()

const unsubs = []

// 默认不开启（与后端口径一致：history.enabled 缺失/非法 = 关闭，见 42 §2 (125)）
const historyEnabled = ref(false)
// 无 git 时该选项 disable（CFG-011-S03：history 插件纯 git）
// 真实门槛两项：系统可执行 git（插件 exec.LookPath）+ work-dir 为 git 仓库（提交前 os.Stat .git）
const gitAvailable = ref(true)
// 不可用原因：no_git_installed（缺 git 可执行）/ no_git_repo（非 git 仓库）
const noGitHint = ref('historyConfig.no_git_repo')

// 保留策略（history.checkpoint_keep / history.checkpoint_ttl_days，字符串数字；缺失/空 → 默认 500 / 7）
const checkpointKeep = ref(String(DEFAULT_CHECKPOINT_KEEP))
const checkpointTtlDays = ref(String(DEFAULT_CHECKPOINT_TTL_DAYS))
const keepError = ref('')
const ttlError = ref('')
const saving = ref(false)

// 只读时间轴：history.status / history.timeline（JSON 文本；解析失败 → 未启用/无数据，不报错）
const status = ref(null)
const timeline = ref([])
const clearing = ref(false)

// 模式（active 正常 / fused 已熔断放行 / off 未启用）
const modeClass = computed(() => 'is-' + statusMode(status.value))
const modeText = computed(() => t('historyConfig.mode_' + statusMode(status.value)))
const statusEnabledNow = computed(() => statusEnabled(status.value))
const statusCount = computed(() => Number(status.value?.checkpointCount) || 0)
const bytesText = computed(() => formatBytes(status.value?.bytes))
const lastTimeText = computed(() => formatTime(status.value?.lastCheckpointAt) || '—')
const lastDurationText = computed(() => {
  const ms = status.value?.lastDurationMs
  return (ms === undefined || ms === null) ? '—' : `${Number(ms) || 0} ms`
})
const failCount = computed(() => Number(status.value?.failCount) || 0)
const statusDirty = computed(() => status.value?.dirty === true)
const lastError = computed(() => (status.value?.lastError ? String(status.value.lastError) : ''))

// 空态可区分：未启用 vs 已启用但无检查点
const timelineEmptyText = computed(() => (
  statusEnabledNow.value ? t('historyConfig.empty_none') : t('historyConfig.empty_disabled')
))

const timelineColumns = computed(() => ([
  { prop: 'n', label: t('historyConfig.col_n'), width: 64 },
  { prop: 'ts', label: t('historyConfig.col_time'), width: 170 },
  { prop: 'tool', label: t('historyConfig.col_tool') },
  { prop: 'session', label: t('historyConfig.col_session') },
  { prop: 'files', label: t('historyConfig.col_files'), width: 80 },
  { prop: 'delta', label: t('historyConfig.col_delta'), width: 110 },
]))

async function loadVCS() {
  try {
    const res = await getVCSInfo()
    // gitInstalled 缺失（旧桥）按已安装处理，保持既有行为
    const installed = res.gitInstalled !== false
    const isRepo = !!res.git
    gitAvailable.value = installed && isRepo
    noGitHint.value = installed ? 'historyConfig.no_git_repo' : 'historyConfig.no_git_installed'
  } catch (_) {
    gitAvailable.value = true
  }
}

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    // 默认不开启：只有显式 "true" 才视为开启（缺失/其他值 = 关闭；与后端口径一致）
    historyEnabled.value = c['history.enabled'] === 'true'
    // 保留策略：仅在「用户未编辑」（dirty=false）时回填，避免刷新冲掉未保存输入
    if (!dirty.value) {
      checkpointKeep.value = retentionValue(c['history.checkpoint_keep'], DEFAULT_CHECKPOINT_KEEP)
      checkpointTtlDays.value = retentionValue(c['history.checkpoint_ttl_days'], DEFAULT_CHECKPOINT_TTL_DAYS)
    }
    // 只读时间轴（解析失败/键缺失 → 未启用/无数据，不报错）
    status.value = parseStatus(c['history.status'])
    timeline.value = parseTimeline(c['history.timeline'])
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('historyConfig.enable'), e))
  }
}

// 开启前先确认（大工程打点 checkpoint 可能影响效率）；取消 → 回退开关、不写库。
// history.enabled 由插件订阅 data-prj-config-refresh **实时同步** → 保存即生效（无需重启）。
async function handleChange(val) {
  const prev = !val
  if (val === true) {
    try {
      await confirm(t('historyConfig.enable_confirm'), t('historyConfig.enable_confirm_title'))
    } catch (_) {
      historyEnabled.value = false
      return
    }
  }
  markDirty()
  try {
    await setConfig('history.enabled', String(val))
    markSaved()
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    historyEnabled.value = prev // 落库失败 → 回退开关，避免与已保存状态不一致
    markSaved()
    message.error(saveFailedText(t, e))
  }
}

// 保存保留策略：正整数前置校验（非法不写库），合法则写两个 prj 键。
async function handleRetentionSave() {
  const kr = validatePositiveInt(checkpointKeep.value)
  keepError.value = kr.ok ? '' : positiveIntErrorText(t, kr.reason)
  const tr = validatePositiveInt(checkpointTtlDays.value)
  ttlError.value = tr.ok ? '' : positiveIntErrorText(t, tr.reason)
  if (!kr.ok || !tr.ok) return
  markDirty()
  saving.value = true
  try {
    await setConfig('history.checkpoint_keep', String(kr.value))
    await setConfig('history.checkpoint_ttl_days', String(tr.value))
    checkpointKeep.value = String(kr.value)
    checkpointTtlDays.value = String(tr.value)
    markSaved()
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 清空历史：写 history.clear（任意新值即触发后端清空该链；零新增 MQ 主题），破坏性 → 二次确认。
async function handleClear() {
  try {
    await confirm(t('historyConfig.clear_confirm'), t('historyConfig.clear_confirm_title'))
  } catch (_) {
    return // 用户取消
  }
  clearing.value = true
  try {
    await setConfig('history.clear', new Date().toISOString())
    message.success(t('historyConfig.cleared'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    clearing.value = false
  }
}

onMounted(() => {
  loadConfig()
  loadVCS()
  // data-prj-config-refresh：配置变更后 server 广播，自动重载（20-gui）。
  // **本页自身保存期间（saving）跳过**：handleRetentionSave 写两个 prj 键，persist 每次 save 恒广播；
  // 早到的广播会读到「尚含旧值」的快照（history.enabled / status 按 DB 无条件回填），与本次提交的本地态打架。
  // 统一口径与 ContextConfig I-138 一致：保存期间不重载；保存结束后到达的广播读到的是本次已落库值，重载无害。
  // （保留策略另有 !dirty 守卫，防刷新冲掉未保存输入；开关即存路径 saving=false，仍照常重载。）
  unsubs.push(onDataRefresh('prj-config', () => { if (!saving.value) loadConfig() }))
})

onUnmounted(() => {
  unsubs.forEach(fn => fn())
})
</script>

<style scoped>
.history-root {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.history-tab-content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-top: 12px;
}
.form-layout {
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
}
.form-item {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  width: 100%;
}
.form-item-full {
  width: 100%;
}
.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
.history-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}
.history-reminder {
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
}
.history-warn { color: var(--warning, #e6a23c); }
.history-error {
  margin-top: 6px;
  font-size: 12px;
  color: var(--danger, #d9534f);
  line-height: 1.5;
}
/* ⑤ dirty 标记（仅显示） */
.unsaved-mark { font-size: 12px; color: var(--warning, #e6a23c); white-space: nowrap; }
.history-num-row {
  display: flex;
  flex-wrap: wrap;
  gap: 20px;
  margin-top: 4px;
}
.history-num {
  display: flex;
  align-items: center;
  gap: 8px;
}
.history-num-label {
  font-size: 13px;
  color: var(--text-secondary);
  white-space: nowrap;
}
.history-num-input {
  width: 120px;
}
.history-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 8px;
}
/* ── 检查点时间轴（只读） ── */
.tl-status {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
  margin: 6px 0;
  font-size: 13px;
  color: var(--text-primary);
}
.tl-status-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.tl-status-key {
  color: var(--text-muted);
  font-size: 12px;
}
.tl-status-val {
  font-weight: 500;
}
.tl-status-val.is-active { color: var(--success); }
.tl-status-val.is-fused { color: var(--warning); }
.tl-status-val.is-off { color: var(--text-muted); }
.tl-status-dirty {
  color: var(--warning);
  font-size: 12px;
}
.tl-status-error {
  margin: 4px 0;
  font-size: 12px;
  color: var(--danger);
  line-height: 1.5;
  word-break: break-all;
}
.tl-n {
  font-family: var(--font-mono);
}
.tl-add { color: var(--success); }
.tl-del { color: var(--danger); margin-left: 8px; }
</style>
