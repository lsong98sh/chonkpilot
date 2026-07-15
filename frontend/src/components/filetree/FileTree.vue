<template>
  <div class="file-tree" @contextmenu="onTreeContextMenu">
    <el-tree
      ref="treeRef"
      lazy
      :load="loadNode"
      :props="defaultProps"
      node-key="path"
      highlight-current
      @node-click="handleNodeClick"
      @node-contextmenu="handleNodeContextMenu"
      :filter-node-method="filterNode"
    >
      <template #default="{ data }">
        <span class="tree-node">
          <el-icon class="node-icon" :size="14">
            <Folder v-if="data.is_dir" />
            <Collection v-else-if="isDBConfig(data)" />
            <Document v-else />
          </el-icon>
          <span v-show="editingPath !== data.path" class="node-label">{{ data.label }}</span>
          <el-input
            v-if="editingPath === data.path"
            ref="editInputRef"
            v-model="editingValue"
            size="small"
            class="inline-edit-input"
            @keyup.enter="confirmEdit"
            @keyup.escape="cancelEdit"
            @blur="confirmEdit"
            @mousedown.stop
            @click.stop
          />
          <el-tag v-if="isDBConfig(data)" size="small" type="info" class="db-tag">DB</el-tag>
        </span>
      </template>
    </el-tree>

    <!-- Context menu -->
    <div
      v-if="ctxMenu.visible"
      class="context-menu"
      :style="ctxMenu.style"
      @click.stop
      @contextmenu.prevent
    >
      <template v-for="item in ctxMenu.items" :key="item.key">
        <div v-if="item.type === 'separator'" class="context-menu-separator" />
        <div
          v-else
          class="context-menu-item"
          :class="{ danger: item.danger }"
          @click="handleCtxAction(item.key)"
        >
          <el-icon v-if="item.icon" :size="14"><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { Folder, Document, Collection, Plus, FolderOpened, CopyDocument, Edit, Delete, Link, Rank, Top, Upload } from '@element-plus/icons-vue'
import { ElMessageBox, ElMessage } from 'element-plus'
import { getFileTree, getFileTreeChildren, createFileInDir, createDirInDir, renameFile, deleteFilePath, duplicateFile, revealInExplorer, openWithDefault, openWithDialog } from '../../api/file'
import { useFileTree } from '../../composables/useFileTree'

const treeRef = ref(null)

const { loading, onFileChanged, teardown } = useFileTree()

const defaultProps = {
  children: 'children',
  label: 'label',
  isLeaf: 'isLeaf',
}

// ─── Context menu state ──────────────────────────

const ctxMenu = reactive({
  visible: false,
  x: 0,
  y: 0,
  data: null,
  isDir: false,
  isDB: false,
  style: {},
  items: [],
})

const dirMenu = [
  { key: 'newFile', label: '新建文件', icon: Plus },
  { key: 'newFolder', label: '新建文件夹', icon: FolderOpened },
  { type: 'separator' },
  { key: 'copyPath', label: '复制路径', icon: Link },
  { key: 'copyRelativePath', label: '复制相对路径', icon: Rank },
  { key: 'copyFilename', label: '复制文件名', icon: CopyDocument },
  { type: 'separator' },
  { key: 'revealInExplorer', label: '在资源管理器中显示', icon: Upload },
  { key: 'rename', label: '重命名', icon: Edit },
  { key: 'delete', label: '删除', icon: Delete, danger: true },
]

const fileMenu = [
  { key: 'open', label: '打开', icon: Document },
  { key: 'openWith', label: '打开方式', icon: Top },
  { type: 'separator' },
  { key: 'copyPath', label: '复制路径', icon: Link },
  { key: 'copyRelativePath', label: '复制相对路径', icon: Rank },
  { key: 'copyFilename', label: '复制文件名', icon: CopyDocument },
  { type: 'separator' },
  { key: 'revealInExplorer', label: '在资源管理器中显示', icon: Upload },
  { key: 'rename', label: '重命名', icon: Edit },
  { key: 'duplicate', label: '复制', icon: CopyDocument },
  { key: 'delete', label: '删除', icon: Delete, danger: true },
]

