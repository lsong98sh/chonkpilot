/**
 * 自由布局容器 · 区块保活（UILAYOUT-1 §3.11 · §7 P3⑥）—— **运行期行为断言**（非源码守卫）。
 *
 * 背景（§3.11「关键风险」）：本系统区块的状态在**组件内**（`ChatPanel` 的消息与流式增量、
 * `CodeView` 的页签集合…），区块一旦被卸载即丢失。参考工程只在自由页签做到了保活
 * （`views/FreeTabsView.vue` 的 `v-for` + `v-show`），固定页签与区域类型分支都是
 * 「`v-if` 切换 → 卸载重建」（❌）。P3 把三处都改成「全部渲染 + `v-show` 隐藏」。
 *
 * 验证方式：`test/support/vue-sfc-hooks.mjs` 直载真实 `.vue` + `test/support/headless.js`
 * 的无 DOM 渲染器挂载 → 断言**切页签 / 切区域类型后组件实例未被卸载**（`onUnmounted` 未触发）
 * 且**组件内部状态保留**（自增计数不回退）。用「探针组件」代替真实区块：保活机制与区块无关
 * （`ChatPanel` / `CodeView` 贵在"内部状态多"，而"不卸载"是同一个渲染层保证）。
 */
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { register } from 'node:module'
import { createRenderer, defineComponent, h, nextTick, onUnmounted, reactive, ref } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { hostOps, createEl, installDomStubs } from './support/headless.js'

register('./support/vue-sfc-hooks.mjs', import.meta.url)
// 钩子在独立线程装载：等一个宏任务再 import（否则 dynamic import 可能抢在钩子就绪前）。
// ⚠️ 子系统（`layout-free/`）内部的 JS 导入无扩展名（打包器风格）→ **必须在 register 之后**
//    才能 import（静态 import 会在本文件执行前解析，那时钩子还没挂上）。
await new Promise((r) => setTimeout(r, 0))

const { setLayoutDeps } = await import('../src/layout-free/deps.js')
const { useRuntimeStore } = await import('../src/layout-free/stores/runtime.js')
const LfFixedTabsView = (await import('../src/layout-free/views/LfFixedTabsView.vue')).default
const LfFreeTabsView = (await import('../src/layout-free/views/LfFreeTabsView.vue')).default
const LfLayoutLeaf = (await import('../src/layout-free/components/layout/LfLayoutLeaf.vue')).default
const LfViewPool = (await import('../src/layout-free/components/layout/LfViewPool.vue')).default

/** 探针组件：记录挂载 / 卸载，并持有"区块内部状态"（自增计数） */
const probes = []
function makeProbe(name) {
  return defineComponent({
    name,
    setup() {
      const count = ref(0)
      const rec = {
        name,
        count,
        unmounted: false,
        add: () => {
          count.value += 1
        },
      }
      probes.push(rec)
      onUnmounted(() => {
        rec.unmounted = true
      })
      return () => h('span', `${name}:${count.value}`)
    },
  })
}
const ProbeA = makeProbe('probe-a')
const ProbeB = makeProbe('probe-b')

const StubIcon = defineComponent({
  props: { name: { type: String, default: '' } },
  setup: () => () => h('i'),
})

const restoreDom = installDomStubs()
after(restoreDom)

before(() => {
  setLayoutDeps({
    views: {
      definitions: [
        { key: 'probe-a', label: 'Probe A', icon: 'search', mode: 'singleton', host: 'layout', designable: true },
        { key: 'probe-b', label: 'Probe B', icon: 'search', mode: 'singleton', host: 'layout', designable: true },
      ],
      components: { 'probe-a': ProbeA, 'probe-b': ProbeB },
    },
    i18n: (key) => key,
    mq: null,
  })
  probes.length = 0
})

/**
 * 挂载一个组件到无 DOM 宿主树（返回 app 句柄）。
 * WIN-027 起单例 view 由视图池统一持有 → 测试必须同时挂 `LfViewPool`，
 * 槽位注册后由池经 Teleport 渲染真实组件（保活 = 池内实例不销毁）。
 */
