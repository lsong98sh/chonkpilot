/**
 * 「目录选择器」纯逻辑 + 接线守卫单测（2026-10-10 更新：browser 形态改走服务端 `gui.show-dialog`，
 * 数据源 = options props；`GET /dirs` 与形态分派已删除 → 双形态共用 gui.select-dir 两段式）。
 *
 * 覆盖：① root → 子目录视图模型；② 逐级进入 + 面包屑 + 返回上级；③ 只读与作用域（清单外回落根）；
 *      ④ 空清单 → 空视图；⑤ 面包屑断链截断；⑥ 接线守卫（选择器 options 驱动、无 /dirs、无 watch、
 *      无字面量主题）；⑦ I-74（占用拒绝人话键 / 两个打开入口明确提示）；⑧ 语义分离（选目录不开窗口）。
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
  buildDirLevels,
  childDirs,
  breadcrumb,
  isWithinRoot,
  normPath,
  baseName,
} from '../src/utils/dirPicker.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) =>
  JSON.parse(readFileSync(join(srcDir, 'locales', loc, name), 'utf8'))

// gui.show-dialog options 等价场景样例（服务端 filepath.ToSlash；根 depth 0）
const ROOT = 'E:/TEMP/ck-dp/wd'
const OPTIONS = [
  { path: ROOT, name: 'wd', depth: 0 },
  { path: ROOT + '/docs', name: 'docs', depth: 1 },
  { path: ROOT + '/docs/sub', name: 'sub', depth: 2 },
  { path: ROOT + '/src', name: 'src', depth: 1 },
]

test('① root → 真实子目录（docs / src），根不可返回上级', () => {
  const root = buildDirLevels(OPTIONS, ROOT, ROOT)
  assert.deepEqual(root.children.map((c) => c.name), ['docs', 'src'])
  assert.equal(root.canUp, false)
  assert.deepEqual(root.crumbs.map((c) => c.name), ['wd'])
})

test('② 进入 docs → 子目录 sub；面包屑 2 层；返回上级到 root', () => {
  const docs = buildDirLevels(OPTIONS, ROOT, ROOT + '/docs')
  assert.deepEqual(docs.children.map((c) => c.name), ['sub'])
  assert.equal(docs.canUp, true)
  assert.deepEqual(docs.crumbs.map((c) => c.name), ['wd', 'docs'])
  assert.deepEqual(childDirs(OPTIONS, ROOT + '/docs/sub'), [])
})

test('③ 只读与作用域：清单外当前层回落根；越界判定拒绝；路径归一化', () => {
  const outside = 'E:/TEMP/ck-dp/elsewhere'
  const lv = buildDirLevels(OPTIONS, ROOT, outside)
  assert.equal(lv.current, ROOT, '清单外路径必须回落到 root')
  assert.deepEqual(lv.children.map((c) => c.name), ['docs', 'src'])
  assert.equal(isWithinRoot(ROOT, ROOT + '/docs'), true)
  assert.equal(isWithinRoot(ROOT, outside), false)
  assert.equal(isWithinRoot(ROOT, 'E:/TEMP/ck-dp/elsewhere-x'), false)
  // normPath 归一化（反斜杠 / 尾斜杠）与末级名兜底
  assert.equal(normPath('E:\\TEMP\\ck-dp\\wd\\'), ROOT)
  assert.equal(baseName(ROOT + '/docs'), 'docs')
})

test('④ 空清单 → 空视图（UI 据此提示「无子目录」）', () => {
  const lv = buildDirLevels([], '', '')
  assert.deepEqual(lv.children, [])
  assert.deepEqual(lv.crumbs, [])
  assert.equal(lv.current, '')
  // 空提示 i18n 键双语齐备
  for (const loc of ['zh-CN', 'en-US']) {
    const common = readLocale(loc, 'common.json')
    assert.ok(common.dir_picker_title, `${loc} 缺 common.dir_picker_title`)
    assert.ok(common.dir_picker_empty, `${loc} 缺 common.dir_picker_empty`)
  }
})

test('⑤ 面包屑断链截断：current 不在清单内层级不全时只收已认可的层', () => {
  // current = docs/sub（在清单内）→ 面包屑 wd/docs/sub
  assert.deepEqual(breadcrumb(OPTIONS, ROOT, ROOT + '/docs/sub').map((c) => c.name), ['wd', 'docs', 'sub'])
  // current 越界 → 空面包屑
  assert.deepEqual(breadcrumb(OPTIONS, ROOT, 'E:/TEMP/ck-dp/elsewhere'), [])
})

test('⑥ 接线守卫：DirPickerDialog 由 options 驱动、不再拉取 /dirs、无 watch', () => {
  const vue = read('views/common/DirPickerDialog.vue')
  assert.match(vue, /props\.options/, '选择器视图须取自 props.options（gui.show-dialog 下发）')
  assert.match(vue, /buildDirLevels\(props\.options/, '须用 options 构造层级视图')
  assert.match(vue, /emit\('confirm',\s*levels\.value\.current\)/, '确认须回传当前层路径')
  assert.doesNotMatch(vue, /fetchDirs|\/dirs/, '不得再拉取 GET /dirs（数据源 = 服务端下发 options）')
  assert.doesNotMatch(vue, /\bwatch(Effect)?\s*\(/, '不得使用 watch/watchEffect')

  // 全仓前端（除 msgkeys/事件定义）不得再出现 /dirs 或已删除的 api/dirs
  const asyncSrc = read('utils/dirPicker.js')
  assert.doesNotMatch(asyncSrc, /dispatchDirPick/, '形态分派已删除（双形态共用 gui.select-dir）')
  assert.doesNotMatch(read('composables/useDirPicker.js'), /DirPickerDialog|isBrowserForm|dispatchDirPick/,
    'useDirPicker 已收敛为两段式，不再有 browser 分支')
})

test('⑥ 接线守卫：useDirPicker 走两段式 gui.select-dir（openDirDialog(widgetId)），无 watch', () => {
  const comp = read('composables/useDirPicker.js')
  assert.match(comp, /openDirDialog\(widgetId\)/, '须走两段式 gui.select-dir（api/config.openDirDialog）')
  assert.match(comp, /if \(inflight\) return inflight/, '防二次点击：复用在飞 Promise')
  assert.doesNotMatch(comp, /\bwatch(Effect)?\s*\(/, '不得使用 watch/watchEffect')
  assert.doesNotMatch(comp, /mq\.emit\(/, '不得直接发送 MQ 消息（统一经 utils/guiAsync.js）')
})

test('⑥ browser 形态已删（PRODSIM-1）：无运行时形态判据、无 http shim', () => {
  assert.throws(() => read('utils/runtimeForm.js'), 'utils/runtimeForm.js 应已删除')
  assert.throws(() => readFileSync(join(here, '..', '..', 'lib', 'llm', 'httpapi', 'ck_http_bridge.js'), 'utf8'),
    'browser 桥 shim 应已删除')
})

// ── I-74：同 work-dir 单实例占用 → 「选择即报错」（明确提示，不再静默）──────
test('⑦ I-74：占用拒绝串归类为人话键 + i18n 双语齐备', async () => {
  const { classifyError } = await import('../src/utils/errorMessage.js')
  const cls = classifyError('dir.open: workdir busy: E:/proj')
  assert.equal(cls.key, 'chat.error_workdir_busy', '占用拒绝串须命中专用人话键')
  assert.equal(cls.detail, 'dir.open: workdir busy: E:/proj', '原始串须原样保留')

  for (const loc of ['zh-CN', 'en-US']) {
    const chat = readLocale(loc, 'chat.json')
    assert.ok(typeof chat.error_workdir_busy === 'string' && chat.error_workdir_busy.trim(),
      `${loc} 缺 chat.error_workdir_busy`)
  }
})

test('⑦ I-74：两个「打开目录」入口（工具栏按钮 / 最近目录）占用拒绝时明确提示', () => {
  const wd = read('layout/workdirActions.js')
  assert.match(wd, /path = await pickDir\(\)/, 'Open 须先走纯选择取路径')
  assert.match(wd, /await openDir\(path\)/, '须经 gui.dir.open 打开目录（native 起新窗口）')
  assert.match(wd, /export async function openDirAndRefresh/, '须有统一「打开目录」实现')
  assert.match(wd, /classifyError\(/, '打开入口须归类错误（占用 / 越界）')
  const recent = read('layout/bar-views/RecentDirsView.vue')
  assert.match(recent, /openDirAndRefresh\(dir\)/, '最近目录入口须走统一「打开目录」实现')
  // PRODSIM-1：目标项目在**新窗口**呈现，本窗口不切换项目 → 不再就地刷新（reinitProjectState 已移出）。
  assert.doesNotMatch(wd, /window\.location\.reload/, '不得整页重载')
  assert.doesNotMatch(wd, /setPendingWorkDir|takePendingWorkDir/, '重载暂存机制已删除')
  assert.doesNotMatch(wd, /open_dir_browser_unsupported/, '已删除「browser 不支持」早退分支')
  assert.doesNotMatch(wd, /openInBrowser/, '打开目录不再由前端自行 instance-claim（收敛到 gui.dir.open）')
  assert.ok((wd.match(/message\.error\(/g) || []).length >= 1, '打开入口须明确提示（不静默，统一实现）')
})

test('⑨ 「打开目录」接线：gui.dir.open{path}（开新项目窗口）+ App 项目重初始化序列', () => {
  // api：openDir → gui.dir.open{path}（契约主题不变）
  const api = read('api/config.js')
  assert.match(api, /export function openDir\(path\)\s*\{[\s\S]*?guiReq\('dir\.open',\s*\{\s*path\s*\}\)/,
    'api.openDir 须经 gui.dir.open{path}')

  // 就地刷新：App 以 projectEpoch 重挂主布局（整树重建）+ 注册重初始化序列（清缓存 → 重读活动会话）。
  const app = read('App.vue')
  assert.match(app, /:key="projectEpoch"/, '主布局须以项目纪元为 key（切换即整树重挂载）')
  assert.match(app, /setProjectReinitHandler\(/, 'App 须注册就地重初始化序列')
  assert.match(app, /invalidateInitDataCache\(\)/, '重初始化须清 init-data 单飞缓存（重取新项目快照）')
  const reinitRaw = read('utils/projectReinit.js')
  assert.match(reinitRaw, /projectEpoch\.value\s*\+=\s*1/, 'reinitProjectState 须自增项目纪元')
  // 去注释后再守（头注释里引用旧行为 `window.location.reload()`，不算真实调用）
  const reinit = reinitRaw.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^[ \t]*\/\/.*$/gm, '')
  assert.doesNotMatch(reinit, /window\.location\.reload/, '不得整页重载（需真实调用；注释引用不算）')
  // 重载暂存机制（setPendingWorkDir/takePendingWorkDir + 登录视图自动认领）已随重载一并删除。
  assert.doesNotMatch(read('utils/instanceState.js'), /setPendingWorkDir|takePendingWorkDir|pending-workdir/,
    '重载暂存机制已删除')
})

test('⑧ 语义分离：选目录 = 纯选择（不开窗口）；开窗口只经 gui.dir.open', () => {
  const local = readFileSync(join(here, '..', '..', 'lib', 'gui', 'bridge', 'local.go'), 'utf8')
  const dlg = local.slice(local.indexOf('// callOpenDirDialog'), local.indexOf('// callPickExecutable'))
  assert.ok(dlg.length > 0, '未找到 callOpenDirDialog')
  assert.doesNotMatch(dlg, /openNewWindow|recordRecentDir/, '纯选择入口不得开窗口 / 记最近目录')
  const openFn = local.slice(local.indexOf('func callOpenDir('), local.indexOf('// callOpenDirDialog'))
  assert.match(openFn, /b\.projectOpener\(dir\)/, 'gui.dir.open 须经宿主注入的 opener 同进程开/激活项目主窗口')
  assert.doesNotMatch(openFn, /openNewWindow|exec\.Command/, 'PRODSIM-1：不再 spawn 新进程（唯一产物 = 同进程多窗口）')

  // 消费方：信任目录只取路径，不得自行触发 gui.dir.open
  for (const [name, rel] of [['SecurityConfig', 'views/settings/SecurityConfig.vue']]) {
    assert.doesNotMatch(read(rel), /openDir\(/, `${name} 只应取路径，不得触发 gui.dir.open`)
  }
})
