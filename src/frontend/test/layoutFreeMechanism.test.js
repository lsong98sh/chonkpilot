/**
 * 自由布局子系统 · 机制层守卫（UILAYOUT-1 §7 P0/P1 交付面，仓库级静态断言）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 与既有 `uxLayoutSplit.test.js` /
 * `statusbarMemoryEntry.test.js` 同法：**源码守卫**（读 `.vue` / `.js` 文本做集合与接线断言）。
 *
 * 覆盖：
 *   P0① 并入落点与去重边界（无 `@/` 别名、导入不越出子系统、demo 视图与副本已删）
 *   P0④ 组件命名域 `Lf*`
 *   P1① 门面 `engine.js` 的 API 与注入点 `deps.js`
 *   P1② 禁 `watch` / `watchEffect` / `$subscribe`
 *   P1③ 去重：dialog / icon / split 副本改注入既有实现
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative, resolve } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..', 'src', 'layout-free')

function walk(dir, out = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name)
    if (e.isDirectory()) walk(p, out)
    else out.push(p)
  }
  return out
}

const FILES = walk(ROOT)
const SOURCES = FILES.filter((f) => /\.(vue|js|mjs)$/.test(f))
const read = (f) => readFileSync(f, 'utf8')
const rel = (f) => relative(ROOT, f).split('\\').join('/')

test('layout-free：并入后不再有 @/ 别名导入（依赖相对路径）', () => {
  const offenders = SOURCES.filter((f) => /from\s+'@\//.test(read(f))).map(rel)
  assert.deepEqual(offenders, [], `仍有 @/ 导入：\n${offenders.join('\n')}`)
})

test('layout-free：所有相对导入都落在子系统内（机制层不 import 宿主代码）', () => {
  const outside = []
  for (const f of SOURCES) {
    for (const m of read(f).matchAll(/from\s+'(\.\.?\/[^']+)'/g)) {
      const p = resolve(dirname(f), m[1])
      const r = relative(ROOT, p)
      if (r.startsWith('..')) outside.push(`${rel(f)}  ←  ${m[1]}`)
    }
  }
  assert.deepEqual(outside, [], `导入越出子系统：\n${outside.join('\n')}`)
})

test('layout-free：禁 watch / watchEffect / $subscribe（§4.2）', () => {
  const offenders = []
  for (const f of SOURCES) {
    const text = read(f)
    // 行首语句形式（注释行以 * / // 开头，不会命中）
    if (/^\s*watch(Effect)?\s*\(/m.test(text)) offenders.push(`${rel(f)}  ←  语句 watch(`)
    // import 列表里的 watch
    for (const m of text.matchAll(/import\s*\{([^}]*)\}\s*from\s*'vue'/g)) {
      if (/(^|,)\s*watch(Effect)?\s*(,|$)/.test(m[1])) offenders.push(`${rel(f)}  ←  import { watch }`)
    }
    if (/\$subscribe\s*\(/.test(text)) offenders.push(`${rel(f)}  ←  $subscribe(`)
  }
  assert.deepEqual(offenders, [], `仍在使用 watch 类 API：\n${offenders.join('\n')}`)
})

test('layout-free P0③/P1③：demo 视图与去重副本已删', () => {
  const gone = [
    'views/registry.js',
    'views/presets',
    'components/dialog',
    'components/split',
    'components/icon/Icon.vue',
    'components/icon/icon-set.js',
    'components/icon/index.js',
  ]
  const still = gone.filter((p) => existsSync(join(ROOT, p)))
  assert.deepEqual(still, [], `应已删除：\n${still.join('\n')}`)

  // 也不得再 import 这些落点（只看 import 说明符；注释里提到「原副本」是文档说明）
  const refs = []
  for (const f of SOURCES) {
    for (const m of read(f).matchAll(/from\s+'([^']+)'/g)) {
      if (/components\/(dialog|split)|icon\/Icon\.vue|icon-set|views\/registry/.test(m[1])) {
        refs.push(`${rel(f)}  ←  ${m[1]}`)
      }
    }
  }
  assert.deepEqual(refs, [], `仍 import 已删副本：\n${refs.join('\n')}`)
})

test('layout-free P0④：组件命名域统一 Lf 前缀', () => {
  const comps = FILES.filter((f) => /\.vue$/.test(f) && /(^|\/)(components|views)\//.test(rel(f)))
  const bad = comps.map((f) => rel(f).split('/').pop()).filter((n) => !n.startsWith('Lf'))
  assert.deepEqual(bad, [], `组件文件未加 Lf 前缀：\n${bad.join('\n')}`)
  assert.ok(comps.length >= 25, `组件数偏少（${comps.length}）—— 复制是否完整？`)
})

test('layout-free P1①：engine.js 门面 API 与 deps.js 注入点齐备', () => {
  const engine = read(join(ROOT, 'engine.js'))
  assert.match(engine, /export\s+function\s+createLayoutEngine\s*\(/, 'createLayoutEngine 门面')
  for (const api of ['install(', 'get state()', 'get actions()', 'get renderLayout()', 'renderBar(', 'get editing()', 'get selectedNodeId()', 'validate()']) {
    assert.ok(engine.includes(api), `engine 缺 API：${api}`)
  }
  // 去重注册（§3.10）：既有实现注入后注册为子系统命名域组件
  assert.match(engine, /app\.component\('LfIcon'/, '注入的 Icon 注册为全局 LfIcon')
  assert.match(engine, /app\.component\('LfSplitPanel'/, '注入的 SplitPanel 注册为全局 LfSplitPanel')
  // v-mq 指令：宿主已装则复用（避免重复注册）
  assert.match(engine, /_context[^\n]*directives[^\n]*mq/, '需检测宿主已装 v-mq')

  const deps = read(join(ROOT, 'deps.js'))
  for (const api of ['setLayoutDeps', 'getLayoutDeps', 'getMq', 'getDialog', 'resolveIcon', 'viewDefinitions', 'resolveViewComponent']) {
    assert.ok(deps.includes(`export function ${api}`), `deps 缺注入点：${api}`)
  }
})

test('layout-free P1③：mq / dialog 都走注入（不再直连内置 standalone mq）', () => {
  const offenders = []
  for (const f of SOURCES) {
    const r = rel(f)
    if (r === 'utils/mq.js' || r === 'deps.js' || r === 'engine.js' || r === 'main.js') continue
    const text = read(f)
    if (/^import\s+mq\s+from\s+'[^']*utils\/mq'/m.test(text)) offenders.push(`${r}  ←  直连内置 mq`)
    if (/from\s+'[^']*components\/dialog'/.test(text)) offenders.push(`${r}  ←  直连副本 dialog`)
  }
  assert.deepEqual(offenders, [], `未走注入：\n${offenders.join('\n')}`)
})

/**
 * G-57（2026-10-17）守卫：配置 `sizes` 变化 → **幂等重量测**；用户拖拽不被覆盖。
 *
 * 口径（设计稿 §7 / [41 G-57]）：
 *   · 「配置占比快照」`knownRatios` + `ratiosEqual` 是「配置变化 vs 用户拖拽」的判据；
 *   · `onUpdated` 里 `measure(true)` **必须**由 `!ratiosEqual(ratios.value, knownRatios)` **门控**
 *     —— 无条件 `measure(true)` 会重置运行态像素（该文件既有告警）；
 *   · 用户拖拽 `commit()` 必须**记账**（`markRatiosKnown(sizes)`）→ 配置随后的原地改写判为「无变化」。
 */
