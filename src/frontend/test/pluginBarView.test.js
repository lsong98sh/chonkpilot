/**
 * PLUGIN-UI-2：插件状态栏条目两种形态——
 *   ① `ui.statusbar[].view` → 栏内**内嵌插件视图**（`plugin:<id>:<view>`，尺寸取 `default_size`）；
 *   ② 无 `view` + `command` → 既有按钮形态（不回归）。
 * 索引进度徽标即走 ①（codegraph 的 `badge`），宿主不再读插件业务状态键。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { pluginBarItems } from '../packages/plugin-ui/src/pluginBar.js'
import { usePlugins } from '../packages/plugin-ui/src/usePlugins.js'

const { state } = usePlugins()

test('view 条目 → kind:view + plugin:<id>:<view> + default_size', () => {
  state.plugins = [{
    id: 'codegraph',
    name: '代码知识库',
    enabled: true,
    views: [
      { id: 'badge', title: '索引进度', default_size: { width: 200, height: 20 } },
      { id: 'status', title: '知识库状态' },
    ],
    statusbar: [{ id: 'codegraph', zone: 'right', view: 'badge', tooltip: '代码知识库索引进度' }],
  }]
  const items = pluginBarItems('right')
  assert.equal(items.length, 1)
  assert.deepEqual(items[0], {
    id: 'plugin_codegraph_codegraph',
    kind: 'view',
    zone: 'right',
    viewKey: 'plugin:codegraph:badge',
    instanceKey: null,
    width: 200,
    height: 20,
    label: '代码知识库索引进度',
    tooltip: '代码知识库索引进度',
  })
})

test('无 view 的条目仍走按钮形态（既有行为不回归）', () => {
  state.plugins = [{
    id: 'demo',
    name: 'Demo',
    enabled: true,
    views: [{ id: 'config', title: '配置' }],
    statusbar: [{
      id: 'd1', zone: 'left', icon: 'code', tooltip: 'Demo',
      command: { target: 'host', action: 'open-view', view: 'config' },
    }],
  }]
  const items = pluginBarItems('left')
  assert.equal(items.length, 1)
  assert.equal(items[0].kind, 'button')
  assert.equal(items[0].icon, 'code')
  assert.equal(items[0].command.type, 'open-tabs')
  assert.equal(items[0].command.viewKey, 'plugin:demo:config')
})

test('插件禁用 → 栏条目不出现', () => {
  state.plugins = [{
    id: 'codegraph', name: 'x', enabled: false, views: [{ id: 'badge' }],
    statusbar: [{ id: 'codegraph', zone: 'right', view: 'badge' }],
  }]
  assert.deepEqual(pluginBarItems('right'), [])
})
