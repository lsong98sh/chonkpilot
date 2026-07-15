<template>
  <div class="code-view" :class="{ empty: !currentFile }">
    <template v-if="currentFile">
      <div class="code-header">
        <!-- Tree selector when filetree is hidden -->
        <el-select
          v-if="!filetreeVisible"
          v-model="treeSelectValue"
          size="small"
          class="tree-selector"
          placeholder="Select file..."
          filterable
          @change="onTreeSelect"
        >
          <el-option
            v-for="f in flatFiles"
            :key="f.path"
            :label="f.name"
            :value="f.path"
          />
        </el-select>
        <span class="file-path">{{ currentFile.path }}</span>
        <span v-if="fileDeleted" class="deleted-badge">DELETED</span>
        <span class="file-type-tag">{{ renderType }}</span>
        <!-- DB Config action bar -->
        <template v-if="isDBConfig">
          <el-tag size="small" type="warning" effect="plain" class="db-config-tag">DB Config</el-tag>
          <el-button size="small" text type="primary" @click="openDBConfigInSettings">
            <el-icon><Setting /></el-icon> Open in Settings
          </el-button>
        </template>
        <!-- Source/Preview toggle for markdown and html -->
        <span v-if="renderType === 'markdown' || renderType === 'html'" class="source-toggle">
          <el-button text size="small" :type="showSource ? 'primary' : ''" @click="showSource = true">Code</el-button>
          <el-button text size="small" :type="!showSource ? 'primary' : ''" @click="showSource = false">Preview</el-button>
        </span>
        <!-- Version history / diff button (code files only) -->
        <el-button v-if="renderType === 'code'" size="small" text type="info" @click="showVersionDiff = true">
          <el-icon><Clock /></el-icon> Diff With
        </el-button>
      </div>
      <!-- Version diff dialog -->
      <VersionDiffDialog v-model="showVersionDiff" :file-path="currentFile.path" />
      <div class="code-content" :class="{ 'no-pad': noPadTypes.includes(renderType) }">
        <!-- Loading -->
        <div v-if="loading" class="loading-state">
          <el-icon class="is-loading" :size="24"><Loading /></el-icon>
          <span>Loading...</span>
        </div>
        <!-- Code via Monaco Editor -->
        <div v-else-if="renderType === 'code'" ref="monacoContainer" class="monaco-container" />
        <!-- Markdown: preview mode (default) -->
        <MarkdownRender
          v-else-if="renderType === 'markdown' && !showSource"
          :content="codeContent"
          class="markdown-preview"
        />
        <!-- Markdown: source mode -->
        <pre v-else-if="renderType === 'markdown' && showSource" class="source-code"><code>{{ codeContent }}</code></pre>
        <!-- PDF via native iframe (most reliable for browser PDF viewer) -->
        <iframe
          v-else-if="renderType === 'pdf'"
          :src="fileRawUrl"
          class="pdf-preview"
          frameborder="0"
        />
        <!-- HTML: preview mode (default) -->
        <iframe
          v-else-if="renderType === 'html' && !showSource"
          :src="fileRawUrl"
          class="html-preview"
          frameborder="0"
        />
        <!-- HTML: source mode -->
        <pre v-else-if="renderType === 'html' && showSource" class="source-code"><code>{{ codeContent }}</code></pre>
        <!-- Image via native img with wheel zoom + drag-to-pan via scroll -->
        <div
          v-else-if="renderType === 'image'"
          ref="imageContainer"
          class="image-preview-area"
          @wheel.prevent="onImgWheel"
          @mousedown="onImgMouseDown"
        >
          <img
            :src="fileRawUrl"
            class="preview-image"
            :style="imgStyle"
            alt="preview"
            @load="onImgLoad"
            draggable="false"
          />
          <span class="image-zoom-label">{{ Math.round(imageZoom * 100) }}%</span>
        </div>
        <!-- Office via Flyfish FileViewer (docx/xlsx/pptx + legacy doc/xls/ppt) -->
        <FileViewer
          v-else-if="renderType === 'docx' || renderType === 'xlsx' || renderType === 'pptx'"
          :url="fileRawUrl"
          :name="currentFile.name"
          class="file-preview-container"
        />
        <!-- Audio -->
        <audio
          v-else-if="renderType === 'audio'"
          :src="fileRawUrl"
          controls
          class="media-preview"
        />
        <!-- Video -->
        <video
          v-else-if="renderType === 'video'"
          :src="fileRawUrl"
          controls
          class="media-preview"
        />
        <!-- Hex dump for unknown binary files -->
        <HexView
          v-else-if="renderType === 'hex'"
          :path="currentFile.path"
          class="file-preview-container"
        />
        <!-- Unsupported -->
        <div v-else-if="renderType === 'unsupported'" class="unsupported-state">
          <el-icon :size="48" color="var(--text-muted)"><WarningFilled /></el-icon>
          <p>Preview not available for this file type</p>
        </div>
        <!-- Project Config (ide.db) -->
        <ProjectConfig v-else-if="renderType === 'ide-config'" />
        <!-- Plain text fallback -->
        <pre v-else><code>{{ codeContent }}</code></pre>
      </div>
    </template>
    <template v-else>
      <div class="empty-state">
        <el-icon :size="48" color="var(--text-muted)"><Document /></el-icon>
        <p>Select a file to preview</p>
        <p class="hint">Choose a file from the explorer sidebar</p>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { Clock, Document, Loading, WarningFilled } from '@element-plus/icons-vue'
