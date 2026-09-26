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
import { onMounted, onUnmounted } from 'vue'
import { EventNames } from '../../events/event-names'
import LangSwitcher from '../lang/LangSwitcher.vue'
import { getAllConfig } from '../../api/config'
import { onDataRefresh } from '../../utils/dataClient'
import { useMemoryCategories } from '../../composables/useMemoryCategories'

// 记忆类别共享状态（与上下文管理页同源）：总量 + 分类列表 → 内容编辑弹框
const {
  enabled: memoryEnabled,
  total: memoryTotal,
  setEnabled,
  load: loadMemoryList,
  openCategoryList,
} = useMemoryCategories()

// 读记忆开关（memory.enabled）→ 启用则取类别清单（关闭态不发 data-memory-list）。
// 状态栏入口属辅助展示：读失败静默（不打断状态栏，也不谎报总量）。
async function refreshMemory() {
  try {
    const res = await getAllConfig()
    const cfg = res.config || res
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
  refreshMemory()
  // data-prj-config-refresh：配置变更（记忆开关）后 server 广播 → 重新判定并取清单
  _unsubs.push(onDataRefresh('prj-config', refreshMemory))
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
</style>
