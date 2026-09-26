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
          <div v-for="it in systemRows" :key="it.id" class="path-row">
            <label class="path-label">{{ it.name }}</label>
            <div class="path-value">
              <template v-if="it.path">
                <span class="mono">{{ it.path }}</span>
                <span v-if="it.version" class="version">{{ it.version }}</span>
              </template>
              <span v-else class="muted">{{ $t('config.page.notDetected') }}</span>
            </div>
          </div>
        </div>
      </template>

      <!-- 用户：可编辑，写 usr 库 -->
      <template #user>
        <div class="page-body">
          <div class="path-toolbar">
            <span class="hint">{{ $t('config.page.pathsUserHint') }}</span>
            <!-- ① 用户级路径改后需重启才生效（后端只对 prj 执行配置热重载，usr 路径键不重跑） -->
            <span class="save-hint">{{ $t('config.page.pathsUserRestartHint') }}</span>
            <span class="save-hint">{{ $t('config.page.blurToSave') }}</span>
            <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
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
                :placeholder="it.detected || $t('config.autoDetectPlaceholder')"
                @update:model-value="markDirty"
                @blur="saveUser(it)"
              />
              <Button size="small" v-mq:[EventNames.configPickExecutable].click="{ id: it.id }">{{ $t('common.select') }}</Button>
              <Button size="small" :disabled="!userValues[it.key]" v-mq:[EventNames.configResetKey].click="{ level: 'user', key: it.key }">{{ $t('config.page.reset') }}</Button>
            </div>
          </div>
        </div>
      </template>

      <!-- 项目：可编辑，写 prj 库（团队共享）；chromePath 不出现（两级） -->
      <template #project>
        <div class="page-body">
          <div class="path-toolbar">
            <span class="hint">{{ $t('config.page.pathsProjectHint') }}</span>
            <!-- 项目级路径为运行期热生效（prjExecConfigKeys → loadExecConfig 重跑） -->
            <span class="save-hint">{{ $t('config.page.pathsProjectInstantHint') }}</span>
            <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
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
                :placeholder="it.userValue || it.detected || $t('config.autoDetectPlaceholder')"
                @update:model-value="markDirty"
                @blur="saveProject(it)"
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
import { Tabs, Input, Button, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import {
  getUserConfig, saveUserConfig, resetUserKey,
  getAllConfig, setConfig, deleteConfig,
  detectToolchains, pickExecutable,
} from '../../api/config'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { APPLY_INSTANT, APPLY_RESTART, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'

const { t } = useI18n()

// ⑤ dirty 可视标记（仅显示，不改失焦即存的时机）
const { dirty, markDirty, markSaved } = useUnsavedMark()

// 路径项定义：三维 = 名称 / 探测 id / 配置 key；级别决定出现在哪些页签
const PATH_ITEMS = [
  { id: 'chrome', name: 'Chrome', key: 'chromePath', levels: ['system', 'user'] },
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
const userValues = ref({}) // key → value（usr）
const prjValues = ref({}) // key → value（prj）
// 各层「上次落库值」快照：仅用于判断本次失焦是否真有改动（无改动不弹成功提示，避免噪声）
const lastUser = ref({})
const lastPrj = ref({})

// 系统页签：探测结果只读
const systemRows = computed(() =>
  PATH_ITEMS.filter(it => it.levels.includes('system')).map(it => {
    const d = detected.value[it.id] || {}
    return { id: it.id, name: it.name, path: d.path || '', version: d.version || '' }
  })
)

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
    for (const tool of tools) m[tool.id] = { path: tool.path || '', version: tool.version || '' }
    detected.value = m
  } catch (e) {
    message.error(loadFailedText(t, t('config.page.systemTab'), e))
  } finally {
    detecting.value = false
  }
}

// ① 用户级路径改后需**重启**才生效（后端不重跑 usr 路径键）；无改动不弹提示（避免噪声）。
async function saveUser(it) {
  const v = userValues.value[it.key] || ''
  if (v === (lastUser.value[it.key] || '')) {
    markSaved()
    return
  }
  try {
    await saveUserConfig({ [it.key]: v })
    lastUser.value = { ...lastUser.value, [it.key]: v }
    markSaved()
    message.success(savedText(t, APPLY_RESTART))
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

// 项目级路径为运行期热生效（prjExecConfigKeys → loadExecConfig 重跑）→ 即时生效标注。
async function saveProject(it) {
  const v = prjValues.value[it.key] || ''
  if (v === (lastPrj.value[it.key] || '')) {
    markSaved()
    return
  }
  try {
    await setConfig(it.key, v)
    lastPrj.value = { ...lastPrj.value, [it.key]: v }
    markSaved()
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

// 继承控件「重置继承」：删本层 key → 回落上级/系统（仅处理本页路径 key）
async function resetKey({ level, key }) {
  const it = PATH_ITEMS.find(x => x.key === key)
  if (!it) return
  try {
    if (level === 'user') {
      await resetUserKey(key)
      userValues.value = { ...userValues.value, [key]: '' }
      lastUser.value = { ...lastUser.value, [key]: '' }
      markSaved()
      message.success(savedText(t, APPLY_RESTART))
    } else if (level === 'project') {
      await deleteConfig(key)
      prjValues.value = { ...prjValues.value, [key]: '' }
      lastPrj.value = { ...lastPrj.value, [key]: '' }
      markSaved()
      message.success(savedText(t, APPLY_INSTANT))
    }
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

// 选择可执行文件（gui.pick-executable）：写入对应编辑框所在层
async function pick({ id }) {
  const it = PATH_ITEMS.find(x => x.id === id)
  if (!it) return
  try {
    const path = await pickExecutable()
    if (!path) return
    if (activeTab.value === 'project' && it.levels.includes('project')) {
      prjValues.value = { ...prjValues.value, [it.key]: path }
      await saveProject(it)
    } else {
      userValues.value = { ...userValues.value, [it.key]: path }
      await saveUser(it)
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
.muted { font-size: 12px; color: var(--text-muted); }
.badge { font-size: 10px; padding: 1px 5px; border-radius: 3px; align-self: flex-start; }
.badge-own { background: var(--accent, #409eff); color: #fff; }
.badge-prj { background: var(--warning, #e6a23c); color: #fff; }
.badge-sys { background: var(--bg-hover, #eee); color: var(--text-muted); }
</style>
