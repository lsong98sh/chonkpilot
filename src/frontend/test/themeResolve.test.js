/**
 * THEME-1 —— 主题静态化（2026-10-23）纯逻辑单测。
 *
 * 覆盖：
 *  1) `isValidTheme` / `schemeOf` 判定（出厂三主题 + 运行态扩展 + 非法值）。
 *  2) `THEME_TOKENS` 白名单完整性：与 `public/shared/ck-theme.js` 实现一致，
 *     且每个 token 在 `public/shared/ck-theme.css` 有 `--ck-*` 映射（否则插件页面取不到值）。
 *  3) 资产一致性：`src/initdata/themes/themes.css` 三套主题块齐备；
 *     `assets/styles/variables.css` 不再含颜色类 token（颜色唯一源 = themes.css）；
 *     `layout/theme.css` 的 `--lf-*` 映射到 `--ck-*` 语义别名。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  THEMES,
  THEME_TOKENS,
  isValidTheme,
  schemeOf,
  themes,
} from '../src/utils/themeResolve.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..')
const SRC = (p) => readFileSync(join(ROOT, 'src', p), 'utf8')
const PUB = (p) => readFileSync(join(ROOT, 'public', p), 'utf8')
const REPO = (p) => readFileSync(join(ROOT, '..', p), 'utf8')

// ── 1) 纯逻辑 ─────────────────────────────────────────────────────

test('THEMES：出厂三主题（light / dark / nord）', () => {
  assert.deepEqual(THEMES, ['light', 'dark', 'nord'])
  // node 环境无 window.ckTheme → themes() 回落出厂列表
  assert.deepEqual(themes(), THEMES)
})

test('isValidTheme：出厂主题可用；空 / 非字符串 / 未知名一律 false', () => {
  for (const id of ['light', 'dark', 'nord']) assert.equal(isValidTheme(id), true, id)
  for (const bad of ['', '   ', 'unknown', ' dark', 'dark ', null, undefined, 42, {}, []]) {
    assert.equal(isValidTheme(bad), false, `bad=${JSON.stringify(bad)}`)
  }
})

test('schemeOf：dark / nord → dark，light 与未知 → light（含命名推断）', () => {
  assert.equal(schemeOf('light'), 'light')
  assert.equal(schemeOf('dark'), 'dark')
  assert.equal(schemeOf('nord'), 'dark')
  assert.equal(schemeOf('solarized-dark'), 'dark')
  assert.equal(schemeOf('my-night'), 'dark')
  assert.equal(schemeOf('solarized-light'), 'light')
  assert.equal(schemeOf(''), 'light')
  assert.equal(schemeOf(null), 'light')
})

// ── 2) 白名单完整性（前端与公共资产一致）────────────────────────────

test('THEME_TOKENS：13 个 --ck-* 且无重复', () => {
  assert.equal(THEME_TOKENS.length, 13)
  assert.equal(new Set(THEME_TOKENS).size, THEME_TOKENS.length, '不得重复')
  for (const t of THEME_TOKENS) assert.match(t, /^--ck-[a-z0-9-]+$/, t)
})

test('THEME_TOKENS ⊆ public/shared/ck-theme.js 白名单', () => {
  const js = PUB('shared/ck-theme.js')
  for (const t of THEME_TOKENS) {
    assert.ok(js.includes(`'${t}'`), `ck-theme.js 缺少白名单项 ${t}`)
  }
})

test('THEME_TOKENS ⊆ public/shared/ck-theme.css 的 --ck-* 映射', () => {
  const css = PUB('shared/ck-theme.css')
  for (const t of THEME_TOKENS) {
    assert.match(css, new RegExp(`${t}:\\s*var\\(`), `ck-theme.css 缺少映射 ${t}`)
  }
})

// ── 3) 资产一致性 ─────────────────────────────────────────────────

test('themes.css：三套主题块齐备（light=:root / dark / nord）', () => {
  const css = REPO('initdata/themes/themes.css')
  assert.match(css, /:root\s*\{[\s\S]*?--bg-primary:\s*#f8f9fa/, 'light 必须在 :root')
  assert.match(css, /\[data-theme="dark"\]\s*\{[\s\S]*?--bg-primary:\s*#1e1e2e/)
  assert.match(css, /\[data-theme="nord"\]\s*\{[\s\S]*?--bg-primary:\s*#2e3440/)
  // 每条主题块都声明三类核心色
  for (const token of ['--bg-primary', '--text-primary', '--accent', '--border']) {
    const hits = css.match(new RegExp(token + ':', 'g')) || []
    assert.ok(hits.length >= 3, `${token} 须在三个主题块各声明（实得 ${hits.length}）`)
  }
})

test('variables.css：颜色类 token 已迁出（只留几何 / 字体 / 尺寸）', () => {
  const css = SRC('assets/styles/variables.css')
  for (const gone of ['--bg-primary', '--text-primary', '--accent:', '--border:', '--success', '--danger']) {
    assert.equal(css.includes(gone), false, `variables.css 不应再声明 ${gone}`)
  }
  for (const kept of ['--toolbar-height', '--statusbar-height', '--split-hairline', '--split-hit', '--border-radius', '--font-sans', '--font-mono', '--transition-fast']) {
    assert.ok(css.includes(kept), `variables.css 须保留 ${kept}`)
  }
})

test('layout/theme.css：--lf-* 映射到 --ck-* 语义别名（不再直连主题色）', () => {
  const theme = SRC('layout/theme.css')
  for (const [lf, ck] of [
    ['--lf-bg', '--ck-bg'],
    ['--lf-panel', '--ck-surface'],
    ['--lf-border', '--ck-border'],
    ['--lf-text', '--ck-text'],
    ['--lf-text-weak', '--ck-text-muted'],
    ['--lf-accent', '--ck-accent'],
    ['--lf-bar', '--ck-toolbar'],
    ['--lf-tree-hover', '--ck-hover'],
    ['--lf-radius', '--ck-radius'],
  ]) {
    assert.match(theme, new RegExp(`${lf}:\\s*var\\(${ck}\\)`), `${lf} 须映射到 ${ck}`)
  }
})
