<template>
  <div class="settings-page">
    <Tabs :tabs="tabs" v-model="activeTab" class="settings-tabs">
      <!-- 系统（资源）：探测结果只读展示，不可编辑 -->
      <template #system>
        <div class="page-body">
          <div class="path-toolbar">
            <span class="hint">{{ $t('config.page.pathsSystemHint') }}</span>
            <Button size="small" :loading="detecting" v-mq:[EventNames.configDetectToolchains].click>
              <Icon name="refresh" :size="13" /> {{ $t('config.page.redetect') }}
            </Button>
          </div>
          <div v-for="it in systemRows" :key="it.id" class="path-item">
            <div class="path-row">
              <label class="path-label">{{ it.name }}</label>
              <div class="path-value">
                <template v-if="it.path">
                  <span class="mono">{{ it.path }}</span>
                  <!-- 版本：显示**规范化**串（前端本地计算），原始探测串放 title 回显 -->
                  <span v-if="it.versionNorm" class="version" :title="it.rawVersion">{{ it.versionNorm }}</span>
                  <!-- 低于 usr 配置的最小版本 → 只提示（不阻断，前端本地判定） -->
                  <span v-if="it.belowMin" class="version-warn" :title="versionHint(it)">
                    <Icon name="warning-filled" :size="12" />
                  </span>
                </template>
                <span v-else class="muted">{{ $t('config.page.notDetected') }}</span>
                <!-- 安装引导入口（P2-3 ④）：只给下载页 / winget 命令，不下载不安装 -->
                <Button v-if="it.guide" size="small" data-install-guide @click="toggleGuide(it.id)">
                  {{ $t('config.page.installGuide') }}
                </Button>
              </div>
            </div>
            <div v-if="it.guide && guideOpenId === it.id" class="guide-panel">
              <div class="guide-row">
                <span class="guide-label">{{ $t('config.page.downloadPage') }}</span>
                <span class="mono guide-url">{{ it.guide.downloadUrl }}</span>
                <Button size="small" data-guide-open @click="openGuide(it.guide.downloadUrl)">
                  {{ $t('config.page.openDownloadPage') }}
                </Button>
              </div>
              <div v-if="it.guide.winget" class="guide-row">
                <span class="guide-label">{{ $t('config.page.wingetCommand') }}</span>
                <code class="mono guide-cmd">{{ it.guide.winget }}</code>
                <Button size="small" data-guide-copy @click="copyWinget(it.guide.winget)">
                  {{ $t('common.copy') }}
                </Button>
              </div>
              <div v-else class="guide-row muted">{{ $t('config.page.wingetUnavailable') }}</div>
              <div class="guide-row muted">{{ $t('config.page.installGuideHint') }}</div>
            </div>
          </div>
        </div>
      </template>

      <!-- 用户：可编辑，写 usr 库——编辑只改本地待保存态（显示「未保存」），点【保存】才落库 -->
      <template #user>
        <div class="page-body">
          <div class="path-toolbar">
            <span class="hint">{{ $t('config.page.pathsUserHint') }}</span>
            <!-- ① 用户级路径改后需重启才生效（后端只对 prj 执行配置热重载，usr 路径键不重跑） -->
            <span class="save-hint">{{ $t('config.page.pathsUserRestartHint') }}</span>
            <span v-if="userDirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
            <Button size="small" type="primary" :disabled="!userDirty" :loading="savingUser" data-paths-save-user @click="saveUserTab">{{ $t('common.save') }}</Button>
          </div>
          <div v-for="it in userRows" :key="it.id" class="path-row">
            <label class="path-label">
              {{ it.name }}
              <span v-if="it.overriddenBy" class="badge badge-prj">{{ $t('config.page.overriddenByProject') }}</span>
              <span v-else-if="userValues[it.key]" class="badge badge-own">{{ $t('config.page.sourceUser') }}</span>
              <span v-else-if="it.detected" class="badge badge-sys">{{ $t('config.page.sourceSystem') }}</span>
            </label>
            <div class="path-input">
              <Input
                v-model="userValues[it.key]"
                :maxlength="MAX_PATH"
                :placeholder="it.detected || $t('config.autoDetectPlaceholder')"
                @update:model-value="refreshUserDirty"
              />
              <Button size="small" v-mq:[EventNames.configPickExecutable].click="{ id: it.id }">{{ $t('common.select') }}</Button>
              <Button size="small" :disabled="!userValues[it.key]" v-mq:[EventNames.configResetKey].click="{ level: 'user', key: it.key }">{{ $t('config.page.reset') }}</Button>
            </div>
          </div>
        </div>
      </template>

      <!-- 项目：可编辑，写 prj 库（团队共享）——显式【保存】才落库 -->
      <template #project>
        <div class="page-body">
          <div class="path-toolbar">
            <span class="hint">{{ $t('config.page.pathsProjectHint') }}</span>
            <!-- 项目级路径为运行期热生效（prjExecConfigKeys → loadExecConfig 重跑） -->
            <span class="save-hint">{{ $t('config.page.pathsProjectInstantHint') }}</span>
            <span v-if="prjDirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
            <Button size="small" type="primary" :disabled="!prjDirty" :loading="savingPrj" data-paths-save-project @click="saveProjectTab">{{ $t('common.save') }}</Button>
          </div>
          <div v-for="it in projectRows" :key="it.id" class="path-row">
            <label class="path-label">
              {{ it.name }}
              <span v-if="prjValues[it.key]" class="badge badge-own">{{ $t('config.page.sourceProject') }}</span>
              <span v-else class="badge badge-sys">{{ it.userValue ? $t('config.page.inheritUser') : $t('config.page.inheritSystem') }}</span>
            </label>
            <div class="path-input">
              <Input
                v-model="prjValues[it.key]"
                :maxlength="MAX_PATH"
                :placeholder="it.userValue || it.detected || $t('config.autoDetectPlaceholder')"
                @update:model-value="refreshPrjDirty"
              />
              <Button size="small" v-mq:[EventNames.configPickExecutable].click="{ id: it.id }">{{ $t('common.select') }}</Button>
              <Button size="small" :disabled="!prjValues[it.key]" v-mq:[EventNames.configResetKey].click="{ level: 'project', key: it.key }">{{ $t('config.page.reset') }}</Button>
            </div>
          </div>
        </div>
      </template>
    </Tabs>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Tabs, Input, Button, message } from '@ckfw/components/ui/index.js'
