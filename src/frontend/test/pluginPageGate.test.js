/**
 * G-107 · PluginPageGate（插件页承载门）运行期断言。
 *
 * 背景：插件页 tab（`plugin-page:<id>:<page>`）在插件 manifest / 视图注册**之前**打开时，
 * 旧路径直接挂 `PluginPageHost`（entry=''）→ 宿主 onMounted 的 check() 落 idle 态；
 * 视图后到翻转 entry 也无人重跑 → 页面永久空白。承载门 = 视图就绪才挂宿主。
 *
 * 覆盖：
 *   ① 视图未注册 → 渲染「启动中」占位（layoutFree.container.viewStarting），不挂宿主；
 *   ② 视图注册后 → 挂载 PluginPageHost 并把 entry 传入（宿主走完整就绪链路：starting 状态
 *      下出现宿主占位 notReadyTitle —— 若 entry 没传入，宿主会停在 idle、三块区域全不渲染）；
 *   ③ entry 变更（插件升级重注册）→ :key 重挂宿主（重新探活）。
 *
 * 挂载方式沿用既有约定（`test/support/vue-sfc-hooks.mjs` 直载真实 SFC + `headless.js` 无 DOM 渲染器）。
 */
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { register } from 'node:module'
import { createRenderer, defineComponent, h, nextTick } from 'vue'
import { createEl, hostOps, installDomStubs } from './support/headless.js'
import { readSrc } from './support/feSource.mjs'

register('./support/vue-sfc-hooks.mjs', import.meta.url)
await new Promise((r) => setTimeout(r, 0))

const { PluginPageGate, usePlugins } = await loadModules()

async function loadModules() {
  const [gate, plugins, mqMod, pkg] = await Promise.all([
    import('../packages/plugin-ui/src/PluginPageGate.vue'),
    import('../packages/plugin-ui/src/usePlugins.js'),
    import('../../../../chonkpilot-framework/host/web/src/utils/mq.js'),
    import('../packages/plugin-ui/src/index.js'),
  ])
  // 插件 UI 包宿主端口（D-70 ②）：测试用宿主 mq + 极简组件/主题桩。
  pkg.configurePluginUI({
    host: {
      mq: { on: (...a) => mqMod.default.on(...a), emit: (...a) => mqMod.default.emit(...a) },
      views: { register: () => {}, unregister: () => {} },
      instance: { currentId: () => 'win-1' },
    },
    theme: { subscribe: () => () => {}, snapshot: () => ({}) },
    components: {
      Icon: { name: 'Icon', render: () => null },
      Button: { name: 'Button', render: () => null },
      Switch: { name: 'Switch', render: () => null },
    },
  })
  return {
    PluginPageGate: gate.default,
    usePlugins: plugins.usePlugins,
  }
}

const restoreDom = installDomStubs()
after(restoreDom)

/* ── fetch 桩：`/publish` 返回空插件清单；`/plugins/…` 探活按 probeOk 返回 200/404 ── */
let probeOk = false
const probeCalls = []
const savedFetch = globalThis.fetch
before(async () => {
  globalThis.fetch = async (url, opts = {}) => {
    const u = String(url)
    if (!u.startsWith('/plugins/')) {
      return { ok: true, status: 200, json: async () => ({ ok: true, result: { plugins: [] }, errors: [] }) }
    }
    probeCalls.push({ url: u, method: opts.method })
    return { ok: probeOk, status: probeOk ? 200 : 404, json: async () => ({}) }
  }
  // 只装一次事件链（幂等）
  usePlugins()
  const { initPlugins } = await import('../packages/plugin-ui/src/usePlugins.js')
  initPlugins()
  await flush()
})
after(() => {
  globalThis.fetch = savedFetch
})

async function flush() {
  await new Promise((r) => setTimeout(r, 0))
  await nextTick()
}

const { state } = usePlugins()

function resetPlugins(list) {
  state.plugins = list
}

function walk(node, out = []) {
  if (!node) return out
  out.push(node)
  for (const c of node.children || []) walk(c, out)
  return out
}

function classesOf(node) {
  const cls = node && node.props && node.props.class
  if (!cls) return []
  if (typeof cls === 'string') return cls.split(/\s+/).filter(Boolean)
  return []
}

function byClass(root, name) {
  return walk(root).find((n) => classesOf(n).includes(name)) || null
}

function textOf(root) {
  return walk(root)
    .filter((n) => n.tag === '#text')
    .map((n) => n.text)
    .join('')
}

function mount() {
  const renderer = createRenderer(hostOps)
  const app = renderer.createApp(
    defineComponent({
      setup: () => () =>
        h(PluginPageGate, { pluginId: 'codegraph', pageId: 'config' }),
    }),
  )
  app.config.globalProperties.$t = (key) => key
  const root = createEl('root')
  app.mount(root)
  return { app, root }
}

test('① 视图未注册 → 「启动中」占位，不挂宿主 iframe / 宿主占位', async () => {
  resetPlugins([{ id: 'codegraph', status: 'starting' }]) // 插件在，但 views 未注册
  const { app, root } = mount()
  await nextTick()

  assert.match(textOf(root), /layoutFree\.container\.viewStarting/, '须渲染 viewStarting 占位')
  assert.equal(byClass(root, 'pph-frame'), null, '视图未注册不得挂宿主 iframe')
  assert.equal(byClass(root, 'pph-placeholder'), null, '不得出现宿主侧占位（宿主根本未挂载）')

  app.unmount()
})

test('② 视图注册后 → 挂载宿主并传入 entry（宿主走完整就绪链路，非 idle 空白）', async () => {
  resetPlugins([
    { id: 'codegraph', status: 'starting', views: [{ id: 'config', entry: 'webui/config.html', title: '代码知识库' }] },
  ])
  const { app, root } = mount()
  await flush()

  // entry 传入宿主：status=starting → 宿主自身的 not-ready 占位（idle 态下三块区域全不渲染）
  assert.ok(byClass(root, 'pph-placeholder'), '宿主须挂载并进入就绪链路（出现宿主占位）')
  assert.match(textOf(root), /layoutFree\.plugin\.notReadyTitle/, '宿主占位须有标题（entry 已生效）')
  assert.equal(byClass(root, 'pph-frame'), null, '插件未 running 仍不得挂 iframe')

  app.unmount()
})

test('③ entry 变更（重注册）→ :key 重挂宿主，重新探活', async () => {
  resetPlugins([
    { id: 'codegraph', status: 'running', views: [{ id: 'config', entry: 'webui/config.html', title: '' }] },
  ])
  probeOk = true
  probeCalls.length = 0
  const { app, root } = mount()
  await flush()
  assert.equal(probeCalls.length, 1, '首挂探活一次')

  // 插件升级重注册：entry 变更 → :key 翻转 → 宿主重挂 → 重新探活
  resetPlugins([
    { id: 'codegraph', status: 'running', views: [{ id: 'config', entry: 'webui/config2.html', title: '' }] },
  ])
  await flush()
  assert.equal(probeCalls.length, 2, 'entry 变更须重挂宿主并重新探活')
  assert.match(probeCalls[1].url, /config2\.html/, '重挂后探活指向新 entry')

  app.unmount()
  probeOk = false
})
