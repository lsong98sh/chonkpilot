<template>
  <div class="tree-node">
    <div
      class="node-row"
      :class="{ selected: edit.selectedNodeId === node.id, hidden: node.type !== 'split' && !visible }"
      :style="{ paddingLeft: `${depth * 14 + 6}px` }"
      v-mq:[EventNames.layoutSelectNode].click.stop="{ nodeId: node.id }"
      @contextmenu="onContext"
    >
      <template v-if="node.type === 'split'">
        <button
          class="caret"
          v-mq:[EventNames.containerClick].click.stop="() => edit.toggleCollapse(node.id)"
        >
          {{ collapsed ? '▸' : '▾' }}
        </button>
        <span class="ico"><LfIcon name="columns-2" :size="12" /></span>
        <span class="label">容器 · {{ node.direction === 'row' ? '左右' : '上下' }}</span>
        <span class="badge">{{ node.children.length }}</span>
      </template>
      <template v-else>
        <span class="caret-space" />
        <LfIcon class="ico" :name="nodeIcon" :size="12" />
        <span class="label">{{ nodeTitle }}</span>
        <span v-if="instanceKey" class="key">{{ instanceKey }}</span>
      </template>
      <span class="grow" />
      <button
        v-if="node.type !== 'split'"
        class="mini"
        :title="visible ? '隐藏该区域' : '显示该区域'"
        v-mq:[EventNames.areaSetVisible].click.stop="{ nodeId: node.id, visible: !visible }"
      >
        <LfIcon :name="visible ? 'eye' : 'eye-closed'" :size="11" />
      </button>
      <button
        class="mini"
        title="分割（左右）"
        v-mq:[EventNames.layoutSplit].click.stop="{ nodeId: node.id, direction: 'row' }"
      >
        <LfIcon name="split-square-horizontal" :size="11" />
      </button>
      <button
        class="mini"
        title="分割（上下）"
        v-mq:[EventNames.layoutSplit].click.stop="{ nodeId: node.id, direction: 'column' }"
      >
        <LfIcon name="split-square-vertical" :size="11" />
      </button>
    </div>

    <template v-if="node.type === 'split' && !collapsed">
      <LfLayoutTree v-for="child in node.children" :key="child.id" :node="child" :depth="depth + 1" />
    </template>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useEditStore } from '../../stores/edit'
import { viewIcon, viewLabel } from '../../config/views'
import { regionKind, regionKindMeta } from '../../utils/regions'
import { EventNames } from '../../events/event-names'

defineOptions({ name: 'LfLayoutTree' })

const props = defineProps({
  /** 布局节点 */
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
})

const edit = useEditStore()

const collapsed = computed(() => edit.isCollapsed(props.node.id))
/** 区域显隐（WIN-025）：容器不可隐藏，仅叶子区域 */
const visible = computed(() => props.node.visible !== false)

/** 区域图标：单容器用 view 图标，tab 容器用类型图标 */
const nodeIcon = computed(() => {
  const n = props.node
  if (n.type !== 'leaf') return 'columns-2'
  const k = regionKind(n)
  if (k === 'single') return n.view ? viewIcon(n.view.viewKey) : 'square-dashed'
  return regionKindMeta(n).icon
})

const nodeTitle = computed(() => {
  const n = props.node
  if (n.title) return n.title
  if (n.type !== 'leaf') return '容器'
  const k = regionKind(n)
  if (k === 'single') return viewLabel(n.view?.viewKey)
  return regionKindMeta(n).label
})

const instanceKey = computed(() => (props.node.view?.instanceKey ?? '') || '')

function onContext(event) {
  event.preventDefault()
  event.stopPropagation()
  edit.openContextMenu(props.node.id, event.clientX, event.clientY)
}
</script>

<style scoped>
.node-row {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding-right: 4px;
  border-radius: 5px;
  cursor: default;
  user-select: none;
}

.node-row:hover {
  background: var(--lf-tree-hover);
}

.node-row.selected {
  background: var(--lf-select);
  box-shadow: inset 0 0 0 1px var(--lf-select-border);
}

/* 已隐藏区域：整行降透明度（仍可点选 / 恢复） */
.node-row.hidden .ico,
.node-row.hidden .label,
.node-row.hidden .key {
  opacity: 0.45;
}

.caret {
  width: 14px;
  height: 14px;
  border: none;
  background: transparent;
  padding: 0;
  font-size: 9px;
  color: var(--lf-text-weak);
  flex: none;
  cursor: pointer;
}

.caret-space {
  width: 14px;
  flex: none;
}

.ico {
  font-size: 12px;
  flex: none;
}

.label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.key {
  font-size: 10px;
  font-family: Consolas, monospace;
  color: var(--lf-accent);
  background: var(--lf-accent-soft);
  border-radius: 8px;
  padding: 0 5px;
  flex: none;
}

.badge {
  font-size: 10px;
  color: var(--lf-text-weak);
  background: #eef1f6;
  border-radius: 8px;
  padding: 0 5px;
  flex: none;
}

.grow {
  flex: 1;
}

.mini {
  width: 18px;
  height: 18px;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--lf-text-weak);
  font-size: 11px;
  line-height: 1;
  opacity: 0;
  flex: none;
  cursor: pointer;
}

.node-row:hover .mini {
  opacity: 1;
}

.mini:hover {
  border-color: var(--lf-accent);
  color: var(--lf-accent);
  background: #fff;
}
</style>
