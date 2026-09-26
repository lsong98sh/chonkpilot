/**
 * 「目录选择」接线单测（2026-09-19，browser 形态接服务端等价面 `GET /dirs`）。
 *
 * 覆盖：① browser 判据成立 → 走 `/dirs`（mock fetch）并产出层级视图模型；
 *      ② GUI/native 判据 → 走既有 native（**不发** `/dirs`）；
 *      ③ 确认选择后把路径交给调用方；
 *      ④ 服务端返回空/错误 → 明确提示（不静默）；
 *      ⑤ 只读与作用域（不得选 work_dir 之外）；
 *      ⑥ 接线守卫（.vue/composable 消费 `/dirs`，无 watch，无新增 MQ 主题）。
 *
 * 注：前端暂无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯逻辑直调 + `*.vue` 源码守卫（与 instanceScope / toolStopWiring 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  dispatchDirPick,
  buildDirLevels,
  childDirs,
  isWithinRoot,
  normPath,
} from '../src/utils/dirPicker.js'
import { fetchDirs } from '../src/api/dirs.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) =>
  JSON.parse(readFileSync(join(srcDir, 'locales', loc, name), 'utf8'))

// GET /dirs 等价场景的样例响应（服务端 filepath.ToSlash；根 depth 0）
const WORK = 'E:/TEMP/ck-dp/wd'
const SAMPLE = {
  instance_id: 'ins-x',
  work_dir: WORK,
  dirs: [
    { path: WORK, name: 'wd', depth: 0 },
    { path: WORK + '/docs', name: 'docs', depth: 1 },
    { path: WORK + '/docs/sub', name: 'sub', depth: 2 },
    { path: WORK + '/src', name: 'src', depth: 1 },
  ],
}

/** 临时替换 global.fetch；返回还原函数。 */
function mockFetch(impl) {
  const prev = globalThis.fetch
  globalThis.fetch = impl
  return () => { globalThis.fetch = prev }
}

test('① browser 判据成立 → 走 /dirs（mock fetch）并产出层级视图模型', async () => {
  let calledUrl = null
  const restore = mockFetch(async (url) => {
    calledUrl = url
    return { ok: true, status: 200, json: async () => SAMPLE }
  })
  try {
    const res = await dispatchDirPick({
      isBrowser: true,
      nativePick: () => { throw new Error('native 不应在 browser 形态被调用') },
      browserPick: () => fetchDirs(),
    })
    assert.equal(calledUrl, '/dirs', 'browser 形态必须请求 GET /dirs')
    assert.equal(res.workDir, WORK)

    // 根层 → 真实子目录（docs / src）
    const root = buildDirLevels(res.dirs, res.workDir, res.workDir)
    assert.deepEqual(root.children.map((c) => c.name), ['docs', 'src'])
    assert.equal(root.canUp, false)
    assert.deepEqual(root.crumbs.map((c) => c.name), ['wd'])

    // 进入 docs → 子目录 sub；面包屑 2 层
    const docs = buildDirLevels(res.dirs, res.workDir, WORK + '/docs')
    assert.deepEqual(docs.children.map((c) => c.name), ['sub'])
    assert.equal(docs.canUp, true)
    assert.deepEqual(docs.crumbs.map((c) => c.name), ['wd', 'docs'])
    assert.deepEqual(childDirs(res.dirs, WORK + '/docs/sub'), [])
  } finally {
    restore()
  }
})

test('② GUI/native 判据 → 走 native，且**不发** /dirs', async () => {
  let fetched = 0
  const restore = mockFetch(async () => { fetched++; return { ok: true, json: async () => SAMPLE } })
  try {
    const native = await dispatchDirPick({
      isBrowser: false,
      nativePick: () => Promise.resolve('E:/proj/other'),
      browserPick: () => fetchDirs(),
    })
    assert.equal(native, 'E:/proj/other')
    assert.equal(fetched, 0, 'native 形态不得请求 /dirs')
  } finally {
    restore()
  }
})

