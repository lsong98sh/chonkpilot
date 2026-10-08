/**
 * useTaskView — 任务活动视图（完全层级任务树 / 运行态 / 取消）
 *
 * 数据双源（20-gui，任务编排在 server）：
 *   - `tasks`：task_id → TaskInfo 运行态（进程 / 输出 / 耗时），
 *     由 server 编排广播 tasks.started/updated/done 增量合并（载荷字段对齐：
 *     type→kind、tool_name→tool、status→state），查询走 data-tasktree-tasks。
 *   - `nodes`：node_id → TaskTreeNode（tasktree 树节点，层级唯一事实源），
 *     初始化/切换走 data-tasktree-list，任务管理器走 (full)，增量同样由
 *     tasks.* 事件 upsert。
 *     节点主键统一 = task_id（事件路径与落库路径同主键，parent_node_id 亦为 task_id）；
 *     llm 节点的会话身份存 session_id 字段（点击子会话/切换会话读该字段）。
 *   - 关闭走 data-tasktree-delete（级联删子树，持久生效）；取消走
 *     /publish task-stop（服务端恒按子树级联，含 llm_run 派生的子会话/工具）；
 *     主会话取消走 llm-cancel{session,turn}（21-llm-server，替代 CancelChat RPC）。
 *
 * 本 composable 是**模块级单例**：多个入口共享同一份快照与事件订阅。
 */
import { reactive } from 'vue'
import mq from '../utils/mq'
import { eventBelongsToInstance } from '../utils/instanceScope'
import { EventNames } from '../events/event-names'
import { newTurnId } from '../api/chat'
import { copyDslFields } from '../utils/dslView'

// data-tasktree-* 辅助：发送并等待回复。
function tasktreeReq(action, body = {}) {
  return mq.emit(`data-tasktree-${action}`, body).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(`data-tasktree-${action}: backend unreachable`)
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) {
      const e = new Error(emsg || `data-tasktree-${action} failed`)
      e.action = action
      throw e
    }
    return p
  })
}

// task_id → TaskInfo（含 tasks.started/updated/done 增量合并）
const tasks = reactive(new Map())

// node_id → TaskTreeNode（tasktree 节点，task/session 统一；parent_node_id 表达层级）
const nodes = reactive(new Map())

// 用户显式移除的 node_id 集合（`removeNode` 能力保留；「关闭任务」自 42 §2 (126) 起改为
// 逻辑删除 + 只读展示，不再走本地移除）
const dismissed = new Set()

let subscribed = false

/** 归一化事件/查询返回的任务字段（两处 JSON 键略有差异）。 */
function normalize(info) {
  if (info.elapsed == null && info.elapsed_seconds != null) {
    info.elapsed = info.elapsed_seconds
  }
  // 事件 payload 用 "output"，TaskList 快照 JSON 用 "partial_output"，统一为 output
  if (info.output == null && info.partial_output != null) {
    info.output = info.partial_output
  }
  // server tasks.* 载荷用 "state"（running/done/error/cancelled），前端统一为 status
  if (info.status == null && info.state != null) {
    info.status = info.state
  }
  // server tasks.* 载荷用 "kind"（21-llm-server），兼容旧 "type"
  if (info.kind == null && info.type != null) {
    info.kind = info.type
  }
  return info
}

function upsert(payload) {
  if (!payload || !payload.task_id) return
  if (dismissed.has(payload.task_id)) return
  const prev = tasks.get(payload.task_id) || {}
  tasks.set(payload.task_id, normalize({ ...prev, ...payload }))
  // 完成态保留展示（不自动删除）；「关闭」= 后端逻辑删除 + 本地标 closed 只读展示
  // （42 §2 (126)）；显式 removeNode 的节点进入 dismissed 不再复活
}

/**
 * 任务事件 → tasktree 节点（字段映射：type→kind、tool_name→tool、status→state）。
 * 主键统一 task_id：node_id = task_id（与 data-tasktree-list 落库主键一致），
 * llm 节点的会话身份另存 session_id（点击子会话/切换会话用）。
 * DSL-3 扩展字段（容器进度 / steps / shadow / `$RETURN` 两态）经 `copyDslFields`
 * **原样透传**（清单见 `utils/dslView.js` 的 `DSL_FIELDS`，实时事件与查询读取共用）——
 * payload 含则带上、缺省不写 → 增量事件不误清既有字段（配合 upsertNode 的合并）。 */
