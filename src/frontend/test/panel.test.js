/**
 * 浮层面板（Panel）机制单测。
 *
 * 两类断言：
 *   ① 几何纯函数（`utils/panelGeometry.js`）：初始位置级联 / 视口 clamp / 八向缩放；
 *   ② 源码守卫：无蒙板、点击外部不关闭、激活置顶（manager `raise` / `applyZ`）、内容走非池化 view。
 *
 * 纯函数是叶子模块（零依赖）→ 直接 import 断言；组件源码只做接线守卫（本仓无组件级运行器）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  PANEL_VISIBLE_EDGE,
  clampMove,
  computeResize,
  resolveInitialPosition,
} from '../../../../chonkpilot-framework/host/web/src/layout-free/utils/panelGeometry.js'
import { PANEL_Z_BASE } from '../../../../chonkpilot-framework/host/web/src/layout-free/components/panel/panelOptions.js'

const HERE = dirname(fileURLToPath(import.meta.url))
const PANEL_DIR = join(HERE, '..', '..', '..', '..', 'chonkpilot-framework', 'host', 'web', 'src', 'layout-free', 'components', 'panel')
const read = (name) => readFileSync(join(PANEL_DIR, name), 'utf8')

const VP = { innerWidth: 1200, innerHeight: 800 }

test('Panel 初始位置：未给 x/y → 按打开顺序级联（默认 48 / 64）', () => {
  assert.deepEqual(resolveInitialPosition({ width: 480, height: 360, viewport: VP, cascadeIndex: 0 }), {
    x: 48,
    y: 64,
  })
  assert.deepEqual(resolveInitialPosition({ width: 480, height: 360, viewport: VP, cascadeIndex: 2 }), {
    x: 104,
    y: 120,
  })
})

test('Panel 初始位置：x/y = null 视同未给（不得被 Number(null)=0 吃掉）', () => {
  const p = resolveInitialPosition({ x: null, y: null, width: 480, height: 360, viewport: VP, cascadeIndex: 1 })
  assert.deepEqual(p, { x: 76, y: 92 })
})

test('Panel 初始位置：显式位置保留；越界 → 夹回视口内（首帧完整可见）', () => {
  assert.deepEqual(resolveInitialPosition({ x: 300, y: 200, width: 480, height: 360, viewport: VP }), {
    x: 300,
    y: 200,
  })
  assert.deepEqual(resolveInitialPosition({ x: 9999, y: 9999, width: 480, height: 360, viewport: VP }), {
    x: 720,
    y: 440,
  })
  assert.deepEqual(resolveInitialPosition({ x: -50, y: -50, width: 480, height: 360, viewport: VP }), {
    x: 0,
    y: 0,
  })
})

test('Panel 移动：允许部分出界，但至少保留 60px 可见 / 标题栏不离屏', () => {
  const farLeft = clampMove({ x: -5000, y: -5000, width: 480, height: 360, viewport: VP })
  assert.equal(farLeft.x, -(480 - PANEL_VISIBLE_EDGE))
  assert.equal(farLeft.y, 0)

  const farRight = clampMove({ x: 5000, y: 5000, width: 480, height: 360, viewport: VP })
  assert.equal(farRight.x, 1200 - PANEL_VISIBLE_EDGE)
  assert.equal(farRight.y, 800 - PANEL_VISIBLE_EDGE)

  assert.deepEqual(clampMove({ x: 100, y: 100, width: 480, height: 360, viewport: VP }), { x: 100, y: 100 })
})

test('Panel 缩放：东 / 南边只改尺寸，西 / 北边对边不动', () => {
  assert.deepEqual(
    computeResize({ dir: 'e', x: 100, y: 100, width: 480, height: 360, dx: 50, dy: 0, viewport: VP }),
    { x: 100, y: 100, width: 530, height: 360 },
  )
  assert.deepEqual(
    computeResize({ dir: 's', x: 100, y: 100, width: 480, height: 360, dx: 0, dy: 40, viewport: VP }),
    { x: 100, y: 100, width: 480, height: 400 },
  )
  // 西边向右拖 30：宽度 -30，右边界仍 = 100+480
  assert.deepEqual(
    computeResize({ dir: 'w', x: 100, y: 100, width: 480, height: 360, dx: 30, dy: 0, viewport: VP }),
    { x: 130, y: 100, width: 450, height: 360 },
  )
  // 北边向下拖 20：高度 -20，下边界仍 = 100+360
  assert.deepEqual(
    computeResize({ dir: 'n', x: 100, y: 100, width: 480, height: 360, dx: 0, dy: 20, viewport: VP }),
    { x: 100, y: 120, width: 480, height: 340 },
  )
})

test('Panel 缩放：min 尺寸生效；不允许拖出视口', () => {
  const shrunk = computeResize({
    dir: 'se',
    x: 100,
    y: 100,
    width: 480,
    height: 360,
    dx: -1000,
    dy: -1000,
    minWidth: 240,
    minHeight: 120,
    viewport: VP,
  })
  assert.deepEqual(shrunk, { x: 100, y: 100, width: 240, height: 120 })

  const grown = computeResize({ dir: 'se', x: 100, y: 100, width: 480, height: 360, dx: 5000, dy: 5000, viewport: VP })
  assert.deepEqual(grown, { x: 100, y: 100, width: 1100, height: 700 })
})

test('Panel 源码守卫：无蒙板 / 点外部不关闭，激活置顶，内容 = 非池化 view', () => {
  const shell = read('LfPanelShell.vue')
  const manager = read('PanelManager.js')

  // 无蒙板：外壳不渲染遮罩层（overlay / backdrop / mask）
  assert.ok(!/class="[^"]*(overlay|backdrop|mask)/i.test(shell), 'Panel 不应有蒙板')
  assert.ok(!/backdrop-filter/i.test(shell), 'Panel 不应有背景模糊蒙板')

  // 激活置顶：点击 → activate → manager.raise → applyZ 重排
  assert.match(shell, /@mousedown\.capture="onActivate"/, '点击面板需触发激活')
  assert.match(manager, /raise\(id\)/, 'manager 需提供 raise')
  assert.match(manager, /applyZ\(\)/, 'manager 需统一重排 z-order')
  assert.ok(PANEL_Z_BASE > 1500 && PANEL_Z_BASE < 2000, '面板层级须在内容设置(1500)与对话框(2000)之间')

  // 内容 = view（与 dialog 同口径：非池化挂载，隐藏 / 最小化不卸载）
  assert.match(manager, /pooled:\s*false/, '面板 view 必须非池化挂载')
  // 最小化 = 坍缩标题条（尺寸来自 panelOptions 常量）
  assert.match(shell, /PANEL_MINIMIZED_WIDTH/, '最小化宽度取 10em 常量')
  assert.match(shell, /PANEL_MINIMIZED_HEIGHT/, '最小化高度取 1.2em 常量')

  // 回归：maxWidth / maxHeight 缺省（null）不得写成 0px（`Number(null)=0` 会把面板压成 min 尺寸）
  assert.match(shell, /maxWidth:\s*resolvedOptions\.value\.maxWidth\s*\?\s*cssLength/, 'maxWidth 需判空后用 cssLength')
  assert.match(shell, /maxHeight:\s*resolvedOptions\.value\.maxHeight\s*\?\s*cssLength/, 'maxHeight 需判空后用 cssLength')

  // 能力齐备：show/hide/minimize/maximize/restore/move/resize
  for (const fn of ['show', 'hide', 'minimize', 'maximize', 'restore', 'setPosition', 'setSize', 'setZIndex']) {
    assert.match(shell, new RegExp(`function ${fn}\\(`), `外壳缺方法：${fn}`)
  }
  for (const fn of ['showView', 'showContent', 'close', 'raise', 'applyZ']) {
    assert.match(manager, new RegExp(`${fn}\\(`), `管理器缺方法：${fn}`)
  }
})