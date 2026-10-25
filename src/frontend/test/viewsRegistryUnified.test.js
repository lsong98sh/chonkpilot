/**
 * UIREG-1 前端守卫：**视图注册面统一** + **重复 codegraph/vfts 设置页删除**。
 *
 * 覆盖：
 *   ① 内置视图经统一注册 API（`registerViews`）接线；注册 key 集合 == `view-definitions` 定义 key 集合
 *      （逐条核对，防漏注册）；`views.registry.js` 不再有静态组件映射表；
 *   ② 内置 + 插件合并后 key 集合无缺失 / 无重复；组件解析只有一条路径（注册表 `resolve`）；
 *   ③ 跨来源重复 key → 显式告警且不互相覆盖；
 *   ④ 重复设置页已删除且 `src/` 内零残留引用；设置入口改为嵌入 codebase 插件配置页（复用
 *      `PluginPageHost`，不自造 iframe；旧页签名经兼容映射仍可定位）。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ 纯逻辑 import + 源码守卫（同 configSurface）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { VIEW_DEFINITIONS, definitionOf, hostOf } from '../src/layout/view-definitions.js'
import {
  createViewRegistry,
  registerPluginViews,
  registerViews,
  registeredViewKeys,
  unregisterPluginViews,
} from '../src/layout/view-registry.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const BUNDLED_VIEW_KEYS = VIEW_DEFINITIONS.map((d) => d.key)

/** 递归列出 `src/` 下全部文本源文件（.js / .vue / .json）。 */
function walk(dir) {
  const out = []
  for (const name of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, name.name)
    if (name.isDirectory()) out.push(...walk(full))
    else if (/\.(js|vue|json)$/.test(name.name)) out.push(full)
  }
  return out
}

/* ═══════════ ① 内置视图：统一注册 API + key 集合完整 ═══════════ */

