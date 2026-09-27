/**
 * 场景编辑弹窗「另存为」（2026-09-27 用户口径）：
 * **编辑**模式（!isNew）底部按钮区新增「另存为」→ `promptInput` 输入**新目录名**（= 场景 id）→
 * 复用既有保存路径写入新目录（新建语义：id 不存在 → 后端新建目录），**零后端 / 零消息面改动**。
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

const DIALOG = 'views/scenario/ScenarioEditDialog.vue'

test('另存为按钮：仅编辑模式显示（v-if="!isNew"），位于「取消」左侧', () => {
  const d = read(DIALOG)
  assert.match(d, /<Button v-if="!isNew"[^>]*@click="handleSaveAs"/,
    '须有 !isNew 守卫的「另存为」按钮')
  assert.match(d, /\{\{ \$t\('scenario\.save_as'\) \}\}/, '按钮文案须走 i18n scenario.save_as')
  // 位置：另存为 → 取消 → 保存（同属 .edit-footer）
  const footer = d.slice(d.indexOf('class="edit-footer"'))
  const iSaveAs = footer.indexOf('scenario.save_as')
  const iCancel = footer.indexOf('scenarioCancel')
  const iSave = footer.indexOf('EventNames.scenarioSave')
  assert.ok(iSaveAs > -1 && iCancel > iSaveAs && iSave > iCancel,
    '按钮顺序须为 另存为 → 取消 → 保存')
})

test('另存为：走 promptInput 输入新目录名 + 复用既有目录名校验', () => {
  const d = read(DIALOG)
  assert.match(d, /import \{ message, confirm, promptInput \} from '\.\.\/\.\.\/components\/ui'/,
    '须引入 promptInput')
  assert.match(d, /await promptInput\(t\('scenario\.save_as_prompt'\)/,
    '另存为须走 promptInput 且用 i18n 提示文案')
  assert.match(d, /if \(input === null\) return/, '取消（null）须中止')
  assert.match(d, /handleSaveAs[\s\S]*?\/\^\[A-Za-z0-9_-\]\+\$\/\s*\.test\(id\)/,
    '另存为须复用 /^[A-Za-z0-9_-]+$/ 目录名校验')
})

test('另存为：复用同一保存路径（doSave）+ 成功按「新建」口径提示 + emit done', () => {
  const d = read(DIALOG)
  assert.match(d, /async function doSave\(idOverride\)/, '须抽出可传 override id 的 doSave')
  assert.match(d, /handleSave[\s\S]*?await doSave\(\)/, '「保存」复用 doSave()')
  assert.match(d, /handleSaveAs[\s\S]*?await doSave\(id\)/, '「另存为」复用 doSave(id)')
  assert.match(d, /const asCreate = props\.isNew \|\| !!idOverride/, 'idOverride 非空视为新建')
  assert.match(d, /message\.success\(asCreate \? t\('scenario\.created'\) : t\('scenario\.updated'\)\)/,
    '另存为成功须按「新建」口径提示')
  assert.match(d, /emit\('done'\)/, '成功须沿用既有收尾（emit done → 列表重载）')
})

test('i18n：scenario.save_as / save_as_prompt 中英齐备（不删既有键）', () => {
  const zh = JSON.parse(read('locales/zh-CN/scenario.json'))
  const en = JSON.parse(read('locales/en-US/scenario.json'))
  assert.equal(zh.save_as, '另存为')
  assert.equal(en.save_as, 'Save As')
  assert.ok(zh.save_as_prompt && en.save_as_prompt, 'save_as_prompt 中英须齐备')
  assert.ok(zh.dir_name_required && en.dir_name_required, '不得删除既有 dir_name_required')
  assert.ok(zh.dir_name_format && en.dir_name_format, '不得删除既有 dir_name_format')
})

test('另存为：零新增 MQ 主题（本地 @click，仍仅走 data-scenario-save）', () => {
  const d = read(DIALOG)
  assert.doesNotMatch(d, /v-mq:\[EventNames\.scenarioSaveAs\]/, '不得新增场景另存为 MQ 主题')
  assert.doesNotMatch(read('events/event-names.js'), /scenarioSaveAs|scenario-save-as/,
    'event-names 不得新增另存为主题')
  assert.match(d, /saveScenario\(\{ \.\.\.form\.value, agents: payloadAgents \}\)/,
    '须复用既有 saveScenario（data-scenario-save）')
  assert.match(d, /handleSaveAs[\s\S]*?doSave[\s\S]*?saveScenario\(/,
    '另存为链路须落到既有 saveScenario')
})
