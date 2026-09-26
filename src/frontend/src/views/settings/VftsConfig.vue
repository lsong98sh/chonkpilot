<template>
  <div class="vf-root">
    <div class="tab-toolbar">
      <span class="tab-title">{{ $t('projectConfig.vfts') }}</span>
      <div class="tab-actions">
        <!-- ⑤ dirty 标记（仅显示，不改按钮保存的时机） -->
        <span v-if="unsaved" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" :disabled="saving || !hasProjectOverride" @click="handleResetDefaults">{{ $t('projectConfig.index_reset_default') }}</Button>
        <Button size="small" type="primary" :loading="saving" @click="handleIndexSave">{{ $t('projectConfig.save') }}</Button>
      </div>
    </div>
    <div class="vf-tab-content">
      <form class="form-layout">
        <div class="form-item form-item-full">
          <div class="vf-toggle">
            <label class="form-label">{{ $t('projectConfig.vfts') }}</label>
            <Switch v-model="vfEnabled" @update:model-value="handleChange" />
          </div>
          <div class="vf-hint">
            启用后本项目的 vfts 全文检索工具（vfts_query）会注入到 LLM 工具面，
            首次启用会自动后台建索引；停用即从工具面移除（索引保留在
            <code>.chonkpilot/vfts/</code>，重开会增量续刷）。
          </div>
        </div>
        <hr class="b-divider" />
        <div class="form-item form-item-full">
          <label class="form-label">{{ $t('projectConfig.index_state_label') }}</label>
          <div class="vf-state">
            <span class="vf-state-text">{{ stateText }}</span>
            <span v-if="showProgress" class="vf-progress">（{{ progressDone }}/{{ progressTotal }}）</span>
            <span v-if="phaseText" class="vf-phase">{{ phaseText }}</span>
          </div>
          <div v-if="statusError" class="vf-error">{{ statusError }}</div>
          <div v-if="statItems.length" class="vf-stats">
            <span v-for="it in statItems" :key="it.label" class="vf-stat">{{ it.label }}：{{ it.value }}</span>
          </div>
          <!-- 运行时可观测口径（真实可得）：vfts 查询工具是否已注册到工具面（tools-list）。
               插件侧无「引擎子进程运行中」信号下发 → 不伪造进程态，只报注册态。 -->
          <div v-if="vfEnabled" class="vf-engine">
            {{ $t('projectConfig.engine_tool_state') }}：{{ toolsRegistered ? $t('projectConfig.engine_registered') : $t('projectConfig.engine_unregistered') }}
            <span class="vf-engine-hint">{{ $t('projectConfig.engine_tool_state_hint') }}</span>
          </div>
        </div>
        <hr class="b-divider" />
        <div class="form-item form-item-full">
          <label class="form-label">参与索引的扩展名</label>
          <Textarea v-model="vfExts" :rows="3" placeholder=".go, .js, .ts, .md, .txt（逗号或换行分隔）" />
          <div class="vf-hint">留空 = 使用引擎默认扩展名集合；保存后后台重建索引。</div>
        </div>
        <div class="form-item form-item-full">
          <label class="form-label">排除目录</label>
          <Textarea v-model="vfSkipDirs" :rows="3" placeholder="node_modules, dist（逗号或换行分隔）" />
          <div class="vf-hint">在引擎默认跳过目录（已含 <code>.chonkpilot</code>）之外追加排除；保存后后台重建索引。</div>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Switch, Textarea, Button, message } from '../../components/ui'
import { getAllConfig, setConfig, deleteConfig } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { hasEngineTools } from '../../utils/engineStatus'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import mq from '../../utils/mq'
import { DEFAULT_SKIP_DIRS, VFTS_DEFAULT_EXTS } from './indexDefaults'

const { t } = useI18n()
const unsubs = []

