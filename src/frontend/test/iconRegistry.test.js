/**
 * 图标注册表守卫（ICON-1，2026-10-28）。
 *
 * 背景：`layout` / `layout-free` / 默认布局文档统一用 Lucide 命名，但历史上
 * `components/icon/icons.js` 只有 55 个名，未知 name 在 `Icon.vue` 里静默渲染为空白，
 * 导致顶栏主题 / 语言 / 任务详情、状态栏 DevTools、区域显隐等一批图标实际是空的。
 *
 * 本用例是根因防线：源码里出现的每个静态图标名，必须能在 `icons.js` 里查到。
 *
 * 覆盖：
 *  1) 注册表自检：每个值都是完整 `<svg>…</svg>`、viewBox 合法、无重复键。
 *  2) 静态引用自检：`<Icon|LfIcon name="x">`、`icon: 'x'`、`:name="'x'"`、
 *     `LfBarEditor` 的 `iconPresets` 清单，全部必须存在于注册表。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'
import { icons } from '../../../../chonkpilot-framework/host/web/src/components/icon/icons.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..')
const SRC = join(ROOT, 'src')
// 平台基座源码（图标注册表 / 布局引擎 / 通用控件已迁出主仓，TOPO-1 §2.22.2）
const FW_SRC = join(HERE, '..', '..', '..', '..', 'chonkpilot-framework', 'host', 'web', 'src')

function walk(dir, out = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name)
    if (e.isDirectory()) walk(p, out)
    else out.push(p)
  }
  return out
}

const SOURCES = [...walk(SRC), ...walk(FW_SRC)].filter((f) => /\.(vue|js|mjs)$/.test(f))
const read = (f) => readFileSync(f, 'utf8')
const rel = (f) => relative(ROOT, f).split('\\').join('/')

const REGISTRY = new Set(Object.keys(icons))

/** 采集源码中所有静态图标名（dynamic 绑定 / 拼接值不采集） */
function collectStaticNames() {
  const hits = new Map() // name -> Set<file>
  const add = (name, file) => {
    // 只认纯图标名；`:name="a ? 'x' : 'y'"` 这类动态表达式不采集
    if (!/^[a-z0-9-]+$/.test(name || '')) return
    if (!hits.has(name)) hits.set(name, new Set())
    hits.get(name).add(rel(file))
  }

  for (const f of SOURCES) {
    const text = read(f)

    // <Icon name="x" ...> / <LfIcon name="x" ...>（排除 :name 动态绑定）
    for (const m of text.matchAll(/<(?:Lf)?Icon\b[^>]*?(?<!:)\bname="([^"]+)"/g)) add(m[1], f)

    // <LfIcon :name="'x'" ...>
    for (const m of text.matchAll(/<(?:Lf)?Icon\b[^>]*?:name="'([^']+)'"/g)) add(m[1], f)

    // 对象字面量卡片：{ label: '…', icon: 'x' }
    for (const m of text.matchAll(/\bicon(?:Name)?\s*:\s*'([^']+)'/g)) add(m[1], f)
  }

  // LfBarEditor 的候选图标清单（纯数组，不在上面任何模式内）
  const presetFile = join(FW_SRC, 'layout-free/components/settings/LfBarEditor.vue')
  const presetText = read(presetFile)
  const block = presetText.match(/const iconPresets\s*=\s*\[([\s\S]*?)\]/)
  assert.ok(block, 'LfBarEditor.vue 里找不到 iconPresets 清单')
  for (const m of block[1].matchAll(/'([a-z0-9-]+)'/g)) add(m[1], presetFile)

  return hits
}

test('图标注册表：每个值都是完整 <svg>…</svg> 且 viewBox 合法', () => {
  const bad = []
  for (const [name, svg] of Object.entries(icons)) {
    if (!svg.startsWith('<svg ') || !svg.endsWith('</svg>')) bad.push(`${name}：不是完整 svg 串`)
    const vb = svg.match(/viewBox="([^"]+)"/)
    if (!vb) bad.push(`${name}：缺 viewBox`)
    else if (!['0 0 24 24', '0 0 1024 1024'].includes(vb[1])) bad.push(`${name}：viewBox=${vb[1]}`)
    if (!/stroke="currentColor"|fill="currentColor"/.test(svg)) bad.push(`${name}：未继承 currentColor`)
  }
  assert.deepEqual(bad, [])
})

test('图标注册表：无重复键（同名后写覆盖前写）', () => {
  const text = read(join(FW_SRC, 'components/icon/icons.js'))
  const keys = [...text.matchAll(/^\s{2}'([a-z0-9-]+)':/gm)].map((m) => m[1])
  const dup = keys.filter((k, i) => keys.indexOf(k) !== i)
  assert.deepEqual(dup, [], `重复的图标键：${dup.join(', ')}`)
})

test('图标引用：源码里出现的静态图标名必须都在 icons.js 中', () => {
  const missing = []
  for (const [name, files] of collectStaticNames()) {
    if (!REGISTRY.has(name)) missing.push(`${name}  ← ${[...files].join(', ')}`)
  }
  assert.deepEqual(missing, [], `以下图标名未注册到 components/icon/icons.js：\n${missing.join('\n')}`)
})

test('图标引用：布局子系统与默认布局文档用到的 Lucide 名已齐备', () => {
  // 这批是本次补齐的核心缺口（回归守卫，缺失即说明回退）
  const required = [
    'sunny', 'moon', 'moon-night', 'language', 'flag',
    'panel-left', 'panel-right', 'panel-top', 'panel-bottom',
    'settings', 'eye', 'eye-closed', 'eye-off', 'x', 'trash-2', 'ellipsis',
    'chevron-down', 'chevron-right', 'grid', 'square-dashed', 'terminal', 'clipboard-list',
    'split-square-horizontal', 'split-square-vertical', 'columns-2', 'rows-2',
    'wrench', 'sliders-horizontal', 'pin', 'pin-off', 'copy', 'paste', 'lock', 'unlock',
  ]
  const lost = required.filter((n) => !REGISTRY.has(n))
  assert.deepEqual(lost, [], `缺失的图标：${lost.join(', ')}`)
})
