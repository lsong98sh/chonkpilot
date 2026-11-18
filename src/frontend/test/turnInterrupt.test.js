/**
 * 「停止」乐观置态 → 后端 llm-complete 幂等收敛（第 1 条，前端部分）。
 *
 * 纯状态机 utils/turnInterrupt.js 的单测 + SFC 接线守卫（node:test 直跑，无 DOM）：
 *   - 点击停止：首次置位，重复点击幂等；
 *   - 真实终态：同一 turn 只收敛一次（重复 llm-complete 不重复收尾），并清空本地标志；
 *   - 普通（未点停止）终态路径不变：首次收敛，重复终态幂等；
 *   - 不同 turn / 新一轮复位后可再次停止；
 *   - MessageList.onMessageCancel 顺序 = 先本地置态、再发 llm-cancel；
 *   - SessionChat 停止按钮乐观置态（stopping）与 5s 兜底复位。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  createTurnInterruptState,
  markInterruptedLocally,
  convergeOnComplete,
  resetTurnInterruptState,
} from '../src/utils/turnInterrupt.js'
import { readSrc } from './support/feSource.mjs'

function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('乐观停止：首次置位 true，重复点击 false（幂等）', () => {
  const st = createTurnInterruptState()
  assert.equal(st.local, false)
  assert.equal(markInterruptedLocally(st), true, '首次点击应置位')
  assert.equal(st.local, true)
  assert.equal(markInterruptedLocally(st), false, '重复点击不得重复置位/收尾')
  assert.equal(st.local, true, '状态保持已停止')
})

test('乐观置态 → 真实 complete：同 turn 首次收敛、重复终态幂等、清空本地标志', () => {
  const st = createTurnInterruptState()
  markInterruptedLocally(st)

  assert.equal(convergeOnComplete(st, 't1'), true, '真实终态仍按既有路径收敛一次')
  assert.equal(st.local, false, '收到真实终态后清空本地乐观标志')

  assert.equal(convergeOnComplete(st, 't1'), false, '同 turn 重复 llm-complete 不得重复收尾')
  assert.equal(st.local, false)
  assert.equal(convergeOnComplete(st, 't1'), false, '再次重复仍幂等')
})

test('普通完成/错误路径：未点停止同样只收敛一次（行为不变）', () => {
  const st = createTurnInterruptState()
  assert.equal(convergeOnComplete(st, 't2'), true, '普通终态首次收敛')
  assert.equal(convergeOnComplete(st, 't2'), false, '重复终态幂等（不重复 drain/回写队列）')
})

test('不同 turn 各自收敛（不会被上一轮终态吞掉）', () => {
  const st = createTurnInterruptState()
  assert.equal(convergeOnComplete(st, 't1'), true)
  assert.equal(convergeOnComplete(st, 't2'), true, '新 turn 的终态必须正常收敛')
  assert.equal(convergeOnComplete(st, 't2'), false)
})

test('新一轮/切换会话复位后可再次乐观停止并收敛', () => {
  const st = createTurnInterruptState()
  markInterruptedLocally(st)
  convergeOnComplete(st, 't1')

  resetTurnInterruptState(st)
  assert.deepEqual(st, { local: false, convergedTurn: null }, '复位清空本地与收敛标记')
  assert.equal(markInterruptedLocally(st), true, '新一轮可再次乐观停止')
  assert.equal(convergeOnComplete(st, 't2'), true)
})

test('MessageList：onMessageCancel 先本地置态、再发 llm-cancel；onLlmComplete 幂等收敛', () => {
  const vue = readSrc('views/chat/MessageList.vue')
  const cancel = fnBody(vue, 'onMessageCancel')
  assert.ok(cancel, '未找到 onMessageCancel')
  const localIdx = cancel.indexOf('finishTurnLocallyInterrupted()')
  const cancelIdx = cancel.indexOf('confirmTurnCancel(')
  assert.ok(localIdx >= 0, '点击停止须先执行本地乐观收尾')
  assert.ok(cancelIdx >= 0, '仍须发送 llm-cancel')
  assert.ok(localIdx < cancelIdx, '顺序必须：先本地置态，再 confirmTurnCancel')

  const finish = fnBody(vue, 'finishTurnLocallyInterrupted')
  assert.ok(finish, '未找到 finishTurnLocallyInterrupted')
  assert.match(finish, /clearPendingTimers\(\)/, '须清理 pending 进度定时器')
  assert.match(finish, /clearInboxStartTimer\(\)/, '须清理 inbox 起轮定时器')
  assert.match(finish, /handleDone\(\)/, '须收起加载占位/结束本地流式段')
  assert.doesNotMatch(finish, /mq\.emit|messages\.value\.(push|splice)|publish/, '不得伪造消息/发 MQ/写库')

  const complete = fnBody(vue, 'onLlmComplete')
  assert.ok(complete, '未找到 onLlmComplete')
  assert.match(complete, /convergeOnComplete\(/, '终态须经幂等状态机收敛')
  assert.match(complete, /if \(!firstTerminal\) return/, '重复终态须提前返回，不重复收尾')

  assert.doesNotMatch(vue, /autoContinueTimer/, '已修复未声明变量引用（onUnmounted 不再 ReferenceError）')
})

test('SessionChat：停止按钮乐观置态 + 5s 兜底复位（无 watch）', () => {
  const vue = readSrc('views/tasks/SessionChat.vue')
  assert.match(vue, /:disabled="stopping"/, '停止中须禁用按钮')
  assert.match(vue, /\$t\('taskView\.stopping_session'\)/, '停止中文案走 i18n')
  assert.doesNotMatch(vue, /\bwatch\(/, '不得引入 watch')

  const fn = fnBody(vue, 'stopSession')
  assert.ok(fn, '未找到 stopSession')
  assert.match(fn, /stopping\.value = true/, '点击立即置 stopping')
  assert.match(fn, /setTimeout\(resetStopping, 5000\)/, '须有 5s 兜底复位')
  assert.match(fn, /confirmTurnCancel\(\{ sessionId: sessionId\.value, turnId: t\.turn_id \|\| '' \}\)/,
    '仍走现有 llm-cancel{session,turn}')

  for (const loc of ['zh-CN', 'en-US']) {
    const json = JSON.parse(readSrc(`locales/${loc}/taskView.json`))
    assert.ok(typeof json.stopping_session === 'string' && json.stopping_session.trim(),
      `${loc} 缺 taskView.stopping_session`)
  }
})