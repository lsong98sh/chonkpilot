/**
 * 长会话性能（评审第 11 条）· 任务树共享索引 + 源码接线守卫。
 *
 * 子级关系此前由每个 SessionTreeNode 各自扫全量 `nodes`（O(N²)），SessionTree 导航时
 * 还临时重建索引。现收口为 `utils/taskTreeIndex.js` 纯函数，由 useTaskView 用 computed
 * 维护一次、全组件共享。本文件覆盖：
 *   ① 纯逻辑：nodeTimeOf 回退链 / buildChildIndex 无序输入 / 孤儿 / 时间倒序 / 同父多子；
 *   ② 源码守卫：SessionTreeNode / SessionTree / useTaskView 不再本地扫全量索引。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'
import { buildChildIndex, buildSessionNodeIndex, nodeTimeOf } from '../src/utils/taskTreeIndex.js'

// ── ① 纯逻辑：时间取值 ──────────────────────────────────────────

test('nodeTimeOf：created_at 优先，缺失回退 rt-*（unix nano）与 seq', () => {
  assert.equal(nodeTimeOf({ created_at: '2026-01-02T03:04:05.000Z' }), Date.parse('2026-01-02T03:04:05.000Z'))
  // rt-<unix nano> → ms
  assert.equal(nodeTimeOf({ node_id: 'rt-1700000000123456789' }), 1700000000123)
  // created_at 非法 → 回退 node_id，再回退 seq
  assert.equal(nodeTimeOf({ node_id: 'rt-1700000000123456789', created_at: 'not-a-date' }), 1700000000123)
  assert.equal(nodeTimeOf({ node_id: 'x', seq: 42 }), 42)
  assert.equal(nodeTimeOf({}), 0)
})

// ── ② 纯逻辑：一次遍历的父子索引 ────────────────────────────────

/** 构造 node_id → node 的 Map（输入顺序故意打乱）。 */
function nodeMap(list) {
  return new Map(list.map((n) => [n.node_id, n]))
}

test('buildChildIndex：无序输入 → parent_id 分组，每个子数组时间倒序', () => {
  const nodes = nodeMap([
    { node_id: 'b', parent_node_id: 'a', created_at: '2026-01-01T00:00:00Z' },
    { node_id: 'root', parent_node_id: '', created_at: '2026-01-04T00:00:00Z' },
    { node_id: 'c', parent_node_id: 'a', created_at: '2026-01-03T00:00:00Z' },
    { node_id: 'd', parent_node_id: 'c', created_at: '2026-01-02T00:00:00Z' },
  ])
  const idx = buildChildIndex(nodes)
  // 根节点归入 '' 键；a 下两个子节点新在前
  assert.deepEqual(idx.get('').map((n) => n.node_id), ['root'])
  assert.deepEqual(idx.get('a').map((n) => n.node_id), ['c', 'b'])
  assert.deepEqual(idx.get('c').map((n) => n.node_id), ['d'])
  assert.equal(idx.get('nope'), undefined)
  // 索引规模 = 不同 parent_id 数（不是 N² 结构）
  assert.equal(idx.size, 3)
})

test('buildChildIndex：孤儿节点按声明的 parent_id 归档，不丢失也不变根', () => {
  const nodes = nodeMap([
    { node_id: 'root', parent_node_id: '' },
    { node_id: 'orphan', parent_node_id: 'missing', created_at: '2026-01-01T00:00:00Z' },
  ])
  const idx = buildChildIndex(nodes)
  assert.deepEqual(idx.get('missing').map((n) => n.node_id), ['orphan'])
  assert.deepEqual(idx.get('').map((n) => n.node_id), ['root'])
})

test('buildChildIndex：同一父多子按时间倒序（nodeTimeOf 回退可用）', () => {
  const nodes = nodeMap([
    { node_id: 'p', parent_node_id: '' },
    { node_id: 'x1', parent_node_id: 'p', node_id_time: 1, seq: 1 },
    { node_id: 'rt-1700000000300000000', parent_node_id: 'p' },
    { node_id: 'x3', parent_node_id: 'p', created_at: '2026-02-01T00:00:00Z' },
  ])
  const idx = buildChildIndex(nodes)
  assert.deepEqual(idx.get('p').map((n) => n.node_id), ['x3', 'rt-1700000000300000000', 'x1'])
  // 自定义 timeOf 也可注入（纯函数可测）
  const bySeq = buildChildIndex(nodes, (n) => n.seq || 0)
  assert.deepEqual(bySeq.get('p').map((n) => n.node_id), ['x1', 'rt-1700000000300000000', 'x3'])
})

