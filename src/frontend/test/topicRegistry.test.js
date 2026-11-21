/**
 * 前端本地主题注册表（阶段 3b）L1 守卫。
 *
 * 覆盖：
 *   ① 控制主题内置（scope=global, owner=system）且不可被普通 owner 覆盖；
 *   ② `installTopicSpecs(TOPIC_SPECS)` 装载 61 契约全集（抽查），已存在不覆盖、非法跳过；
 *   ③ `registerTopic` 幂等 / 冲突 / 非法 scope / 通配拒绝 / string 缺省 local；
 *   ④ `unregisterTopic` 本地删除但**订阅保留**；
 *   ⑤ `installLocalEventTopics` 去重、scope=local、不覆盖既有 specs；
 *   ⑥ `sealTopics` 前后：未注册 topic 拒绝（不发后端 / 不本地派发），注册主题照常；
 *      `validateTopics` 精确 / 通配 / 非法 pattern 校验；
 *   ⑦ `resetTopicRegistry` 隔离：清注册表 / seal 状态，**不碰** on 回调；
 *   ⑧ 源码守卫：main.js 装配 specs + local events；App.vue 调 seal；pluginHost.mq 暴露注册 API。
 */
import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'

const mq = (await import('../../../../chonkpilot-framework/host/web/src/utils/mq.js')).default
const { TOPIC_SPECS } = await import('../src/events/topic-specs.generated.js')
const { EventNames } = await import('../src/events/event-names.js')

const CONTROL_TOPICS = [
  'topic-register', 'topic-unregister', 'topic-seal',
  'topic-registered', 'topic-unregistered', 'topic-register-error', 'topic-validation',
]

// ── 桩：fetch（记录后端发布）+ console（校验失败会主动打印，测试期静音） ──
const fetchCalls = []
const warnings = []
const errors = []
const origFetch = globalThis.fetch
const origWarn = console.warn
const origError = console.error

globalThis.fetch = async (url, opts = {}) => {
  let body = null
  try { body = JSON.parse(opts.body) } catch { /* 非 JSON 请求体 */ }
  fetchCalls.push({ url, body })
  return { ok: true, status: 200, json: async () => ({ ok: true, result: null, errors: [] }) }
}
console.warn = (...args) => warnings.push(args)
console.error = (...args) => errors.push(args)

after(() => {
  globalThis.fetch = origFetch
  console.warn = origWarn
  console.error = origError
  mq.resetTopicRegistry()
})

function resetAll() {
  mq.resetTopicRegistry()
  fetchCalls.length = 0
  warnings.length = 0
  errors.length = 0
}

test('① 控制主题内置：scope=global / owner=system，普通 owner 不可覆盖', () => {
  resetAll()
  assert.equal(mq.listTopics().length, CONTROL_TOPICS.length)
  for (const topic of CONTROL_TOPICS) {
    assert.deepEqual(mq.topicSpec(topic), {
      topic, scope: 'global', owner: 'system', localOnly: false,
    })
    assert.equal(mq.isTopicRegistered(topic), true)
  }
  assert.equal(mq.registerTopic('topic-register', { scope: 'global' }), false, '控制主题不可被普通 owner 覆盖')
  assert.equal(mq.unregisterTopic('topic-register'), false, '控制主题不可反注册')
})

test('② installTopicSpecs 装载 61 契约全集（抽查），已存在不覆盖 / 非法跳过', () => {
  resetAll()
  const n = mq.installTopicSpecs(TOPIC_SPECS)
  assert.ok(TOPIC_SPECS.length > 0, '生成物 TOPIC_SPECS 不得为空')
  assert.equal(n, TOPIC_SPECS.length)
  assert.equal(mq.listTopics().length, TOPIC_SPECS.length + CONTROL_TOPICS.length)
  for (const topic of ['session-start', 'inbox-add', 'gui.init-data']) {
    assert.equal(mq.isTopicRegistered(topic), true, `应登记 ${topic}`)
  }
  assert.equal(mq.topicSpec('gui.init-data').scope, 'instance')
  assert.equal(mq.topicSpec('session-start').scope, 'instance')

  // 已存在：不覆盖（即使换 owner / scope）
  assert.equal(mq.installTopicSpecs([{ topic: 'gui.init-data', scope: 'local' }], { owner: 'other' }), 0)
  assert.equal(mq.topicSpec('gui.init-data').scope, 'instance')
  assert.equal(mq.topicSpec('gui.init-data').owner, 'builtin')

  // 非法条目：跳过并 warn
  assert.equal(mq.installTopicSpecs([{ topic: '', scope: 'global' }, { topic: 'bad*', scope: 'global' }, null]), 0)
  assert.ok(warnings.length >= 1, '非法条目须有告警')
  assert.equal(mq.isTopicRegistered('bad*'), false)
})

