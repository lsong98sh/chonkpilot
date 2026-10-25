/**
 * PLUGIN-UI-1 第二段前端守卫：插件 iframe 路由（已统一 `/plugins/<id>/…`）+ sandbox + ckMq 受控入口。
 *
 * 覆盖：
 *   ① 路由统一：`/plugins/<id>/<entry>`，helper 不再按 `self_serve` 分流；
 *   ② `PluginPageHost` sandbox 放开 same-origin（同源 iframe 才能拿到 ckMq），且不再接收分流属性；
 *   ③ ckMq 只暴露 on/off/emit，且订阅在 frame 重载 / 卸载 / beforeunload 时全部退订；
 *   ④ 内置插件（browser/codebase/desktop/file/mcp/task/websearch）为 selfServe 单源
 *      （`src/initdata/.../<id>/webui` 已删除，唯一源 = `src/plugins/<id>/webui`）；
 *   ⑤~⑥ action 面 / codebase 单源；
 *   ⑦ 全量单源 + demo-python 无 `ui.views`（旧路由下线后不得留死入口）；
 *   ⑧ 源码守卫：不再存在 `/plugin-ui/` 的资源拼接用法（仅允许 `_sdk/plugin.js` 字面量）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'
import { PLUGIN_UI_PREFIX, pluginUIPageURL } from '../packages/plugin-ui/src/pluginUiRoute.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoDir = join(here, '..', '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const pkgDir = join(here, '..', 'packages', 'plugin-ui', 'src')
const readPkg = (rel) => readFileSync(join(pkgDir, rel), 'utf8')

/** 递归列出目录下的全部文件（绝对路径）。 */
function walkFiles(dir) {
  const out = []
  for (const d of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, d.name)
    if (d.isDirectory()) out.push(...walkFiles(p))
    else if (d.isFile()) out.push(p)
  }
  return out
}

test('① 路由统一：一律 /plugins/<id>/<entry>（entry 原样保留，不再按 self_serve 分流）', () => {
  assert.equal(PLUGIN_UI_PREFIX, '/plugins/')
  assert.equal(
    pluginUIPageURL('codebase', 'webui/config.html'),
    '/plugins/codebase/webui/config.html',
  )
  assert.equal(
    pluginUIPageURL('browser', 'webui/config.html'),
    '/plugins/browser/webui/config.html',
  )
  const route = readPkg('pluginUiRoute.js')
  assert.doesNotMatch(route, /pluginUIPrefix/, '前缀分流函数已删除')
  assert.doesNotMatch(
    route,
    /function pluginUIPageURL\([^)]*selfServe/,
    'pluginUIPageURL 不再接收 selfServe 参数',
  )
})

