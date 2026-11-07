/**
 * 会话消息合并去重（2026-11-07）——真机缺陷回归。
 *
 * 缺陷：tool_pair 本地流式条目 role='assistant'，DB 历史视图 role='tool'；旧合并按
 * role+type+content 判重 → 永不命中 → 历史重载后同一次工具调用渲染两张
 * （同 `tp_<tool_call_id>`，Vue `:key` 重复）。L4 实测 `.toolpair-section` 6 条 =
 * 同 3 个标签各两次、title 全 None。
 *
 * 本用例以期望值断言**条数与顺序**：同一 tool_call_id 只保留一条（历史在前，本地不重复追加）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mergeHistoryWithLocals, withoutDuplicates } from '../src/utils/sessionMerge.js'

function pair(callId, role, purpose) {
  return {
    id: 'tp_' + callId,
    role,
    type: 'tool_pair',
    tool_call_id: callId,
    purpose: purpose || '',
    content: undefined,
  }
}

test('历史 tool_pair + 本地流式 tool_pair（同 tool_call_id）→ 只保留 1 条，顺序 = 历史在前', () => {
  const hist = [pair('c1', 'tool', 'task_run(cmd)')]
  const locals = [pair('c1', 'assistant', '')]
  const merged = mergeHistoryWithLocals(hist, locals)
  assert.equal(merged.length, 1, '同一次工具调用只能有 1 个条目')
  assert.equal(merged[0].id, 'tp_c1')
  assert.equal(merged[0].role, 'tool', '保留历史视图（本地重复份被丢弃）')
})

test('3 条工具调用（批量 2 + file_read）重载后仍为 3 条且顺序不变', () => {
  const ids = ['tc-1', 'tc-2', 'tc-3']
  const hist = ids.map((x) => pair(x, 'tool', 'purpose-' + x))
  const locals = ids.map((x) => pair(x, 'assistant', ''))
  const merged = mergeHistoryWithLocals(hist, locals)
  assert.equal(merged.length, 3, '期望精确 3 条（不是 6）')
  assert.deepEqual(merged.map((m) => m.id), ['tp_tc-1', 'tp_tc-2', 'tp_tc-3'])
  assert.deepEqual(merged.map((m) => m.tool_call_id), ids)
})

test('本地独有的 tool_pair（历史未落库）按到达顺序追加在后', () => {
  const hist = [pair('c1', 'tool', 'p1')]
  const locals = [pair('c1', 'assistant', ''), pair('c2', 'assistant', '')]
  const merged = mergeHistoryWithLocals(hist, locals)
  assert.deepEqual(merged.map((m) => m.id), ['tp_c1', 'tp_c2'])
})

test('非工具消息：同 id 或同 role+type+content 不重复；新增流式文本保留', () => {
  const hist = [
    { id: 'u1', role: 'user', type: 'text', content: 'hello' },
    { id: 'm1', role: 'assistant', type: 'text', content: 'answer' },
  ]
  const locals = [
    { id: 'u1', role: 'user', type: 'text', content: 'hello' }, // 同 id → 丢弃
    { id: 'local-2', role: 'assistant', type: 'text', content: 'answer' }, // 同内容同角色 → 丢弃
    { id: 'local-3', role: 'assistant', type: 'text', content: 'streaming...' }, // 独有 → 保留
  ]
  const merged = mergeHistoryWithLocals(hist, locals)
  assert.deepEqual(merged.map((m) => m.id), ['u1', 'm1', 'local-3'])
})

test('前插更早历史（loadMore）：与现有消息同 id / 同 tool_call_id 的旧份被剔除', () => {
  const existing = [pair('c3', 'assistant', ''), { id: 'm9', role: 'assistant', type: 'text', content: 'x' }]
  const older = [
    pair('c1', 'tool', 'p1'),
    pair('c2', 'tool', 'p2'),
    pair('c3', 'tool', 'p3'), // 与新会话本地卡同 tool_call_id → 丢弃
    { id: 'm9', role: 'assistant', type: 'text', content: 'x' }, // 同 id → 丢弃
  ]
  const add = withoutDuplicates(existing, older)
  assert.deepEqual(add.map((m) => m.id), ['tp_c1', 'tp_c2'])
})