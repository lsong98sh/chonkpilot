<template>
  <!-- 全局蒙板：有任何模态抽屉打开时铺满全屏 -->
  <div
    v-if="hasModal"
    class="drawer-overlay"
    :style="{ zIndex: overlayZ }"
    v-mq:[EventNames.drawerClose].click="() => topModalDrawer()"
  />

  <div
    v-for="item in openDrawers"
    :key="item.drawerId"
    class="drawer"
    :class="[`drawer-${item.def?.position ?? 'right'}`, { 'is-modal': item.modal }]"
    :style="drawerStyle(item)"
  >
    <div class="drawer-head">
      <LfIcon :name="item.def?.icon || item.icon || 'panel-right'" :size="14" />
      <span class="ttl">{{ item.def?.label ?? (item.label || item.drawerId) }}</span>
      <span class="grow" />
      <button
        class="d-btn"
        title="关闭抽屉"
        v-mq:[EventNames.drawerClose].click="{ drawerId: item.drawerId }"
      >
        ✕
      </button>
    </div>

    <div class="drawer-main" :class="{ vertical: isToolbarVertical(item) }">
      <LfBarStrip
        v-if="toolbarItems(item).length && toolbarPosition(item) === 'top'"
        :items="toolbarItems(item)"
        position="top"
        :name="`drawer-${item.drawerId}`"
        :background="toolbarDef(item)?.background ?? ''"
        :zone-background="toolbarDef(item)?.zoneBackground ?? {}"
        :default-side-width="140"
      />
      <LfBarStrip
        v-if="toolbarItems(item).length && toolbarPosition(item) === 'left'"
        :items="toolbarItems(item)"
        position="left"
        :name="`drawer-${item.drawerId}`"
        :background="toolbarDef(item)?.background ?? ''"
        :zone-background="toolbarDef(item)?.zoneBackground ?? {}"
        :default-side-width="120"
      />

      <div class="drawer-body">
        <div v-if="selectableViews(item).length > 1" class="drawer-tabs">
          <button
            v-for="(t, i) in selectableViews(item)"
            :key="`${t.viewKey}-${t.instanceKey}-${i}`"
            class="drawer-tab"
            :class="{ active: activeKey(item) === tabKey(t, i) }"
            v-mq:[EventNames.containerClick].click="() => selectTab(item.drawerId, tabKey(t, i))"
          >
            <LfIcon :name="viewIcon(t.viewKey)" :size="13" />
            <span>{{ t.label || viewLabel(t.viewKey) }}</span>
            <span v-if="t.instanceKey" class="key">{{ t.instanceKey }}</span>
          </button>
        </div>
        <div class="drawer-view">
          <LfViewHost v-if="currentView(item)" :view-ref="currentView(item)" />
          <div v-else class="drawer-empty">该抽屉没有可显示的内容。</div>
        </div>
      </div>

      <LfBarStrip
        v-if="toolbarItems(item).length && toolbarPosition(item) === 'right'"
        :items="toolbarItems(item)"
        position="right"
        :name="`drawer-${item.drawerId}`"
        :background="toolbarDef(item)?.background ?? ''"
        :zone-background="toolbarDef(item)?.zoneBackground ?? {}"
        :default-side-width="120"
      />
      <LfBarStrip
        v-if="toolbarItems(item).length && toolbarPosition(item) === 'bottom'"
        :items="toolbarItems(item)"
        position="bottom"
        :name="`drawer-${item.drawerId}`"
        :background="toolbarDef(item)?.background ?? ''"
        :zone-background="toolbarDef(item)?.zoneBackground ?? {}"
        :default-side-width="140"
      />
    </div>
  </div>
</template>

<script setup>
import { computed, reactive } from 'vue'
import LfViewHost from '../../views/LfViewHost.vue'
import LfBarStrip from '../../components/bar/LfBarStrip.vue'
import { useConfigStore } from '../../stores/config'
import { useRuntimeStore } from '../../stores/runtime'
import { viewIcon, viewLabel } from '../../config/views'
import { EventNames } from '../../events/event-names'

const config = useConfigStore()
const runtime = useRuntimeStore()

/** 抽屉内当前激活的内容（按抽屉 id 记录，key = tabKey） */
const activeTab = reactive({})

const openDrawers = computed(() =>
  runtime.drawers.map((d) => {
    const def = config.liveDrawerById(d.drawerId)
    return {
      ...d,
      def,
      modal: def?.modal !== false,
      tabs: def?.tabs ?? [],
    }
  }),
)

const hasModal = computed(() => openDrawers.value.some((d) => d.modal))
const overlayZ = 1400

function drawerStyle(item) {
  const pos = item.def?.position ?? 'right'
  const size = item.def?.size ?? 360
  const horizontal = pos === 'left' || pos === 'right'
  return {
    zIndex: 1401,
    [horizontal ? 'width' : 'height']: `${size}px`,
  }
}

