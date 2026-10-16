<template>
  <div
    class="layout-leaf"
    :class="{ selected: isSelected, empty: isEmpty, runtime: !design, 'has-bg': !!node.background }"
    :data-leaf-id="node.id"
    :data-leaf-kind="kind"
    :style="node.background ? { background: node.background } : {}"
    @mousedown="onMouseDown"
    @contextmenu="onContextMenu"
  >
    <!-- 区域标题：仅设计态绘制；运行态不显示任何区域标题 -->
    <div v-if="design" class="leaf-design-title">
      <span class="dot" />
      <span class="txt">{{ title }}</span>
      <span class="kind">
        <LfIcon :name="kindMeta.icon" :size="11" />
        {{ kindMeta.label }}
      </span>
      <span v-if="toolbarDef" class="kind" :title="tr('container.leafToolbarTitle')">
        <LfIcon name="wrench" :size="11" />
        {{ toolbarDef.label }}
      </span>
    </div>

    <div class="leaf-main" :class="{ vertical: toolbarVertical }">
      <LfBarStrip
        v-if="toolbarItems.length && toolbarPos === 'top'"
        :items="toolbarItems"
        position="top"
        :name="`area-${node.id}`"
        :background="toolbarDef?.background ?? ''"
        :zone-background="toolbarDef?.zoneBackground ?? {}"
        :default-side-width="120"
      />
      <LfBarStrip
        v-if="toolbarItems.length && toolbarPos === 'left'"
        :items="toolbarItems"
        position="left"
        :name="`area-${node.id}`"
        :background="toolbarDef?.background ?? ''"
        :zone-background="toolbarDef?.zoneBackground ?? {}"
        :default-side-width="100"
      />

      <div class="leaf-content" :class="{ 'is-design': design }">
        <!-- 保活（§3.11）：三种区域类型**全部渲染 + v-show 隐藏** —— 切换区域类型不卸载
             （区块身份 / 会话 / 流式增量 / 页签集合不因切换或设计态预览而丢失）。 -->
        <!-- 自由 tab 容器 -->
        <LfFreeTabsView
          v-show="kind === 'free-tabs'"
          :leaf-id="node.id"
          :tab-position="tabPos"
          :design="design"
        />
        <!-- 固定 tab 容器 -->
        <LfFixedTabsView
          v-show="kind === 'fixed-tabs'"
          :node-id="node.id"
          :tabs="node.tabs ?? []"
          :tab-position="tabPos"
          :design="design"
        />
        <!-- 单容器：一个 view -->
        <LfViewHost v-show="kind === 'single' && !!viewRef" :view-ref="viewRef" :design="design" />
        <!-- 空白区域 -->
        <div v-show="isEmpty" class="leaf-empty">
          <LfIcon name="square-dashed" :size="22" />
          <div class="hint">{{ tr('container.emptyRegion') }}</div>
          <div class="sub">{{ tr('container.emptyRegionHint') }}</div>
        </div>
      </div>

      <LfBarStrip
        v-if="toolbarItems.length && toolbarPos === 'right'"
        :items="toolbarItems"
        position="right"
        :name="`area-${node.id}`"
        :background="toolbarDef?.background ?? ''"
        :zone-background="toolbarDef?.zoneBackground ?? {}"
        :default-side-width="100"
      />
      <LfBarStrip
        v-if="toolbarItems.length && toolbarPos === 'bottom'"
        :items="toolbarItems"
        position="bottom"
        :name="`area-${node.id}`"
        :background="toolbarDef?.background ?? ''"
        :zone-background="toolbarDef?.zoneBackground ?? {}"
        :default-side-width="120"
      />
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import LfViewHost from '../../views/LfViewHost.vue'
import LfFreeTabsView from '../../views/LfFreeTabsView.vue'
import LfFixedTabsView from '../../views/LfFixedTabsView.vue'
import LfBarStrip from '../../components/bar/LfBarStrip.vue'
import { useConfigStore } from '../../stores/config'
import { useEditStore } from '../../stores/edit'
import { useRuntimeStore } from '../../stores/runtime'
import { viewLabel } from '../../config/views'
import { regionKind, regionKindMeta, tabPosition } from '../../utils/regions'
import { tr } from '../../deps.js'

const props = defineProps({
  /** 区域（叶子）节点 */
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  /** 设计态：只读渲染 view + 绘制区域标题 */
  design: { type: Boolean, default: false },
  /** 设计态下选中的节点 id */
  selectedId: { type: String, default: '' },
})

