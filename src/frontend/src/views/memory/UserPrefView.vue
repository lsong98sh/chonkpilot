<!--
  「用户偏好」只读预览页签（2026-09-27 用户口径）：
  「用户偏好」是唯一 user 级记忆类别，落 `~/.chonkpilot/用户偏好.md`（在工作目录之外）→ 走
  `file-open` 会被 filesys 越界校验拒绝，故经既有 `data-memory-read` 在本页签（预览区）只读展示，
  与项目级条目的预览区体验一致（不再弹窗）。

  取数自带：CodeView 的 `<component :is>` 不传任何 props，故本组件自行发起
  `data-memory-read`（零新增消息面），含加载态与失败提示（message.error + i18n）。
-->
<template>
  <div class="user-pref-view">
    <div v-if="loading" class="user-pref-loading">
      <Icon name="loading" :size="20" class="is-loading" />
      <span>{{ $t('common.loading') }}</span>
    </div>
    <pre v-else class="user-pref-text">{{ content }}</pre>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { dataRequest } from '../../utils/dataClient'
import { loadFailedText } from '../../utils/settingsFeedback'

// 唯一 user 级类别名（与后端 persist.MemoryUserCategory / ContextConfig.USER_PREF_CATEGORY 同字面量）
const USER_PREF_CATEGORY = '用户偏好'

const { t } = useI18n()

const loading = ref(true)
const content = ref('')

// 经既有 data-memory-read 读全文；失败 → 可见提示（不谎报成功），页签保留
async function load() {
  loading.value = true
  try {
    const res = await dataRequest('memory', 'read', { data: { category: USER_PREF_CATEGORY } })
    const d = res && res.data ? res.data : {}
    content.value = d.content || ''
  } catch (e) {
    message.error(loadFailedText(t, t('projectConfig.memory_user_pref'), e))
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.user-pref-view {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 12px 16px;
}
.user-pref-loading {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--fg-secondary);
  font-size: 12px;
}
.user-pref-text {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-primary);
}
</style>
