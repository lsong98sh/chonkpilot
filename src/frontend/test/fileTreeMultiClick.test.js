/**
 * FT-005-S02【多选下单击不触发改名】（2026-11-05）。
 *
 * 口径：
 *   - 多选（>1）状态下的普通（鼠标）单击只把多选收敛为单选，随后直接返回：
 *     不打开临时页签、不进入改名；
 *   - 键盘 Enter 调 `onRowClick(selNode)`（无 event）保持原逻辑：
 *     折叠多选后继续打开/改名判定；
 *   - 不改多选集合逻辑、拖拽与改名实现。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 源码 / 文档守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc, readRepo } from './support/feSource.mjs'

const FILE_TREE = 'views/filetree/FileTree.vue'
const DOC = 'docs/spec/30-function-points/32-文件树与文件操作.md'

/** 取函数体（`function name(...) {` 到首个顶格 `}`） */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('FT-005-S02：多选（>1）下普通鼠标单击 → 清多选收敛为单选后直接返回', () => {
  const src = readSrc(FILE_TREE)
  assert.match(src, /function onRowClick\(node, event\)/, 'onRowClick 须同时接收 node / event')
  const fn = fnBody(src, 'onRowClick')
  assert.ok(fn, '未找到 onRowClick')

  assert.match(fn, /const wasMulti = selectedKeys\.value\.size > 1/,
    '须把「是否多选」记到局部变量 wasMulti（判定在清多选之前）')
  assert.match(fn, /if \(wasMulti\) \{\s*selectedKeys\.value = new Set\(\)\s*\}/,
    'wasMulti 时须清多选集合')
  assert.match(fn, /selectedKey\.value = node\.path/,
    '收敛后须把点击项设为单选主选中')
  assert.match(fn, /if \(wasMulti && event\) return/,
    '多选收敛后（鼠标点击）须直接返回')
})

test('FT-005-S02：收敛分支到 return 之间不得改名 / 打开页签', () => {
  const fn = fnBody(readSrc(FILE_TREE), 'onRowClick')
  assert.ok(fn, '未找到 onRowClick')

  const from = fn.indexOf('const wasMulti')
  const to = fn.indexOf('if (wasMulti && event) return')
  assert.ok(from >= 0 && to > from, '未找到 wasMulti 分支区间')
  const branch = fn.slice(from, to)
  assert.doesNotMatch(branch, /doRename/, '多选收敛分支不得进入改名')
  assert.doesNotMatch(branch, /fileOpen/, '多选收敛分支不得打开页签')
  assert.doesNotMatch(branch, /onToggle/, '多选收敛分支不得展开/收起目录')
})

test('FT-005-S02：提前 return 受 event 存在约束，键盘 Enter 语义不变', () => {
  const src = readSrc(FILE_TREE)
  const fn = fnBody(src, 'onRowClick')
  assert.ok(fn, '未找到 onRowClick')
  assert.doesNotMatch(fn, /if \(wasMulti\) return/,
    '不得无条件 return（会改变键盘 Enter 折叠多选后继续处理的语义）')

  const key = fnBody(src, 'onKeyDown')
  assert.ok(key, '未找到 onKeyDown')
  assert.match(key, /e\.key === 'Enter'[\s\S]*onRowClick\(selNode\)/,
    'Enter 仍以无 event 参数调用 onRowClick（不触发提前 return）')
})

test('文档：FT-005 总表行为 ✅，顶部状态行不再把 FT-005 列为 ⚠️', () => {
  const doc = readRepo(DOC)
  const header = doc.split('\n').find(l => l.startsWith('> 状态：'))
  assert.ok(header, '未找到文档顶部状态行')
  assert.doesNotMatch(header, /FT-005\//, '顶部状态行须移除 FT-005 ⚠️')
  assert.match(header, /FT-006\/007\/008 ⚠️/, '须保留 FT-006/007/008 ⚠️ 表述')
  assert.match(header, /FT-016 🗄 已下线/, '须保留 FT-016 🗄 表述')

  const row = doc.split('\n').find(l => l.startsWith('| FT-005 |'))
  assert.ok(row, '未找到 FT-005 总表行')
  assert.match(row, /\| ✅ \|/, 'FT-005 总表行状态须为 ✅')
  assert.doesNotMatch(row, /⚠️/, 'FT-005 总表行不得保留 ⚠️')
  assert.doesNotMatch(row, /S02 多选下单击仍可能触发改名/, '总表行须去掉旧缺陷说明')
})

test('文档：FT-005-S02 明细预期已更新（不再「未完全实现」）', () => {
  const doc = readRepo(DOC)
  const row = doc.split('\n').find(l => l.startsWith('| FT-005-S02 |'))
  assert.ok(row, '未找到 FT-005-S02 明细行')
  assert.doesNotMatch(row, /未完全实现/, '不得保留「未完全实现」')
  assert.match(row, /普通单击只把多选收敛为单选，不触发改名\/打开（2026-11-05）/,
    '预期须为收敛为单选、不触发改名/打开')
  assert.equal(row.split('|').length, 9, '须保持 7 列（含首尾空列）')
})