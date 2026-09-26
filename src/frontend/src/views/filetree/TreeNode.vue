<template>
  <div>
    <div
      class="tree-row"
      :class="{
        selected: selectedKey === node.path,
        'multi-selected': selectedKeys.includes(node.path),
        'drop-target': dragOverPath === node.path,
        'is-dir': node.is_dir,
        'is-db': isDBConfig(node),
      }"
      :style="{ paddingLeft: 8 + depth * 18 + 'px' }"
      :data-path="node.path"
      draggable="true"
      @click="$emit('row-click', node, $event)"
      @dblclick="$emit('row-dblclick', node)"
      @contextmenu.prevent="$emit('context-menu', $event, node)"
      @dragstart.stop="onDragStart($event)"
      @dragend.stop="onDragEnd($event)"
      @dragover.stop.prevent="onDragOver($event)"
      @dragleave.stop="onDragLeave($event)"
      @drop.stop.prevent="onDrop($event)"
    >
      <span v-if="node.is_dir" class="arrow" @click.stop="$emit('toggle', node)">
        <svg
          v-if="!node.expanded"
          width="10" height="10" viewBox="0 0 16 16" fill="currentColor"
          class="arrow-icon"
        >
          <path d="M5.5 3.5L10.5 8L5.5 12.5" stroke="currentColor" stroke-width="1.5"
                fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <svg
          v-else
          width="10" height="10" viewBox="0 0 16 16" fill="currentColor"
          class="arrow-icon expanded"
        >
          <path d="M3.5 5.5L8 10.5L12.5 5.5" stroke="currentColor" stroke-width="1.5"
                fill="none" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </span>
      <span v-else class="arrow-placeholder" />

      <!-- Directory icons -->
      <Icon v-if="node.is_dir" :name="node.expanded ? 'folder-opened' : 'folder'" :size="14" :color="getIconColor(node)" class="node-icon" />
      <!-- DB config icon (special file) -->
      <span v-else-if="isDBConfig(node)" class="node-icon">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" :stroke="getIconColor(node)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <ellipse cx="12" cy="5" rx="9" ry="3" />
          <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
          <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
        </svg>
      </span>
      <!-- Regular file icon -->
      <Icon v-else name="document" :size="14" :color="getIconColor(node)" class="node-icon" />

      <span v-if="editingPath !== node.path" class="node-label">{{ node.label }}</span>
      <Input
        v-else
        ref="editInputRef"
        :model-value="editingValue"
        @update:model-value="$emit('update:editingValue', $event)"
        class="inline-edit-input"
        @keyup.enter="$emit('confirm-edit')"
        @keyup.escape="$emit('cancel-edit')"
        @blur="$emit('confirm-edit')"
        @mousedown.stop
        @click.stop
      />

      <Tag v-if="isDBConfig(node)" size="small" type="info" class="db-tag">DB</Tag>

      <span v-if="node.is_dir && node.expanded && node._loading" class="loading-indicator">⋯</span>
    </div>

    <template v-if="node.is_dir && node.expanded && visibleChildren.length > 0">
      <TreeNode
        v-for="child in visibleChildren"
        :key="child.path"
        :node="child"
        :depth="depth + 1"
        :selected-key="selectedKey"
        :editing-path="editingPath"
        :editing-value="editingValue"
        :search-query="searchQuery"
        :selected-keys="selectedKeys"
        :drag-over-path="dragOverPath"
        @toggle="(n) => $emit('toggle', n)"
        @row-click="(n, e) => $emit('row-click', n, e)"
        @row-dblclick="(n) => $emit('row-dblclick', n)"
        @context-menu="(e, n) => $emit('context-menu', e, n)"
        @update:editing-value="(v) => $emit('update:editingValue', v)"
        @confirm-edit="$emit('confirm-edit')"
        @cancel-edit="$emit('cancel-edit')"
        @drag-start="(n, e) => $emit('drag-start', n, e)"
        @drag-end="(n, e) => $emit('drag-end', n, e)"
        @drag-over="(n, e) => $emit('drag-over', n, e)"
        @drag-leave="(n, e) => $emit('drag-leave', n, e)"
        @drop="(n, e) => $emit('drop', n, e)"
      />
    </template>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import Icon from '../../components/icon/Icon.vue'
import { Tag, Input } from '../../components/ui'

defineOptions({ name: 'TreeNode' })

const props = defineProps({
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  selectedKey: { type: String, default: '' },
  selectedKeys: { type: Array, default: () => [] },
  editingPath: { type: String, default: '' },
  editingValue: { type: String, default: '' },
  searchQuery: { type: String, default: '' },
})

const emit = defineEmits([
  'toggle',
  'row-click',
  'row-dblclick',
  'context-menu',
  'update:editingValue',
  'confirm-edit',
  'cancel-edit',
  'drag-start',
  'drag-end',
  'drag-over',
  'drag-leave',
  'drop',
])

