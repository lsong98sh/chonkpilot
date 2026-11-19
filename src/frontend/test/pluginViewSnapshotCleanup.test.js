/**
 * 插件视图注册表快照清理（阶段4 P0）：
 *   · 同一插件再次注册但 views 为空 / 全部无 id → 必须卸载旧 key（不得残留）；
 *   · usePlugins.applySnapshot 用全量快照替换台账时，下线插件 → unregister + notifyChange。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  createViewRegistry,
  registerPluginViews,
  registeredViewKeys,
  unregisterPluginViews,
} from '../src/layout/view-registry.js'
import { applySnapshot, subscribePluginChange } from '../packages/plugin-ui/src/usePlugins.js'
import { configurePluginHost } from '../packages/plugin-ui/src/host.js'

test('registerPluginViews：旧 views 存在后收到空 views → 旧 key 被卸载', () => {
  const id = 'p0empty'
  registerPluginViews(
    { id, name: 'P0', views: [{ id: 'page', title: '页', entry: 'webui/page.html' }] },
    ({ plugin, view }) => ({ name: `${plugin.id}:${view.id}` }),
  )
  assert.ok(registeredViewKeys().includes(`plugin:${id}:page`), '初次注册 key 应存在')

  // 空 views（插件重启后 manifest 暂未带 views）
  registerPluginViews({ id, name: 'P0', views: [] }, () => ({}))
  assert.ok(!registeredViewKeys().includes(`plugin:${id}:page`), '空 views 必须卸载旧 key')

  unregisterPluginViews(id)
})

test('registerPluginViews：views 存在但全部缺 id → 也卸载旧 key', () => {
  const id = 'p0invalid'
  registerPluginViews(
    { id, views: [{ id: 'page', entry: 'webui/page.html' }] },
    ({ plugin, view }) => ({ name: `${plugin.id}:${view.id}` }),
  )
  assert.ok(registeredViewKeys().includes(`plugin:${id}:page`))

  registerPluginViews({ id, views: [{ id: '' }, null] }, () => ({}))
  assert.ok(!registeredViewKeys().includes(`plugin:${id}:page`), '无有效 view 必须卸载旧 key')

  unregisterPluginViews(id)
})

test('registerPluginViews：重复 view id 去重；无组件工厂不写孤立定义', () => {
  const id = 'p0dedupe'
  const key = `plugin:${id}:page`
  registerPluginViews(
    { id, views: [{ id: 'page' }, { id: 'page', title: '重复' }] },
    ({ plugin, view }) => ({ name: `${plugin.id}:${view.id}` }),
  )
  const defs = createViewRegistry().definitions.filter((d) => d.key === key)
  assert.equal(defs.length, 1, '同一 view id 只能生成一条布局定义')

  registerPluginViews({ id, views: [{ id: 'orphan' }] }) // 无组件工厂
  assert.ok(
    !createViewRegistry().definitions.some((d) => d.key === `plugin:${id}:orphan`),
    '不可解析的 view 不应留下孤立定义',
  )
  unregisterPluginViews(id)
})

test('applySnapshot：快照删除旧插件 → unregisterPluginViews + notifyChange', () => {
  const registered = []
  const unregistered = []
  configurePluginHost({
    mq: { on: () => () => {}, emit: async () => ({}) },
    views: {
      register: (p) => registered.push(p.id),
      unregister: (id) => unregistered.push(id),
    },
  })

  const changed = []
  const off = subscribePluginChange((id) => changed.push(id))

  applySnapshot([
    { id: 'keep', views: [{ id: 'v1' }] },
    { id: 'gone', views: [{ id: 'v1' }] },
  ])
  assert.deepEqual(registered.sort(), ['gone', 'keep'])
  assert.ok(changed.includes('keep') && changed.includes('gone'))

  registered.length = 0
  changed.length = 0
  applySnapshot([{ id: 'keep', views: [{ id: 'v1' }] }])

  assert.deepEqual(unregistered, ['gone'], '下线插件必须注销视图')
  assert.ok(changed.includes('gone'), '下线插件必须通知变更（事件驱动消费方据此收敛）')
  assert.ok(registered.includes('keep'), '留存插件仍应注册')
  assert.ok(!registered.includes('gone'), '下线插件不得再注册')

  off()
  applySnapshot([])
})