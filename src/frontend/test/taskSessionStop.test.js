/**
 * TASK-008：子会话详情头部「终止会话」入口（2026-11-05）。
 *
 * 口径（用户拍板 A）：
 *   - 仅任务派生（`tasks.*` 有运行态快照）的子会话显示；普通聊天子会话无快照不显示；
 *   - 点击发**现有** `llm-cancel{session,turn}`（经 `useTaskView.confirmTurnCancel`），
 *     零新增 MQ 主题；当前轮 interrupted，在飞工具由 server 级联取消；
 *   - `data-tasktree-tasks` 快照行无 `turn_id`（冷启动）→ 回落 llm 节点并以空 turn 发送，
 *     server `onLLMCancel` 按 session 解析运行中轮次；
 *   - 文案走 i18n（中英齐备），不硬编码。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 源码 / 文案守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../src/events/event-names.js'
import { useTaskView } from '../src/composables/useTaskView.js'
import { readRepo, readSrc } from './support/feSource.mjs'

const read = (rel) => readSrc(rel)
const LOCALES = ['zh-CN', 'en-US']

/** 取函数体（`function name(...) {` 到首个顶格 `}`） */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('SessionChat：头部按钮可见性 = 运行中轮次，文案走 i18n', () => {
  const vue = read('views/tasks/SessionChat.vue')
  assert.match(vue, /import \{[^}]*\bButton\b[^}]*\} from '@ckfw\/components\/ui\/index\.js'/,
    '须从平台基座 UI 导入 Button')

  const btn = vue.match(/<Button[^>]*stopSession[\s\S]*?<\/Button>/)
  assert.ok(btn, '未找到「终止会话」按钮')
  assert.match(btn[0], /v-if="runningTurn"/, '仅运行中轮次存在时可见')
  assert.match(btn[0], /@click="stopSession"/, '点击须触发 stopSession')
  assert.match(btn[0], /\$t\('taskView\.stop_session'\)/, '文案须走 i18n')
  assert.doesNotMatch(btn[0], /终止会话/, '按钮不得硬编码中文')
})

test('SessionChat：runningTurn 由 tasks 运行态快照派生（turn_id 优先，缺失回落 llm 节点）', () => {
  const vue = read('views/tasks/SessionChat.vue')
  assert.match(vue, /const \{ refresh: refreshTasks, activeTasks, confirmTurnCancel \} = useTaskView\(\)/,
    '须从 useTaskView 取 activeTasks / confirmTurnCancel')
  assert.match(vue, /const runningTurn = computed\(\(\) => \{/, '须定义 runningTurn computed')
  const body = (vue.match(/const runningTurn = computed\(\(\) => \{([\s\S]*?)\n\}\)/) || [])[1]
  assert.ok(body, '未找到 runningTurn 实现')
  assert.match(body, /activeTasks\(sessionId\.value\)/, '须按当前子会话过滤运行态任务')
  assert.match(body, /\.find\(\(t\) => !!t\.turn_id\)/, '须优先挑选携带 turn_id 的运行中任务')
  // data-tasktree-tasks 快照行无 turn_id（冷启动）→ 回落 llm 节点，由 server 按 session 解析
  assert.match(body, /\.find\(\(t\) => \(t\.kind \|\| t\.type\) === 'llm'\)/, '须回落任务派生的 llm 节点')
  assert.doesNotMatch(vue, /\bwatch\(/, '不得新增 props/store watch（项目硬规则）')
})

test('SessionChat：终止走现有 llm-cancel{session,turn}，不新增 MQ 主题', () => {
  const vue = read('views/tasks/SessionChat.vue')
  const fn = fnBody(vue, 'stopSession')
  assert.ok(fn, '未找到 stopSession')
  assert.match(fn, /confirmTurnCancel\(\{ sessionId: sessionId\.value, turnId: t\.turn_id \|\| '' \}\)/,
    '须调用 confirmTurnCancel({sessionId, turnId})（turn 缺失由 server 按 session 解析）')
  // 零新增主题：不得直接发 llm-cancel 字面量，也不得直接 mq.emit
  assert.doesNotMatch(vue, /['"]llm-cancel['"]/, '不得新增 llm-cancel 主题字面量')
  assert.doesNotMatch(vue, /mq\.emit\(/, '本入口不得自行发消息（复用 useTaskView）')
})

test('数据面：无 turn_id 的 llm 运行态节点仍可见（冷启动回落前提）', () => {
  const tv = useTaskView()
  const sid = 's-stop-fallback'
  mq.emitRemote({
    type: EventNames.taskStarted,
    payload: JSON.stringify({
      task_id: 't-stop-fallback', kind: 'llm', session_id: sid, state: 'running',
    }),
  })
  const running = tv.activeTasks(sid)
  assert.equal(running.length, 1, 'llm 运行态节点须进入 activeTasks')
  assert.equal(running[0].turn_id, undefined, '该用例模拟快照行（无 turn_id）')
})

test('i18n：taskView.stop_session 中英齐备', () => {
  for (const loc of LOCALES) {
    const json = JSON.parse(read(`locales/${loc}/taskView.json`))
    assert.ok(typeof json.stop_session === 'string' && json.stop_session.trim().length > 0,
      `${loc} 缺 taskView.stop_session`)
  }
})

test('文档：TASK-008 收口为 ✅ 且明细描述「终止会话」', () => {
  const doc = readRepo('docs/spec/30-function-points/34-任务.md')
  const row = doc.match(/^\|\s*TASK-008\s*\|.*$/m)
  assert.ok(row, '未找到 TASK-008 总表行')
  assert.match(row[0], /✅/, 'TASK-008 总表状态须为 ✅')
  assert.match(row[0], /终止会话/, 'TASK-008 描述须含「终止会话」')

  const detail = doc.match(/^\|\s*TASK-008-S01\s*\|.*$/m)
  assert.ok(detail, '未找到 TASK-008-S01 明细行')
  assert.match(detail[0], /点「终止会话」/, '明细步骤须为点「终止会话」')
  assert.match(detail[0], /interrupted/, '明细预期须含当前轮 interrupted')
  assert.match(detail[0], /级联取消/, '明细预期须含在飞工具级联取消')
})