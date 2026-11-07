/**
 * 向导记忆类别键（SCEN 修复）：`memoryCategoryKey` 净化 / 去重（2026-11-05）。
 *
 * 背景：en-US 下预置类别显示名含空格（如 "Project overview"），`buildPayload` 原样作为
 * category 落库 → 后端 `capfs.ValidMemoryCategoryName` 拒绝（空白 / 首尾空白均非法）。
 * 修复：payload 用稳定安全键，UI 仍用显示名。
 *
 * 纯逻辑直调（`src/utils/memoryCategory.js`）+ 接线守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { memoryCategoryKey } from '../src/utils/memoryCategory.js'
import { readSrc } from './support/feSource.mjs'

const read = (rel) => readSrc(rel)

/** 与后端 capfs.ValidMemoryCategoryName 同口径的 JS 校验（测试侧独立实现）。 */
function validCategory(name) {
  if (!name || Array.from(name).length > 64) return false
  if (name !== name.trim() || name.startsWith('.')) return false
  for (const ch of name) {
    if (/[\s\u0000-\u001f\u007f-\u009f]/.test(ch)) return false
    if ('/\\:*?"<>|'.includes(ch)) return false
  }
  return !/^(con|prn|aux|nul|com[1-9]|lpt[1-9])$/i.test(name)
}

test('预设：用稳定 id（kebab），不受 en-US 含空格显示名影响', () => {
  assert.equal(memoryCategoryKey({ id: 'project-overview', name: 'Project overview', preset: true }), 'project-overview')
  assert.equal(memoryCategoryKey({ id: 'build-release', name: 'Build & release', preset: true }), 'build-release')
})

test('自定义：Project Overview → Project-Overview（空格转 -）', () => {
  const key = memoryCategoryKey({ id: 'custom-1', name: 'Project Overview', preset: false })
  assert.equal(key, 'Project-Overview')
  assert.ok(validCategory(key))
})

test('自定义：非法字符 / 控制字符 / 前导点 / 首尾空白 → 安全键', () => {
  const key = memoryCategoryKey({ id: 'custom-2', name: '  .a/b\\c:d*e?f"g<h>i|j\u0001  ', preset: false })
  assert.equal(key, 'abcdefghij-')
  assert.ok(validCategory(key))

  assert.equal(memoryCategoryKey({ name: '  ..hidden  ', preset: false }), 'hidden')
  assert.equal(memoryCategoryKey({ name: 'a\tb\nc', preset: false }), 'a-b-c')
})

test('自定义：名称为空 → custom-<id 后缀> 回退', () => {
  assert.equal(memoryCategoryKey({ id: 'custom-1731666000000', name: '   ', preset: false }), 'custom-1731666000000')
  assert.equal(memoryCategoryKey({ id: 'custom-x', name: '', preset: false }), 'custom-x')
})

test('Windows 保留设备名 → 加前缀（避免后端拒绝）', () => {
  assert.equal(memoryCategoryKey({ name: 'CON', preset: false }), 'cat-CON')
  assert.equal(memoryCategoryKey({ name: 'lpt9', preset: false }), 'cat-lpt9')
  assert.equal(memoryCategoryKey({ name: 'CONSOLE', preset: false }), 'CONSOLE')
})

test('去重：同名追加 -2 / -3，且同批全部唯一、全部通过后端口径', () => {
  const used = new Set()
  const list = [
    { id: 'project-overview', name: 'Project overview', preset: true },
    { id: 'custom-1', name: 'Project Overview', preset: false },
    { id: 'custom-2', name: 'Project-Overview', preset: false },
    { id: 'custom-3', name: '项目 概要', preset: false },
    { id: 'custom-4', name: '项目 概要', preset: false },
  ]
  const keys = list.map(m => memoryCategoryKey(m, used))
  assert.deepEqual(keys, [
    'project-overview',
    'Project-Overview',
    'Project-Overview-2',
    '项目-概要',
    '项目-概要-2',
  ])
  assert.equal(new Set(keys).size, keys.length)
  for (const k of keys) assert.ok(validCategory(k), `非法类别键：${k}`)
})

test('接线：buildPayload 使用 memoryCategoryKey（不再用显示名当 category）', () => {
  const w = read('views/scenario/ScenarioWizardDialog.vue')
  assert.match(w, /import \{ memoryCategoryKey \} from '\.\.\/\.\.\/utils\/memoryCategory'/)
  assert.match(w, /const usedCategoryKeys = new Set\(\)/)
  assert.match(w, /category: memoryCategoryKey\(m, usedCategoryKeys\)/)
  assert.doesNotMatch(w, /category:\s*m\.name/, 'category 不得直接用显示名（en-US 含空格）')
})