test('③ registerTopic：幂等 / 冲突 / 非法 scope / 通配拒绝 / string 缺省 local', async () => {
  resetAll()

  assert.equal(mq.registerTopic('custom-topic-1', { scope: 'instance' }), true)
  assert.equal(mq.topicSpec('custom-topic-1').owner, 'frontend')
  assert.equal(mq.topicSpec('custom-topic-1').scope, 'instance')
  assert.equal(mq.topicSpec('custom-topic-1').localOnly, false)
  assert.equal(mq.registerTopic('custom-topic-1', { scope: 'instance' }), true, '同 owner + 同 scope 幂等')

  assert.equal(mq.registerTopic('custom-topic-1', { scope: 'instance', owner: 'plugin:x' }), false, '不同 owner 冲突')
  assert.equal(mq.registerTopic('custom-topic-1', { scope: 'session', owner: 'frontend' }), false, '同 owner 不同 scope 冲突')
  assert.equal(mq.registerTopic('bad*', { scope: 'global' }), false, 'topic 不得含通配符')
  assert.equal(mq.registerTopic('bad-topic', { scope: 'nope' }), false, '非法 scope')
  assert.equal(mq.registerTopic({ topic: 'obj-no-scope' }), false, '对象形式 scope 必填')
  assert.equal(mq.registerTopic(''), false, 'topic 不得为空')

  assert.equal(mq.registerTopic({ topic: 'obj-topic', scope: 'workdir' }), true)
  assert.equal(mq.topicSpec('obj-topic').scope, 'workdir')

  // string 形式缺省 local：不发后端
  const beforeLocal = fetchCalls.length
  assert.equal(mq.registerTopic('local-topic-1'), true)
  assert.equal(mq.topicSpec('local-topic-1').scope, 'local')
  assert.equal(mq.topicSpec('local-topic-1').localOnly, true)
  assert.equal(fetchCalls.length, beforeLocal, 'local scope 不向后端注册')

  // fire-and-forget 的 topic-register（fetch 在 emit 首段同步发起）
  await Promise.resolve()
  const registeredTopics = fetchCalls
    .filter((c) => c.body && c.body.type === 'topic-register')
    .map((c) => JSON.parse(c.body.payload).topic)
    .sort()
  assert.deepEqual(registeredTopics, ['custom-topic-1', 'obj-topic'])
})

test('④ unregisterTopic：本地删除，订阅保留（subPatterns 不清）', async () => {
  resetAll()
  assert.equal(mq.registerTopic('custom-topic-2', { scope: 'instance' }), true)

  const seen = []
  const unsub = mq.on('custom-topic-2', (p) => seen.push(p))
  assert.equal(mq.validateTopics().some((v) => v.pattern === 'custom-topic-2'), false, '已登记 → 无校验错误')

  const beforeUnreg = fetchCalls.length
  assert.equal(mq.unregisterTopic('custom-topic-2'), true)
  assert.equal(mq.isTopicRegistered('custom-topic-2'), false)
  // 订阅未删除 → pattern 仍在 → 校验报未注册
  assert.ok(
    mq.validateTopics().some((v) => v.pattern === 'custom-topic-2' && v.error.includes('未注册')),
    '反注册后订阅 pattern 应保留',
  )
  await Promise.resolve()
  assert.ok(
    fetchCalls.slice(beforeUnreg).some((c) => c.body && c.body.type === 'topic-unregister'),
    '非 local 主题应发 topic-unregister',
  )

  // 未 seal：已有订阅仍可收到派发
  fetchCalls.length = 0
  await mq.emit('custom-topic-2', { x: 1 })
  assert.equal(seen.length, 1)
  unsub()
  assert.equal(mq.validateTopics().some((v) => v.pattern === 'custom-topic-2'), false, '退订后 pattern 清空')

  assert.equal(mq.unregisterTopic('never-registered'), false)
})

