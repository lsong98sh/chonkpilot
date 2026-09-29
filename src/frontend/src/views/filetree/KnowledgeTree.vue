<template>
  <div ref="rootRef" class="knowledge-tree" :class="'kb-scope-' + scope">
    <div class="kb-tree-header">
      <span class="kb-tree-title" :title="root">{{ kbHeaderTitle }}</span>
      <span class="kb-level-switch">
        <Button
          v-for="lv in KB_LEVELS"
          :key="lv.kind"
          text
          size="mini"
          :class="{ active: kbLevel === lv.kind }"
          v-mq:[EventNames.kbLevelSelect].click="{ kind: lv.kind }"
        >{{ $t(lv.label) }}</Button>
      </span>
    </div>
    <div class="kb-tree-body" @contextmenu.prevent="onBlankContext">
      <TreeNode
        v-for="node in treeData"
        :key="node.path"
        :node="node"
        :depth="0"
        :selected-key="selectedKey"
        :selected-keys="[]"
        :drag-over-path="dragOverPath"
        :editing-path="editingPath"
        :editing-value="editingValue"
        @toggle="onToggle"
        @row-click="onRowClick"
        @row-dblclick="onRowDblClick"
        @context-menu="onRowContext"
        @update:editing-value="editingValue = $event"
        @confirm-edit="confirmEdit"
        @cancel-edit="cancelEdit"
        @drag-start="onTreeDragStart"
        @drag-end="onTreeDragEnd"
        @drag-over="onTreeDragOver"
        @drag-leave="onTreeDragLeave"
        @drop="onTreeDrop"
      />
      <div v-if="treeData.length === 0" class="kb-tree-empty">
        {{ loading ? t('common.loading') : t(emptyKey) }}
      </div>
    </div>

    <!-- 右键菜单 -->
    <Teleport to="body">
      <div
        v-if="ctx.visible"
        class="kb-ctx"
        :style="{ left: ctx.x + 'px', top: ctx.y + 'px' }"
        @contextmenu.prevent
        @mousedown.stop
      >
        <template v-for="(item, i) in ctx.items" :key="i">
          <div v-if="item.divider" class="kb-ctx-sep" />
          <div
            v-else
            class="kb-ctx-item"
            :class="{ danger: item.danger }"
            v-mq:[EventNames.kbCtxAction].click="{ key: item.key, scope }"
          >{{ item.label }}</div>
        </template>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import TreeNode from './TreeNode.vue'
import { confirm, message, promptInput, Button } from '../../components/ui'
import {
  getKnowledgeRoot, listPrimitives,
  createPrimitiveDir, renamePrimitiveDir, deletePrimitiveDir,
  createPrimitive, renamePrimitive, deletePrimitive, movePrimitive,
} from '../../api/knowledge'
import { nearestTypeToken, TYPE_DIR_REL } from '../../utils/primitive'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

defineOptions({ name: 'KnowledgeTree' })

// 类型范围复用：「知识库」页签（skill/prompt/resource）与「工具」页签（tool）共用本组件，
// 由 kinds 过滤目录/文件；scope 唯一标识本实例（右键动作事件按 scope 分流，避免多实例串扰）；
// titleKey / emptyKey 供标题与空态文案（知识库 vs 工具）。
const props = defineProps({
  kinds: { type: Array, default: () => [] },
  scope: { type: String, default: 'knowledge' },
  titleKey: { type: String, default: 'fileTree.mode_knowledge' },
  emptyKey: { type: String, default: 'fileTree.kb_empty' },
})

const { t } = useI18n()

const i18nNewDir = computed(() => t('fileTree.new_folder'))

const rootRef = ref(null) // 根节点（多实例：用于判定本实例是否可见）
const root = ref('')
const rootName = ref('capability')
const kbHeaderTitle = computed(() => rootName.value || t(props.titleKey))

// 类型范围过滤：kinds 非空 → 仅保留该范围内的目录/文件（知识库 = 非 tool；工具 = 仅 tool）
const kindFilter = computed(() => new Set(props.kinds || []))
const filtering = computed(() => kindFilter.value.size > 0)

// 路径相对知识库根（正斜杠；根自身 → 空串，越界 → 原样）
function relToRoot(p) {
  const r = String(root.value || '').replace(/\\/g, '/').replace(/\/+$/, '')
  const x = String(p || '').replace(/\\/g, '/')
  if (!r) return x
  if (x === r) return ''
  return x.startsWith(r + '/') ? x.slice(r.length + 1) : x
}

