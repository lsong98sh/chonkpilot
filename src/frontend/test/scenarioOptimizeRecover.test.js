/**
 * SCEN-005-S02【恢复优化】：场景编辑 AgentEditor 的优化前快照 / 恢复入口（2026-11-05）。
 *
 * 口径（与 TextEditDialog 的【恢复优化】同源）：
 *   - 快照按 agent `_key` 保存（Map<key, prompt>），**仅真正开始优化前**写入；
 *   - 优化完成 / 失败都保留快照（可恢复）；
 *   - 恢复 = 回填当前 agent.prompt 为快照并清空该快照；恢复是草稿态，不自动落库；
 *   - 按钮仅在快照存在时可见，优化中禁用；文案走 i18n，不硬编码。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 源码 / 文案守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'

const read = (rel) => readSrc(rel)
const LOCALES = ['zh-CN', 'en-US']

/** 取函数体（`function name(...) {` 到首个顶格 `}`） */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('父组件：按 _key 保存优化前快照，且只在开始优化前写入', () => {
  const d = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(d, /const optimizeSnapshots = ref\(new Map\(\)\)/, '须用 Map<key, prompt> 保存快照')
  const fn = fnBody(d, 'handleOptimize')
  assert.ok(fn, '未找到 handleOptimize')
  // 首次赋值出现在真正发起请求之前（而不是载入/切换 agent 时）
  assert.match(fn, /optimizeSnapshots\.value\.set\(key, agent\.prompt\)[\s\S]*optimizeAgentPrompt\(/,
    '须在 optimizeAgentPrompt 之前保存快照')
  // 完成 / 失败分支不得清空快照（仅恢复时清空）
  assert.doesNotMatch(fn, /optimizeSnapshots[^\n]*\.(delete|clear)\(/, '完成/失败须保留快照')
})

test('父组件：恢复优化回填当前 agent.prompt 并清空该 agent 快照（不自动保存）', () => {
  const d = read('views/scenario/ScenarioEditDialog.vue')
  const fn = fnBody(d, 'handleRecoverOptimize')
  assert.ok(fn, '未找到 handleRecoverOptimize')
  assert.match(fn, /const agent = agents\.value\[idx\]/)
  assert.match(fn, /optimizeSnapshots\.value\.has\(key\)/, '无快照须直接返回')
  assert.match(fn, /agent\.prompt = optimizeSnapshots\.value\.get\(key\)/, '须回填快照')
  assert.match(fn, /optimizeSnapshots\.value\.delete\(key\)/, '恢复后须清空该快照')
  assert.doesNotMatch(fn, /handleSave|saveScenario/, '恢复是草稿态，不得自动落库')
  assert.match(fn, /if \(optimizing\.value\) return/, '优化中不得恢复')
})

test('父组件：把「快照存在」与事件透传给 AgentEditor', () => {
  const d = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(d, /:can-recover-optimize="hasOptimizeSnapshot\(selectedAgent\._key\)"/)
  assert.match(d, /@recover-optimize="handleRecoverOptimize"/)
  assert.match(d, /function hasOptimizeSnapshot\(key\) \{[\s\S]*optimizeSnapshots\.value\.has\(key\)/)
})

test('AgentEditor：【恢复优化】按钮可见/禁用正确，emit recover-optimize，文案走 i18n', () => {
  const e = read('views/scenario/AgentEditor.vue')
  assert.match(e, /recover-optimize/, '须声明 recover-optimize 事件')
  const btn = e.match(/<Button[^>]*recover-optimize[\s\S]*?<\/Button>/)
  assert.ok(btn, '未找到【恢复优化】按钮')
  assert.match(btn[0], /v-if="canRecoverOptimize"/, '快照存在时才可见')
  assert.match(btn[0], /:disabled="optimizing"/, '优化中须禁用')
  assert.match(btn[0], /\$t\('scenario\.recover_optimize'\)/, '文案须走 i18n')
  assert.doesNotMatch(btn[0], /恢复优化/, '按钮不得硬编码中文')
  assert.match(e, /canRecoverOptimize:\s*\{\s*type: Boolean,/, '须接收 canRecoverOptimize prop')
})

test('i18n：scenario.recover_optimize 中英齐备', () => {
  for (const loc of LOCALES) {
    const s = JSON.parse(read(`locales/${loc}/scenario.json`))
    assert.ok(typeof s.recover_optimize === 'string' && s.recover_optimize.trim().length > 0,
      `${loc} 缺 scenario.recover_optimize`)
  }
})