test('⑤ installLocalEventTopics：去重 / scope=local / 不覆盖 specs', () => {
  resetAll()
  mq.installTopicSpecs(TOPIC_SPECS)

  const n = mq.installLocalEventTopics(['dup-local', 'dup-local', 'gui.init-data', 'session-changed'])
  assert.equal(n, 2, 'dup-local 去重 + session-changed 新增；gui.init-data 已存在')
  assert.deepEqual(mq.topicSpec('dup-local'), {
    topic: 'dup-local', scope: 'local', owner: 'frontend', localOnly: true,
  })
  assert.equal(mq.topicSpec('session-changed').scope, 'local')
  assert.equal(mq.topicSpec('gui.init-data').scope, 'instance', '已登记 specs 不被 local 覆盖')

  // 全量 EventNames：值可能重复；已登记（含 61 契约）不覆盖
  const n2 = mq.installLocalEventTopics(Object.values(EventNames))
  assert.ok(n2 > 100, `前端本地事件应登记（新增 ${n2}）`)
  assert.equal(mq.topicSpec('llm-start').scope, 'local')
  assert.equal(mq.topicSpec('gui.window.status').scope, 'instance', 'framework 键已属 specs 时不被改')
})

test('⑥ seal 前后：未注册 topic 拒绝；validateTopics 精确 / 通配 / 非法 pattern', async () => {
  resetAll()
  mq.installTopicSpecs(TOPIC_SPECS)

  const seen = []
  const unknownSeen = []
  const unsubs = [
    mq.on('session-start', (p) => seen.push(p)),
    mq.on('gui.*', () => {}), // 合法通配（命中 gui.init-data 等）
    mq.on('nope.*', () => {}), // 未匹配任何已登记主题
    mq.on('a..b', () => {}), // 空段
    mq.on('x.>.*', () => {}), // `>` 非最后一段
    mq.on('unknown-topic-xyz', (p) => unknownSeen.push(p)),
  ]

  // seal 前：未注册 topic 放行（发后端 + 本地派发）
  await mq.emit('unknown-topic-xyz', { a: 1 })
  assert.equal(unknownSeen.length, 1)
  assert.equal(fetchCalls.length, 1)
  assert.equal(fetchCalls[0].body.type, 'unknown-topic-xyz')

  const sealP = mq.sealTopics()
  const validation = mq.validateTopics()
  const byPattern = Object.fromEntries(validation.map((v) => [v.pattern, v.error]))
  assert.equal(byPattern['session-start'], undefined, '已登记精确 pattern 无错误')
  assert.equal(byPattern['gui.*'], undefined, '命中已登记主题的通配无错误')
  assert.match(byPattern['nope.*'], /未匹配/)
  assert.match(byPattern['a..b'], /空段/)
  assert.match(byPattern['x.>.*'], /最后一段/)
  assert.match(byPattern['unknown-topic-xyz'], /未注册/)
  await sealP
  assert.ok(errors.length >= 4, 'seal 应汇总打印校验失败')

  // sealed 后：未注册 topic 拒绝（不发后端 / 不本地派发 / errors 返回）
  fetchCalls.length = 0
  const blocked = await mq.emit('unknown-topic-xyz', { a: 2 })
  assert.deepEqual(blocked.results, [])
  assert.equal(blocked.backend, null)
  assert.match(blocked.errors[0], /未注册/)
  assert.equal(unknownSeen.length, 1, 'sealed 后不得本地派发')
  assert.equal(fetchCalls.length, 0, 'sealed 后不得发后端')

  // 已登记主题照常（pre/on/... + 后端）
  fetchCalls.length = 0
  await mq.emit('session-start', { a: 3 })
  assert.equal(seen.length, 1)
  assert.equal(fetchCalls.length, 1)
  assert.equal(fetchCalls[0].body.type, 'session-start')

  // 控制主题已登记：seal / register 等正常发布
  assert.equal(mq.isTopicRegistered('topic-seal'), true)

  for (const u of unsubs) u()
})