test('layout-free G-57：配置 sizes 变化 → 幂等重量测（判据门控 + 拖拽落账）', () => {
  const src = read(join(ROOT, 'components', 'layout', 'LfLayoutSplit.vue'))
  assert.match(src, /let knownRatios = null/, '缺配置占比快照 knownRatios')
  assert.match(src, /function ratiosEqual\(/, '缺占比近似相等判据 ratiosEqual')
  assert.match(src, /function markRatiosKnown\(/, '缺记账函数 markRatiosKnown')
  // commit（用户拖拽落账）必须记账，否则「配置自己被写回」会被误判为变化 → 覆盖运行态
  const commit = src.match(/function commit\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(commit, '未找到 commit()')
  assert.match(commit[0], /markRatiosKnown\(sizes\)/, 'commit() 须把拖拽结果记账（防自触发重量测）')
  // onMounted 必须记账初值（否则首帧后的合法配置变更会被漏判）
  const mounted = src.match(/onMounted\(\(\)\s*=>\s*\{[\s\S]*?\n\}\)/)
  assert.ok(mounted, '未找到 onMounted')
  assert.match(mounted[0], /markRatiosKnown\(\)/, 'onMounted 须记账初值')
  // onUpdated：结构签名分支之外的 measure(true) 必须由占比判据门控
  const updated = src.match(/onUpdated\(\(\)\s*=>\s*\{[\s\S]*?\n\}\)/)
  assert.ok(updated, '未找到 onUpdated')
  assert.match(updated[0], /measure\(true\)/, 'onUpdated 须包含重量测调用')
  assert.match(updated[0], /!ratiosEqual\(ratios\.value, knownRatios\)/, 'onUpdated 须用占比判据门控重量测')
  assert.match(updated[0], /if \(sig !== lastChildSig\) \{/, 'onUpdated 须保留结构签名分支')
})
