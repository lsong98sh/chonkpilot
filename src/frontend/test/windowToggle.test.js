/**
 * WIN-002-S04【双击顶栏】最大化/还原（2026-11-05）。
 *
 * 口径：
 *   - 顶栏根节点用原生 `@dblclick`（不再走 `v-mq:[EventNames.windowToggle]` → 无订阅者）；
 *   - handler 先做 `NO_DRAG` 守卫（交互元素让给控件自身），再发
 *     `gui.window.status{command:'maximize'}` —— 复用宿主切换语义，不自行判断 isMaximized；
 *   - 不新增 `window-toggle` 订阅者。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 源码 / 文档守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc, readRepo } from './support/feSource.mjs'

/** 取函数体（`function name(...) {` 到首个顶格 `}`） */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

test('ChonkTopBar 根节点：原生 @dblclick，且不再有 window-toggle 的 v-mq 绑定', () => {
  const s = readSrc('layout/ChonkTopBar.vue')
  assert.match(
    s,
    /<div\s+class="toolbar ck-topbar"[^>]*@dblclick="onTopBarDblClick"/,
    '根节点须使用原生 @dblclick="onTopBarDblClick"',
  )
  assert.doesNotMatch(s, /v-mq:\[EventNames\.windowToggle\]/, '不得再绑定 window-toggle')
})

test('ChonkTopBar：handler 先做 NO_DRAG 守卫，再发 gui.window.status{command:maximize}', () => {
  const s = readSrc('layout/ChonkTopBar.vue')
  const fn = fnBody(s, 'onTopBarDblClick')
  assert.ok(fn, '未找到 onTopBarDblClick')

  const guard = fn.search(/e\.target\.closest\(NO_DRAG\)/)
  const emit = fn.search(/mq\.emit\(\s*EventNames\.guiWindowStatus,\s*\{\s*command:\s*'maximize'\s*\}/)
  assert.ok(guard >= 0, '须命中 NO_DRAG 守卫')
  assert.ok(emit >= 0, '须发 gui.window.status{command:\'maximize\'}')
  assert.ok(guard < emit, '守卫须在 emit 之前（交互元素上不触发）')
  assert.doesNotMatch(fn, /isMaximized/, '不自行判断最大化态（复用宿主切换语义）')
})

test('ChonkTopBar：不需要 window-toggle 订阅者 / 无用发射', () => {
  const s = readSrc('layout/ChonkTopBar.vue')
  assert.doesNotMatch(s, /mq\.on\(EventNames\.windowToggle/, '不得新增 window-toggle 订阅')
})

test('文档：WIN-002 总表行与 S04 明细已标为已实现，并写明双击顶栏', () => {
  const doc = readRepo('docs/spec/30-function-points/31-窗口与布局.md')

  const row = doc.match(/^\| WIN-002 \|.*$/m)
  assert.ok(row, '总表缺 WIN-002 行')
  assert.doesNotMatch(row[0], /未实现|⚠️/, '状态不应再含「未实现」/⚠️')
  assert.match(row[0], /双击顶栏（非控件区）切换最大化\/还原/, '总表说明须写明双击顶栏切换')
  assert.match(row[0], /2026-11-05/)

  const s04 = doc.match(/^\| WIN-002-S04 \|.*$/m)
  assert.ok(s04, '明细缺 WIN-002-S04 行')
  assert.doesNotMatch(s04[0], /未实现|⚠️/, 'S04 不应再含「未实现」/⚠️')
  assert.match(s04[0], /双击顶栏非交互区/, 'S04 步骤须写明双击非交互区')
  assert.match(s04[0], /最大化\/还原切换/, 'S04 预期须写明最大化/还原切换')
  assert.match(s04[0], /ChonkTopBar\.vue#onTopBarDblClick/, 'S04 穿透点须指向 handler')
  assert.match(s04[0], /gui\.window\.status\{command:'maximize'\}/, 'S04 穿透点须含窗口命令')
})