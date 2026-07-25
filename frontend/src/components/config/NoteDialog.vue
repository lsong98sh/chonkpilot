<template>
  <div />
</template>

<script setup>
import { ref, h, defineComponent } from 'vue'
import { Button, confirm, message } from '../ui'
import Icon from '../icon/Icon.vue'
import { dialog } from '../dialog'
import { getNotes, getNote, saveNote, deleteNote } from '../../api/note'

function contentPreview(text) {
  if (!text) return ''
  let preview = text.replace(/\n/g, ' ').trim()
  if (preview.length > 150) preview = preview.slice(0, 150) + '...'
  return preview
}

function open() {
  const editing = ref(false)
  const editTitle = ref('')
  const editContent = ref('')
  const isEditingExisting = ref(false)
  const saving = ref(false)
  const notes = ref([])

  async function loadNotes() {
    try {
      const res = await getNotes()
      notes.value = res.notes || []
    } catch (e) {
      message.error('Failed to load notes: ' + e.message)
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
      message.warning('Title is required')
      return
    }
    saving.value = true
    try {
      await saveNote(editTitle.value.trim(), editContent.value)
      message.success('Note saved')
      editing.value = false
      await loadNotes()
    } catch (e) {
      message.error('Failed to save note: ' + e.message)
    } finally {
      saving.value = false
    }
  }

  async function handleDelete(title) {
    try {
      const confirmed = await confirm(`Delete note "${title}"?`, 'Confirm')
      if (!confirmed) return
      await deleteNote(title)
      message.success('Note deleted')
      await loadNotes()
    } catch (e) {
      message.error('Failed to delete note: ' + e.message)
    }
  }

  loadNotes()

  const handle = dialog.show(defineComponent({
    setup() {
      return () => {
        if (editing.value) {
          return h('div', { class: 'editor-section' }, [
            h('input', {
              class: 'b-input title-input',
              value: editTitle.value,
              onInput: (e) => { editTitle.value = e.target.value },
              placeholder: 'Note title',
              disabled: isEditingExisting.value,
            }),
            h('textarea', {
              class: 'b-textarea content-input',
              value: editContent.value,
              onInput: (e) => { editContent.value = e.target.value },
              rows: 12,
              placeholder: 'Write your note here...',
            }),
            h('div', { class: 'editor-footer' }, [
              h(Button, { size: 'small', onClick: cancelEdit }, () => 'Cancel'),
              h(Button, { size: 'small', type: 'primary', onClick: saveEdit, loading: saving.value }, () => 'Save'),
            ]),
          ])
        }

        return [
          h('div', { class: 'note-toolbar' }, [
            h('span', { class: 'note-count' }, `${notes.value.length} notes`),
            h(Button, { size: 'small', type: 'primary', onClick: startNew }, () => [
              h(Icon, { name: 'plus', size: 14 }),
              ' New Note',
            ]),
          ]),
          notes.value.length === 0
            ? h('div', { class: 'empty-notes' }, [
                h('p', null, 'No notes yet. Notes let the LLM record test results, compilation issues, or important discussion points.'),
              ])
            : h('div', { class: 'note-list' }, notes.value.map(n =>
                h('div', {
                  key: n.title,
                  class: 'note-item',
                  onClick: () => startEdit(n),
                }, [
                  h('div', { class: 'note-item-title' }, n.title),
                  h('div', { class: 'note-item-preview' }, contentPreview(n.content)),
                  h('div', { class: 'note-item-meta' }, `Updated: ${n.updated_at.slice(0, 10)}`),
                  h(Button, {
                    text: true,
                    size: 'small',
                    type: 'danger',
                    class: 'note-item-del',
                    onClick: (e) => { e.stopPropagation(); handleDelete(n.title) },
                  }, () => h(Icon, { name: 'delete', size: 14 })),
                ])
              )),
        ]
      }
    }
  }), {
    title: '📝 Notes',
    width: 700,
    closable: true,
    bodyClass: 'notes-dialog-body',
    onAction: (action) => {
      if (action === 'close' || action === 'cancel') handle.close()
    }
  })
}

defineExpose({ open })
</script>

<style>
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
</style>
