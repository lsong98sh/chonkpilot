/**
 * 阶段1「前端状态注册 v1」单测：
 *   ① ckStore 状态注册（create / ref / set / getSync / destroy / onChange / alias / definitions）；
 *   ② hostState 安装 + selection owner 侧发布（canonical 与 layout-free 兼容 alias 同值）；
 *   ③ 既有 CK Store getter 兼容（register/get/listKeys/unregister）+ 状态优先；
 *   ④ main.js 装配顺序 / setHostDeps.state 源码守卫。
 */
import { test, after } from 'node:test'
import assert from 'node:assert/strict'
import { createPinia, setActivePinia } from 'pinia'
import {
  create,
  ref,
  set,
  getSync,
  get,
  destroy,
  alias,
  definitions,
  onChange,
  listStateKeys,
  register,
  unregister,
  ckStore,
} from '../src/utils/ckStore.js'
import { setLayoutDeps } from '@ckfw/layout-free/deps.js'
import { useKeysStore } from '@ckfw/layout-free/stores/keys.js'
import { installHostStates, publishHostState } from '../src/stores/hostState.js'
import { useSelectionStore } from '../src/stores/selection.js'
import { readSrc } from './support/feSource.mjs'

const TEST_KEYS = [
  'test.hs.reg',
  'test.hs.dup',
  'test.hs.alias.target',
  'test.hs.alias.view',
  'test.hs.priority',
  'test.hs.legacy',
  'test.hs.persist',
]

/** 临时静音 console.warn（只读 ref 赋值 / 重复注册会告警）。 */
function withWarn(fn) {
  const warns = []
  const orig = console.warn
  console.warn = (...args) => warns.push(args.join(' '))
  try {
    fn()
  } finally {
    console.warn = orig
  }
  return warns
}

after(() => {
  for (const key of TEST_KEYS) destroy(key)
  for (const key of TEST_KEYS) unregister(key)
})

// ── ① 状态注册基础行为 ──────────────────────────────────────────────

test('状态：create / ref 稳定只读 / set 更新 / destroy 幂等', () => {
  const key = 'test.hs.reg'
  const handle = create(key, {
    initial: 'a',
    defaultValue: 'a',
    label: '测试状态',
    description: '测试描述',
    owner: 'host',
    scope: 'window',
    persist: 'none',
  })
  assert.equal(handle.key, key)
  assert.equal(handle.get(), 'a')
  assert.equal(handle.value, 'a')

  const r1 = ref(key)
  const r2 = ref(key)
  assert.equal(r1, r2, '同一 key 多次 ref() 返回同一只读 ref')
  assert.equal(r1.value, 'a')
  // 消费者写只读 ref 无效（Vue 会告警，这里静音）。
  withWarn(() => {
    r1.value = 'bad'
  })
  assert.equal(getSync(key), 'a', '只读 ref 写入不得改变状态')

  assert.equal(set(key, 'b'), true)
  assert.equal(r1.value, 'b', 'set 后 ref 更新')
  assert.equal(handle.value, 'b')

  handle.value = 'c'
  assert.equal(getSync(key), 'c', 'handle.value 写入走 set 路径')

  assert.equal(handle.set('d'), true)
  assert.equal(getSync(key), 'd')

  handle.ref.value = 'e'
  assert.equal(getSync(key), 'e', 'handle.ref 可写，且写入走 set 路径')

  assert.equal(handle.destroy(), true)
  assert.equal(handle.destroy(), false, 'destroy 幂等')
  assert.equal(r1.value, undefined, '既有 ref 保留、值置 undefined（不崩）')
  assert.equal(getSync(key), undefined)
  assert.equal(ref(key), null, 'destroy 后 ref(key) → null')

  assert.equal(set(key, 'x'), false, '写入不存在状态 → false')
})

test('状态：重复 create 保留先注册者并告警（幂等返回已有 handle）', () => {
  const key = 'test.hs.dup'
  const first = create(key, { initial: 1 })
  let second
  const warns = withWarn(() => {
    second = create(key, { initial: 2 })
  })
  assert.equal(second, first, '重复 create 返回已有 handle')
  assert.equal(getSync(key), 1, '先注册者的值不被覆盖')
  assert.ok(warns.some((w) => w.includes(key)), '重复注册应告警')
  destroy(key)
})

test('状态：onChange 只由 set 触发；alias 解引用到目标', () => {
  const target = 'test.hs.alias.target'
  const view = 'test.hs.alias.view'
  create(target, { initial: 't0', label: '目标' })
  assert.equal(alias(view, target, { label: '别名', hidden: true }), true)

  assert.equal(ref(view), ref(target), 'alias 的 ref 与目标同一对象')
  assert.equal(getSync(view), 't0')

  const seen = []
  const un = onChange(view, (value) => seen.push(value))
  set(target, 't1')
  set(view, 't2')
  assert.deepEqual(seen, ['t1', 't2'], 'alias 订阅收到目标 set 通知')
  un()
  set(target, 't3')
  assert.deepEqual(seen, ['t1', 't2'], '退订后不再通知')

  const def = definitions().find((d) => d.key === view)
  assert.ok(def, 'definitions 含 alias 元数据')
  assert.equal(def.aliasOf, target)
  assert.equal(def.hidden, true)

  destroy(view)
  destroy(target)
  assert.equal(ref(view), null, 'canonical 删除后 alias 一并移除（不再悬空）')
  assert.equal(getSync(view), undefined)
})

