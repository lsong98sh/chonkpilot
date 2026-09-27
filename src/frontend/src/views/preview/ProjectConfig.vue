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

// 外部页签定位（可选）：`innerTab` = 目标页签名（如 'codegraph' / 'vfts'）；
// `innerTabNonce` 每次请求递增 → 只在「新请求」时生效（无需 watch），缺省/非法 → 落默认页签。
const props = defineProps({
  innerTab: { type: String, default: '' },
  innerTabNonce: { type: Number, default: 0 },
})

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

const configRoot = ref(null)

// 激活页签 = 用户点击选择（selection）与外部请求（有效 innerTab + 新 nonce）合流：
// get 优先返回未消费的外部请求，否则返回用户选择；set（点击页签）落 selection 并消费当前 nonce。
const TAB_NAMES = computed(() => tabsConfig.value.map(x => x.name))
const selection = ref('')
const appliedNonce = ref(0)
const activeTab = computed({
  get() {
    if (props.innerTabNonce > appliedNonce.value && TAB_NAMES.value.includes(props.innerTab)) {
      return props.innerTab
    }
    return selection.value
  },
  set(name) {
    selection.value = name
    appliedNonce.value = props.innerTabNonce
  },
})
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
  /* 头部页签固定 + 正文区 flex:1/overflow:auto/min-height:0（由 Tabs 内建 flex 撑满，
     此处仅让正文可滚；不再写死 max-height） */
  overflow-y: auto;
}
.project-config-panel :deep(.b-input),
.project-config-panel :deep(.b-textarea),
.project-config-panel :deep(.b-select__native),
.project-config-panel :deep(.b-btn),
.project-config-panel :deep(.b-tag),
.project-config-panel :deep(.b-checkbox) {
  font-size: 13px;
}
</style>
