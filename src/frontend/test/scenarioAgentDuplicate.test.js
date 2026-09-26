/**
 * 场景内 agent 重名 → 保存报错（2026-09-26 用户裁决，[42 §2 (175)] · [37-场景 SCEN-003-S05]）。
 *
 * 前端职责：**保存前预检**同场景内 agent 重名（trim + 大小写不敏感），命中即以 **i18n（中英）**
 * 提示并中止；后端 `capfs.WriteScenarioDir` 为**权威拦截**，其错误经既有 catch → `message.error`
 * **显式提示**（不得静默失败）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 源码/文案守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

test('i18n：scenario.agent_duplicate 中英双语齐备（含 {name} 占位）', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const s = JSON.parse(read(`locales/${loc}/scenario.json`))
    assert.ok(s.agent_duplicate, `${loc} scenario.agent_duplicate 缺失`)
    assert.match(s.agent_duplicate, /\{name\}/, `${loc} 文案须含 {name} 占位`)
  }
})

test('保存前重名预检 + i18n 提示 + 后端错误显式提示（不静默失败）', () => {
  const d = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(d, /function findDuplicateAgentName\(/, '须有重名预检函数')
  assert.match(d, /findDuplicateAgentName\(agents\.value\)/, '保存前须调用重名预检')
  assert.match(d, /t\('scenario\.agent_duplicate', \{ name: dupName \}\)/, '命中重名须走 i18n 提示')
  // 预检口径 = trim + 大小写不敏感（与后端 capfs 校验同口径）
  assert.match(d, /\.trim\(\)/, '预检须 trim')
  assert.match(d, /\.toLowerCase\(\)/, '预检须大小写不敏感')
  // 后端错误必须显式提示：catch → message.error（不得静默）
  assert.match(d, /catch \(e\)[\s\S]*message\.error\(t\('scenario\.save_failed'\)/,
    '后端保存错误须显式 message.error')
})

test('项目规则：ScenarioEditDialog 不得用 watch / watchEffect（本次改动同守）', () => {
  assert.doesNotMatch(read('views/scenario/ScenarioEditDialog.vue'), /\bwatchEffect\(|\bwatch\(/)
})