import { FileViewer } from '@file-viewer/vue3'
import '@file-viewer/vue3/dist/file-viewer3.css'
import '@file-viewer/preset-office'
import MarkdownRender from '@ashlesss/markstream-vue'
import HexView from './HexView.vue'
import '@ashlesss/markstream-vue/index.css'
import { readFile, getFileTree, getFileUrl, getFileUrlSync, warmUpHttpConfig } from '../../api/file'
import ProjectConfig from '../preview/ProjectConfig.vue'
import VersionDiffDialog from './VersionDiffDialog.vue'
import bridge from '../../utils/bridge'

const props = defineProps({
  filetreeVisible: { type: Boolean, default: true },
})

const currentFile = ref(null)
const fileDeleted = ref(false)
const codeContent = ref('')
const loading = ref(false)
const monacoContainer = ref(null)
const showSource = ref(false)
const imageZoom = ref(1)
const naturalSize = ref({ w: 0, h: 0 })
const imageContainer = ref(null)
const imageDrag = ref(null) // { startX, startY, scrollLeft, scrollTop }
const flatFiles = ref([])
const treeSelectValue = ref('')
const rawUrlKey = ref(0) // increment to re-evaluate fileRawUrl after HTTP cache is ready
const showVersionDiff = ref(false)
let monacoInstance = null

// Flatten file tree for dropdown selector
function flattenTree(node, list = []) {
  if (!node) return list
  if (node.type === 'file') {
    list.push({ path: node.path, name: node.name })
  }
  if (node.children) {
    for (const child of node.children) {
      flattenTree(child, list)
    }
  }
  return list
}

async function loadFileTree() {
  try {
    const res = await getFileTree()
    flatFiles.value = flattenTree(res.tree)
  } catch (e) {
    console.error('Failed to load file tree:', e)
  }
}

function onTreeSelect(path) {
  if (path) {
    window.dispatchEvent(new CustomEvent('file:open', { detail: { path } }))
  }
}

// Image zoom: explicit pixel size for all zoom levels → scroll bars appear when > container
const imgStyle = computed(() => {
  const z = imageZoom.value
  const { w, h } = naturalSize.value
  if (w && h) {
    return {
      width: `${w * z}px`,
      height: `${h * z}px`,
      maxWidth: 'none',
      maxHeight: 'none',
    }
  }
  return { maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }
})

function onImgLoad(e) {
  naturalSize.value = { w: e.target.naturalWidth, h: e.target.naturalHeight }
}

function onImgWheel(e) {
  const delta = e.deltaY > 0 ? -0.05 : 0.05
  imageZoom.value = Math.max(0.1, Math.min(5, Math.round((imageZoom.value + delta) * 100) / 100))
}

// Image drag-to-pan: uses scrollLeft/scrollTop, works at any zoom level with scrollbars
function onImgMouseDown(e) {
  if (!imageContainer.value) return
  // Only left button
  if (e.button !== 0) return
  imageDrag.value = {
    startX: e.clientX,
    startY: e.clientY,
    scrollLeft: imageContainer.value.scrollLeft,
    scrollTop: imageContainer.value.scrollTop,
  }
  imageContainer.value.style.cursor = 'grabbing'
  window.addEventListener('mousemove', onImgWindowMouseMove)
  window.addEventListener('mouseup', onImgMouseUp)
}

function onImgWindowMouseMove(e) {
  if (!imageDrag.value || !imageContainer.value) return
  const dx = e.clientX - imageDrag.value.startX
  const dy = e.clientY - imageDrag.value.startY
  imageContainer.value.scrollLeft = imageDrag.value.scrollLeft - dx
  imageContainer.value.scrollTop = imageDrag.value.scrollTop - dy
}

