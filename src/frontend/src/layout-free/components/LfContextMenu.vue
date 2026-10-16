<template>
  <div v-if="edit.contextMenu.visible" class="ctx-mask" v-mq:[EventNames.contextMenuClose].mousedown>
    <div
      class="ctx-menu"
      :style="{ left: `${edit.contextMenu.x}px`, top: `${edit.contextMenu.y}px` }"
      v-mq:[EventNames.containerClick].mousedown.stop
    >
      <div class="ctx-title">{{ title }}</div>
      <button class="ctx-item" v-mq:[EventNames.contextMenuSelect].click="{ action: 'split-row', nodeId }">
        <LfIcon class="ico" name="split-square-horizontal" :size="13" /> {{ tr('contextMenu.splitRow') }}
      </button>
      <button class="ctx-item" v-mq:[EventNames.contextMenuSelect].click="{ action: 'split-column', nodeId }">
        <LfIcon class="ico" name="split-square-vertical" :size="13" /> {{ tr('contextMenu.splitColumn') }}
      </button>
      <div class="ctx-sep" />
      <button
        class="ctx-item"
        :disabled="!isContainer"
        v-mq:[EventNames.contextMenuSelect].click="{ action: 'add-area', nodeId }"
      >
        <LfIcon class="ico" name="plus" :size="13" /> {{ tr('contextMenu.addArea') }}
      </button>
      <div class="ctx-sep" />
      <button
        class="ctx-item"
        :disabled="!sibling?.canPrev"
        v-mq:[EventNames.contextMenuSelect].click="{ action: 'swap-prev', nodeId }"
      >
        <LfIcon class="ico" :name="swapIcons.prev" :size="13" /> {{ swapLabels.prev }}
      </button>
      <button
        class="ctx-item"
        :disabled="!sibling?.canNext"
        v-mq:[EventNames.contextMenuSelect].click="{ action: 'swap-next', nodeId }"
      >
        <LfIcon class="ico" :name="swapIcons.next" :size="13" /> {{ swapLabels.next }}
      </button>
      <div class="ctx-sep" />
      <button
        class="ctx-item danger"
        :disabled="leafCount <= 1"
        v-mq:[EventNames.contextMenuSelect].click="{ action: 'remove-area', nodeId }"
      >
        <LfIcon class="ico" name="trash-2" :size="13" /> {{ tr('contextMenu.removeArea') }}
      </button>
      <div class="ctx-sep" />
      <button class="ctx-item" v-mq:[EventNames.contextMenuSelect].click="{ action: 'open-settings', nodeId }">
        <LfIcon class="ico" name="settings" :size="13" /> {{ tr('contextMenu.openSettings') }}
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useEditStore } from '../stores/edit'
import { useConfigStore } from '../stores/config'
import { findNode, isLeaf, isSplit } from '../utils/layout'
import { EventNames } from '../events/event-names'
import { tr } from '../deps.js'

const edit = useEditStore()
const config = useConfigStore()

const nodeId = computed(() => edit.contextMenu.nodeId)
const node = computed(() => findNode(config.draft.layout, nodeId.value))
const isContainer = computed(() => !!node.value && isSplit(node.value))
const leafCount = computed(() => config.leaves.length)
const title = computed(() => {
  const n = node.value
  if (!n) return tr('container.unknownNode')
  if (isLeaf(n)) return tr('container.regionTitle', { name: n.title || config.leafSummary(n.id) })
  return tr(n.direction === 'row' ? 'container.nodeTitleRow' : 'container.nodeTitleColumn', { id: n.id })
})

/** 同级交换（按父容器排布方向给不同的文案与图标） */
const sibling = computed(() => (nodeId.value ? config.siblingInfo(nodeId.value) : null))
const swapLabels = computed(() =>
  sibling.value?.direction === 'column'
    ? { prev: tr('contextMenu.swapPrevColumn'), next: tr('contextMenu.swapNextColumn') }
    : { prev: tr('contextMenu.swapPrevRow'), next: tr('contextMenu.swapNextRow') },
)
const swapIcons = computed(() => {
  const vertical = sibling.value?.direction === 'column'
  return vertical ? { prev: 'arrow-up', next: 'arrow-down' } : { prev: 'arrow-left', next: 'arrow-right' }
})
</script>

<style scoped>
.ctx-mask {
  position: fixed;
  inset: 0;
  z-index: 1200;
}

.ctx-menu {
  position: fixed;
  min-width: 210px;
  background: #fff;
  border: 1px solid var(--lf-border-strong);
  border-radius: 9px;
  box-shadow: 0 14px 40px rgba(16, 26, 44, 0.25);
  padding: 5px;
}

.ctx-title {
  font-size: 11px;
  color: var(--lf-text-weak);
  padding: 4px 8px 6px;
  border-bottom: 1px solid var(--lf-border);
  margin-bottom: 4px;
  font-family: Consolas, monospace;
}

.ctx-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px 8px;
  border: none;
  border-radius: 6px;
  background: transparent;
  text-align: left;
  color: var(--lf-text);
}

.ctx-item:hover:not(:disabled) {
  background: var(--lf-accent-soft);
  color: var(--lf-accent);
}

.ctx-item:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.ctx-item.danger:hover:not(:disabled) {
  background: #ffeaea;
  color: #c62828;
}

.ctx-sep {
  height: 1px;
  background: var(--lf-border);
  margin: 4px 2px;
}
</style>
