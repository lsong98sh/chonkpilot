/**
 * ckMq（插件 iframe 受控 mq 入口）L1 守卫 —— 2026-10-30 定稿。
 *
 * 覆盖：
 *   ① `new(win)` 只接受宿主窗口的**直接子 frame**，非法 / 跨源抛错（降级选 A：不兜底）；
 *   ② 路由键 = 订阅方 window 引用：订阅按 win 登记，`release(win)` 只清该 win；
 *   ③ 暴露面只有 on / off / emit；emit 转发宿主 mq，release 后 emit 返回 null；
 *   ④ 同一 frame 重载（再次 `new(win)`）隐式作废旧表。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createCkMq } from '../../../../chonkpilot-framework/host/web/src/plugin-ui/ckMq.js'

/** 宿主总线桩：记录订阅 / 发布，可按 topic 注入一次投递。 */
function makeBus() {
  const subs = new Map() // topic -> Set<cb>
  const emitted = []
  return {
    emitted,
    on(topic, cb) {
      if (!subs.has(topic)) subs.set(topic, new Set())
      subs.get(topic).add(cb)
      return () => subs.get(topic)?.delete(cb)
    },
    emit(topic, payload) {
      emitted.push([topic, payload])
      return Promise.resolve({ payload, results: [], backend: null })
    },
    deliver(topic, payload) {
      for (const cb of [...(subs.get(topic) || [])]) cb(payload)
    },
    count(topic) {
      return (subs.get(topic) || new Set()).size
    },
  }
}

const root = { name: 'host' }
const frameA = { name: 'frameA', parent: root }
const frameB = { name: 'frameB', parent: root }

test('① new(win) 只接受宿主窗口的直接子 frame（非法 / 跨源抛错）', () => {
  const ckMq = createCkMq(makeBus(), root)
  assert.throws(() => ckMq.new(root), /直接子 frame/, '宿主自己不是子 frame')
  assert.throws(() => ckMq.new(null), /直接子 frame/)
  assert.throws(() => ckMq.new({ parent: {} }), /直接子 frame/, 'parent 不是宿主窗口')
  // 跨源：读 parent 抛 DOMException → 同样视为非法
  const crossOrigin = {
    get parent() {
      throw new Error('SecurityError')
    },
  }
  assert.throws(() => ckMq.new(crossOrigin), /直接子 frame/)
  assert.doesNotThrow(() => ckMq.new(frameA))
})

test('② 路由键 = 订阅方 window 引用；release(win) 只清该 frame', () => {
  const bus = makeBus()
  const ckMq = createCkMq(bus, root)
  const a = ckMq.new(frameA)
  const b = ckMq.new(frameB)
  const seenA = []
  const seenB = []
  a.on('plugin-status', (p) => seenA.push(p))
  b.on('plugin-status', (p) => seenB.push(p))
  assert.equal(bus.count('plugin-status'), 2)

  bus.deliver('plugin-status', { id: 'codegraph' })
  assert.deepEqual(seenA, [{ id: 'codegraph' }])
  assert.deepEqual(seenB, [{ id: 'codegraph' }])

  ckMq.release(frameA)
  assert.equal(bus.count('plugin-status'), 1, '只退掉 frameA 的订阅')
  bus.deliver('plugin-status', { id: 'codegraph' })
  assert.equal(seenA.length, 1, '释放后 frameA 不再收')
  assert.equal(seenB.length, 2, 'frameB 不受影响')
})

test('③ 暴露面只有 on / off / emit；emit 转发宿主 mq，release 后返回 null', async () => {
  const bus = makeBus()
  const ckMq = createCkMq(bus, root)
  const api = ckMq.new(frameA)
  assert.deepEqual(Object.keys(api).sort(), ['emit', 'off', 'on'], '不得裸暴露宿主 mq 的其它方法')

  const res = await api.emit('plugin-list', {})
  assert.deepEqual(bus.emitted, [['plugin-list', {}]])
  assert.equal(res.backend, null, '返回值 = 宿主 mq 信封')

  const seen = []
  const off = api.on('plugin-status', (p) => seen.push(p))
  off()
  assert.equal(bus.count('plugin-status'), 0, 'on 返回的取消函数须退订')

  api.on('plugin-status', (p) => seen.push(p))
  api.off('plugin-status')
  assert.equal(bus.count('plugin-status'), 0, 'off(topic) 撤该主题全部')

  api.on('plugin-status', (p) => seen.push(p))
  ckMq.release(frameA)
  assert.equal(bus.count('plugin-status'), 0, 'release 清空该 frame 全部订阅')
  assert.equal(await api.emit('plugin-list', {}), null, 'release 后 emit 返回 null')
  assert.equal(typeof api.on('x', () => {}), 'function', 'release 后 on 返回空实现（不抛）')
})

test('④ 同一 frame 再次 new(win)（重载）隐式作废旧表', () => {
  const bus = makeBus()
  const ckMq = createCkMq(bus, root)
  const oldApi = ckMq.new(frameA)
  oldApi.on('plugin-status', () => {})
  assert.equal(bus.count('plugin-status'), 1)

  const newApi = ckMq.new(frameA)
  assert.equal(bus.count('plugin-status'), 0, '重载须先退旧表（旧 JS 上下文不得残留）')
  newApi.on('plugin-status', () => {})
  assert.equal(bus.count('plugin-status'), 1)
})
