<template>
  <div class="home">
    <!-- 顶部工具栏：靠左区 / 中间区 / 靠右区 + 右侧固定的「内容设置」图标 -->
    <header class="toolbar">
      <div class="toolbar-brand">
        <LfIcon class="logo" name="layout-dashboard" :size="16" />
        <span class="name">Layout Free</span>
      </div>
      <div class="toolbar-zones">
        <LfBarZones
          name="topToolbar"
          :items="config.live.topToolbar?.items ?? []"
          :background="topBar.background"
          :zone-background="topBar.zoneBackground"
          empty-text="（顶部工具栏未配置条目）"
        >
          <template #right-extra>
            <button
              class="icon-btn"
              title="内容设置"
              v-mq:[EventNames.editorOpen].click="{ tab: 'layout' }"
            >
              <LfIcon name="settings" :size="15" />
            </button>
          </template>
        </LfBarZones>
      </div>
    </header>

    <!-- 自由布局容器：结构按已保存的 JSON，节点背景色取草稿（改一下立刻看得到） -->
    <main class="workspace">
      <LfFreeLayoutContainer :layout="config.liveLayoutWithDraftBackgrounds" />
    </main>

    <!-- 底部状态栏：靠左区 / 中间区 / 靠右区 -->
    <footer class="statusbar">
      <LfBarZones
        name="statusbar"
        :items="config.live.statusbar?.items ?? []"
        :background="statusBar.background"
        :zone-background="statusBar.zoneBackground"
        :default-side-width="420"
      >
        <template #right-extra>
          <span class="hint" :title="runtime.hint">{{ runtime.hint }}</span>
        </template>
      </LfBarZones>
    </footer>

    <!-- 对话框 / 抽屉 / 内容设置编辑器（右键菜单挂在 App.vue，全局唯一） -->
    <LfDrawerHost />
    <LfContentSettingsDialog />
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted } from 'vue'
import LfBarZones from '../components/bar/LfBarZones.vue'
import LfFreeLayoutContainer from '../components/layout/LfFreeLayoutContainer.vue'
import LfDrawerHost from '../components/drawer/LfDrawerHost.vue'
import LfContentSettingsDialog from '../components/settings/LfContentSettingsDialog.vue'
import { useConfigStore } from '../stores/config'
import { useEditStore } from '../stores/edit'
import { useRuntimeStore } from '../stores/runtime'
import { EventNames } from '../events/event-names'
import { getMq } from '../deps.js'

const config = useConfigStore()
const edit = useEditStore()
const runtime = useRuntimeStore()

/**
 * 顶部工具栏 / 状态栏：条目取 live（保存后生效），
 * **背景色取草稿**（纯视觉属性，改一下立刻看得到效果）。
 */
const topBar = computed(() => config.renderBar('topToolbar'))
const statusBar = computed(() => config.renderBar('statusbar'))

function onKeydown(e) {
  if (e.key === 'Escape') {
    edit.closeContextMenu()
    getMq().emit(EventNames.dropdownCloseAll)
    if (edit.open) {
      if (edit.showJson) edit.showJson = false
      else getMq().emit(EventNames.editorClose)
    } else if (runtime.drawers.length) {
      runtime.closeAllDrawers()
    }
    return
  }
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    if (!edit.open) return
    e.preventDefault()
    getMq().emit(EventNames.editorSave)
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<style scoped>
.home {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.toolbar {
  flex: none;
  display: flex;
  align-items: center;
  height: 38px;
  padding: 0 8px;
  background: var(--lf-bar);
  border-bottom: 1px solid var(--lf-border);
  box-shadow: 0 1px 3px rgba(20, 30, 50, 0.05);
  z-index: 20;
}

.toolbar-brand {
  flex: none;
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding-right: 10px;
  margin-right: 6px;
  border-right: 1px solid var(--lf-border);
  height: 100%;
}

.toolbar-brand .logo {
  color: var(--lf-accent);
  font-size: 15px;
}

.toolbar-brand .name {
  font-weight: 700;
  letter-spacing: 0.2px;
  white-space: nowrap;
}

.toolbar-zones {
  flex: 1;
  min-width: 0;
  height: 100%;
}

.icon-btn {
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--lf-border);
  border-radius: 7px;
  background: #fff;
  flex: none;
}

.icon-btn:hover {
  border-color: var(--lf-accent);
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
}

.icon-btn .glyph {
  font-size: 15px;
  line-height: 1;
}

.workspace {
  flex: 1;
  min-height: 0;
  padding: 5px;
  background: var(--lf-bg);
}

.workspace > * {
  width: 100%;
  height: 100%;
}

.statusbar {
  flex: none;
  height: 28px;
  background: var(--lf-bar);
  border-top: 1px solid var(--lf-border);
  z-index: 20;
}

.hint {
  font-size: 11px;
  color: var(--lf-text-weak);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 320px;
}
</style>
