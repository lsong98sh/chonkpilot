<!--
  栏视图条目 `index-badge`（UILAYOUT-1 §7 P4① / 裁决 2；STB-1 改点击落点）：codegraph / vfts 索引状态徽标。
  数据源 = prj 配置键（plugins.codebase.codegraph.enabled / codegraph.status /
  plugins.codebase.vfts.enabled / vfts.status）；
  刷新 = 既有 `data-prj-config-refresh` 广播；点击 → 统一命令 `open-view{status-index}`
  （preview 详情页；详情页内保留「去设置」入口，配置仍可达）。**零新增 MQ 主题**。
-->
<template>
  <div class="sb-section sb-index">
    <span
      class="sb-idx"
      :class="'is-' + cgBadge.tone"
      data-engine="codegraph"
      :title="$t('statusBar.idx_open_codegraph')"
      @click="openDetail"
    >
      <Icon name="collection" :size="12" class="sb-idx-icon" />
      <span class="sb-idx-state">{{ cgBadge.text }}</span>
    </span>
    <span
      class="sb-idx"
      :class="'is-' + vfBadge.tone"
      data-engine="vfts"
      :title="$t('statusBar.idx_open_vfts')"
      @click="openDetail"
    >
      <Icon name="search" :size="12" class="sb-idx-icon" />
      <span class="sb-idx-state">{{ vfBadge.text }}</span>
    </span>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '../../components/icon'
import { indexBadge } from '../../utils/indexStatus'
import { usePrjConfigSnapshot } from '../../composables/usePrjConfigSnapshot'
import { openStatusDetail } from '../statusDetail.js'

const { t } = useI18n()
const { prjCfg } = usePrjConfigSnapshot()

const cgBadge = computed(() => indexBadge({
  enabledRaw: prjCfg.value['plugins.codebase.codegraph.enabled'],
  statusRaw: prjCfg.value['codegraph.status'],
  t,
}))
const vfBadge = computed(() => indexBadge({
  enabledRaw: prjCfg.value['plugins.codebase.vfts.enabled'],
  statusRaw: prjCfg.value['vfts.status'],
  t,
}))

function openDetail() {
  openStatusDetail('index')
}
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
