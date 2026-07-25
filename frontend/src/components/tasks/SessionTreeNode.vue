<template>
  <div class="session-tree-node">
    <div
      class="node-header"
      :class="{ active: node.session_id === selectedId }"
      :style="{ paddingLeft: depth * 16 + 'px' }"
      @click="$emit('select', node)"
    >
      <!-- 展开/折叠箭头 -->
      <Icon
        v-if="node.children.length > 0"
        class="expand-icon"
        @click.stop="$emit('toggle-expand', node.session_id)"
        :size="12"
        :name="node.expanded ? 'arrow-down' : 'arrow-right'"
      />
      <span v-else class="leaf-spacer" />

      <!-- 状态图标 -->
      <Icon
        v-if="getStatus(node.session_id) === 'running'"
        class="status-icon is-loading"
        color="var(--accent)"
        title="Running"
        name="loading"
      />
      <Icon
        v-else-if="getStatus(node.session_id) === 'error'"
        class="status-icon"
        color="#f56c6c"
        title="Error"
        name="circle-close-filled"
      />
      <Icon
        v-else-if="getStatus(node.session_id) === 'completed'"
        class="status-icon"
        color="#67c23a"
        title="Completed"
        name="circle-check-filled"
      />

      <span class="node-title">{{ node.title || '#' + (node.session_id?.slice(0, 8) || '?') }}</span>
    </div>

    <!-- 子节点（递归） -->
    <div v-if="node.expanded && node.children.length > 0" class="node-children">
      <SessionTreeNode
        v-for="child in node.children"
        :key="child.session_id"
        :node="child"
        :depth="depth + 1"
        :selected-id="selectedId"
        :get-status="getStatus"
        @select="(s) => $emit('select', s)"
        @toggle-expand="(sid) => $emit('toggle-expand', sid)"
      />
    </div>
  </div>
</template>

<script setup>
import Icon from '../icon/Icon.vue'

defineProps({
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  selectedId: { type: String, default: null },
  getStatus: { type: Function, default: () => '' },
})

defineEmits(['select', 'toggle-expand'])
</script>

<style scoped>


.node-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: all var(--transition-fast);
  font-size: 13px;
}

.node-header:hover {
  background: var(--bg-hover);
}

.node-header.active {
  background: var(--bg-hover);
  border-left-color: var(--accent);
}

.expand-icon {
  flex-shrink: 0;
  cursor: pointer;
  color: var(--text-muted);
  width: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.leaf-spacer {
  display: inline-block;
  width: 16px;
  flex-shrink: 0;
}

.status-icon {
  flex-shrink: 0;
  font-size: 14px;
}

.node-title {
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.node-children {
  /* 子节点无需额外间距 */
}
</style>