function mount(component, props) {
  const renderer = createRenderer(hostOps)
  const app = renderer.createApp(
    defineComponent({
      setup: () => () => h('div', [h(component, props), h(LfViewPool)]),
    }),
  )
  app.component('LfIcon', StubIcon)
  app.directive('mq', {})
  app.mount(createEl('root'))
  return app
}

test('保活①固定页签：全部标签页都挂载；切激活不卸载、状态不丢（§3.11 ❌→✅）', async () => {
  setActivePinia(createPinia())
  probes.length = 0
  const runtime = useRuntimeStore()
  const tabs = [
    { viewKey: 'probe-a', instanceKey: null },
    { viewKey: 'probe-b', instanceKey: null },
  ]
  const props = reactive({ nodeId: 'leaf_tabs', tabs, tabPosition: 'top', design: false })
  const app = mount(LfFixedTabsView, props)
  await nextTick()

  assert.equal(probes.length, 2, '两个标签页的 view 都挂载（旧实现 v-if 只挂载激活项）')

  probes[0].add()
  probes[0].add()
  assert.equal(probes[0].count.value, 2)

  // 切到第二个标签页
  runtime.setFixedTab('leaf_tabs', 'probe-b|')
  await nextTick()
  assert.equal(probes.length, 2, '切页签不得新建实例')
  assert.equal(probes[0].unmounted, false, '切页签不得卸载非激活标签（保活）')
  assert.equal(probes[0].count.value, 2, '切走再切回前，内部状态须保留（聊天消息 / 页签集合等价物）')

  // 切回第一个标签页：状态仍是同一实例的值
  runtime.setFixedTab('leaf_tabs', 'probe-a|')
  await nextTick()
  assert.equal(probes.length, 2)
  assert.equal(probes[0].count.value, 2, '切回不得重建（实例身份未变）')

  app.unmount()
})

test('保活②自由页签：切激活不卸载（既有 v-for + v-show 行为锁定）', async () => {
  setActivePinia(createPinia())
  probes.length = 0
  const runtime = useRuntimeStore()
  runtime.openTab('leaf_free', { viewKey: 'probe-a', instanceKey: null })
  runtime.openTab('leaf_free', { viewKey: 'probe-b', instanceKey: null })
  const app = mount(LfFreeTabsView, { leafId: 'leaf_free', tabPosition: 'top', design: false })
  await nextTick()

  assert.equal(probes.length, 2, '自由页签两页都挂载')
  probes[1].add()
  probes[1].add()
  probes[1].add()

  // 切到第一个标签页
  const first = runtime.tabsOf('leaf_free')[0]
  runtime.activateTab('leaf_free', first.tabId)
  await nextTick()
  assert.equal(probes.length, 2)
  assert.equal(probes[1].unmounted, false, '切页签不得卸载非激活标签（保活）')
  assert.equal(probes[1].count.value, 3, '内部状态保留')

  app.unmount()
})

test('保活③区域类型切换：free-tabs → fixed-tabs 不卸载原区域分支的子组件（§3.11 ❌→✅）', async () => {
  setActivePinia(createPinia())
  probes.length = 0
  const runtime = useRuntimeStore()
  runtime.openTab('leaf_kind', { viewKey: 'probe-a', instanceKey: null })
  const node = reactive({ id: 'leaf_kind', type: 'leaf', kind: 'free-tabs', tabs: [] })
  const app = mount(LfLayoutLeaf, { node, depth: 0, design: false, selectedId: '' })
  await nextTick()
  assert.equal(probes.length, 1, '自由页签分支装入一个 view')
  probes[0].add()

  // 区域类型切换（配置层动作：free-tabs → fixed-tabs）
  node.kind = 'fixed-tabs'
  node.tabs = [{ viewKey: 'probe-b', instanceKey: null }]
  await nextTick()

  assert.equal(probes[0].unmounted, false, '切区域类型不得卸载原分支的子组件（v-show 分支保活）')
  assert.equal(probes[0].count.value, 1, '原分支 view 内部状态保留')

  app.unmount()
})
