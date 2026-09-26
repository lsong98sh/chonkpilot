<template>
  <div ref="configRoot" class="ide-config project-config-panel">
    <Tabs :tabs="tabsConfig" v-model="activeTab" class="cfg-tabs">
      <template #security>
        <SecurityConfig />
      </template>
      <template #context>
        <ContextConfig />
      </template>
      <template #codegraph>
        <CodegraphConfig />
      </template>
      <template #vfts>
        <VftsConfig />
      </template>
      <template #history>
        <HistoryConfig />
      </template>
      <template #log>
        <LogConfig />
      </template>
      <template #configIO>
        <ConfigIOPage />
      </template>
    </Tabs>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Tabs } from '../../components/ui'
import SecurityConfig from '../settings/SecurityConfig.vue'
import ContextConfig from '../settings/ContextConfig.vue'
import CodegraphConfig from '../settings/CodegraphConfig.vue'
import VftsConfig from '../settings/VftsConfig.vue'
import HistoryConfig from '../settings/HistoryConfig.vue'
import LogConfig from '../settings/LogConfig.vue'
import ConfigIOPage from '../config/SettingsConfigIOPage.vue'

const { t } = useI18n()

const activeTab = ref('security')
const configRoot = ref(null)

const tabsConfig = computed(() => [
  { label: t('projectConfig.security'), name: 'security' },
  { label: t('projectConfig.context'), name: 'context' },
  { label: t('projectConfig.codegraph'), name: 'codegraph' },
  { label: t('projectConfig.vfts'), name: 'vfts' },
  { label: t('projectConfig.history'), name: 'history' },
  { label: t('projectConfig.log'), name: 'log' },
  // 批 3 · ⑯：配置导入/导出 + 恢复出厂（usr 全局配置；页签追加在末位，不影响既有索引）
  { label: t('configIO.tabTitle'), name: 'configIO' },
])
</script>

<style scoped>
.ide-config {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 0;
  background: var(--bg-primary);
}
.project-config-panel .cfg-tabs :deep(.b-tabs-body) {
  padding: 0.5em 16px 0;
  max-height: 680px;
  overflow-y: auto;
}
.project-config-panel :deep(.b-input),
.project-config-panel :deep(.b-textarea),
.project-config-panel :deep(.b-select__native),
.project-config-panel :deep(.b-btn),
.project-config-panel :deep(.b-tag),
.project-config-panel :deep(.b-table-inline),
.project-config-panel :deep(.b-checkbox) {
  font-size: 13px;
}
</style>
