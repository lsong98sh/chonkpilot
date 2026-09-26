<template>
  <div class="panel-inner">
    <div class="panel-header">
      <Icon name="list" />
      <span>{{ $t('common.sub_sessions') }}</span>
      <div class="header-spacer" />
      <Button text class="icon-btn" :title="$t('common.refresh_session_list')" v-mq:[EventNames.sessionRefresh].click>
        <Icon name="refresh" :size="11" />
      </Button>
    </div>
    <div class="tree-scroll">
      <EmptyState v-if="loading && treeData.length === 0" :message="$t('common.loading')" />
      <template v-else>
        <!-- 完全层级任务树（§一）：顶层 = 主会话直接派生的任务/会话节点混排，无主会话根节点 -->
        <SessionTreeNode
          v-for="node in treeData"
          :key="node.node_id"
          :node="node"
          :depth="0"
          :active-id="selectedId"
          :top-session="currentTopId"
        />
        <EmptyState
          v-if="treeData.length === 0"
          :message="$t('chat.no_sessions')"
        />
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import Icon from '../../components/icon/Icon.vue'
import { Button } from '../../components/ui'
import EmptyState from '../../components/common/EmptyState.vue'
import SessionTreeNode from './SessionTreeNode.vue'
import { useTaskView } from '../../composables/useTaskView'

const loading = ref(false)
const currentTopId = ref(null)
const selectedId = ref(null)
const { loadTree, refresh, nodeTree, nodeTimeOf } = useTaskView()

// 完全层级树：顶层 = parent 为空（主会话直接派生物），递归展开在 SessionTreeNode 内
const treeData = computed(() => (currentTopId.value ? nodeTree(currentTopId.value) : []))

function findNode(nodes, id) {
  for (const n of nodes) {
    if (n.node_id === id) return n
    const found = findNode(n.children, id)
    if (found) return found
  }
  return null
}

/** 按会话身份取 session 节点（节点主键 = task_id，会话身份在 session_id 字段）。 */
function findNodeBySession(nodes, sid) {
  if (!sid) return null
  for (const n of nodes) {
    if (n.node_type === 'session' && n.session_id === sid) return n
    const found = findNodeBySession(n.children || [], sid)
    if (found) return found
  }
  return null
}

/** 全树中 created_at 最新的 session 节点（时间取既有 nodeTimeOf 工具，缺失自动回退）。 */
function newestSessionNode(nodes) {
  let best = null
  let bestTime = -Infinity
  const walk = (list) => {
    for (const n of list) {
      if (n.node_type === 'session') {
        const t = nodeTimeOf(n)
        if (t > bestTime) {
          bestTime = t
          best = n
        }
      }
      walk(n.children || [])
    }
  }
  walk(nodes || [])
  return best
}

/** 选中节点（selectedId = node_id）并广播其会话身份（subsession-changed 载荷仍为 session_id）。 */
function setSelected(node) {
  if (!node) {
    selectedId.value = null
    mq.emit(EventNames.subsessionChanged, { session_id: '' })
    return
  }
  selectedId.value = node.node_id
  mq.emit(EventNames.subsessionChanged, { session_id: node.session_id || '' })
}

/** 展开某节点的父级链路，使新出现的节点无需手动展开即可见。 */
function expandTo(nodeId) {
  const map = new Map()
  const walk = (nodes) => {
    for (const n of nodes) {
      map.set(n.node_id, n)
      walk(n.children || [])
    }
  }
  walk(treeData.value)
  let cur = map.get(nodeId)
  while (cur) {
    cur.expanded = true
    cur = cur.parent_node_id ? map.get(cur.parent_node_id) : null
  }
}

