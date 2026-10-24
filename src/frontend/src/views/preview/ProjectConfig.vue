<template>
  <div ref="configRoot" class="ide-config project-config-panel">
    <Tabs :tabs="tabsConfig" v-model="activeTab" class="cfg-tabs">
      <template #security>
        <SecurityConfig />
      </template>
      <template #context>
        <ContextConfig />
      </template>
      <template #codebase>
        <!-- 代码知识库（codegraph + vfts）配置页 = codebase 插件的 ui.views.config（iframe 承载）。
             原两个重复设置页已删除（UIREG-1）；容器复用插件 view 机制。 -->
        <PluginPageHost
          v-if="codebaseView"
          :plugin-id="CODEBASE_PLUGIN_ID"
          :page-id="CODEBASE_CONFIG_VIEW_ID"
          :entry="codebaseView.entry || ''"
          :title="codebaseView.title || ''"
        />
        <div v-else class="plugin-missing">{{ t('projectConfig.codebase_unavailable') }}</div>
      </template>
      <template #history>
        <HistoryConfig />
      </template>
      <template #filetree>
        <FileTreeConfig />
      </template>
      <template #groupchat>
        <GroupChatConfig />
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
import PluginPageHost from '../../components/plugin/PluginPageHost.vue'
import { usePlugins } from '../../composables/usePlugins'
import SecurityConfig from '../settings/SecurityConfig.vue'
import ContextConfig from '../settings/ContextConfig.vue'
import HistoryConfig from '../settings/HistoryConfig.vue'
import FileTreeConfig from '../settings/FileTreeConfig.vue'
import GroupChatConfig from '../settings/GroupChatConfig.vue'
import LogConfig from '../settings/LogConfig.vue'
import ConfigIOPage from '../config/SettingsConfigIOPage.vue'

const { t } = useI18n()

// 代码库插件（codegraph/vfts 配置）承载：与「插件」页打开的是同一个 iframe view
// （`plugin:<id>:<view>`），此处直接复用 `PluginPageHost`，不新造容器。
const CODEBASE_PLUGIN_ID = 'codebase'
const CODEBASE_CONFIG_VIEW_ID = 'config'
const { pluginView } = usePlugins()
const codebaseView = computed(() => pluginView(CODEBASE_PLUGIN_ID, CODEBASE_CONFIG_VIEW_ID))

// 外部页签定位（可选）：`innerTab` = 目标页签名（如 'codebase' / 'history'）；
// `innerTabNonce` 每次请求递增 → 只在「新请求」时生效（无需 watch），缺省/非法 → 落默认页签。
// 旧页签名 'codegraph' / 'vfts' 已合并为 'codebase'（UIREG-1）→ 兼容映射，旧发送方不必改。
const props = defineProps({
  innerTab: { type: String, default: '' },
  innerTabNonce: { type: Number, default: 0 },
})

const LEGACY_TAB_ALIAS = { codegraph: 'codebase', vfts: 'codebase' }

const tabsConfig = computed(() => [
  { label: t('projectConfig.security'), name: 'security' },
  { label: t('projectConfig.context'), name: 'context' },
  { label: t('projectConfig.codebase'), name: 'codebase' },
  { label: t('projectConfig.history'), name: 'history' },
  { label: t('projectConfig.filetree'), name: 'filetree' },
  // SUBCHAT-3 阶段 E：群聊最小配置项（最大轮次；群聊窗口 UI 属阶段 G）
  { label: t('projectConfig.groupchat'), name: 'groupchat' },
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
    const requested = LEGACY_TAB_ALIAS[props.innerTab] || props.innerTab
    if (props.innerTabNonce > appliedNonce.value && TAB_NAMES.value.includes(requested)) {
      return requested
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
.plugin-missing {
  padding: 16px 4px;
  font-size: 13px;
  color: var(--text-muted);
}
</style>
