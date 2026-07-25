<template>
  <div class="file-tree" @contextmenu="onTreeContextMenu">
    <TreeNode
      v-for="node in treeData"
      :key="node.path"
      :node="node"
      :depth="0"
      :selected-key="selectedKey"
      :editing-path="editingPath"
      :editing-value="editingValue"
      :search-query="searchQuery"
      @toggle="onToggle"
      @row-click="onRowClick"
      @context-menu="onRowContextMenu"
      @update:editing-value="editingValue = $event"
      @confirm-edit="confirmEdit"
      @cancel-edit="cancelEdit"
    />

    <div v-if="treeData.length === 0" class="tree-empty">
      <p v-if="_cachedWorkDir">文件夹为空</p>
      <p v-else>尚未打开项目</p>
    </div>

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
          <Icon v-if="item.icon" :name="item.icon" :size="14" />
          <span>{{ item.label }}</span>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, nextTick, onMounted, onUnmounted } from 'vue'
import Icon from '../icon/Icon.vue'
import { confirm, message } from '../ui'
import { getFileTree, getFileTreeChildren, createFileInDir, createDirInDir, renameFile, deleteFilePath, duplicateFile, revealInExplorer, openWithDefault, openWithDialog, loadInitData, saveFileTreeState, saveWindowState } from '../../api/file'
import { onFileChanged } from '../../utils/fileTree'
import bridge from '../../utils/bridge'
import TreeNode from './TreeNode.vue'

const treeData = ref([])
const selectedKey = ref('')
const searchQuery = ref('')

const _cachedWorkDir = ref('')
let _unsubCapture = null
let _unsubSet = null
const _cleanup = []

// onFileChanged imported from utils/fileTree

const _navFlatNodes = computed(() => {
  const result = []
  function walk(nodes) {
    for (const node of nodes) {
      result.push(node)
      if (node.is_dir && node.expanded && node.children) {
        walk(node.children)
      }
    }
  }
  walk(treeData.value)
  return result
})

function treeNode(raw) {
  const node = {
    label: raw.name,
    path: raw.path.replace(/\\/g, '/'),
    is_dir: raw.is_dir,
  }
  if (raw.is_dir) {
    node.children = []
    node.expanded = false
    node._loading = false
  }
  return node
}

function normalizeTreeDataPaths(nodes) {
  if (!nodes) return []
  for (const n of nodes) {
    if (n.path) n.path = n.path.replace(/\\/g, '/')
    if (n.children) normalizeTreeDataPaths(n.children)
  }
  return nodes
}

function getParentPath(path) {
  if (!path) return ''
  const idx = path.lastIndexOf('/')
  const bidx = path.lastIndexOf('\\')
  return path.substring(0, Math.max(idx, bidx)) || ''
}

function isDBConfig(data) {
  return data.path && data.path.startsWith('db://')
}

function sortChildren(children) {
  children.sort((a, b) => (b.is_dir ? 1 : 0) - (a.is_dir ? 1 : 0))
}

async function loadDirChildren(dirNode) {
  try {
    const res = await getFileTreeChildren(dirNode.path)
    const raw = res.children || []
    const oldByPath = {}
    if (dirNode.children) {
      for (const c of dirNode.children) {
        oldByPath[c.path] = c
      }
    }
    const merged = []
    for (const r of raw) {
      const normalizedPath = r.path.replace(/\\/g, '/')
      const old = oldByPath[normalizedPath]
      if (old) {
        old.label = r.name
        merged.push(old)
      } else {
        merged.push(treeNode(r))
      }
    }
    dirNode.children = merged
    sortChildren(dirNode.children)
    dirNode._loading = false
  } catch (_) {
    dirNode.children = []
    dirNode._loading = false
  }
}

async function onToggle(node) {
  if (!node || !node.is_dir) return
  if (node._loading) return
  if (node.expanded) {
    node.expanded = false
    window.go.main.App.UnwatchDir(node.path, true).catch(e => console.warn('[FileTree] UnwatchDir error:', e))
    saveFileTreeSnapshot()
    return
  }
  node.expanded = true
  node._loading = true
  await loadDirChildren(node)
  window.go.main.App.WatchDir(node.path).catch(e => console.warn('[FileTree] WatchDir error:', e))
  saveFileTreeSnapshot()
}