// 目录是否落在类型范围内：类型目录 / 类型目录下子目录按 token 判定；
// 通用容器目录（无类型 token，如 capability 根、`knowledge/`）仅在其为某允许类型目录的祖先时显示。
function dirInScope(path) {
  if (!filtering.value) return true
  const tok = nearestTypeToken(path)
  if (tok) return kindFilter.value.has(tok)
  const rel = relToRoot(path)
  if (rel === '') return true
  for (const k of props.kinds) {
    const kr = TYPE_DIR_REL[k]
    if (kr && (kr === rel || kr.startsWith(rel + '/'))) return true
  }
  return false
}

// 文件是否落在类型范围内：按后端给出的原语类型 token 判定
function fileInScope(raw) {
  if (!filtering.value) return true
  return kindFilter.value.has(raw.type || '')
}

// 知识库三级（系统/用户/项目；12-数据层）：头部切换，一次展示一级
const KB_LEVELS = [
  { kind: 'app', label: 'fileTree.kb_level_app' },
  { kind: 'user', label: 'fileTree.kb_level_user' },
  { kind: 'project', label: 'fileTree.kb_level_project' },
]
const kbLevel = ref('app')
const treeData = ref([])
const selectedKey = ref('')
const loading = ref(false)
const _unsubs = []

const ctx = ref({ visible: false, x: 0, y: 0, items: [], data: null })
const editingPath = ref('')
const editingValue = ref('')
const editingOrigPath = ref('')
const editingIsNew = ref(false)
// 正在改名的行是目录还是文件（I-73②：目录行必须走 rename-dir 目录语义，文件行走 rename 文件语义）
const editingIsDir = ref(false)

// ── 加载 ──
function treeNode(raw) {
  const n = { label: raw.name, path: raw.path.replace(/\\/g, '/'), is_dir: raw.is_dir }
  if (raw.is_dir) {
    n.children = []
    n.expanded = false
    n._loading = false
    n.type = ''
  } else {
    n.type = raw.type || ''
  }
  return n
}

async function loadChildren(node) {
  if (!node || !node.is_dir || node._loading) return
  node._loading = true
  try {
    const res = await listPrimitives(node.path)
    const dirs = (res.dirs || [])
      .map(raw => treeNode({ ...raw, is_dir: true })) // 后端 dirs 条目不含 is_dir，目录本身即目录
      .filter(n => dirInScope(n.path)) // 类型范围过滤（工具页签 / 知识库页签互不显示对方目录）
    const files = (res.files || [])
      .filter(f => f.type && f.type !== 'file') // 仅 *.type.md / 类型目录下的原语
      .filter(fileInScope) // 类型范围过滤
      .map(raw => treeNode({ ...raw, is_dir: false }))
    node.children = [...dirs, ...files]
    node.expanded = true
  } catch (e) {
    node.children = []
    console.warn('[KT] loadChildren error', node.path, e && e.message)
    if (node.path === root.value) treeData.value = []
  } finally {
    node._loading = false
  }
}

async function loadRoot() {
  loading.value = true
  try {
    const res = await getKnowledgeRoot(kbLevel.value)
    root.value = (res && res.root) || ''
    const lv = KB_LEVELS.find(l => l.kind === kbLevel.value)
    rootName.value = t(props.titleKey) + (lv ? ' -' + t(lv.label) : '')
    const rnode = { label: rootName.value, path: root.value, is_dir: true, expanded: false, children: [], _loading: false, type: '' }
    treeData.value = [rnode]
  } catch (e) {
    treeData.value = []
  } finally {
    loading.value = false
  }
}

// 切换知识库级别（系统/用户/项目）：重置并重新加载根
async function switchLevel({ kind }) {
  if (!kind || kind === kbLevel.value) return
  kbLevel.value = kind
  selectedKey.value = ''
  treeData.value = []
  await loadRoot()
}

async function reload() {
  treeData.value = []
  await loadRoot()
}

async function expandRoot() {
  if (treeData.value.length && treeData.value[0].path === root.value && treeData.value[0].children && !treeData.value[0].expanded) {
    await toggleNode(treeData.value[0], true)
  }
}

// ── 展开 / 点击 ──
async function toggleNode(node, forceExpand) {
  if (!node.is_dir || node._loading) return
  if (node.expanded && !forceExpand) {
    node.expanded = false
    return
  }
  node.expanded = true
  await loadChildren(node)
}

function onToggle(node) {
  toggleNode(node, false)
}