const edit = useEditStore()
const config = useConfigStore()
const runtime = useRuntimeStore()

const kind = computed(() => regionKind(props.node))
const kindMeta = computed(() => regionKindMeta(props.node))
const tabPos = computed(() => tabPosition(props.node))
const isSelected = computed(() => props.design && props.selectedId === props.node.id)
const isEmpty = computed(() => kind.value === 'single' && !props.node.view)
/** 单容器的 view 引用（`v-show` 分支恒渲染 → 缺 view 时给一个占位引用，避免 undefined props） */
const viewRef = computed(() => props.node.view || { viewKey: '', instanceKey: null })
const title = computed(() => {
  if (props.node.title) return props.node.title
  if (kind.value === 'single') return viewLabel(props.node.view?.viewKey)
  return kindMeta.value.label
})

/* ── 区域挂载的自定义工具栏（四向）───────────────────── */

const toolbarDef = computed(() => {
  const id = props.node.toolbar?.toolbarId
  return id ? config.toolbarById(id) : null
})

const toolbarItems = computed(() => toolbarDef.value?.items ?? [])

const toolbarPos = computed(() => {
  const p = props.node.toolbar?.position
  return ['top', 'bottom', 'left', 'right'].includes(p) ? p : 'top'
})

const toolbarVertical = computed(
  () => toolbarItems.value.length > 0 && (toolbarPos.value === 'left' || toolbarPos.value === 'right'),
)

function onMouseDown(event) {
  runtime.setActiveLeaf(props.node.id)
  if (!props.design) return
  // 阻止冒泡：否则外层容器也会把自己设为选中节点
  event.stopPropagation()
  edit.select(props.node.id)
}

function onContextMenu(event) {
  if (!props.design) return
  event.preventDefault()
  event.stopPropagation()
  runtime.setActiveLeaf(props.node.id)
  edit.select(props.node.id)
  edit.openContextMenu(props.node.id, event.clientX, event.clientY)
}
</script>

<style scoped>
.layout-leaf {
  position: relative;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: #fff;
  border: 1px solid var(--lf-border);
  overflow: hidden;
  transition: box-shadow 0.12s, border-color 0.12s, background 0.12s;
}

.layout-leaf.selected {
  border-color: var(--lf-select-border);
  background: #fffdf4;
  box-shadow: inset 0 0 0 1px var(--lf-select-border), 0 0 0 2px rgba(240, 160, 0, 0.18);
  z-index: 1;
}

/* 用 background-image 而不是 background 简写：
   简写会把内联的 background-color（用户配置的背景色）一并重置，
   于是给「空白区域」设背景色会被这条斜纹规则整条覆盖掉 —— 只设 image 就两者共存。 */
.layout-leaf.empty {
  background-image: repeating-linear-gradient(45deg, #fbfcfe 0 8px, #f4f6fa 8px 16px);
}

/* 设了背景色时不要斜纹（半透明底色叠斜纹会发脏） */
.layout-leaf.empty.has-bg {
  background-image: none;
}

/* 设计态区域标题条 */
.leaf-design-title {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 8px;
  font-size: 11px;
  color: var(--lf-text-weak);
  border-bottom: 1px solid var(--lf-border);
  background: #f7f9fc;
  pointer-events: none;
}

.leaf-design-title .dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--lf-border-strong);
}

.leaf-design-title .txt {
  font-weight: 600;
  color: var(--lf-text);
}

.leaf-design-title .kind {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
  border-radius: 8px;
  padding: 0 5px;
}

.selected .leaf-design-title {
  background: var(--lf-select);
  border-bottom-color: var(--lf-select-border);
}

.selected .leaf-design-title .dot {
  background: var(--lf-select-border);
}

/* 区域主体：内容 +（可选的）四向自定义工具栏 */
.leaf-main {
  flex: 1;
  min-height: 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.leaf-main.vertical {
  flex-direction: row;
}

.leaf-content {
  flex: 1;
  min-height: 0;
  min-width: 0;
  display: flex;
}

.leaf-content > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

/* 设计态：view 内容只读，不可操作（区域本身仍可点选/右键） */
.leaf-content.is-design,
.leaf-content.is-design > * {
  pointer-events: none;
  user-select: none;
}

.leaf-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  color: var(--lf-text-weak);
}

.leaf-empty .hint {
  font-size: 12px;
}

.leaf-empty .sub {
  font-size: 11px;
  opacity: 0.8;
}
</style>