function onRowClick(node) {
  selectedKey.value = node.path
  if (node.is_dir) {
    onToggle(node)
  } else {
    if (isDBConfig(node)) {
      const key = node.path.replace('db://', '')
      window.dispatchEvent(new CustomEvent('file:open', {
        detail: { path: node.path, isDBConfig: true, dbKey: key }
      }))
    } else {
      window.dispatchEvent(new CustomEvent('file:open', { detail: { path: node.path } }))
    }
  }
  // Save snapshot after selection change
  if (typeof window._saveFileTreeSnapshotDebounced === 'function') {
    window._saveFileTreeSnapshotDebounced()
  }
}

const ctxMenu = reactive({
  visible: false, x: 0, y: 0,
  data: null, isDir: false, isDB: false,
  style: {}, items: [],
})

const dirMenu = [
  { key: 'newFile', label: '新建文件', icon: 'plus' },
  { key: 'sep-dir-1', type: 'separator' },
  { key: 'copyPath', label: '复制路径', icon: 'link' },
  { key: 'copyRelativePath', label: '复制相对路径', icon: 'rank' },
  { key: 'sep-dir-2', type: 'separator' },
  { key: 'revealInExplorer', label: '在资源管理器中显示', icon: 'upload' },
  { key: 'rename', label: '重命名', icon: 'edit' },
  { key: 'delete', label: '删除', icon: 'delete', danger: true },
]

const fileMenu = [
  { key: 'open', label: '打开', icon: 'document' },
  { key: 'openWith', label: '打开方式', icon: 'top' },
  { key: 'sep-file-1', type: 'separator' },
  { key: 'sendFile', label: '让 LLM 阅读此文件', icon: 'chat-dot-square' },
  { key: 'sep-file-2', type: 'separator' },
  { key: 'copyPath', label: '复制路径', icon: 'link' },
  { key: 'copyRelativePath', label: '复制相对路径', icon: 'rank' },
  { key: 'copyFilename', label: '复制文件名', icon: 'copy-document' },
  { key: 'sep-file-3', type: 'separator' },
  { key: 'revealInExplorer', label: '在资源管理器中显示', icon: 'upload' },
  { key: 'rename', label: '重命名', icon: 'edit' },
  { key: 'duplicate', label: '复制', icon: 'copy-document' },
  { key: 'delete', label: '删除', icon: 'delete', danger: true },
]

function onRowContextMenu(event, node) {
  event.preventDefault()
  event.stopPropagation()
  openContextMenu(event, node)
}

function onTreeContextMenu(e) {
  e.preventDefault()
  if (!e.target.closest('.tree-row') && treeData.value.length > 0) {
    const rootName = _cachedWorkDir.value
      ? (_cachedWorkDir.value.split(/[/\\]/).filter(Boolean).pop() || _cachedWorkDir.value)
      : ''
    openContextMenu(e, { path: _cachedWorkDir.value, label: rootName, is_dir: true, isRoot: true })
  }
}

function openContextMenu(event, data) {
  let x = event.clientX
  let y = event.clientY
  const isRoot = data.isRoot
  const isDir = data.is_dir
  let items
  if (isRoot) {
    items = dirMenu.filter(item => item.key !== 'rename' && item.key !== 'delete')
  } else if (isDir) {
    items = dirMenu
  } else {
    items = fileMenu
  }
  const mc = items.length
  const mw = 200
  if (x + mw > window.innerWidth) x = window.innerWidth - mw - 12
  if (y + mc * 30 + 8 > window.innerHeight) y = window.innerHeight - mc * 30 - 20
  ctxMenu.visible = true
  ctxMenu.x = x; ctxMenu.y = y
  ctxMenu.data = data
  ctxMenu.isDir = isDir
  ctxMenu.isDB = isDBConfig(data)
  ctxMenu.style = { left: x + 'px', top: y + 'px' }
  ctxMenu.items = items
}