function nodeFromEvent(p) {
  const isLLM = p.kind === 'llm' || p.type === 'llm'
  // 标题：name 优先（与 DB tasktree.title=info.Name 一致），purpose 仅作回退
  const title = p.name || p.purpose || p.tool || p.tool_name || p.task_id
  const node = {
    node_id: p.task_id,
    node_type: isLLM ? 'session' : 'task',
    top_session: p.top_session || '',
    session_id: p.session_id || '',
    task_id: p.task_id || '',
    kind: isLLM ? 'llm' : (p.kind || p.type || 'tool'),
    parent_node_id: p.parent_id || '',
    title,
    status: p.state || p.status || 'running',
    created_at: p.started_at || new Date().toISOString(),
    updated_at: p.started_at || new Date().toISOString(),
  }
  // DSL-3 新字段随事件实时透传（payload 含则带上，缺省不写）
  copyDslFields(p, node)
  return node
}

function upsertNode(node) {
  if (!node || !node.node_id) return
  if (dismissed.has(node.node_id)) return
  const prev = nodes.get(node.node_id) || {}
  // 新节点默认展开（初始加载/增量均完整可见调用树）；已存在节点保留用户折叠状态
  const expanded = prev.expanded !== undefined ? prev.expanded : true
  nodes.set(node.node_id, { ...prev, ...node, expanded })
}

/**
 * 按 session_id 取会话节点（llm 节点主键 = task_id，会话身份存 session_id 字段）。
 * 命中一个 kind=llm 的 session 节点；未加载 → null。
 */
function nodeBySession(sessionId) {
  if (!sessionId) return null
  for (const [, n] of nodes) {
    if (n.node_type === 'session' && n.session_id === sessionId) return n
  }
  return null
}

/**
 * 子域 llm-complete（决策 3）：按 session_id 更新 LLM 会话节点终态。
 *  子会话节点（kind=llm，node_id=task_id；由 llm_run 委派派生）终态由 tasks.done 事件驱动，
 *  本订阅覆盖 batch 池会话节点（kind=pool）——SetNodeStatus 只写 DB 不发事件，
 *  此前靠容器终态的 markSubtreeTerminal 冗余同步兜底，现由子域事件可靠驱动
 *  （executeTask 的 llm-complete 先于容器终态发出，顺序有保证）。
 *  server 枚举映射（20-gui）：complete/completed→done、error/timeout→error、
 *  interrupted/cancelled→cancelled。
 *
 * 缺口 8：事件按 instance 过滤（只消费本 instance 的事件；桥已过滤，此处为前端二次防线）。
 */
function onSubLlmComplete(d) {
  if (!eventBelongsToInstance(d)) return
  if (!d || d.sub !== true || !d.session_id) return
  const node = nodeBySession(d.session_id)
  if (!node || dismissed.has(node.node_id)) return
  const mapStatus = {
    complete: 'done',
    completed: 'done',
    error: 'error',
    timeout: 'error',
    cancelled: 'cancelled',
    interrupted: 'cancelled',
  }[d.status]
  if (!mapStatus) return
  if (node.status === mapStatus) return // 树未加载该节点：终态由 ListTaskTree 读取
  upsertNode({ node_id: node.node_id, status: mapStatus })
}

/**
 * tasks.* 事件统一入口：运行时快照（tasks）+ 树节点（nodes）双写。
 * 缺口 8：先按 instance 过滤 —— 只消费本 instance 的事件（他实例的 tasks.* 丢弃，
 * 多 instance 同进程不串）；无 instance 归属 → 放行（旧行为）。
 */
function onTaskEvent(payload) {
  if (!eventBelongsToInstance(payload)) return
  if (!payload || !payload.task_id) return
  if (dismissed.has(payload.task_id)) return
  const node = nodeFromEvent(payload)
  if (dismissed.has(node.node_id)) return
  upsert(payload)
  upsertNode(node)
}

/** 从面板移除节点（完成态手动关闭）；同一 node_id 之后不再因事件/刷新出现。 */
function removeNode(id) {
  if (!id) return
  nodes.delete(id)
  tasks.delete(id)
  dismissed.add(id)
}

/**
 * 清除某任务的待裁决明细（I-103）：用户裁决动作成功后本地即时消隐，避免「刷新前残留」。
 * `awaiting` 随 `data-tasktree-tasks` 读入 `tasks` 快照（I-99），`upsert` 为增量合并
 * （`{...prev, ...payload}`）→ 后续无 awaiting 的快照不会自动抹掉旧值，故须显式删除。
 */
function clearAwaiting(taskId) {
  if (!taskId) return
  const t = tasks.get(taskId)
  if (t && t.awaiting) delete t.awaiting
  const n = nodes.get(taskId)
  if (n && n.awaiting) delete n.awaiting
}

/** 手动关闭节点：后端级联**逻辑删除**（标 `closed`/`deleted_at`，行保留），本地同步把整棵
 * 子树标为 `closed` 并**保留展示**（口径见 42 §2 (126)：已关闭节点同列表内灰色只读展示，
 * 级联子节点一并显示，不提供恢复/重开入口）。 */
