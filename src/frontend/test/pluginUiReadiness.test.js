/**
 * PLUGIN-UI-1 前端体验收口 · 插件页「未就绪 / 资源不可用」占位 + 自动重试（运行期断言）。
 *
 * 覆盖：
 *   ① 未就绪（插件 `status !== 'running'`）→ 渲染占位、**不**渲染 iframe、不做探活；
 *   ② 就绪（`running` + 资源 200）→ 渲染 iframe、无占位（正常路径无占位闪烁）；
 *   ③ 状态转 `running` → 自动重试，上限 `AUTO_RETRY_LIMIT` 次；超限后只剩手动「重试」；
 *   ④ 源码守卫：零 watch / 零 watchEffect、iframe 有 `@error`、占位文案 zh/en 齐备。
 *
 * 挂载方式沿用既有约定（`test/support/vue-sfc-hooks.mjs` 直载真实 SFC + `headless.js` 无 DOM 渲染器）。
 */
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { register } from 'node:module'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { createRenderer, defineComponent, h, nextTick } from 'vue'
import { createEl, hostOps, installDomStubs } from './support/headless.js'

register('./support/vue-sfc-hooks.mjs', import.meta.url)
await new Promise((r) => setTimeout(r, 0))

const { PluginPageHost, usePlugins, mq, AUTO_RETRY_LIMIT } = await loadModules()

async function loadModules() {
  const [host, plugins, mqMod, readiness, pkg] = await Promise.all([
    import('../packages/plugin-ui/src/PluginPageHost.vue'),
    import('../packages/plugin-ui/src/usePlugins.js'),
    import('../src/utils/mq.js'),
    import('../packages/plugin-ui/src/usePluginReadiness.js'),
    import('../packages/plugin-ui/src/index.js'),
  ])
  // 插件 UI 包宿主端口（D-70 ②）：测试用宿主 mq + 极简组件/主题桩。
  pkg.configurePluginUI({
    host: {
      mq: { on: (...a) => mqMod.default.on(...a), emit: (...a) => mqMod.default.emit(...a) },
      views: { register: () => {}, unregister: () => {} },
    },
    theme: { subscribe: () => () => {}, snapshot: () => ({}) },
    components: {
      Icon: { name: 'Icon', render: () => null },
      Button: { name: 'Button', render: () => null },
      Switch: { name: 'Switch', render: () => null },
    },
  })
  return {
    PluginPageHost: host.default,
    usePlugins: plugins.usePlugins,
    mq: mqMod.default,
    AUTO_RETRY_LIMIT: readiness.AUTO_RETRY_LIMIT,
  }
}

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const pkgDir = join(here, '..', 'packages', 'plugin-ui', 'src')
const readPkg = (rel) => readFileSync(join(pkgDir, rel), 'utf8')

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
  // 只装一次事件链（幂等）：让 `plugin-status` 能经 usePlugins 推到订阅者
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

function statusEvent(id, status, detail = '') {
  mq.emitRemote({ type: 'plugin-status', payload: { id, status, detail } })
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
  if (Array.isArray(cls)) return cls.filter((x) => typeof x === 'string')
  return []
}

function byClass(root, name) {
  return walk(root).find((n) => classesOf(n).includes(name)) || null
}

function allByClass(root, name) {
  return walk(root).filter((n) => classesOf(n).includes(name))
}

function textOf(root) {
  return walk(root)
    .filter((n) => n.tag === '#text')
    .map((n) => n.text)
    .join('')
}

function mount(props) {
  const renderer = createRenderer(hostOps)
  const app = renderer.createApp(
    defineComponent({
      setup: () => () => h(PluginPageHost, props),
    }),
  )
  app.config.globalProperties.$t = (key) => key
  const root = createEl('root')
  app.mount(root)
  return { app, root }
}

const PROPS = {
  pluginId: 'codebase',
  pageId: 'config',
  entry: 'webui/config.html',
}

test('① 未就绪（status=starting）→ 占位、不渲染 iframe、不探活', async () => {
  resetPlugins([{ id: 'codebase', status: 'starting', detail: '等待外部进程连接' }])
  probeCalls.length = 0
  const { app, root } = mount({ ...PROPS })
  await nextTick()

  assert.ok(byClass(root, 'pph-placeholder'), '须渲染未就绪占位')
  assert.equal(byClass(root, 'pph-frame'), null, '未就绪时不得渲染 iframe（避免白屏）')
  assert.equal(probeCalls.length, 0, '已知状态非 running 时不做探活（状态门先短路）')

  const text = textOf(root)
  assert.match(text, /layoutFree\.plugin\.notReadyTitle/, '占位须有 i18n 标题')
  assert.match(text, /layoutFree\.plugin\.notReadyStatus/, '占位须显示插件状态')
  assert.match(text, /layoutFree\.plugin\.retry/, '占位须有「重试」按钮')

  app.unmount()
})

