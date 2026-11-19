<template>
  <div class="ws-content">
    <div v-if="tabs.length" class="ws-tabs">
      <div
        v-for="tab in tabs"
        :key="tab.tabId"
        class="ws-tab"
        :class="{ active: tab.tabId === active.tabId }"
        :title="tab.workDir"
        @click="store.activateTab(tab.tabId)"
      >
        <span class="ws-tab-label">{{ tab.title || tab.sessionId.slice(0, 8) }}</span>
        <Icon v-if="tab.closeable" name="close" class="ws-tab-close" @click.stop="store.closeTab(tab.tabId)" />
      </div>
    </div>
    <div class="ws-body">
      <ChatPanel
        v-if="activeTab"
        :key="activeTab.tabId"
        :session-id="activeTab.sessionId"
        :work-dir="activeTab.workDir"
        :data-dir="dataDirOf(activeTab.workDir)"
        bound
      />
      <ChatPanel v-else />
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import Icon from '@ckfw/components/icon/Icon.vue'
import ChatPanel from '../chat/ChatPanel.vue'
import { useWorkspacesStore } from '../../stores/workspaces'

defineOptions({ name: 'WorkspaceContent' })

const store = useWorkspacesStore()
const tabs = computed(() => store.tabs)
const active = computed(() => store.active)
const activeTab = computed(() => store.activeTab)

function dataDirOf(workDir) {
  const p = store.projects[String(workDir || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()]
  return (p && p.dataDir) || ''
}
</script>

<style scoped>
.ws-content { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.ws-tabs { display: flex; align-items: stretch; gap: 1px; overflow-x: auto; background: var(--bg-secondary); border-bottom: 1px solid var(--border); flex: none; }
.ws-tab { display: flex; align-items: center; gap: 4px; padding: 5px 8px; max-width: 220px; cursor: pointer; color: var(--text-secondary); font-size: 12px; border-right: 1px solid var(--border); }
.ws-tab.active { color: var(--text-primary); background: var(--bg-primary); }
.ws-tab-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ws-tab-close { width: 12px; height: 12px; opacity: .6; }
.ws-tab-close:hover { opacity: 1; }
.ws-body { flex: 1; min-height: 0; }
</style>