// ─── Inline edit state ──────────────────────────

const editingPath = ref('')   // path of node currently being edited (empty = none)
const editingValue = ref('')  // current value in the inline input
const editingOrigPath = ref('') // original path (for temp files created by newFile/newFolder)
const editingIsNew = ref(false) // true if this was just created (cancel = delete)
const editInputRef = ref(null)

function isDBConfig(data) {
  return data.path && data.path.startsWith('db://')
}

function getWorkDir() {
  // The workDir is the root of the tree; we can get it from tree root's first child's parent path,
  // or more simply from the tree state. For our purposes, basePath is the parent directory of the ctx node.
  return ''
}

function handleNodeContextMenu(event, data, node) {
  event.preventDefault()
  event.stopPropagation()

  openContextMenu(event, data)
}

function onTreeContextMenu(e) {
  e.preventDefault()
  // Check if click target is NOT on a tree node → show root directory menu
  if (!e.target.closest('.el-tree-node')) {
    const tree = treeRef.value
    if (!tree) return
    const root = tree.store?.root
    if (!root || root.childNodes.length === 0) return
    const firstPath = root.childNodes[0].data?.path
    if (!firstPath) return
    const rootPath = getParentPath(firstPath)
    if (!rootPath) return
    const rootName = rootPath.split(/[/\\]/).filter(Boolean).pop() || rootPath
    openContextMenu(e, { path: rootPath, label: rootName, is_dir: true, isRoot: true })
  }
}

function openContextMenu(event, data) {
  let x = event.clientX
  let y = event.clientY

  const isDir = data.is_dir
  const isRoot = data.isRoot

  // Build menu items: root menu removes rename
  let items
  if (isRoot) {
    items = []
    for (const item of dirMenu) {
      if (item.key === 'rename' || item.key === 'delete') continue // root: no rename, no delete
      items.push(item)
    }
  } else if (isDir) {
    items = dirMenu
  } else {
    items = fileMenu
  }

  const itemCount = items.length
  const menuHeight = itemCount * 30 + 8
  const menuWidth = 200

  if (x + menuWidth > window.innerWidth) x = window.innerWidth - menuWidth - 12
  if (y + menuHeight > window.innerHeight) y = window.innerHeight - menuHeight - 12

  ctxMenu.visible = true
  ctxMenu.x = x
  ctxMenu.y = y
  ctxMenu.data = data
  ctxMenu.isDir = isDir
  ctxMenu.isDB = isDBConfig(data)
  ctxMenu.style = { left: x + 'px', top: y + 'px' }
  ctxMenu.items = items
}

function closeContextMenu() {
  ctxMenu.visible = false
}

function documentClickHandler(e) {
  if (e.button !== 0) return // only left-click closes the context menu
  closeContextMenu()
}

// ─── Keyboard shortcuts ──────────────────────────

function onKeyDown(e) {
  // F2: inline rename the currently selected node
  if (e.key === 'F2') {
    // Don't trigger if we're already editing
    if (editingPath.value) return
    const tree = treeRef.value
    if (!tree) return
    const currentKey = tree.getCurrentKey()
    if (!currentKey) return
    const node = tree.getNode(currentKey)
    if (!node || !node.data || node.data.path.startsWith('db://')) return
    // workDir root is not a real node in tree data, skip level-0
    const targetNode = node.parent?.level === 0 ? null : node
    if (!targetNode) return
    doRename(targetNode.data.path, targetNode.data.label)
  }
}

onMounted(() => {
  document.addEventListener('click', documentClickHandler)
  document.addEventListener('keydown', onKeyDown)
  window.addEventListener('file:search', onFileSearch)
  onFileChanged((changedDir) => {
    reloadNode(changedDir)
  })
  // Auto-expand the root so the first-level files are visible immediately
  nextTick(() => reloadRoot())
})

onUnmounted(() => {
  document.removeEventListener('click', documentClickHandler)
  document.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('file:search', onFileSearch)
  teardown()
})

function onFileSearch(e) {
  nextTick(() => {
    treeRef.value?.filter(e.detail?.query || '')
  })
}

// ─── Context menu actions ────────────────────────

