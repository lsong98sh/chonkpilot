<template>
  <div />
</template>

<script setup>
import { ref, h, defineComponent, nextTick, onUnmounted } from 'vue'
import { Button } from '../ui'
import Icon from '../icon/Icon.vue'
import { dialog } from '../dialog'
import { readFile } from '../../api/file'

const props = defineProps({
  filePath: { type: String, default: '' },
})

let monacoDiffInstance = null

function formatVersionLabel(v) {
  const d = new Date(v.created_at)
  const time = d.toLocaleTimeString()
  const turn = v.turn_id ? v.turn_id.substring(0, 8) : ''
  return `#${v.id}  ${time}  (turn: ${turn})`
}

async function open(filePath) {
  const versions = await loadVersions(filePath)
  const selectedVersionId = ref(versions.length > 0 ? versions[versions.length - 1].id : null)
  const loadingDiff = ref(false)
  const diffStatus = ref(versions.length > 0 ? '' : 'No versions available')
  const restoring = ref(false)

  const handle = dialog.show(defineComponent({
    setup() {
      const diffContainer = ref(null)
      let innerMonacoDiffInstance = null

      async function showDiff(oldContent, newContent) {
        if (innerMonacoDiffInstance) {
          innerMonacoDiffInstance.dispose()
          innerMonacoDiffInstance = null
        }
        await nextTick()
        if (!diffContainer.value) return
        try {
          const monaco = await import('monaco-editor')
          await nextTick()
          const origModel = monaco.editor.createModel(oldContent, 'plaintext')
          const modModel = monaco.editor.createModel(newContent, 'plaintext')
          innerMonacoDiffInstance = monaco.editor.createDiffEditor(diffContainer.value, {
            originalEditable: false,
            readOnly: true,
            fontSize: 13,
            minimap: { enabled: false },
            renderSideBySide: true,
            theme: document.documentElement.getAttribute('data-theme') === 'dark' ? 'vs-dark' : 'vs',
            automaticLayout: true,
          })
          innerMonacoDiffInstance.setModel({ original: origModel, modified: modModel })
          monacoDiffInstance = innerMonacoDiffInstance
        } catch (e) {
          console.error('[VersionDiff] Monaco init failed:', e)
        }
      }

      async function loadDiff() {
        if (!selectedVersionId.value) return
        loadingDiff.value = true
        diffStatus.value = ''
        try {
          const vc = await window.go.main.App.GetVersionContent(selectedVersionId.value)
          if (!vc) {
            diffStatus.value = 'Version content not found'
            return
          }
          const oldContent = vc.Content || ''
          const currentResp = await readFile(filePath)
          const newContent = currentResp?.content || ''
          await nextTick()
          await showDiff(oldContent, newContent)
          diffStatus.value = `Showing version #${vc.id} (${new Date(vc.created_at).toLocaleString()})`
        } catch (e) {
          console.error('[VersionDiff] loadDiff error:', e)
          diffStatus.value = 'Failed to load diff'
        } finally {
          loadingDiff.value = false
        }
      }

      // Load initial diff
      if (selectedVersionId.value) {
        nextTick(() => loadDiff())
      }

      return () => h('div', null, [
        h('div', { class: 'diff-toolbar' }, [
          h('span', { class: 'diff-label' }, 'Compare with version:'),
          h('select', {
            class: 'b-select',
            value: selectedVersionId.value,
            onChange: (e) => {
              const val = e.target.value ? Number(e.target.value) : null
              selectedVersionId.value = val
              if (val) loadDiff()
            },
            style: { width: '280px' },
          }, [
            h('option', { value: '', disabled: true }, 'Select a version...'),
            ...versions.map(v =>
              h('option', { key: v.id, value: v.id }, formatVersionLabel(v))
            ),
          ]),
          selectedVersionId.value ? h(Button, {
            size: 'small',
            type: 'danger',
            text: true,
            loading: restoring.value,
            disabled: restoring.value,
            onClick: async () => {
              try {
                await confirm(
                  '恢复后将覆盖当前文件内容。确定要恢复此版本吗？',
                  '确认恢复',
                  { confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning' }
                )
              } catch { return }
              restoring.value = true
              try {
                await window.go.main.App.RestoreVersion(selectedVersionId.value)
                diffStatus.value = 'File restored! Reloading diff...'
                await loadDiff()
              } catch (e) {
                console.error('[VersionDiff] restore error:', e)
                diffStatus.value = 'Restore failed: ' + (e.message || e)
              } finally {
                restoring.value = false
              }
            },
          }, { default: () => [h(Icon, { name: 'refresh', size: 14, style: { marginRight: '4px' } }), ' Restore This Version'] }) : null,
          h('span', { class: 'diff-status' }, diffStatus.value),
        ]),
        h('div', {
          ref: diffContainer,
          class: 'diff-container',
          vLoading: loadingDiff.value,
        }),
      ])
    }
  }), {
    title: 'Version History & Diff',
    width: '85%',
    height: '70vh',
    closable: true,
    bodyClass: 'version-diff-body',
    onAction: (action) => {
      if (action === 'close' || action === 'cancel') {
        if (monacoDiffInstance) {
          monacoDiffInstance.dispose()
          monacoDiffInstance = null
        }
        handle.close()
      }
    }
  })
}

async function loadVersions(filePath) {
  if (!filePath) return []
  try {
    const result = await window.go.main.App.GetFileVersions(filePath)
    return result || []
  } catch (e) {
    console.error('[VersionDiff] loadVersions error:', e)
    return []
  }
}

onUnmounted(() => {
  if (monacoDiffInstance) {
    monacoDiffInstance.dispose()
    monacoDiffInstance = null
  }
})

defineExpose({ open })
</script>

<style>
.diff-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  flex-shrink: 0;
}
.diff-label {
  font-size: 13px;
  color: var(--text-secondary);
  white-space: nowrap;
}
.diff-status {
  font-size: 12px;
  color: var(--text-muted);
  margin-left: auto;
  white-space: nowrap;
}
.diff-container {
  height: 65vh;
  border: 1px solid var(--border);
  border-radius: 4px;
  overflow: hidden;
}
</style>
