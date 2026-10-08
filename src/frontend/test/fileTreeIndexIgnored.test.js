/**
 * 文件树「被索引排除条目灰显」前端守卫（2026-09-27）。
 *
 * 覆盖：
 *  ① 纯逻辑 `src/utils/indexIgnored.js`：workdir 相对路径换算；`data-index-ignored` 结果 →
 *     被排除集合（**仅 enabled=true 引擎、两引擎并集**）；请求 = 一次批量 + 去重、topic 正确、
 *     `paths` 为数组；**失败 / 主题不可用 / 应答异常 / enabled=false / ignored 空 → 空集**
 *     （静默降级，不灰显）；`truncated` 透传（调用方据此不缓存）；
 *  ② 源码守卫：`FileTree.vue` 按批判定（`judgeLoadedNodes`）在树数据到手 / 每层目录加载后 /
 *     变更合并后触发；订阅**既有** `onDataRefresh('prj-config')` 重判；无 `watch`/`watchEffect`；
 *  ③ 源码守卫：`TreeNode.vue` 命中节点带 `is-ignored`，样式 = 文本 `--fg-disabled`（**只改颜色**：
 *     仍保留既有点击 / 双击 / 右键 / 拖拽交互），原生 `title` 取 i18n；
 *  ④ 零新增 MQ 主题：`event-names.js` 不得出现索引排除相关主题（唯一使用者 = 已批准的
 *     `data-index-ignored`，字面量只在 `utils/indexIgnored.js`）；
 *  ⑤ i18n：新增键 zh/en 齐备，既有键不丢失。
 *
 * 真机（真实引擎开关 + `.gitignore` 生效 → DOM 灰显 / 灰显色）由 L4 `run_filetree_ignored.py` 覆盖。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  INDEX_IGNORED_TOPIC,
  toWorkdirRelPath,
  ignoredSetFromResult,
  fetchIndexIgnored,
} from '../src/utils/indexIgnored.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

// ── ① 纯逻辑：workdir 相对路径换算 ─────────────────────────────

test('toWorkdirRelPath：workdir 内 → 相对；越界 / 空 / 反斜杠归一', () => {
  const wd = 'E:/repo'
  assert.equal(toWorkdirRelPath('E:/repo/src/a.go', wd), 'src/a.go')
  assert.equal(toWorkdirRelPath('E:/repo/a.gen.go', wd), 'a.gen.go')
  // 反斜杠归一（Windows）
  assert.equal(toWorkdirRelPath('E:\\repo\\src\\a.go', wd), 'src/a.go')
  // workdir 尾斜杠容忍
  assert.equal(toWorkdirRelPath('E:/repo/src/a.go', 'E:/repo/'), 'src/a.go')
  // 盘符大小写差异不计
  assert.equal(toWorkdirRelPath('e:/repo/x.go', wd), 'x.go')
  // 越界 / 空 / 等于 workdir 本身 → ''
  assert.equal(toWorkdirRelPath('E:/other/x.go', wd), '')
  assert.equal(toWorkdirRelPath('', wd), '')
  assert.equal(toWorkdirRelPath(wd, wd), '')
  assert.equal(toWorkdirRelPath('E:/repoX/a.go', wd), '')
  assert.equal(toWorkdirRelPath(undefined, wd), '')
  assert.equal(toWorkdirRelPath('E:/repo/a.go', ''), '')
})

// ── ① 纯逻辑：结果 → 被排除集合 ───────────────────────────────

test('ignoredSetFromResult：仅 enabled=true 引擎、两引擎并集', () => {
  const s = ignoredSetFromResult({
    codegraph: { enabled: true, ignored: ['a.gen.go', 'sub/x.go'] },
    vfts: { enabled: true, ignored: ['a.gen.go', 'vdir/v.go'] },
  })
  assert.deepEqual([...s].sort(), ['a.gen.go', 'sub/x.go', 'vdir/v.go'])
})

test('ignoredSetFromResult：引擎未启用 / 空 / 异常 → 不产生灰显', () => {
  // enabled=false → 其 ignored 不参与（即便带内容也不灰）
  assert.equal(ignoredSetFromResult({ codegraph: { enabled: false, ignored: ['a.gen.go'] }, vfts: { enabled: false, ignored: [] } }).size, 0)
  // 缺失 / 非对象 / ignored 非数组 / 空 → 空集
  assert.equal(ignoredSetFromResult(null).size, 0)
  assert.equal(ignoredSetFromResult(undefined).size, 0)
  assert.equal(ignoredSetFromResult({}).size, 0)
  assert.equal(ignoredSetFromResult({ codegraph: { enabled: true, ignored: [] } }).size, 0)
  assert.equal(ignoredSetFromResult({ codegraph: { enabled: true, ignored: 'x' } }).size, 0)
  assert.equal(ignoredSetFromResult({ codegraph: { enabled: true, ignored: [1, '', null] } }).size, 0)
  // enabled 非严格 true（"true" / 1）→ 不判定（后端恒布尔，防串口）
  assert.equal(ignoredSetFromResult({ codegraph: { enabled: 'true', ignored: ['a.gen.go'] } }).size, 0)
})

// ── ① 请求：topic / paths 数组 / 去重 / 批量 ──────────────────

test('fetchIndexIgnored：发 data-index-ignored，paths 为数组（去重保序），一次请求', async () => {
  const calls = []
  const emit = (topic, payload) => {
    calls.push({ topic, payload })
    return Promise.resolve({
      backend: {
        ok: true,
        result: {
          codegraph: { enabled: true, ignored: ['a.gen.go'] },
          vfts: { enabled: true, ignored: ['a.gen.go', 'vdir/v.go'] },
        },
      },
    })
  }
  const r = await fetchIndexIgnored(['a.gen.go', 'main.go', 'a.gen.go', 'vdir/v.go'], { emit })
  assert.equal(calls.length, 1, '一批路径只请求一次')
  assert.equal(calls[0].topic, 'data-index-ignored')
  assert.equal(INDEX_IGNORED_TOPIC, 'data-index-ignored')
  assert.ok(Array.isArray(calls[0].payload.paths), 'paths 必须是数组')
  assert.deepEqual(calls[0].payload.paths, ['a.gen.go', 'main.go', 'vdir/v.go'], '去重保序')
  assert.deepEqual([...r.ignored].sort(), ['a.gen.go', 'vdir/v.go'])
  assert.equal(r.truncated, false)
  // 空入参 → 不发请求
  const calls2 = []
  const r2 = await fetchIndexIgnored([], { emit: (t, p) => { calls2.push(t); return Promise.resolve({}) } })
  assert.equal(calls2.length, 0)
  assert.equal(r2.ignored.size, 0)
})

test('fetchIndexIgnored：enabled=false / 空 ignored → 空集（不灰显）', async () => {
  const emit = () => Promise.resolve({
    backend: {
      ok: true,
      result: { codegraph: { enabled: false, ignored: ['a.gen.go'] }, vfts: { enabled: false, ignored: [] } },
    },
  })
  const r = await fetchIndexIgnored(['a.gen.go'], { emit })
  assert.equal(r.ignored.size, 0)
  assert.equal(r.truncated, false)
})

test('fetchIndexIgnored：失败 / 主题不可用 / 应答异常 → 空集（静默降级）', async () => {
  // 网络/桥失败（backend=null）
  assert.equal((await fetchIndexIgnored(['a.gen.go'], { emit: () => Promise.resolve(null) })).ignored.size, 0)
  // 后端信封 ok=false
  assert.equal((await fetchIndexIgnored(['a.gen.go'], { emit: () => Promise.resolve({ backend: { ok: false, errors: ['boom'] } }) })).ignored.size, 0)
  // result.ok=false
  assert.equal((await fetchIndexIgnored(['a.gen.go'], { emit: () => Promise.resolve({ backend: { ok: true, result: { ok: false } } }) })).ignored.size, 0)
  // errors 非空
  assert.equal((await fetchIndexIgnored(['a.gen.go'], { emit: () => Promise.resolve({ backend: { ok: true, result: {}, errors: ['x'] } }) })).ignored.size, 0)
  // 请求抛错 / reject
  assert.equal((await fetchIndexIgnored(['a.gen.go'], { emit: () => { throw new Error('no topic') } })).ignored.size, 0)
  assert.equal((await fetchIndexIgnored(['a.gen.go'], { emit: () => Promise.reject(new Error('reject')) })).ignored.size, 0)
})

test('fetchIndexIgnored：truncated 透传（调用方据此不缓存已判定）', async () => {
  const emit = () => Promise.resolve({
    backend: { ok: true, result: { codegraph: { enabled: true, ignored: ['a.gen.go'] }, vfts: { enabled: true, ignored: [] }, truncated: true } },
  })
  const r = await fetchIndexIgnored(['a.gen.go'], { emit })
  assert.equal(r.truncated, true)
  assert.deepEqual([...r.ignored], ['a.gen.go'])
})

// ── ② 源码守卫：FileTree 判定时机 / 刷新 / 禁 watch ────────────

test('FileTree：按批判定（单飞 + 缓存）在树数据到手 / 每层加载 / 变更合并后触发', () => {
  const ft = read('views/filetree/FileTree.vue')
  assert.match(ft, /import \{ fetchIndexIgnored, toWorkdirRelPath \} from '\.\.\/\.\.\/utils\/indexIgnored'/, '应复用 indexIgnored 工具')
  assert.match(ft, /async function judgeLoadedNodes\(\)/, '应有按批判定入口')
  assert.match(ft, /_judgedRel/, '应缓存「已查询」路径（同一路径只请求一次）')
  assert.match(ft, /if \(_judgeBusy\) \{ _judgeAgain = true; return \}/, '并发触发单飞 + 补跑（避免重复请求）')
  // 至少三处触发：树数据到手 / loadDirChildren 末尾 / file.changed 合并后（含 refreshDirInTree 根重建）
  const calls = ft.match(/judgeLoadedNodes\(\)/g) || []
  assert.ok(calls.length >= 4, 'judgeLoadedNodes 应在多处触发（实际 ' + calls.length + ' 处）')
  assert.match(ft, /新一批已加载节点 → 按批判定排除状态/, 'loadDirChildren 末尾应判定')
  assert.match(ft, /树数据到手 → 判定排除状态/, 'loadInitData 后应判定')
  assert.match(ft, /变更合并可能新增节点 → 补判排除状态/, 'filesys.changed 合并后应判定')
  // 截断兜底：截断时不缓存（未判定节点不灰显）
  assert.match(ft, /if \(!truncated\) for \(const rel of pending\) _judgedRel\.add\(rel\)/, 'truncated 时不得缓存已判定')
})

test('FileTree：订阅既有 data-prj-config-refresh 重判（灰能变回不灰）', () => {
  const ft = read('views/filetree/FileTree.vue')
  assert.match(ft, /import \{ onDataRefresh \} from '\.\.\/\.\.\/utils\/dataClient'/, '应复用 dataClient.onDataRefresh')
  assert.match(ft, /onDataRefresh\('prj-config', rejudgeIndexIgnored\)/, '订阅既有 prj-config 广播')
  assert.match(ft, /_judgedRel\.clear\(\)/, '刷新应清「已查询」缓存')
  assert.match(ft, /_ignoredRel\.clear\(\)/, '刷新应清「已排除」缓存')
  assert.match(ft, /applyIgnoredFlags\(\)/, '刷新应先清灰标记再重判')
})

test('FileTree：不得使用 watch / watchEffect', () => {
  const ft = read('views/filetree/FileTree.vue')
  assert.doesNotMatch(ft, /\bwatch\s*\(|\bwatchEffect\s*\(/, '不得使用 watch/watchEffect')
})

// ── ③ 源码守卫：TreeNode 类/样式/title + 交互保留 ──────────────

test('TreeNode：命中节点带 is-ignored，样式 = 文本 --fg-disabled（只改颜色）', () => {
  const tn = read('views/filetree/TreeNode.vue')
  assert.match(tn, /'is-ignored':\s*!!node\._ignored/, 'is-ignored 由节点标记派生')
  const rule = tn.match(/\.tree-row\.is-ignored[^{]*\{([^}]*)\}/)
  assert.ok(rule, '应有 .tree-row.is-ignored 样式规则')
  assert.match(rule[1], /color:\s*var\(--fg-disabled\)/, '灰显色必须用 --fg-disabled')
  assert.doesNotMatch(rule[1], /pointer-events|display|visibility/, '只改颜色，不得动交互/布局属性')
})

test('TreeNode：灰显节点加原生 title（i18n，不改布局/换行）', () => {
  const tn = read('views/filetree/TreeNode.vue')
  assert.match(tn, /:title="node\._ignored \? \$t\('fileTree\.index_ignored'\) : undefined"/, '灰显节点应带 i18n 原生 title')
  assert.doesNotMatch(tn, /index_ignored[\s\S]{0,80}white-space/, '不得因 title 改变换行/布局')
})

test('TreeNode：交互未被移除（点击 / 双击 / 右键 / 拖拽仍在）', () => {
  const tn = read('views/filetree/TreeNode.vue')
  assert.match(tn, /@click="\$emit\('row-click'/, '仍有点击')
  assert.match(tn, /@dblclick="\$emit\('row-dblclick'/, '仍有双击')
  assert.match(tn, /@contextmenu\.prevent="\$emit\('context-menu'/, '仍有右键菜单')
  assert.match(tn, /draggable="true"/, '仍可拖拽')
  assert.match(tn, /@dragstart\.stop=/, '仍绑定拖拽启动')
  assert.match(tn, /class="arrow"/, '目录展开箭头仍在')
})

// ── ④ 零新增 MQ 主题 ────────────────────────────────────────

test('零新增 MQ 主题：event-names 不得新增索引排除主题，字面量只在 util', () => {
  const names = read('events/event-names.js')
  assert.doesNotMatch(names, /indexIgnored|index-ignored|data-index-ignored/, 'event-names 不得新增索引排除主题')
  const util = read('utils/indexIgnored.js')
  assert.match(util, /'data-index-ignored'/, 'util 内使用已批准的 data-index-ignored')
  // FileTree 不得出现裸主题字面量（应经 util 常量）
  const ft = read('views/filetree/FileTree.vue')
  assert.doesNotMatch(ft, /mq\.emit\(\s*['"]data-index-ignored/, 'FileTree 不得直发裸主题')
})

// ── ⑤ i18n ─────────────────────────────────────────────────

test('i18n：index_ignored 键 zh/en 齐备，既有键不丢失', () => {
  const zh = readLocale('zh-CN', 'fileTree.json')
  const en = readLocale('en-US', 'fileTree.json')
  assert.equal(zh.index_ignored, '已排除：不会参与索引')
  assert.equal(en.index_ignored, 'Excluded: not indexed')
  for (const loc of LOCALES) {
    const m = readLocale(loc, 'fileTree.json')
    assert.ok(typeof m.index_ignored === 'string' && m.index_ignored.length > 0, loc + ' 缺 index_ignored')
    // 既有键不得被删
    for (const k of ['new_folder', 'type_resource', 'mode_memory', 'no_project']) {
      assert.equal(typeof m[k], 'string', loc + ' 既有键丢失 ' + k)
    }
  }
})

// ── ⑥ utils/fileTree.js 订阅退订 + 死导出清理（E5-④）────────────

test('utils/fileTree：订阅 API 返回退订函数；死导出 loading 已删', () => {
  const util = read('utils/fileTree.js')
  // 死导出：loading 无消费者 → 删除（含 vue ref 依赖一并移除）
  assert.doesNotMatch(util, /\bloading\b/, '死导出 loading 须删除')
  assert.doesNotMatch(util, /from 'vue'/, '删除 loading 后不应再依赖 vue')
  assert.doesNotMatch(util, /\bexport \{[^}]*\bloading\b/, '不得再导出 loading')
  // 退订：两个订阅 API 均返回 unsub（按身份守卫的单槽退订）
  assert.match(util, /function onFileChanged\(cb\)\s*\{[\s\S]*?return \(\) => \{ if \(callback === cb\) callback = null \}/,
    'onFileChanged 须返回退订函数（身份守卫）')
  assert.match(util, /function onFileChangedEvent\(cb\)\s*\{[\s\S]*?return \(\) => \{ if \(eventCallback === cb\) eventCallback = null \}/,
    'onFileChangedEvent 须返回退订函数（身份守卫）')
})

test('FileTree：卸载时退订 utils/fileTree 的回调（防遗留已卸载组件闭包）', () => {
  const ft = read('views/filetree/FileTree.vue')
  assert.match(ft, /_cleanup\.push\(onFileChanged\(/, 'onFileChanged 退订须纳入 _cleanup')
  assert.match(ft, /_cleanup\.push\(onFileChangedEvent\(/, 'onFileChangedEvent 退订须纳入 _cleanup')
  assert.match(ft, /for \(const fn of _cleanup\) fn\(\)/, 'onUnmounted 须遍历执行 _cleanup')
})
