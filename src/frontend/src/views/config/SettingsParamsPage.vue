<template>
  <div class="settings-page">
    <Tabs :tabs="tabs" v-model="activeTab" class="settings-tabs">
      <!-- 系统（资源）：只读默认值，不落库 -->
      <template #system>
        <div class="page-body">
          <p class="hint">{{ $t('config.page.paramsSystemHint') }}</p>
          <div v-for="f in systemRows" :key="f.key" class="param-row">
            <label class="param-label">{{ f.label }}</label>
            <span class="param-value mono">{{ f.value }}</span>
          </div>
        </div>
      </template>

      <!-- 用户：超时重试（usr 库）——编辑只改本地待保存态（显示「未保存」），点【保存】才落库 -->
      <template #user>
        <div class="page-body">
          <div class="tab-toolbar">
            <p class="hint">{{ $t('config.page.paramsUserHint') }}</p>
            <span v-if="userDirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
            <Button size="small" type="primary" :disabled="!userDirty" :loading="savingUser" data-params-save-user @click="saveUserTab">{{ $t('common.save') }}</Button>
          </div>
          <div v-for="f in timeoutFields" :key="f.key" class="param-row">
            <label class="param-label">{{ f.label }}</label>
            <div class="param-input">
              <!-- C 数值项：前置校验（非法值不写库并内联报错） -->
              <Input
                type="number"
                v-model.number="userValues[f.key]"
                :min="f.min" :max="f.max" :step="f.step"
                :error="!!userErrors[f.key]"
                @update:model-value="refreshUserDirty"
              />
              <span class="unit">{{ f.unit }}</span>
              <span v-if="userErrors[f.key]" class="param-error">{{ userErrors[f.key] }}</span>
            </div>
            <Button size="small" :disabled="!userOverridden(f.key)" v-mq:[EventNames.configResetKey].click="{ level: 'user', key: f.key }">{{ $t('config.page.reset') }}</Button>
          </div>
        </div>
      </template>

      <!-- 项目：服务参数（prj 库）——上下文压缩两项已收敛至「上下文管理」页（去重）；
           编辑只改本地待保存态（显示「未保存」），点【保存】才落库 -->
      <template #project>
        <div class="page-body">
          <div class="tab-toolbar">
            <p class="hint">{{ $t('config.page.paramsProjectHint') }}</p>
            <span v-if="prjDirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
            <Button size="small" type="primary" :disabled="!prjDirty" :loading="savingPrj" data-params-save-project @click="saveProjectTab">{{ $t('common.save') }}</Button>
          </div>
          <div v-for="f in serviceFields" :key="f.key" class="param-row">
            <label class="param-label">{{ f.label }}</label>
            <div class="param-input">
              <!-- ③ 数值项：type=number + 前置校验（非法值不写库并内联报错） -->
              <Input
                :type="f.int ? 'number' : 'text'"
                v-model="prjValues[f.key]"
                :placeholder="f.hint"
                :error="!!prjErrors[f.key]"
                @update:model-value="refreshPrjDirty"
              />
              <span v-if="prjErrors[f.key]" class="param-error">{{ prjErrors[f.key] }}</span>
            </div>
            <Button size="small" :disabled="!hasPrj(f.key)" v-mq:[EventNames.configResetKey].click="{ level: 'project', key: f.key }">{{ $t('config.page.reset') }}</Button>
          </div>
        </div>
      </template>
    </Tabs>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Tabs, Input, Button, message } from '../../components/ui'
import {
  getUserConfig, saveUserConfig, resetUserKey,
  getAllConfig, setConfig, deleteConfig,
} from '../../api/config'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { validatePositiveInt, positiveIntErrorText, validateNonNegativeInt, nonNegativeIntErrorText } from '../../utils/settingsValidation'
import { useUnsavedMark } from '../../composables/useUnsavedMark'

const { t } = useI18n()

