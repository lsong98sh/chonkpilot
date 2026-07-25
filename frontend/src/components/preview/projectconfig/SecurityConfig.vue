<template>
  <div class="list-container">
    <div class="tab-toolbar">
      <span class="tab-title">信任目录</span>
      <div class="tab-actions">
        <Button size="small" type="primary" @click="addEntry">
          <Icon name="plus" /> 添加
        </Button>
      </div>
    </div>
    <div class="table-wrap">
      <table class="b-table-inline">
        <thead>
          <tr>
            <th style="width:40px">#</th>
            <th style="min-width:300px">信任目录</th>
            <th style="width:80px;text-align:center">读写</th>
            <th style="width:70px;text-align:center">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="entries.length === 0">
            <td colspan="4" class="b-table-empty">暂无信任目录</td>
          </tr>
          <tr v-for="(row, rowIndex) in entries" :key="rowIndex">
            <td style="width:40px">{{ rowIndex + 1 }}</td>
            <td style="min-width:300px">
              <div class="security-dir-row">
                <Input v-model="entries[rowIndex].dir" placeholder="C:\path\to\trusted" @change="save" />
                <Button size="small" @click="selectDir(rowIndex)">...</Button>
              </div>
            </td>
            <td style="width:80px;text-align:center">
              <label class="b-checkbox">
                <input type="checkbox" v-model="entries[rowIndex].writable" @change="save" />
              </label>
            </td>
            <td style="width:70px;text-align:center">
              <Button text size="small" type="danger" @click="deleteEntry(rowIndex)">删除</Button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import Icon from '../../icon/Icon.vue'
import { Input, Button } from '../../ui'
import { getProjectSecurity, saveProjectSecurity } from '../../../api/config'
import { PickFolder } from '../../../../wailsjs/go/main/App'
import { message, confirm } from '../../ui'

const props = defineProps({
  maxHeight: { type: Number, default: 600 },
})

const entries = ref([])

async function load() {
  try {
    const res = await getProjectSecurity()
    entries.value = res.entries || []
  } catch (_) { entries.value = [] }
}

async function save() {
  try {
    await saveProjectSecurity(entries.value)
  } catch (e) {
    message.error('保存安全配置失败: ' + (e.message || ''))
  }
}

function addEntry() {
  entries.value.push({ dir: '', writable: false })
  save()
}

async function selectDir(index) {
  try {
    const res = await PickFolder()
    if (res?.path) {
      entries.value[index].dir = res.path
      save()
    }
  } catch (_) {
    const path = prompt('输入目录路径:')
    if (path) {
      entries.value[index].dir = path
      save()
    }
  }
}

async function deleteEntry(index) {
  try {
    await confirm('删除此信任目录？')
  } catch { return }
  entries.value.splice(index, 1)
  save()
}

onMounted(load)
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
.security-dir-row {
  display: flex;
  gap: 4px;
  align-items: center;
}
.security-dir-row .b-input {
  flex: 1;
}
.b-checkbox {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  font-size: 13px;
}
.b-checkbox input[type="checkbox"] {
  accent-color: var(--accent, #409eff);
}
</style>