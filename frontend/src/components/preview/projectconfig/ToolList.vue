<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">自定义工具</span>
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
            <th style="width:40px">#</th>
            <th style="min-width:140px">名称</th>
            <th style="width:90px">类型</th>
            <th style="min-width:200px">工具描述</th>
            <th style="width:70px;text-align:center">来源</th>
            <th style="width:150px;text-align:center">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="tools.length === 0">
            <td colspan="6" class="b-table-empty">暂无工具</td>
          </tr>
          <tr v-for="(row, rowIndex) in tools" :key="rowIndex">
            <td style="width:40px">{{ rowIndex + 1 }}</td>
            <td style="min-width:140px">{{ row.name }}</td>
            <td style="width:90px">{{ row.type }}</td>
            <td style="min-width:200px">{{ row.description || row.command }}</td>
            <td style="width:70px;text-align:center">
              <Tag v-if="row._source === 'llm'" size="small" type="warning">模型</Tag>
              <Tag v-else size="small" type="info">用户</Tag>
            </td>
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
import { Button, Tag } from '../../ui'
import { message, confirm } from '../../ui'

const props = defineProps({
  tools: { type: Array, required: true },
  maxHeight: { type: Number, default: 600 },
})

const emit = defineEmits(['add', 'edit', 'delete'])

async function handleDelete(index) {
  try {
    await confirm('删除此工具？')
  } catch { return }
  emit('delete', index)
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
}.b-table-inline th {
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