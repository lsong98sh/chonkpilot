<template>
  <LfTabbedContainer
    :tabs="tabModels"
    :active-key="activeId"
    :active-index="activeIndex"
    :position="tabPosition"
    :empty-text="emptyText"
    show-close-active
    :close-active-topic="EventNames.tabsCloseActive"
    :close-active-payload="{ targetLeafId: leafId }"
    ctx-scope="free"
    :ctx-owner-id="leafId"
  >
    <template v-if="tabs.length">
      <div v-for="tab in tabs" v-show="tab.tabId === activeId" :key="tab.tabId" class="pane">
        <LfViewHost :view-ref="toRef(tab)" :design="design" />
      </div>
    </template>
    <div v-else class="pane-empty">
      <LfIcon name="gallery-vertical" :size="26" />
      <div>{{ tr('container.freeTabsEmpty') }}</div>
      <div class="sub">
        {{ tr('container.freeTabsEmptySub') }}
      </div>
    </div>
  </LfTabbedContainer>
</template>

<script setup>
import { computed } from 'vue'
import LfTabbedContainer from '../components/tabs/LfTabbedContainer.vue'
import LfViewHost from './LfViewHost.vue'
import { useRuntimeStore } from '../stores/runtime'
import { viewIcon, viewLabel } from '../config/views'
import { EventNames } from '../events/event-names'
import { tr } from '../deps.js'

const props = defineProps({
  /** 该区域的节点 id（自由 tab 容器以区域为实例） */
  leafId: { type: String, required: true },
  /** 标签栏位置 top | bottom | left | right */
  tabPosition: { type: String, default: 'top' },
  /** 设计态（只读渲染内部 view） */
  design: { type: Boolean, default: false },
})

const runtime = useRuntimeStore()

const tabs = computed(() => runtime.tabsOf(props.leafId))
const activeId = computed(
  () => runtime.activeTab[props.leafId] ?? tabs.value[tabs.value.length - 1]?.tabId ?? '',
)
const activeIndex = computed(() => tabs.value.findIndex((t) => t.tabId === activeId.value))
const emptyText = computed(() =>
  props.design ? tr('container.freeTabsEmptyDesign') : tr('container.freeTabsEmptyRuntime'),
)

const tabModels = computed(() =>
  tabs.value.map((t) => ({
    key: t.tabId,
    label: t.label || viewLabel(t.viewKey),
    icon: viewIcon(t.viewKey),
    instanceKey: t.instanceKey,
    pinned: t.pinned === true,
    title: `${t.label || viewLabel(t.viewKey)}${t.instanceKey ? ` · ${t.instanceKey}` : ''}`,
    closable: t.pinned !== true,
    topic: EventNames.tabsActivate,
    payload: { leafId: props.leafId, tabId: t.tabId },
    closeTopic: EventNames.tabsClose,
    closePayload: { leafId: props.leafId, tabId: t.tabId },
  })),
)

function toRef(tab) {
  return { viewKey: tab.viewKey, instanceKey: tab.instanceKey }
}
</script>

<style scoped>
.pane {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
}

.pane > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.pane-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--lf-text-weak);
  text-align: center;
  padding: 12px;
}

.pane-empty .sub {
  font-size: 11px;
  max-width: 340px;
  line-height: 1.6;
}
</style>