test('② 就绪（running + 探活 200）→ 渲染 iframe、无占位', async () => {
  resetPlugins([{ id: 'codebase', status: 'running', detail: '' }])
  probeOk = true
  probeCalls.length = 0
  const { app, root } = mount({ ...PROPS })
  await flush()

  const frame = byClass(root, 'pph-frame')
  assert.ok(frame, '就绪后须渲染 iframe')
  assert.equal(byClass(root, 'pph-placeholder'), null, '正常路径不得出现占位')
  assert.equal(probeCalls.length, 1, '自持资源挂 iframe 前须探活一次')
  assert.equal(probeCalls[0].method, 'HEAD', '探活用 HEAD（不重复下载页面）')
  assert.equal(
    frame.props.src,
    '/plugins/codebase/webui/config.html?p=config',
    '首帧 src 与既有格式一致（无重试代次）',
  )

  // iframe load → 进入 ready（占位 / loading 都退场）
  frame.props.onLoad()
  await nextTick()
  assert.equal(byClass(root, 'pph-overlay'), null, 'load 后不再显示 loading 遮罩')

  app.unmount()
  probeOk = false
})

test('③ 状态转 running 自动重试上限 3 次；超限后仅手动重试可用', async () => {
  resetPlugins([{ id: 'codebase', status: 'starting', detail: '' }])
  probeOk = false
  probeCalls.length = 0
  const { app, root } = mount({ ...PROPS })
  await nextTick()
  assert.ok(byClass(root, 'pph-placeholder'))
  assert.equal(probeCalls.length, 0)

  // 每次 status→running 触发一次自动重试（探活仍 404 → 继续占位）
  for (let i = 0; i < AUTO_RETRY_LIMIT; i += 1) {
    statusEvent('codebase', 'running')
    await flush()
  }
  assert.equal(probeCalls.length, AUTO_RETRY_LIMIT, `自动重试须恰好 ${AUTO_RETRY_LIMIT} 次`)
  assert.ok(byClass(root, 'pph-placeholder'), '探活仍失败 → 继续占位')
  assert.equal(allByClass(root, 'pph-frame').length, 0)

  // 超限：再来一次 running 不再自动重试
  statusEvent('codebase', 'running')
  await flush()
  assert.equal(probeCalls.length, AUTO_RETRY_LIMIT, '超过上限后不得继续自动重试')
  assert.match(textOf(root), /layoutFree\.plugin\.autoRetryExhausted/, '须提示自动重试已停止')

  // 手动「重试」：不占自动预算，仍可再次探活
  const btn = byClass(root, 'pph-retry')
  assert.ok(btn, '须有手动重试按钮')
  const beforeManual = probeCalls.length
  btn.props.onClick()
  await flush()
  assert.equal(probeCalls.length, beforeManual + 1, '手动重试须重新探活')
  assert.ok(byClass(root, 'pph-placeholder'), '仍失败 → 继续占位')

  // 资源恢复后手动重试成功 → 挂 iframe、占位退场（重试代次进 ?t=）
  probeOk = true
  byClass(root, 'pph-retry').props.onClick()
  await flush()
  const frame = byClass(root, 'pph-frame')
  assert.ok(frame, '资源恢复后须渲染 iframe')
  assert.equal(byClass(root, 'pph-placeholder'), null, '成功后占位须退场')
  assert.match(frame.props.src, /[?&]t=\d+/, '重试后 src 须带重试代次（绕过缓存）')

  app.unmount()
  probeOk = false
})

test('④ 源码守卫：零 watch / 零 watchEffect；iframe 有 error 回调；zh/en 文案齐备', () => {
  const readiness = readPkg('usePluginReadiness.js')
  const host = readPkg('PluginPageHost.vue')

  for (const [name, code] of [['usePluginReadiness', readiness], ['PluginPageHost', host]]) {
    assert.doesNotMatch(code, /\bwatch\s*\(/, `${name} 不得使用 watch`)
    assert.doesNotMatch(code, /\bwatchEffect\s*\(/, `${name} 不得使用 watchEffect`)
  }
  assert.match(readiness, /export const LOAD_TIMEOUT_MS = 3000/, '首帧加载超时须为 3s')
  assert.match(readiness, /export const AUTO_RETRY_LIMIT = 3/, '自动重试上限须为 3')
  assert.match(readiness, /subscribePluginChange\(onPluginChange\)/, '就绪态须由插件变更事件驱动')
  assert.match(readiness, /import \{ pluginById, subscribePluginChange \} from '\.\/usePlugins'/, '状态取自既有 usePlugins')

  assert.match(host, /v-if="showFrame"/, 'iframe 须按就绪态条件挂载')
  assert.match(host, /@error="onFrameError"/, 'iframe 须接 error')
  assert.match(host, /class="pph-retry"/, '占位须有手动重试按钮')
  // 占位样式段（`.pph-placeholder` 之后）只用主题 token，不得硬编码颜色
  const placeholderCss = host.slice(host.indexOf('.pph-placeholder'))
  assert.doesNotMatch(placeholderCss, /#[0-9a-fA-F]{3,6}\b/, '占位样式不得硬编码颜色（只用主题 token）')

  const keys = [
    'notReadyTitle',
    'notReadyStatusHint',
    'notReadyAssetHint',
    'notReadyTimeoutHint',
    'notReadyErrorHint',
    'notReadyStatus',
    'autoRetryExhausted',
    'retry',
  ]
  for (const locale of ['zh-CN', 'en-US']) {
    const json = JSON.parse(read(`locales/${locale}/layoutFree.json`))
    for (const k of keys) {
      assert.ok(json.plugin && json.plugin[k], `${locale} 缺 layoutFree.plugin.${k}`)
    }
  }
})
