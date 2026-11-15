/**
 * 上下文用量环纯函数 + 会话状态归并单测。
 *
 * 口径（61 §4.3 `session-usage`）：send = 请求发出前估算；done = step 结束真实 usage（含缓存命中）。
 * 环分子 = 真实 prompt_tokens（缺失回落估算），分母 = max_context_token（0/缺失 → null，不伪造 0%）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  cachePercent,
  formatPercent,
  formatSignedPercent,
  formatTokens,
  isMainTurn,
  mergeUsage,
  toneOf,
  usageDeviation,
  usagePercent,
  usageTokens,
} from '../src/utils/llmUsage.js'
import { handleUsage, usageOf } from '../src/composables/useLlmUsage.js'
import { readSrc } from './support/feSource.mjs'

test('send → done：done 用真实值并保留同 step 的估算', () => {
  const send = { session: 's1', turn: 't1', step: 1, phase: 'send', est_prompt_tokens: 1000, max_context_token: 128000 }
  const done = { session: 's1', turn: 't1', step: 1, phase: 'done', prompt_tokens: 1200, cached_tokens: 800, completion_tokens: 30, total_tokens: 1230 }
  const a = mergeUsage(null, send, 1)
  const b = mergeUsage(a, done, 2)
  assert.equal(a.prompt_tokens, undefined)
  assert.equal(usageTokens(a), 1000)
  assert.equal(b.prompt_tokens, 1200)
  assert.equal(b.est_prompt_tokens, 1000)
  assert.equal(usageTokens(b), 1200)
  assert.equal(usagePercent(b), 1200 / 128000 * 100)
  assert.equal(cachePercent(b), 800 / 1200 * 100)
  assert.equal(usageDeviation(b), 20)
})

test('同一 step 的 done 之后迟到的 send 不覆盖', () => {
  const done = { session: 's1', turn: 't1', step: 2, phase: 'done', prompt_tokens: 900 }
  const lateSend = { session: 's1', turn: 't1', step: 2, phase: 'send', est_prompt_tokens: 700 }
  const a = mergeUsage(null, done, 1)
  const b = mergeUsage(a, lateSend, 2)
  assert.equal(b, a)
  assert.equal(b.prompt_tokens, 900)
})

test('新一轮 send 不继承上一轮真实值（环立即显示新请求估算）', () => {
  const done = { session: 's1', turn: 't1', step: 1, phase: 'done', prompt_tokens: 900 }
  const send = { session: 's1', turn: 't2', step: 1, phase: 'send', est_prompt_tokens: 300 }
  const a = mergeUsage(null, done, 1)
  const b = mergeUsage(a, send, 2)
  assert.equal(b.turn, 't2')
  assert.equal(b.prompt_tokens, undefined)
  assert.equal(usageTokens(b), 300)
})

test('分母未配置 → 百分比 null（环显示 —），不是 0%', () => {
  assert.equal(usagePercent({ prompt_tokens: 100 }), null)
  assert.equal(usagePercent({ prompt_tokens: 100, max_context_token: 0 }), null)
  assert.equal(toneOf(null), 'muted')
  assert.equal(formatPercent(null), '—')
})

test('占比封顶 100 且阈值 tone 正确', () => {
  assert.equal(usagePercent({ prompt_tokens: 128000, max_context_token: 128000 }), 100)
  assert.equal(usagePercent({ prompt_tokens: 256000, max_context_token: 128000 }), 100)
  assert.equal(toneOf(0), 'normal')
  assert.equal(toneOf(59.9), 'normal')
  assert.equal(toneOf(60), 'warn')
  assert.equal(toneOf(84.9), 'warn')
  assert.equal(toneOf(85), 'danger')
})

test('数字/百分比格式化', () => {
  assert.equal(formatTokens(1234567), '1,234,567')
  assert.equal(formatTokens(0), '0')
  assert.equal(formatSignedPercent(3.24), '+3.2%')
  assert.equal(formatSignedPercent(-1.05), '-1.1%')
  assert.equal(formatPercent(9.62), '9.6%')
  assert.equal(formatPercent(10), '10%')
})

test('子轮次（parents 非空）不进入主会话状态', () => {
  assert.equal(isMainTurn({ parents: [] }), true)
  assert.equal(isMainTurn({}), true)
  assert.equal(isMainTurn({ parents: ['p'] }), false)

  handleUsage({ session: 'child', turn: 't', step: 1, phase: 'send', est_prompt_tokens: 1, parents: ['parent'] })
  assert.equal(usageOf('child'), null)
})

test('按会话保存最近一次用量', () => {
  handleUsage({ session: 'sess-a', turn: 't1', step: 1, phase: 'send', est_prompt_tokens: 500, max_context_token: 10000 })
  handleUsage({ session: 'sess-b', turn: 't2', step: 1, phase: 'send', est_prompt_tokens: 100, max_context_token: 10000 })
  assert.equal(usageOf('sess-a').turn, 't1')
  assert.equal(usageOf('sess-b').turn, 't2')
  assert.equal(usageOf(''), null)
})

// 源码守卫（前端无组件级渲染测试）：未配置上下文窗口 → 点击环/提示 → 打开该 LLM 编辑框。
test('未配置 maxContextToken：环可点击并跳转到该 LLM 的配置页', () => {
  const ring = readSrc('components/common/UsageRing.vue')
  assert.match(ring, /usage_no_context_cap/)
  assert.match(ring, /@click\.stop="\$emit\('open-config'\)"/)
  assert.match(ring, /is-actionable/)

  const input = readSrc('views/chat/InputBox.vue')
  assert.match(input, /@open-config="\$emit\('open-config'\)"/)

  const panel = readSrc('views/chat/ChatPanel.vue')
  assert.match(panel, /kind: 'settings-llm'/)
  assert.match(panel, /llm: name/)
  assert.match(panel, /sessionUsage\.value\.llm/)

  const codeview = readSrc('views/codeview/CodeView.vue')
  assert.match(codeview, /openSpecialTab\(kind, event\.title, event\.tab, event\.llm\)/)
  assert.match(codeview, /focusLlm/)
  assert.match(codeview, /focusNonce/)

  const settings = readSrc('views/config/SettingsLLMPage.vue')
  assert.match(settings, /focusLlm: \{ type: String/)
  assert.match(settings, /focusRequestedLLM/)
})