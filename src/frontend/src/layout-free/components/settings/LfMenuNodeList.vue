<template>
  <div class="menu-node-list">
    <template v-for="node in nodes" :key="node.id">
      <div
        class="mn-row"
        :class="{ selected: edit.selectedMenuNodeId === node.id }"
        :style="{ paddingLeft: `${depth * 16 + 6}px` }"
        v-mq:[EventNames.menuNodeSelect].click="{ nodeId: node.id }"
      >
        <span class="ico"><LfIcon :name="node.icon || (node.children?.length ? 'folder-open' : 'dot')" :size="12" /></span>
        <span class="lbl">{{ node.label }}</span>
        <span class="cmd">
          {{ node.children?.length ? `子菜单 · ${node.children.length} 项` : describeCommand(node.command) }}
        </span>
        <span class="grow" />
        <button
          class="mini"
          title="添加子条目"
          v-mq:[EventNames.menuNodeAdd].click.stop="{ bar, itemId, parentId: node.id, sub: false }"
        >
          ＋
        </button>
        <button
          class="mini"
          title="添加子菜单"
          v-mq:[EventNames.menuNodeAdd].click.stop="{ bar, itemId, parentId: node.id, sub: true }"
        >
          <LfIcon name="folder-open" :size="12" />
        </button>
        <button
          class="mini"
          title="上移"
          v-mq:[EventNames.menuNodeMove].click.stop="{ bar, itemId, nodeId: node.id, delta: -1 }"
        >
          ↑
        </button>
        <button
          class="mini"
          title="下移"
          v-mq:[EventNames.menuNodeMove].click.stop="{ bar, itemId, nodeId: node.id, delta: 1 }"
        >
          ↓
        </button>
        <button
          class="mini danger"
          title="删除"
          v-mq:[EventNames.menuNodeRemove].click.stop="{ bar, itemId, nodeId: node.id }"
        >
          ✕
        </button>
      </div>

      <LfMenuNodeList
        v-if="node.children?.length"
        :nodes="node.children"
        :bar="bar"
        :item-id="itemId"
        :depth="depth + 1"
      />
    </template>
  </div>
</template>

<script setup>
import { useEditStore } from '../../stores/edit'
import { describeCommand } from '../../utils/commands'
import { EventNames } from '../../events/event-names'

defineOptions({ name: 'LfMenuNodeList' })

defineProps({
  /** 菜单条目数组 */
  nodes: { type: Array, required: true },
  /** 'toolbar' | 'statusbar' */
  bar: { type: String, required: true },
  /** 所属栏条目的 id */
  itemId: { type: String, required: true },
  depth: { type: Number, default: 0 },
})

const edit = useEditStore()
</script>

<style scoped>
.mn-row {
  display: flex;
  align-items: center;
  gap: 5px;
  height: 24px;
  padding-right: 4px;
  border-radius: 5px;
  cursor: pointer;
}

.mn-row:hover {
  background: var(--lf-tree-hover);
}

.mn-row.selected {
  background: var(--lf-select);
  box-shadow: inset 0 0 0 1px var(--lf-select-border);
}

.mn-row .lbl {
  flex: none;
  max-width: 30%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mn-row .cmd {
  font-size: 11px;
  color: var(--lf-text-weak);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mn-row .grow {
  flex: 1;
}

.mini {
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  background: #fff;
  color: var(--lf-text-weak);
  height: 19px;
  min-width: 19px;
  line-height: 1;
  font-size: 11px;
  opacity: 0;
  cursor: pointer;
}

.mn-row:hover .mini,
.mn-row.selected .mini {
  opacity: 1;
}

.mini:hover {
  border-color: var(--lf-accent);
  color: var(--lf-accent);
}

.mini.danger:hover {
  border-color: #d33;
  color: #d33;
}
</style>
