/**
 * HTTP/SSE 版桥 shim 守卫（browser 形态；19 §8 方案 A 的最小切片 D）。
 *
 * 被测对象 = 服务端用 go:embed 提供、并在 index.html 注入的
 * `chonkpilot-llm/httpapi/ck_http_bridge.js`（前端源码零改动）。
 * 断言：SSE 解析（data 行 JSON）→ 投递到既有入口 window.mq.emitRemote（信封原样）；
 * 模块脚本尚未执行时排队，待 window.mq 就绪后按序补投；坏 JSON / 空 data 不投递。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import vm from 'node:vm'

const SHIM_PATH = fileURLToPath(new URL('../../lib/llm/httpapi/ck_http_bridge.js', import.meta.url))
const SHIM_SRC = readFileSync(SHIM_PATH, 'utf8')

/** 造一个最小浏览器沙箱并执行 shim，返回可观测句柄。 */
function runShim({ mqReady = true } = {}) {
  const opened = []
  const deliveries = []
  const timers = []
  class FakeEventSource {
    constructor(url) {
      this.url = url
      this.onmessage = null
      opened.push(this)
    }
  }
  const win = {}
  if (mqReady) win.mq = { emitRemote: (env) => deliveries.push(env) }
  const sandbox = {
    window: win,
    EventSource: FakeEventSource,
    console: { warn() {} },
    setTimeout: (fn, ms) => {
      timers.push({ fn, ms })
      return timers.length
    },
    clearTimeout: () => {},
  }
  vm.runInNewContext(SHIM_SRC, sandbox)
  return { win, opened, deliveries, timers }
}

test('shim 打开 SSE（/events）并只开一次', () => {
  const { win, opened } = runShim()
  assert.equal(opened.length, 1)
  assert.equal(opened[0].url, '/events')
  assert.equal(win.__ckHttpBridge, true)
})

test('SSE data → 解析后原样投递到 window.mq.emitRemote', () => {
  const { opened, deliveries } = runShim()
  const env = { type: 'data-prj-config-refresh', payload: '{"id":"x"}', src: 'mq' }
  opened[0].onmessage({ data: JSON.stringify(env) })
  // 跨 vm realm 对象原型不同 → 按 JSON 值比较（字段与值一致即可）
  assert.equal(JSON.stringify(deliveries), JSON.stringify([env]))
})

test('坏 JSON / 空 data 不投递（不抛错）', () => {
  const { opened, deliveries } = runShim()
  opened[0].onmessage({ data: 'not-json' })
  opened[0].onmessage({ data: '' })
  opened[0].onmessage({})
  assert.equal(deliveries.length, 0)
})

test('window.mq 未就绪 → 事件排队，就绪后按序补投', () => {
  const { win, opened, deliveries, timers } = runShim({ mqReady: false })
  const e1 = { type: 'llm-receive', payload: '{"a":1}', src: 'mq' }
  const e2 = { type: 'tasks.updated', payload: '{"b":2}', src: 'mq' }
  opened[0].onmessage({ data: JSON.stringify(e1) })
  opened[0].onmessage({ data: JSON.stringify(e2) })
  assert.equal(deliveries.length, 0)
  assert.ok(timers.length >= 1, '未就绪时应排一次补投定时器')
  // 模块脚本执行（main.js 挂 window.mq）→ 定时器触发补投，顺序不变
  win.mq = { emitRemote: (env) => deliveries.push(env) }
  timers[0].fn()
  assert.equal(JSON.stringify(deliveries), JSON.stringify([e1, e2]))
})

test('window.mq 就绪时不排队（不排定时器）', () => {
  const { opened, timers } = runShim()
  opened[0].onmessage({ data: JSON.stringify({ type: 'x', payload: '{}', src: 'mq' }) })
  assert.equal(timers.length, 0)
})