test('② PluginPageHost：sandbox 含 allow-same-origin；不再接收 selfServe 分流属性', () => {
  const host = readPkg('PluginPageHost.vue')
  assert.match(host, /sandbox="allow-scripts allow-forms allow-same-origin"/, 'sandbox 须放开 same-origin')
  assert.doesNotMatch(host, /selfServe/, '宿主页壳不再按 self_serve 分流')
  // PLUGIN-UI-1 就绪态收口：src 的拼接移入 usePluginReadiness（统一前缀）
  const readiness = readPkg('usePluginReadiness.js')
  assert.match(readiness, /pluginUIPageURL\(props\.pluginId, props\.entry\)/, '前缀须统一（无 selfServe 参数）')
  assert.doesNotMatch(readiness, /selfServe/, '就绪态不再按 self_serve 分流')
  assert.doesNotMatch(host, /`\/plugin-ui\/\$\{/, '不得内联旧前缀硬编码（统一走 pluginUiRoute）')
  assert.doesNotMatch(readiness, /`\/plugin-ui\/\$\{/, '不得内联旧前缀硬编码（统一走 pluginUiRoute）')
})

test('③ ckMq 受控入口：只有 on/off/emit；frame 重载 / 卸载 / beforeunload 全部退订', () => {
  const host = readPkg('PluginPageHost.vue')
  assert.match(host, /__ckMqBind/, '须支持 ck-mq.js 注入点（补投注入前排队调用）')
  assert.match(host, /win\.ckMq = api/, '未引入 ck-mq.js 时须直接挂 contentWindow.ckMq')
  for (const m of ['on(topic, cb)', 'off(topic, cb)', 'emit(topic, payload)']) {
    assert.ok(host.includes(m), `ckMq 须暴露 ${m}`)
  }
  assert.doesNotMatch(host, /ckMq\s*=\s*mq\b/, '不得把宿主 mq 对象裸暴露为 ckMq')
  assert.match(host, /function clearFrameSubs\(\)/, '须有统一退订函数')
  assert.match(host, /window\.addEventListener\('beforeunload', clearFrameSubs\)/, 'beforeunload 须退订')
  assert.match(host, /onBeforeUnmount\(\(\) => \{[\s\S]*clearFrameSubs\(\)/, '组件卸载须退订')
  assert.match(host, /function injectCkMq\(\) \{[\s\S]*clearFrameSubs\(\)/, '重新注入前须退订旧 frame 订阅')

  const shared = readFileSync(join(here, '..', 'public', 'shared', 'ck-mq.js'), 'utf8')
  for (const m of ['on:', 'off:', 'emit:']) assert.ok(shared.includes(m), `ck-mq.js 缺 ${m}`)
  assert.ok(shared.includes('__ckMqBind'), 'ck-mq.js 须提供宿主注入点')
  assert.doesNotMatch(shared, /emitRemote|\.pre\(|\.post\(/, 'ck-mq.js 不得透出宿主 mq 其它方法')
})

test('④ 注册面 / 设置页 / 预览页签不再透传 self_serve（路由已统一）', () => {
  const reg = read('layout/views.registry.js')
  assert.doesNotMatch(reg, /self_serve|selfServe/, '插件 view 工厂不再透传 self_serve')
  const pc = read('views/preview/ProjectConfig.vue')
  assert.doesNotMatch(pc, /self-serve|selfServe|self_serve/, '设置页不再透传 self_serve')
  const cv = read('views/codeview/CodeView.vue')
  assert.doesNotMatch(cv, /selfServe|self_serve/, '预览页签不再透传 self_serve')
})

test('⑤ pluginAction：注入 X-CK-Plugin-Id，成功取 result / 失败抛错误信封', async () => {
  const { pluginAction } = await import('../packages/plugin-ui/src/pluginAction.js')
  const calls = []
  const origFetch = globalThis.fetch
  try {
    globalThis.fetch = async (url, opts) => {
      calls.push({ url, opts })
      return { ok: true, status: 200, json: async () => ({ v: 1 }) }
    }
    const res = await pluginAction('codebase', 'ui.ping', { a: 1 })
    assert.deepEqual(res, { v: 1 })
    assert.equal(calls[0].url, '/plugins/codebase/api/ui.ping')
    assert.equal(calls[0].opts.method, 'POST')
    assert.equal(calls[0].opts.headers['X-CK-Plugin-Id'], 'codebase', '须注入来源插件 id')
    assert.deepEqual(JSON.parse(calls[0].opts.body), { a: 1 })

    globalThis.fetch = async () => ({
      ok: false,
      status: 403,
      json: async () => ({ ok: false, errors: ['plugin id mismatch'] }),
    })
    await assert.rejects(() => pluginAction('codebase', 'ui.ping'), /plugin id mismatch/)
  } finally {
    globalThis.fetch = origFetch
  }
})

test('⑥ codebase 单源：manifest 声明 selfServe，initdata 不再携带 webui', () => {
  const manifestPath = join(repoDir, 'src', 'initdata', 'capability', 'plugins', 'codebase', 'plugin.json')
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
  assert.equal(manifest.ui?.selfServe, true, 'codebase plugin.json 须声明 ui.selfServe: true')
  const legacyWebui = join(repoDir, 'src', 'initdata', 'capability', 'plugins', 'codebase', 'webui')
  assert.equal(existsSync(legacyWebui), false, 'initdata codebase webui 目录须已删除（唯一源 = chonkpilot-plugins/codebase/webui）')
  const srcWebui = join(repoDir, '..', 'chonkpilot-plugins', 'codebase', 'webui', 'config.html')
  assert.equal(existsSync(srcWebui), true, 'chonkpilot-plugins/codebase/webui/config.html 须存在（页面随插件仓）')
})

/**
 * ⑦ PLUGIN-UI-1 全量单源断言：`src/initdata/capability/plugins/ *\/webui/` 不得存在。
 *
 * 唯一白名单 = `demo-python`：Python 样例（`runtime.exe = python`，无 Go 构建产物 / 无法 embed，
 * 且 `product: false` 不随产物投放），其页面只能留在 initdata 内随目录安装。
 * 旧路由 `/plugin-ui/<id>/…`（宿主直读磁盘）已下线 → 该样例**不得再声明 `ui.views`**
 * （含引用它的菜单 / 状态栏入口），避免出现点了就 404 的死入口；`webui/` 目录保留作写法参考。
 * 其余插件必须 `ui.selfServe: true`，且 `ui.views[].entry` 在 `src/plugins/<id>/` 下真实存在。
 */
test('⑦ 全量单源：initdata 仅 demo-python 保留 webui 样例，且其 manifest 无 ui 死入口', () => {
  const allowInitdataWebui = new Set(['demo-python'])
  const initPluginsDir = join(repoDir, 'src', 'initdata', 'capability', 'plugins')

  const offenders = readdirSync(initPluginsDir, { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .filter((d) => existsSync(join(initPluginsDir, d.name, 'webui')))
    .map((d) => d.name)
    .filter((id) => !allowInitdataWebui.has(id))
  assert.deepEqual(offenders, [], `以下插件仍带 initdata webui 副本：${offenders.join(', ')}`)

  // demo-python：样例 webui 保留，但 manifest 不再声明页面入口（旧路由下线 → 不可达）。
  const demoDir = join(initPluginsDir, 'demo-python')
  assert.equal(existsSync(join(demoDir, 'webui', 'config.html')), true, '样例页面保留（作写法参考）')
  const demo = JSON.parse(readFileSync(join(demoDir, 'plugin.json'), 'utf8'))
  assert.equal(demo.ui?.views, undefined, 'demo-python 不得声明 ui.views（否则是 404 死入口）')
  assert.equal(demo.ui?.menus, undefined, 'demo-python 不得声明引用页面的菜单')
  assert.equal(demo.ui?.statusbar, undefined, 'demo-python 不得声明引用页面的状态栏入口')

  for (const id of ['browser', 'codebase', 'desktop', 'file', 'mcp', 'task', 'websearch']) {
    const manifest = JSON.parse(readFileSync(join(initPluginsDir, id, 'plugin.json'), 'utf8'))
    assert.equal(manifest.ui?.selfServe, true, `${id} plugin.json 须声明 ui.selfServe: true`)
    for (const view of manifest.ui.views ?? []) {
      const asset = join(repoDir, '..', 'chonkpilot-plugins', id, view.entry)
      assert.equal(existsSync(asset), true, `${id} 缺页面资源 ${view.entry}（唯一源应在 chonkpilot-plugins/${id}/）`)
    }
  }
})

/**
 * ⑧ 源码守卫：前端不再存在 `/plugin-ui/` 的**资源拼接**用法。
 *
 * 旧前缀（宿主直读磁盘 webui）已下线；`/plugin-ui/` 下只剩共享桥 SDK，
 * 因此前端源码只允许出现 `_sdk/plugin.js` 字面量（当前为 0 处——页面在插件侧引用）。
 */
test('⑧ 源码守卫：无 /plugin-ui/ 资源拼接（仅允许 _sdk/plugin.js 字面量）', () => {
  const offenders = []
  for (const file of walkFiles(srcDir)) {
    const code = readFileSync(file, 'utf8')
    for (const m of code.matchAll(/\/plugin-ui\/[^\s"'`)]*/g)) {
      if (m[0] === '/plugin-ui/_sdk/plugin.js') continue
      // 2026-10-25（D-70 ②）：`packages/plugin-ui/**` 是同仓 npm 包的真实导入路径，不是旧资源前缀。
      if (code.slice(Math.max(0, m.index - 'packages'.length), m.index) === 'packages') continue
      offenders.push(`${relative(srcDir, file)}: ${m[0]}`)
    }
  }
  assert.deepEqual(offenders, [], `不得残留旧前缀资源用法：${offenders.join(', ')}`)
})
