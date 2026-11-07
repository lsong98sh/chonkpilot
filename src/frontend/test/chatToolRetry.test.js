/**
 * 工具重试 / 轮次继续 候选判定单测（任务层语义分工）。
 *
 * 口径：
 *   - **工具重试**只接受 interrupted（abort/进程重启遗留）；cancelled 是任务层不可逆终态，
 *     不能走 tool-retry（后端只会 tool not found）。
 *   - cancelled / interrupted 的轮次走**轮次级「继续」**（isUnfinishedTurn → 显示继续）。
 *   - 通知轮（后端自动发起）既不显示「继续」也不提供工具重试；pair 自带 notify / notify_turn
 *     标记同样排除。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  isRetryableToolPair,
  findRetryableToolPair,
  isUnfinishedTurn,
  isNotifyTurn,
  detectNotifyTurn,
} from '../src/utils/chatRetry.js'
import { readSrc } from './support/feSource.mjs'

const pair = (status, extra = {}) => ({ type: 'tool_pair', status, tool_call_id: 'c1', ...extra })

test('cancelled 的 tool_pair 不可工具重试（走轮次级继续）', () => {
  assert.equal(isRetryableToolPair(pair('cancelled')), false)
  assert.equal(findRetryableToolPair([pair('done'), pair('cancelled')]), null)
})

test('interrupted 仍可工具重试；其他终态 / 显式不可重试不提供重试', () => {
  assert.equal(isRetryableToolPair(pair('interrupted')), true)
  assert.equal(isRetryableToolPair(pair('done')), false)
  assert.equal(isRetryableToolPair(pair('failed')), false)
  assert.equal(isRetryableToolPair(pair('cancelled', { retryable: false })), false)
  assert.equal(isRetryableToolPair(pair('interrupted', { retryable: false })), false)
  assert.equal(isRetryableToolPair(pair('interrupted', { _meta: { retryable: false } })), false)
})

test('cancelled / interrupted 的轮次判为未完成 → 显示「继续」', () => {
  assert.equal(isUnfinishedTurn({ status: 'cancelled' }), true)
  assert.equal(isUnfinishedTurn({ finish_reason: 'cancelled' }), true)
  assert.equal(isUnfinishedTurn({ status: 'interrupted' }), true)
  assert.equal(isUnfinishedTurn({ finish_reason: 'interrupted' }), true)
  // 既有未完成 finish_reason
  assert.equal(isUnfinishedTurn({ finish_reason: 'length' }), true)
  assert.equal(isUnfinishedTurn({ finish_reason: 'insufficient_system_resource' }), true)
  // 正常完成 / 无 turn → 不显示继续
  assert.equal(isUnfinishedTurn({ status: 'complete', finish_reason: 'stop' }), false)
  assert.equal(isUnfinishedTurn(null), false)
})

test('pair 带 notify / notify_turn 标记（顶层或 _meta）不提供重试', () => {
  assert.equal(isRetryableToolPair(pair('interrupted', { notify: true })), false)
  assert.equal(isRetryableToolPair(pair('interrupted', { notify_turn: true })), false)
  assert.equal(isRetryableToolPair(pair('interrupted', { _meta: { notify: true } })), false)
  assert.equal(isRetryableToolPair(pair('interrupted', { _meta: { notify_turn: true } })), false)
})

test('通知轮整体不提供重试（即使存在可重试 pair）', () => {
  const list = [pair('interrupted'), pair('interrupted')]
  assert.equal(findRetryableToolPair(list, true), null)
  assert.equal(findRetryableToolPair(list, false)?.status, 'interrupted')
})

test('通知轮判定：user-notify 气泡 / 历史 kind=notify；普通用户文本不算', () => {
  assert.equal(isNotifyTurn([{ role: 'user', type: 'text' }]), false)
  assert.equal(isNotifyTurn([{ role: 'user', type: 'user-notify' }]), true)
  assert.equal(isNotifyTurn([
    { role: 'user', type: 'text' },
    { role: 'assistant', type: 'text' },
    { role: 'user', type: 'user-notify' },
    { role: 'assistant', type: 'text' },
  ]), true)
  // 历史行（kind=notify）经历史恢复后仍可判定
  assert.equal(isNotifyTurn([{ role: 'user', kind: 'notify', type: 'text' }]), true)
  // 通知后有新的用户消息 → 不再是通知轮
  assert.equal(isNotifyTurn([
    { role: 'user', type: 'user-notify' },
    { role: 'user', type: 'text' },
  ]), false)
})

test('事件显式 notify / notify_turn 优先于消息形态', () => {
  const msgs = [{ role: 'user', type: 'user-notify' }]
  assert.equal(detectNotifyTurn({ notify: false }, msgs), false)
  assert.equal(detectNotifyTurn({ notify_turn: false }, msgs), false)
  assert.equal(detectNotifyTurn({ notify: true }, [{ role: 'user', type: 'text' }]), true)
  assert.equal(detectNotifyTurn({}, msgs), true)
})

test('接线守卫：工具重试仅 interrupted；cancelled 轮走继续；通知轮两者皆隐藏', () => {
  const retrySrc = readSrc('utils/chatRetry.js')
  assert.match(retrySrc, /const RETRYABLE_STATUSES = \['interrupted'\]/,
    '工具重试候选只允许 interrupted（cancelled 在任务层不可逆）')

  const src = readSrc('views/chat/MessageList.vue')
  assert.match(src, /import \{[^}]*findRetryableToolPair[^}]*\} from '\.\.\/\.\.\/utils\/chatRetry'/,
    'MessageList 须引入工具重试候选判定')
  assert.match(src, /const lastInterruptedPair = computed\(\(\) => findRetryableToolPair\(messages\.value, notifyTurn\.value\)\)/,
    '重试目标须同时受 notifyTurn 门控')
  // lastTurnIncomplete：最后 turn 未完成（含 cancelled/interrupted）→ 继续
  const lastTurn = src.match(/function lastTurnIncomplete\(msgs\)\s*\{[\s\S]*?\n\}/)
  assert.ok(lastTurn, '未找到 lastTurnIncomplete')
  assert.match(lastTurn[0], /isUnfinishedTurn\(lastTurn\)/,
    'cancelled / interrupted turn 须判为未完成（显示继续）')
  // assessContinueState：通知轮不显示「继续」
  const assess = src.match(/function assessContinueState\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(assess, '未找到 assessContinueState')
  assert.match(assess[0], /if \(notifyTurn\.value\) \{[\s\S]*?showContinue\.value = false[\s\S]*?return/,
    '通知轮须直接收起「继续」')
  // 载荷语义不变：tool-retry {session, turn}
  const click = src.match(/function onRetryToolClick\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(click, '未找到 onRetryToolClick')
  assert.match(click[0], /session: sid,\s*\n\s*turn:/, 'tool-retry 载荷须保持 {session, turn}')
  // ChatPanel 仍以 lastInterruptedPair 作为按钮显示条件（null = 不提供）
  const panel = readSrc('views/chat/ChatPanel.vue')
  assert.match(panel, /v-if="messageListRef\?\.lastInterruptedPair"/, 'ChatPanel 重试按钮仍读 lastInterruptedPair')
})