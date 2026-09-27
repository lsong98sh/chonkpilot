<template>
  <div class="statusbar">
    <!-- 记忆总 token 数（A4 主入口，2026-09-24 由上下文管理页迁入状态栏）：
         点击 → 记忆分类列表 → 选中某个类别 → 内容编辑弹框（可编辑、可保存）。
         仅在记忆库启用时显示（memory.enabled 非 true → 不显示，也不取类别清单）。 -->
    <div
      v-if="memoryEnabled"
      class="sb-section sb-mem"
      :title="$t('statusBar.memoryTotal')"
      @click="openCategoryList"
    >
      <span>{{ $t('statusBar.memoryTotalLabel') }}</span>
      <span class="sb-mem-value">{{ memoryTotal }}</span>
    </div>

    <!-- 索引 / codebase 状态区（2026-09-27）：codegraph / vfts 两枚徽标，恒显示（「未启用」本身即信息）。
         数据源 = prj 配置既有键（enable-codegraph / codegraph.status / enable-vfts / vfts.status），
         刷新 = 既有 data-prj-config-refresh 广播。每枚徽标 = [图标] + 状态文本（图标在左、文本/数字紧贴其右），
         紧凑单行不换行；悬停原生 title；点击 → 既有 project-config-open（带 tab 定位到对应设置页签）。 -->
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

    <!-- 调试入口（B3）：DevTools 快捷键（F12 / Ctrl+Shift+I 等）与右键菜单 Inspect 已在宿主层屏蔽，
         用户只能经此处让宿主程序化打开 DevTools（gui.devtools.open → WebView2 OpenDevToolsWindow）。 -->
    <div class="sb-section" :title="$t('statusBar.openDevTools')" v-mq:[EventNames.guiDevToolsOpen].click>
      <svg class="sb-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="16 18 22 12 16 6" />
        <polyline points="8 6 2 12 8 18" />
        <line x1="14" y1="4" x2="10" y2="20" />
      </svg>
    </div>

    <div class="sb-spacer"></div>
    <LangSwitcher />
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { EventNames } from '../../events/event-names'
import LangSwitcher from '../lang/LangSwitcher.vue'
import Icon from '../../components/icon/Icon.vue'
import { getAllConfig } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { indexBadge } from '../../utils/indexStatus'
import { useMemoryCategories } from '../../composables/useMemoryCategories'

const { t } = useI18n()

// 记忆类别共享状态（与上下文管理页同源）：总量 + 分类列表 → 内容编辑弹框
const {
  enabled: memoryEnabled,
  total: memoryTotal,
  setEnabled,
  load: loadMemoryList,
  openCategoryList,
} = useMemoryCategories()

// prj 配置快照：一次读取供「记忆」门控（memory.enabled）与「索引状态」两枚徽标（4 个既有键）派生。
// 读失败静默（不谎报状态、不打断状态栏；下次广播/重载再试）。
const prjCfg = ref({})

// 索引徽标模型（computed 派生，不用 watch）：tone → 字色 token；text = 状态文案（图标 + 文本由模板拼装）。
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

// 读 prj 配置 → 记忆开关 + 索引状态；随后取记忆类别清单（仅启用时发 data-memory-list）。
async function reloadPrjConfig() {
  try {
    const res = await getAllConfig()
    const cfg = res.config || res || {}
    prjCfg.value = cfg && typeof cfg === 'object' ? cfg : {}
    setEnabled(cfg['memory.enabled'] === 'true')
  } catch (_) {
    prjCfg.value = {}
    setEnabled(false)
  }
  try {
    await loadMemoryList()
  } catch (_) { /* 静默：下次广播/重载再试 */ }
}

const _unsubs = []
onMounted(() => {
  reloadPrjConfig()
  // data-prj-config-refresh：配置变更（记忆开关 / 索引开关 / 索引状态回写）后 server 广播 → 重载
  _unsubs.push(onDataRefresh('prj-config', reloadPrjConfig))
  // data-memory-refresh：记忆沉淀写回后刷新类别 token（总量随之更新）
  _unsubs.push(onDataRefresh('memory', () => { loadMemoryList().catch(() => {}) }))
})

onUnmounted(() => {
  _unsubs.forEach(fn => fn())
  _unsubs.length = 0
})
</script>

<style scoped>
.statusbar {
  height: var(--statusbar-height);
  background: var(--bg-secondary, #f5f5f5);
  border-top: 1px solid var(--border);
  display: flex;
  align-items: center;
  padding: 0 12px;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted, #888);
  flex-shrink: 0;
  user-select: none;
}
.sb-section {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  padding: 0 4px;
  border-radius: 3px;
  height: 20px;
}
.sb-section:hover {
  background: var(--bg-hover, #e8e8e8);
}
.sb-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}
.sb-spacer {
  flex: 1;
}
/* 记忆总 token 数（点击 → 分类列表 → 内容编辑弹框） */
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
/* 索引 / codebase 状态区：codegraph + vfts 两枚徽标（紧凑单行，不换行撑高） */
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
}
.sb-idx-icon {
  flex-shrink: 0;
}
/* 字色按状态取语义 token（图标与状态文本同色）：未启用/未初始化 --fg-disabled；索引中 --fg-secondary；
   完成 --text-primary；失败 --fg-important（红） */
.sb-idx.is-disabled {
  color: var(--fg-disabled);
}
.sb-idx.is-progress {
  color: var(--fg-secondary);
}
.sb-idx.is-ready {
  color: var(--text-primary);
}
.sb-idx.is-error {
  color: var(--fg-important);
}
</style>
