/**
 * 场景编辑「优化提示词」接线守卫（2026-09-27）。
 *
 * 背景：`ScenarioEditDialog.handleOptimize` 原为桩（只弹「尚未实现」）；同一优化能力早已实现并在
 * `TextEditDialog` / `ContextConfig` 使用（`api/config.js optimizeAgentPrompt` → 既有消息面
 * `gui.prompt-optimise`，流式事件 optimize-token/done/error）。本守卫断言前端已接线：
 *   - 删除桩提示与 i18n 键 `scenario.optimize_not_implemented`（zh/en 一并回收）；
 *   - 复用既有 `optimizeAgentPrompt`，流式追加写入当前 agent.prompt（草稿态，不自动落库）；
 *   - 空 prompt → warning 且不发请求；失败 → `message.error`；优化中防重入（按钮 loading/禁用）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 源码 / 文案守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const LOCALES = ['zh-CN', 'en-US']

/** 取函数体（`function name(...) {` 到首个顶格 `}`） */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('场景编辑：优化提示词复用既有 optimizeAgentPrompt（非桩，流式回显写入 prompt）', () => {
  const d = read('views/scenario/ScenarioEditDialog.vue')
  // 桩已移除：不再有「尚未实现」提示 / i18n 键
  assert.doesNotMatch(d, /optimize_not_implemented/, '不得再引用已删的桩 i18n 键')
  // 复用既有链路（与 TextEditDialog 同源）
  assert.match(d, /import \{[^}]*optimizeAgentPrompt[^}]*\} from '\.\.\/\.\.\/api\/config'/,
    '须复用既有优化链路（api/config.js optimizeAgentPrompt）')

  const fn = fnBody(d, 'handleOptimize')
  assert.ok(fn, '未找到 handleOptimize')
  // 不再只弹提示（桩形态）→ 确实调用既有优化通道
  assert.doesNotMatch(fn, /message\.info\(/, 'handleOptimize 不得只弹提示（桩形态）')
  assert.match(fn, /optimizeAgentPrompt\(/, '须调用既有 optimizeAgentPrompt')
  // title / useCase 走 i18n（不硬编码），useCase 为自由文本 → 不改消息面
  assert.match(fn, /title: t\('scenario\.optimize_title'/, 'title 须走 i18n（含 {name}）')
  assert.match(fn, /useCase: t\('scenario\.optimize_use_case'\)/, 'useCase 须走 i18n')
  assert.match(fn, /prompt: agent\.prompt/, '须透传当前 agent.prompt')
  // 流式追加写入 prompt（草稿态）
  assert.match(fn, /\.prompt = \(cur\.prompt \|\| ''\) \+ chunk/, '流式 token 须追加写入当前 agent.prompt')
  // 完成不自动落库（草稿态：落库由用户点顶部【保存】决定）
  assert.doesNotMatch(fn, /handleSave|saveScenario/, '优化完成不得自动落库（草稿态）')
  // 空入参 → warning 且不发请求（warning 在 optimizeAgentPrompt 之前 return）
  assert.match(fn, /message\.warning\(t\('common\.input_required'\)\)/, '空 prompt 须提示（不发请求）')
  assert.match(fn, /optimizing\.value = true[\s\S]*optimizeAgentPrompt\(/,
    '仅在通过空值校验后才置优化中并发起请求')
  // 失败 → 可见提示
  assert.match(fn, /message\.error\(t\('common\.optimize_failed'\)/, '失败须 message.error')
  // 防重入
  assert.match(fn, /if \(optimizing\.value\) return/, '优化中须防重入')
})

test('场景编辑：优化按钮 loading / 禁用（optimizing 由父组件透传）', () => {
  const d = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(d, /:optimizing="optimizing"/, '父组件须把 optimizing 透传给 AgentEditor')
  const e = read('views/scenario/AgentEditor.vue')
  assert.match(e, /optimizing:\s*\{\s*type: Boolean,\s*default: false,?\s*\}/, 'AgentEditor 须接收 optimizing')
  const btn = e.match(/<Button[^>]*agentOptimize[^>]*>/)
  assert.ok(btn, '未找到「优化提示词」按钮')
  assert.match(btn[0], /:loading="optimizing"/, '优化按钮须 loading')
  assert.match(btn[0], /:disabled="optimizing"/, '优化按钮须禁用（防重入）')
})

test('i18n：scenario.optimize_title / optimize_use_case 中英齐备，桩键已回收', () => {
  for (const loc of LOCALES) {
    const s = JSON.parse(read(`locales/${loc}/scenario.json`))
    assert.ok(typeof s.optimize_title === 'string' && s.optimize_title.trim().length > 0,
      `${loc} 缺 scenario.optimize_title`)
    assert.match(s.optimize_title, /\{name\}/, `${loc} optimize_title 须含 {name} 占位`)
    assert.ok(typeof s.optimize_use_case === 'string' && s.optimize_use_case.trim().length > 0,
      `${loc} 缺 scenario.optimize_use_case`)
    assert.equal(s.optimize_not_implemented, undefined,
      `${loc} 应回收 scenario.optimize_not_implemented（桩文案）`)
  }
})

test('项目规则：ScenarioEditDialog 不得用 watch / watchEffect（本次改动同守）', () => {
  assert.doesNotMatch(read('views/scenario/ScenarioEditDialog.vue'), /\bwatchEffect\(|\bwatch\(/)
})
