/**
 * DSL-3 收口：实时事件（`tasks.*` upsert）也要携带 tasktree 新字段（2026-10-07）。
 *
 * 背景：`data-tasktree-list` 结果含 `loop_current` / `loop_total` / `steps` / `shadow` /
 * `return_*`，但实时事件路径此前只映射固定字段 → 运行中看不到容器进度 / 步骤记录 / `$RETURN`。
 * 现 `nodeFromEvent` 经 `utils/dslView.js` 的 `copyDslFields`（清单 = `DSL_FIELDS`）原样透传。
 *
 * 覆盖：① `DSL_FIELDS` 清单齐备；② `copyDslFields` 语义（含则带 / 缺省不写 / false 与空串照带 /
 * 不改入参）；③ `useTaskView.nodeFromEvent` 接线（调用 copyDslFields）。
 * 注：`useTaskView.js` 内含无扩展名相对导入（Vite 风格），无法在 node:test 直跑 → 接线走源码守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { DSL_FIELDS, copyDslFields } from '../src/utils/dslView.js'

const here = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(here, '..', 'src', rel), 'utf8')

const EXPECTED_FIELDS = [
  'loop_current', 'loop_total', 'steps', 'shadow',
  'return_kind', 'return_inline', 'return_file', 'return_size',
]

test('DSL_FIELDS：透传清单齐备（容器进度 / steps / shadow / $RETURN 两态）', () => {
  assert.deepEqual([...DSL_FIELDS].sort(), [...EXPECTED_FIELDS].sort())
})

test('copyDslFields：来源含则原样拷入（含 false / 空串 / 数组引用）', () => {
  const steps = [{ no: 1, status: 'done', session_id: 's1' }]
  const src = {
    task_id: 'job1', // 非 DSL 字段：不拷
    loop_current: 3,
    loop_total: 10000,
    steps,
    shadow: false,
    return_kind: 'inline',
    return_inline: '',
    return_file: 'out.md',
    return_size: 70000,
  }
  const dst = { node_id: 'job1' }
  const out = copyDslFields(src, dst)
  assert.equal(out, dst, '返回同一引用（就地写入）')
  for (const f of EXPECTED_FIELDS) assert.ok(f in dst, `应拷入 ${f}`)
  assert.equal(dst.loop_current, 3)
  assert.equal(dst.steps, steps, '步骤数组原样引用（不深拷贝）')
  assert.equal(dst.shadow, false, 'false 亦须带（非缺省）')
  assert.equal(dst.return_inline, '', '空串亦须带')
  assert.equal(dst.task_id, undefined, '非 DSL 字段不拷')
  assert.equal(src.task_id, 'job1', '不改入参')
})

test('copyDslFields：缺省字段不写入 → 增量合并不误清既有字段', () => {
  const dst = { node_id: 'job1', loop_current: 2, loop_total: 5, steps: [{ no: 7 }] }
  copyDslFields({ status: 'done' }, dst) // 后续事件不含扩展字段
  assert.deepEqual(dst, { node_id: 'job1', loop_current: 2, loop_total: 5, steps: [{ no: 7 }] })
})

test('copyDslFields：来源非对象 / 缺目标 → 容错（不抛错，原样返回）', () => {
  assert.deepEqual(copyDslFields(null, {}), {})
  assert.deepEqual(copyDslFields(undefined, {}), {})
  assert.deepEqual(copyDslFields('not-object', {}), {})
  assert.equal(copyDslFields({ loop_current: 1 }, null), null)
  assert.equal(copyDslFields({ loop_current: 1 }, undefined), undefined)
})

test('接线守卫：nodeFromEvent 经 copyDslFields 透传 DSL 扩展字段', () => {
  const src = read('composables/useTaskView.js')
  assert.match(src, /import\s*\{\s*copyDslFields\s*\}\s*from\s*'\.\.\/utils\/dslView'/,
    'useTaskView 须引入 copyDslFields')
  const fn = src.match(/function nodeFromEvent\s*\(p\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 nodeFromEvent')
  assert.match(fn[0], /copyDslFields\(p,\s*node\)/, 'nodeFromEvent 须对事件 payload 透传 DSL 字段')
})
