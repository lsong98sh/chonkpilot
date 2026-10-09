<template>
  <div class="ft-root">
    <div class="tab-toolbar">
      <span class="tab-title">{{ $t('projectConfig.filetree') }}</span>
      <div class="tab-actions">
        <!-- dirty 标记（仅显示，不改保存按钮的时机） -->
        <span v-if="unsaved" class="unsaved-mark">{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" :disabled="saving || !hasOverride" @click="handleReset">{{ $t('projectConfig.index_reset_default') }}</Button>
        <Button size="small" type="primary" :loading="saving" @click="handleSave">{{ $t('projectConfig.save') }}</Button>
      </div>
    </div>
    <div class="ft-tab-content">
      <form class="form-layout">
        <div class="form-item form-item-full">
          <label class="form-label">{{ $t('projectConfig.filetree_hide_dirs_label') }}</label>
          <Textarea v-model="hideDirs" :rows="4" :placeholder="$t('projectConfig.filetree_hide_dirs_placeholder')" />
          <div class="ft-hint">{{ $t('projectConfig.filetree_hide_dirs_hint') }}</div>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Textarea, Button, message } from '../../components/ui'
import { getAllConfig, setConfig, deleteConfig } from '../../api/config'
import { usePrjConfigRefresh } from '../../composables/usePrjConfigRefresh'
import { APPLY_INSTANT, savedText, saveFailedText, loadFailedText } from '../../utils/settingsFeedback'
import { DEFAULT_HIDDEN_DIRS } from './indexDefaults'

// 项目级配置键（prj-config 域；团队共享，非个人运行态）。登记见 64-配置项一览。
const HIDE_DIRS_KEY = 'filetree.hide-dirs'

const { t } = useI18n()
const unsubs = []

// 项目级「不显示的目录」原始文本（'' = 未配置 → 回填默认镜像）
const hideDirs = ref('')
const orig = ref('')
const loadedOnce = ref(false)
const saving = ref(false)

// 展示镜像 = 项目级值；为空 → 回填缺省清单（展示值 = 实际生效值，I-65 ⑫）
function displayHideDirs(raw) { return raw || DEFAULT_HIDDEN_DIRS.join(', ') }

// 输入框是否仍等于「上次加载值的展示镜像」（= 用户未编辑）
function isPristine() { return hideDirs.value === displayHideDirs(orig.value) }

const unsaved = computed(() => !isPristine())
const hasOverride = computed(() => orig.value !== '')

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    const raw = c[HIDE_DIRS_KEY] || ''
    // 仅首次加载或用户未编辑时覆盖输入框（避免其它来源的广播冲掉未保存编辑）
    if (!loadedOnce.value || isPristine()) hideDirs.value = displayHideDirs(raw)
    orig.value = raw
    loadedOnce.value = true
  } catch (e) {
    message.error(loadFailedText(t, t('projectConfig.filetree'), e))
  }
}

// 保存：内容有改动才写入；清空 = 删项目级键（回落缺省清单）。
// 生效：后端经 data-prj-config-refresh 广播即时重过滤（隐藏者不再被 watch / 不再显示）。
async function handleSave() {
  saving.value = true
  try {
    if (isPristine()) {
      message.info(t('projectConfig.index_nothing_changed'))
      return
    }
    const v = hideDirs.value.trim()
    if (v === '') {
      if (orig.value !== '') await deleteConfig(HIDE_DIRS_KEY)
      orig.value = ''
      hideDirs.value = displayHideDirs('')
    } else {
      if (v !== orig.value) await setConfig(HIDE_DIRS_KEY, v)
      orig.value = v
      hideDirs.value = v
    }
    message.success(savedText(t, APPLY_INSTANT))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

// 恢复默认：清除项目级键（回落缺省清单）
async function handleReset() {
  saving.value = true
  try {
    if (orig.value !== '') await deleteConfig(HIDE_DIRS_KEY)
    orig.value = ''
    hideDirs.value = displayHideDirs('')
    message.success(t('projectConfig.index_reset_done'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadConfig()
  // data-prj-config-refresh：配置变更后自动重载（统一机制 usePrjConfigRefresh，I-138；
  // 按键过滤 + 突发合并 + 保存期间跳过）。
  unsubs.push(usePrjConfigRefresh({
    keys: [HIDE_DIRS_KEY],
    reload: loadConfig,
    isSaving: () => saving.value,
  }))
})

onUnmounted(() => {
  unsubs.forEach(fn => fn())
})
</script>

<style scoped>
.ft-root {
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
.unsaved-mark {
  font-size: 12px;
  color: var(--warning);
  white-space: nowrap;
}
.ft-tab-content {
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
.ft-hint {
  font-size: 12px;
  color: var(--fg-secondary);
  line-height: 1.6;
}
</style>