function onImgMouseUp() {
  if (imageContainer.value) {
    imageContainer.value.style.cursor = ''
  }
  imageDrag.value = null
  window.removeEventListener('mousemove', onImgWindowMouseMove)
  window.removeEventListener('mouseup', onImgMouseUp)
}

// File type lists
const codeExtensions = ['js', 'ts', 'py', 'go', 'java', 'css', 'json', 'xml', 'yaml', 'yml', 'sh', 'bat', 'sql', 'rs', 'vue', 'cpp', 'c', 'h', 'hpp', 'swift', 'kt', 'rb', 'php', 'pl', 'r', 'm', 'properties', 'log', 'yamllog']
const noPadTypes = ['code', 'pdf', 'docx', 'xlsx', 'pptx', 'image', 'audio', 'video', 'markdown', 'html', 'ide-config']
// flyfish handles: docx, xlsx, pptx (also doc/xls/ppt)
// image -> native img
// pdf -> native iframe
// markdown -> markstream-vue
const binaryTypes = ['docx', 'xlsx', 'pptx', 'image', 'audio', 'video']
const hexExtensions = ['exe', 'dll', 'so', 'bin', 'obj', 'lib', 'dylib', 'class', 'pyc', 'o', 'a', 'out', 'wasm', 'dat']
const unsupportedExtensions = ['zip', '7z', 'rar', 'tar', 'gz']

// Map binary file extensions to preview types handled by flyfish
function getBinaryType(ext) {
  const docx = ['docx', 'doc']
  const xlsx = ['xlsx', 'xls']
  const pptx = ['pptx', 'ppt']
  const images = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico', 'bmp']
  const audio = ['mp3', 'wav', 'wma', 'ogg']
  const video = ['mp4', 'webm', 'mkv', 'avi', 'mov']
  if (docx.includes(ext)) return 'docx'
  if (xlsx.includes(ext)) return 'xlsx'
  if (pptx.includes(ext)) return 'pptx'
  if (images.includes(ext)) return 'image'
  if (audio.includes(ext)) return 'audio'
  if (video.includes(ext)) return 'video'
  return ''
}

function getExtension(path) {
  return path?.split('.').pop()?.toLowerCase() || ''
}

// Detect backup/temp files: *~, ~$*, *.swp, *.swo
function isBackupFile(path) {
  if (!path) return false
  const name = path.split('\\').pop()?.split('/').pop() || ''
  return name.endsWith('~') || name.startsWith('~$') || name.endsWith('.swp') || name.endsWith('.swo')
}

// Detect ide.db file (must be named ide.db exactly, not based on extension)
function isIdeDbFile(path) {
  if (!path) return false
  const name = path.split('\\').pop()?.split('/').pop() || ''
  return name === 'ide.db'
}

const fileRawUrl = computed(() => {
  // rawUrlKey triggers recomputation after HTTP cache is warmed up
  void rawUrlKey.value
  if (!currentFile.value) return ''
  return getFileUrlSync(currentFile.value.path)
})

const renderType = computed(() => {
  if (!currentFile.value) return ''
  const path = currentFile.value.path
  // ide.db → full IDE config editor (check before db:// since db://ide.db is also IDE config)
  if (isIdeDbFile(path)) return 'ide-config'
  // DB config → no file content, navigation to settings handled in handleFileOpen
  if (path && path.startsWith('db://')) return 'none'
  // Backup files → unsupported
  if (isBackupFile(path)) return 'unsupported'
  const ext = getExtension(path)
  if (ext === 'md') return 'markdown'
  if (ext === 'pdf') return 'pdf'
  if (codeExtensions.includes(ext)) return 'code'
  if (ext === 'html' || ext === 'htm') return 'html'
  const binType = getBinaryType(ext)
  if (binType) return binType
  if (hexExtensions.includes(ext)) return 'hex'
  if (unsupportedExtensions.includes(ext)) return 'unsupported'
  // Default to text fallback
  return 'text'
})

const isDBConfig = computed(() => {
  const path = currentFile.value?.path
  return (path?.startsWith('db://') && path !== 'db://ide.db') || false
})

function openDBConfigInSettings() {
  if (!currentFile.value?.path) return
  const key = currentFile.value.path.replace('db://', '')
  // Map config key to Settings tab name
  const tabMap = {
    project_llms: 'llm',
    project_agents: 'agents',
    project_tools: 'tools',
  }
  const tab = tabMap[key] || 'project'
  window.dispatchEvent(new CustomEvent('config:open-tab', { detail: { tab } }))
}

