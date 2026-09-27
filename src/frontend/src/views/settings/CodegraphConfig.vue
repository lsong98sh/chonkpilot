<template>
  <div class="cg-root">
    <div class="tab-toolbar">
      <span class="tab-title">{{ $t('projectConfig.codegraph') }}</span>
      <div class="tab-actions">
        <!-- ⑤ dirty 标记（仅显示，不改按钮保存的时机） -->
        <span v-if="unsaved" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" :disabled="saving || !cgEnabled" @click="handleRebuild">{{ $t('projectConfig.index_rebuild') }}</Button>
        <Button size="small" :disabled="saving || !cgEnabled" @click="handleRetry">{{ $t('projectConfig.index_retry') }}</Button>
        <Button size="small" type="danger" :disabled="saving || !cgEnabled" @click="handleClear">{{ $t('projectConfig.index_clear') }}</Button>
        <Button size="small" type="primary" :loading="saving" @click="handleIndexSave">{{ $t('projectConfig.save') }}</Button>
      </div>
    </div>
    <div class="cg-tab-content">
      <form class="form-layout">
        <div class="form-item form-item-full">
          <div class="cg-toggle">
            <label class="form-label">{{ $t('projectConfig.codegraph') }}</label>
            <Switch v-model="cgEnabled" @update:model-value="handleChange" />
          </div>
          <div class="cg-hint">
            启用后本项目的 codegraph 索引工具（codegraph_symbol_search 等）会注入到 LLM 工具面，
            首次启用会自动后台建索引；停用即从工具面移除（索引保留在
            <code>.chonkpilot/codegraph/</code>，重开会增量续刷）。
          </div>
        </div>
        <hr class="b-divider" />
        <div class="form-item form-item-full">
          <label class="form-label">{{ $t('projectConfig.index_state_label') }}</label>
          <div class="cg-state">
            <span class="cg-state-text">{{ stateText }}</span>
            <!-- 停用态不展示引擎状态明细（陈旧 codegraph.status 会误判为仍在索引/已就绪） -->
            <template v-if="cgEnabled">
              <span v-if="showProgress" class="cg-progress">（{{ progressDone }}/{{ progressTotal }}）</span>
              <span v-if="phaseText" class="cg-phase">{{ phaseText }}</span>
            </template>
          </div>
          <!-- 失败着色：编排/引擎错误信息（同停用态一致，不在关闭后回显陈旧错误） -->
          <div v-if="cgEnabled && statusError" class="cg-error">{{ statusError }}</div>
          <!-- 运行时可观测口径（真实可得）：查询工具是否已注册到工具面（tools-list）。
               插件侧无「引擎子进程运行中」信号下发 → 不伪造进程态，只报注册态。 -->
          <div v-if="cgEnabled" class="cg-engine">
            {{ $t('projectConfig.engine_tool_state') }}：{{ toolsRegistered ? $t('projectConfig.engine_registered') : $t('projectConfig.engine_unregistered') }}
            <span class="cg-engine-hint">{{ $t('projectConfig.engine_tool_state_hint') }}</span>
          </div>
        </div>
        <hr class="b-divider" />
        <div class="form-item form-item-full">
          <label class="form-label">{{ $t('projectConfig.index_exts_label') }}</label>
          <Textarea v-model="cgExts" :rows="3" placeholder=".go, .js, .ts, .py（逗号或换行分隔）" />
          <div class="cg-hint">{{ $t('projectConfig.index_exts_hint') }}</div>
        </div>
        <div class="form-item form-item-full">
          <!-- label 右侧「叠加 gitignore」勾选：勾选后输入框仍可编辑（保存时把规则与开关一并下发引擎，
               由引擎按 gitignore 语义叠加各级 .gitignore / info/exclude / 全局 ignore） -->
          <div class="form-label-row">
            <label class="form-label">{{ $t('projectConfig.exclude_paths') }}</label>
            <label class="b-checkbox">
              <input type="checkbox" v-model="cgStackGitignore" />
              <span>{{ $t('projectConfig.stack_gitignore') }}</span>
            </label>
          </div>
          <Textarea v-model="cgSkipDirs" :rows="3" placeholder="node_modules/, dist/, !dist/keep.log（逗号或换行分隔）" />
          <div class="cg-hint">{{ $t('projectConfig.exclude_paths_hint') }}</div>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Switch, Textarea, Button, message, confirm } from '../../components/ui'
