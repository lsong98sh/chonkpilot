/**
 * 左侧资源面板「项目记忆」模式（第 4 页签，2026-09-26）前端守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = `*.vue` / composable 源码守卫
 * （与 sessionNavCopy / statusbarMemoryEntry 同法）。
 *
 * 覆盖：
 *  1) ExplorerPane 第 4 页签「项目记忆」（位于知识库之后）+ MemoryPane 内容体
 *  2) useMemoryTree 数据流：零新增消息面（data-memory-list / data-memory-read / file-open）
 *  3) 两类条目的查看实现：项目级 → file-open；「用户偏好」(user 级) → data-memory-read + 只读弹框
 *  4) 记忆库总开关关闭 → 不请求 data-memory-list + 空态提示 + 跳转「上下文管理」
 *
 * 真机行为由 L4 `run_memory_tree.py` 覆盖。
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

test('项目记忆：MemoryPane 数据源零新增消息（复用 data-memory-list / read / file-open）', () => {
  const pane = read('views/filetree/MemoryPane.vue')
  assert.match(pane, /useMemoryTree/, 'MemoryPane 逻辑应封装到 useMemoryTree composable')
  assert.match(pane, /onDataRefresh\('memory'/, '应订阅 data-memory-refresh 自动刷新')
  const comp = read('composables/useMemoryTree.js')
  assert.match(comp, /useMemoryCategories\(\)/, '应复用记忆类别共享状态（沿用关闭态不发 list 的门控）')
  assert.match(comp, /await load\(\)/, '取清单经共享 composable（data-memory-list）')
  assert.doesNotMatch(comp, /dataClient\.list\('memory'\)/, '不得绕过共享 composable 自取清单')
  assert.match(comp, /dataRequest\('memory', 'read'/, '读内容走既有 data-memory-read')
  assert.match(comp, /EventNames\.fileOpen/, '项目级条目走既有 file-open')
})

test('项目记忆：类别启用按 prj 键 memory.category.<类别名>（缺省启用）过滤', () => {
  const comp = read('composables/useMemoryTree.js')
  assert.match(comp, /memory\.category\./, '应读 prj 类别启用键')
  assert.match(comp, /v === undefined \|\| v === '' \? true/, '缺键/空值 → 默认启用')
  assert.match(comp, /c => categoryEnabled\(c\.category\)/, '清单按启用态过滤')
})

test('项目记忆：项目级 → file-open（temporary）；用户偏好(user 级) → 只读弹框', () => {
  const comp = read('composables/useMemoryTree.js')
  assert.match(comp, /mq\.emit\(EventNames\.fileOpen, \{ path: item\.path, temporary: true \}\)/,
    '项目级条目应走 file-open + 临时页签')
  assert.match(comp, /item\.level === 'user'/, 'user 级须分流（不试图绕过 filesys 越界校验）')
  assert.match(comp, /MemoryViewDialog/, 'user 级走只读弹框')
  const view = read('components/common/MemoryViewDialog.vue')
  assert.match(view, /<pre class="mem-view-text">/, '只读弹框应展示全文（无编辑/保存）')
  assert.doesNotMatch(view, /<textarea|onSave/, '只读弹框不得含编辑/保存入口')
})

test('项目记忆：关闭态不发 data-memory-list + 空态提示 + 跳转上下文管理', () => {
  const pane = read('views/filetree/MemoryPane.vue')
  // 空态分支仅记忆库关闭时出现（提示 + 跳转）
  assert.match(pane, /v-if="!enabled"[\s\S]{0,400}memory_disabled/, '关闭 → 空态提示「记忆库未启用」')
  assert.match(pane, /fileTree\.memory_open_settings/, '关闭 → 提供跳转入口')
  assert.match(pane, /EventNames\.previewTabOpen[\s\S]{0,60}settings-project/,
    '跳转走既有 previewTabOpen（项目配置页含「上下文管理」）')
  // 门控在共享 composable（关闭态 load() 直接返回，不发 data-memory-list）
  const comp = read('composables/useMemoryCategories.js')
  assert.match(comp, /if \(!enabled\.value\)/, '记忆库关闭时 load() 不发 data-memory-list')
  for (const loc of LOCALES) {
    const ft = readLocale(loc, 'fileTree.json')
    assert.ok(ft.mode_memory && ft.memory_empty && ft.memory_disabled
      && ft.memory_disabled_hint && ft.memory_open_settings, loc + ' 缺少项目记忆文案')
  }
})