function topModalDrawer() {
  const modal = openDrawers.value.filter((d) => d.modal)
  const last = modal[modal.length - 1]
  return last ? { drawerId: last.drawerId } : null
}

/* ── 抽屉自定义工具栏 ─────────────────────────────────── */

/** 抽屉挂载的自定义工具栏条目（未挂载 / 已删除 → 空数组） */
function toolbarDef(item) {
  const id = item.def?.toolbarId
  if (!id) return null
  return config.liveToolbarById(id) ?? null
}

function toolbarItems(item) {
  return toolbarDef(item)?.items ?? []
}

function toolbarPosition(item) {
  const p = item.def?.toolbarPosition
  return ['top', 'bottom', 'left', 'right'].includes(p) ? p : 'top'
}

function isToolbarVertical(item) {
  const p = toolbarPosition(item)
  return toolbarItems(item).length > 0 && (p === 'left' || p === 'right')
}

/** 抽屉可展示的内容：抽屉自身配置的 tabs + 命令带来的额外 view */
function selectableViews(item) {
  const list = [...(item.tabs ?? [])]
  if (item.viewKey) {
    list.push({ viewKey: item.viewKey, instanceKey: item.instanceKey ?? null, label: '' })
  }
  return list
}

function tabKey(t, i) {
  return `${t.viewKey}|${t.instanceKey ?? ''}|${i}`
}

/** 抽屉内切换内容（UI 局部状态，不参与命令层） */
function selectTab(drawerId, key) {
  activeTab[drawerId] = key
}

function activeKey(item) {
  const list = selectableViews(item)
  const stored = activeTab[item.drawerId]
  if (stored && list.some((t, i) => tabKey(t, i) === stored)) return stored
  // 命令指定了 view → 默认选中它
  if (item.viewKey) {
    const idx = list.findIndex((t, i) => tabKey(t, i) === `${item.viewKey}|${item.instanceKey ?? ''}|${i}`)
    if (idx >= 0) return tabKey(list[idx], idx)
  }
  return list.length ? tabKey(list[0], 0) : ''
}

function currentView(item) {
  const list = selectableViews(item)
  const key = activeKey(item)
  const idx = list.findIndex((t, i) => tabKey(t, i) === key)
  const tab = list[idx]
  if (!tab) return null
  return { viewKey: tab.viewKey, instanceKey: tab.instanceKey ?? null }
}
</script>

<style scoped>
.drawer-overlay {
  position: fixed;
  inset: 0;
  background: rgba(18, 26, 40, 0.32);
}

.drawer {
  position: fixed;
  display: flex;
  flex-direction: column;
  background: #fff;
  box-shadow: 0 12px 44px rgba(16, 26, 44, 0.28);
  overflow: hidden;
}

.drawer-right {
  top: 0;
  right: 0;
  bottom: 0;
  border-left: 1px solid var(--lf-border-strong);
}

.drawer-left {
  top: 0;
  left: 0;
  bottom: 0;
  border-right: 1px solid var(--lf-border-strong);
}

.drawer-top {
  top: 0;
  left: 0;
  right: 0;
  border-bottom: 1px solid var(--lf-border-strong);
}

.drawer-bottom {
  bottom: 0;
  left: 0;
  right: 0;
  border-top: 1px solid var(--lf-border-strong);
}

.drawer-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 8px 0 12px;
  border-bottom: 1px solid var(--lf-border);
  background: #f7f9fc;
}

.drawer-head .ttl {
  font-weight: 600;
  font-size: 13px;
}

.drawer-head .grow {
  flex: 1;
}

.d-btn {
  width: 24px;
  height: 22px;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: var(--lf-text-weak);
}

.d-btn:hover {
  background: #e2e7f0;
  color: #d33;
}

.drawer-tabs {
  flex: none;
  display: flex;
  align-items: stretch;
  gap: 2px;
  padding: 3px 6px 0;
  border-bottom: 1px solid var(--lf-border);
  background: #f3f5f9;
  overflow-x: auto;
}

.drawer-tab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  border: 1px solid transparent;
  border-bottom: none;
  border-radius: 6px 6px 0 0;
  background: #e8ecf3;
  color: var(--lf-text-weak);
  font-size: 12px;
  white-space: nowrap;
}

.drawer-tab.active {
  background: #fff;
  color: var(--lf-text);
  font-weight: 600;
  border-color: var(--lf-border);
}

.drawer-tab .key {
  font-size: 10px;
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
  border-radius: 8px;
  padding: 0 4px;
}

.drawer-main {
  flex: 1;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

/* 工具栏在左右两侧时，内容与工具栏横向并排 */
.drawer-main.vertical {
  flex-direction: row;
}

.drawer-body {
  flex: 1;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.drawer-view {
  flex: 1;
  min-height: 0;
  min-width: 0;
  display: flex;
}

.drawer-view > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.drawer-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--lf-text-weak);
  font-size: 12px;
}
</style>
