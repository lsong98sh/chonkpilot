<!--
  日志配置（项目级；项目配置页 → tab kind = settings-project 的「日志」页签）

  覆盖两件事（零新增 MQ 主题）：
    ① prj `logLevel` 前端入口 —— 读/写既有 prj config 表（getAllConfig/setConfig）；
       后端 `chonkpilot-gui/loglevel.go` 订阅 data-prj-config-refresh **运行时生效、免重启**
       → 保存后给用户明确反馈（「已保存并即时生效」）。
    ② 日志目录入口 —— 数据源 = 既有 `gui.init-data` 的只读字段 `logDir`
       （chonkpilot-gui/logfile.go；未挂文件 sink 时字段缺省）：
       GUI 形态用既有 native `reveal` 打开；browser 形态（native 不可用）显示路径 + 复制；
       缺 `logDir` 时**不显示空路径**，给明确提示。
-->
<template>
  <div class="log-root">
    <form class="form-layout">
      <div class="form-item form-item-full">
        <label class="form-label">{{ $t('projectConfig.log_level_label') }}</label>
        <div class="log-level">
          <Select
            :model-value="level"
            :options="levelOptions"
            aria-label="logLevel"
            @update:model-value="onLevelChange"
          />
        </div>
        <div class="log-hint">{{ $t('projectConfig.log_level_hint') }}</div>
      </div>

      <hr class="b-divider" />

      <div class="form-item form-item-full">
        <label class="form-label">{{ $t('projectConfig.log_dir_label') }}</label>
        <div v-if="view.show" class="log-dir-row">
          <span class="mono" :title="view.path">{{ view.path }}</span>
          <Button v-if="view.canOpen" size="small" @click="openDir">
            <Icon name="folder-opened" :size="13" /> {{ $t('projectConfig.log_dir_open') }}
          </Button>
          <Button v-if="view.canCopy" size="small" @click="copyPath">
            <Icon name="copy-document" :size="13" /> {{ $t('projectConfig.log_dir_copy') }}
          </Button>
        </div>
        <div v-if="view.hintKey" class="log-hint">{{ $t(view.hintKey) }}</div>
      </div>
    </form>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Select, Button, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { getAllConfig, setConfig } from '../../api/config'
import { loadInitData, revealInExplorer } from '../../api/file'
import { isBrowserForm } from '../../utils/runtimeForm'
import { logDirView } from '../../utils/logDirView'
import { saveFailedText, loadFailedText } from '../../utils/settingsFeedback'

const { t } = useI18n()

// 后端 parseLogLevel 可识别：debug/info/warn(warning)/error；空/未知 → info（不改动现值）。
const DEFAULT_LEVEL = 'info'
const level = ref(DEFAULT_LEVEL)
const logDir = ref('')

const levelOptions = computed(() => [
  { label: t('projectConfig.log_level_debug'), value: 'debug' },
  { label: t('projectConfig.log_level_info'), value: 'info' },
  { label: t('projectConfig.log_level_warn'), value: 'warn' },
  { label: t('projectConfig.log_level_error'), value: 'error' },
])

// 视图模型：show/path/canOpen/canCopy/hintKey（见 utils/logDirView.js）
const view = computed(() => logDirView({ logDir: logDir.value, isBrowser: isBrowserForm() }))

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    const raw = String(c['logLevel'] || '').trim().toLowerCase()
    level.value = ['debug', 'info', 'warn', 'error'].includes(raw) ? raw : DEFAULT_LEVEL
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('projectConfig.log_level_label'), e))
  }
}

async function loadLogDir() {
  try {
    const r = await loadInitData()
    logDir.value = (r && r.logDir) || ''
  } catch (e) {
    console.warn('[LogConfig] loadLogDir error:', e)
    logDir.value = ''
  }
}

// 保存即生效（后端 data-prj-config-refresh → applyLogLevel，免重启）→ 明确反馈。
async function onLevelChange(v) {
  if (!v) return
  const prev = level.value
  level.value = v
  try {
    await setConfig('logLevel', v)
    message.success(t('projectConfig.log_saved'))
  } catch (e) {
    level.value = prev
    message.error(saveFailedText(t, e))
  }
}

// GUI 形态：既有 native reveal 在资源管理器中定位目录。
async function openDir() {
  try {
    await revealInExplorer(view.value.path)
  } catch (e) {
    message.error(t('projectConfig.log_dir_open_failed') + ': ' + (e.message || ''))
  }
}

// browser 形态：native 不可用 → 复制路径。
async function copyPath() {
  try {
    await navigator.clipboard.writeText(view.value.path)
    message.success(t('projectConfig.log_dir_copied'))
  } catch (e) {
    message.error(t('projectConfig.save_failed') + ': ' + (e.message || ''))
  }
}

onMounted(() => {
  loadConfig()
  loadLogDir()
})
</script>

<style scoped>
.log-root {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
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
.log-level {
  width: 220px;
}
.log-dir-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.mono {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono, Consolas, monospace);
  font-size: 12px;
  color: var(--text-primary);
  word-break: break-all;
}
.log-hint {
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.6;
}
.b-divider {
  border: none;
  border-top: 1px solid var(--border, #dee2e6);
  margin: 8px 0;
  width: 100%;
}
</style>
