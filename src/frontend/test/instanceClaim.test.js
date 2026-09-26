/**
 * 实例认领 + 心跳（阶段 2a；61 §4.1 ①②）单测：形态/认证读数（首屏注入 `window.__ck`）、
 * 认领应答 → 本地实例 id、错误码分派（unauthorized / forbidden）、心跳门控（仅分离形态发布）。
 *
 * 注：本文件放在 src 之外（frontend/test/），`npm test` = node:test 直跑（见 package.json）；
 * 用 `globalThis.window` / `globalThis.fetch` 桩替代浏览器环境。
 */
import test from 'node:test'
import assert from 'node:assert/strict'

/** 装一版最小浏览器环境（window.__ck / fetch 桩），返回可写句柄。 */
function setupEnv({ ck = null, bridge = false, backend = { ok: true, result: null, errors: [] } } = {}) {
  const win = {}
  if (bridge) win.__ckHttpBridge = true
  if (ck) win.__ck = ck
  globalThis.window = win
  globalThis.fetch = async () => ({ json: async () => backend })
  return win
}

const { runtimeForm, isSplitForm, injectedInstanceId, currentInstanceId, setClaimedInstance, claimedInstanceId } =
  await import('../src/utils/instanceState.js')
const mq = (await import('../src/utils/mq.js')).default
const { claim, useInstanceClaim, HEARTBEAT_INTERVAL_MS, CLAIM_OK, CLAIM_UNAUTHORIZED, CLAIM_FORBIDDEN } =
  await import('../src/composables/useInstanceClaim.js')

test('① 形态读数：__ck.form 优先；缺注入时回落（shim 标记 = browser，否则 desktop）', () => {
  setupEnv({ ck: { form: 'gui', requireAuth: true, authed: true, instanceId: 'ins-g' } })
  assert.equal(runtimeForm(), 'gui')
  assert.equal(isSplitForm(), true) // gui = 分离形态（-tags split）
  assert.equal(currentInstanceId(), 'ins-g')

  setupEnv({ ck: { form: 'desktop', requireAuth: false, authed: true, instanceId: 'ins-d' } })
  assert.equal(runtimeForm(), 'desktop')
  assert.equal(isSplitForm(), false) // 桌面单体默认构建：不发布心跳、不判超时
  assert.equal(currentInstanceId(), 'ins-d')

  setupEnv({ bridge: true }) // 旧构建（无 __ck）：shim 存在 = browser
  assert.equal(runtimeForm(), 'browser')
  assert.equal(isSplitForm(), true)

  setupEnv({}) // 旧构建 + GUI（无 shim）
  assert.equal(runtimeForm(), 'desktop')
})

test('② 认领成功：应答 {instance_id, work_dir, data_dir} → 存本地（业务 payload 注入取它）', async () => {
  setupEnv({
    ck: { form: 'browser', requireAuth: false, authed: true, instanceId: 'ins-injected' },
    backend: { ok: true, result: { instance_id: 'ins-claimed', work_dir: '/w', data_dir: '/d' }, errors: [] },
  })
  const ok = await claim({})
  assert.equal(ok, true)
  assert.equal(claimedInstanceId(), 'ins-claimed')
  assert.equal(currentInstanceId(), 'ins-claimed') // 认领优先于首屏注入值
  assert.equal(useInstanceClaim().state.value, CLAIM_OK)
  assert.equal(useInstanceClaim().instance.value.workDir, '/w')
  useInstanceClaim().stopHeartbeat() // 清理（browser = 分离形态 → 会起真定时器）
  setClaimedInstance(null) // 复位（模块级单例）
})

test('③ 错误分派：unauthorized / forbidden 各自成态（**不静默**；forbidden 不跳登录）', async () => {
  setupEnv({
    ck: { form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-x' },
    backend: { ok: false, result: { ok: false, error: 'instance-unauthorized' }, errors: ['instance-unauthorized'] },
  })
  await claim({})
  assert.equal(useInstanceClaim().state.value, CLAIM_UNAUTHORIZED)
  assert.equal(useInstanceClaim().errorCode.value, 'instance-unauthorized')

  setupEnv({
    ck: { form: 'browser', requireAuth: false, authed: true, instanceId: 'ins-x' },
    backend: { ok: false, result: { ok: false, error: 'instance-forbidden' }, errors: ['instance-forbidden'] },
  })
  await claim({})
  assert.equal(useInstanceClaim().state.value, CLAIM_FORBIDDEN)
  useInstanceClaim().stopHeartbeat()
  setClaimedInstance(null)
})

test('④ 心跳门控：仅分离形态（gui/browser）claim 成功后起 30s 续期；desktop 不发布', async () => {
  const backend = { ok: true, result: { instance_id: 'ins-hb', work_dir: '/w', data_dir: '/d' }, errors: [] }
  const realSetInterval = globalThis.setInterval
  const realClearInterval = globalThis.clearInterval
  const ticks = []
  let beat = null
  const seen = []
  const off = mq.on('instance-heartbeat', (p) => seen.push(p))
  try {
    globalThis.setInterval = (fn, ms) => {
      ticks.push(ms)
      beat = fn
      return ticks.length
    }
    globalThis.clearInterval = () => {}

    setupEnv({ ck: { form: 'browser', requireAuth: false, authed: true, instanceId: 'ins-hb' }, backend })
    await claim({})
    assert.deepEqual(ticks, [HEARTBEAT_INTERVAL_MS])
    assert.equal(HEARTBEAT_INTERVAL_MS, 30000) // 61 §4.1 ②：30s（服务端 90s 未收即回收）
    assert.equal(typeof beat, 'function')
    beat() // 触发一次续期
    assert.deepEqual(seen, [{ instance_id: 'ins-hb' }]) // payload = {instance_id}（零新增字段）

    ticks.length = 0
    setClaimedInstance(null)
    setupEnv({ ck: { form: 'desktop', requireAuth: false, authed: true, instanceId: 'ins-hb' }, backend })
    await claim({})
    assert.deepEqual(ticks, []) // desktop（默认构建）：不发布心跳（-tags split 门控同口径）
    assert.equal(injectedInstanceId(), 'ins-hb')
  } finally {
    off()
    globalThis.setInterval = realSetInterval
    globalThis.clearInterval = realClearInterval
    setClaimedInstance(null)
  }
})
