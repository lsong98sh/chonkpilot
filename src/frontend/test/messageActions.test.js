/**
 * 长会话性能（评审第 11 条）· 消息动作总线。
 *
 * 消息卡此前每条消息各 `mq.on(topic)` 再按 id 过滤（N 条消息 → N 个全局订阅）。
 * 现 useMessageActions 对每个 topic 全局只订阅一次，按 `{id}` 查表分发。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import mq from '@ckfw/utils/mq.js'
import { readSrc } from './support/feSource.mjs'
import { registerMessageActions } from '../src/composables/useMessageActions.js'

const TOPIC = 'test-message-actions'

function fire(id) {
  mq.emitRemote({ type: TOPIC, payload: JSON.stringify({ id }) })
}

test('按 message id 分发：只触发对应实例的 handler', () => {
  let a = 0
  let b = 0
  const offA = registerMessageActions('a', { [TOPIC]: () => a++ })
  const offB = registerMessageActions('b', { [TOPIC]: () => b++ })

  fire('a')
  assert.equal(a, 1)
  assert.equal(b, 0)
  fire('b')
  assert.equal(a, 1)
  assert.equal(b, 1)
  fire('other')
  assert.equal(a, 1, '未登记的 id 不得触发')
  assert.equal(b, 1)

  offA()
  offA() // cleanup 幂等
  fire('a')
  fire('b')
  assert.equal(a, 1, 'cleanup 后不再触发')
  assert.equal(b, 2)
  offB()
})

test('源码守卫：每 topic 只在 helper 内订阅一次，MessageItem 不再逐条 mq.on', () => {
  const helper = readSrc('composables/useMessageActions.js')
  assert.equal((helper.match(/mq\.on\(/g) || []).length, 1, 'helper 内每个 topic 只能有一处订阅调用')
  assert.match(helper, /if \(!unsubs\.has\(topic\)\)/, '仅该 topic 首个 handler 时建立订阅')
  assert.match(helper, /byId\.delete\(messageId\)/, 'cleanup 须摘除该消息的 handler')
  assert.match(helper, /if \(byId\.size > 0\) return/, '最后一个 handler 移除后才取消订阅')

  const vue = readSrc('views/chat/MessageItem.vue')
  assert.doesNotMatch(vue, /mq\.on\(/, 'MessageItem 不得再逐条订阅')
  assert.match(vue, /registerMessageActions\(props\.message\.id, handlersForNeed\)/)
  assert.match(vue, /if \(cleanupMessageActions\) \{\s*\n\s*cleanupMessageActions\(\)/)
  // need 仍按 type 筛选 DOM 实际交互（行为不变）
  assert.match(vue, /const need = new Set\(\)/)
})