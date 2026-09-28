/**
 * 「涉及文件变动」per-tool 选项（usr 键 `tool_async.touch_files`）—— 纯逻辑 + 页面/i18n 源码守护
 * （2026-09-28）。
 *
 * 契约（与后端同源，改一处须同步另一处）：
 *   - 缺省：**self 内置工具**仅 `filesys_run` / `script_run` 涉及（true），其余内置不涉及（false）；
 *     **dir 节点 / 第三方 / 无法判定**保守按涉及（true）；
 *   - 显式 `touch_files` 优先；与缺省一致的项**不写库**；
 *   - 派生「打点 / 不打点」标记（打点 = 涉及）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 纯逻辑直调 + `*.vue`/locale 源码守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  SELF_NODE_ID, DEFAULT_TOUCH_TOOLS,
  defaultTouchFiles, effectiveTouchFiles, checkpointEnabled, touchFilesOverride,
} from '../src/utils/toolTouchFiles.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

const PAGE = 'views/config/SettingsToolAsyncPage.vue'
const SELF = { alias: 'self', node: 'self', category: 'core' }
const DIRN = { alias: 'mydir', node: 'mydir', category: 'dir' }
const THIRD = { alias: 'github', node: 'github', category: 'external' }

// ═══════════════════════════════════════════════════════════════
// ① 纯逻辑：缺省映射（self 白名单 / 保守按涉及）
// ═══════════════════════════════════════════════════════════════
test('① 常量：self 节点标识 + 白名单 = filesys_run / script_run', () => {
  assert.equal(SELF_NODE_ID, 'self')
  assert.deepEqual(DEFAULT_TOUCH_TOOLS, ['filesys_run', 'script_run'])
})

test('① defaultTouchFiles：self 内置仅白名单涉及；dir 节点/第三方/无法判定 → 保守涉及', () => {
  // self 内置：白名单内（含暴露名前缀剥离）
  assert.equal(defaultTouchFiles('self_filesys_run', SELF), true)
  assert.equal(defaultTouchFiles('self_script_run', SELF), true)
  assert.equal(defaultTouchFiles('filesys_run', SELF), true, '无前缀（契约名）亦识别')
  // self 内置：其余不涉及
  for (const n of ['file_read', 'file_find', 'file_diff', 'web_fetch', 'browser_run', 'desktop_run']) {
    assert.equal(defaultTouchFiles('self_' + n, SELF), false, `self ${n} 应不涉及`)
  }
  // dir 节点 / 第三方 / 无法判定 → 保守涉及
  assert.equal(defaultTouchFiles('mydir_run', DIRN), true)
  assert.equal(defaultTouchFiles('github_create_issue', THIRD), true)
  assert.equal(defaultTouchFiles('anything', null), true)
  assert.equal(defaultTouchFiles('anything', undefined), true)
  // `alias=self` / `node=self` 任一命中即视为 self（与 toolSource 同口径）
  assert.equal(defaultTouchFiles('self_file_read', { node: 'self' }), false)
  assert.equal(defaultTouchFiles('self_file_read', { alias: 'self' }), false)
})

test('① effectiveTouchFiles / checkpointEnabled：显式配置优先于缺省', () => {
  assert.equal(effectiveTouchFiles({ mode: 'never' }, 'self_file_read', SELF), false, '未配 touch_files → 缺省')
  assert.equal(effectiveTouchFiles({ touch_files: true }, 'self_file_read', SELF), true, '显式 true 覆盖缺省')
  assert.equal(effectiveTouchFiles({ touch_files: false }, 'self_filesys_run', SELF), false, '显式 false 覆盖缺省')
  assert.equal(effectiveTouchFiles(null, 'github_x', THIRD), true)
  // 打点派生 = 生效值（true = 打点）
  assert.equal(checkpointEnabled({ touch_files: true }, 'self_file_read', SELF), true)
  assert.equal(checkpointEnabled({ touch_files: false }, 'self_filesys_run', SELF), false)
  assert.equal(checkpointEnabled(null, 'self_filesys_run', SELF), true)
})

test('① touchFilesOverride：与缺省一致 → undefined（不写库）；偏离 → 布尔值', () => {
  // self 内置不涉及工具：显式 true（偏离缺省 false）→ 写库
  assert.equal(touchFilesOverride(true, 'self_file_read', SELF), true)
  assert.equal(touchFilesOverride(false, 'self_file_read', SELF), undefined, '与缺省一致不写库')
  // 涉及工具：缺省 true → 显式 false 写库、true 不写库
  assert.equal(touchFilesOverride(false, 'self_filesys_run', SELF), false)
  assert.equal(touchFilesOverride(true, 'self_filesys_run', SELF), undefined)
  // 第三方缺省 true → 显式 false 写库
  assert.equal(touchFilesOverride(false, 'github_x', THIRD), false)
  assert.equal(touchFilesOverride(true, 'github_x', THIRD), undefined)
})

// ═══════════════════════════════════════════════════════════════
// ② 页面：列 / 开关 / 派生标记 / 落库字段
// ═══════════════════════════════════════════════════════════════
test('② 页面：新增「涉及文件变动」列（表头 ? 说明 + 开关 + 派生打点标记）', () => {
  const src = read(PAGE)
  assert.match(src, /import \{[^}]*\bSwitch\b[^}]*\} from '\.\.\/\.\.\/components\/ui'/,
    '须引入 Switch 组件')
  assert.match(src, /import \{[^}]*defaultTouchFiles[^}]*\} from '\.\.\/\.\.\/utils\/toolTouchFiles'/,
    '须引用 utils/toolTouchFiles')
  // 列定义 + 表头/单元格模板
  assert.match(src, /\{ label: t\('config\.toolAsync\.touchFiles'\), prop: 'touch'/,
    '须新增 touch 列（label = config.toolAsync.touchFiles）')
  assert.match(src, /<template #header-touch>/, '表头须带 ? 说明槽（#header-touch）')
  assert.match(src, /<template #touch="\{ row \}">/, '须有 #touch 单元格模板')
  assert.match(src, /cell-touch/, '须有单元格容器 .cell-touch')
  assert.match(src, /touch-badge/, '须有派生标记 .touch-badge')
  assert.match(src, /\$t\('config\.toolAsync\.checkpointOn'\)/, '标记=打点 文案键')
  assert.match(src, /\$t\('config\.toolAsync\.checkpointOff'\)/, '标记=不打点 文案键')
  // 开关绑定 + 变更处理（只改本地待保存态）
  assert.match(src, /@update:model-value="\(v\) => onTouchChange\(row, v\)"/, '开关须走 onTouchChange')
  const fn = src.match(/function onTouchChange\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 onTouchChange')
  assert.match(fn[0], /row\.touch = v/, 'onTouchChange 须更新 row.touch')
  assert.match(fn[0], /syncRow\(row\)/, 'onTouchChange 须同步待保存态（不立即落库）')
})

test('② 页面：buildEntry 仅在与缺省不一致时写 touch_files；行缺省/恢复默认均回落缺省', () => {
  const src = read(PAGE)
  const be = src.match(/function buildEntry\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(be, '未找到 buildEntry')
  assert.match(be[0], /touchFilesOverride\(row\.touch, row\.name, row\.server\)/, '须用 touchFilesOverride 判定是否写库')
  assert.match(be[0], /if \(tf !== undefined\) e\.touch_files = tf/, '仅偏离缺省时写入 touch_files')
  // 待保存态与已落库态比较须含 touch_files（否则改开关不触发 dirty）
  const canon = src.match(/function canon\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(canon, '未找到 canon')
  assert.match(canon[0], /touch_files: e\.touch_files/, 'dirty 比较须含 touch_files')
  // 行初始化 + 恢复默认回落缺省
  assert.match(src, /touch: defaultTouchFiles\(tl\.name, srv\)/, '行初始化须给 touch 缺省值')
  assert.match(src, /row\.touch = defaultTouchFiles\(row\.name, row\.server\)/, '恢复默认须回落缺省')
  assert.match(src, /row\.touch = effectiveTouchFiles\(u, row\.name, row\.server\)/, '回填须用生效值')
})

// ═══════════════════════════════════════════════════════════════
// ③ i18n：页签改名 + 新键 zh/en 齐备
// ═══════════════════════════════════════════════════════════════
test('③ i18n：页签 label 由「工具异步配置」改为「工具配置」', () => {
  const zh = readLocale('zh-CN', 'config.json')
  const en = readLocale('en-US', 'config.json')
  assert.equal(zh.page.toolAsync, '工具配置', 'zh 页签名 = 工具配置')
  assert.equal(en.page.toolAsync, 'Tool Configuration', 'en 页签名 = Tool Configuration')
  for (const loc of LOCALES) {
    const cfg = readLocale(loc, 'config.json')
    assert.doesNotMatch(cfg.page.toolAsync, /异步|Async/i, `${loc} 页签名不应再含「异步/Async」`)
  }
})

test('③ i18n：toolAsync 新键 zh/en 齐备且非空（含 hint 的「粒度/安全」口径）', () => {
  const zh = readLocale('zh-CN', 'config.json').toolAsync
  const en = readLocale('en-US', 'config.json').toolAsync
  assert.deepEqual(Object.keys(zh).sort(), Object.keys(en).sort(), 'zh/en toolAsync 键集须一致')
  for (const k of ['touchFiles', 'touchFilesHint', 'checkpointOn', 'checkpointOff']) {
    assert.ok(typeof zh[k] === 'string' && zh[k].trim().length > 0, `zh 缺 ${k}`)
    assert.ok(typeof en[k] === 'string' && en[k].trim().length > 0, `en 缺 ${k}`)
  }
  // hint 须写明「粒度变粗 / 不丢安全」口径
  assert.match(zh.touchFilesHint, /粒度变粗/, 'zh hint 须说明「粒度变粗」')
  assert.match(zh.touchFilesHint, /不丢安全/, 'zh hint 须说明「不丢安全」')
  assert.match(zh.touchFilesHint, /轮末补点/, 'zh hint 须说明轮末补点保留')
  assert.match(en.touchFilesHint, /granularity/i, 'en hint 须说明 granularity')
  assert.match(en.touchFilesHint, /safety is not lost/i, 'en hint 须说明 safety is not lost')
})
