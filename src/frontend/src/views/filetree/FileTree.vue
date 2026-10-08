<template>
  <div class="filetree-panel">
    <div class="panel-header">
      <Icon name="folder" />
      <span class="header-path" :title="_cachedWorkDir">{{ displayPath }}</span>
      <Icon v-if="vcsInfo.git" name="git" class="vcs-icon vcs-git" :title="$t('common.git')" />
      <Icon v-if="vcsInfo.svn" name="svn" class="vcs-icon vcs-svn" :title="$t('common.svn')" />
      <Icon name="setting" class="header-settings-icon" :title="$t('common.ide_config')" v-mq:[EventNames.projectConfigOpen].click />
    </div>
    <div class="file-tree" @contextmenu="onTreeContextMenu">
    <TreeNode
      v-for="node in treeData"
      :key="node.path"
      :node="node"
      :depth="0"
      :selected-key="selectedKey"
      :selected-keys="Array.from(selectedKeys)"
      :drag-over-path="dragOverPath"
      :editing-path="editingPath"
      :editing-value="editingValue"
      :search-query="searchQuery"
      @toggle="onToggle"
      @row-click="onRowClick"
      @row-dblclick="onRowDblClick"
      @context-menu="onRowContextMenu"
      @update:editing-value="editingValue = $event"
      @confirm-edit="confirmEdit"
      @cancel-edit="cancelEdit"
      @drag-start="onTreeDragStart"
      @drag-end="onTreeDragEnd"
      @drag-over="onTreeDragOver"
      @drag-leave="onTreeDragLeave"
      @drop="onTreeDrop"
    />

    <div v-if="treeData.length === 0" class="tree-empty">
      <p v-if="_cachedWorkDir">{{ $t('fileTree.empty_folder') }}</p>
      <p v-else>{{ $t('fileTree.no_project') }}</p>
    </div>

    <div
      v-if="ctxMenu.visible"
      class="context-menu"
      :style="ctxMenu.style"
      v-mq:[EventNames.containerClick].click.stop
      @contextmenu.prevent
    >
      <template v-for="item in ctxMenu.items" :key="item.key">
        <div v-if="item.type === 'separator'" class="context-menu-separator" />
        <div
          v-else
          class="context-menu-item"
          :class="{ danger: item.danger }"
          v-mq:[EventNames.fileCtxAction].click="{ key: item.key }"
        >
          <Icon v-if="item.icon" :name="item.icon" :size="14" />
          <span>{{ item.label }}</span>
        </div>
      </template>
    </div>
  </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { confirm, message, promptInput } from '../../components/ui'
import { getFileTree, getFileTreeChildren, createFileInDir, createDirInDir, renameFile, moveFile, copyFileTo, deleteFilePath, revealInExplorer, openWithDefault, openWithDialog, loadInitData, saveFileTreeState, saveWindowState, setWorkDir, newFileReqId } from '../../api/file'
import { createPrimitive } from '../../api/knowledge'
import { nearestTypeToken } from '../../utils/primitive'
import { onFileChanged, onFileChangedEvent } from '../../utils/fileTree'
import { fileOpErrorText } from '../../utils/fileOpError'
import { fetchIndexIgnored, toWorkdirRelPath } from '../../utils/indexIgnored'
import { onDataRefresh } from '../../utils/dataClient'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics, FieldKeys, GuiVcsInfoKeys } from '../../events/msgkeys'

import TreeNode from './TreeNode.vue'

const { t } = useI18n()

const treeData = ref([])
const selectedKey = ref('')
// 多选（Ctrl/Shift+点击）：selectedKeys 为 Set 的数组视图；selectedKey 保持主选中（改名/导航语义）
const selectedKeys = ref(new Set())
const searchQuery = ref('')

const _cachedWorkDir = ref('')
const vcsInfo = ref({ git: false, svn: false })
const displayPath = computed(() => {
  const d = _cachedWorkDir.value
  if (!d) return ''
  const parts = d.replace(/\\/g, '/').split('/').filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
})
async function fetchVCSInfo() {
  try {
    // gui.vcs.info（61-消息一览 §1）：探测工作目录 VCS 类型 → result {git, svn}
    const env = await mq.emit(MsgTopics.guiVcsInfo, {})
    const r = env && env.backend && env.backend.result
    vcsInfo.value = (r && typeof r === 'object')
      ? { git: !!r[GuiVcsInfoKeys.git], svn: !!r[GuiVcsInfoKeys.svn] }
      : { git: false, svn: false }
  } catch (_) {
    vcsInfo.value = { git: false, svn: false }
  }
}
// 统一卸载句柄：onMounted 内注册的监听 / 订阅退订函数统一入 _cleanup，onUnmounted 依次调用。
const _cleanup = []

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

// ── 被索引排除条目灰显（data-index-ignored，61-消息一览 §3.6；只读、零副作用）──
// 命中「当前已启用引擎」排除规则的节点 → node._ignored=true → .tree-row.is-ignored 文本灰显
// （--fg-disabled）；**只改颜色**，点击/双击/拖拽/右键等交互不变。
// 请求克制：按批合并——每次只把「尚未查询过」的已加载节点相对路径**一次**发出（同一路径只请求一次）；
// 刷新（既有 prj-config 变更广播）→ 清缓存重判（此前已灰的节点若不再命中即恢复）。
const _ignoredRel = new Set() // 已判定为「被排除」的 workdir 相对路径（'/' 分隔）
const _judgedRel = new Set()  // 已查询过的相对路径（避免重复请求）
let _judgeBusy = false
let _judgeAgain = false

