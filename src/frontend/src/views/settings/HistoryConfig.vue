<template>
  <div class="history-root">
    <div class="history-tab-content">
      <form class="form-layout">
        <div class="form-item form-item-full">
          <div class="history-toggle">
            <label class="form-label">{{ $t('historyConfig.enable') }}</label>
            <Switch v-model="historyEnabled" :disabled="!gitAvailable" @update:model-value="handleChange" />
            <!-- ⑤ dirty 标记（仅显示） -->
            <span v-if="dirty" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
          </div>
          <div v-if="!gitAvailable" class="history-reminder history-warn">{{ $t(noGitHint) }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_default_off') }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_on') }}</div>
          <div class="history-reminder">{{ $t('historyConfig.desc_off') }}</div>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Switch } from '../../components/ui'
import { message, confirm } from '../../components/ui'
import { getAllConfig, setConfig, getVCSInfo } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { useUnsavedMark } from '../../composables/useUnsavedMark'

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
  } catch (e) {
    // ④ 加载失败须用户可见（不再仅 console）
    message.error(loadFailedText(t, t('historyConfig.enable'), e))
  }
}

// 开启前先确认（对用户仓库有副作用：会在其仓库产生 git 提交/快照）；取消 → 回退开关、不写库。
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

onMounted(() => {
  loadConfig()
  loadVCS()
  // data-prj-config-refresh：配置变更后 server 广播，自动重载（20-gui）
  unsubs.push(onDataRefresh('prj-config', loadConfig))
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
/* ⑤ dirty 标记（仅显示） */
.unsaved-mark { font-size: 12px; color: var(--warning, #e6a23c); white-space: nowrap; }
</style>