test('① 内置视图经 registerViews 注册，key 集合 == view-definitions（无缺失）', () => {
  const src = read('layout/views.registry.js')
  // 组件解析只有一条路径：不得再有静态组件映射表
  assert.doesNotMatch(src, /VIEW_COMPONENTS/, '不得再有静态组件映射表（VIEW_COMPONENTS）')
  assert.match(src, /registerViews\(\[/, '内置视图须经统一 API registerViews 注册')

  const start = src.indexOf('registerViews([')
  const end = src.indexOf("], 'builtin')")
  assert.ok(start >= 0 && end > start, '内置注册块形态应为 registerViews([...], \'builtin\')')
  const registered = [...src.slice(start, end).matchAll(/\{\s*key:\s*'([^']+)'/g)].map((m) => m[1])
  assert.deepEqual(registered, BUNDLED_VIEW_KEYS, '注册 key 集合/顺序须与 view-definitions 完全一致')

  // 宿主装配点只负责把组件交给注册表（解析实现只在 view-registry.js）
  assert.match(src, /from '\.\/view-registry\.js'/, '须从 view-registry.js 取注册 API')
})

/* ═══════════ ② 内置 + 插件合并：无缺失 / 无重复 / 单条解析路径 ═══════════ */

test('② 内置 + 插件合并注册：key 无缺失、无重复，解析只有一条路径', () => {
  registerViews(
    BUNDLED_VIEW_KEYS.map((key) => ({ key, component: { name: `B_${key}` } })),
    'builtin',
  )
  registerPluginViews(
    {
      id: 'codebase',
      name: '代码知识库',
      icon: 'code',
      views: [
        { id: 'config', title: '代码知识库', entry: 'webui/config.html' },
        { id: 'status', title: '知识库状态', entry: 'webui/status.html' },
      ],
    },
    ({ plugin, view }) => ({ name: `P_${plugin.id}_${view.id}` }),
  )

  const pluginKeys = ['plugin:codebase:config', 'plugin:codebase:status']
  const keys = registeredViewKeys()
  assert.deepEqual(keys, [...BUNDLED_VIEW_KEYS, ...pluginKeys], '合并后 key 集合须完整（无缺失）')
  assert.equal(new Set(keys).size, keys.length, '合并后 key 不得重复')
  // 每条 key 都有定义（命令落点解析不落空）
  for (const key of keys) assert.ok(definitionOf(key), `${key} 缺 view 定义`)
  // 插件 view 定义齐备（容器 / entry 供插件页打开机制使用）
  assert.equal(hostOf(pluginKeys[0]), 'layout')
  assert.deepEqual(definitionOf(pluginKeys[0]).containers, ['preview', 'tab', 'drawer'])
  assert.equal(definitionOf(pluginKeys[0]).plugin.entry, 'webui/config.html')

  const reg = createViewRegistry((k) => `T:${k}`)
  assert.equal(reg.components, undefined, '不再暴露组件表（避免第二条解析路径）')
  assert.equal(reg.resolve('chat').name, 'B_chat')
  assert.equal(reg.resolve('search').name, 'B_search', '栏视图条目同走一条解析路径')
  assert.equal(reg.resolve(pluginKeys[0]).name, 'P_codebase_config', '插件 view 同走一条解析路径')
  assert.equal(reg.resolve('nope'), null)
  // definitions getter：内置走 i18n，插件用插件标题
  assert.equal(reg.definitions.find((d) => d.key === 'chat').label, 'T:view.chat')
  assert.equal(reg.definitions.find((d) => d.key === pluginKeys[0]).label, '代码知识库')

  // 幂等卸载：插件 view 连同定义一起移除，内置不受影响
  unregisterPluginViews('codebase')
  assert.deepEqual(registeredViewKeys(), BUNDLED_VIEW_KEYS, '插件卸载须只移除自己的 key')
  assert.equal(definitionOf(pluginKeys[0]), null, '插件 view 定义须随卸载移除')
})

/* ═══════════ ③ 跨来源重复 key：显式告警 + 不覆盖 ═══════════ */

test('③ 跨来源重复 key：显式告警且保留先注册者（内置不被插件覆盖）', () => {
  const warns = []
  const origWarn = console.warn
  console.warn = (...args) => warns.push(args.join(' '))
  try {
    registerViews([{ key: 'chat', component: { name: 'Hijack' } }], 'plugin:evil')
  } finally {
    console.warn = origWarn
  }
  assert.equal(warns.length, 1, '重复 key 须显式告警一次')
  assert.match(warns[0], /重复注册/, '告警文案须指明重复注册')
  assert.match(warns[0], /chat/, '告警须含冲突 key')
  assert.equal(createViewRegistry().resolve('chat').name, 'B_chat', '不得覆盖先注册（内置）视图')
})

/* ═══════════ ④ 重复设置页删除 + 设置入口改为插件配置页 ═══════════ */

test('④ 重复设置页已删除，src 内无残留引用', () => {
  for (const rel of ['views/settings/CodegraphConfig.vue', 'views/settings/VftsConfig.vue']) {
    assert.equal(existsSync(join(srcDir, rel)), false, `${rel} 应已删除`)
  }
  const offenders = walk(srcDir)
    .filter((f) => /CodegraphConfig|VftsConfig/.test(readFileSync(f, 'utf8')))
    .map((f) => f.slice(srcDir.length + 1))
  assert.deepEqual(offenders, [], `仍有残留引用：\n${offenders.join('\n')}`)
})

test('④ 设置入口嵌入 codebase 插件配置页（复用 PluginPageHost，不自造 iframe）', () => {
  const pc = read('views/preview/ProjectConfig.vue')
  // 复用既有插件 view 机制：PluginPageHost + 插件元数据（plugin:<id>:<view>）
  assert.match(pc, /import PluginPageHost from '\.\.\/\.\.\/\.\.\/packages\/plugin-ui\/src\/PluginPageHost\.vue'/, '须复用 PluginPageHost')
  assert.match(pc, /pluginView\(CODEBASE_PLUGIN_ID, CODEBASE_CONFIG_VIEW_ID\)/, '须按插件 view 元数据取 entry')
  assert.match(pc, /const CODEBASE_PLUGIN_ID = 'codebase'/)
  assert.match(pc, /const CODEBASE_CONFIG_VIEW_ID = 'config'/)
  assert.doesNotMatch(pc, /<iframe/, '不得自造 iframe 容器')
  // 页签：codegraph / vfts 合并为 codebase（旧页签名兼容映射，旧发送方不必改）
  assert.match(pc, /\{ label: t\('projectConfig\.codebase'\), name: 'codebase' \}/, '缺「代码知识库」页签')
  assert.match(pc, /LEGACY_TAB_ALIAS = \{ codegraph: 'codebase', vfts: 'codebase' \}/, '旧页签名须兼容映射')
  // 其余设置页保留
  for (const name of ['security', 'context', 'history', 'filetree', 'groupchat', 'log', 'configIO']) {
    assert.match(pc, new RegExp(`name: '${name}'`), `设置页签 ${name} 不得删除`)
  }
  // i18n 双语齐备
  for (const loc of ['zh-CN', 'en-US']) {
    const pcLoc = JSON.parse(readFileSync(join(srcDir, 'locales', loc, 'projectConfig.json'), 'utf8'))
    assert.ok(pcLoc.codebase, `${loc} projectConfig 缺 codebase`)
    assert.ok(pcLoc.codebase_unavailable, `${loc} projectConfig 缺 codebase_unavailable`)
  }
})
