/**
 * opened-files 落库顺序回归（2026-11-07 实跑缺陷）。
 *
 * 缺陷现象：重启恢复逐文件 fileOpen（a.txt → b.txt）时，两次全量写并发落库 →
 * 先发出的 `[a]` 后落地，覆盖后发出的 `[a,b]` → `prj opened-files` 退化为单文件
 * （实跑证据：`重启后 prj opened-files 丢失 a.txt/b.txt: first=['a.txt'] final=['a.txt']`）。
 *
 * 断言的是**期望行为**（不从实现反推）：写请求串行执行、最后一次写 = 最新全量列表、
 * 同轮多次调用可合并（中间态不必落库）、单次失败不阻断后续。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createLatestWriteQueue } from '../src/utils/serialWrite.js'

const tick = (ms = 0) => new Promise((r) => setTimeout(r, ms))

test('同轮多次入队：合并为最新一份，中间态不写', async () => {
  const calls = []
  const q = createLatestWriteQueue(async (v) => { calls.push(v) })
  const p1 = q(['a.txt'])
  const p2 = q(['a.txt', 'b.txt'])
  await Promise.all([p1, p2])
  assert.deepEqual(calls, [['a.txt', 'b.txt']])
})

test('逐次入队（跨 tick）：严格串行，最后一次写 = 最新列表（不出现乱序覆盖）', async () => {
  const calls = []
  const q = createLatestWriteQueue(async (v) => {
    calls.push('start:' + v.join(','))
    await tick(20)
    calls.push('end:' + v.join(','))
  })
  q(['a.txt'])
  await tick(0) // 让第一次写真正开始（处于 in-flight）
  await q(['a.txt', 'b.txt'])
  assert.deepEqual(calls, [
    'start:a.txt', 'end:a.txt',
    'start:a.txt,b.txt', 'end:a.txt,b.txt',
  ])
})

test('单次写失败不阻断后续入队（失败被吞，同原 .catch 口径）', async () => {
  let n = 0
  const calls = []
  const q = createLatestWriteQueue(async (v) => {
    n += 1
    calls.push(v)
    if (n === 1) throw new Error('boom')
  })
  q(['a.txt'])
  await tick(0)
  await q(['a.txt', 'b.txt'])
  assert.equal(n, 2)
  assert.deepEqual(calls, [['a.txt'], ['a.txt', 'b.txt']])
})

test('连续多次入队后收敛为最后一次全量列表', async () => {
  const calls = []
  const q = createLatestWriteQueue(async (v) => { calls.push(v) })
  q(['a.txt'])
  q(['a.txt', 'b.txt'])
  q(['a.txt', 'b.txt', 'c.txt'])
  await q(['a.txt', 'b.txt', 'c.txt', 'd.txt'])
  assert.equal(calls.length, 1)
  assert.deepEqual(calls[0], ['a.txt', 'b.txt', 'c.txt', 'd.txt'])
})