#!/usr/bin/env node
/**
 * mq topic 审计 CLI（仓库级）：
 *   node scripts/audit-topics.mjs      （或 npm run audit:topics）
 *
 * 审计对象 = 自由布局子系统 `src/layout-free`（UILAYOUT-1 §3.8 的 80 个 `lf-*` 前台主题）。
 * 同一断言亦随 `npm test` 跑（test/layoutFreeTopicAudit.test.js）——见 §3.13「纳入前端测试」。
 */
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { findOrphanTopics } from './topic-audit.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..', 'src', 'layout-free')

const r = findOrphanTopics(root)
console.log(`topic 总数: ${r.total}`)
console.log(`\n=== 有订阅、无发布（组件绕开 mq 直连 store）: ${r.noPub.length} ===`)
r.noPub.forEach((t) => console.log(`  ${t}`))
console.log(`\n=== 发布方由宿主承担（合法豁免，已登记）: ${r.hostExempt.length} ===`)
r.hostExempt.forEach((t) => console.log(`  ${t}`))
console.log(`\n=== 无生产者（登记为无源，一期全 singleton）: ${r.noSource.length} ===`)
r.noSource.forEach((t) => console.log(`  ${t}`))
console.log(`\n=== 有发布、无订阅: ${r.noSub.length} ===`)
r.noSub.forEach((t) => console.log(`  ${t}`))
if (r.noPub.length || r.noSub.length) process.exitCode = 1