// ⑤ 显式保存（2026-10-06 统一口径）：编辑只改本地待保存态（按页签显示「未保存」），
// 点该页签右上角【保存】才落库；无改动时保存按钮禁用。dirty 由本地态 vs 已保存快照比对得出。
const { dirty: userDirty, markDirty: markUserDirty, markSaved: markUserSaved } = useUnsavedMark()
const { dirty: prjDirty, markDirty: markPrjDirty, markSaved: markPrjSaved } = useUnsavedMark()
const savingUser = ref(false)
const savingPrj = ref(false)

const activeTab = ref('user')
const tabs = computed(() => [
  { label: t('config.page.systemTab'), name: 'system' },
  { label: t('config.page.userTab'), name: 'user' },
  { label: t('config.page.projectTab'), name: 'project' },
])

// 超时重试（系统/用户两级）；allowZero = 该项显式 0 合法（仅 retryCount，见后端 loadLLMRuntimeConfig
// 的 n>=0 口径；其余两项后端口径为 n>0，0 会被静默回落默认）
// 注：重试退避间隔不自持（经 router.RetryWait：Retry-After 优先 + 指数退避），故无 retryDelay 项。
const timeoutFields = computed(() => [
  { key: 'responseTimeout', label: t('config.responseTimeout'), min: 0, max: 600, step: 30, unit: 's' },
  { key: 'streamTimeout', label: t('config.streamTimeout'), min: 0, max: 600, step: 30, unit: 's' },
  { key: 'retryCount', label: t('config.retryCount'), min: 0, max: 10, step: 1, unit: '', allowZero: true },
])

// 系统默认值表（代码常量，不落库；「系统」页签展示 + 各输入框 placeholder 与「重置」判定共用一份）。
// 【前端镜像，须与后端常量同步】后端权威源（变更任一项必须同步本表 + 对应断言测试）：
//   - responseTimeout/streamTimeout/retryCount →
//     chonkpilot-data/internal/config/userconfig.go userConfigSystemDefaults（120/60/2）
//   - timeout_sec/max_concurrency/skip_dirs →
//     chonkpilot-mcp-server/server/config.go DefaultConfig()（300/16/12 项跳过目录；与
//     chonkpilot-mcp-tools/internal/fileops/fileops.go SkipDirs 同集）
// 守护测试：chonkpilot-llm/server TestSystemDefaultParamsMirror。
// 注：keep_full_max_turns / keep_full_max_tokens / compress_token_threshold 的唯一编辑入口已收敛至
// 「上下文管理」页（ContextConfig.vue），本页不再镜像；其后端默认值（10 / 24000 / 20000）仍由上用例守护。
const SYSTEM_DEFAULTS = Object.freeze({
  responseTimeout: '120',
  streamTimeout: '60',
  retryCount: '2',
  timeout_sec: '300',
  max_concurrency: '16',
  skip_dirs: '.git, .svn, node_modules, .trae, .chonkpilot, __pycache__, .venv, venv, build, dist, .next, .nuxt',
})

// 服务参数（系统默认 + 项目级）；int = 须为「大于 0 的整数」（后端 Atoi + n>0 口径，见 settingsValidation）
const serviceFields = computed(() => [
  { key: 'timeout_sec', label: t('config.page.timeoutSec'), hint: SYSTEM_DEFAULTS.timeout_sec, int: true },
  { key: 'max_concurrency', label: t('config.page.maxConcurrency'), hint: SYSTEM_DEFAULTS.max_concurrency, int: true },
  { key: 'skip_dirs', label: t('config.page.skipDirs'), hint: SYSTEM_DEFAULTS.skip_dirs, int: false },
])

// 系统默认（只读展示，不落库）：逐项取自 SYSTEM_DEFAULTS（与后端常量同源镜像）
const systemRows = computed(() => [
  ...timeoutFields.value.map(f => ({ key: f.key, label: f.label, value: SYSTEM_DEFAULTS[f.key] })),
  ...serviceFields.value.map(f => ({ key: f.key, label: f.label, value: SYSTEM_DEFAULTS[f.key] })),
])