function closeContextMenu() { ctxMenu.visible = false }

function documentClickHandler(e) {
  if (e.button !== 0) return
  closeContextMenu()
}

function onKeyDown(e) {
  if (editingPath.value) return
  const tag = document.activeElement?.tagName?.toLowerCase()
  if (tag === 'input' || tag === 'textarea') return
  if (!selectedKey.value) return
  const selNode = findNode(treeData.value, selectedKey.value)
  if (e.key === 'F2') {
    if (!selNode || selNode.path.startsWith('db://')) return
    doRename(selNode.path, selNode.label)
  }
  if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
    e.preventDefault()
    const flat = _navFlatNodes.value
    if (flat.length === 0) return
    const idx = flat.findIndex(n => n.path === selectedKey.value)
    let newIdx
    if (e.key === 'ArrowUp') {
      newIdx = idx <= 0 ? flat.length - 1 : idx - 1
    } else {
      newIdx = idx >= flat.length - 1 ? 0 : idx + 1
    }
    selectedKey.value = flat[newIdx].path
    document.querySelector(`[data-path="${CSS.escape(flat[newIdx].path)}"]`)?.scrollIntoView({ block: 'nearest' })
  }
  if (e.key === 'ArrowRight' && selNode) {
    e.preventDefault()
    if (!selNode.is_dir) return
    if (!selNode.expanded) {
      onToggle(selNode)
      return
    }
    if (selNode.children && selNode.children.length > 0) {
      selectedKey.value = selNode.children[0].path
    }
  }
  if (e.key === 'ArrowLeft') {
    e.preventDefault()
    if (selNode && selNode.is_dir && selNode.expanded) {
      onToggle(selNode)
      return
    }
    const parentPath = getParentPath(selectedKey.value)
    if (parentPath) {
      const parent = findNode(treeData.value, parentPath)
      if (parent && parent.is_dir) {
        selectedKey.value = parent.path
        return
      }
    }
    const flat = _navFlatNodes.value
    if (flat.length > 0) {
      selectedKey.value = flat[0].path
    }
  }
  if (e.key === 'Enter') {
    if (selNode && selNode.is_dir) {
      onToggle(selNode)
    } else if (selNode) {
      onRowClick(selNode)
    }
  }
}

const editingPath = ref('')
const editingValue = ref('')
const editingOrigPath = ref('')
const editingIsNew = ref(false)

function findNode(nodes, targetPath) {
  for (const n of nodes) {
    if (n.path === targetPath) return n
    if (n.children) {
      const found = findNode(n.children, targetPath)
      if (found) return found
    }
  }
  return null
}

async function refreshDirInTree(dirPath) {
  const normalized = dirPath.replace(/\\/g, '/')
  const node = findNode(treeData.value, normalized)
  if (node && node.is_dir) {
    await loadDirChildren(node)
  } else if (normalized === _cachedWorkDir.value) {
    try {
      const res = await getFileTree('')
      const raw = res.tree?.children || []
      treeData.value = raw
        .filter(n => n.name !== '.ide')
        .map(n => treeNode(n))
      sortChildren(treeData.value)
    } catch (_) {
      treeData.value = []
    }
  }
}

async function startInlineCreate(dirPath, isDir) {
  const defaultName = 'untitled'
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
      name = defaultName + ' ' + (attempt + 1)
    }
  }
  await refreshDirInTree(dirPath)
  const parent = findNode(treeData.value, dirPath)
  if (parent && !parent.expanded) {
    await onToggle(parent)
  }
  selectedKey.value = newPath
  editingPath.value = newPath
  editingValue.value = ''
  editingOrigPath.value = newPath
  editingIsNew.value = true
  await nextTick()
  const input = document.querySelector('.inline-edit-input input')
  if (input) input.focus()
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
  const parentDir = getParentPath(origPath)
  if (!val || val === oldName) {
    if (isNew) {
      try { await deleteFilePath(origPath) } catch (e) { console.error('[FileTree] deleteFilePath error:', e) }
    }
    await refreshDirInTree(parentDir)
    return
  }
  if (isNew) {
    try {
      await renameFile(origPath, val)
      message.success('已创建: ' + val)
    } catch (e) {
      message.error(e?.message || '创建失败')
      try { await deleteFilePath(origPath) } catch (e) { console.error('[FileTree] deleteFilePath error:', e) }
    }
  } else {
    try {
      await renameFile(path, val)
      message.success('已重命名为: ' + val)
    } catch (e) {
      message.error(e?.message || '重命名失败')
    }
  }
  await refreshDirInTree(parentDir)
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
      await refreshDirInTree(getParentPath(origPath))
    } catch (e) { console.error('[FileTree] deleteFilePath error:', e) }
  }
}

