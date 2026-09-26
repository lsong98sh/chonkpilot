/**
 * useTaskStatus 单测（I-94）——纯映射逻辑，不依赖 Vue / 组件实例（可用 `npm test` 直接跑）。
 *
 * 覆盖：
 *   ① 既有 4 态（running/error/done/stopped）图标/配色/动画逐值不变；
 *   ② detached / awaiting 由层权威 `state` 细化（list 载荷 `status` 已塌缩为 running）；
 *   ③ 8 态均有图标且 tooltip i18n key 在 zh-CN / en-US 双语均存在且非空；
 *   ④ 未知 / 空闲态不渲染图标；
 *   ⑤ 非 running 的 status 不被陈旧 state 覆盖（避免事件增量合并误判）。
 *
 * 注：本文件放在 src 之外（frontend/test/），以免 node 内置模块导入进入 tsconfig 对
 * src 下 js/vue 的类型检查范围。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { TASK_STATE_META, resolveTaskState, stateMetaFor } from '../src/composables/useTaskStatus.js'

const here = dirname(fileURLToPath(import.meta.url))
const localesDir = join(here, '..', 'src', 'locales')
function loadLocale(locale) {
  return JSON.parse(readFileSync(join(localesDir, locale, 'taskView.json'), 'utf8'))
}
const zh = loadLocale('zh-CN')
const en = loadLocale('en-US')

// 后端（chonkpilot-task/task.go）认可的 8 态
const ALL_STATES = ['pending', 'running', 'awaiting', 'detached', 'done', 'error', 'cancelled', 'interrupted']

test('既有 4 态：图标 / 配色 / 动画逐值不变', () => {
  assert.deepEqual(
    { ...TASK_STATE_META.running },
    { icon: 'loading', color: 'var(--accent)', spin: true, pulse: false, label: 'taskView.state_running' }
  )
  assert.deepEqual(
    { ...TASK_STATE_META.error },
    { icon: 'circle-close-filled', color: 'var(--danger)', spin: false, pulse: false, label: 'taskView.state_error' }
  )
  assert.deepEqual(
    { ...TASK_STATE_META.done },
    { icon: 'circle-check-filled', color: '#67c23a', spin: false, pulse: false, label: 'taskView.state_done' }
  )
  assert.deepEqual(
    { ...TASK_STATE_META.stopped },
    { icon: 'circle-close-filled', color: '#bfbfbf', spin: false, pulse: false, label: 'taskView.state_stopped' }
  )
})

test('detached / awaiting：status 塌缩为 running 时回读层权威 state', () => {
  // list 载荷形态：status=frontVisibleStatus(state)（detached/awaiting → running），state=细态
  assert.equal(resolveTaskState({ status: 'running', state: 'detached' }), 'detached')
  assert.equal(resolveTaskState({ status: 'running', state: 'awaiting' }), 'awaiting')

  const detached = stateMetaFor(resolveTaskState({ status: 'running', state: 'detached' }))
  assert.equal(detached.icon, 'arrow-right')
  assert.equal(detached.pulse, false)

  const awaiting = stateMetaFor(resolveTaskState({ status: 'running', state: 'awaiting' }))
  assert.equal(awaiting.icon, 'warning-filled')
  assert.equal(awaiting.color, 'var(--warning)')
  assert.equal(awaiting.pulse, true, 'awaiting 应有脉冲提示')

  // 事件增量节点形态：status 直接是细态
  assert.equal(resolveTaskState({ status: 'detached' }), 'detached')
  assert.equal(resolveTaskState({ status: 'awaiting' }), 'awaiting')
})

test('8 态均有图标，且 tooltip 文案 zh-CN / en-US 双语存在且非空', () => {
  for (const state of ALL_STATES) {
    const meta = stateMetaFor(state)
    assert.ok(meta.icon, `${state} 应有图标`)
    assert.ok(meta.label, `${state} 应有 tooltip(label)`)
    const key = meta.label.replace(/^taskView\./, '')
    assert.ok(typeof zh[key] === 'string' && zh[key].length > 0, `${state} 缺 zh-CN 文案 ${meta.label}`)
    assert.ok(typeof en[key] === 'string' && en[key].length > 0, `${state} 缺 en-US 文案 ${meta.label}`)
  }
})

test('真实取消态 cancelled 有图标（旧链路的 stopped 为历史死值）', () => {
  const meta = stateMetaFor('cancelled')
  assert.equal(meta.icon, 'circle-close-filled')
  assert.equal(meta.label, 'taskView.state_cancelled')
})

test('未知 / 空闲态不渲染图标', () => {
  assert.equal(stateMetaFor('idle').icon, '')
  assert.equal(stateMetaFor('weird').icon, '')
  assert.equal(resolveTaskState({}), 'idle')
  assert.equal(resolveTaskState(undefined), 'idle')
})

test('非 running 的 status 不被陈旧 state 覆盖', () => {
  // 事件合并可能留下旧的 state，但 status 已是终态 → 以 status 为准
  assert.equal(resolveTaskState({ status: 'done', state: 'detached' }), 'done')
  assert.equal(resolveTaskState({ status: 'cancelled', state: 'awaiting' }), 'cancelled')
  assert.equal(resolveTaskState({ status: 'interrupted', state: 'awaiting' }), 'interrupted')
  assert.equal(resolveTaskState({ status: 'pending', state: 'running' }), 'pending')
})