const userValues = ref({})
const prjValues = ref({})
// ③ 项目级数值项的校验错误（key → 文案）；非空 = 该输入非法，且**未写库**
const prjErrors = ref({})
// C 用户级数值项的校验错误（key → 文案）；非空 = 该输入非法，且**未写库**
const userErrors = ref({})
// 各层「上次落库/加载值」快照：用于「未保存」判定（本地态 ≠ 快照 → 有改动 → 保存按钮可用）
const lastUserVals = ref({})
const lastServiceVals = ref({})
// prjStored：后端 prj 库是否已有该键（「重置」可用性判定，与 UI 显示值解耦）。
// 必要性：skip_dirs 存空数组时回读显示为空串，但键**确实存在**（"[]"）→ 仍应可重置。
const prjStored = ref({})

function hasPrj(key) {
  if (prjStored.value[key]) return true
  const v = prjValues.value[key]
  return v !== undefined && v !== null && v !== ''
}

// userOverridden：用户级「重置」可用性。usr 读取端会把缺失键补成系统默认（无法区分"未设"与
// "设成默认值"），故按"当前有效值 ≠ 系统默认"判定是否确有覆盖；无覆盖 → 重置无意义（禁用）。
function userOverridden(key) {
  const v = Number(userValues.value[key])
  return Number.isFinite(v) && v !== Number(SYSTEM_DEFAULTS[key])
}

// refreshUserDirty / refreshPrjDirty：由「本地态 vs 已保存快照」重算 dirty（无 watch）；
// 编辑 / 重置 / 保存后显式调用。
function refreshUserDirty() {
  const changed = timeoutFields.value.some(f =>
    String(userValues.value[f.key] ?? '') !== String(lastUserVals.value[f.key] ?? ''))
  changed ? markUserDirty() : markUserSaved()
}
function refreshPrjDirty() {
  const changed = serviceFields.value.some(f =>
    String(prjValues.value[f.key] ?? '') !== String(lastServiceVals.value[f.key] ?? ''))
  changed ? markPrjDirty() : markPrjSaved()
}

async function loadUser() {
  try {
    const res = await getUserConfig()
    const uc = res.config || res
    const m = {}
    for (const f of timeoutFields.value) m[f.key] = uc[f.key] ?? 0
    userValues.value = m
    lastUserVals.value = { ...m }
    userErrors.value = {}
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('config.page.userTab'), e))
  }
}

async function loadProject() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    const has = (k) => c[k] !== undefined && c[k] !== null && c[k] !== ''
    prjStored.value = {
      timeout_sec: has('timeout_sec'),
      max_concurrency: has('max_concurrency'),
      skip_dirs: has('skip_dirs'),
    }
    prjValues.value = {
      timeout_sec: c.timeout_sec || '',
      max_concurrency: c.max_concurrency || '',
      skip_dirs: parseSkipDirs(c.skip_dirs),
    }
    lastServiceVals.value = { ...prjValues.value }
    prjErrors.value = {}
  } catch (e) {
    message.error(loadFailedText(t, t('config.page.projectTab'), e))
  }
}

// skip_dirs 存 JSON 数组字符串（12-数据层），UI 用逗号分隔
function parseSkipDirs(raw) {
  if (!raw) return ''
  try {
    const arr = JSON.parse(raw)
    return Array.isArray(arr) ? arr.join(', ') : String(raw)
  } catch (_) {
    return String(raw)
  }
}

function toSkipDirs(v) {
  const arr = String(v || '').split(',').map(s => s.trim()).filter(Boolean)
  return JSON.stringify(arr)
}

// 用户级超时/重试：**显式保存**（页签右上角【保存】）逐项落库；后端每轮读取
// （loadLLMRuntimeConfig）→ 保存即生效（无需重启）。
// C 前置校验：整数；retryCount >= 0，其余 > 0（对齐后端 n>=0 / n>0 口径）；非法值**不写库** +
// 内联报错 + 明确提示；清空 = 删键回落系统默认（与「重置」同口径，**不视为错误**）。
async function saveUserTab() {
  if (!userDirty.value || savingUser.value) return
  savingUser.value = true
  let saved = 0
  let failed = false
  try {
    for (const f of timeoutFields.value) {
      const r = await commitUser(f)
      if (r === 'saved') saved++
      else if (r === 'failed') failed = true
    }
    // 单项非法 / 失败已各自给出可见提示 → 仅在有实际写入且无失败时给一次成功反馈
    if (saved > 0 && !failed) message.success(savedText(t, APPLY_INSTANT))
  } finally {
    savingUser.value = false
    refreshUserDirty()
  }
}