// INPUTVAL-1：路径 ≤512 字符、绝对路径（空 = 未配置，允许）。
import { MAX_PATH, isAbsolutePath, trimStr } from '../../utils/fieldRules'
import Icon from '@ckfw/components/icon/Icon.vue'
import {
  getUserConfig, saveUserConfig, resetUserKey,
  getAllConfig, setConfig, deleteConfig,
  detectToolchains, pickExecutable,
} from '../../api/config'
import mq from '@ckfw/utils/mq.js'
import { guideFor, versionBadge, openGuidePage, copyGuideCommand } from '../../utils/toolchainGuide'
import { EventNames } from '../../events/event-names'
import { APPLY_INSTANT, APPLY_RESTART, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'

const { t } = useI18n()

// ⑤ 显式保存（2026-10-06 统一口径）：编辑只改本地待保存态（按页签显示「未保存」），
// 点该页签右上角【保存】才落库；无改动时保存按钮禁用。dirty 由本地态 vs 已保存快照比对得出。
const { dirty: userDirty, markDirty: markUserDirty, markSaved: markUserSaved } = useUnsavedMark()
const { dirty: prjDirty, markDirty: markPrjDirty, markSaved: markPrjSaved } = useUnsavedMark()
const savingUser = ref(false)
const savingPrj = ref(false)

// 路径项定义：三维 = 名称 / 探测 id / 配置 key；级别决定出现在哪些页签
const PATH_ITEMS = [
  { id: 'chrome', name: 'Chrome', key: 'chromePath', levels: ['system', 'user', 'project'] },
  { id: 'java', name: 'Java', key: 'javaPath', levels: ['system', 'user', 'project'] },
  { id: 'python', name: 'Python', key: 'pythonPath', levels: ['system', 'user', 'project'] },
  { id: 'node', name: 'Node.js', key: 'nodePath', levels: ['system', 'user', 'project'] },
  { id: 'go', name: 'Go', key: 'goPath', levels: ['system', 'user', 'project'] },
  { id: 'rust', name: 'Rust', key: 'rustPath', levels: ['system', 'user', 'project'] },
  { id: 'c', name: 'C/C++', key: 'cCompilerPath', levels: ['system', 'user', 'project'] },
]

const activeTab = ref('user')
const tabs = computed(() => [
  { label: t('config.page.systemTab'), name: 'system' },
  { label: t('config.page.userTab'), name: 'user' },
  { label: t('config.page.projectTab'), name: 'project' },
])

const detecting = ref(false)
const detected = ref({}) // id → {path, version}
const userCfg = ref({}) // usr 配置快照（完整视图：路径键 + toolchain.<id>.minVersion）
const userValues = ref({}) // key → value（usr）
const prjValues = ref({}) // key → value（prj）
// 各层「上次落库值」快照：用于「未保存」判定（本地态 ≠ 快照 → 有改动 → 保存按钮可用）
const lastUser = ref({})
const lastPrj = ref({})

// refreshUserDirty / refreshPrjDirty：由「本地态 vs 已保存快照」重算 dirty（无 watch）；
// 编辑 / 选择 / 重置 / 保存后显式调用。
function refreshUserDirty() {
  const changed = PATH_ITEMS.some(it =>
    (userValues.value[it.key] || '') !== (lastUser.value[it.key] || ''))
  changed ? markUserDirty() : markUserSaved()
}
function refreshPrjDirty() {
  const changed = PATH_ITEMS.some(it =>
    (prjValues.value[it.key] || '') !== (lastPrj.value[it.key] || ''))
  changed ? markPrjDirty() : markPrjSaved()
}

// 系统页签：探测结果只读（版本显示规范化串；低于最小版本只提示；每项带安装引导入口）。
// 规范化 / 下限判定**全在前端**（`toolchainGuide.versionBadge`）：探测载荷只有
// `{id, name, path, version}`（61 §1 原样），最小版本取 usr 配置快照的自由键。
const systemRows = computed(() =>
  PATH_ITEMS.filter(it => it.levels.includes('system')).map(it => {
    const d = detected.value[it.id] || {}
    const raw = typeof d.version === 'string' ? d.version : ''
    const minVersion = userCfg.value['toolchain.' + it.id + '.minVersion'] || ''
    const badge = versionBadge(raw, minVersion)
    return {
      id: it.id,
      name: it.name,
      path: d.path || '',
      versionNorm: d.path ? badge.norm : '',
      rawVersion: raw,
      minVersion,
      belowMin: badge.belowMin,
      guide: guideFor(it.id),
    }
  })
)

// 安装引导（P2-3 ④）：同一时刻只展开一项；只给下载页 / winget 命令，**不下载不安装**
const guideOpenId = ref('')
function toggleGuide(id) {
  guideOpenId.value = guideOpenId.value === id ? '' : id
}
function versionHint(it) {
  return t('config.page.versionBelowMin', { min: it.minVersion, cur: it.versionNorm })
}
function openGuide(url) {
  openGuidePage(url)
}
async function copyWinget(cmd) {
  try {
    await copyGuideCommand(cmd)
    message.success(t('config.page.wingetCopied'))
  } catch (e) {
    message.error(t('config.page.wingetCopyFailed'))
  }
}

// 用户页签：可编辑（含 chrome）
const userRows = computed(() =>
  PATH_ITEMS.filter(it => it.levels.includes('user')).map(it => ({
    id: it.id,
    key: it.key,
    name: it.name,
    value: userValues.value[it.key] || '',
    detected: (detected.value[it.id] || {}).path || '',
    overriddenBy: !!(prjValues.value[it.key]),
  }))
)

// 项目页签：可编辑（不含 chrome）
const projectRows = computed(() =>
  PATH_ITEMS.filter(it => it.levels.includes('project')).map(it => ({
    id: it.id,
    key: it.key,
    name: it.name,
    value: prjValues.value[it.key] || '',
    userValue: userValues.value[it.key] || '',
    detected: (detected.value[it.id] || {}).path || '',
  }))
)

async function loadUser() {
  try {
    const res = await getUserConfig()
    const uc = res.config || res
    userCfg.value = uc || {}
    const m = {}
    for (const it of PATH_ITEMS) m[it.key] = uc[it.key] || ''
    userValues.value = m
    lastUser.value = { ...m }
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('config.page.userTab'), e))
  }
}