function getMonacoLanguage(filePath) {
  const ext = getExtension(filePath)
  const map = {
    js: 'javascript', ts: 'typescript', py: 'python', go: 'go',
    java: 'java', css: 'css', json: 'json', xml: 'xml',
    yaml: 'yaml', yml: 'yaml', sh: 'shell', bat: 'bat',
    sql: 'sql', rs: 'rust', vue: 'html', cpp: 'cpp',
    c: 'c', h: 'cpp', hpp: 'cpp', swift: 'swift',
    kt: 'kotlin', rb: 'ruby', php: 'php', pl: 'perl',
    r: 'r', m: 'objective-c',
    properties: 'properties', log: 'plaintext', yamllog: 'yaml',
  }
  return map[ext] || 'plaintext'
}

async function initMonaco() {
  if (monacoInstance) return
  try {
    const monaco = await import('monaco-editor')
    await nextTick()
    if (monacoContainer.value) {
      monacoInstance = monaco.editor.create(monacoContainer.value, {
        value: codeContent.value || '',
        language: getMonacoLanguage(currentFile.value?.path || ''),
        readOnly: true,
        fontSize: 13,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        lineNumbers: 'on',
        renderWhitespace: 'selection',
        theme: document.documentElement.getAttribute('data-theme') === 'dark' ? 'vs-dark' : 'vs',
        automaticLayout: true,
      })
    }
  } catch (e) {
    console.error('Monaco init failed', e)
  }
}

async function setupMonaco() {
  if (renderType.value !== 'code') return
  await nextTick()
  if (!monacoInstance && monacoContainer.value) {
    await initMonaco()
  }
  if (monacoInstance) {
    monacoInstance.setValue(codeContent.value || '')
    try {
      const monaco = await import('monaco-editor')
      const lang = getMonacoLanguage(currentFile.value?.path || '')
      const model = monacoInstance.getModel()
      if (model) {
        monaco.editor.setModelLanguage(model, lang)
      }
    } catch (e) { console.warn('[CodeView] Failed to set monaco language:', e) }
  }
}

async function handleFileOpen(event) {
  try {
    const path = event.detail?.path
    if (!path) return

    console.log('[CodeView] file:open', path, 'isIdeDbFile:', isIdeDbFile(path))

    fileDeleted.value = false
    loading.value = true
    currentFile.value = { path, name: path.split('\\').pop()?.split('/').pop() || path }
    codeContent.value = ''
    showSource.value = false

    // Clean up old Monaco instance
    if (monacoInstance) {
      monacoInstance.dispose()
      monacoInstance = null
    }

    // DB config → navigate to settings panel (unless it's ide.db preview)
    if ((event.detail?.isDBConfig || path.startsWith('db://')) && path !== 'db://ide.db') {
      const key = event.detail?.dbKey || path.replace('db://', '')
      const tabMap = { project_agents: 'agents', project_tools: 'tools' }
      const tab = tabMap[key] || 'project'
      window.dispatchEvent(new CustomEvent('config:open-tab', { detail: { tab } }))
      loading.value = false
      return
    }

    const ext = getExtension(path)

    // ide.db → don't try to read SQLite binary, ProjectConfig handles it
    if (isIdeDbFile(path)) {
      console.log('[CodeView] rendering ProjectConfig for:', path)
      loading.value = false
      return
    }

    try {
      const binType = getBinaryType(ext)
      const isPdf = ext === 'pdf'
      if (binType || isPdf || hexExtensions.includes(ext) || unsupportedExtensions.includes(ext)) {
        // Binary/raw files: just set the URL, Flyfish/iframe/audio/video/HexView handles the rest
      } else {
        // Text files: read content for Monaco / markdown / html source / plain text
        const result = await readFile(path)
        codeContent.value = result.content || ''
      }
    } catch (err) {
      console.error('Failed to read file:', err)
      codeContent.value = ''
    } finally {
      loading.value = false
      if (renderType.value === 'code') {
        await setupMonaco()
      }
    }
  } catch (err) {
    console.error('[CodeView] handleFileOpen error:', err)
    loading.value = false
  }
}

watch(renderType, async () => {
  if (renderType.value !== 'code' && monacoInstance) {
    monacoInstance.dispose()
    monacoInstance = null
  }
})

/**
 * Handle file:changed event — if the currently open file was modified,
 * re-read its content and update the editor.
 */
