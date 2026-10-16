<!--
  栏视图条目 `index-badge`（UILAYOUT-1 §7 P4① / 裁决 2）：codegraph / vfts 索引状态徽标。
  从 `views/statusbar/StatusBar.vue` 平移（实时值 → 栏视图条目）。
  数据源 = prj 配置既有键（enable-codegraph / codegraph.status / enable-vfts / vfts.status）；
  刷新 = 既有 `data-prj-config-refresh` 广播；点击 → 既有 `project-config-open {tab}`。
-->
<template>
  <div class="sb-section sb-index">
    <span
      class="sb-idx"
      :class="'is-' + cgBadge.tone"
      data-engine="codegraph"
      :title="$t('statusBar.idx_open_codegraph')"
      v-mq:[EventNames.projectConfigOpen].click="{ tab: 'codegraph' }"
    >
      <Icon name="collection" :size="12" class="sb-idx-icon" />
      <span class="sb-idx-state">{{ cgBadge.text }}</span>
    </span>
    <span
      class="sb-idx"
      :class="'is-' + vfBadge.tone"
      data-engine="vfts"
      :title="$t('statusBar.idx_open_vfts')"
      v-mq:[EventNames.projectConfigOpen].click="{ tab: 'vfts' }"
    >
      <Icon name="search" :size="12" class="sb-idx-icon" />
      <span class="sb-idx-state">{{ vfBadge.text }}</span>
    </span>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '../../components/icon'
import { getAllConfig } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { indexBadge } from '../../utils/indexStatus'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()

const prjCfg = ref({})

const cgBadge = computed(() => indexBadge({
  enabledRaw: prjCfg.value['enable-codegraph'],
  statusRaw: prjCfg.value['codegraph.status'],
  t,
}))
const vfBadge = computed(() => indexBadge({
  enabledRaw: prjCfg.value['enable-vfts'],
  statusRaw: prjCfg.value['vfts.status'],
  t,
}))

async function reloadPrjConfig() {
  try {
    const res = await getAllConfig()
    const cfg = res.config || res || {}
    prjCfg.value = cfg && typeof cfg === 'object' ? cfg : {}
  } catch (_) {
    prjCfg.value = {}
  }
}

let offRefresh = null
onMounted(() => {
  reloadPrjConfig()
  offRefresh = onDataRefresh('prj-config', reloadPrjConfig)
})
onUnmounted(() => { if (typeof offRefresh === 'function') offRefresh() })
</script>

<style scoped>
.sb-section {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 4px;
  border-radius: 3px;
  height: 20px;
  -webkit-app-region: no-drag;
}
.sb-index {
  gap: 8px;
  color: var(--text-secondary);
  white-space: nowrap;
  flex-shrink: 0;
}
.sb-idx {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  white-space: nowrap;
  cursor: pointer;
}
.sb-idx-icon {
  flex-shrink: 0;
}
.sb-idx.is-disabled { color: var(--fg-disabled); }
.sb-idx.is-progress { color: var(--fg-secondary); }
.sb-idx.is-ready { color: var(--text-primary); }
.sb-idx.is-error { color: var(--fg-important); }
</style>