async function loadProject() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    const m = {}
    for (const it of PATH_ITEMS) m[it.key] = c[it.key] || ''
    prjValues.value = m
    lastPrj.value = { ...m }
  } catch (e) {
    message.error(loadFailedText(t, t('config.page.projectTab'), e))
  }
}

async function detect() {
  detecting.value = true
  try {
    const tools = await detectToolchains()
    const m = {}
    // 探测项 = {id, name, path, version}（61 §1 原样）；版本规范化 / 下限判定由本页本地算
    for (const tool of tools) if (tool && tool.id) m[tool.id] = tool
    detected.value = m
  } catch (e) {
    message.error(loadFailedText(t, t('config.page.systemTab'), e))
  } finally {
    detecting.value = false
  }
}

// ① 用户级路径：**显式保存**（页签右上角【保存】）→ 收集全部改动为**单个对象一次批量写**
// （1 次 saveUserConfig，替代逐键 N 次往返）；改后需**重启**才生效（后端不重跑 usr 路径键）
// → 成功反馈按 APPLY_RESTART 标注。
async function saveUserTab() {
  if (!userDirty.value || savingUser.value) return
  savingUser.value = true
  try {
    const patch = {}
    for (const it of PATH_ITEMS) {
      if (!it.levels.includes('user')) continue
      const v = trimStr(userValues.value[it.key])
      if (v && !isAbsolutePath(v)) {
        message.warning(t('fieldRules.invalidPath', { field: it.name }))
        return
      }
      if (v === (lastUser.value[it.key] || '')) continue
      patch[it.key] = v
    }
    if (Object.keys(patch).length > 0) {
      await saveUserConfig(patch)
      lastUser.value = { ...lastUser.value, ...patch }
      message.success(savedText(t, APPLY_RESTART))
    }
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    savingUser.value = false
    refreshUserDirty()
  }
}