// 节点的 workdir 相对路径（db:// 等非文件系统节点 → ''，不参与判定）
function workdirRelPathOf(node) {
  if (!node || !node.path || node.path.startsWith('db://')) return ''
  return toWorkdirRelPath(node.path, _cachedWorkDir.value)
}

// 收集当前**已加载**的全部节点相对路径（含折叠但仍保留 children 的节点）
function collectLoadedRelPaths() {
  const out = new Set()
  const walk = (nodes) => {
    for (const n of nodes) {
      const rel = workdirRelPathOf(n)
      if (rel) out.add(rel)
      if (n.children && n.children.length) walk(n.children)
    }
  }
  walk(treeData.value)
  return out
}

// 把判定结果落到节点标记（仅改 _ignored，不动任何交互状态）
function applyIgnoredFlags() {
  const walk = (nodes) => {
    for (const n of nodes) {
      const rel = workdirRelPathOf(n)
      n._ignored = !!(rel && _ignoredRel.has(rel))
      if (n.children && n.children.length) walk(n.children)
    }
  }
  walk(treeData.value)
}

// 判定「当前已加载节点」的排除状态：单飞 + 补跑（并发触发只保一批在飞，避免重复请求）。
async function judgeLoadedNodes() {
  if (!_cachedWorkDir.value) return
  if (_judgeBusy) { _judgeAgain = true; return }
  _judgeBusy = true
  try {
    do {
      _judgeAgain = false
      const pending = []
      for (const rel of collectLoadedRelPaths()) {
        if (!_judgedRel.has(rel)) pending.push(rel)
      }
      if (pending.length === 0) break
      const { ignored, truncated } = await fetchIndexIgnored(pending)
      // 截断（入参超上限）→ 未判定的节点不灰显，且**不缓存**，留待下次补判
      if (!truncated) for (const rel of pending) _judgedRel.add(rel)
      for (const rel of ignored) _ignoredRel.add(rel)
      applyIgnoredFlags()
    } while (_judgeAgain)
  } finally {
    _judgeBusy = false
  }
}

// 排除规则 / 引擎开关变化（既有 data-prj-config-refresh 广播）→ 清缓存重判。
function rejudgeIndexIgnored() {
  _judgedRel.clear()
  _ignoredRel.clear()
  applyIgnoredFlags()
  judgeLoadedNodes()
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
  // 新一批已加载节点 → 按批判定排除状态（灰显）
  judgeLoadedNodes()
}

// 递归刷新已展开（expanded=true）的子孙目录（一.7 缺陷修复）：
// 只重读内容、不改变展开标志，保证展开状态与磁盘内容一致。
async function refreshExpandedDescendants(node) {
  if (!node || !node.is_dir || !node.children) return
  for (const child of node.children) {
    if (child.is_dir && child.expanded) {
      child._loading = true
      await loadDirChildren(child)
      child._loading = false
      await refreshExpandedDescendants(child)
    }
  }
}

// onToggle 收敛为 mq 事件（61-消息一览 §2.3）：展开/收起动作统一经 filesys.watch /
// filesys.unwatch 驱动——本地 mq.on 处理 UI 状态 + filesys.list 读子项 + 快照，
// 后端桥注册/注销目录轮询关注；测试可经 window.mq.emit 精确模拟。
function onToggle(node) {
  if (!node || !node.is_dir) return
  if (node._loading) return
  if (node.expanded) {
    mq.emit(EventNames.fileCollapse, {
      req_id: newFileReqId(),
      work_dir: _cachedWorkDir.value,
      path: node.path,
    })
  } else {
    mq.emit(EventNames.fileExpand, {
      req_id: newFileReqId(),
      work_dir: _cachedWorkDir.value,
      path: node.path,
      recursive: true,
    })
  }
}

// I-47：根节点（treeData 即工作目录的子项）默认展开却从未声明 watch —— 展开/收起
// 才发 filesys.watch，而根无 toggle 入口 → 根目录 create/delete/rename 不进树。
// 初始化（含重启恢复）时对工作目录根声明 filesys.watch（与展开同口径）；
// filesys 侧 watch 按路径去重（dirWatcher.add 幂等），声明本身不产生 filesys.changed
// 广播，故不会造成重复监听/重复广播。
function declareRootWatch() {
  const wd = _cachedWorkDir.value
  if (!wd) return
  mq.emit(EventNames.fileExpand, {
    req_id: newFileReqId(),
    work_dir: wd,
    path: wd,
    recursive: false,
  })
}

// 上次单击记录（改名判定：再次单击已选中单选节点，间隔 > 500ms）
let lastRowClick = { path: '', time: 0 }

