/**
 * 长会话性能（评审第 11 条）· 共享 1s 时钟。
 *
 * SessionTreeNode 此前每个 running LLM 节点各挂一个 setInterval → N 个 timer。
 * 现收敛为 useSharedNow 的模块级单例：引用计数 0→1 启动唯一 timer，归零清除。
 * 本文件：① 源码守卫（节点组件无 setInterval）；② 1.1s 实测 retain/release 循环。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'
import { useSharedNow } from '../src/composables/useSharedNow.js'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

test('SessionTreeNode：删除本地 setInterval，改 useSharedNow 引用计数', () => {
  const vue = readSrc('views/tasks/SessionTreeNode.vue')
  assert.match(vue, /useSharedNow/, '须接入共享时钟')
  assert.doesNotMatch(vue, /setInterval/, '不得再有每节点定时器')
  assert.doesNotMatch(vue, /syncElapsedTimer/, '旧计时器同步逻辑须删除')
  assert.match(vue, /watch\(isLLMRunning, \(on\) => setClockHeld\(!!on\), \{ immediate: true \}\)/)
  assert.match(vue, /let clockHeld = false/, '须用本地 held 标志保证 retain/release 成对')
  assert.match(vue, /elapsedSec = computed/, '运行时长展示逻辑保留')
})

test('useSharedNow：全文件仅一处 setInterval，release 防负数', () => {
  const src = readSrc('composables/useSharedNow.js')
  const hits = src.match(/setInterval\(/g) || []
  assert.equal(hits.length, 1, '共享时钟只能有 1 个 setInterval')
  assert.match(src, /if \(refs <= 0\) return/, 'release 在计数为 0 时必须 no-op（防负数）')
  assert.match(src, /clearInterval\(timer\)/, '归零时须清除定时器')
})

test('useSharedNow：retain 启表、release 停表、重复 release 不误停', async () => {
  const { now, retain, release } = useSharedNow()
  const t0 = Date.now()
  retain()
  assert.ok(now.value >= t0 - 5 && now.value <= Date.now() + 5, '首个 retain 须同步当前时间')

  await sleep(1200)
  assert.ok(now.value - t0 >= 900, 'retain 后共享 now 须按 1s 节拍推进')

  release()
  const stoppedAt = now.value
  await sleep(1150)
  assert.equal(now.value, stoppedAt, '唯一持有者 release 后定时器停止（now 不再推进）')

  // 防负数 / 幂等：计数已为 0 时重复 release 不得破坏后续启动
  release()
  release()
  retain()
  const t1 = Date.now()
  await sleep(1200)
  assert.ok(now.value - t1 >= 900, '重复 release 后再次 retain 仍须正常启动')
  assert.ok(now.value > stoppedAt, '时钟恢复推进')
  release()
})