const vfEnabled = ref(false)
// vfts.status 由 vfts plugin 回写（JSON 文本：state/phase/progressDone/progressTotal/
// indexedFiles/chunkCount/tokenizer/exts/err，键见引擎 server.Status）
const status = ref(null)
// 运行时可观测态：vfts 查询工具是否已注册到工具面（tools-list 是否存在 vfts_query）。
// 插件侧无「引擎子进程运行中」信号 → 只报真实可得的注册态（见 utils/engineStatus.js）。
const toolsRegistered = ref(false)
// 项目级索引配置（逗号/换行分隔文本；空 = 引擎默认集）
const vfExts = ref('')
const vfSkipDirs = ref('')
// 上次从项目配置读到的原始值（'' = 项目级无该键）；用于判断用户是否真的改动过，
// 避免把「默认值镜像」直接保存成显式项目配置（固化后引擎默认变更不再自动生效）
const origExts = ref('')
const origSkipDirs = ref('')
const loadedOnce = ref(false)
const saving = ref(false)

// 引擎状态 state 取值（引擎 Meta："" 未初始化 | indexing | ready | error）
const stateTexts = computed(() => ({
  '': t('projectConfig.index_state_uninitialized'),
  indexing: t('projectConfig.index_state_indexing'),
  ready: t('projectConfig.index_state_ready'),
  error: t('projectConfig.index_state_error'),
}))

const stateText = computed(() => {
  const s = status.value
  if (!s) return vfEnabled.value ? stateTexts.value[''] : t('projectConfig.index_state_disabled')
  // state 为空串 = 引擎未初始化（不再落入「启用中…」的误导性回显）
  return stateTexts.value[s.state] || s.state || stateTexts.value['']
})
const progressDone = computed(() => status.value?.progressDone || 0)
const progressTotal = computed(() => status.value?.progressTotal || 0)
// 终态（ready/error）不再展示进度
const isTerminal = computed(() => {
  const st = status.value?.state
  return st === 'ready' || st === 'error'
})
const showProgress = computed(() => progressTotal.value > 0 && !isTerminal.value)
const phaseText = computed(() => {
  const s = status.value
  if (!s || s.state !== 'indexing' || !s.phase) return ''
  return `${t('projectConfig.index_phase')}：${s.phase}`
})
const statusError = computed(() => {
  const s = status.value
  if (!s) return ''
  return s.err || s.message || ''
})
// 状态明细（有则显示）：已索引文件 / 分块数 / 分词器 / 生效扩展名
const statItems = computed(() => {
  const s = status.value
  if (!s) return []
  const out = []
  if (typeof s.indexedFiles === 'number') out.push({ label: t('projectConfig.index_stat_files'), value: String(s.indexedFiles) })
  if (typeof s.chunkCount === 'number') out.push({ label: t('projectConfig.index_stat_chunks'), value: String(s.chunkCount) })
  if (s.tokenizer) out.push({ label: t('projectConfig.index_stat_tokenizer'), value: s.tokenizer })
  if (Array.isArray(s.exts) && s.exts.length) out.push({ label: t('projectConfig.index_stat_exts'), value: s.exts.join(', ') })
  return out
})

// 项目级键是否存在（存在才允许「恢复默认」删键）
const hasProjectOverride = computed(() => origExts.value !== '' || origSkipDirs.value !== '')

// 展示镜像 = 项目级值；为空 → 回填引擎默认（展示值 = 实际生效值，I-65 ⑫）
function displayExts(raw) { return raw || VFTS_DEFAULT_EXTS.join(', ') }
function displaySkipDirs(raw) { return raw || DEFAULT_SKIP_DIRS.join(', ') }

// 输入框是否仍等于「上次加载值的展示镜像」（= 用户未编辑）
function isPristine(current, orig, display) { return current === display(orig) }

// ⑤ 未保存标记：任一索引输入与已加载值不一致（仅显示，不改保存时机）
const unsaved = computed(() =>
  !isPristine(vfExts.value, origExts.value, displayExts) ||
  !isPristine(vfSkipDirs.value, origSkipDirs.value, displaySkipDirs)
)

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    vfEnabled.value = c['enable-vfts'] === 'true'
    const rawExts = c['vfts.exts'] || ''
    const rawSkipDirs = c['vfts.skip-dirs'] || ''
    // 仅在「首次加载」或「用户未编辑」时覆盖输入框：索引期间插件每 500ms 回写
    // vfts.status 并广播 prj-config-refresh，避免把未保存的编辑冲掉。
    if (!loadedOnce.value || isPristine(vfExts.value, origExts.value, displayExts)) {
      vfExts.value = displayExts(rawExts)
    }
    if (!loadedOnce.value || isPristine(vfSkipDirs.value, origSkipDirs.value, displaySkipDirs)) {
      vfSkipDirs.value = displaySkipDirs(rawSkipDirs)
    }
    origExts.value = rawExts
    origSkipDirs.value = rawSkipDirs
    loadedOnce.value = true
    const raw = c['vfts.status']
    if (raw) {
      try { status.value = JSON.parse(raw) } catch (_) { status.value = null }
    } else {
      status.value = null
    }
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('projectConfig.vfts'), e))
  }
}

