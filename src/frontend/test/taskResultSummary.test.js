/**
 * 任务详情「最终结果（result_summary）」展示单测。
 *
 * 后端 `tasks.done` / TaskInfo 带 `result_summary`（终态结果全文），`useTaskView.upsert`
 * 合并保留；详情面板需在 output 之外展示最终结果（两者相同只显示一次）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../src/events/event-names.js'
import { useTaskView } from '../src/composables/useTaskView.js'
import { taskResultSections } from '../src/utils/taskResult.js'
import { readSrc } from './support/feSource.mjs'

test('分区：仅 result_summary / 仅 output / 两者不同 / 两者相同', () => {
  assert.deepEqual(taskResultSections({ result_summary: 'final' }), {
    finalResult: 'final', partialOutput: '',
  })
  assert.deepEqual(taskResultSections({ output: 'stream' }), {
    finalResult: '', partialOutput: 'stream',
  })
  assert.deepEqual(taskResultSections({ result_summary: 'final', output: 'stream' }), {
    finalResult: 'final', partialOutput: 'stream',
  })
  // 相同内容（忽略首尾空白）只展示一次（优先最终结果）
  assert.deepEqual(taskResultSections({ result_summary: 'same\n', output: 'same' }), {
    finalResult: 'same\n', partialOutput: '',
  })
  // 空 / 非字符串安全
  assert.deepEqual(taskResultSections({ result_summary: '  ', output: null }), {
    finalResult: '', partialOutput: '',
  })
})

test('useTaskView：tasks.done 合并保留 result_summary 与 output', () => {
  const tv = useTaskView() // 注册 tasks.* 订阅
  mq.emitRemote({
    type: EventNames.taskEnded,
    payload: JSON.stringify({
      task_id: 't-result-summary',
      state: 'done',
      result_summary: '最终结果全文',
      output: '部分输出',
    }),
  })
  const t = tv.tasks.get('t-result-summary')
  assert.ok(t, 'tasks.done 须 upsert 任务快照')
  assert.equal(t.result_summary, '最终结果全文', 'normalize/upsert 须保留 result_summary')
  assert.equal(t.output, '部分输出', 'output 仍保留（作为部分输出）')
  assert.equal(t.status, 'done', 'state→status 映射不变')
})

test('TaskDetailView：最终结果区块 + i18n（双语齐备，不硬编码中文）', () => {
  const vue = readSrc('components/task/TaskDetailView.vue')
  assert.match(vue, /taskResultSections/, '详情须使用 result_summary 分区判定')
  assert.match(vue, /taskView\.final_result/, '最终结果标题须走 i18n')
  assert.match(vue, /taskView\.partial_output/, '部分输出标题须走 i18n')
  assert.doesNotMatch(vue, />\s*最终结果\s*</, '模板不得硬编码中文')
  for (const loc of ['zh-CN', 'en-US']) {
    const json = JSON.parse(readSrc(`locales/${loc}/taskView.json`))
    for (const k of ['final_result', 'partial_output']) {
      assert.ok(typeof json[k] === 'string' && json[k].trim(), `${loc} taskView 缺 ${k}`)
    }
  }
})