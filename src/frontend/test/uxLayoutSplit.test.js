/**
 * UX 布局修复回归（P0-3 首启预览区过窄 / P1-5 小屏断点 / P1-6 resizer 合规与潜在 bug /
 * P1-8 高度 token 一致）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = 纯逻辑直调
 * （splitLayout.js / cssToken.js，两者为从组件抽出的纯模块）+ `*.vue` / `*.css`
 * 源码守卫（与 statusbarMemoryEntry / toolStopWiring 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  DEFAULT_MIN_SIZE,
  DEFAULT_MAX_SIZE,
  shouldShowResizer,
  resolveDragTarget,
  paneMin,
  paneMax,
} from '../src/components/split/splitLayout.js'
import {
  FALLBACK_TOOLBAR_HEIGHT,
  FALLBACK_STATUSBAR_HEIGHT,
  parseCssPx,
  computeContentHeight,
} from '../src/utils/cssToken.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC_DIR = join(HERE, '..', 'src')
const SRC = (p) => readFileSync(join(SRC_DIR, p), 'utf8')

function listSrcFiles(dir) {
  const out = []
  for (const ent of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, ent.name)
    if (ent.isDirectory()) out.push(...listSrcFiles(p))
    else out.push(p)
  }
  return out
}

// ── P1-6 resizer 渲染条件（① 两侧 resizable !== false 才渲染）──────────────

test('P1-6 resizer：相邻两侧都不得显式 resizable:false（否则渲染 0 宽 resizer）', () => {
  // 外层：toolbar / statusbar 均 resizable:false，中间 content 撑满
  const outer = [
    { id: 'toolbar', size: 44, resizable: false },
    { id: 'content', flex: true },
    { id: 'statusbar', size: 24, resizable: false },
  ]
  assert.equal(shouldShowResizer(outer, 0), false, 'toolbar（固定）+ content → 不渲染')
  assert.equal(shouldShowResizer(outer, 1), false, 'content + statusbar（固定）→ 不渲染')
  assert.equal(shouldShowResizer(outer, 2), false, '末尾 pane 之后无 resizer')

  // 定尺（未声明 resizable）+ flex → 可拖拽边，须渲染
  const body = [
    { id: 'content', flex: true, min: 280 },
    { id: 'chat', size: 520, min: 280, max: 800 },
  ]
  assert.equal(shouldShowResizer(body, 0), true, 'flex + 定尺 → 渲染 resizer（拖定尺邻居）')
  assert.equal(shouldShowResizer([], 0), false, '空 panes 防御')
})

// ── P1-6 拖拽目标（② resizable:false 不生效）──────────────────────────────

test('P1-6 拖拽目标：目标显式 resizable:false 时不启动拖拽（固定区不可拖）', () => {
  const outer = [
    { id: 'toolbar', size: 44, resizable: false },
    { id: 'content', flex: true },
    { id: 'statusbar', size: 24, resizable: false },
  ]
  // flex 的 content → 目标 statusbar（resizable:false）→ 不可拖（原 bug：可改 statusbar 高）
  assert.equal(resolveDragTarget(outer, 1), null, 'flex → 其后固定区（resizable:false）→ 不拖')
  // 自身为 resizable:false 的固定区 → 不可拖
  assert.equal(resolveDragTarget(outer, 0), null, '固定区自身 → 不拖')

  // 正常：flex → 其后定尺 pane
  const body = [
    { id: 'content', flex: true },
    { id: 'chat', size: 520, min: 280, max: 800 },
  ]
  assert.equal(resolveDragTarget(body, 0), 1, 'flex → 其后定尺 pane')
  assert.equal(resolveDragTarget(body, 1), 1, '定尺 pane → 拖自身')

  // 两侧都撑满 → 无可拖目标
  assert.equal(resolveDragTarget([{ id: 'a', flex: true }, { id: 'b', flex: true }], 0), null)
})

test('P1-6 默认 min/max 退化值：不再退化到 100 / 2000', () => {
  assert.equal(DEFAULT_MIN_SIZE, 160)
  assert.equal(DEFAULT_MAX_SIZE, 2000)
  assert.equal(paneMin({}), DEFAULT_MIN_SIZE)
  assert.equal(paneMax({}), DEFAULT_MAX_SIZE)
  assert.equal(paneMin({ min: 280 }), 280, '显式 min 优先')
  assert.equal(paneMax({ max: 800 }), 800, '显式 max 优先')
})

test('P1-6 SplitPanel：常态透明（无内联描边色）+ 统一走纯逻辑 + flex 最小尺寸保护', () => {
  const sp = SRC('components/split/SplitPanel.vue')
  assert.doesNotMatch(sp, /gapColor|gap-color/, 'resizer 不得再走 gapColor（常态须 transparent）')
  assert.doesNotMatch(sp, /:style="\{\s*background/, 'resizer 常态不得内联描边色')
  assert.match(sp, /shouldShowResizer\(visiblePanes, i\)/, '渲染条件须走 splitLayout.shouldShowResizer')
  assert.match(sp, /resolveDragTarget\(visiblePanes\.value, visibleIndex\)/, '拖拽目标须走 splitLayout.resolveDragTarget')
  assert.match(sp, /paneMin\(pane\)|paneMin\(targetPane\)/, '定尺 min 须走 paneMin')
  assert.match(sp, /minWidth: `\$\{flexMin\}px`/, 'flex pane 须按 pane.min 给主轴最小尺寸保护')

  const resizerCss = sp.match(/\.split-resizer \{[\s\S]*?\n\}/)[0]
  assert.doesNotMatch(resizerCss, /background:/, '常态不写 background（默认 transparent）')
  assert.match(sp, /\.split-resizer:hover\s*\{[\s\S]*?background:\s*var\(--accent\)/, 'hover 才 accent')

  const ml = SRC('views/layout/MainLayout.vue')
  assert.doesNotMatch(ml, /gap-color/, 'MainLayout 不得再传已移除的 gap-color')
})

// ── P1-8 高度 token（③ contentH 计算）────────────────────────────────────

test('P1-8 contentH：token 口径（无 DOM 回退 44/24；有 DOM 读 :root token）', () => {
  assert.equal(FALLBACK_TOOLBAR_HEIGHT, 44)
  assert.equal(FALLBACK_STATUSBAR_HEIGHT, 24)
  // 无 DOM（node:test 默认）→ 回退常量
  assert.equal(computeContentHeight(800), 800 - 44 - 24, '800 → 732')

  // 解析：'44px' / '24' → 数字；空 / 非法 → fallback
  assert.equal(parseCssPx('44px', 0), 44)
  assert.equal(parseCssPx('24', 0), 24)
  assert.equal(parseCssPx('', 7), 7)
  assert.equal(parseCssPx('auto', 9), 9)

  // 模拟 DOM：token 改动 → contentH 同源变化（证明走 token 而非硬编码）
  globalThis.document = { documentElement: {} }
  globalThis.getComputedStyle = () => ({
    getPropertyValue: (n) => ({ '--toolbar-height': '48px', '--statusbar-height': '30px' }[n] ?? ''),
  })
  try {
    assert.equal(computeContentHeight(800), 800 - 48 - 30, '800 → 722（读 :root token）')
  } finally {
    delete globalThis.document
    delete globalThis.getComputedStyle
  }
})

test('P1-8 接线守卫：MainLayout contentH / toolbar / statusbar 均走 token，无硬编码 44/24', () => {
  const ml = SRC('views/layout/MainLayout.vue')
  assert.match(ml, /computeContentHeight\(vh\)/, 'contentH 须走 cssToken.computeContentHeight')
  assert.doesNotMatch(ml, /vh\s*-\s*44\s*-\s*24/, '不得再硬编码 vh - 44 - 24')
  assert.match(ml, /readRootPx\('--statusbar-height'/, 'statusbar 高须取 token')
  assert.match(ml, /const STATUSBAR_H = readRootPx\('--statusbar-height'/, 'STATUSBAR_H 取 token')
  assert.match(ml, /\{ id: 'statusbar', size: STATUSBAR_H, resizable: false \}/, 'statusbar pane 高走 token')
  assert.match(ml, /\{ id: 'toolbar', size: TOOLBAR_H, resizable: false \}/, 'toolbar pane 高走 token')

  const sb = SRC('views/statusbar/StatusBar.vue')
  assert.match(sb, /\.statusbar \{[\s\S]*?height:\s*var\(--statusbar-height\)/, 'StatusBar 高走 token')
  assert.doesNotMatch(sb, /height:\s*24px/, 'StatusBar 不得再硬编码 24px')
})

test('P1-8 variables.css：新增 --statusbar-height；4 个零引用死 token 已删（全 src 零引用）', () => {
  const vars = SRC('assets/styles/variables.css')
  assert.match(vars, /--toolbar-height:\s*44px/)
  assert.match(vars, /--statusbar-height:\s*24px/, '须新增 statusbar 高 token')

  const dead = ['--sidebar-width', '--sidebar-collapsed-width', '--codeview-width', '--task-panel-height']
  for (const name of dead) {
    assert.doesNotMatch(vars, new RegExp(name + '\\s*:'), name + ' 定义应删除')
  }
  const offenders = []
  for (const file of listSrcFiles(SRC_DIR)) {
    const text = readFileSync(file, 'utf8')
    for (const name of dead) {
      if (text.includes(name)) offenders.push(name + ' @ ' + file)
    }
  }
  assert.deepEqual(offenders, [], '死 token 在 src 下须零引用')
})

// ── P0-3 首启预览区过窄 / P1-5 小屏断点 ────────────────────────────────

test('P0-3/P1-5 MainLayout：默认尺寸收紧 + flex 最小保护 + 窄屏断点（无 watch）', () => {
  const ml = SRC('views/layout/MainLayout.vue')
  // P0-3 默认：filetree 320 / chat 520（1280×800 下 preview ≈ 428 ≥ 350）
  assert.match(ml, /const filetreeWidth = ref\(320\)/, '默认 filetree 320')
  assert.match(ml, /const chatWidth = ref\(520\)/, '默认 chat 520')
  assert.match(ml, /Number\(l\.chatWidth\) \|\| 520/, '恢复缺省值同为 520')
  assert.match(ml, /Number\(l\.filetreeWidth\) \|\| 320/, '恢复缺省值同为 320')

  // P1-5 flex pane 最小尺寸保护
  assert.match(ml, /const PREVIEW_MIN = 280/, 'preview 最小宽 280')
  assert.match(ml, /const SESSION_CHAT_MIN = 240/, 'session-chat 最小宽 240')
  assert.match(ml, /\{ id: 'preview', flex: true, min: PREVIEW_MIN \}/, 'preview 传 min')
  assert.match(ml, /\{ id: 'session-chat', flex: true, min: SESSION_CHAT_MIN \}/, 'session-chat 传 min')

  // P1-5 小屏断点：窄屏收起文件树（preview 槽位索引不位移 → CodeView 持续挂载）
  assert.match(ml, /const NARROW_MAX = 900/, '小屏断点 900')
  assert.match(ml, /const narrow = computed\(\(\) => viewportW\.value <= NARROW_MAX\)/)
  assert.match(ml, /visible: filetreeOpen\.value && !narrow\.value/, '窄屏收起文件树')
  // 响应式：窗口缩放 → 视口宽更新 → 有效尺寸重算
  assert.match(ml, /viewportW\.value = window\.innerWidth/, 'resize 时更新视口宽')
  assert.match(ml, /const effChatWidth = computed/, '有效 chat 宽按视口 clamp')
  assert.match(ml, /const effFiletreeWidth = computed/, '有效 filetree 宽按视口 clamp')

  // 前端铁律：不得用 watch / watchEffect 监听
  assert.doesNotMatch(ml, /\bwatch(Effect)?\s*\(/, '不得使用 watch / watchEffect')
})
