<!--
  配置 导入 / 导出 + 恢复出厂（用户视角缺陷批 3 · ⑯；项目配置页 → 「配置导入/导出」页签）

  覆盖三件事（**零新增数据主题**，只新增一个 native 能力 gui.file.save）：
    ① 导出：usr 主库视图（`data-user-config-list`）→ 快照 JSON（含 app/schema/exportedAt 信封）
       → `gui.file.save`（系统「另存为」；browser 形态降级为浏览器下载 + 剪贴板复制）；
       含显著密钥警示 + 「排除密钥」选项（**默认包含**）。
    ② 导入：选择 JSON 文件 / 粘贴 JSON → 结构校验（JSON 可解析 + 键白名单，未知键忽略并计数）
       → 确认弹窗写明「按键覆盖」的影响范围（键名 + 数量）→ 自动备份 → `data-user-config-save`。
    ③ 恢复出厂：**二次确认** → 自动备份 → `data-user-config-delete`（不带 key = 清空整份 usr 配置）。

  安全：快照内容（可能含 API Key）**不进任何日志/console**；备份走 `gui.file.save` mode=backup
  （落 `<dataDir>/backup/`），提示里给出备份路径供用户回退。
  刷新：订阅既有 `data-user-config-refresh`（禁 watch/watchEffect）；需重启键按批 2
  `APPLY_RESTART` 口径标注。
-->
<template>
  <div class="config-io-root">
    <!-- ① 导出 -->
    <section class="io-section io-export">
      <h4 class="io-title">{{ $t('configIO.exportTitle') }}</h4>
      <p class="io-hint">{{ $t('configIO.exportScopeHint') }}</p>
      <p class="io-alert io-secret-warn">{{ $t('configIO.exportSecretWarn') }}</p>
      <label class="io-switch-row io-exclude-switch">
        <Switch v-model="excludeSecrets" />
        <span class="io-switch-label">{{ $t('configIO.excludeSecrets') }}</span>
        <span class="io-hint io-switch-hint">{{ $t('configIO.excludeSecretsHint') }}</span>
      </label>
      <p class="io-hint">{{ currentSummary }}</p>
      <div class="io-actions">
        <Button type="primary" :loading="busy.export" @click="onExportFile">{{ $t('configIO.exportToFile') }}</Button>
        <Button @click="onCopyExport">{{ $t('configIO.copyToClipboard') }}</Button>
      </div>
      <p v-if="lastExport" class="io-result io-export-result">{{ lastExport.text }}</p>
    </section>

    <hr class="b-divider" />

    <!-- ② 导入 -->
    <section class="io-section io-import">
      <h4 class="io-title">{{ $t('configIO.importTitle') }}</h4>
      <p class="io-hint">{{ $t('configIO.importHint') }}</p>
      <div class="io-actions">
        <Button :loading="busy.import" @click="pickFile">{{ $t('configIO.chooseFile') }}</Button>
        <input
          ref="fileInput"
          class="io-file-input"
          type="file"
          accept=".json,application/json"
          @change="onFileChange"
        />
      </div>
      <Textarea v-model="pasteText" class="io-paste" :rows="4" :placeholder="$t('configIO.pastePlaceholder')" />
      <div class="io-actions">
        <Button :disabled="busy.import" @click="onImportPaste">{{ $t('configIO.parseImport') }}</Button>
      </div>
      <div v-if="importError" class="io-alert io-error io-import-error">
        <div>{{ importError.text }}</div>
        <div v-if="importError.hint" class="io-hint">{{ importError.hint }}</div>
        <details v-if="importError.detail">
          <summary>{{ $t('configIO.showDetail') }}</summary>
          <pre class="io-detail">{{ importError.detail }}</pre>
        </details>
      </div>
      <div v-if="lastImport" class="io-result io-import-result">
        <div>{{ $t('configIO.importDone', { count: lastImport.count }) }}</div>
        <div v-if="lastImport.ignored" class="io-hint">{{ $t('configIO.importUnknownNote', { count: lastImport.ignored, keys: lastImport.ignoredKeys }) }}</div>
        <div v-if="lastImport.restartKeys" class="io-restart">
          <span class="io-restart-tag">{{ $t('configIO.restartTag') }}</span>
          <span class="io-hint">{{ $t('configIO.importRestartNote', { keys: lastImport.restartKeys }) }}</span>
        </div>
        <div v-if="lastImport.backupText" class="io-hint">{{ lastImport.backupText }}</div>
      </div>
    </section>

    <hr class="b-divider" />

    <!-- ③ 恢复出厂 -->
    <section class="io-section io-reset">
      <h4 class="io-title">{{ $t('configIO.resetTitle') }}</h4>
      <p class="io-alert io-danger io-reset-scope">{{ $t('configIO.resetScope') }}</p>
      <div class="io-actions">
        <Button type="danger" :loading="busy.reset" @click="onReset">{{ $t('configIO.resetButton') }}</Button>
      </div>
      <p v-if="lastReset" class="io-result io-reset-result">{{ lastReset.text }}</p>
    </section>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Switch, Textarea, message, confirm } from '../../components/ui'
