/**
 * 自由布局子系统 · mq 主题审计（仓库级断言，UILAYOUT-1 §7 P1④ / §3.13）。
 *
 * 口径：`src/layout-free/events/event-names.js` 里的**每个主题都要有发布方与订阅方**
 * （防「新增主题忘接线」）。断言本体 = `scripts/topic-audit.mjs` 的纯函数，
 * 与 CLI `npm run audit:topics` 共用一份实现（子工程 `scripts/audit-topics.mjs` 已迁入）。
 *
 * 另断言 P1④ 的命名口径：主题值统一 `lf-` 前缀、键名不变（`EventNames.xxx` 引用面零改动）、
 * 值唯一（防重名互相顶掉）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { findOrphanTopics } from '../scripts/topic-audit.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..', 'src', 'layout-free')
const result = findOrphanTopics(ROOT)

test('layout-free：主题审计 —— 每个主题都有发布方与订阅方', () => {
  assert.deepEqual(result.noSub, [], `有发布、无订阅：\n${result.noSub.join('\n')}`)
  assert.deepEqual(result.noPub, [], `有订阅、无发布（组件绕开 mq 直连 store）：\n${result.noPub.join('\n')}`)
})

test('layout-free：主题命名 —— 统一 lf- 前缀（91 个，§3.8 / §4.1 ④）', () => {
  assert.equal(result.total, 91, '主题总数应为 91（80 + UILAYOUT-2 新增 11）')
  const unprefixed = result.topics.filter((t) => !t.startsWith('lf-'))
  assert.deepEqual(unprefixed, [], `未加 lf- 前缀：\n${unprefixed.join('\n')}`)
  const badKeys = result.keys.filter((k) => k.startsWith('lf'))
  assert.deepEqual(badKeys, [], `键名不应加前缀（引用面零改动）：\n${badKeys.join('\n')}`)
})

test('layout-free：主题值唯一（防重名互相顶掉）', () => {
  const seen = new Map()
  const dup = []
  for (const t of result.topics) {
    if (seen.has(t)) dup.push(t)
    seen.set(t, true)
  }
  assert.deepEqual(dup, [], `重复主题值：\n${dup.join('\n')}`)
})

test('layout-free：与既有近名主题不再混淆（tab-* vs lf-tabs-*）', () => {
  const en = readFileSync(join(ROOT, 'events', 'event-names.js'), 'utf8')
  // 子工程原 `tabs-*` 与主工程既有 `tab-select/tab-close/tab-pin/tab-more-toggle` 只差一个字母
  // → 加前缀后不得再出现裸的 `'tabs-*'`
  const bare = [...en.matchAll(/^\s{2}\w+:\s*'(tabs?-[\w-]+)'/gm)].map((m) => m[1])
  assert.deepEqual(bare, [], `仍存在与主工程近名的裸主题：\n${bare.join('\n')}`)
})
