/**
 * 状态栏「记忆总 token 数」入口（P1-1，2026-09-24 迁移）+ B2「打开全部配置」图标移除（P1-2）前端守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = 纯逻辑直调
 * （useMemoryCategories 的共享口径）+ `*.vue` 源码守卫（与 toolStopWiring / configSurface 同法）。
 * 真机行为（显示数值 → 点击弹分类列表 → 选中开内容弹框）由 L4 `test_statusbar.py::SB2b` 覆盖。
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

test('MemoryTotal：状态栏显示总量入口（.sb-mem）并复用共享 composable', () => {
  const statusbar = read('views/statusbar/StatusBar.vue')
  assert.match(statusbar, /sb-section sb-mem/, '状态栏应有记忆总量入口（.sb-mem）')
  assert.match(statusbar, /useMemoryCategories/, '应复用共享 composable（与上下文管理页同源）')
  assert.match(statusbar, /openCategoryList/, '点击应打开记忆分类列表')
  assert.match(statusbar, /statusBar\.memoryTotalLabel/, '应显示 i18n 标签')
})

test('MemoryTotal：B2「打开全部配置」图标与菜单已移除（无死代码）', () => {
  const statusbar = read('views/statusbar/StatusBar.vue')
  assert.doesNotMatch(statusbar, /configMenuItems|utils\/configMenu/, '状态栏不得再引用配置菜单清单')
  assert.doesNotMatch(statusbar, /sb-menu|Popover/, '状态栏不得再渲染配置菜单/弹层')
  assert.doesNotMatch(statusbar, /openGlobalConfig/, '不得再引用已移除的 i18n 键')
  for (const loc of LOCALES) {
    const sb = readLocale(loc, 'statusBar.json')
    assert.equal(sb.openGlobalConfig, undefined, loc + ' 应回收 openGlobalConfig 文案')
    assert.ok(sb.memoryTotal && sb.memoryTotalLabel, loc + ' 应有 memoryTotal / memoryTotalLabel 文案')
  }
  // configMenu.js 仍被工具栏「设置」下拉复用 → 保留（不许删除后留下断链引用）
  const toolbar = read('views/toolbar/Toolbar.vue')
  assert.match(toolbar, /configMenuItems/, '工具栏仍复用配置菜单清单')
  assert.ok(read('utils/configMenu.js').length > 0, 'utils/configMenu.js 仍在（工具栏在用）')
})

test('MemoryTotal：上下文管理页仅保留只读总量（不留重复入口）', () => {
  const ctx = read('views/settings/ContextConfig.vue')
  assert.doesNotMatch(ctx, /@click="openCategoryList"/, '上下文管理页不得再挂点击入口')
  assert.doesNotMatch(ctx, /MemoryCategoryListDialog/, '分类列表弹框逻辑应整体迁至 composable')
  assert.match(ctx, /useMemoryCategories/, '应复用共享 composable')
  assert.match(ctx, /mem-total-value/, '仍只读展示总量')
})

test('MemoryTotal：composable 只读配置门控 + 编辑器读写口径', () => {
  const comp = read('composables/useMemoryCategories.js')
  assert.match(comp, /dataClient\.list\('memory'\)/, '取清单走既有 data-memory-list')
  assert.match(comp, /dataRequest\('memory', 'read'/, '读内容走既有 data-memory-read')
  assert.match(comp, /dataClient\.save\('memory'/, '保存走既有 data-memory-save')
  assert.match(comp, /if \(!enabled\.value\)/, '记忆库关闭时不得发 data-memory-list')
  assert.match(comp, /const total = computed/, '应汇总 token 总量')
})