test('buildChildIndex：只遍历 nodes 一次（O(N) 建索引，非逐父扫描）', () => {
  const src = readSrc('utils/taskTreeIndex.js')
  const start = src.indexOf('export function buildChildIndex')
  const body = src.slice(start, src.indexOf('\n}', start))
  assert.equal((body.match(/for \(const \[, n\] of nodes\)/g) || []).length, 1,
    '索引构建只能对 nodes 遍历一次')
})

test('buildSessionNodeIndex：session_id → session 节点（同 id 命中第一个）', () => {
  const nodes = nodeMap([
    { node_id: 't1', node_type: 'task', session_id: 's1' }, // 非 session 节点不入索引
    { node_id: 's-1', node_type: 'session', session_id: 's1' },
    { node_id: 's-1b', node_type: 'session', session_id: 's1' },
    { node_id: 's-2', node_type: 'session', session_id: '' },
  ])
  const idx = buildSessionNodeIndex(nodes)
  assert.equal(idx.size, 1)
  assert.equal(idx.get('s1').node_id, 's-1', '保持既有「命中第一个」语义')
})

// ── ③ 源码守卫：索引只有一处、组件不再本地扫全量 ──────────────────

test('useTaskView：childIndex 由 computed 建一次，导出 childrenOf，级联用索引', () => {
  const src = readSrc('composables/useTaskView.js')
  assert.match(src, /const childIndex = computed\(\(\) => buildChildIndex\(nodes\)\)/,
    'childIndex 必须是基于 nodes 的 computed（只建一次、全组件共享）')
  assert.match(src, /function childrenOf\(parentId\)/, '须导出 childrenOf 供树组件取直接子级')
  assert.match(src, /childrenOf,\s*\n\s*refresh,/, 'useTaskView 返回值须含 childrenOf')
  // 级联标 closed：用 childrenOf 遍历，不再每个栈元素扫全量 nodes
  const start = src.indexOf('async function deleteTreeNode(')
  const body = src.slice(start, src.indexOf('\n}', start))
  assert.match(body, /for \(const child of childrenOf\(cur\)\)/, 'deleteTreeNode 级联须走共享索引')
  assert.doesNotMatch(body, /for \(const \[id, m\] of nodes\)/, '不得再扫全量 nodes')
  // nodeBySession 查表
  const bySession = src.slice(src.indexOf('function nodeBySession('), src.indexOf('function onSubLlmComplete('))
  assert.match(bySession, /sessionNodeIndex\.value\.get\(sessionId\)/)
  assert.doesNotMatch(bySession, /for \(const \[, n\] of nodes\)/)
})

test('SessionTreeNode：children 走 childrenOf，且无本地全量扫描/重复时间排序', () => {
  const vue = readSrc('views/tasks/SessionTreeNode.vue')
  assert.match(vue, /const children = computed\(\(\) => childrenOf\(props\.node\.node_id\)\)/)
  assert.doesNotMatch(vue, /for \(const \[, n\] of nodes\)/, '不得每节点扫全量 nodes')
  assert.doesNotMatch(vue, /function nodeTime\(/, '本地 nodeTime 重复实现须删除')
  assert.doesNotMatch(vue, /childrenSorted/)
})

test('SessionTree：删本地 buildChildIndex，导航/展开/自动选中复用共享索引', () => {
  const vue = readSrc('views/tasks/SessionTree.vue')
  assert.doesNotMatch(vue, /function buildChildIndex\(/, '本地索引构建须删除')
  assert.match(vue, /childIndex/, '须使用 useTaskView 的共享 childIndex')
  assert.match(vue, /const \{ loadTree, refresh, nodeTree, nodeTimeOf, childIndex \} = useTaskView\(\)/)
  // 既有行为调用点仍在（全树导航 / 展开父链 / 自动选中新子会话）
  assert.match(vue, /findNode\(treeData\.value, selectedId\.value, childIndex\.value\)/)
  assert.match(vue, /function expandTo\(nodeId\)/)
  assert.match(vue, /newestSessionNode\(treeData\.value, idx\)/)
})