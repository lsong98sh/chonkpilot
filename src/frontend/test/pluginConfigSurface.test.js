/**
 * PLUGIN-UI-1 第三段前端守卫：6 个插件（browser/desktop/file/mcp/task/websearch）配置面收口。
 *
 * 覆盖：
 *   ① manifest 仍是配置页唯一入口（`ui.views` id=config + 动作面 load/save/reset），
 *      且不再声明行为宿主写入口 `config.fields`（单源：插件私有存储 + 动作面）；
 *   ② 插件页只走 `POST /plugins/<id>/api/{load,save,reset}`，不再直调 config.* / 宿主方法；
 *   ③ 设置导航（设置 → 插件维护）能打开 6 个插件的配置页（`plugin:<id>:config` 注册面）；
 *   ④ 无遗留 Vue 设置页改同一批配置（框架级 FileTreeConfig 只写 filetree.hide-dirs）。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ 纯逻辑 import + 源码 / manifest 守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { definitionOf } from '../src/layout/view-definitions.js'
import { registerPluginViews, registeredViewKeys, unregisterPluginViews } from '../src/layout/view-registry.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoDir = join(here, '..', '..', '..')
const PLUGINS = ['browser', 'desktop', 'file', 'mcp', 'task', 'websearch']
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const manifestOf = (id) =>
  JSON.parse(readFileSync(join(repoDir, 'src', 'initdata', 'capability', 'plugins', id, 'plugin.json'), 'utf8'))
const pluginsDir = join(repoDir, '..', 'chonkpilot-plugins')
const pageOf = (id) => readFileSync(join(pluginsDir, id, 'webui', 'config.html'), 'utf8')

/** 递归列出目录下全部文件（不存在 → []）。 */
function walk(dir) {
  let out = []
  let entries
  try {
    entries = readdirSync(dir, { withFileTypes: true })
  } catch {
    return out
  }
  for (const e of entries) {
    const p = join(dir, e.name)
    if (e.isDirectory()) out = out.concat(walk(p))
    else if (/\.(js|vue|json)$/.test(e.name) && statSync(p).isFile()) out.push(p)
  }
  return out
}

test('① manifest：ui.views 配置页 + 动作面 rpc + 不再声明 config.fields', () => {
  for (const id of PLUGINS) {
    const m = manifestOf(id)
    assert.equal(m.ui?.selfServe, true, `${id} 须声明 ui.selfServe: true`)
    const cfg = (m.ui?.views || []).find((v) => v.id === 'config')
    assert.ok(cfg, `${id} 缺 ui.views[id=config]（设置区入口）`)
    assert.equal(cfg.entry, 'webui/config.html', `${id} 配置页 entry`)
    for (const action of ['load', 'save', 'reset']) {
      assert.ok((m.ui?.rpc || []).includes(action), `${id} ui.rpc 缺 ${action}`)
    }
    assert.equal(m.config?.fields, undefined, `${id} 不得再声明 config.fields（单源 = 插件动作面）`)
    assert.ok(m.config?.scope, `${id} config.scope 应保留（按现有 scope）`)
  }
})

test('② 插件页：只走 /api/{load,save,reset}，不直调 config.* / 宿主方法', () => {
  for (const id of PLUGINS) {
    const src = pageOf(id)
    assert.ok(src.includes(`/plugins/${id}/api/`), `${id} config.html 缺动作面路径`)
    assert.ok(src.includes('X-CK-Plugin-Id'), `${id} config.html 缺来源插件头`)
    for (const action of ["'load'", "'save'", "'reset'"]) {
      assert.ok(src.includes(action), `${id} config.html 缺 action ${action}`)
    }
    for (const banned of ["ckPlugin.call('config.", 'plugin-config-']) {
      assert.equal(src.includes(banned), false, `${id} config.html 仍直调配置写入口：${banned}`)
    }
  }
})

test('③ 设置导航：插件维护卡片按 config_view 打开 plugin:<id>:config', () => {
  // 设置菜单 → 插件维护（plugin-manager）入口存在
  const persist = read('layout/persist.js')
  assert.match(persist, /tb_set_plugins[\s\S]*plugin-manager/, '设置菜单缺「插件维护」入口')
  // 卡片：config_view → openView → plugin:<id>:<view>
  const page = readFileSync(join(here, '..', 'packages', 'plugin-ui', 'src', 'PluginManagerPage.vue'), 'utf8')
  assert.match(page, /p\.config_view/, '插件维护卡片须按 config_view 显示配置按钮')
  assert.match(page, /const viewKey = `plugin:\$\{p\.id\}:\$\{viewId\}`/, '配置按钮须打开 plugin:<id>:<view>')
  // 注册面：manifest 的 views → 可解析 key（命令落点不落空）
  for (const id of PLUGINS) {
    const m = manifestOf(id)
    registerPluginViews({ id, name: m.name, icon: m.icon, views: m.ui.views }, () => ({ name: id }))
    const key = `plugin:${id}:config`
    assert.ok(registeredViewKeys().includes(key), `${key} 未注册`)
    assert.ok(definitionOf(key), `${key} 缺 view 定义`)
    unregisterPluginViews(id)
    assert.equal(definitionOf(key), null, `${key} 卸载后应移除`)
  }
})

test('④ 无遗留 Vue 设置页改同一批配置（框架级 FileTreeConfig 不在其中）', () => {
  // 6 个插件的键前缀不得出现在任何 Vue 设置页 / 配置页
  const dirs = ['views/settings', 'views/config', 'views/preview']
  const files = dirs.flatMap((d) => walk(join(srcDir, d)))
  const bad = []
  for (const f of files) {
    const code = readFileSync(f, 'utf8')
    for (const id of PLUGINS) {
      if (code.includes(`plugins.${id}.`)) bad.push(`${f.slice(srcDir.length + 1)} → plugins.${id}.*`)
    }
    if (code.includes('plugin-config-set')) bad.push(`${f.slice(srcDir.length + 1)} → plugin-config-set`)
  }
  assert.deepEqual(bad, [], `遗留 Vue 页仍在写插件配置：\n${bad.join('\n')}`)

  // FileTreeConfig = 框架级文件树隐藏目录（宿主机读，非 file 插件默认参数）→ 保留
  const ft = read('views/settings/FileTreeConfig.vue')
  assert.match(ft, /HIDE_DIRS_KEY = 'filetree\.hide-dirs'/, 'FileTreeConfig 应只写框架级 filetree.hide-dirs')
  for (const id of PLUGINS) {
    assert.equal(ft.includes(`plugins.${id}.`), false, `FileTreeConfig 不得写 ${id} 插件配置`)
  }
})
