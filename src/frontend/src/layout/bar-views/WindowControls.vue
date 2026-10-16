<!--
  窗口控件（UILAYOUT-1 §7 P4①）：最小化 / 最大化还原 / 关闭 + 拖拽分隔线。
  **固定右端、不进配置文件**（38 §4.2 硬约束：删掉即无法操作窗口）——
  由 free 形态顶栏外壳渲染在 `LfBarZones` 的 `#right-extra` 插槽（位于所有靠右条目之后）。
  行为与 legacy `Toolbar.vue` 同口径：发 `gui.window.status {command}`；最大化态由
  `window-maximized-changed` 广播同步。
-->
<template>
  <span class="win-controls-sep" />
  <div class="win-controls">
    <button class="win-btn win-minimize" :title="$t('toolbar.minimize')" @click="send('minimize')">
      <svg width="10" height="10" viewBox="0 0 12 12"><rect x="1" y="5.5" width="10" height="1" fill="currentColor" /></svg>
    </button>
    <button class="win-btn win-maximize" :title="isMaximized ? $t('toolbar.restore') : $t('toolbar.maximize')" @click="send('maximize')">
      <svg v-if="!isMaximized" width="10" height="10" viewBox="0 0 12 12">
        <rect x="1.5" y="1.5" width="9" height="9" rx="1" fill="none" stroke="currentColor" stroke-width="1.2" />
      </svg>
      <svg v-else width="10" height="10" viewBox="0 0 12 12">
        <rect x="3.5" y="0.5" width="8" height="8" rx="1" fill="none" stroke="currentColor" stroke-width="1.2" />
        <path d="M3.5 3.5H1.5a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1v-2" fill="none" stroke="currentColor" stroke-width="1.2" />
      </svg>
    </button>
    <button class="win-btn win-close" :title="$t('toolbar.close')" @click="send('close')">
      <svg width="10" height="10" viewBox="0 0 12 12"><path d="M1 1l10 10M11 1L1 11" stroke="currentColor" stroke-width="1.4" fill="none" /></svg>
    </button>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const isMaximized = ref(false)

async function syncMaximizeState() {
  try {
    const env = await mq.emit(EventNames.guiWindowStatus, { command: null })
    const r = env && env.backend && env.backend.result
    if (r && typeof r.maximized === 'boolean') isMaximized.value = r.maximized
  } catch (_) { /* 忽略 */ }
}

function send(command) {
  mq.emit(EventNames.guiWindowStatus, { command })
}

const _unsubs = []
onMounted(() => {
  syncMaximizeState()
  window.addEventListener('resize', syncMaximizeState)
  _unsubs.push(mq.on(EventNames.windowMaximizedChanged, (data) => {
    if (data && typeof data.maximized === 'boolean') isMaximized.value = data.maximized
  }))
})
onUnmounted(() => {
  window.removeEventListener('resize', syncMaximizeState)
  _unsubs.forEach((fn) => fn())
  _unsubs.length = 0
})
</script>

<style scoped>
.win-controls-sep {
  width: 1px;
  height: 20px;
  background: var(--border);
  margin: 0 4px;
  flex-shrink: 0;
}
.win-controls {
  display: flex;
  align-items: center;
  gap: 0;
  -webkit-app-region: no-drag;
}
.win-btn {
  width: 36px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  border-radius: 0;
  transition: background 0.1s, color 0.1s;
}
.win-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.win-btn.win-close:hover {
  background: #e81123;
  color: #fff;
}
</style>
