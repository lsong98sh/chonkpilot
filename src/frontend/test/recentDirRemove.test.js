/**
 * 「最近项目」下拉删除记录单测（2026-10-05）。
 *
 * 需求：下拉中每条最近项增加删除入口，**只删除该条记录**（`gui.recent.remove`），
 * **绝不删除对应项目目录 / `.chonkpilot` 数据资产**；删除后即时刷新下拉；不影响当前已打开项目。
 *
 * 覆盖：① 消息面（`gui.recent.remove`）与前端内部事件（`recent-dir-remove`）接线；
 *      ② Toolbar 删除入口走 `v-mq` + `.stop`（不误触发「打开」）；
 *      ③ 删除处理器只调 `removeRecentDir` + `loadRecentDirs`（无 openDir / 无资产删除）；
 *      ④ i18n 双语键齐备；⑤ 无 `watch`/`watchEffect`。
 *
 * 注：前端 `npm test` = node:test 直跑，无组件级渲染器 → 纯逻辑直调 + `*.vue` 源码守卫
 * （与 dirPicker / toolStopWiring 同法）；「只删记录不删资产」的真值另有 Go 侧 bridge 契约测试。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) =>
  JSON.parse(readFileSync(join(srcDir, 'locales', loc, name), 'utf8'))

test('① 消息面/事件：gui.recent.remove + 前端内部事件 recent-dir-remove 已登记', () => {
  const events = read('events/event-names.js')
  assert.match(events, /recentDirRemove:\s*'recent-dir-remove'/, 'event-names 须登记 recent-dir-remove')

  const api = read('api/config.js')
  assert.match(api, /export function removeRecentDir\(path\)/, 'api 须导出 removeRecentDir')
  assert.match(api, /guiReq\('recent\.remove',\s*\{\s*path\s*\}\s*\)/, 'api 须经 gui.recent.remove 删记录')
})

test('② Toolbar 删除入口：v-mq 触发 + .stop（不误触发打开项目）', () => {
  const toolbar = read('views/toolbar/Toolbar.vue')
  assert.match(toolbar, /class="recent-remove"/, '最近项须有删除按钮')
  assert.match(
    toolbar,
    /v-mq:\[EventNames\.recentDirRemove\]\.click\.stop="\{ dir: dir \}"/,
    '删除入口须走 v-mq 且 .stop 阻止冒泡（否则同时触发打开项目）',
  )
  assert.match(
    toolbar,
    /import\s*\{[^}]*removeRecentDir[^}]*\}\s*from\s*'\.\.\/\.\.\/api\/config'/,
    'Toolbar 须引入 removeRecentDir',
  )
  assert.match(toolbar, /mq\.on\(EventNames\.recentDirRemove/, 'Toolbar 须订阅 recent-dir-remove')
})

test('③ 删除处理器：只删记录（removeRecentDir + loadRecentDirs），无打开/资产删除动作', () => {
  const toolbar = read('views/toolbar/Toolbar.vue')
  const body = toolbar.match(/async function handleRecentDirRemove\s*\(dir\)\s*\{[\s\S]*?\n\}/)
  assert.ok(body, '未找到 handleRecentDirRemove')
  assert.match(body[0], /await removeRecentDir\(dir\)/, '须调用 removeRecentDir 删记录')
  assert.match(body[0], /await loadRecentDirs\(\)/, '删除后须重读列表即时刷新下拉')
  assert.doesNotMatch(
    body[0],
    /openDir\(|filesys\.remove|dataClient\.remove|removeFile|rmdir|unlink|RemoveAll/i,
    '删除记录不得触发打开项目 / 删除文件或数据面实体（只删记录，不删资产）',
  )
})

test('③ api.removeRecentDir 不得走数据面删除（那会删资产）', () => {
  const api = read('api/config.js')
  const fn = api.match(/export function removeRecentDir\(path\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 removeRecentDir 实现')
  assert.doesNotMatch(fn[0], /dataClient\.remove|filesys|removeFile|rmdir|unlink/i,
    'removeRecentDir 只允许经 gui.recent.remove，禁止数据面/文件删除')
})

test('④ i18n：删除入口文案 zh-CN / en-US 齐备', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const toolbar = readLocale(loc, 'toolbar.json')
    assert.ok(toolbar.remove_recent_dir, `${loc} 缺 toolbar.remove_recent_dir`)
  }
})

test('⑤ 无 watch / watchEffect（前端规则）', () => {
  assert.doesNotMatch(read('views/toolbar/Toolbar.vue'), /\bwatch(Effect)?\s*\(/,
    'Toolbar.vue 不得使用 watch/watchEffect')
})