import { getAllConfig, setConfig } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { hasEngineTools } from '../../utils/engineStatus'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import mq from '../../utils/mq'
import { DEFAULT_SKIP_DIRS, CODEGRAPH_DEFAULT_EXTS } from './indexDefaults'

const { t } = useI18n()
const unsubs = []

const cgEnabled = ref(false)
// codegraph.status 由 codegraph plugin 回写（JSON 文本：state/phase/progressDone/progressTotal/
// message，键见插件 pushStatusPhase/saveStatusErr）
const status = ref(null)
// 运行时可观测态：codegraph 查询工具是否已注册到工具面（tools-list 是否存在 codegraph_*）。
// 插件侧无「引擎子进程运行中」信号 → 只报真实可得的注册态（见 utils/engineStatus.js）。
const toolsRegistered = ref(false)
// 项目级索引配置（逗号/换行分隔文本；空 = 引擎默认规则集）
const cgExts = ref('')
const cgSkipDirs = ref('')
// 叠加 gitignore 勾选（'codegraph.stack-gitignore' == "true"）：勾选后仍可编辑输入框，
// 保存时把「用户规则 + 叠加开关」一并下发引擎（gitignore 语义在引擎侧统一实现）。
const cgStackGitignore = ref(false)
// 上次从项目配置读到的原始值（'' = 项目级无该键）；用于「仅写入实际改动的键」与重建入口
const origExts = ref('')
const origSkipDirs = ref('')
const origStackGitignore = ref('')
const loadedOnce = ref(false)
const saving = ref(false)

// 引擎状态 state 取值（引擎 Meta："" 未初始化 | not_initialized | indexing | ready；插件侧 error）
const stateTexts = computed(() => ({
  '': t('projectConfig.index_state_uninitialized'),
  not_initialized: t('projectConfig.index_state_uninitialized'),
  indexing: t('projectConfig.index_state_indexing'),
  ready: t('projectConfig.index_state_ready'),
  error: t('projectConfig.index_state_error'),
}))

// 开关关闭 → 一律显示「已停用」，不展示陈旧的 codegraph.status（避免误判仍在索引/已就绪）
const stateText = computed(() => {
  if (!cgEnabled.value) return t('projectConfig.index_state_disabled')
  const s = status.value
  if (!s) return stateTexts.value['']
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
// 阶段（configure/index）：仅在索引进行中展示
const phaseText = computed(() => {
  const s = status.value
  if (!s || s.state !== 'indexing' || !s.phase) return ''
  return `${t('projectConfig.index_phase')}：${s.phase}`
})
// 失败原因（插件编排失败写 message，引擎侧错误写 err）
const statusError = computed(() => {
  const s = status.value
  if (!s) return ''
  return s.message || s.err || ''
})

// 展示镜像 = 项目级值；为空 → 回填引擎默认（展示值 = 实际生效值，I-65 ⑫）
function displayExts(raw) { return raw || CODEGRAPH_DEFAULT_EXTS.join(', ') }
function displaySkipDirs(raw) { return raw || DEFAULT_SKIP_DIRS.join(', ') }

// 输入框是否仍等于「上次加载值的展示镜像」（= 用户未编辑）
function isPristine(current, orig, display) { return current === display(orig) }

// ⑤ 未保存标记：任一索引输入/勾选与已加载值不一致（仅显示，不改保存时机）
const unsaved = computed(() =>
  !isPristine(cgExts.value, origExts.value, displayExts) ||
  !isPristine(cgSkipDirs.value, origSkipDirs.value, displaySkipDirs) ||
  cgStackGitignore.value !== (origStackGitignore.value === 'true')
)

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    cgEnabled.value = c['enable-codegraph'] === 'true'
    const rawExts = c['codegraph.exts'] || ''
    const rawSkipDirs = c['codegraph.skip-dirs'] || ''
    const rawStack = c['codegraph.stack-gitignore'] || ''
    // 仅在「首次加载」或「用户未编辑」时覆盖输入框：索引期间插件每 500ms 回写
    // codegraph.status 并广播 prj-config-refresh，避免把未保存的编辑冲掉。
    if (!loadedOnce.value || isPristine(cgExts.value, origExts.value, displayExts)) {
      cgExts.value = displayExts(rawExts)
    }
    if (!loadedOnce.value || isPristine(cgSkipDirs.value, origSkipDirs.value, displaySkipDirs)) {
      cgSkipDirs.value = displaySkipDirs(rawSkipDirs)
    }
    if (!loadedOnce.value || cgStackGitignore.value === (origStackGitignore.value === 'true')) {
      cgStackGitignore.value = rawStack === 'true'
    }
    origExts.value = rawExts
    origSkipDirs.value = rawSkipDirs
    origStackGitignore.value = rawStack
    loadedOnce.value = true
    const raw = c['codegraph.status']
    if (raw) {
      try { status.value = JSON.parse(raw) } catch (_) { status.value = null }
    } else {
      status.value = null
    }
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('projectConfig.codegraph'), e))
  }
}

