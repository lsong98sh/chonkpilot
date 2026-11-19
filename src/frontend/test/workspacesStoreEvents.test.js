/**
 * workspaces store 事件订阅生命周期（阶段4 P0 泄漏修复）。
 *
 * 覆盖：
 *   ① bindEvents 幂等，且返回的是**副本**（组件 push 本地订阅不得污染 store 缓存）；
 *   ② unbindEvents 退订并清空缓存（幂等）；
 *   ③ bind → unbind → bind 后 workdir-opened 仍能更新 store（重挂不丢订阅）。
 */
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { createPinia, setActivePinia } from 'pinia'
import mq from '@ckfw/utils/mq.js'
import { useWorkspacesStore, workspaceKey } from '../src/stores/workspaces.js'
import { EventNames } from '../src/events/event-names.js'

const HERE = dirname(fileURLToPath(import.meta.url))

// store 的 subscribeProject 会经 `/publish` 发后端（Node 下无同源服务）→ 统一桩掉 fetch。
const realFetch = globalThis.fetch
before(() => {
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ ok: true, result: {}, errors: [] }) })
})
after(() => {
  globalThis.fetch = realFetch
})

function setup() {
  setActivePinia(createPinia())
  return useWorkspacesStore()
}

test('bindEvents 返回副本：本地 push 不污染 store 缓存', () => {
  const store = setup()
  const first = store.bindEvents()
  assert.equal(first.length, 2)
  assert.notEqual(first, store._unsubs, 'bindEvents 必须返回副本')

  const local = () => {}
  first.push(local)
  assert.equal(store._unsubs.length, 2, '组件本地订阅不得进入 store 缓存')
  assert.equal(store.bindEvents().length, 2, '再次 bind 仍只有 store 级两条')

  store.unbindEvents()
})

test('unbindEvents 幂等并清空缓存', () => {
  const store = setup()
  store.bindEvents()
  store.unbindEvents()
  assert.equal(store._unsubs, null)
  store.unbindEvents() // 幂等
  assert.equal(store._unsubs, null)
})

test('bind → unbind → bind 后 workdir-opened 仍能更新 store', async () => {
  const store = setup()
  store.bindEvents()

  await mq.emit(EventNames.workdirOpened, { work_dir: 'C:/p0/a' })
  assert.ok(store.projects[workspaceKey('C:/p0/a')], '首次 bind 后事件应生效')

  store.unbindEvents()
  await mq.emit(EventNames.workdirOpened, { work_dir: 'C:/p0/b' })
  assert.ok(!store.projects[workspaceKey('C:/p0/b')], 'unbind 后不得再响应事件')

  const second = store.bindEvents()
  await mq.emit(EventNames.workdirOpened, { work_dir: 'C:/p0/c' })
  assert.ok(store.projects[workspaceKey('C:/p0/c')], '重新 bind 后事件应恢复生效')

  for (const fn of second) fn()
  store.unbindEvents()
})

test('WorkspaceTree 挂载绑定 store 事件、卸载调用 unbindEvents（源码守卫）', () => {
  const src = readFileSync(join(HERE, '..', 'src', 'views', 'workdirs', 'WorkspaceTree.vue'), 'utf8')
  assert.match(src, /unsubs\s*=\s*store\.bindEvents\(\)/, 'onMounted 须绑定 store 事件')
  const unmounted = src.match(/onUnmounted\(\(\)\s*=>\s*\{[\s\S]*?\n\}\)/)
  assert.ok(unmounted, '未找到 onUnmounted')
  assert.match(unmounted[0], /store\.unbindEvents\(\)/, 'onUnmounted 须清空 store 订阅缓存')
  assert.match(unmounted[0], /for \(const fn of unsubs\) fn\(\)/, '组件本地订阅仍须逐条退订')
})