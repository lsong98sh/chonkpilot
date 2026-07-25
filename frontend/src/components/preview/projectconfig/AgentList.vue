<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">智能体</span>
      <div class="tab-actions">
        <Button size="small" @click="loadMissing">
          <Icon name="refresh" /> 从资源加载
        </Button>
        <Button size="small" @click="$emit('open-analyze')">
          <Icon name="magic-stick" /> AI 辅助
        </Button>
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
            <th style="min-width:140px">标题</th>
            <th style="min-width:200px">使用场景</th>
            <th style="width:70px;text-align:center">来源</th>
            <th style="width:150px;text-align:center">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="agents.length === 0">
            <td colspan="5" class="b-table-empty">暂无智能体</td>
          </tr>
          <tr v-for="(row, rowIndex) in agents" :key="rowIndex">
            <td style="width:40px">{{ rowIndex + 1 }}</td>
            <td style="min-width:140px">
              <span class="agent-title-cell">
                <Icon v-if="row._source === 'system'" name="star-filled" :size="14" class="agent-system-icon" />
                <Icon v-else-if="row._source === 'llm'" name="magic-stick" :size="14" class="agent-llm-icon" />
                <span>{{ row.title }}</span>
              </span>
            </td>
            <td style="min-width:200px">{{ row.useCase }}</td>
            <td style="width:70px;text-align:center">
              <Tag v-if="row._source === 'llm'" size="small" type="warning">模型</Tag>
              <Tag v-else-if="row._source === 'system'" size="small" type="success">系统</Tag>
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
import { loadMissingAgentsFromResource } from '../../../api/config'

const props = defineProps({
  agents: { type: Array, required: true },
  maxHeight: { type: Number, default: 600 },
})

const emit = defineEmits(['add', 'edit', 'delete', 'refresh', 'open-analyze'])

async function handleDelete(index) {
  try {
    await confirm('删除此智能体？')
  } catch { return }
  emit('delete', index)
}

async function loadMissing() {
  try {
    const res = await loadMissingAgentsFromResource()
    const count = res.inserted || 0
    if (count > 0) {
      emit('refresh')
      message.success(`已从资源加载 ${count} 个缺失的智能体`)
    } else {
      message.info('所有资源智能体已存在')
    }
  } catch (e) {
    message.error('加载失败: ' + (e.message || ''))
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
.agent-title-cell {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.agent-system-icon {
  color: #e6a23c;
}
.agent-llm-icon {
  color: #409eff;
}
</style>