// 读工具面判定「查询工具已注册」（真实可得信号；插件无进程运行态下发）。
async function loadEngineTools() {
  try {
    const env = await mq.emit('tools-list', {})
    const res = env && env.backend && env.backend.result
    const tools = res && Array.isArray(res.tools) ? res.tools : []
    toolsRegistered.value = hasEngineTools(tools, 'codegraph')
  } catch (_) {
    toolsRegistered.value = false
  }
}

async function handleChange(val) {
  try {
    await setConfig('enable-codegraph', String(val))
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

// 保存索引配置（exts/skip-dirs/stack-gitignore）：写入后 data-prj-config-refresh 触发插件重新
// configure + 重建索引。插件侧保存幂等（值未变不重建）且对同一次保存的键做去抖（只重建一轮）。
// skip-dirs = 用户排除规则（gitignore 语法）；stack-gitignore = 是否叠加各级 .gitignore / info/exclude / 全局 ignore。
async function handleIndexSave() {
  saving.value = true
  try {
    await setConfig('codegraph.exts', cgExts.value)
    await setConfig('codegraph.skip-dirs', cgSkipDirs.value)
    await setConfig('codegraph.stack-gitignore', String(cgStackGitignore.value))
    origExts.value = cgExts.value // 本次写入值 = 新的项目级原始值（后续加载的未编辑判定基准）
    origSkipDirs.value = cgSkipDirs.value
    origStackGitignore.value = String(cgStackGitignore.value)
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 写动作信号键（codegraph.action = rebuild|clear|retry）：经既有 data-prj-config 面让插件
// 收到一次变更信号（零新增消息主题）。persist save 恒广播 data-prj-config-refresh →
// 重复点击同值仍生效；插件按该键执行后回写 codegraph.status，UI 经既有回显自动刷新。
function emitAction(action) {
  return setConfig('codegraph.action', action)
}

// 重建索引：插件对该 workdir 强制全量重建（索引配置不变）
async function handleRebuild() {
  saving.value = true
  try {
    await emitAction('rebuild')
    message.success(t('projectConfig.index_rebuild_started'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 重试失败：引擎未记录失败明细（parse 失败文件被跳过）→ 插件降级为全量重建
async function handleRetry() {
  saving.value = true
  try {
    await emitAction('retry')
    message.success(t('projectConfig.index_retry_started'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 清除索引：删除索引产物（状态回「未初始化」；索引配置保留）——破坏性操作，二次确认后执行
async function handleClear() {
  try {
    await confirm(t('projectConfig.index_clear_confirm'), t('projectConfig.index_clear'))
  } catch (_) {
    return // 用户取消
  }
  saving.value = true
  try {
    await emitAction('clear')
    message.success(t('projectConfig.index_clear_started'))
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
.cg-root {
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
.cg-tab-content {
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
.cg-hint {
  font-size: 12px;
  color: var(--fg-secondary);
  line-height: 1.6;
}
.cg-hint code {
  background: var(--bg-secondary, rgba(0, 0, 0, 0.05));
  padding: 0 4px;
  border-radius: 3px;
}
.cg-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}
.cg-state {
  font-size: 13px;
  color: var(--text-primary);
}
.cg-state-text {
  font-weight: 500;
}
.cg-progress {
  color: var(--text-muted);
}
.cg-phase {
  margin-left: 8px;
  font-size: 12px;
  color: var(--text-muted);
}
.cg-error {
  font-size: 12px;
  color: var(--danger, #d9534f);
  line-height: 1.6;
  word-break: break-all;
}
.cg-engine {
  margin-top: 4px;
  font-size: 13px;
  color: var(--text-primary);
}
.cg-engine-hint {
  margin-left: 8px;
  font-size: 11px;
  color: var(--text-muted);
}
.b-divider {
  border: none;
  border-top: 1px solid var(--border, #dee2e6);
  margin: 8px 0;
}
</style>
