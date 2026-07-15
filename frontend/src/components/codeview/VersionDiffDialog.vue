<template>
  <el-dialog
    v-model="visible"
    title="Version History & Diff"
    :width="dialogWidth"
    top="5vh"
    destroy-on-close
    draggable
    class="version-diff-dialog"
    @close="onClose"
  >
    <div class="diff-toolbar">
      <span class="diff-label">Compare with version:</span>
      <el-select v-model="selectedVersionId" size="small" placeholder="Select a version..." @change="onVersionChange">
        <el-option
          v-for="v in versions"
          :key="v.id"
          :label="formatVersionLabel(v)"
          :value="v.id"
        />
      </el-select>
      <el-button
        v-if="selectedVersion"
        size="small"
        type="danger"
        text
        :loading="restoring"
        :disabled="restoring"
        @click="restoreVersion"
      >
        <el-icon><Refresh /></el-icon> Restore This Version
      </el-button>
      <span class="diff-status">{{ diffStatus }}</span>
    </div>

    <div v-loading="loadingDiff" class="diff-container" ref="diffContainer" />
  </el-dialog>
</template>

<script setup>
import { ref, computed, watch, nextTick, onUnmounted } from 'vue'
import { ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  filePath: { type: String, default: '' },
})

const emit = defineEmits(['update:modelValue'])

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const dialogWidth = ref('85%')
const versions = ref([])
const selectedVersionId = ref(null)
const selectedVersion = computed(() => versions.value.find(v => v.id === selectedVersionId.value))
const loadingDiff = ref(false)
const diffStatus = ref('')
const restoring = ref(false)
const diffContainer = ref(null)
let monacoDiffInstance = null

function formatVersionLabel(v) {
  const d = new Date(v.created_at)
  const time = d.toLocaleTimeString()
  const turn = v.turn_id ? v.turn_id.substring(0, 8) : ''
  return `#${v.id}  ${time}  (turn: ${turn})`
}

async function loadVersions() {
  if (!props.filePath) return
  try {
    const result = await window.go.main.App.GetFileVersions(props.filePath)
    versions.value = result || []
    if (versions.value.length > 0) {
      selectedVersionId.value = versions.value[versions.value.length - 1].id
      await loadDiff()
    } else {
      diffStatus.value = 'No versions available'
    }
  } catch (e) {
    console.error('[VersionDiff] loadVersions error:', e)
    diffStatus.value = 'Failed to load versions'
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
    // Read current file content via the existing API
    const currentResp = await (await import('../../api/file')).readFile(props.filePath)
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

async function showDiff(oldContent, newContent) {
  if (monacoDiffInstance) {
    monacoDiffInstance.dispose()
    monacoDiffInstance = null
  }
  await nextTick()
  if (!diffContainer.value) return

  try {
    const monaco = await import('monaco-editor')
    await nextTick()

    const origModel = monaco.editor.createModel(oldContent, 'plaintext')
    const modModel = monaco.editor.createModel(newContent, 'plaintext')

    monacoDiffInstance = monaco.editor.createDiffEditor(diffContainer.value, {
      originalEditable: false,
      readOnly: true,
      fontSize: 13,
      minimap: { enabled: false },
      renderSideBySide: true,
      theme: document.documentElement.getAttribute('data-theme') === 'dark' ? 'vs-dark' : 'vs',
      automaticLayout: true,
    })
    monacoDiffInstance.setModel({ original: origModel, modified: modModel })
  } catch (e) {
    console.error('[VersionDiff] Monaco init failed:', e)
  }
}

async function restoreVersion() {
  if (!selectedVersionId.value) return
  try {
    await ElMessageBox.confirm(
      '恢复后将覆盖当前文件内容。确定要恢复此版本吗？',
      '确认恢复',
      { confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning' }
    )
  } catch {
    return // user cancelled
  }
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
}

function onVersionChange() {
  loadDiff()
}

function onClose() {
  if (monacoDiffInstance) {
    monacoDiffInstance.dispose()
    monacoDiffInstance = null
  }
}

watch(() => props.filePath, () => {
  if (props.filePath) {
    loadVersions()
  }
})

onUnmounted(() => {
  if (monacoDiffInstance) {
    monacoDiffInstance.dispose()
    monacoDiffInstance = null
  }
})
</script>

<style scoped>
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
.diff-toolbar .el-select {
  width: 280px;
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