async function handleCtxAction(key) {
  const data = ctxMenu.data
  closeContextMenu()
  if (!data) return

  switch (key) {
    case 'newFile':
      await startInlineCreate(data.path, false)
      break
    case 'newFolder':
      await startInlineCreate(data.path, true)
      break
    case 'copyPath':
      await copyToClipboard(data.path)
      break
    case 'copyRelativePath':
      await copyRelativePath(data.path)
      break
    case 'copyFilename':
      await copyToClipboard(data.label)
      break
    case 'revealInExplorer':
      await doReveal(data.path)
      break
    case 'rename':
      await doRename(data.path, data.label)
      break
    case 'delete':
      await doDelete(data.path, data.label, data.is_dir)
      break
    case 'duplicate':
      await doDuplicate(data.path)
      break
    case 'open':
      await doOpen(data.path)
      break
    case 'openWith':
      await doOpenWith(data.path)
      break
  }
}

async function startInlineCreate(dirPath, isDir) {
  const defaultName = isDir ? 'untitled' : 'untitled'
  // Find a unique default name by retry
  let name = defaultName
  let attempt = 0
  let newPath
  while (true) {
    try {
      if (isDir) {
        const res = await createDirInDir(dirPath, name)
        newPath = res.path
      } else {
        const res = await createFileInDir(dirPath, name)
        newPath = res.path
      }
      break
    } catch (_) {
      attempt++
      if (attempt > 100) throw _
      name = `${defaultName} ${attempt + 1}`
    }
  }

  // Reload the parent directory and wait for tree to update
  await reloadNodeAsync(dirPath)

  // Find the new node and start editing
  const tree = treeRef.value
  if (tree) {
    const node = tree.getNode(newPath)
    if (node) {
      // Expand parent so the new node is visible
      const parent = tree.getNode(dirPath)
      if (parent) parent.expand()
      // Select the new node
      tree.setCurrentKey(newPath)
    }
  }

  editingPath.value = newPath
  editingValue.value = ''
  editingOrigPath.value = newPath
  editingIsNew.value = true

  await nextTick()
  // Focus and select all text in the input
  const input = editInputRef.value
  if (input) {
    input.focus()
    // el-input exposes the native input via inputRef
    const nativeInput = input.$el?.querySelector('input')
    if (nativeInput) {
      nativeInput.select()
    }
  }
}

async function confirmEdit() {
  if (!editingPath.value) return
  const path = editingPath.value
  const val = editingValue.value.trim()
  const origPath = editingOrigPath.value
  const isNew = editingIsNew.value
  const oldName = path.split(/[/\\]/).pop()

  editingPath.value = ''
  editingValue.value = ''
  editingOrigPath.value = ''
  editingIsNew.value = false

  if (!val || val === oldName) {
    // No change: if new, delete the temp file/folder
    if (isNew) {
      try {
        await deleteFilePath(origPath)
      } catch (e) { console.warn('[FileTree] Failed to delete temp file on cancel:', e) }
    }
    reloadNode(getParentPath(origPath))
    return
  }

  if (isNew) {
    try {
      await renameFile(origPath, val)
      ElMessage.success(`已创建: ${val}`)
    } catch (e) {
      ElMessage.error(e?.message || '创建失败')
      try { await deleteFilePath(origPath) } catch (_) {}
    }
  } else {
    // Rename existing file
    try {
      await renameFile(path, val)
      ElMessage.success(`已重命名为: ${val}`)
    } catch (e) {
      ElMessage.error(e?.message || '重命名失败')
    }
  }
  reloadNode(getParentPath(origPath))
}

async function cancelEdit() {
  if (!editingPath.value) return
  const path = editingPath.value
  const origPath = editingOrigPath.value
  const isNew = editingIsNew.value

  editingPath.value = ''
  editingValue.value = ''
  editingOrigPath.value = ''
  editingIsNew.value = false

  if (isNew) {
    try {
      await deleteFilePath(origPath)
      reloadNode(getParentPath(origPath))
    } catch (_) {}
  }
}

async function reloadNodeAsync(path) {
  const tree = treeRef.value
  if (!tree) return
  const node = tree.getNode(path)
  if (node && node.loaded) {
    node.loaded = false
    node.loadData()
    // Wait for lazy load to complete (local FS read is fast)
    await new Promise((r) => setTimeout(r, 200))
    return
  }
  reloadRoot()
}