async function onFileChanged(data) {
  const changedPath = data?.path
  if (!changedPath || !currentFile.value) return

  const ext = getExtension(changedPath)
  // Skip binary files, DB config, ide.db — those don't need live reload
  if (getBinaryType(ext) || ext === 'pdf' || hexExtensions.includes(ext) || unsupportedExtensions.includes(ext)) return
  if (currentFile.value.path.startsWith('db://') || isIdeDbFile(currentFile.value.path)) return

  // Normalize paths for comparison (both are platform-native full paths)
  if (changedPath !== currentFile.value.path) return

  console.log('[CodeView] file changed externally:', changedPath, 'op:', data.op)

  // Mark as deleted if the file was removed
  if (data.op === 'deleted') {
    fileDeleted.value = true
    return
  }

  console.log('[CodeView] file changed externally, reloading:', changedPath)
  try {
    const result = await readFile(changedPath)
    const newContent = result.content || ''
    if (newContent !== codeContent.value) {
      codeContent.value = newContent
      // If Monaco is active, update its content directly
      if (monacoInstance) {
        monacoInstance.setValue(newContent)
      }
    }
  } catch (err) {
    console.error('[CodeView] failed to reload changed file:', err)
  }
}

let unsubFileChanged = null

onMounted(() => {
  window.addEventListener('file:open', handleFileOpen)
  unsubFileChanged = bridge.on('file:changed', onFileChanged)
  loadFileTree()
  // Warm up HTTP config cache for binary file previews
  warmUpHttpConfig().then(() => { rawUrlKey.value++ }).catch(() => {})
})

onUnmounted(() => {
  window.removeEventListener('file:open', handleFileOpen)
  if (unsubFileChanged) unsubFileChanged()
  if (monacoInstance) {
    monacoInstance.dispose()
    monacoInstance = null
  }
  onImgMouseUp() // clean up image drag listeners
})
</script>

<style scoped>
.code-view {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
}

.code-view.empty {
  align-items: center;
  justify-content: center;
}

.code-header {
  display: flex;
  align-items: center;
  padding: 6px 16px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
  font-size: 13px;
  color: var(--text-secondary);
  flex-shrink: 0;
  gap: 8px;
}

.file-path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree-selector {
  width: 180px;
  margin-right: 12px;
  flex-shrink: 0;
}

.file-type-tag {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--bg-surface);
  color: var(--text-muted);
  text-transform: uppercase;
  flex-shrink: 0;
}

.deleted-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  background: #f56c6c;
  color: #fff;
  font-weight: 600;
  margin-left: 8px;
}

.code-content {
  flex: 1;
  overflow: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
}

.code-content.no-pad {
  padding: 0;
}

.code-content pre {
  margin: 0;
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 1.6;
}

.code-content code {
  color: var(--text-primary);
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  height: 100%;
  color: var(--text-muted);
  font-size: 14px;
}

/* Monaco fills container */
.monaco-container {
  width: 100%;
  flex: 1;
  min-height: 0;
}

/* FileViewer (Flyfish) + media preview container */
.file-preview-container {
  width: 100%;
  height: 100%;
  flex: 1;
  min-height: 0;
}

.media-preview {
  max-width: 100%;
  max-height: 100%;
  display: block;
  margin: auto;
}

/* PDF iframe preview */
.pdf-preview {
  width: 100%;
  flex: 1;
  min-height: 0;
  border: none;
}

/* HTML iframe preview */
.html-preview {
  width: 100%;
  flex: 1;
  min-height: 0;
  border: none;
}


/* Markdown preview via markstream-vue */
.markdown-preview {
  width: 100%;
  flex: 1;
  min-height: 0;
  padding: 16px;
  overflow: auto;
  color: var(--text-primary);
  font-size: 14px;
  line-height: 1.7;
}

/* Source/Preview toggle */
.source-toggle {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
  flex-shrink: 0;
}

/* Image preview area: scrollable container */
.image-preview-area {
  width: 100%;
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
  position: relative;
  cursor: grab;
}

.image-preview-area:active {
  cursor: grabbing;
}

.preview-image {
  display: block;
  flex-shrink: 0;
  user-select: none;
  -webkit-user-drag: none;
}

.image-zoom-label {
  position: absolute;
  bottom: 8px;
  right: 8px;
  background: rgba(0,0,0,0.6);
  color: #fff;
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 4px;
  pointer-events: none;
  font-variant-numeric: tabular-nums;
}

/* Source code view */
.source-code {
  margin: 0;
  padding: 16px;
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 1.6;
  overflow: auto;
}
.source-code code {
  color: var(--text-primary);
}

.unsupported-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  text-align: center;
  color: var(--text-muted);
}

.unsupported-state p { margin-top: 16px; font-size: 14px; }

.empty-state {
  text-align: center;
  color: var(--text-muted);
}

.empty-state p { margin-top: 16px; font-size: 14px; }
.empty-state .hint { font-size: 12px; margin-top: 8px; }
</style>