test('状态：persist=session 走 chonkpilot.state.* 前缀，create 时优先恢复', () => {
  const key = 'test.hs.persist'
  const storageKey = `chonkpilot.state.${key}`
  const map = new Map([[storageKey, JSON.stringify('restored')]])
  const saved = globalThis.localStorage
  globalThis.localStorage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
  }
  try {
    const handle = create(key, { initial: 'init', persist: 'session' })
    assert.equal(handle.get(), 'restored', 'create 优先读持久化值')
    set(key, 'next')
    assert.equal(map.get(storageKey), JSON.stringify('next'), 'set 写入带前缀的 localStorage')
    assert.equal(handle.destroy(), true)
    assert.ok(!map.has(storageKey), 'destroy 清除持久化项')
  } finally {
    if (saved === undefined) delete globalThis.localStorage
    else globalThis.localStorage = saved
  }
})

// ── ② hostState 安装 + selection 发布 ──────────────────────────────

test('publishHostState：未安装 hostState 时安全 no-op', () => {
  assert.equal(publishHostState('test.hs.not-installed', 1), false)
})

test('installHostStates：canonical + alias 同底层值；selection setter 持续更新 ref', () => {
  setActivePinia(createPinia())
  const selection = useSelectionStore()

  // owner 侧先有值；install 用当前值做 initial。
  selection.setWorkDir('E:/proj')
  selection.setActiveSession('s-1')
  selection.setActiveFile({ path: 'E:/proj/a.md', renderType: 'markdown' })
  selection.setActiveTaskId('t-1')
  selection.setExplorerMode('project')

  installHostStates(selection)

  assert.equal(ref('workdir').value, 'E:/proj')
  assert.equal(ref('project').value, 'E:/proj', 'project alias → workdir')
  assert.equal(ref('selected-session').value, 's-1')
  assert.equal(ref('sessionId').value, 's-1')
  assert.equal(ref('sessionId'), ref('selected-session'), 'alias 与 canonical 同一 ref')
  assert.equal(ref('active-file-path').value, 'E:/proj/a.md')
  assert.equal(ref('filePath').value, 'E:/proj/a.md')
  assert.equal(ref('active-task-id').value, 't-1')
  assert.equal(ref('taskId').value, 't-1')
  assert.equal(ref('explorer-mode').value, 'project')
  assert.equal(ref('mcpServer').value, 'filesystem')
  assert.equal(ref('model').value, 'deepseek-chat')
  assert.equal(ref('tabGroup').value, 'main')
  assert.equal(ref('searchBox').value, 'main')

  // selection setter → registry ref 更新（owner 写入投影）。
  selection.setActiveSession('s-2')
  assert.equal(ref('selected-session').value, 's-2')
  assert.equal(ref('sessionId').value, 's-2')

  selection.setActiveSubSession('ss-1')
  assert.equal(ref('active-task-id').value, '', 'subsession-changed 清 taskId')
  assert.equal(ref('taskId').value, '')

  selection.setActiveTaskId('t-2')
  assert.equal(ref('active-task-id').value, 't-2')
  assert.equal(ref('taskId').value, 't-2')

  selection.setActiveFile({ path: 'E:/proj/b.md', renderType: 'markdown' })
  assert.equal(ref('filePath').value, 'E:/proj/b.md')

  selection.setWorkDir('')
  assert.equal(ref('project').value, '')

  selection.setExplorerMode('memory')
  assert.equal(ref('explorer-mode').value, 'memory')
  selection.setExplorerMode('knowledge')
  assert.equal(ref('explorer-mode').value, 'memory', '非法模式不写入')

  // 反向：外部 owner 经状态 API 写 alias，ref 可读（alias 解引用）。
  assert.equal(set('sessionId', 's-3'), true)
  assert.equal(ref('selected-session').value, 's-3')

  selection.reset()
  assert.equal(ref('workdir').value, '')
  assert.equal(ref('selected-session').value, '')
  assert.equal(ref('active-file-path').value, '')
  assert.equal(ref('active-task-id').value, '')
  assert.equal(ref('explorer-mode').value, 'workspaces')
})