function onRowClick(node) {
  // 重命名态中点击其它节点 → 提交并退出（tree-row 是 div 不可聚焦，光靠 input 的
  // blur 不保证触发；显式切换即退出，避免重命名输入框残留）
  if (editingPath.value && editingPath.value !== node.path) {
    confirmEdit()
  }
  selectedKey.value = node.path
  if (node.is_dir) {
    onToggle(node)
    return
  }
  mq.emit(EventNames.fileOpen, { path: node.path, temporary: true })
}

function onRowDblClick(node) {
  if (editingPath.value && editingPath.value !== node.path) {
    confirmEdit()
  }
  selectedKey.value = node.path
  if (node.is_dir) {
    onToggle(node)
    return
  }
  mq.emit(EventNames.fileOpen, { path: node.path, temporary: false })
}

// ── 内联改名 ──
function findNode(nodes, p) {
  for (const n of nodes) {
    if (n.path === p) return n
    if (n.children) {
      const f = findNode(n.children, p)
      if (f) return f
    }
  }
  return null
}

async function refreshNode(node) {
  if (!node || !node.is_dir) return
  await loadChildren(node)
}

async function refreshPathDir(dirPath) {
  const node = findNode(treeData.value, dirPath) || (treeData.value[0] && treeData.value[0].path === dirPath ? treeData.value[0] : null)
  if (node) await refreshNode(node)
}

async function refreshParentOf(path) {
  const p = String(path).replace(/\\/g, '/')
  const parentPath = parentOfPath(p)
  const node = findNode(treeData.value, parentPath)
  if (node && node.expanded) await refreshNode(node)
}

// 父目录路径（知识库路径统一正斜杠；顶层项 → 知识库根）
function parentOfPath(path) {
  const p = String(path).replace(/\\/g, '/')
  const idx = p.lastIndexOf('/')
  return idx > 0 ? p.slice(0, idx) : root.value
}

// ── 拖拽移动（FP：知识库树同级移动，只改路径不改内容）──
// 复用 TreeNode 既有 drag-* 事件，拖拽源登记与 FileTree 同机制（window.__chonkDragPath）。
// 移动走**知识库域** data-knowledge-rename / rename-dir（按文件/目录二选一，见 api/knowledge.js
// movePrimitive）—— G-26：filesys 自 G-21 起只按 instance 登记表解析 work_dir、不采信载荷，
// 知识库 app/user 级 capability 路径在工作目录之外，借 filesys.rename 自报 work_dir 会被拒。
// 范围限制：系统级（app）只读 → 禁止拖拽移动（与场景 app 级只读口径一致）；只在当前知识库
// 根内同级移动（源与目标都必须落在 root 下）。
const dragOverPath = ref('')

function isKbReadonly() {
  return kbLevel.value === 'app'
}

function onTreeDragStart(node, e) {
  // app 级（系统级）只读；知识库根节点不可拖
  if (isKbReadonly() || node.path === root.value) {
    if (e && e.preventDefault) e.preventDefault() // 取消拖拽
    if (isKbReadonly()) message.warning(t('fileTree.kb_readonly'))
    return
  }
  dragOverPath.value = ''
  window.__chonkDragPath = node.path
}

function onTreeDragEnd() {
  dragOverPath.value = ''
  window.__chonkDragPath = ''
}

function onTreeDragOver(node) {
  const src = window.__chonkDragPath || ''
  if (!src || src === node.path) {
    dragOverPath.value = ''
    return
  }
  dragOverPath.value = node.path
}

function onTreeDragLeave(node) {
  if (dragOverPath.value === node.path) dragOverPath.value = ''
}

// drop：目录 → 移入该目录；文件 → 移入其父目录；同目录不动作；
// 自身/自身子目录 → 拒绝；同名冲突 → 后端拒绝并提示（不静默覆盖）
async function onTreeDrop(target) {
  const srcPath = window.__chonkDragPath || ''
  window.__chonkDragPath = ''
  dragOverPath.value = ''
  if (!srcPath || srcPath === target.path) return
  if (isKbReadonly()) {
    message.warning(t('fileTree.kb_readonly'))
    return
  }
  const srcNode = findNode(treeData.value, srcPath)
  if (!srcNode) return
  const srcParent = parentOfPath(srcPath)
  const targetDir = (target.is_dir ? target.path : parentOfPath(target.path)).replace(/\\/g, '/')
  if (!targetDir || targetDir === srcParent) return // 同目录不动作
  // 同级限制：源与目标必须在当前知识库根内
  if (!srcPath.startsWith(root.value) || !targetDir.startsWith(root.value)) return
  const name = srcPath.split('/').pop()
  const newPath = targetDir.replace(/\/+$/, '') + '/' + name
  if (newPath === srcPath || newPath.startsWith(srcPath + '/')) { // 自身 / 自身子目录
    message.warning(t('fileTree.move_into_self'))
    return
  }
  await doMove(srcNode, srcParent, targetDir, newPath)
}

