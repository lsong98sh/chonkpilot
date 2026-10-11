/**
 * 聊天消息列表「虚拟滚动 / 窗口化渲染」纯逻辑单测（2026-10-11）。
 *
 * 覆盖 utils/virtualWindow.js 的 buildOffsets / computeRange / applyMeasured 边界：
 * 空列表、≤ 阈值（enabled=false、pad=0）、> 阈值（窗口 + 上下 pad、首尾不出界）、
 * 滚动到中部/底部、overscan 生效与钳制、变高 offsets、非法输入安全。
 */
import test from 'node:test'
import assert from 'node:assert/strict'

import {
  VIRTUALIZE_THRESHOLD, OVERSCAN, EST_ITEM_HEIGHT,
  buildOffsets, computeRange, applyMeasured,
} from '../src/utils/virtualWindow.js'

// ── 常量 ──────────────────────────────────────────────────────────────
test('常量：阈值 / 缓冲 / 估算高度为约定值', () => {
  assert.equal(VIRTUALIZE_THRESHOLD, 120)
  assert.equal(OVERSCAN, 8)
  assert.equal(EST_ITEM_HEIGHT, 80)
})

// ── buildOffsets ──────────────────────────────────────────────────────
test('buildOffsets：空前缀和 + 缺失高度回退估算', () => {
  assert.deepEqual(buildOffsets([]), [0])
  assert.deepEqual(buildOffsets(null), [0])
  assert.deepEqual(buildOffsets([10, 20, null, 40]), [0, 10, 30, 110, 150]) // null → 80
  assert.deepEqual(buildOffsets([undefined, 0, -5, NaN, 30]), [0, 80, 160, 240, 320, 350]) // ≤0/NaN 均回退
  assert.deepEqual(buildOffsets([10, 20, null, 40], 5), [0, 10, 30, 35, 75]) // 自定义估算高度
})

// ── computeRange：未启用（≤ 阈值 / 空）────────────────────────────────
test('computeRange：空列表安全（enabled=false，pad=0）', () => {
  const r = computeRange({ total: 0, offsets: [0] })
  assert.deepEqual(r, { enabled: false, start: 0, end: 0, topPad: 0, bottomPad: 0 })
})

test('computeRange：≤ 阈值完全沿用全量渲染（enabled=false，end=total，pad=0）', () => {
  for (const total of [1, 100, VIRTUALIZE_THRESHOLD]) {
    const offsets = buildOffsets(new Array(total))
    const r = computeRange({ total, scrollTop: 1234, viewport: 800, offsets })
    assert.deepEqual(r, { enabled: false, start: 0, end: total, topPad: 0, bottomPad: 0 },
      `total=${total} 须不启用窗口化`)
  }
})

// ── computeRange：启用（> 阈值）──────────────────────────────────────
test('computeRange：> 阈值启用窗口化（顶部：start=0，上下 pad 正确）', () => {
  const total = 300
  const offsets = buildOffsets(new Array(total)) // est=80
  const r = computeRange({ total, scrollTop: 0, viewport: 0, offsets })
  assert.equal(r.enabled, true)
  assert.equal(r.start, 0)
  assert.equal(r.end, 0 + 1 + OVERSCAN) // last=0 → end=1+overscan
  assert.equal(r.topPad, 0)
  assert.equal(r.bottomPad, offsets[total] - offsets[r.end])
  assert.ok(r.end <= total, 'end 不出界')
})

test('computeRange：滚动到中部（窗口 + 上下 pad 精确）', () => {
  const total = 300
  const offsets = buildOffsets(new Array(total)) // est=80 → off[i]=80i
  const r = computeRange({ total, scrollTop: 8000, viewport: 800, offsets })
  assert.equal(r.start, 92)   // first=100 → 100-8
  assert.equal(r.end, 119)    // last=110 → 110+1+8
  assert.equal(r.topPad, offsets[92])            // 7360
  assert.equal(r.bottomPad, offsets[300] - offsets[119]) // 14480
})

test('computeRange：滚动到底部（end=total，bottomPad=0）', () => {
  const total = 300
  const offsets = buildOffsets(new Array(total))
  const r = computeRange({ total, scrollTop: 23500, viewport: 500, offsets })
  assert.ok(r.end === total, 'end 收敛到 total')
  assert.equal(r.bottomPad, 0, '已到末尾 → 底部占位为 0')
  assert.equal(r.start, 286)
  assert.equal(r.end, 300)
  assert.equal(r.topPad, offsets[286])
})

test('computeRange：overscan 生效且首尾钳制（不出界）', () => {
  const total = 300
  const offsets = buildOffsets(new Array(total))
  const top = computeRange({ total, scrollTop: 0, viewport: 800, offsets, overscan: 2 })
  assert.equal(top.start, 0, 'start 钳制到 0')
  assert.equal(top.end, 10 + 1 + 2, 'overscan 生效：end=last+1+overscan')

  const bottom = computeRange({ total, scrollTop: 24000, viewport: 800, offsets, overscan: 20 })
  assert.equal(bottom.end, total, 'end 钳制到 total')
  assert.equal(bottom.bottomPad, 0)
  assert.equal(bottom.start, total - 20, 'start=first-overscan（未越界）')
})

test('computeRange：变高 offsets（非等比）窗口与 pad 一致', () => {
  const heights = [100, 50, 200, 30, 60, 80, 90, 120, 70, 40, 100]
  const total = heights.length
  const offsets = buildOffsets(heights)
  const r = computeRange({ total, scrollTop: 150, viewport: 100, offsets, threshold: 2, overscan: 1 })
  assert.equal(r.enabled, true)
  assert.equal(r.start, 1) // first=2 → 2-1
  assert.equal(r.end, 5)   // last=3 → 3+1+1
  assert.equal(r.topPad, offsets[1])              // 100
  assert.equal(r.bottomPad, offsets[11] - offsets[5]) // 940-440=500
})

test('computeRange：offsets 缺省时用估算兜底（防御性，不抛错）', () => {
  const r = computeRange({ total: 200, scrollTop: 1600, viewport: 800 })
  assert.equal(r.enabled, true)
  assert.ok(r.end > r.start && r.end <= 200)
  assert.ok(r.topPad >= 0 && r.bottomPad >= 0)
})

// ── applyMeasured ─────────────────────────────────────────────────────
test('applyMeasured：原地回填并返回同数组', () => {
  const heights = [1, 2, 3]
  const ret = applyMeasured(heights, 1, 99)
  assert.equal(ret, heights, '返回同一数组（原地更新）')
  assert.equal(heights[1], 99)
  const empty = applyMeasured([], 0, 50)
  assert.deepEqual(empty, [50])
})

test('applyMeasured：非法输入安全（不改动、不抛错）', () => {
  const heights = [1, 2, 3]
  const snap = heights.slice()
  for (const [idx, h] of [[-1, 10], [1.5, 10], [1, 0], [1, -3], [1, NaN], [1, Infinity], [1, 'x']]) {
    assert.equal(applyMeasured(heights, idx, h), heights)
  }
  assert.deepEqual(heights, snap, '非法输入不得改动')
  assert.deepEqual(applyMeasured(null, 0, 10), [], '非数组 → 空数组')
})