async function startInlineRename(path, oldName) {
  // Cancel any ongoing inline create
  if (editingPath.value) {
    await cancelEdit()
    await new Promise((r) => setTimeout(r, 100))
  }

  // Find the tree node
  const tree = treeRef.value
  const node = tree?.getNode(path)
  if (!node) return

  // Ensure parent is expanded so node is visible
  const parent = tree?.getNode(getParentPath(path))
  if (parent && !parent.expanded) {
    parent.expand()
    await new Promise((r) => setTimeout(r, 200))
  }

  // Scroll node into view
  node.expand() // no-op for files, ensures visibility

  editingPath.value = path
  editingValue.value = oldName
  editingOrigPath.value = path
  editingIsNew.value = false

  await nextTick()
  const input = editInputRef.value
  if (input) {
    input.focus()
    const nativeInput = input.$el?.querySelector('input')
    if (nativeInput) {
      // Move cursor to end, then select all after a tick
      nativeInput.setSelectionRange(oldName.length, oldName.length)
      setTimeout(() => {
        try { nativeInput.select() } catch (e) { console.warn('[FileTree] Failed to select input:', e) }
      }, 0)
    }
  }
}

async function doRename(path, oldName) {
  if (editingPath.value) {
    // Already editing something, cancel it first
    await cancelEdit()
    await new Promise((r) => setTimeout(r, 100))
  }
  await startInlineRename(path, oldName)
}

async function doDelete(path, label, isDir) {
  try {
    const type = isDir ? '文件夹' : '文件'
    await ElMessageBox.confirm(
      `确定要删除${type} "${label}" 吗？${isDir ? '文件夹内的所有内容将被删除。' : ''}`,
      '删除确认',
      {
        confirmButtonText: '删除',
        cancelButtonText: '取消',
        type: 'warning',
        confirmButtonClass: 'el-button--danger',
      }
    )
    await deleteFilePath(path)
    ElMessage.success(`已删除: ${label}`)
    reloadNode(getParentPath(path))
  } catch (_) {
    // cancelled
  }
}

async function doDuplicate(path) {
  try {
    const res = await duplicateFile(path)
    ElMessage.success(`已复制: ${res.path}`)
    reloadNode(getParentPath(path))
  } catch (e) {
    ElMessage.error(e?.message || '复制失败')
  }
}

async function doReveal(path) {
  try {
    await revealInExplorer(path)
  } catch (e) {
    ElMessage.error(e?.message || '打开资源管理器失败')
  }
}

async function doOpen(path) {
  try {
    await openWithDefault(path)
  } catch (e) {
    ElMessage.error(e?.message || '打开失败')
  }
}

async function doOpenWith(path) {
  try {
    await openWithDialog(path)
  } catch (e) {
    ElMessage.error(e?.message || '打开失败')
  }
}

async function copyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制到剪贴板')
  } catch (_) {
    // Fallback
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
    ElMessage.success('已复制到剪贴板')
  }
}

async function copyRelativePath(absPath) {
  // Derive relative path from the tree root directory
  // We'll get workDir from the root node's first loaded child
  const tree = treeRef.value
  if (!tree) return copyToClipboard(absPath)
  const root = tree.store?.root
  let workDir = ''
  if (root && root.childNodes && root.childNodes.length > 0) {
    // The root's children have paths rooted at workDir; use the first child
    const firstChild = root.childNodes[0]
    if (firstChild && firstChild.data) {
      // Derive: node path = workDir + relative. So workDir = first file's path - first file's name
      const fp = firstChild.data.path
      if (fp) {
        const fn = firstChild.data.label
        workDir = fp.substring(0, fp.length - fn.length)
      }
    }
  }
  if (workDir && absPath.startsWith(workDir)) {
    const rel = absPath.substring(workDir.length)
    await copyToClipboard(rel)
  } else {
    await copyToClipboard(absPath)
  }
}

