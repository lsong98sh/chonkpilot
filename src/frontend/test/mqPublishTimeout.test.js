/**
 * mq.publishToBackend 超时定时器清理（E5-⑤）单测。
 *
 * 缺陷：带 opts.timeout 的 publish 会 `setTimeout(() => c.abort(), timeout)`，但原先未保存 /
 * 未清除句柄 → 请求完成后计时器仍存活（默认 15s/30s），高频请求累积，且可能对已完成请求再
 * abort。修复：保存句柄并在请求收尾（finally）clearTimeout。
 *
 * 注：`utils/mq.js` 只依赖带扩展名的 `./instanceState.js` → Node ESM 可直载；用
 * `globalThis.fetch` 桩替代浏览器环境。
 */
import test from 'node:test'
import assert from 'node:assert/strict'

const mq = (await import('../src/utils/mq.js')).default

/** 装一把定时器桩：记录 setTimeout 的句柄/时长与 clearTimeout 的入参。 */
function withTimerSpy() {
  const set = []
  const cleared = []
  const realSetTimeout = globalThis.setTimeout
  const realClearTimeout = globalThis.clearTimeout
  globalThis.setTimeout = (fn, ms) => {
    const h = realSetTimeout(fn, ms)
    set.push({ h, ms })
    return h
  }
  globalThis.clearTimeout = (h) => {
    cleared.push(h)
    return realClearTimeout(h)
  }
  return {
    set,
    cleared,
    restore() {
      globalThis.setTimeout = realSetTimeout
      globalThis.clearTimeout = realClearTimeout
    },
  }
}

/** 装一个成功应答的 fetch 桩。 */
function withFetchStub() {
  const realFetch = globalThis.fetch
  globalThis.fetch = async () => ({ json: async () => ({ ok: true, result: null, errors: [] }) })
  return () => { globalThis.fetch = realFetch }
}

test('带 timeout 的 publish：请求完成后清理超时定时器（不再累积 / 不误 abort）', async () => {
  const restoreFetch = withFetchStub()
  const spy = withTimerSpy()
  try {
    await mq.emit('data-unknown-topic-for-timeout-test', { a: 1 }, { timeout: 15000 })
    assert.equal(spy.set.length, 1, '带 timeout 的 publish 应恰好装一个定时器')
    assert.equal(spy.set[0].ms, 15000, '定时器时长应为 opts.timeout')
    assert.ok(spy.cleared.includes(spy.set[0].h), '请求完成后须 clearTimeout 该定时器')
  } finally {
    spy.restore()
    restoreFetch()
  }
})

test('不带 timeout 的 publish：不创建 AbortController / 不设定时器', async () => {
  const restoreFetch = withFetchStub()
  const spy = withTimerSpy()
  try {
    await mq.emit('data-unknown-topic-for-timeout-test', { a: 1 })
    assert.equal(spy.set.length, 0, '未指定 timeout 不应创建超时定时器')
  } finally {
    spy.restore()
    restoreFetch()
  }
})
