/**
 * llm-receive 载荷形状守卫（2026-11-16 删除 llm-token 后）：
 *   - 文本/推理必须读 `payload.text`（server 定稿形状 `{type, payload:{text}}`）；
 *   - 兼容扁平 `text`（旧/异常发送方）；
 *   - `llm-token` 不再作为渲染路径存在。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { register } from 'node:module'

// sessionMessages → @ckfw/components/ui（.vue）→ 需要 SFC 钩子；静态 import 会先于 register 解析。
register('./support/vue-sfc-hooks.mjs', import.meta.url)
await new Promise((r) => setTimeout(r, 0))

const { createSessionMessages } = await import('../src/utils/sessionMessages.js')

test('reason：读 payload.text 并生成 reasoning 段', () => {
  const s = createSessionMessages()
  s.handleReceive({ type: 'reason', payload: { text: '思考中' } })
  assert.equal(s.messages.value.length, 1)
  assert.equal(s.messages.value[0].role, 'assistant')
  assert.equal(s.messages.value[0].type, 'reasoning')
  assert.equal(s.messages.value[0].content, '思考中')
})

test('text：读 payload.text，且与 reason 分段', () => {
  const s = createSessionMessages()
  s.handleReceive({ type: 'reason', payload: { text: 'R' } })
  s.handleReceive({ type: 'text', payload: { text: 'A' } })
  assert.deepEqual(
    s.messages.value.map((m) => [m.type, m.content]),
    [['reasoning', 'R'], ['text', 'A']],
  )
})

test('扁平 text 兼容：payload 缺失时读 data.text', () => {
  const s = createSessionMessages()
  s.handleReceive({ type: 'text', text: 'flat' })
  assert.equal(s.messages.value[0].content, 'flat')
})

test('payload 为空：不崩溃、不追加空段', () => {
  const s = createSessionMessages()
  s.handleReceive({ type: 'reason', payload: {} })
  assert.equal(s.messages.value.length, 0)
})