async function doMove(srcNode, srcParent, targetDir, newPath) {
  const name = srcNode.label
  try {
    // 目录 / 文件语义分流（同 confirmEdit，I-73②）：目录走 rename-dir（名称原样、可跨目录移动），
    // 文件走 rename（保留 *.type.md 后缀）；目标已存在时由后端拒绝（不覆盖）。
    await movePrimitive(srcNode.path, newPath, root.value, !!srcNode.is_dir)
    message.success(t('fileTree.moved', { name }))
  } catch (e) {
    // 同名冲突等由后端拒绝（data-knowledge-* 无 overwrite 入参）→ 明确提示原因，不静默失败
    message.error(t('fileTree.move_failed_detail', { name, error: e?.message || t('fileTree.move_failed') }))
    return
  }
  // 刷新源目录（移除旧项）与目标目录（展开并载入新项；与 FileTree.doMove 同口径：
  // 目标目录此前若为收起态，仅刷新不展开会导致移入项不可见）
  await refreshPathDir(srcParent)
  await openAndRefreshDir(targetDir)
}

// 进入内联改名。isDir = 行语义（目录 → 改名时走 renamePrimitiveDir）。
// 输入框预填当前/默认名并**全选**，用户可直接输入覆盖。
function startEdit(path, value, isNew, isDir) {
  editingPath.value = path
  editingValue.value = value
  editingOrigPath.value = path
  editingIsNew.value = isNew
  editingIsDir.value = !!isDir
  nextTick(() => {
    // Input.vue 根节点即 <input>（class 直接落在 input 上），不存在后代 input → 直接选中它
    const input = document.querySelector('.knowledge-tree .inline-edit-input')
    if (!input) return
    input.focus()
    input.select()
  })
}

async function confirmEdit() {
  const path = editingPath.value
  if (!path) return
  const val = editingValue.value.trim()
  const orig = editingOrigPath.value
  const isNew = editingIsNew.value
  const isDir = editingIsDir.value
  const oldName = path.split(/[/\\]/).pop()
  const parentDir = orig.slice(0, Math.max(orig.lastIndexOf('/'), 0))
  editingPath.value = ''
  editingValue.value = ''
  editingIsNew.value = false
  editingIsDir.value = false
  if (!val || val === oldName) {
    // 改名未发生（空/与原名相同）：新建态视为放弃 → 删除刚建的占位目录
    if (isNew) {
      try { await deletePrimitiveDir(orig) } catch (e) { /* noop */ }
    }
    await refreshPathDir(parentDir)
    return
  }
  try {
    // 目录 / 文件语义分流（I-73②）：目录行必须走 rename-dir，否则目标名被追加 .md
    if (isNew || isDir) {
      await renamePrimitiveDir(orig, val)
    } else {
      await renamePrimitive(orig, val)
    }
    message.success(t('fileTree.renamed', { name: val }))
  } catch (e) {
    message.error(e?.message || t('fileTree.rename_failed'))
    if (isNew) {
      try { await deletePrimitiveDir(orig) } catch (e) { /* noop */ }
    }
  }
  await refreshPathDir(parentDir)
}

async function cancelEdit() {
  const path = editingPath.value
  const isNew = editingIsNew.value
  const orig = editingOrigPath.value
  editingPath.value = ''
  editingValue.value = ''
  editingIsNew.value = false
  editingIsDir.value = false
  if (isNew && path) {
    try { await deletePrimitiveDir(orig) } catch (e) { /* noop */ }
  }
}

function onKeyDown(e) {
  if (editingPath.value) return
  // 多实例（知识库 / 工具两页签共用同一 document keydown）→ 非可见实例不接管按键（避免隐藏树被改名）
  if (rootRef.value && rootRef.value.offsetParent === null) return
  // 焦点在可输入元素内（input / textarea / contenteditable）→ 不接管按键（F2 改名同理）
  const el = document.activeElement
  const tag = el?.tagName?.toLowerCase()
  if (tag === 'input' || tag === 'textarea' || (el && el.isContentEditable)) return
  if (!selectedKey.value) return
  const sel = findNode(treeData.value, selectedKey.value)
  if (!sel) return
  if (e.key === 'F2') {
    e.preventDefault()
    startEdit(sel.path, sel.label, false, sel.is_dir)
  }
}

