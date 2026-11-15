/**
 * 工具配置页四模式（2026-11-14 重构）源码守卫：
 *   - 模式 sync/async/auto/manual（manual 不再折叠为 auto；never→sync / always→async 仅读取兼容）；
 *   - 转异步阈值仅 auto/manual 可填；
 *   - 执行硬上限所有模式可填，仅 >0 写库（<=0 / 留空 = 不处理）；
 *   - 旧参数 on_timeout / cancel_on_timeout 已退役（不再出现在页面/i18n）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 页面 / locale 源码守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'

const read = (rel) => readSrc(rel)
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const PAGE = 'views/config/SettingsToolAsyncPage.vue'

test('① 列：工具 | 模式 | 转异步阈值 | 执行硬上限 | 涉及文件变动 | 操作', () => {
  const src = read(PAGE)
  assert.match(src, /const CFG_KEY = 'tool_async'/, 'usr 键须为 tool_async（零消息面变更）')
  assert.match(src, /prop: 'threshold'/, '须有转异步阈值列')
  assert.match(src, /prop: 'timeout'/, '须有执行硬上限列')
  assert.match(src, /prop: 'touch'/, '须有涉及文件变动列')
  assert.doesNotMatch(src, /#onTimeout=|#cancelTimeout=|prop: 'onTimeout'|prop: 'cancelTimeout'/, '旧两列应删除')
})

test('② 四模式：manual 保留，旧值仅读取映射', () => {
  const src = read(PAGE)
  assert.match(src, /const MODES = \['sync', 'async', 'auto', 'manual'\]/)
  const fn = src.match(/function normalizeUiMode\(m\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 normalizeUiMode')
  assert.match(fn[0], /v === 'never'\) return 'sync'/, 'never → sync')
  assert.match(fn[0], /v === 'always'\) return 'async'/, 'always → async')
  assert.match(fn[0], /return MODES.includes\(v\) \? v : 'auto'/, 'manual 等合法值原样保留（不折叠）')
})

test('③ 转异步阈值仅 auto/manual：sync/async 显示 — 且不可填', () => {
  const src = read(PAGE)
  const fn = src.match(/function isThresholdMode\(row\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 isThresholdMode')
  assert.match(fn[0], /row\.mode === 'auto' \|\| row\.mode === 'manual'/)
  assert.match(src, /v-if="isThresholdMode\(row\)"[\s\S]{0,600}<span v-else class="cell-dash">—<\/span>/, '非阈值模式须显示 —')
  // buildEntry：仅 isThresholdMode 时写入 threshold
  const be = src.match(/function buildEntry\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(be, '未找到 buildEntry')
  assert.match(be[0], /if \(isThresholdMode\(row\)\) \{[\s\S]{0,200}threshold/, '仅 auto/manual 写 threshold')
})

test('④ 执行硬上限：所有模式可填；仅 >0 写库（<=0 / 留空不处理）', () => {
  const src = read(PAGE)
  const be = src.match(/function buildEntry\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(be, '未找到 buildEntry')
  assert.match(be[0], /Number\.isFinite\(n\) && n > 0\) e\.hard_timeout = n/, '仅 >0 写 hard_timeout')
  // 输入未按模式禁用（dir 节点禁用逻辑已删除）
  assert.doesNotMatch(src, /:disabled="row\.dirNode === true"/, '所有模式均可填硬上限')
  assert.doesNotMatch(src, /row\.dirNode/, 'dir 节点硬上限禁用逻辑应删除')
  // 失焦：<=0 → 清显示（不写库）
  assert.match(src, /row\.hardTimeout = '' \/\/ <=0 = 不处理/, '<=0 清显示（不写库）')
})

test('⑤ 旧参数退役：页面 / i18n 不再引用 on_timeout / cancel_on_timeout', () => {
  const src = read(PAGE)
  assert.doesNotMatch(src, /on_timeout|cancel_on_timeout|onTimeout|cancelOnTimeout/)
  for (const loc of LOCALES) {
    const ta = readLocale(loc, 'config.json').toolAsync
    for (const k of ['onTimeout', 'onTimeoutFail', 'onTimeoutBackground', 'cancelOnTimeout',
      'cancelOnTimeoutHint', 'advancedHint']) {
      assert.equal(ta[k], undefined, `${loc} 旧键 config.toolAsync.${k} 应删除`)
    }
  }
})

test('⑥ i18n 新键 zh/en 齐备：四模式 + 转异步阈值 + 硬上限提示', () => {
  const zh = readLocale('zh-CN', 'config.json').toolAsync
  const en = readLocale('en-US', 'config.json').toolAsync
  assert.deepEqual(Object.keys(zh).sort(), Object.keys(en).sort(), 'zh/en 键集须一致')
  for (const k of ['modeSync', 'modeAsync', 'modeAuto', 'modeManual',
    'transferThreshold', 'transferThresholdHint', 'hardTimeout', 'hardTimeoutHint']) {
    assert.ok(typeof zh[k] === 'string' && zh[k].trim(), `zh 缺 ${k}`)
    assert.ok(typeof en[k] === 'string' && en[k].trim(), `en 缺 ${k}`)
  }
  assert.match(zh.modeHint, /自动异步/, 'zh modeHint 须覆盖四模式')
  assert.match(zh.modeHint, /手动异步/, 'zh modeHint 须覆盖手动异步')
  assert.match(zh.hardTimeoutHint, />0/, 'zh hardTimeoutHint 须写明 >0 取消')
  assert.match(zh.hardTimeoutHint, /<=0/, 'zh hardTimeoutHint 须写明 <=0 不处理')
})