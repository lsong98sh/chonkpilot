/**
 * 弹窗位置计算（纯函数；AskUser S1-6，2026-10-29）。
 *
 * 语义（`docs/ask-user/AskUser专题设计方案.md` §4.6）：ask 弹窗**一律居中于整个桌面（屏幕工作区）
 * 的正中** —— 不是「窗口内居中」，不相对父窗口、**不记忆上次拖拽位置**、不随机偏移；
 * 每次弹出都重新居中。工作区 = 屏幕去掉任务栏的可用区（`screen.avail*`）。
 *
 * 多显示器：取**弹窗所在窗口当前显示器**的工作区（`window.screen` 即当前窗口所在屏幕）；
 * 取不到（无 `screen` / 非法值）→ 回落「视口内居中」。
 *
 * 单位说明（DPI / 浏览器缩放）：Chromium 的 `screen.*` 与 `screenX/screenY/innerWidth` 同为
 * **CSS 像素**（浏览器缩放已折进 CSS 像素）→ 直接相减即为同一坐标系，**不得**再乘
 * `devicePixelRatio`（否则二次缩放）。
 *
 * 本模块为**叶子模块**（无 import、无副作用）→ 可直跑单测（`npm test`）。
 */

/** 数值化：非有限数（undefined / NaN / Infinity）→ `null`（调用方据此判定"不可用"）。 */
function num(v) {
  const n = Number(v)
  return Number.isFinite(n) ? n : null
}

function clamp(v, lo, hi) {
  if (hi < lo) return lo
  return Math.min(Math.max(v, lo), hi)
}

/**
 * 屏幕工作区换算到**视口坐标系**（`position: fixed` 的 left/top 基准）。
 *
 * @param {object} o
 * @param {{availLeft?:number, availTop?:number, availWidth?:number, availHeight?:number,
 *          screenX?:number, screenY?:number, outerWidth?:number, outerHeight?:number}} o.screen
 * @param {{innerWidth?:number, innerHeight?:number}} o.viewport
 * @returns {{left:number, top:number, width:number, height:number}|null} null = 无法判定（回落）
 */
export function workAreaInClient({ screen = {}, viewport = {} } = {}) {
  const availLeft = num(screen.availLeft)
  const availTop = num(screen.availTop)
  const availWidth = num(screen.availWidth)
  const availHeight = num(screen.availHeight)
  if (availLeft === null || availTop === null || availWidth === null || availHeight === null) return null
  if (availWidth <= 0 || availHeight <= 0) return null

  // 视口起点 = 窗口外框左上角（screenX/screenY）+ 装饰：左右边框对称 → 各半；
  // 上下装饰（标题栏 / 边框）整体在上 → 全量计入 top。
  const screenX = num(screen.screenX) ?? 0
  const screenY = num(screen.screenY) ?? 0
  const innerW = num(viewport.innerWidth)
  const innerH = num(viewport.innerHeight)
  const outerW = num(screen.outerWidth)
  const outerH = num(screen.outerHeight)
  const dx = innerW !== null && outerW !== null ? Math.max(0, (outerW - innerW) / 2) : 0
  const dy = innerH !== null && outerH !== null ? Math.max(0, outerH - innerH) : 0

  return {
    left: availLeft - (screenX + dx),
    top: availTop - (screenY + dy),
    width: availWidth,
    height: availHeight,
  }
}

/**
 * 弹窗「屏幕工作区居中」位置（视口内 clamp 兜底）。
 *
 * @param {object} o
 * @param {number} o.width 弹窗宽（CSS px）
 * @param {number} o.height 弹窗高（CSS px）
 * @param {{innerWidth:number, innerHeight:number}} o.viewport 视口尺寸
 * @param {object} o.screen `window.screen` + 窗口位置的快照（见 `workAreaInClient`）
 * @returns {{left:number, top:number}}
 */
export function computeScreenCenteredPosition({ width, height, viewport = {}, screen = {} } = {}) {
  const vw = num(viewport.innerWidth) ?? 0
  const vh = num(viewport.innerHeight) ?? 0
  const w = Math.max(0, num(width) ?? 0)
  const h = Math.max(0, num(height) ?? 0)

  const area = workAreaInClient({ screen, viewport })
  let left
  let top
  if (area) {
    // 工作区中心（视口坐标系）→ 弹窗左上角
    left = Math.round(area.left + area.width / 2 - w / 2)
    top = Math.round(area.top + area.height / 2 - h / 2)
  } else {
    // 无法判定屏幕工作区 → 回落窗口内居中（与既有行为同口径）
    left = Math.round((vw - w) / 2)
    top = Math.round((vh - h) / 2)
  }

  // 视口内 clamp 兜底：保证整窗可见（弹窗比视口大 → 顶到 0，由 max-height/max-width 内滚动兜底）
  return {
    left: clamp(left, 0, Math.max(0, vw - w)),
    top: clamp(top, 0, Math.max(0, vh - h)),
  }
}