// 读工具面判定「查询工具已注册」（真实可得信号；插件无进程运行态下发）。
async function loadEngineTools() {
  try {
    const env = await mq.emit('tools-list', {})
    const res = env && env.backend && env.backend.result
    const tools = res && Array.isArray(res.tools) ? res.tools : []
    toolsRegistered.value = hasEngineTools(tools, 'vfts')
  } catch (_) {
    toolsRegistered.value = false
  }
}

async function handleChange(val) {
  try {
    await setConfig('enable-vfts', String(val))
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

// 写单键：清空 = 删项目级键（恢复引擎默认，避免把默认集固化成显式配置）
async function writeIndexKey(key, textRef, origRef, display) {
  const v = textRef.value.trim()
  if (v === '') {
    await deleteConfig(key)
  } else {
    await setConfig(key, textRef.value)
  }
  origRef.value = v
  textRef.value = display(v) // 回填（清空 → 默认镜像，与首次打开一致）
}

// 保存索引配置（exts/skip-dirs）：仅写入实际改动的键；写入后 data-prj-config-refresh
// 触发插件重新 configure + 重建索引（插件侧对同一次保存的两键做去抖，只重建一轮）。
async function handleIndexSave() {
  saving.value = true
  try {
    const tasks = []
    if (!isPristine(vfExts.value, origExts.value, displayExts)) {
      tasks.push(writeIndexKey('vfts.exts', vfExts, origExts, displayExts))
    }
    if (!isPristine(vfSkipDirs.value, origSkipDirs.value, displaySkipDirs)) {
      tasks.push(writeIndexKey('vfts.skip-dirs', vfSkipDirs, origSkipDirs, displaySkipDirs))
    }
    if (tasks.length === 0) {
      message.info(t('projectConfig.index_nothing_changed'))
      return
    }
    await Promise.all(tasks)
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 恢复默认：清除项目级 exts/skip-dirs 键（引擎默认集生效，回填默认镜像）
async function handleResetDefaults() {
  saving.value = true
  try {
    const tasks = []
    if (origExts.value !== '') tasks.push(deleteConfig('vfts.exts'))
    if (origSkipDirs.value !== '') tasks.push(deleteConfig('vfts.skip-dirs'))
    if (tasks.length === 0) return
    await Promise.all(tasks)
    origExts.value = ''
    origSkipDirs.value = ''
    vfExts.value = displayExts('')
    vfSkipDirs.value = displaySkipDirs('')
    message.success(t('projectConfig.index_reset_done'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadConfig()
  loadEngineTools()
  // data-prj-config-refresh：配置/状态变更后自动重载
  unsubs.push(onDataRefresh('prj-config', loadConfig))
})

onUnmounted(() => {
  unsubs.forEach(fn => fn())
})
</script>

<style scoped>
.vf-root {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
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
.vf-tab-content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-top: 0;
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
.vf-hint {
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.6;
}
.vf-hint code {
  background: var(--bg-secondary, rgba(0, 0, 0, 0.05));
  padding: 0 4px;
  border-radius: 3px;
}
.vf-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}
.vf-state {
  font-size: 13px;
  color: var(--text-primary);
}
.vf-state-text {
  font-weight: 500;
}
.vf-progress {
  color: var(--text-muted);
}
.vf-phase {
  margin-left: 8px;
  font-size: 12px;
  color: var(--text-muted);
}
.vf-error {
  font-size: 12px;
  color: var(--danger, #d9534f);
  line-height: 1.6;
  word-break: break-all;
}
.vf-engine {
  margin-top: 4px;
  font-size: 13px;
  color: var(--text-primary);
}
.vf-engine-hint {
  margin-left: 8px;
  font-size: 11px;
  color: var(--text-muted);
}
.vf-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.6;
}
.b-divider {
  border: none;
  border-top: 1px solid var(--border, #dee2e6);
  margin: 8px 0;
}
</style>