async function loadTreeFor(topId) {
  if (!topId) return
  loading.value = true
  try {
    // full 模式：初始化即显示全部节点（含已完成工具调用），不做 llm/running 过滤。
    // 并行拉任务快照（data-tasktree-tasks）→ 待裁决明细 awaiting{options,timeout_s}（I-103：
    // 任务区据此**直接按 awaiting 渲染裁决条**，不依赖消息卡 —— 刷新/切会话后仍可见可操作）。
    await Promise.all([loadTree(topId, 'full'), refresh('', topId)])
  } finally {
    loading.value = false
  }
  // 保留仍存在的当前选中；否则自动选中全树 created_at 最新的 session 节点
  const cur = findNode(treeData.value, selectedId.value)
  if (cur) {
    mq.emit(EventNames.subsessionChanged, { session_id: cur.session_id || '' })
  } else {
    // 全树取时间最新的会话节点（I-36 后子会话节点挂在 llm_run 根下、非顶层）；
    // 全树无会话节点时保持不选中（selectedId 为空），不退回选中首个顶层节点。
    const latest = newestSessionNode(treeData.value)
    if (latest) {
      expandTo(latest.node_id)
      setSelected(latest)
    } else {
      selectedId.value = null
      mq.emit(EventNames.subsessionChanged, { session_id: '' })
    }
  }
}

function shouldAutoSelectNew() {
  // Auto-select the new sub-session when nothing is selected or the current
  // selection is not running. Keep the selection unchanged while the currently
  // selected node is still running.
  if (!selectedId.value) return true
  const cur = findNode(treeData.value, selectedId.value)
  return !cur || cur.status !== 'running'
}

onMounted(() => {
  const unsubs = []
  unsubs.push(mq.on(EventNames.sessionChanged, ({ session_id }) => {
    currentTopId.value = session_id
    if (session_id) loadTreeFor(session_id)
    else {
      selectedId.value = null
      mq.emit(EventNames.subsessionChanged, { session_id: '' })
    }
  }))
  unsubs.push(mq.on(EventNames.sessionRefresh, () => {
    if (currentTopId.value) loadTreeFor(currentTopId.value)
  }))
  // 任务启动/更新时自动展开父级链路，任务行无需手动展开即可见
  // （server tasks.* 载荷字段：task_id = 节点主键 = 节点 id，21-llm-server）
  const autoExpand = (data) => {
    if (!data || !data.task_id) return
    expandTo(data.task_id)
  }
  for (const t of [EventNames.taskStarted, EventNames.taskUpdated]) {
    unsubs.push(mq.on(t, autoExpand))
  }
  // Keep selectedId (node_id) in sync with user clicks / external selection changes
  // （外部 subsession-changed 载荷为 session_id，经 session 节点反查 node_id）
  unsubs.push(mq.on(EventNames.subsessionChanged, ({ session_id }) => {
    const n = session_id ? findNodeBySession(treeData.value, session_id) : null
    selectedId.value = n ? n.node_id : null
  }))
  // 会话创建（session-new）：后端会话行首次落库的唯一事件，按 parent_session_id 分流——
  //   - 无父（主会话）：刷新当前任务树（会话导航列表由 SessionsPane 订阅同一事件刷新）；
  //   - 有父（子会话，llm_run 派生）：重载当前 top_session 任务树，再走既有自动选中新节点。
  // 与另两个会话事件的职责区分（不重叠）：
  //   session-changed：前端当前会话身份已切换/新建（本地动作）→ 重置 currentTopId 并重载其任务树；
  //   session-refresh：手动/外部要求重载当前任务树（数据可能变化）；
  //   session-new：后端确认**新会话行**已落库 → 仅增量刷新，不改当前会话身份。
  unsubs.push(mq.on(EventNames.sessionNew, ({ session_id, parent_session_id }) => {
    if (!currentTopId.value) return
    const reload = loadTreeFor(currentTopId.value)
    if (!parent_session_id) return // 主会话：树已随 session-changed 重载，此处仅确保刷新
    reload.then(() => { // 子会话：重载后自动选中新节点（保留既有 newestSessionNode 兜底）
      if (shouldAutoSelectNew() && session_id) {
        const n = findNodeBySession(treeData.value, session_id)
        if (n) setSelected(n)
      }
    })
  }))
  onUnmounted(() => {
    for (const unsub of unsubs) unsub()
  })
})
</script>

<style scoped>
.tree-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 4px 0;
}
.panel-inner {
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
.header-spacer {
  flex: 1;
  min-width: 0;
}
</style>
