/**
 * 工具行「停止」接线守卫（2026-09-18；2026-11-15 扩到所有在飞工具）——
 * 「点击停止 → 发出 task-stop 且 payload 正确」的等价断言。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 用**源码守卫**
 * 锁定接线契约，防回归到已移除的 gateway 方法面 `mcp-tasks-cancel`（该主题已删 → 总线上无订阅方、
 * 静默丢弃 = 操作点了没反应，正是本次要修的断点）。覆盖：
 *   ① 事件名：event-names.js `taskStop = 'task-stop'`，不再声明 `mcp-tasks-cancel`；
 *   ② 停止动作：MessageItem.vue `stopTool` 发 EventNames.taskStop；
 *      载荷优先 `{task_id}`（server 任务节点 id），无则回退 `{tool_call_id}`；
 *   ③ 显示条件：`canCancel` 覆盖所有 pending/running 工具；
 *   ④ i18n：停止失败提示 `chat.tool_stop_failed` 双语存在且含 {error} 占位。
 *
 * 注：本文件放在 src 之外（frontend/test/），以免 node 内置模块导入进入 tsconfig 对 src 下
 * js/vue 的类型检查范围。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { readSrc } from './support/feSource.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readSrc(rel)

test('event-names：taskStop = task-stop，且不再声明已移除的 mcp-tasks-cancel', () => {
  const names = read('events/event-names.js')
  assert.match(names, /taskStop:\s*'task-stop'/, 'taskStop 必须映射 task-stop')
  assert.doesNotMatch(names, /mcp-tasks-cancel/, 'event-names 不得再声明 mcp-tasks-cancel（方法面已移除）')
})

test('MessageItem：停止动作发 task-stop（tool_call_id 优先 / task_id 回退）', () => {
  const vue = read('views/chat/MessageItem.vue')
  const m = vue.match(/async function stopTool\s*\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(m, '未找到 stopTool')
  assert.match(
    m[0],
    /const payload = callId \? \{ tool_call_id: callId \} : \{ task_id: taskId \}/,
    '停止载荷须 tool_call_id 优先、task_id 回退'
  )
  assert.match(m[0], /mq\.emit\(\s*EventNames\.taskStop\s*,\s*payload\s*\)/, '停止须发 EventNames.taskStop')
  assert.doesNotMatch(vue, /EventNames\.tasksCancel/, 'MessageItem 不得再引用已移除的 tasksCancel')
  assert.doesNotMatch(vue, /mcp-tasks-cancel/, 'MessageItem 不得再直发已移除的 mcp-tasks-cancel')
})

test('MessageItem：停止按钮覆盖所有在飞工具（canCancel）', () => {
  const vue = read('views/chat/MessageItem.vue')
  assert.match(vue, /v-if="canCancel"/, '停止按钮须以 canCancel 为判据')
  const m = vue.match(/const canCancel = computed\(\(\) => \{[\s\S]*?\n\}\)/)
  assert.ok(m, '未找到 canCancel')
  assert.match(m[0], /s === 'pending' \|\| s === 'running'/, 'canCancel 须覆盖 pending/running')
  assert.match(m[0], /props\.message\.task_id \|\| props\.message\.tool_call_id/, 'canCancel 须要求可定位 id')
})

test('i18n：停止失败提示 tool_stop_failed 双语存在且含 {error}', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const j = JSON.parse(read('locales/' + loc + '/chat.json'))
    const v = j.tool_stop_failed
    assert.ok(typeof v === 'string' && v.length > 0, `${loc} 缺 chat.tool_stop_failed`)
    assert.ok(v.includes('{error}'), `${loc} tool_stop_failed 应含 {error} 占位`)
  }
})