async function doRename(path, oldName) {
  if (editingPath.value) {
    await cancelEdit()
    await new Promise(r => setTimeout(r, 100))
  }
  editingPath.value = path
  editingValue.value = oldName
  editingOrigPath.value = path
  editingIsNew.value = false
  await nextTick()
  const input = document.querySelector('.inline-edit-input input')
  if (input) {
    input.focus()
    input.setSelectionRange(oldName.length, oldName.length)
    setTimeout(() => { try { input.select() } catch (e) { console.error('[FileTree] input.select error:', e) } }, 0)
  }
}

async function handleCtxAction(key) {
  const data = ctxMenu.data
  closeContextMenu()
  if (!data) return
  switch (key) {
    case 'newFile': await startInlineCreate(data.path, false); break
    case 'newFolder': await startInlineCreate(data.path, true); break
    case 'copyPath': await copyToClipboard(data.path); break
    case 'copyRelativePath': await copyRelativePath(data.path); break
    case 'copyFilename': await copyToClipboard(data.label); break
    case 'revealInExplorer': await doReveal(data.path); break
    case 'rename': await doRename(data.path, data.label); break
    case 'delete': await doDelete(data.path, data.label, data.is_dir); break
    case 'duplicate': await doDuplicate(data.path); break
    case 'open': await doOpen(data.path); break
    case 'openWith': await doOpenWith(data.path); break
    case 'sendFile':
      if (data.path && !isDBConfig(data)) {
        const messageText = `请阅读 ${data.path} 文件`
        window.dispatchEvent(new CustomEvent('chat:insert-text', {
          detail: { text: messageText }
        }))
      }
      break
  }
}

async function doDelete(path, label, isDir) {
  try {
    const type = isDir ? '文件夹' : '文件'
    await confirm(
      '确定要删除' + type + ' "' + label + '" 吗？' + (isDir ? '文件夹内的所有内容将被删除。' : ''),
      '删除确认'
    )
    await deleteFilePath(path)
    message.success('已删除: ' + label)
    await refreshDirInTree(getParentPath(path))
  } catch (e) { console.error('[FileTree] doDelete error:', e) }
}

async function doDuplicate(path) {
  try {
    const res = await duplicateFile(path)
    message.success('已复制: ' + res.path)
    await refreshDirInTree(getParentPath(path))
  } catch (e) {
    message.error(e?.message || '复制失败')
  }
}

async function doReveal(path) {
  try { await revealInExplorer(path) } catch (_) { message.error('打开资源管理器失败') }
}

async function doOpen(path) {
  try { await openWithDefault(path) } catch (_) { message.error('打开失败') }
}

async function doOpenWith(path) {
  try { await openWithDialog(path) } catch (_) { message.error('打开失败') }
}

async function copyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text)
  } catch (_) {
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
  }
  message.success('已复制到剪贴板')
}

async function copyRelativePath(absPath) {
  if (_cachedWorkDir.value && absPath.startsWith(_cachedWorkDir.value)) {
    await copyToClipboard(absPath.substring(_cachedWorkDir.value.length))
  } else {
    await copyToClipboard(absPath)
  }
}

function onFileSearch(e) {
  searchQuery.value = e.detail?.query || ''
}

