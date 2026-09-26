/**
 * 「通用场景」下拉项 + 设为默认（2026-09-26，用户口径）：
 *   chat 场景下拉**固定首项**为「通用场景」= 选中即**关闭场景**（无场景 → 通用模式，不注入场景层）；
 *   该项可点 ★ **设为默认**（写入 `defaultScenario` 保留值 `__general__`）→ 重开/重启后仍为通用。
 *
 * 背景：此前下拉**只列真实场景**，且未选时会**自动选中第一个场景** → 一旦选过场景就回不到通用态。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ `.vue` 源码守卫 + i18n 实键校验。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const PANEL = 'views/chat/ChatPanel.vue'

// ═══════════════════════════════════════════════════════════════
// ① 下拉固定首项「通用场景」：位置在真实场景列表之前 + 选中即关闭场景
// ═══════════════════════════════════════════════════════════════
test('① 下拉首项「通用场景」：列在 v-for 之前，点击发空 id（关闭场景）', () => {
  const src = read(PANEL)
  const genIdx = src.indexOf("chat.general_scenario")
  const loopIdx = src.indexOf('v-for="s in scenarioOptions"')
  assert.ok(genIdx > 0, '须有「通用场景」项（i18n chat.general_scenario）')
  assert.ok(loopIdx > 0, '须保留真实场景列表')
  assert.ok(genIdx < loopIdx, '「通用场景」须为下拉**首项**（源码顺序在列表之前）')
  // 点击 = 发空 id（selectScenario 归一为 null → 无场景）
  assert.match(src, /v-mq:\[EventNames\.scenarioSelect\]\.click="\{ id: '' \}"/, '通用场景项须发空 id')
  assert.match(src, /function selectScenario\(id\) \{[\s\S]*?activeScenarioId\.value = id \|\| null/, '空 id 须归一为 null（无场景）')
  // 通用项也带「设为默认」星标
  assert.match(src, /v-mq:\[EventNames\.scenarioSetDefault\]\.stop\.click="\{ id: GENERAL_SCENARIO \}"/, '通用场景项须可设为默认')
})

// ═══════════════════════════════════════════════════════════════
// ② 保留值：GENERAL_SCENARIO 与「未配置」区分（否则会被自动选中第一个场景）
// ═══════════════════════════════════════════════════════════════
test('② GENERAL_SCENARIO 保留值：默认选中通用时不自动选第一个场景', () => {
  const src = read(PANEL)
  assert.match(src, /const GENERAL_SCENARIO = '__general__'/, '须有保留值常量 __general__')
  assert.match(src, /if \(defaultScenarioId\.value === GENERAL_SCENARIO\) \{[\s\S]*?activeScenarioId\.value = null/, '默认=通用 → 保持无场景（不回落第一个）')
  assert.match(src, /await saveUserConfig\(\{ defaultScenario: general \? GENERAL_SCENARIO : id \}\)/, '设为默认须写保留值')
})

// ═══════════════════════════════════════════════════════════════
// ③ 文案：未选场景显示「通用场景」（替代旧「通用模式」），双语齐备
// ═══════════════════════════════════════════════════════════════
test('③ 文案：未选场景显示「通用场景」；旧键 general_mode 已清除；双语齐备', () => {
  const src = read(PANEL)
  assert.doesNotMatch(src, /chat\.general_mode/, '不得再引用旧键 chat.general_mode')
  assert.match(src, /if \(!activeScenarioId\.value\) return t\('chat\.general_scenario'\)/, '未选场景须显示「通用场景」')
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    assert.ok(typeof chat.general_scenario === 'string' && chat.general_scenario.trim().length > 0,
      `${loc} 缺 chat.general_scenario`)
    assert.ok(!('general_mode' in chat), `${loc} 不应残留旧词条 general_mode`)
  }
  assert.equal(readLocale('zh-CN', 'chat.json').general_scenario, '通用场景', 'zh 须逐字一致')
})