// 项目级路径：项目级为运行期热生效（prjExecConfigKeys → loadExecConfig 重跑）
// → 成功反馈按 APPLY_INSTANT 标注。
async function saveProjectTab() {
  if (!prjDirty.value || savingPrj.value) return
  savingPrj.value = true
  let saved = 0
  try {
    for (const it of PATH_ITEMS) {
      if (!it.levels.includes('project')) continue
      const v = trimStr(prjValues.value[it.key])
      if (v && !isAbsolutePath(v)) {
        message.warning(t('fieldRules.invalidPath', { field: it.name }))
        return
      }
      if (v === (lastPrj.value[it.key] || '')) continue
      await setConfig(it.key, v)
      lastPrj.value = { ...lastPrj.value, [it.key]: v }
      saved++
    }
    if (saved > 0) message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    savingPrj.value = false
    refreshPrjDirty()
  }
}

// 继承控件「重置继承」：删本层 key → 回落上级/系统（仅处理本页路径 key）。
// 为显式动作（即时落库，不进待保存态）；完成后重算 dirty（其它未保存编辑不受影响）。
async function resetKey({ level, key }) {
  const it = PATH_ITEMS.find(x => x.key === key)
  if (!it) return
  try {
    if (level === 'user') {
      await resetUserKey(key)
      userValues.value = { ...userValues.value, [key]: '' }
      lastUser.value = { ...lastUser.value, [key]: '' }
      refreshUserDirty()
      message.success(savedText(t, APPLY_RESTART))
    } else if (level === 'project') {
      await deleteConfig(key)
      prjValues.value = { ...prjValues.value, [key]: '' }
      lastPrj.value = { ...lastPrj.value, [key]: '' }
      refreshPrjDirty()
      message.success(savedText(t, APPLY_INSTANT))
    }
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

// 选择可执行文件（gui.pick-executable）：写入对应编辑框所在层（只改本地待保存态，点【保存】才落库）
async function pick({ id }) {
  const it = PATH_ITEMS.find(x => x.id === id)
  if (!it) return
  try {
    const path = await pickExecutable()
    if (!path) return
    if (activeTab.value === 'project' && it.levels.includes('project')) {
      prjValues.value = { ...prjValues.value, [it.key]: path }
      refreshPrjDirty()
    } else {
      userValues.value = { ...userValues.value, [it.key]: path }
      refreshUserDirty()
    }
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

const unsubs = []
onMounted(() => {
  loadUser()
  loadProject()
  detect()
  unsubs.push(mq.on(EventNames.configPickExecutable, pick))
  unsubs.push(mq.on(EventNames.configResetKey, resetKey))
  unsubs.push(mq.on(EventNames.configDetectToolchains, detect))
})
onUnmounted(() => unsubs.forEach(fn => fn()))
</script>

<style scoped>
.settings-page { height: 100%; min-height: 0; display: flex; flex-direction: column; overflow: hidden; }
.settings-tabs { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.settings-tabs :deep(.b-tabs-body) { flex: 1; overflow-y: auto; min-height: 0; padding: 8px 0; }
.page-body { display: flex; flex-direction: column; gap: 10px; }
.path-toolbar { display: flex; align-items: center; gap: 12px; }
.hint { font-size: 12px; color: var(--text-muted); flex: 1; }
.save-hint { font-size: 12px; color: var(--text-muted); }
/* ⑤ dirty 标记（仅显示） */
.unsaved-mark { font-size: 12px; color: var(--warning, #e6a23c); white-space: nowrap; }
.path-row { display: flex; align-items: flex-start; gap: 10px; }
.path-label {
  width: 150px;
  flex-shrink: 0;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
  padding-top: 6px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.path-input { flex: 1; display: flex; align-items: center; gap: 6px; }
.path-value { flex: 1; display: flex; align-items: baseline; gap: 10px; padding-top: 6px; }
.mono { font-family: var(--font-mono, Consolas, monospace); font-size: 12px; color: var(--text-primary); word-break: break-all; }
.version { font-size: 11px; color: var(--text-muted); white-space: nowrap; }
/* 低于最小版本：只提示（不阻断） */
.version-warn { display: inline-flex; align-items: center; color: var(--warning, #e6a23c); cursor: help; }
.muted { font-size: 12px; color: var(--text-muted); }
/* 安装引导（P2-3 ④）：静态信息 + 复制命令 / 打开官方下载页，无下载/安装动作 */
.path-item { display: flex; flex-direction: column; gap: 4px; }
.guide-panel {
  margin-left: 160px;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-hover, #fafafa);
}
.guide-row { display: flex; align-items: center; gap: 8px; font-size: 12px; }
.guide-label { color: var(--text-muted); flex-shrink: 0; width: 96px; }
.guide-url { word-break: break-all; }
.guide-cmd { background: var(--bg-code, #f5f5f5); padding: 1px 6px; border-radius: 3px; }
.badge { font-size: 10px; padding: 1px 5px; border-radius: 3px; align-self: flex-start; }
.badge-own { background: var(--accent, #409eff); color: #fff; }
.badge-prj { background: var(--warning, #e6a23c); color: #fff; }
.badge-sys { background: var(--bg-hover, #eee); color: var(--text-muted); }
</style>
