<!--
  栏视图条目 `memory-token`（UILAYOUT-1 §7 P4① / 裁决 2）：记忆总 token 数（A4 主入口）。
  由原 legacy `StatusBar.vue` 平移（该文件已随 UILAYOUT-2-B 删除；实时值 → 栏视图条目；不新增 valueExpr/badgeKey 协议）。
  保留 `.sb-section .sb-mem` / `.sb-mem-value` 既有类名。
-->
<template>
  <div
    v-if="memoryEnabled"
    class="sb-section sb-mem"
    :title="$t('statusBar.memoryTotal')"
    @click="openCategoryList"
  >
    <span>{{ $t('statusBar.memoryTotalLabel') }}</span>
    <span class="sb-mem-value">{{ memoryTotal }}</span>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { getAllConfig } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { useMemoryCategories } from '../../composables/useMemoryCategories'

const {
  enabled: memoryEnabled,
  total: memoryTotal,
  setEnabled,
  load: loadMemoryList,
  openCategoryList,
} = useMemoryCategories()

async function reloadPrjConfig() {
  try {
    const res = await getAllConfig()
    const cfg = res.config || res || {}
    setEnabled(cfg['memory.enabled'] === 'true')
  } catch (_) {
    setEnabled(false)
  }
  try {
    await loadMemoryList()
  } catch (_) { /* 静默：下次广播/重载再试 */ }
}

const _unsubs = []
onMounted(() => {
  reloadPrjConfig()
  _unsubs.push(onDataRefresh('prj-config', reloadPrjConfig))
  _unsubs.push(onDataRefresh('memory', () => { loadMemoryList().catch(() => {}) }))
})
onUnmounted(() => { _unsubs.forEach((fn) => fn()); _unsubs.length = 0 })
</script>

<style scoped>
.sb-section {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  padding: 0 4px;
  border-radius: 3px;
  height: 20px;
  -webkit-app-region: no-drag;
}
.sb-section:hover {
  background: var(--bg-hover, #e8e8e8);
}
.sb-mem {
  color: var(--text-secondary);
}
.sb-mem-value {
  font-weight: 600;
  color: var(--text-primary);
}
.sb-mem:hover .sb-mem-value {
  color: var(--accent, #409eff);
}
</style>