test('definitions：包含新旧 key 且无重复；旧 key 仍隐藏可用', () => {
  installHostStates() // 幂等：只补缺，不告警、不覆盖
  const defs = definitions()
  const keys = defs.map((d) => d.key)

  for (const key of [
    'workdir',
    'selected-session',
    'active-file-path',
    'active-task-id',
    'explorer-mode',
    'mcpServer',
    'model',
    'tabGroup',
    'searchBox',
    'project',
    'sessionId',
    'filePath',
    'taskId',
  ]) {
    assert.ok(keys.includes(key), `definitions 缺少 ${key}`)
  }
  assert.equal(new Set(keys).size, keys.length, 'definitions 中 key 不得重复')

  for (const legacy of ['project', 'sessionId', 'filePath', 'taskId']) {
    const d = defs.find((x) => x.key === legacy)
    assert.equal(d.hidden, true, `${legacy} 应隐藏（不出现在变量下拉）`)
    assert.ok(d.aliasOf, `${legacy} 应声明 aliasOf`)
  }
  // 通过 alias 仍能取值（install 后的 canonical 仍在）。
  assert.equal(typeof getSync('sessionId'), 'string')
  assert.ok(listStateKeys().includes('sessionId'))
})

// ── ③ 既有 getter 兼容 + 状态优先 ──────────────────────────────────

test('兼容：legacy register/get/listKeys/unregister 不回归；状态优先于 getter', async () => {
  const legacy = 'test.hs.legacy'
  const off = register(legacy, () => 'legacy-value')
  assert.equal(await get(legacy), 'legacy-value')
  assert.ok(listStateKeys().includes('workdir'))
  off()
  assert.equal(await get(legacy), null)

  create('test.hs.priority', { initial: 'state-value' })
  const off2 = register('test.hs.priority', () => 'getter-value')
  assert.equal(await get('test.hs.priority'), 'state-value', '状态优先返回当前值')
  assert.equal(getSync('test.hs.priority'), 'state-value')
  off2()
  destroy('test.hs.priority')
})

// ── ④ 装配守卫（源码）──────────────────────────────────────────────

test('装配守卫：main.js 顺序 pinia → subscribers → global store → hostState → layout engine', () => {
  const main = readSrc('main.js')
  const iPinia = main.indexOf('app.use(createPinia())')
  const iSubs = main.indexOf('registerSelectionSubscribers()')
  const iGlobal = main.indexOf('installGlobalCkStore()')
  const iHost = main.indexOf('installHostStates(')
  const iSetup = main.indexOf('setupLayoutEngine(app)')

  assert.ok(iPinia >= 0, '缺 createPinia')
  assert.ok(iSubs > iPinia, 'selection subscribers 须在 pinia 之后')
  assert.ok(iGlobal > iSubs, 'installGlobalCkStore 须在 subscribers 之后')
  assert.ok(iHost > iGlobal, 'installHostStates 须在 installGlobalCkStore 之后')
  assert.ok(iSetup > iHost, 'setupLayoutEngine 须在 installHostStates 之后')

  assert.match(main, /from '\.\/stores\/hostState'/, 'main.js 须 import installHostStates')
  assert.match(main, /installHostStates\(useSelectionStore\(\)\)/, '须用 active pinia 的 selection 初始化')
  assert.match(main, /state:\s*ckStore/, 'setHostDeps 须注入 state: ckStore')

  for (const key of ['workspace.current', 'chat.currentSession', 'preview.currentFile']) {
    assert.ok(main.includes(`ckStore.register('${key}'`), `既有示范 getter 不得移除: ${key}`)
  }
})

// ── ⑤ layout-free 动态变量 key（注入 state / 未注入 fallback）──────────

test('layout-free keys：注入 state 时动态 list / 值 / 写回；未注入回落 KEY_DEFINITIONS', () => {
  installHostStates()
  assert.equal(set('workdir', 'E:/dyn'), true)

  setLayoutDeps({ state: ckStore })
  setActivePinia(createPinia())
  const keys = useKeysStore()
  const list = keys.list
  const byKey = Object.fromEntries(list.map((k) => [k.key, k]))

  assert.equal(byKey.workdir.value, 'E:/dyn', '值来自宿主 ref')
  assert.equal(byKey.workdir.label, '当前项目')
  assert.equal(byKey.project, undefined, 'hidden alias 不出现在变量下拉')
  assert.equal(byKey.sessionId, undefined)
  assert.ok(byKey.mcpServer && byKey.searchBox, 'layout-free 既有 UI 变量保留')

  assert.equal(keys.valueOf('sessionId'), getSync('selected-session'), 'alias 读取解引用到目标')
  keys.setValue('model', 'gpt-test')
  assert.equal(ref('model').value, 'gpt-test', 'setValue 写回宿主状态')
  assert.equal(keys.valueOf('model'), 'gpt-test')

  // standalone / 测试：未注入 state → 原 KEY_DEFINITIONS fallback（行为不变）。
  setLayoutDeps({ state: null })
  setActivePinia(createPinia())
  const standalone = useKeysStore()
  const fb = Object.fromEntries(standalone.list.map((k) => [k.key, k]))
  assert.ok(fb.project, '未注入时旧 key 仍在下拉')
  assert.equal(fb.project.value, 'layout-free', 'fallback 默认值不变')
  standalone.setValue('project', 'local-only')
  assert.equal(standalone.valueOf('project'), 'local-only')
  assert.equal(ref('workdir').value, 'E:/dyn', 'fallback 写入不得影响宿主状态')

  // 收尾：恢复注入（同进程后续用例若读 keys store 仍走宿主状态）。
  setLayoutDeps({ state: ckStore })
})