// commitUser：落库单个 usr 项。返回 'saved'（已写库）/ 'unchanged'（无改动）/ 'invalid'（非法）/
// 'failed'（写库失败）。不含成功 toast（由 saveUserTab 统一给一次）。
async function commitUser(f) {
  const key = f.key
  const rawStr = String(userValues.value[key] ?? '').trim()
  const def = Number(SYSTEM_DEFAULTS[key])
  if (rawStr === '') {
    // 清空 = 删键回落默认；当前已为默认值 → 无键可删，仅归位显示
    if (Number(lastUserVals.value[key]) === def) {
      userValues.value = { ...userValues.value, [key]: def }
      userErrors.value = { ...userErrors.value, [key]: '' }
      return 'unchanged'
    }
    try {
      await resetUserKey(key)
    } catch (e) {
      message.error(saveFailedText(t, e))
      return 'failed'
    }
    userValues.value = { ...userValues.value, [key]: def }
    lastUserVals.value = { ...lastUserVals.value, [key]: def }
    userErrors.value = { ...userErrors.value, [key]: '' }
    return 'saved'
  }
  const r = f.allowZero ? validateNonNegativeInt(rawStr) : validatePositiveInt(rawStr)
  if (!r.ok) {
    const text = f.allowZero ? nonNegativeIntErrorText(t, r.reason) : positiveIntErrorText(t, r.reason)
    userErrors.value = { ...userErrors.value, [key]: text }
    message.error(text)
    return 'invalid'
  }
  if (String(r.value) === String(lastUserVals.value[key])) {
    userValues.value = { ...userValues.value, [key]: r.value }
    userErrors.value = { ...userErrors.value, [key]: '' }
    return 'unchanged'
  }
  try {
    await saveUserConfig({ [key]: r.value })
  } catch (e) {
    message.error(saveFailedText(t, e))
    return 'failed'
  }
  userValues.value = { ...userValues.value, [key]: r.value }
  lastUserVals.value = { ...lastUserVals.value, [key]: r.value }
  userErrors.value = { ...userErrors.value, [key]: '' }
  return 'saved'
}

// 项目级服务参数：**显式保存**（页签右上角【保存】）逐项落库。
async function saveProjectTab() {
  if (!prjDirty.value || savingPrj.value) return
  savingPrj.value = true
  let saved = 0
  let failed = false
  try {
    for (const f of serviceFields.value) {
      const r = await commitProject(f)
      if (r === 'saved') saved++
      else if (r === 'failed') failed = true
    }
    if (saved > 0 && !failed) message.success(savedText(t, APPLY_INSTANT)) // 执行配置热重载（prjExecConfigKeys）
  } finally {
    savingPrj.value = false
    refreshPrjDirty()
  }
}