// 单击（FP）：目录 → 展开/收起；文件 → 打开临时页签；
// Ctrl/Shift+点击 → 多选切换；多选状态（>1）下普通单击 → 清多选并单选；
// 再次单击已选中的单选节点且间隔 > 500ms → 进入改名（双击序列两次 click < 500ms 不触发）
function onRowClick(node, event) {
  // 重命名态中点击其它节点 → 提交并退出（tree-row 是 div 不可聚焦，光靠 input 的
  // blur 不保证触发；显式切换即退出，避免重命名输入框残留）
  if (editingPath.value && editingPath.value !== node.path) {
    confirmEdit()
  }
  const now = Date.now()

  // Ctrl/Shift 多选切换（不触发改名/打开页签）
  if (event && (event.ctrlKey || event.metaKey || event.shiftKey)) {
    const set = new Set(selectedKeys.value)
    if (set.has(node.path)) {
      set.delete(node.path)
    } else {
      set.add(node.path)
    }
    selectedKeys.value = set
    if (set.size > 0) selectedKey.value = node.path
    return
  }

  // 非 Ctrl 点击：若处于多选状态（>1）→ 清多选退化为单选
  if (selectedKeys.value.size > 1) {
    selectedKeys.value = new Set()
  }
  selectedKey.value = node.path

  if (node.is_dir) {
    lastRowClick = { path: node.path, time: now }
    onToggle(node)
    saveFileTreeSnapshotDebounced()
    return
  }
  if (isDBConfig(node)) {
    lastRowClick = { path: node.path, time: now }
    mq.emit(EventNames.fileOpen, { path: node.path, isDBConfig: true, dbKey: node.path.replace('db://', ''), temporary: true })
    saveFileTreeSnapshotDebounced()
    return
  }

  // 改名判定：本次点击 === 上次选中节点，且与上次 click 间隔 > 500ms（多选状态不触发）
  if (lastRowClick.path === node.path && now - lastRowClick.time > 500) {
    lastRowClick = { path: node.path, time: now }
    doRename(node.path, node.label, false)
    return
  }
  lastRowClick = { path: node.path, time: now }

  // FP：单击打开临时页签
  mq.emit(EventNames.fileOpen, { path: node.path, temporary: true })
  saveFileTreeSnapshotDebounced()
}

// 双击（FP）：目录 → 展开/收起；文件 → 打开固定页签
function onRowDblClick(node) {
  selectedKey.value = node.path
  if (node.is_dir) {
    onToggle(node)
    return
  }
  if (isDBConfig(node)) {
    mq.emit(EventNames.fileOpen, { path: node.path, isDBConfig: true, dbKey: node.path.replace('db://', ''), temporary: false })
  } else {
    mq.emit(EventNames.fileOpen, { path: node.path, temporary: false })
  }
  saveFileTreeSnapshotDebounced()
}

const ctxMenu = reactive({
  visible: false, x: 0, y: 0,
  data: null, isDir: false, isDB: false,
  style: {}, items: [],
})

// 应用内剪贴板：复制/黏贴的文件路径列表（FP：剪切板含文件路径时显示「黏贴」）
let clipboardFiles = []

const dirMenu = computed(() => [
  { key: 'newFolder', label: t('fileTree.new_folder'), icon: 'folder' },
  { key: 'newFile', label: t('fileTree.new_file'), icon: 'plus' },
  { key: 'sep-dir-0', type: 'separator' },
  { key: 'copyPath', label: t('fileTree.copy_path'), icon: 'link' },
  { key: 'copyRelativePath', label: t('fileTree.copy_relative_path'), icon: 'rank' },
  { key: 'sep-dir-2', type: 'separator' },
  { key: 'revealInExplorer', label: t('fileTree.reveal_in_explorer'), icon: 'upload' },
  { key: 'openInConsole', label: t('fileTree.open_in_console'), icon: 'terminal' },
  { key: 'sep-dir-3', type: 'separator' },
  { key: 'copy', label: t('fileTree.copy'), icon: 'copy-document' },
  { key: 'paste', label: t('fileTree.paste'), icon: 'paste', clipboardOnly: true },
  { key: 'sep-dir-4', type: 'separator' },
  { key: 'rename', label: t('fileTree.rename'), icon: 'edit' },
  { key: 'delete', label: t('fileTree.delete'), icon: 'delete', danger: true },
])

