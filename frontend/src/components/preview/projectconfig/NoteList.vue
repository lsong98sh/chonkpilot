<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">笔记</span>
      <div class="tab-actions">
        <Button size="small" type="primary" @click="$emit('add')">
          <Icon name="plus" /> 添加
        </Button>
      </div>
    </div>
    <div class="table-wrap">
      <table class="b-table-inline">
        <thead>
          <tr>
            <th style="width:50px">#</th>
            <th style="min-width:160px">标题</th>
            <th style="min-width:240px">预览</th>
            <th style="width:150px;text-align:center">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="notes.length === 0">
            <td colspan="4" class="b-table-empty">暂无笔记</td>
          </tr>
          <tr v-for="(row, rowIndex) in notes" :key="rowIndex">
            <td style="width:50px">{{ rowIndex + 1 }}</td>
            <td style="min-width:160px">{{ row.title }}</td>
            <td style="min-width:240px">{{ preview(row.content) }}</td>
            <td style="width:150px;text-align:center">
              <Button text size="small" @click="$emit('edit', rowIndex)">编辑</Button>
              <Button text size="small" type="danger" @click="handleDelete(rowIndex)">删除</Button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import Icon from '../../icon/Icon.vue'
import { Button } from '../../ui'
import { message, confirm } from '../../ui'
import { deleteNote } from '../../../api/note'

const props = defineProps({
  notes: { type: Array, required: true },
  maxHeight: { type: Number, default: 600 },
})

const emit = defineEmits(['add', 'edit', 'delete', 'refresh'])

function preview(text) {
  if (!text) return ''
  return text.length > 80 ? text.slice(0, 80) + '...' : text
}

async function handleDelete(index) {
  const note = props.notes[index]
  if (!note) return
  try {
    await confirm('删除此笔记？')
  } catch { return }
  try {
    await deleteNote(note.title)
    emit('delete', index)
    message.success('已删除')
  } catch (e) {
    message.error('删除失败: ' + (e.message || ''))
  }
}
</script>

<style scoped>
.list-container {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.tab-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  gap: 8px;
  flex-shrink: 0;
}
.tab-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}
.tab-actions {
  display: flex;
  gap: 6px;
}
.table-wrap {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}
.b-table-inline {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-size-sm, 13px);
}
.b-table-inline thead {
  position: sticky;
  top: 0;
  z-index: 1;
}
.b-table-inline th {
  padding: 8px 12px;
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
  border-bottom: 1px solid var(--border, #dee2e6);
  background: var(--bg-secondary, #fff);
  color: var(--text-secondary, #495057);
  user-select: none;
}
.b-table-inline td {
  padding: 7px 12px;
  border-bottom: 1px solid var(--border, #dee2e6);
  color: var(--text-primary, #212529);
  line-height: 1.4;
}
.b-table-inline tbody tr:hover {
  background: var(--bg-hover, #e9ecef);
}
.b-table-empty {
  text-align: center;
  padding: 24px 12px;
  color: var(--text-muted, #6c757d);
  font-size: var(--font-size-sm, 13px);
}
</style>