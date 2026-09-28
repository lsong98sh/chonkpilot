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
          <label class="form-label">{{ $t('projectConfig.index_exts_label') }}</label>
          <Textarea v-model="vfExts" :rows="3" placeholder=".go, .js, .ts, .md, .txt（逗号或换行分隔）" />
          <div class="vf-hint">{{ $t('projectConfig.index_exts_hint') }}</div>
        </div>
        <div class="form-item form-item-full">
          <!-- label 右侧「叠加 gitignore」勾选：勾选后输入框仍可编辑（保存时把规则与开关一并下发引擎，
               由引擎按 gitignore 语义叠加各级 .gitignore / info/exclude / 全局 ignore） -->
          <div class="form-label-row">
            <label class="form-label">{{ $t('projectConfig.exclude_paths') }}</label>
            <label class="b-checkbox">
              <input type="checkbox" v-model="vfStackGitignore" />
              <span>{{ $t('projectConfig.stack_gitignore') }}</span>
            </label>
          </div>
          <Textarea v-model="vfSkipDirs" :rows="3" placeholder="node_modules/, dist/, !dist/keep.log（逗号或换行分隔）" />
          <div class="vf-hint">{{ $t('projectConfig.exclude_paths_hint') }}</div>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Switch, Textarea, Button, message } from '../../components/ui'
import { getAllConfig, setConfig, setConfigs, deleteConfig } from '../../api/config'
import { usePrjConfigRefresh } from '../../composables/usePrjConfigRefresh'
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
// 项目级索引配置（逗号/换行分隔文本；空 = 引擎默认规则集）
const vfExts = ref('')
const vfSkipDirs = ref('')
// 叠加 gitignore 勾选（'vfts.stack-gitignore' == "true"）：勾选后仍可编辑输入框，
// 保存时把「用户规则 + 叠加开关」一并下发引擎（gitignore 语义在引擎侧统一实现）。
const vfStackGitignore = ref(false)
// 上次从项目配置读到的原始值（'' = 项目级无该键）；用于判断用户是否真的改动过，
// 避免把「默认值镜像」直接保存成显式项目配置（固化后引擎默认变更不再自动生效）
const origExts = ref('')
const origSkipDirs = ref('')
const origStackGitignore = ref('')
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
const hasProjectOverride = computed(() =>
  origExts.value !== '' || origSkipDirs.value !== '' || origStackGitignore.value !== ''
)

// 展示镜像 = 项目级值；为空 → 回填引擎默认（展示值 = 实际生效值，I-65 ⑫）
function displayExts(raw) { return raw || VFTS_DEFAULT_EXTS.join(', ') }
function displaySkipDirs(raw) { return raw || DEFAULT_SKIP_DIRS.join(', ') }

// 输入框是否仍等于「上次加载值的展示镜像」（= 用户未编辑）
function isPristine(current, orig, display) { return current === display(orig) }

// ⑤ 未保存标记：任一索引输入/勾选与已加载值不一致（仅显示，不改保存时机）
const unsaved = computed(() =>
  !isPristine(vfExts.value, origExts.value, displayExts) ||
  !isPristine(vfSkipDirs.value, origSkipDirs.value, displaySkipDirs) ||
  vfStackGitignore.value !== (origStackGitignore.value === 'true')
)

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    vfEnabled.value = c['enable-vfts'] === 'true'
    const rawExts = c['vfts.exts'] || ''
    const rawSkipDirs = c['vfts.skip-dirs'] || ''
    const rawStack = c['vfts.stack-gitignore'] || ''
    // 仅在「首次加载」或「用户未编辑」时覆盖输入框：索引期间插件每 500ms 回写
    // vfts.status 并广播 prj-config-refresh，避免把未保存的编辑冲掉。
    if (!loadedOnce.value || isPristine(vfExts.value, origExts.value, displayExts)) {
      vfExts.value = displayExts(rawExts)
    }
    if (!loadedOnce.value || isPristine(vfSkipDirs.value, origSkipDirs.value, displaySkipDirs)) {
      vfSkipDirs.value = displaySkipDirs(rawSkipDirs)
    }
    if (!loadedOnce.value || vfStackGitignore.value === (origStackGitignore.value === 'true')) {
      vfStackGitignore.value = rawStack === 'true'
    }
    origExts.value = rawExts
    origSkipDirs.value = rawSkipDirs
    origStackGitignore.value = rawStack
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