import { getUserConfigMain, clearUserConfig, saveConfigFile, saveUserConfig } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { classifyError } from '../../utils/errorMessage'
import { saveFailedText, loadFailedText, errorReason } from '../../utils/settingsFeedback'
import {
  buildSnapshot, serializeSnapshot, backupFileName,
  parseImportText, filterImport, restartKeysOf,
} from '../../utils/configIO'

const { t } = useI18n()

const current = ref({})            // usr 主库视图（导出/备份数据源）
const excludeSecrets = ref(false)  // 「排除密钥」开关（默认包含 = false）
const pasteText = ref('')
const busy = ref({ export: false, import: false, reset: false })
const importError = ref(null)      // {text, hint, detail}
const lastImport = ref(null)       // {count, ignored, ignoredKeys, restartKeys, backupText}
const lastReset = ref(null)        // {text}
const lastExport = ref(null)       // {text}
const fileInput = ref(null)

// 当前配置概览：项数 + 密钥类字段数（提示「导出可能含密钥」的具体量）
const currentSummary = computed(() => {
  const { keyCount, removedSecrets } = buildSnapshot(current.value, { excludeSecrets: true })
  return t('configIO.currentSummary', { keys: keyCount, secrets: removedSecrets })
})

async function reload() {
  try {
    current.value = await getUserConfigMain()
  } catch (e) {
    message.error(loadFailedText(t, t('configIO.tabTitle'), e))
  }
}