// 类型显示名
function typeLabel(tok) {
  return { tool: t('fileTree.type_tool'), skill: t('fileTree.type_skill'), prompt: t('fileTree.type_prompt'), resource: t('fileTree.type_resource') }[tok] || tok
}

// ── 右键菜单 ──
function openCtx(e, items, data) {
  let x = e.clientX
  let y = e.clientY
  const w = 180
  if (x + w > window.innerWidth) x = window.innerWidth - w - 8
  ctx.value = { visible: true, x, y, items, data }
}

function blankItems() {
  return [{ key: 'mkdir', label: t('fileTree.new_folder') }]
}

function dirItems(node) {
  const tok = nearestTypeToken(node.path)
  const items = []
  if (tok) {
    items.push({ key: 'newType', label: t('fileTree.new_of_type', { type: typeLabel(tok) }) })
    items.push({ divider: true })
  }
  items.push({ key: 'mkdir', label: t('fileTree.new_folder') })
  items.push({ divider: true })
  items.push({ key: 'rename', label: t('fileTree.rename') })
  items.push({ key: 'delete', label: t('fileTree.delete'), danger: true })
  return items
}

function fileItems(node) {
  return [
    { key: 'rename', label: t('fileTree.rename') },
    { key: 'delete', label: t('fileTree.delete'), danger: true },
  ]
}

function onBlankContext(e) {
  if (e.target.closest('.tree-row')) return
  openCtx(e, blankItems(), null)
}

function onRowContext(e, node) {
  openCtx(e, node.is_dir ? dirItems(node) : fileItems(node), node)
}

function closeCtx() { ctx.value.visible = false }

function runCtx(item) {
  const d = ctx.value.data
  closeCtx()
  if (item.key === 'mkdir') createDir(d ? d.path : root.value)
  else if (item.key === 'newType') createTypeFile(d.path, nearestTypeToken(d.path))
  else if (item.key === 'rename' && d) renameNode(d)
  else if (item.key === 'delete' && d) removeNode(d)
}

// ── 动作 ──
// 展开目录并重新读取其子项（新建/改名后刷新父目录用；不依赖父节点此前是否已展开）
async function openAndRefreshDir(dirPath) {
  const node = findNode(treeData.value, dirPath)
  if (!node || !node.is_dir) return null
  node.expanded = true
  await refreshNode(node)
  return node
}

// 新建子目录：与 FileTree.startInlineCreate 同范式（I-73① 修复）——
// createPrimitiveDir 返回**路径字符串**（旧实现当 {path} 二次解包 → target 恒空 → 直接 return，
// 既不刷新父目录也不进入内联改名）→ 建目录后刷新并展开父目录 → 新行入树 →
// 选中并立即进入内联改名（预填后端实际落盘名，全选可覆盖）
async function createDir(parentPath) {
  const parent = String(parentPath || root.value || '').replace(/\\/g, '/')
  let target = ''
  try {
    target = String((await createPrimitiveDir(parent, i18nNewDir.value)) || '').replace(/\\/g, '/')
  } catch (e) {
    message.error(e?.message || t('fileTree.create_failed'))
    return
  }
  if (!target) {
    message.error(t('fileTree.create_failed'))
    return
  }
  await openAndRefreshDir(parent)
  selectedKey.value = target
  startEdit(target, target.split('/').pop() || i18nNewDir.value, true, true)
}

async function createTypeFile(dirPath, token) {
  if (!token) return
  const name = await promptInput(t('fileTree.new_of_type_prompt', { type: typeLabel(token) }), '')
  if (!name) return
  try {
    // createPrimitive 亦返回路径**字符串**（同 I-73① 的二次解包口径）
    const newPath = String((await createPrimitive(dirPath, token, name)) || '').replace(/\\/g, '/')
    message.success(t('fileTree.created', { name }))
    // 刷新类型目录自身（新文件直接出现在该目录下）；节点未加载时回退刷新其父目录
    const dirNode = findNode(treeData.value, String(dirPath).replace(/\\/g, '/'))
    if (dirNode && dirNode.is_dir) await refreshNode(dirNode)
    else await refreshParentOf(dirPath)
    if (newPath) {
      const node = findNode(treeData.value, newPath)
      if (node) {
        selectedKey.value = node.path
        mq.emit(EventNames.fileOpen, { path: node.path, temporary: false })
      }
    }
  } catch (e) {
    message.error(e?.message || t('fileTree.create_failed'))
  }
}

