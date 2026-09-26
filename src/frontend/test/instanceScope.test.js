/**
 * 「事件按 instance 过滤」单测（2026-09-19 实例隔离第二批，缺口 8）：
 * 给定两 instance 的事件流 → 只有本 instance 的事件被前端消费；无 instance 归属的事件
 * （data-\* 与 filesys.\* 等按 workdir 管理）与本实例未标识 → 放行（单 instance 行为不变）。
 *
 * 注：本文件放在 src 之外（frontend/test/），`npm test` = node:test 直跑（见 package.json）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { eventBelongsToInstance } from '../src/utils/instanceScope.js'

test('缺口 8：他 instance 的 tasks.* / mcp-* 事件不属本实例，本实例的与无归属的放行', () => {
  const mine = 'ins-a'
  // 本实例：consumed
  assert.equal(eventBelongsToInstance({ instance_id: 'ins-a', task_id: 'task-aaa' }, mine), true)
  // 他实例：dropped
  assert.equal(eventBelongsToInstance({ instance_id: 'ins-b', task_id: 'task-bbb' }, mine), false)
  // 无 instance 归属（data-*/filesys.* / server 级广播）：放行（旧行为）
  assert.equal(eventBelongsToInstance({ path: '/x/y' }, mine), true)
  assert.equal(eventBelongsToInstance(null, mine), true)
  assert.equal(eventBelongsToInstance(undefined, mine), true)
})

test('缺口 8：本实例未标识（无注入）→ 过滤条件恒真（单 instance 形态等价）', () => {
  assert.equal(eventBelongsToInstance({ instance_id: 'ins-a' }, ''), true)
  assert.equal(eventBelongsToInstance({ instance_id: 'ins-b' }, ''), true)
})

test('缺口 8 接线守卫：useTaskView 的 tasks.* / llm-complete 入口调用 instance 过滤', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const src = readFileSync(join(here, '..', 'src', 'composables', 'useTaskView.js'), 'utf8')
  assert.match(src, /import\s*\{\s*eventBelongsToInstance\s*\}\s*from\s*'\.\.\/utils\/instanceScope'/,
    'useTaskView 必须引入 instance 过滤')
  const onTask = src.match(/function onTaskEvent\s*\(payload\)\s*\{[\s\S]*?\n\}/)
  assert.ok(onTask, '未找到 onTaskEvent')
  assert.match(onTask[0], /eventBelongsToInstance\(payload\)/, 'onTaskEvent 必须按 instance 过滤')
  const onSub = src.match(/function onSubLlmComplete\s*\(d\)\s*\{[\s\S]*?\n\}/)
  assert.ok(onSub, '未找到 onSubLlmComplete')
  assert.match(onSub[0], /eventBelongsToInstance\(d\)/, 'onSubLlmComplete 必须按 instance 过滤')
})
