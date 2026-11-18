/**
 * taskTreeIndex — tasktree 节点派生索引（纯函数，无 Vue / mq 依赖）
 *
 * 长会话性能（评审第 11 条）：`nodes` 是 node_id → TaskTreeNode 的扁平 Map，
 * 层级由 `parent_node_id` 表达。此前每个树节点各自扫全量 Map 找直接子级
 * （每层 O(N)、整棵树 O(N²)），且 SessionTree 在导航/展开时临时重建索引。
 * 本模块把「按父取子」收口为**一次遍历**的索引：
 *   - `buildChildIndex`：parent_id → 子节点数组（每个子数组按时间倒序，新在前）；
 *   - `buildSessionNodeIndex`：session_id → session 节点（保持「命中第一个」语义）。
 * 索引只在 `useTaskView` 里由 computed 维护一次，所有组件共享同一份。
 *
 * 纯函数便于单测（`test/taskTreeIndex.test.js`）。
 */

/** 节点时间戳：created_at 优先；缺失回退 node_id（rt-* unix nano）与 seq。 */
export function nodeTimeOf(n) {
  const t = n.created_at ? new Date(n.created_at).getTime() : NaN
  if (Number.isFinite(t) && t > 0) return t
  const m = /rt-(\d{10,19})/.exec(n.node_id || '')
  if (m) return Math.floor(Number(m[1]) / 1e6) // unix nano → ms
  return n.seq || 0
}

/**
 * 一次遍历构建 parent_id → 子节点数组索引。
 * 每个子数组按 `timeOf` 倒序（新节点在前）——与 SessionTreeNode 既有展示顺序一致。
 * 孤儿节点（parent_id 不在 nodes 中）仍按其 parent_id 归档，由调用方决定是否展示
 * （树只从根递归，孤儿不会凭空出现；与既有行为一致）。
 *
 * @param {Map<string, object>|Iterable<[string, object]>} nodes node_id → 树节点
 * @param {(n: object) => number} [timeOf] 时间取值（默认 nodeTimeOf）
 * @returns {Map<string, object[]>}
 */
export function buildChildIndex(nodes, timeOf = nodeTimeOf) {
  const index = new Map()
  for (const [, n] of nodes) {
    if (!n) continue
    const parent = n.parent_node_id || ''
    const list = index.get(parent)
    if (list) list.push(n)
    else index.set(parent, [n])
  }
  for (const list of index.values()) {
    list.sort((a, b) => timeOf(b) - timeOf(a))
  }
  return index
}

/**
 * 一次遍历构建 session_id → session 节点索引。
 * 语义与既有 `nodeBySession` 一致：仅 `node_type === 'session'` 且 session_id 非空，
 * 同一 session_id 命中**第一个**（Map 迭代序）。
 *
 * @param {Map<string, object>|Iterable<[string, object]>} nodes
 * @returns {Map<string, object>}
 */
export function buildSessionNodeIndex(nodes) {
  const index = new Map()
  for (const [, n] of nodes) {
    if (!n || n.node_type !== 'session' || !n.session_id) continue
    if (!index.has(n.session_id)) index.set(n.session_id, n)
  }
  return index
}