/** 浏览器下载降级（宿主 native 不可用）；不打印内容 */
function downloadText(name, text) {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** 备份当前配置：优先落 <dataDir>/backup/（gui.file.save mode=backup），宿主不可用 → 下载降级 */
async function backupCurrent() {
  const { envelope } = buildSnapshot(current.value, {})
  const json = JSON.stringify(envelope, null, 2)
  const name = backupFileName(new Date())
  try {
    const res = await saveConfigFile(name, json, 'backup')
    const path = res && res.path ? String(res.path) : ''
    if (path) return { path }
  } catch (_) {
    // browser 形态无宿主文件能力 → 降级为下载（同文件名，用户可据此回退）
  }
  downloadText(name, json)
  return { downloaded: true }
}

// ── ① 导出 ──────────────────────────────────────────────
async function onExportFile() {
  const snap = serializeSnapshot(current.value, { excludeSecrets: excludeSecrets.value })
  busy.value.export = true
  try {
    const res = await saveConfigFile(snap.fileName, snap.json)
    const path = res && res.path ? String(res.path) : ''
    if (!path) {
      message.info(t('configIO.exportCancelled'))
      return
    }
    lastExport.value = { text: t('configIO.exportDone', { path }) }
    message.success(t('configIO.exportDone', { path }))
  } catch (_) {
    downloadText(snap.fileName, snap.json)
    lastExport.value = { text: t('configIO.exportDownloaded') }
    message.info(t('configIO.exportDownloaded'))
  } finally {
    busy.value.export = false
  }
}

async function onCopyExport() {
  const snap = serializeSnapshot(current.value, { excludeSecrets: excludeSecrets.value })
  try {
    await navigator.clipboard.writeText(snap.json)
    message.success(t('configIO.exportCopied'))
  } catch (e) {
    message.error(t('configIO.exportCopyFailed', { error: errorReason(e) || String(e) }))
  }
}

// ── ② 导入 ──────────────────────────────────────────────
/** 错误呈现：主文案用本页精确键；原始详情走批 2 `classifyError` 分类（认得出才补人话提示） */
function setImportError(textKey, params, detail) {
  const raw = detail ? String(detail) : ''
  const cls = raw ? classifyError(raw) : null
  importError.value = {
    text: t(textKey, params || {}),
    hint: cls && cls.key !== 'chat.error_unknown' ? t(cls.key, cls.params || {}) : '',
    detail: raw,
  }
}

function clearImportError() {
  importError.value = null
}

function pickFile() {
  clearImportError()
  if (fileInput.value) fileInput.value.click()
}

function onFileChange(e) {
  const f = e.target && e.target.files && e.target.files[0]
  if (e.target) e.target.value = '' // 允许重复选同一文件
  if (!f) return
  const reader = new FileReader()
  reader.onload = () => { applyImportText(String(reader.result || '')) }
  reader.onerror = () => { setImportError('configIO.importReadFailed', {}, reader.error && reader.error.message) }
  reader.readAsText(f)
}

function onImportPaste() {
  applyImportText(pasteText.value)
}

function askConfirm(text, title) {
  return confirm(text, title).then(() => true).catch(() => false)
}

async function applyImportText(text) {
  clearImportError()
  lastImport.value = null
  const parsed = parseImportText(text)
  if (!parsed.ok) {
    if (parsed.reason === 'empty') setImportError('configIO.importEmpty')
    else if (parsed.reason === 'json') setImportError('configIO.importBadJson', {}, parsed.detail)
    else setImportError('configIO.importBadShape', {}, parsed.detail)
    return
  }
  const { data, applied, ignored } = filterImport(parsed.data)
  if (!applied.length) {
    setImportError('configIO.importNoKeys', { count: ignored.length })
    return
  }

  // 确认弹窗写明影响范围（按键覆盖；**不静默全量替换**）
  let body = t('configIO.importConfirmBody', { count: applied.length, keys: applied.join(', ') })
  if (ignored.length) body += '\n' + t('configIO.importUnknownNote', { count: ignored.length, keys: ignored.join(', ') })
  if (!(await askConfirm(body, t('configIO.importConfirmTitle')))) return

  busy.value.import = true
  try {
    const backup = await backupCurrent()
    await saveUserConfig(data)
    const restartKeys = restartKeysOf(applied)
    lastImport.value = {
      count: applied.length,
      ignored: ignored.length,
      ignoredKeys: ignored.join(', '),
      restartKeys: restartKeys.join(', '),
      backupText: backup.path
        ? t('configIO.importBackup', { path: backup.path })
        : t('configIO.importBackupDownloaded'),
    }
    message.success(t('configIO.importDone', { count: applied.length }))
    if (restartKeys.length) message.warning(t('configIO.importRestartNote', { keys: restartKeys.join(', ') }))
    pasteText.value = ''
    await reload()
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    busy.value.import = false
  }
}

// ── ③ 恢复出厂（二次确认 + 先备份）────────────────────────
async function onReset() {
  if (!(await askConfirm(t('configIO.resetConfirm1'), t('configIO.resetConfirmTitle')))) return
  if (!(await askConfirm(t('configIO.resetConfirm2'), t('configIO.resetConfirmTitle')))) return
  busy.value.reset = true
  try {
    const backup = await backupCurrent()
    await clearUserConfig()
    const backupText = backup.path
      ? t('configIO.resetBackup', { path: backup.path })
      : t('configIO.importBackupDownloaded')
    lastReset.value = { text: t('configIO.resetDone') + ' ' + backupText }
    message.success(t('configIO.resetDone'))
    message.info(backupText)
    await reload()
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    busy.value.reset = false
  }
}

// 刷新用既有 data-user-config-refresh（导入/恢复出厂/其它页面保存后自动刷新；禁 watch）
const unsubs = []
onMounted(() => {
  reload()
  unsubs.push(onDataRefresh('user-config', reload))
})
onUnmounted(() => unsubs.forEach((fn) => fn()))
</script>

<style scoped>
.config-io-root {
  height: 100%;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 0.6em;
}
.io-section {
  display: flex;
  flex-direction: column;
  gap: 0.4em;
}
.io-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}
.io-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--fg-secondary);
}
.io-alert {
  margin: 0;
  padding: 6px 10px;
  border-radius: var(--border-radius);
  font-size: 12px;
  line-height: 1.6;
}
.io-secret-warn {
  background: rgba(230, 162, 60, 0.12);
  border: 1px solid rgba(230, 162, 60, 0.5);
  color: var(--text-primary);
}
.io-danger {
  background: rgba(220, 53, 69, 0.08);
  border: 1px solid rgba(220, 53, 69, 0.4);
  color: var(--text-primary);
}
.io-error {
  background: rgba(220, 53, 69, 0.08);
  border: 1px solid rgba(220, 53, 69, 0.4);
  color: var(--text-primary);
}
.io-detail {
  margin: 6px 0 0;
  max-height: 120px;
  overflow: auto;
  font-size: 11px;
  font-family: var(--font-mono, Consolas, monospace);
  white-space: pre-wrap;
  word-break: break-all;
}
.io-switch-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.io-switch-label {
  font-size: 13px;
  color: var(--text-primary);
}
.io-switch-hint {
  flex: 1;
  min-width: 200px;
}
.io-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.io-file-input {
  display: none;
}
.io-result {
  margin: 0;
  font-size: 12px;
  line-height: 1.7;
  color: var(--text-primary);
  word-break: break-all;
}
.io-restart {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.io-restart-tag {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--warning, #e6a23c);
  color: #fff;
}
.io-paste {
  width: 100%;
}
.b-divider {
  border: none;
  border-top: 1px solid var(--border, #dee2e6);
  margin: 4px 0;
  width: 100%;
}
</style>