async function renameNode(node) {
  startEdit(node.path, node.label, false, node.is_dir)
}

async function removeNode(node) {
  try {
    const type = t(node.is_dir ? 'fileTree.folder' : 'fileTree.file')
    const suffix = node.is_dir ? t('fileTree.confirm_delete_folder_suffix') : ''
    await confirm(
      t('fileTree.confirm_delete', { type, label: node.label }) + suffix,
      t('fileTree.confirm_delete_title')
    )
  } catch { return }
  try {
    if (node.is_dir) await deletePrimitiveDir(node.path)
    else await deletePrimitive(node.path)
    message.success(t('fileTree.deleted', { name: node.label }))
    await refreshParentOf(node.path)
  } catch (e) {
    message.error(e?.message || t('fileTree.delete_failed'))
  }
}

// 外部改动（预览面板保存 / 磁盘变化）→ 刷新对应父目录
// filesys.changed：children=目录批次仅树级消费；这里只处理单文件/目录自身变化
function onFileChanged(data) {
  if (!data || Array.isArray(data.children)) return
  const p = data && (data.path || data.file)
  if (!p || !root.value) return
  const path = String(p).replace(/\\/g, '/')
  if (!path.startsWith(root.value)) return
  refreshParentOf(path)
}

onMounted(() => {
  loadRoot()
  _unsubs.push(mq.on(EventNames.kbCtxAction, ({ key, scope: s }) => {
    // 多实例（知识库 / 工具）共用同一事件：仅受理本实例（scope 匹配或缺省广播）
    if (s && s !== props.scope) return
    if (key) runCtx({ key })
  }))
  _unsubs.push(mq.on(EventNames.kbLevelSelect, switchLevel))
  _unsubs.push(mq.on(EventNames.fileChanged, onFileChanged))
  _unsubs.push(mq.on(EventNames.fileOpen, ({ path }) => {
    // 打开文件时若知识库树根未展开 → 自动展开到目标父目录（导航联动）
    if (path && root.value && String(path).startsWith(root.value)) {
      const target = findNode(treeData.value, String(path).replace(/\\/g, '/'))
      if (!target) expandRoot()
    }
  }))
  document.addEventListener('mousedown', closeCtx)
  document.addEventListener('keydown', onKeyDown)
})

onUnmounted(() => {
  for (const fn of _unsubs) fn()
  _unsubs.length = 0
  document.removeEventListener('mousedown', closeCtx)
  document.removeEventListener('keydown', onKeyDown)
})

defineExpose({ reload, loadRoot })
</script>

<style scoped>
.knowledge-tree {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.kb-tree-header {
  display: flex;
  align-items: center;
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
.kb-tree-title {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-transform: none;
  letter-spacing: 0;
  font-weight: 500;
}
.kb-level-switch {
  display: flex;
  align-items: center;
  gap: 0;
  flex-shrink: 0;
  text-transform: none;
}
.kb-level-switch :deep(.b-btn) {
  font-size: 11px;
  padding: 0 4px;
  min-height: 20px;
  color: var(--text-muted);
}
.kb-level-switch :deep(.b-btn.active) {
  color: var(--accent);
}
.kb-tree-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  user-select: none;
  position: relative;
}
.kb-tree-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--text-secondary, #888);
  font-size: 12px;
  padding: 0 12px;
  text-align: center;
}
.kb-ctx {
  position: fixed;
  z-index: 10000;
  min-width: 150px;
  padding: 4px;
  border: 1px solid var(--border, #dee2e6);
  border-radius: 6px;
  background: var(--bg-primary, #fff);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
}
.kb-ctx-item {
  padding: 6px 12px;
  border-radius: 4px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  white-space: nowrap;
}
.kb-ctx-item:hover {
  background: var(--bg-hover, #f0f0f0);
}
.kb-ctx-item.danger {
  color: var(--danger, #f56c6c);
}
.kb-ctx-item.danger:hover {
  background: color-mix(in srgb, var(--danger, #dc3545) 10%, transparent);
}
.kb-ctx-sep {
  height: 1px;
  margin: 4px 8px;
  background: var(--border, #dee2e6);
}
</style>