function buildTreeSnapshot() {
  if (treeData.value.length === 0) return null
  const rootName = _cachedWorkDir.value
    ? (_cachedWorkDir.value.split(/[/\\]/).filter(Boolean).pop() || _cachedWorkDir.value)
    : ''
  function snap(node) {
    if (!node) return null
    const result = {
      name: node.label,
      path: node.path,
      is_dir: !!node.is_dir,
      expanded: !!node.expanded,
    }
    if (node.is_dir && node.expanded && node.children && node.children.length > 0) {
      result.children = []
      for (const child of node.children) {
        const childSnap = snap(child)
        if (childSnap) result.children.push(childSnap)
      }
    }
    return result
  }
  const children = []
  for (const child of treeData.value) {
    const node = snap(child)
    if (node) children.push(node)
  }
  return {
    name: rootName,
    path: _cachedWorkDir.value,
    is_dir: true,
    expanded: true,
    children,
  }
}

function saveFileTreeSnapshot() {
  saveFileTreeState({
    snapshot: buildTreeSnapshot(),
    selected_path: selectedKey.value || '',
  }).catch(e => console.warn('[FileTree] saveFileTreeState error:', e))
}

function restoreExpandedState(nodes, snapshot) {
  if (!snapshot) return
  for (const n of nodes) {
    const snap = snapshot.find(s => s.path === n.path)
    if (snap) {
      n.expanded = snap.expanded
      if (snap.children && n.children) {
        restoreExpandedState(n.children, snap.children)
      }
    }
  }
}

async function doFileTreeOperate(operate, target) {
  const normalized = target.replace(/\\/g, '/')
  switch (operate) {
    case 'expand': {
      const node = findNode(treeData.value, normalized)
      if (node && node.is_dir) {
        if (!node.expanded) {
          node.expanded = true
          node._loading = true
          await loadDirChildren(node)
          window.go.main.App.WatchDir(node.path).catch(e => console.warn('[FileTree] WatchDir error:', e))
        } else {
          await loadDirChildren(node)
        }
        saveFileTreeSnapshot()
      }
      break
    }
    case 'collapse': {
      const node = findNode(treeData.value, normalized)
      if (node && node.is_dir && node.expanded) {
        await onToggle(node)
      }
      break
    }
    case 'select': {
      const parts = normalized.split('/')
      for (let i = 1; i < parts.length; i++) {
        const dirPath = parts.slice(0, i).join('/')
        const dirNode = findNode(treeData.value, dirPath)
        if (dirNode && dirNode.is_dir && !dirNode.expanded) {
          await onToggle(dirNode)
        }
      }
      selectedKey.value = normalized
      // Save snapshot after selection change
      if (typeof window._saveFileTreeSnapshotDebounced === 'function') {
        window._saveFileTreeSnapshotDebounced()
      }
      break
    }
  }
}