const fileMenu = computed(() => [
  { key: 'open', label: t('fileTree.open'), icon: 'document' },
  { key: 'openWith', label: t('fileTree.open_with'), icon: 'top' },
  { key: 'sep-file-1', type: 'separator' },
  { key: 'sendFile', label: t('fileTree.send_to_llm'), icon: 'chat-dot-square' },
  { key: 'sep-file-2', type: 'separator' },
  { key: 'copyPath', label: t('fileTree.copy_path'), icon: 'link' },
  { key: 'copyRelativePath', label: t('fileTree.copy_relative_path'), icon: 'rank' },
  { key: 'copyFilename', label: t('fileTree.copy_filename'), icon: 'copy-document' },
  { key: 'sep-file-3', type: 'separator' },
  { key: 'revealInExplorer', label: t('fileTree.reveal_in_explorer'), icon: 'upload' },
  { key: 'openInConsole', label: t('fileTree.open_in_console'), icon: 'terminal' },
  { key: 'sep-file-4', type: 'separator' },
  { key: 'copy', label: t('fileTree.copy'), icon: 'copy-document' },
  { key: 'paste', label: t('fileTree.paste'), icon: 'paste', clipboardOnly: true },
  { key: 'rename', label: t('fileTree.rename'), icon: 'edit' },
  { key: 'duplicate', label: t('fileTree.duplicate'), icon: 'copy-document' },
  { key: 'delete', label: t('fileTree.delete'), icon: 'delete', danger: true },
])

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
    items = dirMenu.value.filter(item => item.key !== 'rename' && item.key !== 'delete')
  } else if (isDir) {
    items = dirMenu.value
    // 原语类型目录（@mcp 下 tools/skills/prompts/resources 等）→ 右键顶部加「新建XX」
    const tok = nearestTypeToken(data.path)
    if (tok) {
      items = [{ key: 'newType', label: t('fileTree.new_of_type', { type: typeLabelT(tok) }) }, { type: 'separator' }, ...items]
    }
  } else {
    items = fileMenu.value
  }
  // 多选状态（>1）：仅保留单选+多选通用的项（打开/打开方式/重命名/资源管理器/控制台/新建等单选项隐藏）
  const singleOnly = ['open', 'openWith', 'rename', 'revealInExplorer', 'openInConsole', 'duplicate', 'newFile', 'newFolder', 'newType']
  if (selectedKeys.value.size > 1) {
    items = items.filter(it => it.type === 'separator' || !singleOnly.includes(it.key))
  }
  // 黏贴：仅当应用内剪贴板含文件路径时显示
  if (clipboardFiles.length === 0) {
    items = items.filter(it => !it.clipboardOnly)
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
  // 焦点在可输入元素内（input / textarea / contenteditable，如 chat 富文本输入框）→ **不接管按键**：
  // 否则在输入框里按 Delete/Backspace 会被误当作「删除树节点」（2026-09-27 用户报 bug）。
  const el = document.activeElement
  const tag = el?.tagName?.toLowerCase()
  if (tag === 'input' || tag === 'textarea' || (el && el.isContentEditable)) return
  if (!selectedKey.value) return
  const selNode = findNode(treeData.value, selectedKey.value)
  if (e.key === 'F2') {
    if (!selNode || selNode.path.startsWith('db://')) return
    doRename(selNode.path, selNode.label, selNode.is_dir)
  }
  // FP：Delete 键删除选择节点（多选全部；确认框）
  if (e.key === 'Delete' || e.key === 'Backspace') {
    const targets = []
    if (selectedKeys.value.size > 1) {
      for (const p of selectedKeys.value) {
        const n = findNode(treeData.value, p)
        if (n && !n.path.startsWith('db://') && _cachedWorkDir.value !== n.path) targets.push(n)
      }
    } else if (selNode) {
      if (!selNode.path.startsWith('db://') && _cachedWorkDir.value !== selNode.path) targets.push(selNode)
    }
    if (targets.length === 0) return
    e.preventDefault()
    for (const n of targets) {
      doDelete(n.path, n.label, n.is_dir)
    }
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
  const normalized = (dirPath || '').replace(/\\/g, '/')
  const node = findNode(treeData.value, normalized)
  if (node && node.is_dir) {
    await loadDirChildren(node)
  } else if (normalized === (_cachedWorkDir.value || '').replace(/\\/g, '/')) {
    try {
      const res = await getFileTree('')
      // 兼容旧 /call 契约（{tree:{children}}）与 filemon 契约（{children}）
      const raw = res.tree?.children || res.children || []
      treeData.value = raw
        .filter(n => n.name !== '.ide' && n.name !== '.chonkpilot')
        .map(n => treeNode(n))
      sortChildren(treeData.value)
      // 根目录整树重建 → 重判排除状态（灰显）
      judgeLoadedNodes()
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
  const input = document.querySelector('.inline-edit-input')
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
      message.success(t('fileTree.created', { name: val }))
    } catch (e) {
      message.error(e?.message || t('fileTree.create_failed'))
      try { await deleteFilePath(origPath) } catch (e) { console.error('[FileTree] deleteFilePath error:', e) }
    }
  } else {
    try {
      await renameFile(path, val)
      message.success(t('fileTree.renamed', { name: val }))
    } catch (e) {
      message.error(e?.message || t('fileTree.rename_failed'))
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

// 文件名重命名时默认选中的终点：后缀（含小数点）不选中，仅选中主名。
// 无扩展名 / 以点开头（如 .gitignore）→ 全选。
function extensionStart(name) {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? dot : name.length
}

async function doRename(path, oldName, isDir) {
  if (editingPath.value) {
    await cancelEdit()
    await new Promise(r => setTimeout(r, 100))
  }
  editingPath.value = path
  editingValue.value = oldName
  editingOrigPath.value = path
  editingIsNew.value = false
  await nextTick()
  const input = document.querySelector('.inline-edit-input')
  if (input) {
    input.focus()
    // 文件：默认只选中主名（后缀含小数点保持未选中，便于直接改名保留扩展名）；
    // 目录：全选。
    const selEnd = isDir ? oldName.length : extensionStart(oldName)
    try {
      if (selEnd >= oldName.length) {
        input.select()
      } else {
        input.setSelectionRange(0, selEnd)
      }
    } catch (e) { console.error('[FileTree] input selection error:', e) }
  }
}

// 原语类型显示名（工具/技能/提示词/资源）
function typeLabelT(tok) {
  return { tool: t('fileTree.type_tool'), skill: t('fileTree.type_skill'), prompt: t('fileTree.type_prompt'), resource: t('fileTree.type_resource') }[tok] || tok
}

// 类型目录下新建原语文件（项目 @mcp 与知识库(capability) 一致走 CreatePrimitive 契约模板）
async function doCreateTypeFile(dirPath, tok) {
  if (!tok) return
  const name = await promptInput(t('fileTree.new_of_type_prompt', { type: typeLabelT(tok) }), '')
  if (!name) return
  try {
    const res = await createPrimitive(dirPath, tok, name)
    message.success(t('fileTree.created', { name }))
    await refreshDirInTree(dirPath)
    if (res && res.path) {
      const p = String(res.path).replace(/\\/g, '/')
      const node = findNode(treeData.value, p)
      if (node) selectedKey.value = node.path
      mq.emit(EventNames.fileOpen, { path: p, temporary: false })
    }
  } catch (e) {
    message.error(e?.message || t('fileTree.create_failed'))
  }
}

async function handleCtxAction(key) {
  const data = ctxMenu.data
  closeContextMenu()
  if (!data) return
  switch (key) {
    case 'newFile': await startInlineCreate(data.path, false); break
    case 'newFolder': await startInlineCreate(data.path, true); break
    case 'newType': await doCreateTypeFile(data.path, nearestTypeToken(data.path)); break
    case 'copyPath': await copyToClipboard(data.path); break
    case 'copyRelativePath': await copyRelativePath(data.path); break
    case 'copyFilename': await copyToClipboard(data.label); break
    case 'revealInExplorer': await doReveal(data.path); break
    case 'rename': await doRename(data.path, data.label, data.is_dir); break
    case 'delete': await doDelete(data.path, data.label, data.is_dir); break
    case 'duplicate': await doDuplicate(data.path); break
    case 'openInConsole': await doOpenInConsole(data.path); break
    case 'copy': doCopyFiles(); break
    case 'paste': await doPaste(data); break
    case 'open': await doOpen(data.path); break
    case 'openWith': await doOpenWith(data.path); break
    case 'sendFile':
      if (data.path && !isDBConfig(data)) {
        const messageText = t('fileTree.read_file', { path: data.path })
        mq.emit(EventNames.chatInsertText, { text: messageText })
      }
      break
  }
}

async function doDelete(path, label, isDir) {
  try {
    const type = t(isDir ? 'fileTree.folder' : 'fileTree.file')
    const suffix = isDir ? t('fileTree.confirm_delete_folder_suffix') : ''
    await confirm(
      t('fileTree.confirm_delete', { type, label }) + suffix,
      t('fileTree.confirm_delete_title')
    )
    await deleteFilePath(path)
    // 成功提示只在后端删除成功后（此前无本地乐观删除，失败不会呈现"已成功"）
    message.success(t('fileTree.deleted', { name: label }))
    await refreshDirInTree(getParentPath(path))
  } catch (e) {
    // 用户取消（confirm reject 'cancel'）静默；真实失败必须回显原因（不再只 console.error）
    const reason = fileOpErrorText(e, t('fileTree.unknown_error'))
    if (reason) message.error(t('fileTree.delete_failed_detail', { name: label, error: reason }))
  }
}

// ── 拖拽移动（FP：单选节点拖到目录 → 移动进目录；拖到文件 → 移入其所在目录；拖到 chat → 全路径）──
const dragOverPath = ref('')

function onTreeDragStart(node) {
  dragOverPath.value = ''
  // 拖拽开始时若为多选，仅拖主选中节点（简化：单选语义）
  window.__chonkDragPath = node.path
}

function onTreeDragEnd() {
  dragOverPath.value = ''
  window.__chonkDragPath = ''
}

function onTreeDragOver(node) {
  const srcPath = window.__chonkDragPath || ''
  if (!srcPath || srcPath === node.path) {
    dragOverPath.value = ''
    return
  }
  dragOverPath.value = node.path
}

function onTreeDragLeave(node) {
  if (dragOverPath.value === node.path) dragOverPath.value = ''
}

// drop 到节点：目录 → 移入；文件 → 移入其目录（移动前先展开目标目录）
async function onTreeDrop(target) {
  const srcPath = window.__chonkDragPath || ''
  window.__chonkDragPath = ''
  dragOverPath.value = ''
  if (!srcPath || srcPath === target.path) return
  const srcNode = findNode(treeData.value, srcPath)
  if (!srcNode) return
  const srcParent = getParentPath(srcPath)
  let targetDir = target.is_dir ? target.path : getParentPath(target.path)
  if (!targetDir || targetDir === srcParent) return // 同目录不动作
  const name = srcPath.split(/[/\\]/).pop()
  const newPath = targetDir.replace(/\\/g, '/') + '/' + name
  // 防止把节点移入自身子目录
  if (newPath.startsWith(srcPath + '/')) {
    message.warning(t('fileTree.move_into_self'))
    return
  }
  await doMove(srcNode, newPath, target)
}

async function doMove(srcNode, newPath, target) {
  try {
    await moveFile(srcNode.path, newPath)
    message.success(t('fileTree.moved', { name: srcNode.label }))
  } catch (e) {
    if (e.code === 'exists') {
      // 同名冲突：确认覆盖后重试
      try {
        const ok = await confirm(
          t('fileTree.overwrite_confirm', { name: srcNode.label }),
          t('dialog.confirm_title')
        )
        if (!ok) return
        await moveFile(srcNode.path, newPath, true)
        message.success(t('fileTree.moved', { name: srcNode.label }))
      } catch (e2) {
        // 用户取消覆盖（confirm reject 'cancel'）静默；覆盖失败必须回显原因（不再只 console.error）
        const reason = fileOpErrorText(e2, t('fileTree.unknown_error'))
        if (reason) message.error(t('fileTree.move_failed_detail', { name: srcNode.label, error: reason }))
      }
    } else {
      message.error(t('fileTree.move_failed_detail', { name: srcNode.label, error: fileOpErrorText(e, t('fileTree.unknown_error')) }))
    }
  }
  // 刷新源目录 + 目标目录（展开目标）
  await refreshDirInTree(getParentPath(srcNode.path))
  const tgtDir = target.is_dir ? target.path : getParentPath(target.path)
  const tgtNode = findNode(treeData.value, tgtDir)
  if (tgtNode && tgtNode.is_dir && !tgtNode.expanded) {
    await onToggle(tgtNode)
  }
  await refreshDirInTree(tgtDir)
}

// 拖到 chat 输入区（data-drop-target="chat-input"）→ 插入全路径
function onDocDragOver(e) {
  const target = e.target && e.target.closest ? e.target.closest('[data-drop-target="chat-input"]') : null
  if (target) {
    e.preventDefault()
    target.classList.add('drag-over')
  }
}

function onDocDrop(e) {
  const target = e.target && e.target.closest ? e.target.closest('[data-drop-target="chat-input"]') : null
  if (!target) return
  // 取纯路径：text/plain 优先（dragstart 写入全路径）；application/x-chonk-node 是 JSON 结构不用于插入
  const dt = e.dataTransfer
  let path = ''
  try {
    path = (dt && dt.getData('text/plain')) || ''
  } catch (_) { path = '' }
  if (!path) path = window.__chonkDragPath || ''
  if (!path) return
  e.preventDefault()
  target.classList.remove('drag-over')
  mq.emit(EventNames.chatInsertText, { text: path })
}

// 同目录副本名（前端算好经 filesys.copy new_name 传入）：a.txt → a 副本(1).txt；sub → sub 副本(1)
function duplicateNameOf(base, n) {
  const i = base.lastIndexOf('.')
  if (i > 0) return `${base.slice(0, i)} 副本(${n})${base.slice(i)}`
  return `${base} 副本(${n})`
}

async function doDuplicate(path) {
  const dir = getParentPath(path)
  const base = String(path).split(/[/\\]/).pop() || ''
  for (let n = 1; n <= 100; n++) {
    try {
      const res = await copyFileTo(path, { dest_dir: dir, new_name: duplicateNameOf(base, n) })
      message.success(t('fileTree.copied', { path: res.path }))
      await refreshDirInTree(dir)
      return
    } catch (e) {
      if (e.code !== 'exists') {
        message.error(e?.message || t('fileTree.copy_failed'))
        return
      }
    }
  }
  message.error(t('fileTree.copy_failed'))
}

// 在系统控制台显示（gui.console.open：打开 cmd 到路径所在目录）
async function doOpenInConsole(path) {
  try {
    const env = await mq.emit(MsgTopics.guiConsoleOpen, { [FieldKeys.path]: path })
    const backend = env && env.backend
    const p = backend && backend.result && typeof backend.result === 'object' ? backend.result : {}
    if (!backend || !backend.ok || p.ok === false || (backend.errors && backend.errors[0])) {
      throw new Error((backend.errors && backend.errors[0]) || p.error || 'gui.console.open failed')
    }
    message.success(t('fileTree.console_opened'))
  } catch (e) {
    message.error(e?.message || t('fileTree.open_console_failed'))
  }
}

// 复制：多选时收集全部选中路径，否则复制右键节点
function doCopyFiles() {
  if (selectedKeys.value.size > 1) {
    clipboardFiles = Array.from(selectedKeys.value)
  } else {
    const p = ctxMenu.data?.path
    if (!p) return
    clipboardFiles = [p]
  }
  message.success(t('fileTree.copied_n', { n: clipboardFiles.length }))
}

// 黏贴：把剪贴板内文件复制到目标目录（右键目录=该目录；右键文件=其所在目录）
async function doPaste(data) {
  if (!data || clipboardFiles.length === 0) return
  const targetDir = data.is_dir ? data.path : getParentPath(data.path)
  if (!targetDir) return
  let okCount = 0
  for (const src of clipboardFiles) {
    if (src === targetDir) continue
    try {
      await copyFileTo(src, { dest_dir: targetDir })
      okCount++
    } catch (e) {
      if (e.code === 'exists') {
        const name = src.split(/[/\\]/).pop()
        try {
          const yes = await confirm(t('fileTree.overwrite_confirm', { name }), t('dialog.confirm_title'))
          if (!yes) continue
          await copyFileTo(src, { dest_dir: targetDir, overwrite: true })
          okCount++
        } catch (e2) { console.error('[FileTree] doPaste overwrite error:', e2) }
      } else {
        message.error(e?.message || t('fileTree.paste_failed'))
      }
    }
  }
  clipboardFiles = []
  if (okCount > 0) message.success(t('fileTree.pasted_n', { n: okCount }))
  await refreshDirInTree(targetDir)
}

async function doReveal(path) {
  try { await revealInExplorer(path) } catch (_) { message.error(t('fileTree.open_resource_manager_failed')) }
}

async function doOpen(path) {
  try { await openWithDefault(path) } catch (_) { message.error(t('fileTree.open_failed')) }
}

async function doOpenWith(path) {
  try { await openWithDialog(path) } catch (_) { message.error(t('fileTree.open_failed')) }
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
  message.success(t('fileTree.copied_to_clipboard'))
}

async function copyRelativePath(absPath) {
  if (_cachedWorkDir.value && absPath.startsWith(_cachedWorkDir.value)) {
    await copyToClipboard(absPath.substring(_cachedWorkDir.value.length))
  } else {
    await copyToClipboard(absPath)
  }
}

// Toolbar 经 mq.emit(EventNames.fileSearch, path) 发送裸路径（mq 回调直接收 payload）
function onFileSearch(path) {
  searchQuery.value = typeof path === 'string' ? path : ''
}

// 收集当前展开的目录（仅沿展开节点下行）
function collectExpandedDirs(nodes, out = []) {
  for (const n of nodes) {
    if (!n.is_dir) continue
    if (n.expanded) {
      out.push(n.path)
      if (n.children) collectExpandedDirs(n.children, out)
    }
  }
  return out
}

function saveFileTreeSnapshot() {
  saveFileTreeState({
    expanded_dirs: collectExpandedDirs(treeData.value),
    selected_path: selectedKey.value || '',
  }).catch(e => console.warn('[FileTree] saveFileTreeState error:', e))
}

// Debounced snapshot save (replaces deep watch) — module-level private,
// shared by selection changes, file-change events and toggles.
let snapshotDebounceTimer = null
function saveFileTreeSnapshotDebounced() {
  if (snapshotDebounceTimer) clearTimeout(snapshotDebounceTimer)
  snapshotDebounceTimer = setTimeout(() => {
    snapshotDebounceTimer = null
    saveFileTreeSnapshot()
  }, 500)
}

let resizeTimer = null

// 重启恢复（C6）：后端 LoadInitData 已按 expandedSet 加载 children，
// 前端只需把 expandedKeys（绝对路径，需归一化正斜杠）命中的目录标记为展开。
function restoreExpandedKeys(nodes, expandedKeys) {
  if (!expandedKeys.length) return
  const set = new Set(expandedKeys)
  for (const n of nodes) {
    if (n.is_dir && set.has(n.path)) {
      n.expanded = true
    }
    if (n.children) {
      restoreExpandedKeys(n.children, expandedKeys)
    }
  }
}

onMounted(() => {
  document.addEventListener('click', documentClickHandler)
  document.addEventListener('keydown', onKeyDown)
  // 拖拽到 chat 输入区 → 全路径
  document.addEventListener('dragover', onDocDragOver)
  document.addEventListener('drop', onDocDrop)
  _cleanup.push(mq.on(EventNames.fileSearch, onFileSearch))
  // 展开/收起 = filesys.watch / filesys.unwatch（61-消息一览 §2.3）：
  // 本地处理 UI 状态 + filesys.list 读子项 + 快照；后端注册/注销目录轮询关注。
  _cleanup.push(mq.on(EventNames.fileExpand, async ({ path }) => {
    const node = findNode(treeData.value, path)
    if (!node || !node.is_dir || node.expanded) return
    node.expanded = true
    node._loading = true
    await loadDirChildren(node)
    node._loading = false
    // 缺陷修复（一.7）：折叠期间深层内容可能已变化（折叠后 watcher 停止监听），
    // 展开一级目录时递归刷新所有已展开（expanded=true）的子孙目录，避免
    // 「状态已展开但内容为折叠前旧数据」的误导（子级新建文件不显示）。
    await refreshExpandedDescendants(node)
    saveFileTreeSnapshot()
  }))
  _cleanup.push(mq.on(EventNames.fileCollapse, ({ path }) => {
    const node = findNode(treeData.value, path)
    if (!node || !node.is_dir || !node.expanded) return
    node.expanded = false
    saveFileTreeSnapshot()
  }))
  // 文件监视错误（file.watch.error，filemon watcher 故障广播）：提示用户文件变更检测失效
  _cleanup.push(mq.on(EventNames.fileWatcherError, ({ error }) => {
    console.warn('[FileTree] watcher error:', error)
    message.warning(`File watcher error: ${error || 'unknown'}`)
  }))
  // 右键菜单项事件化：v-mq 触发 → 本地执行
  _cleanup.push(mq.on(EventNames.fileCtxAction, ({ key }) => {
    if (key) handleCtxAction(key)
  }))

  loadInitData().then(result => {
    treeData.value = normalizeTreeDataPaths(result.treeData || [])
    // 根级首屏排序：后端 os.ReadDir 仅按名称字母序（不分目录/文件）→ 前端补「先目录后文件·
    // 组内字母序」（对齐 32 §FT-002），与 loadDirChildren 等其余 4 处保持一致（I-172）。
    sortChildren(treeData.value)
    _cachedWorkDir.value = (result.workDir || '').replace(/\\/g, '/')
    // 写入模块级工作目录：api/file.js 的 filemon 请求自动填充 work_dir
    setWorkDir(_cachedWorkDir.value)
    // I-47：根目录默认展开，初始化时声明根监听（否则根下增删改名不推送）
    declareRootWatch()
    fetchVCSInfo()
    selectedKey.value = result.selectedKey || ''
    const expandedKeys = (result.expandedKeys || []).map(k => String(k).replace(/\\/g, '/'))
    if (expandedKeys.length) {
      restoreExpandedKeys(treeData.value, expandedKeys)
    }
    // 树数据到手 → 判定排除状态（灰显）
    judgeLoadedNodes()
  }).catch(() => {
    treeData.value = []
    _cachedWorkDir.value = ''
  })

  // file.changed（filemon 单文件变更，operation: create|write|remove|rename）：
  // 经 utils/fileTree.js 模块级订阅统一回调，这里只处理 VCS 状态刷新；退订入 _cleanup。
  _cleanup.push(onFileChangedEvent((data) => {
    const path = data?.path || ''
    if (path.endsWith('\\.git') || path.endsWith('/.git')) {
      fetchVCSInfo()
    }
  }))
  // Debounced snapshot save on file-change events
  _cleanup.push(onFileChanged((changes) => {
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
          if (r.name === '.ide' || r.name === '.chonkpilot') continue
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
    // 变更合并可能新增节点 → 补判排除状态（灰显）
    judgeLoadedNodes()
    nextTick(() => saveFileTreeSnapshotDebounced())
  }))

  // 排除规则 / 引擎开关变化（既有 data-prj-config-refresh 广播）→ 重判已加载节点
  _cleanup.push(onDataRefresh('prj-config', rejudgeIndexIgnored))

  // 前端近似判断窗口是否最大化（Windows 无浏览器 API 直读；精确值由后端
  // 退出时 GetWindowPlacement 校正，此处用于 resize 中间态保存）。
  const isMaximizedNow = () => {
    const sw = window.screen.availWidth
    const sh = window.screen.availHeight
    return Math.abs(window.screenX) <= 4 && Math.abs(window.screenY) <= 4 &&
      window.innerWidth >= sw - 4 && window.innerHeight >= sh - 4
  }

  const onWindowResize = () => {
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(() => {
      // 窗口在导航完成前的 Hidden 阶段，screenX/screenY 是 Windows 的屏幕外
      // 隐藏坐标（-25600 等），保存后下次启动会把窗口恢复到屏幕外（taskbar
      // 有图标但窗口不可见）。隐藏态跳过保存，待窗口显示后由真实 resize 覆盖。
      if (window.screenX < -1000 || window.screenY < -1000) return
      saveWindowState({
        width: window.innerWidth,
        height: window.innerHeight,
        x: window.screenX,
        y: window.screenY,
        maximized: isMaximizedNow(),
      }).catch(e => console.warn('[FileTree] saveWindowState error:', e))
    }, 1000)
  }
  window.addEventListener('resize', onWindowResize)
  _cleanup.push(() => window.removeEventListener('resize', onWindowResize))

  // 退出兜底：Alt+F4 / WM_CLOSE 关闭路径不触发后端 window-close 事件，
  // 用 sendBeacon 尽力投递当前几何（含最大化近似）。最小化状态由后端
  // windowStateFromPlacement 按 normal 记录，无需前端特判。
  // 走 gui.ui.save 消息面（原 /call/SaveWindowState 随 /call 清零，2026-09-04）。
  const beaconSaveWindow = () => {
    try {
      if (window.screenX < -1000 || window.screenY < -1000) return
      const payload = JSON.stringify({ window: {
        width: window.innerWidth,
        height: window.innerHeight,
        x: window.screenX,
        y: window.screenY,
        maximized: isMaximizedNow(),
      } })
      const body = JSON.stringify({ type: 'gui.ui.save', payload })
      navigator.sendBeacon('/publish', new Blob([body], { type: 'application/json' }))
    } catch (_) { /* noop */ }
  }
  window.addEventListener('beforeunload', beaconSaveWindow)
  _cleanup.push(() => window.removeEventListener('beforeunload', beaconSaveWindow))
})

onUnmounted(() => {
  document.removeEventListener('click', documentClickHandler)
  document.removeEventListener('keydown', onKeyDown)
  document.removeEventListener('dragover', onDocDragOver)
  document.removeEventListener('drop', onDocDrop)
  for (const fn of _cleanup) fn()
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
  background: var(--bg-secondary);
  border: 1px solid var(--border);
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
  color: var(--text-primary);
  white-space: nowrap;
}
.context-menu-item:hover {
  background: var(--bg-hover);
}
.context-menu-item.danger {
  color: var(--danger);
}
.context-menu-item.danger:hover {
  background: color-mix(in srgb, var(--danger) 12%, transparent);
}
.context-menu-separator {
  height: 1px;
  margin: 4px 8px;
  background: var(--border);
}

/* 焦点态强调：.tree-row 属子组件 TreeNode，必须 :deep 才能命中（2026-09-26 修：原规则无 :deep 故永不生效） */
.file-tree:focus-within :deep(.tree-row.selected) {
  background: var(--accent-bg);
  color: var(--accent);
}

.tree-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-secondary);
  font-size: 13px;
}
.filetree-panel {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.panel-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  min-height: 28px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.8px;
  color: var(--text-muted);
  text-transform: uppercase;
  border-bottom: 1px solid var(--border);
  background: var(--bg-tertiary);
  flex-shrink: 0;
}
.header-path {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  text-transform: none;
  letter-spacing: 0;
  font-weight: 500;
}
.header-settings-icon {
  cursor: pointer;
  opacity: 0.6;
}
.header-settings-icon:hover {
  opacity: 1;
}
.vcs-icon {
  opacity: 0.5;
}
</style>