async function deleteTreeNode(nodeId) {
  if (!nodeId) return
  try {
    await tasktreeReq('delete', { node_id: nodeId })
  } catch (e) {
    console.warn('[useTaskView] data-tasktree-delete failed:', e)
  }
  // 级联标 closed（与后端级联逻辑删除同范围）；节点仍在 `nodes`/`tasks` 中，供只读展示
  const stack = [nodeId]
  const seen = new Set()
  while (stack.length) {
    const cur = stack.pop()
    if (seen.has(cur)) continue
    seen.add(cur)
    const n = nodes.get(cur)
    if (n) n.closed = true
    const t = tasks.get(cur)
    if (t) t.closed = true
    for (const [id, m] of nodes) {
      if (m.parent_node_id === cur) stack.push(id)
    }
  }
}

/**
 * 加载 tasktree 节点（请求-响应）。
 * @param {string} topSession 聚合根主会话
 * @param {'init'|'full'} mode 统一传 'full'（加载全部节点，LLM 统一视为工具；
 *   init 仅为后端兼容保留，不再使用）。
 * 请求带 `include_closed: true`：已关闭（逻辑删除）节点一并返回，供只读灰色展示
 * （后端默认过滤语义不变，42 §2 (126)）。
 */
async function loadTree(topSession, mode) {
  if (!topSession) return
  try {
    const res = await tasktreeReq('list', {
      top_session: topSession, mode: mode || 'init', include_closed: true,
    })
    const list = (res && res.nodes) || []
    for (const n of list) upsertNode(n)
  } catch (e) {
    console.warn('[useTaskView] data-tasktree-list failed:', e)
  }
}

/** 按 top_session 构建完全层级树（顶层 = parent 为空或父节点不在集合内）。 */
function nodeTree(topSession) {
  const items = []
  for (const [, n] of nodes) {
    if (topSession && n.top_session !== topSession) continue
    items.push(n)
  }
  // 顶层按时间倒序：created_at 优先，缺失回退 node_id（rt-* unix nano）与 seq
  items.sort((a, b) => nodeTimeOf(b) - nodeTimeOf(a))
  const childrenOf = {}
  for (const n of items) {
    const list = childrenOf[n.parent_node_id] || (childrenOf[n.parent_node_id] = [])
    list.push(n)
  }
  const roots = []
  for (const n of items) {
    n.children = childrenOf[n.node_id] || []
    if (!n.parent_node_id || !childrenOf[n.parent_node_id] || !nodes.has(n.parent_node_id)) {
      roots.push(n)
    }
  }
  return roots
}

/** 节点时间戳：created_at 优先；缺失回退 node_id（rt-* unix nano）与 seq。 */
function nodeTimeOf(n) {
  const t = n.created_at ? new Date(n.created_at).getTime() : NaN
  if (Number.isFinite(t) && t > 0) return t
  const m = /rt-(\d{10,19})/.exec(n.node_id || '')
  if (m) return Math.floor(Number(m[1]) / 1e6) // unix nano → ms
  return n.seq || 0
}

/** 按 node_id 取树节点。 */
function getNode(nodeId) {
  return nodeId ? nodes.get(nodeId) : null
}

/** 节点运行态：节点主键 = task_id，直接取该 task_id 的运行时快照。 */
function nodeRuntime(nodeId) {
  const node = nodes.get(nodeId)
  if (!node) return null
  const tid = node.task_id || node.node_id
  return tid ? tasks.get(tid) || null : null
}

/** 只注册一次全局事件订阅（tasks.* 编排事件 + 关闭/三选取消）。 */
function ensureSubscribed() {
  if (subscribed) return
  subscribed = true
  mq.on(EventNames.taskStarted, onTaskEvent)
  mq.on(EventNames.taskUpdated, onTaskEvent)
  mq.on(EventNames.taskEnded, onTaskEvent)
  // 子域 llm-complete（决策 3）：池会话/子会话节点终态驱动（替代 markSubtreeTerminal）
  mq.on(EventNames.llmComplete, onSubLlmComplete)
  mq.on(EventNames.taskClose, ({ node_id }) => {
    deleteTreeNode(node_id)
  })
  mq.on(EventNames.taskStopChoice, ({ node_id }) => {
    cancelWithChoice(node_id)
  })
  // 会话树节点展开/折叠（纯前端事件 session-tree-node-toggle）：节点对象归本 store 持有，
  // 由持有方翻转 expanded（替代子组件直改 props.node.expanded；E-08）。
  mq.on(EventNames.sessionTreeNodeToggle, ({ node_id }) => {
    const n = nodes.get(node_id)
    if (n) n.expanded = !n.expanded
  })
}

function matches(t, sessionId, topSession) {
  if (sessionId && t.session_id !== sessionId) return false
  if (topSession && t.top_session !== topSession) return false
  return true
}

