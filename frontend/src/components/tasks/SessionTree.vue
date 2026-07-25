<template>
  <div class="session-tree">
    <div class="tree-scroll">
      <EmptyState v-if="loading && treeData.length === 0" message="Loading..." />
      <EmptyState v-else-if="treeData.length === 0" message="No sub-sessions" />
      <SessionTreeNode
        v-for="node in treeData"
        :key="node.session_id"
        :node="node"
        :depth="0"
        :selected-id="selectedId"
        :get-status="getStatus"
        @select="handleSelect"
        @toggle-expand="onToggleExpand"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { listAllSessions, getActiveSessionID } from '../../api/session'
import bridge from '../../utils/bridge'
import EmptyState from '../common/EmptyState.vue'
import SessionTreeNode from './SessionTreeNode.vue'

const subSessionStatus = ref({})

const sessions = ref([])
const treeData = ref([])
const selectedId = ref(null)
const loading = ref(false)

// 持久化展开状态的 Map<session_id, boolean>
const expandedMap = ref({})

// Track all known session IDs across loads, for detecting new sessions
const allSessionIds = ref(new Set())

function getStatus(sessionId) {
  return subSessionStatus.value[sessionId] || 'idle'
}

function buildSubTree(flatSessions, topLevelSessionID) {
  // 构建 Map<session_id, node>
  const nodeMap = {}
  for (const s of flatSessions) {
    nodeMap[s.session_id] = {
      ...s,
      children: [],
      expanded: expandedMap.value[s.session_id] === true, // 默认折叠
    }
  }

  // 只构建属于 topLevelSessionID 的子树的节点
  // 先找出所有节点的 ancestors（追溯到 topLevelSessionID）
  function isDescendantOfTopLevel(sessionId) {
    let current = sessionId
    const visited = new Set()
    while (current && current !== topLevelSessionID) {
      if (visited.has(current)) return false // 循环引用
      visited.add(current)
      const node = nodeMap[current]
      if (!node || !node.parent_id) return false
      current = node.parent_id
    }
    return current === topLevelSessionID
  }
  const inSubTree = new Set()
  for (const s of flatSessions) {
    if (s.parent_id && isDescendantOfTopLevel(s.session_id)) {
      inSubTree.add(s.session_id)
      // 也把祖先节点加进来（除了 topLevel 本身）
      let cur = s.parent_id
      while (cur && cur !== topLevelSessionID) {
        inSubTree.add(cur)
        const parent = flatSessions.find(x => x.session_id === cur)
        cur = parent ? parent.parent_id : ''
      }
    }
  }

  // 第二布：建立父子关系，只处理子树内的节点
  const roots = []
  for (const s of flatSessions) {
    if (!inSubTree.has(s.session_id)) continue
    const node = nodeMap[s.session_id]
    if (s.parent_id && nodeMap[s.parent_id]) {
      if (nodeMap[s.parent_id].session_id === topLevelSessionID) {
        // 直接挂在 topLevel 下的节点 → 作为根
        roots.push(node)
      } else if (inSubTree.has(s.parent_id)) {
        // 挂在子树内部节点下
        if (detectCycle(nodeMap, s.session_id)) {
          roots.push(node)
          continue
        }
        nodeMap[s.parent_id].children.push(node)
      }
    }
  }

  // 按 created_at 降序排序（同级）
  function sortByCreatedAt(nodes) {
    nodes.sort((a, b) => {
      const ta = a.created_at || a.createdAt || ''
      const tb = b.created_at || b.createdAt || ''
      return tb.localeCompare(ta)
    })
    for (const n of nodes) {
      if (n.children.length > 0) sortByCreatedAt(n.children)
    }
  }
  sortByCreatedAt(roots)
  return roots
}

function detectCycle(nodeMap, sessionId) {
  const visited = new Set()
  let current = sessionId
  while (current && nodeMap[current]) {
    if (visited.has(current)) return true
    visited.add(current)
    current = nodeMap[current].parent_id
  }
  return false
}

// ── Helpers for auto-select logic ──

function collectAllIds(nodes) {
  const ids = new Set()
  function walk(ns) {
    for (const n of ns) {
      ids.add(n.session_id)
      walk(n.children)
    }
  }
  walk(nodes)
  return ids
}

function findNodeById(nodes, sessionId) {
  for (const n of nodes) {
    if (n.session_id === sessionId) return n
    if (n.children.length > 0) {
      const found = findNodeById(n.children, sessionId)
      if (found) return found
    }
  }
  return null
}

function findFirstChild(nodes) {
  if (nodes.length === 0) return null
  return nodes[0]
}

function findNewSessions(tree) {
  const currentIds = collectAllIds(tree)
  const newIds = []
  for (const id of currentIds) {
    if (!allSessionIds.value.has(id)) {
      newIds.push(id)
    }
  }
  return newIds
}

function selectSession(node) {
  selectedId.value = node.session_id
  bridge.emit('subsessionchanged', { session_id: node.session_id })
}

async function loadSessions() {
  if (loading.value) return
  loading.value = true
  try {
    // 获取当前 active top-level session
    let topID = null
    const activeRes = await getActiveSessionID()
    if (activeRes && activeRes.session_id) {
      topID = activeRes.session_id
    }

    if (!topID) {
      treeData.value = []
      if (selectedId.value) {
        selectedId.value = null
        bridge.emit('subsessionchanged', { session_id: '' })
      }
      return
    }

    const res = await listAllSessions()
    const all = res.sessions || []
    sessions.value = all
    treeData.value = buildSubTree(all, topID)

    // ── Auto-select logic ──
    const newIds = findNewSessions(treeData.value)

    if (treeData.value.length > 0) {
      if (newIds.length > 0) {
        // New sub-session(s) detected
        if (selectedId.value && getStatus(selectedId.value) === 'running') {
          // Current selected session is still active — keep it
        } else {
          // Switch to newest new session
          const target = findNodeById(treeData.value, newIds[0])
          if (target) selectSession(target)
        }
      } else if (!selectedId.value) {
        // No previous selection — auto-select first child
        const first = findFirstChild(treeData.value)
        if (first) selectSession(first)
      }
      // else: selection exists and no new sessions — keep current selection
    } else {
      // No sub-sessions at all
      if (selectedId.value) {
        selectedId.value = null
      }
      bridge.emit('subsessionchanged', { session_id: '' })
    }

    // Update tracking set for next load
    allSessionIds.value = collectAllIds(treeData.value)

  } catch (e) {
    console.error('Failed to load sessions:', e)
    treeData.value = []
  } finally {
    loading.value = false
  }
}

function handleSelect(session) {
  selectSession(session)
}

function onToggleExpand(sessionId) {
  const node = findNodeById(treeData.value, sessionId)
  if (node) {
    node.expanded = !node.expanded
    expandedMap.value[sessionId] = node.expanded
  }
}

// Debounce helper
let debounceTimer = null
function debouncedLoadSessions() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    debounceTimer = null
    loadSessions()
  }, 300)
}

// Handle new sub-session detected (event-driven)
function handleNewSubSession() {
  debouncedLoadSessions()
}

// Handle unified session:event from backend
function handleSessionEvent(data) {
  loadSessions()
}

onMounted(() => {
  loadSessions()
  bridge.on('session:event', handleSessionEvent)
  bridge.on('session:refresh', debouncedLoadSessions)
  bridge.on('subsession:new', handleNewSubSession)
})

defineExpose({ loadSessions })

onUnmounted(() => {
})
</script>

<style scoped>
.session-tree {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.tree-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}
</style>