// collectIndexChanges 收集本次「实际改动」的键：文本项（exts/skip-dirs）空串 = 清空（回落引擎
// 默认，走删键，避免把默认集固化成显式配置）→ clears；非空 → entries。勾选 stack-gitignore
// 改动 → entries（"true"/"false"）。同时回填 orig / 展示镜像（清空 → 默认镜像，与首次打开一致）。
function collectIndexChanges() {
  const entries = {}
  const clears = []
  for (const it of [
    { key: 'vfts.exts', textRef: vfExts, origRef: origExts, display: displayExts },
    { key: 'vfts.skip-dirs', textRef: vfSkipDirs, origRef: origSkipDirs, display: displaySkipDirs },
  ]) {
    if (isPristine(it.textRef.value, it.origRef.value, it.display)) continue
    const v = it.textRef.value.trim()
    if (v === '') {
      clears.push(it.key)
      it.origRef.value = ''
      it.textRef.value = it.display('')
    } else {
      entries[it.key] = it.textRef.value
      it.origRef.value = v
    }
  }
  if (vfStackGitignore.value !== (origStackGitignore.value === 'true')) {
    const v = String(vfStackGitignore.value)
    entries['vfts.stack-gitignore'] = v
    origStackGitignore.value = v
  }
  return { entries, clears }
}

// 保存索引配置（exts/skip-dirs/stack-gitignore）：只提交实际改动的键。
// 非空改动值 → **一次批量写**（1 条 data-prj-config-save → 后端整批广播 1 条 data-prj-config-refresh，
// 含 ids 全组键）触发插件重新 configure + 重建索引（插件按整批键集处理 + 去抖一轮）。
// 清空 = 删项目级键（恢复引擎默认）→ 仍走既有 delete 面（语义不变）。
async function handleIndexSave() {
  saving.value = true
  try {
    const { entries, clears } = collectIndexChanges()
    if (Object.keys(entries).length === 0 && clears.length === 0) {
      message.info(t('projectConfig.index_nothing_changed'))
      return
    }
    if (Object.keys(entries).length > 0) await setConfigs(entries)
    if (clears.length > 0) await Promise.all(clears.map((k) => deleteConfig(k)))
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 恢复默认：清除项目级 exts/skip-dirs/stack-gitignore 键（引擎默认集生效，回填默认镜像）
async function handleResetDefaults() {
  saving.value = true
  try {
    const tasks = []
    if (origExts.value !== '') tasks.push(deleteConfig('vfts.exts'))
    if (origSkipDirs.value !== '') tasks.push(deleteConfig('vfts.skip-dirs'))
    if (origStackGitignore.value !== '') tasks.push(deleteConfig('vfts.stack-gitignore'))
    if (tasks.length === 0) return
    await Promise.all(tasks)
    origExts.value = ''
    origSkipDirs.value = ''
    origStackGitignore.value = ''
    vfExts.value = displayExts('')
    vfSkipDirs.value = displaySkipDirs('')
    vfStackGitignore.value = false
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
  // data-prj-config-refresh：配置/状态变更后自动重载（统一机制 usePrjConfigRefresh，I-138）。
  // handleIndexSave 一次批量写改动的项 → 后端整批广播 1 条（含 ids）；清空项走 delete（单键广播）；
  // handleResetDefaults 逐键 delete；统一机制**按键过滤**（仅本页关注键）+ **合并突发广播为 1 次
  // 重载**（读最终快照）+ **保存期间（saving）跳过**，避免读到「尚含旧中间值」的快照与本页正在
  // 提交的本地态打架。
  // （文本输入另有 isPristine 守卫：防索引期间插件每 500ms 回写 status 的广播冲掉未保存编辑。）
  unsubs.push(usePrjConfigRefresh({
    keys: ['enable-vfts', 'vfts.exts', 'vfts.skip-dirs', 'vfts.stack-gitignore', 'vfts.status'],
    reload: loadConfig,
    isSaving: () => saving.value,
  }))
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
/* label 行：左 label + 右「叠加 gitignore」勾选 */
.form-label-row {
  display: flex;
  align-items: center;
  gap: 12px;
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
.vf-hint {
  font-size: 12px;
  color: var(--fg-secondary);
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
