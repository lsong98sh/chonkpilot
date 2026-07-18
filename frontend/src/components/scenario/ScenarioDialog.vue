<template>
  <el-dialog v-model="visible" title="场景管理" width="90vw" top="5vh" append-to-body destroy-on-close draggable class="scenario-dialog">
    <div class="dialog-content">
      <!-- List View -->
      <template v-if="!editing">
        <div class="toolbar-actions">
          <el-button size="small" type="primary" @click="startAdd">
            <el-icon><Plus /></el-icon> 添加场景
          </el-button>
        </div>
        <el-table :data="scenarios" size="small" style="width: 100%" empty-text="暂无场景" class="scenario-table">
          <el-table-column label="名称" prop="name" min-width="160" />
          <el-table-column label="说明" prop="description" min-width="200" show-overflow-tooltip />
          <el-table-column label="操作" width="160" align="center" fixed="right">
            <template #default="{ row }">
              <el-button text size="small" @click="startEdit(row)">编辑</el-button>
              <el-button text size="small" type="danger" @click="handleDelete(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </template>

      <!-- Edit View -->
      <template v-else>
        <div class="edit-header">
          <el-button size="small" @click="cancelEdit">返回列表</el-button>
          <span class="edit-title">{{ isNew ? '添加场景' : '编辑场景' }}</span>
          <el-button size="small" type="primary" @click="confirmEdit">保存</el-button>
        </div>
        <el-form label-position="top" size="small" class="edit-form">
          <el-row :gutter="12">
            <el-col :span="6">
              <el-form-item label="名称">
                <el-input v-model="form.name" placeholder="场景名称" />
              </el-form-item>
            </el-col>
            <el-col :span="18">
              <el-form-item label="说明">
                <el-input v-model="form.description" placeholder="场景说明" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-form-item label="系统提示词" class="prompt-form-item">
            <el-input
              v-model="form.systemPrompt"
              type="textarea"
              :rows="20"
              class="prompt-editor"
              placeholder="输入场景的系统提示词，选择此场景后会自动覆盖默认系统提示词"
            />
          </el-form-item>
        </el-form>
      </template>
    </div>
  </el-dialog>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const emit = defineEmits(['changed'])

const visible = ref(false)
const scenarios = ref([])
const editing = ref(false)
const isNew = ref(false)
const form = ref({ name: '', description: '', systemPrompt: '' })

function open() {
  visible.value = true
  loadList()
}
defineExpose({ open })

function startAdd() {
  isNew.value = true
  form.value = { name: '', description: '', systemPrompt: '' }
  editing.value = true
}

function startEdit(row) {
  isNew.value = false
  form.value = { ...row }
  editing.value = true
}

function cancelEdit() {
  editing.value = false
}

async function confirmEdit() {
  try {
    const res = await window.go.main.App.SaveScenario({ scenario: form.value })
    if (res?.scenario) {
      ElMessage.success(isNew.value ? '场景已创建' : '场景已更新')
    }
    editing.value = false
    await loadList()
    emit('changed')
  } catch (e) {
    ElMessage.error('保存失败: ' + (e.message || e))
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(`确定删除场景「${row.name}」？`, '确认删除', { type: 'warning' })
    await window.go.main.App.DeleteScenario({ id: row.id })
    ElMessage.success('场景已删除')
    await loadList()
    emit('changed')
  } catch (e) {
    if (e !== 'cancel') ElMessage.error('删除失败: ' + (e.message || e))
  }
}

async function loadList() {
  try {
    const res = await window.go.main.App.GetScenarioList()
    scenarios.value = res.scenarios || []
  } catch (e) {
    scenarios.value = []
  }
}
</script>

<style scoped>
.scenario-dialog :deep(.el-dialog) {
  max-height: 680px;
  display: flex;
  flex-direction: column;
}
.scenario-dialog :deep(.el-dialog__header) {
  flex-shrink: 0;
}
.scenario-dialog :deep(.el-dialog__body) {
  flex: 1;
  overflow: hidden;
  padding: 16px;
  display: flex;
  flex-direction: column;
}
.dialog-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}
.toolbar-actions {
  margin-bottom: 12px;
  flex-shrink: 0;
}
.scenario-table {
  flex: 1;
  overflow-y: auto;
}
.edit-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.edit-title {
  flex: 1;
  font-weight: 700;
  font-size: 14px;
}
.edit-form {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.prompt-form-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  margin-bottom: 0;
}
.prompt-form-item :deep(.el-form-item__content) {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.prompt-editor {
  font-family: var(--font-mono, 'Cascadia Code', 'JetBrains Mono', monospace) !important;
  flex: 1;
  min-height: 0;
}
.prompt-editor :deep(textarea) {
  height: 100% !important;
  resize: vertical;
}
</style>