test('② 形态判据 = shim 标记 __ckHttpBridge（GUI 只注入 instance id，不加载 shim）', () => {
  const src = read('utils/runtimeForm.js')
  const body = src.match(/export function isBrowserForm\s*\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(body, '未找到 isBrowserForm')
  assert.match(body[0], /__ckHttpBridge === true/, 'browser 判据须为 __ckHttpBridge')
  assert.doesNotMatch(body[0], /__chonkpilotInstanceId/, '不得用两种形态都注入的 instance id 作判据')
  // 服务端 shim 确实置该标记（非 MQ；见 chonkpilot-llm/httpapi/ck_http_bridge.js）
  const shim = readFileSync(join(here, '..', '..', 'lib', 'llm', 'httpapi', 'ck_http_bridge.js'), 'utf8')
  assert.match(shim, /window\.__ckHttpBridge = true/)
})

test('③ 确认选择后把路径交给调用方（同一路径贯通 dispatch → 调用方）', async () => {
  const picked = await dispatchDirPick({
    isBrowser: true,
    nativePick: () => Promise.resolve(''),
    browserPick: () => Promise.resolve(WORK + '/docs'),
  })
  assert.equal(picked, WORK + '/docs')
  // 取消/未选 → ''
  const cancelled = await dispatchDirPick({
    isBrowser: true,
    nativePick: () => Promise.resolve(''),
    browserPick: () => Promise.resolve(''),
  })
  assert.equal(cancelled, '')
})

test('④ 服务端错误/坏响应 → fetchDirs 明确 reject（不静默）', async () => {
  let restore = mockFetch(async () => ({ ok: false, status: 500, json: async () => ({}) }))
  try {
    await assert.rejects(() => fetchDirs(), /HTTP 500/)
  } finally { restore() }

  restore = mockFetch(async () => ({ ok: true, status: 200, json: async () => { throw new Error('boom') } }))
  try {
    await assert.rejects(() => fetchDirs(), /bad json/)
  } finally { restore() }
})

test('④ 服务端返回空清单 → 层级为空（UI 据此提示「无子目录」）', async () => {
  const restore = mockFetch(async () => ({
    ok: true,
    status: 200,
    json: async () => ({ instance_id: 'ins', work_dir: WORK, dirs: [] }),
  }))
  try {
    const res = await fetchDirs()
    assert.deepEqual(res.dirs, [])
    const lv = buildDirLevels(res.dirs, res.workDir, res.workDir)
    assert.deepEqual(lv.children, [])
    // 空/错误两条明确提示的 i18n 键双语齐备
    for (const loc of ['zh-CN', 'en-US']) {
      const common = readLocale(loc, 'common.json')
      assert.ok(common.dir_picker_title, `${loc} 缺 common.dir_picker_title`)
      assert.ok(common.dir_picker_empty, `${loc} 缺 common.dir_picker_empty`)
      assert.ok(String(common.dir_picker_load_failed).includes('{error}'),
        `${loc} common.dir_picker_load_failed 应含 {error}`)
    }
  } finally {
    restore()
  }
})

test('⑤ 只读与作用域：work_dir 之外的当前层回落根；越界判定拒绝', () => {
  const outside = 'E:/TEMP/ck-dp/elsewhere'
  const lv = buildDirLevels(SAMPLE.dirs, WORK, outside)
  assert.equal(lv.current, WORK, '清单外路径必须回落到 work_dir')
  assert.deepEqual(lv.children.map((c) => c.name), ['docs', 'src'])
  assert.equal(isWithinRoot(WORK, WORK + '/docs'), true)
  assert.equal(isWithinRoot(WORK, outside), false)
  assert.equal(isWithinRoot(WORK, 'E:/TEMP/ck-dp/elsewhere-x'), false)
  // normPath 归一化（反斜杠/尾斜杠）
  assert.equal(normPath('E:\\TEMP\\ck-dp\\wd\\'), WORK)
})

test('⑥ 接线守卫：Toolbar/SecurityConfig 消费 pickDir，browser 分支不假成功', () => {
  const toolbar = read('views/toolbar/Toolbar.vue')
  assert.match(toolbar, /import\s*\{\s*useDirPicker\s*\}/, 'Toolbar 须引入 useDirPicker')
  assert.match(toolbar, /await pickDir\(\)/, 'Toolbar 打开目录须走 pickDir()')
  assert.doesNotMatch(toolbar, /openDirDialog/, 'Toolbar 不得再直调 native openDirDialog')
  assert.match(toolbar, /isBrowserForm\(\)/, 'Toolbar 须区分 browser/webview 形态')
  assert.match(toolbar, /open_dir_browser_unsupported/, 'browser 不可达动作须明确提示（不假成功）')

  const security = read('views/settings/SecurityConfig.vue')
  assert.match(security, /import\s*\{\s*useDirPicker\s*\}/, 'SecurityConfig 须引入 useDirPicker')
  assert.match(security, /await pickDir\(\)/, 'SecurityConfig 选目录须走 pickDir()')
  assert.doesNotMatch(security, /openDirDialog/, 'SecurityConfig 不得再直调 native openDirDialog')
})

test('⑥ 接线守卫：composable 走 dispatchDirPick（browser=/dirs, native=openDirDialog），无 watch', () => {
  const comp = read('composables/useDirPicker.js')
  assert.match(comp, /dispatchDirPick\(/, 'composable 须经 dispatchDirPick 分派')
  assert.match(comp, /isBrowserForm\(\)/, 'composable 须用形态判据')
  assert.match(comp, /openDirDialog\(\)/, 'native 分支须保留既有 gui.dir.open-dialog')
  assert.match(comp, /DirPickerDialog/, 'browser 分支须打开目录选择器')

  const vue = read('views/common/DirPickerDialog.vue')
  assert.match(vue, /fetchDirs\(\)/, '选择器数据源须为 GET /dirs')
  assert.match(vue, /emit\('confirm',\s*levels\.value\.current\)/, '确认须回传当前层路径')
  for (const rel of ['composables/useDirPicker.js', 'views/common/DirPickerDialog.vue']) {
    assert.doesNotMatch(read(rel), /\bwatch(Effect)?\s*\(/, `${rel} 不得使用 watch/watchEffect`)
  }
})

test('⑥ 消息面零变更：前端未新增/发送任何 gui.dir MQ 主题', () => {
  const files = [
    'composables/useDirPicker.js',
    'views/common/DirPickerDialog.vue',
    'api/dirs.js',
    'utils/dirPicker.js',
    'utils/runtimeForm.js',
  ]
  for (const rel of files) {
    const src = read(rel)
    assert.doesNotMatch(src, /mq\.emit\(/, `${rel} 不得发送 MQ 消息（61 为准）`)
    assert.doesNotMatch(src, /'gui\.dir|"gui\.dir/, `${rel} 不得以字符串直发 native 主题`)
  }
  // 既有 native 主题仍在 api/config.js 的 guiReq 中（行为不变）
  assert.match(read('api/config.js'), /guiReq\('dir\.open-dialog'/, 'native 主题须保持不变')
})
