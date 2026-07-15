<template>
  <el-dialog
    v-model="visible"
    title="📝 Notes"
    width="700px"
    top="8vh"
    :close-on-click-modal="false"
    draggable
    @open="loadNotes"
  >
    <!-- Editor view -->
    <div v-if="editing" class="editor-section">
      <el-input
        v-model="editTitle"
        placeholder="Note title"
        size="small"
        class="title-input"
        :disabled="isEditingExisting"
      />
      <el-input
        v-model="editContent"
        type="textarea"
        :rows="12"
        placeholder="Write your note here..."
        class="content-input"
      />
      <div class="editor-footer">
        <el-button size="small" @click="cancelEdit">Cancel</el-button>
        <el-button size="small" type="primary" @click="saveEdit" :loading="saving">Save</el-button>
      </div>
    </div>

    <!-- List view -->
    <div v-else>
      <div class="note-toolbar">
        <span class="note-count">{{ notes.length }} notes</span>
        <el-button size="small" type="primary" @click="startNew">
          <el-icon><Plus /></el-icon> New Note
        </el-button>
      </div>
      <div v-if="notes.length === 0" class="empty-notes">
        <p>No notes yet. Notes let the LLM record test results, compilation issues, or important discussion points.</p>
      </div>
      <div v-else class="note-list">
        <div
          v-for="n in notes"
          :key="n.title"
          class="note-item"
          @click="startEdit(n)"
        >
          <div class="note-item-title">{{ n.title }}</div>
          <div class="note-item-preview">{{ contentPreview(n.content) }}</div>
          <div class="note-item-meta">Updated: {{ n.updated_at.slice(0, 10) }}</div>
          <el-button
            text
            size="small"
            type="danger"
            class="note-item-del"
            @click.stop="handleDelete(n.title)"
          >
            <el-icon><Delete /></el-icon>
          </el-button>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup>
import { ref, defineExpose } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getNotes, getNote, saveNote, deleteNote } from '../../api/note'

const visible = ref(false)
const notes = ref([])
const editing = ref(false)
const editTitle = ref('')
const editContent = ref('')
const isEditingExisting = ref(false)
const saving = ref(false)

function open() {
  visible.value = true
}

defineExpose({ open })

async function loadNotes() {
  try {
    const res = await getNotes()
    notes.value = res.notes || []
  } catch (e) {
    ElMessage.error('Failed to load notes: ' + e.message)
  }
}

function startNew() {
  editing.value = true
  editTitle.value = ''
  editContent.value = ''
  isEditingExisting.value = false
}

function startEdit(note) {
  editing.value = true
  editTitle.value = note.title
  editContent.value = note.content
  isEditingExisting.value = true
}

function cancelEdit() {
  editing.value = false
  editTitle.value = ''
  editContent.value = ''
}

async function saveEdit() {
  if (!editTitle.value.trim()) {
    ElMessage.warning('Title is required')
    return
  }
  saving.value = true
  try {
    await saveNote(editTitle.value.trim(), editContent.value)
    ElMessage.success('Note saved')
    editing.value = false
    await loadNotes()
  } catch (e) {
    ElMessage.error('Failed to save note: ' + e.message)
  } finally {
    saving.value = false
  }
}

async function handleDelete(title) {
  try {
    await ElMessageBox.confirm(`Delete note "${title}"?`, 'Confirm', {
      confirmButtonText: 'Delete',
      cancelButtonText: 'Cancel',
      type: 'warning',
    })
    await deleteNote(title)
    ElMessage.success('Note deleted')
    await loadNotes()
  } catch (e) {
    if (e !== 'cancel') {
      ElMessage.error('Failed to delete note: ' + e.message)
    }
  }
}

function contentPreview(text) {
  if (!text) return ''
  let preview = text.replace(/\n/g, ' ').trim()
  if (preview.length > 150) preview = preview.slice(0, 150) + '...'
  return preview
}
</script>

<style scoped>
.note-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.note-count {
  font-size: 13px;
  color: #999;
}
.empty-notes {
  text-align: center;
  padding: 40px 20px;
  color: #999;
  font-size: 13px;
}
.note-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 55vh;
  overflow-y: auto;
}
.note-item {
  position: relative;
  border: 1px solid #e4e7ed;
  border-radius: 6px;
  padding: 10px 12px;
  cursor: pointer;
  transition: border-color 0.2s;
}
.note-item:hover {
  border-color: #409eff;
}
.note-item-title {
  font-weight: 600;
  font-size: 14px;
  margin-bottom: 4px;
  padding-right: 30px;
}
.note-item-preview {
  font-size: 12px;
  color: #666;
  line-height: 1.5;
  margin-bottom: 4px;
}
.note-item-meta {
  font-size: 11px;
  color: #999;
}
.note-item-del {
  position: absolute;
  top: 6px;
  right: 4px;
}
.editor-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.editor-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.title-input :deep(.el-input__inner) {
  font-weight: 600;
  font-size: 15px;
}
.content-input :deep(.el-textarea__inner) {
  font-family: 'SF Mono', 'Fira Code', 'Consolas', monospace;
  font-size: 13px;
  line-height: 1.6;
}
</style>