onMounted(() => {
  document.addEventListener('click', documentClickHandler)
  document.addEventListener('keydown', onKeyDown)
  window.addEventListener('file:search', onFileSearch)

  loadInitData().then(result => {
    treeData.value = normalizeTreeDataPaths(result.treeData || [])
    _cachedWorkDir.value = (result.workDir || '').replace(/\\/g, '/')
    selectedKey.value = result.selectedKey || ''
    if (result.snapshot) {
      restoreExpandedState(treeData.value, result.snapshot.children || [])
    }
    if (result.filetreeWidth) {
      window.dispatchEvent(new CustomEvent('filetree:resize', { detail: { width: result.filetreeWidth } }))
    }
  }).catch(() => {
    treeData.value = []
    _cachedWorkDir.value = ''
  })

  onFileChanged((changes) => {
    for (const { dir, children } of changes) {
      const normalized = dir.replace(/\\/g, '/')
      const node = findNode(treeData.value, normalized)
      if (node && node.is_dir && node.expanded) {
        const oldByPath = {}
        if (node.children) {
          for (const c of node.children) {
            oldByPath[c.path] = c
          }
        }
        const merged = []
        for (const r of (children || [])) {
          const normalizedPath = r.path.replace(/\\/g, '/')
          const old = oldByPath[normalizedPath]
          if (old) {
            old.label = r.name
            merged.push(old)
          } else {
            merged.push(treeNode(r))
          }
        }
        node.children = merged
        sortChildren(node.children)
      } else if (!node && _cachedWorkDir.value && normalized === _cachedWorkDir.value) {
        const oldByPath = {}
        for (const c of treeData.value) {
          oldByPath[c.path] = c
        }
        const merged = []
        for (const r of (children || [])) {
          if (r.name === '.ide') continue
          const normalizedPath = r.path.replace(/\\/g, '/')
          const old = oldByPath[normalizedPath]
          if (old) {
            old.label = r.name
            merged.push(old)
          } else {
            merged.push(treeNode(r))
          }
        }
        treeData.value = merged
        sortChildren(treeData.value)
      }
    }
    nextTick(() => saveFileTreeSnapshotDebounced())
  })

  // Debounced snapshot save (replaces deep watch)
  let snapshotDebounceTimer = null
  function saveFileTreeSnapshotDebounced() {
    if (snapshotDebounceTimer) clearTimeout(snapshotDebounceTimer)
    snapshotDebounceTimer = setTimeout(() => {
      snapshotDebounceTimer = null
      saveFileTreeSnapshot()
    }, 500)
  }

  // Expose debounced save for manual calls
  window._saveFileTreeSnapshotDebounced = saveFileTreeSnapshotDebounced

  const unsubCapture = bridge.on('filetree:capture', async (data) => {
    const requestID = data?.request_id
    if (!requestID) return
    const snapshot = buildTreeSnapshot()
    if (snapshot) {
      try { await window.go.main.App.SaveFileTreeSnapshot(requestID, snapshot) } catch (e) { console.error('[FileTree] SaveFileTreeSnapshot error:', e) }
    }
  })

  const unsubSet = bridge.on('filetree:set', async (data) => {
    const { request_id, operate, target } = data || {}
    if (!request_id || !operate || !target) return
    try {
      await doFileTreeOperate(operate, target)
      await window.go.main.App.FileTreeOperateDone(request_id)
    } catch (e) {
      console.error('[FileTree] set failed:', e)
      window.go.main.App.FileTreeOperateDone(request_id).catch(e => console.warn('[FileTree] FileTreeOperateDone error:', e))
    }
  })

  _unsubCapture = unsubCapture
  _unsubSet = unsubSet

  const onWindowResize = () => {
    clearTimeout(window._resizeTimer)
    window._resizeTimer = setTimeout(() => {
      saveWindowState({
        width: window.innerWidth,
        height: window.innerHeight,
        x: window.screenX,
        y: window.screenY,
        maximized: false,
      }).catch(e => console.warn('[FileTree] saveWindowState error:', e))
    }, 1000)
  }
  window.addEventListener('resize', onWindowResize)
  _cleanup.push(() => window.removeEventListener('resize', onWindowResize))
})

onUnmounted(() => {
  document.removeEventListener('click', documentClickHandler)
  document.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('file:search', onFileSearch)
  if (_unsubCapture) _unsubCapture()
  if (_unsubSet) _unsubSet()
  for (const fn of _cleanup) fn()
  // fileTree is module-level singleton, no teardown needed
})
</script>

<style scoped>
.file-tree {
  height: 100%;
  position: relative;
  overflow-y: auto;
  overflow-x: hidden;
  user-select: none;
}

.context-menu {
  position: fixed;
  z-index: 9999;
  min-width: 180px;
  padding: 4px 0;
  background: var(--bg-elevated, #fff);
  border: 1px solid var(--border, #ddd);
  border-radius: 4px;
  box-shadow: 0 2px 8px rgba(0,0,0,0.12);
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
  color: #f56c6c;
}
.context-menu-item.danger:hover {
  background: #fef0f0;
}
.context-menu-separator {
  height: 1px;
  margin: 4px 8px;
  background: var(--border, #ddd);
}

.file-tree:focus-within .tree-row.selected {
  background: #ecf5ff;
  color: #409eff;
}

.tree-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-secondary, #888);
  font-size: 13px;
}
</style>