// 拖拽：启动时登记拖拽源（dataTransfer 存 path，供 chat 拖放/跨区识别）；drop 冒泡交给 FileTree 处理
function onDragStart(e) {
  e.dataTransfer.setData('text/plain', props.node.path)
  e.dataTransfer.setData('application/x-chonk-node', JSON.stringify({
    path: props.node.path, is_dir: props.node.is_dir,
  }))
  e.dataTransfer.effectAllowed = 'move'
  emit('drag-start', props.node, e)
}
function onDragEnd(e) {
  emit('drag-end', props.node, e)
}
function onDragOver(e) {
  emit('drag-over', props.node, e)
}
function onDragLeave(e) {
  emit('drag-leave', props.node, e)
}
function onDrop(e) {
  emit('drop', props.node, e)
}

function isDBConfig(data) {
  return data.path && data.path.startsWith('db://')
}

function hasMatchingDescendant(node, q) {
  if (!node.children) return false
  for (const c of node.children) {
    if (c.label.toLowerCase().includes(q)) return true
    if (c.is_dir && hasMatchingDescendant(c, q)) return true
  }
  return false
}

function getIconColor(node) {
  if (node.is_dir) return undefined
  if (isDBConfig(node)) return '#409eff'
  return getFileIconColor(getFileExt(node))
}

function getFileExt(node) {
  if (!node.label) return ''
  const dot = node.label.lastIndexOf('.')
  if (dot < 0) return ''
  return node.label.substring(dot).toLowerCase()
}

function getFileIconColor(ext) {
  const colors = {
    '.docx': '#2B579A', '.doc': '#2B579A',
    '.pptx': '#D04526', '.ppt': '#D04526',
    '.xlsx': '#217346', '.xls': '#217346', '.csv': '#217346',
    '.md': '#083FA1', '.markdown': '#083FA1',
    '.c': '#00599C', '.h': '#A42E2B',
    '.cpp': '#00599C', '.cxx': '#00599C', '.cc': '#00599C', '.hpp': '#A42E2B',
    '.go': '#00ADD8',
    '.java': '#ED8B00', '.class': '#ED8B00',
    '.py': '#3776AB', '.pyw': '#3776AB',
    '.js': '#F7DF1E', '.jsx': '#F7DF1E', '.mjs': '#F7DF1E',
    '.ts': '#3178C6', '.tsx': '#3178C6',
    '.json': '#292929',
    '.html': '#E34F26', '.htm': '#E34F26',
    '.css': '#1572B6', '.scss': '#1572B6', '.less': '#1572B6',
    '.vue': '#4FC08D',
    '.yml': '#CB171E', '.yaml': '#CB171E',
    '.xml': '#0060AC', '.svg': '#FFB13B',
    '.sh': '#4EAA25', '.bash': '#4EAA25', '.zsh': '#4EAA25',
    '.toml': '#9C4121',
    '.rs': '#DEA584',
  }
  return colors[ext] || '#888'
}

const visibleChildren = computed(() => {
  if (!props.searchQuery) return props.node.children || []
  const q = props.searchQuery.toLowerCase()
  return (props.node.children || []).filter(c => {
    if (c.label.toLowerCase().includes(q)) return true
    if (c.is_dir && hasMatchingDescendant(c, q)) return true
    return false
  })
})
</script>

<style scoped>
.tree-row {
  display: flex;
  align-items: center;
  gap: 2px;
  height: 28px;
  padding-right: 8px;
  cursor: pointer;
  border-radius: 3px;
  transition: background 0.1s;
  white-space: nowrap;
}
.tree-row:hover {
  background: var(--bg-hover, rgba(0,0,0,0.04));
}
.tree-row.selected {
  background: var(--bg-hover);
}
/* 多选节点 */
.tree-row.multi-selected {
  background: var(--bg-hover);
  outline: 1px solid var(--accent);
  outline-offset: -1px;
}
/* 拖拽悬停目标（移入该目录） */
.tree-row.drop-target {
  background: color-mix(in srgb, var(--accent) 18%, transparent);
  outline: 1px dashed var(--accent);
  outline-offset: -1px;
}

.arrow {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  color: var(--text-secondary, #888);
  cursor: pointer;
  border-radius: 3px;
  transition: transform 0.15s;
}
.arrow:hover {
  background: var(--bg-hover, rgba(0,0,0,0.06));
  color: var(--text-primary, #333);
}
.arrow-icon {
  display: block;
}
.arrow-placeholder {
  display: inline-block;
  width: 10px;
  flex-shrink: 0;
}

.node-icon {
  flex-shrink: 0;
  color: var(--text-secondary, #888);
  margin: 0 2px;
}
.tree-row.is-dir .node-icon {
  color: #e6a23c;
}

.node-label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
  line-height: 1.4;
}

.inline-edit-input {
  width: calc(100% - 40px);
}
.inline-edit-input :deep(.b-input-wrapper) {
  padding: 0 4px;
  height: 24px;
  border-radius: 3px;
}

.db-tag {
  transform: scale(0.75);
  margin-left: 2px;
  line-height: 14px;
  height: 16px;
  padding: 0 4px;
}

.loading-indicator {
  font-size: 13px;
  color: var(--text-secondary, #888);
  animation: pulse 1s infinite;
}
@keyframes pulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 1; }
}
</style>