test('⑦ resetTopicRegistry：清注册表 / seal 状态，但不碰 on 回调', async () => {
  resetAll()
  mq.installTopicSpecs(TOPIC_SPECS)
  mq.registerTopic('tmp-topic', { scope: 'global' })
  mq.sealTopics()

  const seen = []
  const unsub = mq.on('session-start', () => seen.push(1))
  mq.resetTopicRegistry()

  assert.equal(mq.listTopics().length, CONTROL_TOPICS.length)
  assert.equal(mq.isTopicRegistered('tmp-topic'), false)
  assert.deepEqual(mq.validateTopics(), [], 'subPatterns 已清空')
  for (const topic of CONTROL_TOPICS) assert.equal(mq.isTopicRegistered(topic), true, '控制主题须重新内置')

  // seal 状态已复位 + on 回调未被 reset 清除
  fetchCalls.length = 0
  await mq.emit('session-start', {})
  assert.equal(seen.length, 1, 'reset 不得清除既有 on 回调')
  assert.equal(fetchCalls.length, 1, 'seal 状态复位后照常发后端')
  unsub()
})

test('⑦b 订阅计数：on 重复注册同一 callback 只计一次；clear 清零', () => {
  resetAll()
  const cb = () => {}
  const u1 = mq.on('count-topic', cb)
  const u2 = mq.on('count-topic', cb) // 重复注册 → 仅更新 order，不重复计数
  assert.equal(
    mq.validateTopics().filter((v) => v.pattern === 'count-topic').length,
    1,
    'pattern 只应出现一次',
  )
  u1()
  assert.equal(
    mq.validateTopics().some((v) => v.pattern === 'count-topic'),
    false,
    '重复注册也只计一次，退订一次即归零',
  )
  u2() // 幂等：已无可移除回调

  const u3 = mq.on('count-topic-2', cb)
  assert.equal(mq.validateTopics().some((v) => v.pattern === 'count-topic-2'), true)
  mq.clear('count-topic-2')
  assert.equal(mq.validateTopics().some((v) => v.pattern === 'count-topic-2'), false, 'clear 清零 pattern')
  u3()
})

test('⑧ 源码守卫：main.js 装配、App.vue seal、pluginHost.mq 暴露注册 API', () => {
  const main = readSrc('main.js')
  assert.match(main, /import\s*\{[^}]*TOPIC_SPECS[^}]*\}\s*from\s*['"]\.\/events\/topic-specs\.generated\.js['"]/)
  assert.match(main, /mq\.installTopicSpecs\(TOPIC_SPECS\)/)
  // schema clientTopic（tools-list 等）不进 TOPIC_SPECS（仅总线主题）→ main.js 须用生成的
  // MsgClientTopics 补登记，否则 seal 后 emit 被拒（「topic 未注册（已 sealed）」）。
  assert.match(main, /import\s*\{[^}]*MsgClientTopics[^}]*\}\s*from\s*['"]\.\/events\/msgkeys\.js['"]/)
  assert.match(main, /mq\.installTopicSpecs\(Object\.values\(MsgClientTopics\)/)
  assert.match(main, /mq\.installLocalEventTopics\(Object\.values\(EventNames\)\)/)

  const app = readSrc('App.vue')
  assert.match(app, /mq\.sealTopics\(\)/, '插件就绪后须触发 seal')

  const pluginHost = readSrc('pluginHost.js')
  for (const key of ['registerTopic', 'unregisterTopic', 'topicSpec', 'listTopics']) {
    assert.match(pluginHost, new RegExp('\\b' + key + '\\s*:'), `pluginHost.mq 须暴露 ${key}`)
  }
  assert.match(pluginHost, /\bon:\s*\(\.\.\.args\)\s*=>\s*mq\.on/, '既有 on 保留')
  assert.match(pluginHost, /\bemit:\s*\(\.\.\.args\)\s*=>\s*mq\.emit/, '既有 emit 保留')
})