// commitProject：落库单个 prj 项。返回 'saved' / 'unchanged' / 'invalid' / 'failed'。
async function commitProject(f) {
  const key = f.key
  // ③ 数值项前置校验：非法（非整数 / ≤0）→ 内联报错 + **不写库**（后端会静默回落默认）
  if (f.int) {
    const rawStr = String(prjValues.value[key] ?? '').trim()
    if (rawStr === '') {
      // 清空 = 回落系统默认（删项目级键），与「重置」同口径；非错误
      if (String(lastServiceVals.value[key] || '') === '') return 'unchanged'
      try {
        await deleteConfig(key)
      } catch (e) {
        message.error(saveFailedText(t, e))
        return 'failed'
      }
      prjValues.value = { ...prjValues.value, [key]: undefined }
      prjStored.value = { ...prjStored.value, [key]: false }
      lastServiceVals.value = { ...lastServiceVals.value, [key]: '' }
      prjErrors.value = { ...prjErrors.value, [key]: '' }
      return 'saved'
    }
    const r = validatePositiveInt(rawStr)
    if (!r.ok) {
      const text = positiveIntErrorText(t, r.reason)
      prjErrors.value = { ...prjErrors.value, [key]: text }
      message.error(text)
      return 'invalid'
    }
    if (String(r.value) === String(lastServiceVals.value[key])) {
      prjValues.value = { ...prjValues.value, [key]: String(r.value) }
      prjErrors.value = { ...prjErrors.value, [key]: '' }
      return 'unchanged'
    }
    try {
      await setConfig(key, String(r.value))
    } catch (e) {
      message.error(saveFailedText(t, e))
      return 'failed'
    }
    prjValues.value = { ...prjValues.value, [key]: String(r.value) }
    prjStored.value = { ...prjStored.value, [key]: true }
    lastServiceVals.value = { ...lastServiceVals.value, [key]: String(r.value) }
    prjErrors.value = { ...prjErrors.value, [key]: '' }
    return 'saved'
  }
  // 文本项（skip_dirs）
  const raw = prjValues.value[key]
  if (String(raw ?? '') === String(lastServiceVals.value[key] ?? '')) return 'unchanged'
  try {
    const v = key === 'skip_dirs' ? toSkipDirs(raw) : String(raw ?? '')
    await setConfig(key, v)
  } catch (e) {
    message.error(saveFailedText(t, e))
    return 'failed'
  }
  prjStored.value = { ...prjStored.value, [key]: true } // 已落库 → 「重置」可用
  lastServiceVals.value = { ...lastServiceVals.value, [key]: String(raw ?? '') }
  return 'saved'
}

// 「重置」= 删本层键回落系统默认（显式动作，即时落库）；完成后重算 dirty（其它未保存编辑不受影响）。
async function resetKey({ level, key }) {
  if (!OWN_KEYS.value.has(key)) return
  try {
    if (level === 'user') {
      await resetUserKey(key)
      // 回落系统默认值（非 0：如 responseTimeout 默认 120s）
      const def = Number(SYSTEM_DEFAULTS[key])
      userValues.value = { ...userValues.value, [key]: def }
      lastUserVals.value = { ...lastUserVals.value, [key]: def }
      userErrors.value = { ...userErrors.value, [key]: '' }
      refreshUserDirty()
      message.success(savedText(t, APPLY_INSTANT))
    } else if (level === 'project') {
      await deleteConfig(key)
      prjValues.value = { ...prjValues.value, [key]: undefined }
      lastServiceVals.value = { ...lastServiceVals.value, [key]: '' }
      prjErrors.value = { ...prjErrors.value, [key]: '' }
      prjStored.value = { ...prjStored.value, [key]: false }
      refreshPrjDirty()
      message.success(savedText(t, APPLY_INSTANT))
    }
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

const unsubs = []
onMounted(() => {
  loadUser()
  loadProject()
  unsubs.push(mq.on(EventNames.configResetKey, resetKey))
})
onUnmounted(() => unsubs.forEach(fn => fn()))
</script>

<style scoped>
.settings-page { height: 100%; min-height: 0; display: flex; flex-direction: column; overflow: hidden; }
.settings-tabs { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.settings-tabs :deep(.b-tabs-body) { flex: 1; overflow-y: auto; min-height: 0; padding: 8px 0; }
.page-body { display: flex; flex-direction: column; gap: 10px; }
/* 页签内工具条：说明（左）+「未保存」标记 +【保存】（右上角） */
.tab-toolbar { display: flex; align-items: center; gap: 8px; }
.hint { flex: 1; font-size: 12px; color: var(--text-muted); margin: 0; }
/* ⑤ dirty 标记 / ③ 校验错误（仅显示） */
.unsaved-mark { color: var(--warning, #e6a23c); }
.param-error { font-size: 12px; color: var(--danger, #dc3545); }
.param-row { display: flex; align-items: center; gap: 10px; }
.param-label { width: 200px; flex-shrink: 0; font-size: 13px; font-weight: 500; color: var(--text-primary); }
.param-input { flex: 1; display: flex; align-items: center; gap: 6px; max-width: 420px; }
.param-value { flex: 1; }
.mono { font-family: var(--font-mono, Consolas, monospace); font-size: 12px; }
.unit { font-size: 12px; color: var(--text-muted); }
</style>