function getParentPath(path) {
  if (!path) return ''
  const idx = path.lastIndexOf('/')
  const bidx = path.lastIndexOf('\\')
  const sep = idx > bidx ? '/' : '\\'
  const p = path.substring(0, Math.max(path.lastIndexOf('/'), path.lastIndexOf('\\')))
  return p || ''
}

// ─── Tree operations ─────────────────────────────

/**
 * Lazy load function for el-tree.
 * node.level === 0 → root placeholder, load first-level from getFileTree.
 * Otherwise → call getFileTreeChildren(dir) for that directory.
 */
async function loadNode(node, resolve) {
  try {
    let children
    if (node.level === 0) {
      // Root: fetch first level
      const res = await getFileTree('')
      children = res.tree?.children || []
    } else {
      // Child directory: lazy load
      const res = await getFileTreeChildren(node.data.path)
      children = res.children || []
    }
    // Sort: directories first, files second
    children.sort((a, b) => (b.is_dir ? 1 : 0) - (a.is_dir ? 1 : 0))
    const treeNodes = children
      .filter((n) => n.name !== '.ide') // hide .ide directory
      .map((n) => ({
      label: n.name,
      path: n.path,
      is_dir: n.is_dir,
      children: n.children,
      isLeaf: !n.is_dir,
    }))
    resolve(treeNodes)
  } catch (_) {
    resolve([])
  }
}

/**
 * Reload a specific node's children (e.g. after a file change).
 * Falls back to reloading all visible root-level children if the exact
 * node is not found (e.g. file created directly in the root workDir).
 */
function reloadNode(path) {
  const tree = treeRef.value
  if (!tree) return
  const node = tree.getNode(path)
  if (node && node.loaded) {
    node.loaded = false
    node.loadData()
    return
  }
  // Node not found (e.g. file in root workDir or not yet visited).
  // Reload the root listing to pick up root-level changes.
  reloadRoot()
}

function reloadRoot() {
  const tree = treeRef.value
  if (!tree) return
  const root = tree.store?.root
  if (!root) return
  root.loaded = false
  root.loadData()
}

// ─── File open ───────────────────────────────────

function handleNodeClick(data) {
  if (data.is_dir) return
  if (isDBConfig(data)) {
    const key = data.path.replace('db://', '')
    window.dispatchEvent(new CustomEvent('file:open', { detail: { path: data.path, isDBConfig: true, dbKey: key } }))
  } else {
    window.dispatchEvent(new CustomEvent('file:open', { detail: { path: data.path } }))
  }
}

// ─── Filter / search ─────────────────────────────

function filterNode(value, data) {
  if (!value) return true
  return data.label.toLowerCase().includes(value.toLowerCase())
}


</script>

<style scoped>
.file-tree {
  height: 100%;
  position: relative;
}

.el-tree {
  background: transparent;
}

.tree-node {
  display: flex;
  align-items: center;
  gap: 4px;
}

.db-tag {
  transform: scale(0.75);
  margin-left: 2px;
  line-height: 14px;
  height: 16px;
  padding: 0 4px;
}

.node-icon {
  flex-shrink: 0;
  color: var(--text-secondary);
}

.el-tree :deep(.el-tree-node__content) {
  height: 28px;
}

/* ─── Context menu ────────────────────── */

.context-menu {
  position: fixed;
  z-index: 9999;
  min-width: 180px;
  padding: 4px 0;
  background: var(--bg-elevated, #fff);
  border: 1px solid var(--border, #ddd);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
}

.context-menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 14px;
  font-size: 13px;
  cursor: pointer;
  color: var(--text-primary, #333);
  white-space: nowrap;
}

.context-menu-item:hover {
  background: var(--bg-hover, #f0f0f0);
}

.context-menu-item.danger {
  color: var(--el-color-danger, #f56c6c);
}

.context-menu-item.danger:hover {
  background: var(--el-color-danger-light-9, #fef0f0);
}

.context-menu-separator {
  height: 1px;
  margin: 4px 8px;
  background: var(--border, #ddd);
}

/* ─── Inline edit ────────────────────── */

.inline-edit-input {
  width: calc(100% - 30px);
}

.inline-edit-input :deep(.el-input__wrapper) {
  padding: 0 4px;
  height: 24px;
  border-radius: 3px;
}
</style>