/**
 * 拉取任务快照（请求-响应）。打开面板/入口挂载时调用补全初始状态；
 * 后续靠 tasks.* 事件增量更新。保留用于运行态（badge/详情），树用 loadTree。
 * 请求带 `include_closed: true`（与 loadTree 同口径：已关闭节点一并返回，只读展示；
 * 后端默认过滤语义不变，42 §2 (126)）。
 * @param {{initOnly?: boolean}} opts initOnly=true 时仅加载 llm 与运行中任务
 */
async function refresh(sessionId = '', topSession = '', opts = {}) {
  try {
    const res = await tasktreeReq('tasks', {
      session_id: sessionId, top_session: topSession, include_closed: true,
    })
    const list = (res && res.list) || []
    if (Array.isArray(list)) {
      for (const t of list) {
        if (opts.initOnly && (t.kind || t.type) !== 'llm' && (t.status || t.state) !== 'running') continue
        upsert(t)
      }
    }
  } catch (e) {
    console.warn('[useTaskView] data-tasktree-tasks failed:', e)
  }
}

/** 运行中任务列表（按启动时间升序），可按 session/top_session 过滤。 */
function activeTasks(sessionId = '', topSession = '') {
  const out = []
  for (const t of tasks.values()) {
    if (t.status !== 'running') continue
    if (!matches(t, sessionId, topSession)) continue
    out.push(t)
  }
  out.sort((a, b) => (a.started_at || '').localeCompare(b.started_at || ''))
  return out
}

/** 某会话派生的全部任务（含完成态，按启动时间升序）。 */
function tasksOfSession(sessionId = '') {
  const out = []
  if (!sessionId) return out
  for (const t of tasks.values()) {
    if (t.session_id !== sessionId) continue
    out.push(t)
  }
  out.sort((a, b) => (a.started_at || '').localeCompare(b.started_at || ''))
  return out
}

/** 按 task_id 取任务快照（右侧任务详情视图用）。 */
function getTask(id) {
  return id ? tasks.get(id) : null
}

/** 运行中任务计数，可按 session/top_session/type 过滤。 */
function activeCount(sessionId = '', topSession = '', type = '') {
  let n = 0
  for (const t of tasks.values()) {
    if (t.status !== 'running') continue
    if (!matches(t, sessionId, topSession)) continue
    if (type && (t.kind || t.type) !== type) continue
    n++
  }
  return n
}

/**
 * 逐个取消任务（经 /publish task-stop，后端 Stop/StopCascade）。
 * @param {Array<string|{task_id: string, cascade?: boolean}>|string} ids
 * @param {{cascade?: boolean}} opts 整体级联开关（强制级联）
 */
function cancelTasks(ids, opts = {}) {
  const list = Array.isArray(ids) ? ids : [ids]
  for (const it of list) {
    const entry = (typeof it === 'object' && it !== null) ? it : { task_id: it }
    const cascade = entry.cascade === true || opts.cascade === true
    if (!entry.task_id) continue
    mq.emit(EventNames.taskStop, {
      task_id: entry.task_id,
      ...(cascade ? { cascade: 'all' } : {}),
    })
  }
}

/** 取节点自身的停止目标 id（节点主键 = task_id，即停止目标）。 */
function stopIdOf(node) {
  if (!node) return ''
  return node.task_id || node.node_id
}

/**
 * 节点 ▍ 停止（llm 型节点用，§〇.7）：**直接级联**停止子会话全部工具 + 子 LLM，无弹框。
 */
function cancelWithChoice(nodeId) {
  const node = nodes.get(nodeId)
  if (!node) return
  const id = stopIdOf(node)
  if (!id) return
  cancelTasks(id, { cascade: true })
}

/**
 * 主会话 cancel（20-gui）：发 llm-cancel{session,turn} 消息
 * （替代 CancelChat RPC；server 以 session+turn 定位轮次，取消终态收敛到
 * llm-complete{status:interrupted}）。turn 缺失时仅按 session 取消（server 解析运行中 turn）。
 * @param {{turnId: string, sessionId: string}} opts
 */
function confirmTurnCancel({ turnId, sessionId }) {
  if (!sessionId) return
  mq.emit(EventNames.llmCancel, {
    req_id: newTurnId(),
    session: sessionId,
    turn: turnId || '',
  })
}

export function useTaskView() {
  ensureSubscribed()
  return {
    tasks,
    nodes,
    refresh,
    loadTree,
    nodeTree,
    nodeTimeOf,
    getNode,
    nodeRuntime,
    deleteTreeNode,
    activeTasks,
    tasksOfSession,
    getTask,
    activeCount,
    cancelTasks,
    cancelWithChoice,
    confirmTurnCancel,
    removeNode,
    clearAwaiting,
  }
}
