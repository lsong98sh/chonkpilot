/**
 * ask 弹窗「屏幕工作区居中」位置算法单测（AskUser S1-6，2026-10-29）。
 *
 * 口径（`docs/ask-user/AskUser专题设计方案.md` §4.6）：卡片中心 = **屏幕工作区**中心
 * （不是窗口内居中），不记忆上次拖拽位置，每次弹出重新居中；无法判定屏幕工作区 → 回落
 * 视口内居中；最后在视口内 clamp 兜底。
 *
 * 纯函数在 `src/utils/dialogPosition.js`（叶子模块，无浏览器依赖）→ 直接 import 断言。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { computeScreenCenteredPosition, workAreaInClient } from '../src/utils/dialogPosition.js'

// 典型单屏：1920×1080，任务栏 40px（工作区高 1040）；主窗口 1200×900 在 (0,0)，
// 标题栏 40px → 视口 1200×860，视口起点 = (0, 40)。
const SCREEN_1080 = { availLeft: 0, availTop: 0, availWidth: 1920, availHeight: 1040 }
const WIN_1200 = {
  ...SCREEN_1080,
  screenX: 0, screenY: 0, outerWidth: 1200, outerHeight: 900,
}
const VIEW_1200 = { innerWidth: 1200, innerHeight: 860 }

test('① 工作区换算到视口坐标系：扣掉窗口位置与标题栏（左右边框对称各半）', () => {
  const a = workAreaInClient({ screen: WIN_1200, viewport: VIEW_1200 })
  assert.deepEqual(a, { left: 0, top: -40, width: 1920, height: 1040 })
})

test('① 工作区缺失 / 非法 → null（调用方回落视口内居中）', () => {
  assert.equal(workAreaInClient({ screen: {}, viewport: VIEW_1200 }), null)
  assert.equal(workAreaInClient({ screen: { ...SCREEN_1080, availWidth: 0 }, viewport: VIEW_1200 }), null)
})

test('② 屏幕工作区居中：卡片中心 = 工作区中心（窗口未最大化也按屏幕算）', () => {
  // 工作区中心（屏幕）= (960, 520) → 视口坐标 (960, 480) → 卡片 480×300 → 左上 (720, 330)
  const p = computeScreenCenteredPosition({ width: 480, height: 300, viewport: VIEW_1200, screen: WIN_1200 })
  assert.deepEqual(p, { left: 720, top: 330 })
  // 卡片中心回算 = 工作区中心（视口坐标），容差 0（纯整数四舍五入）
  assert.equal(p.left + 480 / 2, 960)
  assert.equal(p.top + 300 / 2, 480)
})

test('② 最大化窗口：工作区 = 整屏可用区，仍为屏幕居中', () => {
  const screen = { ...SCREEN_1080, screenX: 0, screenY: 0, outerWidth: 1920, outerHeight: 1040 }
  const p = computeScreenCenteredPosition({ width: 480, height: 300, viewport: { innerWidth: 1920, innerHeight: 1040 }, screen })
  assert.deepEqual(p, { left: 720, top: 370 }) // 中心 (960,520) - (240,150)
})

test('② 多显示器：取当前窗口所在屏幕的工作区（availLeft 偏移被折算掉）', () => {
  const screen = { availLeft: 1920, availTop: 0, availWidth: 1920, availHeight: 1040, screenX: 1920, screenY: 0, outerWidth: 1200, outerHeight: 900 }
  const p = computeScreenCenteredPosition({ width: 480, height: 300, viewport: VIEW_1200, screen })
  assert.deepEqual(p, { left: 720, top: 330 })
})

test('③ 视口内 clamp 兜底：窗口偏右下时卡片不越界（负值/超界 → 贴边）', () => {
  const screen = { ...SCREEN_1080, screenX: 1000, screenY: 200, outerWidth: 920, outerHeight: 900 }
  const p = computeScreenCenteredPosition({ width: 480, height: 300, viewport: { innerWidth: 920, innerHeight: 660 }, screen })
  assert.equal(p.left, 0, '工作区中心落在视口左侧之外 → 贴左边界')
  assert.equal(p.top, 0, '工作区中心落在视口上方之外 → 贴上边界')
})

test('③ 弹窗大于视口 → 顶到 0（由 max-height/max-width 内滚动兜底）', () => {
  const p = computeScreenCenteredPosition({ width: 2000, height: 2000, viewport: { innerWidth: 1000, innerHeight: 800 }, screen: WIN_1200 })
  assert.deepEqual(p, { left: 0, top: 0 })
})

test('④ 无法判定屏幕工作区 → 回落视口内居中（既有行为）', () => {
  const p = computeScreenCenteredPosition({ width: 400, height: 200, viewport: { innerWidth: 1000, innerHeight: 800 }, screen: {} })
  assert.deepEqual(p, { left: 300, top: 300 })
})

test('⑤ 纯函数：同输入同输出（每次弹出重新居中，无状态/无缓存）', () => {
  const a = computeScreenCenteredPosition({ width: 480, height: 300, viewport: VIEW_1200, screen: WIN_1200 })
  const b = computeScreenCenteredPosition({ width: 480, height: 300, viewport: VIEW_1200, screen: WIN_1200 })
  assert.deepEqual(a, b)
})
