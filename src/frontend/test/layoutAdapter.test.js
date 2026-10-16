/**
 * 自由布局容器 · 适配层守卫（UILAYOUT-1 §3.4 / §3.5 / §3.9 / §3.13 · §7 P3①②③⑤）。
 *
 * 覆盖：
 *   ① 区块注册表（8 个一期区块：字段齐备 / key 唯一 / 一期全 singleton / 组件映射）
 *   ② 命令模型与落点（`open-view` + 4 类语法糖；**mq 驱动 + 监听断言**，零新增主题）
 *   ③ 持久化（`layout.free` 读写形状 / 默认文档结构 / 旧字段迁移只承接尺寸类 / 解析失败回落）
 *   ④ 布局形态判定（`?layout=` + 有/无 `layout.free` 四象限）
 *   ⑤ 装配落点（main.js 顺序 / MainLayout 双分支与旧分支保留 / 主题映射与去重）
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ 入口类（`layout/index.js`、`.vue`）用
 * **源码守卫**（与既有约定一致，见 `layoutFreeMechanism.test.js`）；纯逻辑模块直接 import 断言。
 */
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { register } from 'node:module'
import { createPinia, setActivePinia } from 'pinia'

// 子系统（layout-free）内部导入无扩展名 → 直载 `.js` / `.vue` 需要 SFC 钩子（见 support/*）。
// ⚠️ 静态 import 在本文件执行前解析：**凡依赖链触到 `layout-free/` 的模块必须动态 import**
//    （见下方 `await import`），纯模块（`view-definitions` / `mode` / `i18n`）可静态引。
register('./support/vue-sfc-hooks.mjs', import.meta.url)
await new Promise((r) => setTimeout(r, 0))

import { VIEW_DEFINITIONS, buildViewRegistry, definitionOf, hostOf } from '../src/layout/view-definitions.js'
import { LAYOUT_MODE_FREE, LAYOUT_MODE_LEGACY, parseLayoutParam, resolveLayoutMode } from '../src/layout/mode.js'
import { setLayoutTranslator } from '../src/layout/i18n.js'
import mq from '../src/utils/mq.js'
import { EventNames } from '../src/events/event-names.js'

const {
  FILE_TARGET,
  PREVIEW_TAB_TARGETS,
  SESSION_TARGET,
  TARGET,
  describeCommand,
  executeCommand,
  normalizeCommand,
  resolveCommand,
} = await import('../src/layout/commands.js')
const {
  DOC_VERSION,
  applyDocToConfig,
  createLayoutPersist,
  defaultLayoutDoc,
  extractFreeDoc,
  migrateLegacyDoc,
  parseFreeDoc,
  toFreeString,
} = await import('../src/layout/persist.js')
const { EventNames: LfEventNames } = await import('../src/layout-free/events/event-names.js')

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = (rel) => readFileSync(join(HERE, '..', 'src', rel), 'utf8')
/** 去掉注释后再断言（注释里会**引述**已移除/不再使用的字段名，如 §3.9 更正注） */
const stripComments = (src) =>
  src
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^[ \t]*\/\/.*$/gm, '')
const CODE = (rel) => stripComments(SRC(rel))

const realFetch = globalThis.fetch
before(() => {
  // `mq.emit` 会经 `/publish` 发后端（Node 下无同源服务）→ 桩掉 fetch（信封形状同桥）
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ ok: true, result: {}, errors: [] }) })
})
after(() => {
  globalThis.fetch = realFetch
})

/* ═══════════════ ① 区块注册表（§3.4 · P3①） ═══════════════ */

/** 布局区块（区域 / 抽屉内容）*/
const LAYOUT_VIEWS = ['explorer', 'preview', 'chat', 'session-tree', 'session-chat', 'task-detail', 'dsl-steps']
/** 栏视图条目（UILAYOUT-1 §7 P4① / 裁决 2：实时 / 条件 / 动态项）*/
const BAR_VIEWS = [
  'search',
  'recent-dirs',
  'chrome-warn',
  'logout',
  'memory-token',
  'queue-status',
  'index-badge',
  'lang-switcher',
]
const EXPECTED_VIEWS = [...LAYOUT_VIEWS, ...BAR_VIEWS]

test('① 全部区块定义齐备，字段完整（key/labelKey/label/icon/mode/host/designable）', () => {
  assert.deepEqual(VIEW_DEFINITIONS.map((v) => v.key), EXPECTED_VIEWS, '定义顺序 = 布局区块 + 栏视图条目')
  for (const d of VIEW_DEFINITIONS) {
    assert.ok(d.labelKey && d.label, `${d.key} 缺 labelKey/label`)
    assert.equal(d.labelKey, `view.${d.labelKey.split('.')[1]}`, `${d.key} 的 labelKey 须在 view.* 下`)
    assert.ok(d.icon, `${d.key} 缺 icon`)
    assert.equal(d.mode, 'singleton', `${d.key} 一期必须 singleton（§6：现有主题全局广播、无实例过滤）`)
    assert.ok(['layout', 'bar', 'dialog'].includes(d.host), `${d.key} host 非法：${d.host}`)
    assert.equal(typeof d.designable, 'boolean', `${d.key} 缺 designable`)
  }
  assert.equal(definitionOf('chat').host, 'layout')
  assert.equal(hostOf('search'), 'bar', 'search 是栏内嵌 view（§3.4）')
  assert.equal(hostOf('nope'), null)
  // 栏视图条目（P4①）：全部 host = bar
  for (const k of BAR_VIEWS) assert.equal(hostOf(k), 'bar', `${k} 应为栏内嵌 view`)
  // 设计态兜底（§3.11）：需要真实数据的区块标记 designable:false
  assert.equal(definitionOf('task-detail').designable, false)
  assert.equal(definitionOf('dsl-steps').designable, false)
})

test('① 组件映射：全部区块接真实组件（含栏视图条目 search 等，P4 抽取后接线）', () => {
  const src = SRC('layout/views.registry.js')
  for (const key of EXPECTED_VIEWS) {
    assert.match(src, new RegExp(`(^|\\n)\\s+'?${key.replace('-', '\\-')}'?:\\s`, 'm'), `组件映射缺 ${key}`)
  }
  // P4①：search 组件映射已接线（P3 时刻意留空）
  assert.match(src, /search:\s*SearchBoxView/, 'search 组件映射须已接线（P4 抽取）')
  assert.match(src, /export function createViewRegistry\(translate\)/)
  assert.match(src, /buildViewRegistry\(translate, VIEW_COMPONENTS\)/)
  // 注册表对象行为（纯逻辑侧）：definitions 为 getter（label 经注入的 i18n 解析）
  const fake = { chat: { name: 'FakeChat' } }
  const reg = buildViewRegistry((k) => `T:${k}`, fake)
  assert.equal(reg.definitions.find((d) => d.key === 'chat').label, 'T:view.chat')
  assert.equal(reg.componentKeys, undefined)
  assert.equal(reg.resolve('chat'), fake.chat)
  assert.equal(reg.resolve('search'), null)
})

/* ═══════════════ ② 命令模型与落点（§3.5 · P3②） ═══════════════ */

test('② 4 类语法糖归一到 open-view / close-tabs-active（编辑 UI 不变）', () => {
  assert.deepEqual(normalizeCommand({ type: 'open-dialog', viewKey: 'settings-llm' }), {
    type: 'open-view',
    viewKey: 'settings-llm',
    instanceKey: null,
    target: TARGET.DIALOG,
    params: null,
  })
  const tabs = normalizeCommand({ type: 'open-tabs', viewKey: 'settings-mcp', targetLeafId: 'leaf_x' })
  assert.equal(tabs.target, 'leaf_x', 'open-tabs 的 targetLeafId → target')
  const drawer = normalizeCommand({ type: 'open-drawer', viewKey: 'explorer', drawerId: 'drawer_files' })
  assert.equal(drawer.target, 'drawer_files')
  assert.equal(drawer.drawerId, 'drawer_files')
  assert.deepEqual(normalizeCommand({ type: 'close-tabs-active', targetLeafId: 'leaf_x' }), {
    type: 'close-tabs-active',
    target: 'leaf_x',
  })
  assert.equal(normalizeCommand({ type: 'open-view', viewKey: 'chat' }).target, 'auto')
  assert.equal(normalizeCommand({}), null)
  assert.equal(normalizeCommand({ type: 'nope' }), null)
})

test('② 落点映射：功能页 / 文件 / 会话 / 注册表区块 / 抽屉 / 拒绝分支', () => {
  // 功能页类 → preview-tab-open { kind }
  for (const kind of PREVIEW_TAB_TARGETS) {
    const r = resolveCommand({ type: 'open-view', viewKey: kind })
    assert.equal(r.ok, true, `${kind} 应可解析`)
    assert.equal(r.topic, EventNames.previewTabOpen)
    assert.deepEqual(r.payload, { kind })
  }
  // 文件
  const file = resolveCommand({ type: 'open-view', viewKey: FILE_TARGET, instanceKey: 'a/b.md' })
  assert.equal(file.topic, EventNames.fileOpen)
  assert.deepEqual(file.payload, { path: 'a/b.md', temporary: true })
  assert.equal(resolveCommand({ type: 'open-view', viewKey: FILE_TARGET }).reasonKey, 'fileNeedPath')
  // 会话
  const sess = resolveCommand({ type: 'open-view', viewKey: SESSION_TARGET, instanceKey: 's-1' })
  assert.equal(sess.topic, EventNames.sessionSelect)
  assert.deepEqual(sess.payload, { session_id: 's-1' })
  assert.equal(resolveCommand({ type: 'open-view', viewKey: SESSION_TARGET }).reasonKey, 'sessionNeedId')
  // 注册表 layout 区块 → 机制层自由页签区（既有机制主题，零新增）
  const area = resolveCommand({ type: 'open-view', viewKey: 'explorer' })
  assert.equal(area.topic, LfEventNames.tabsOpen)
  assert.deepEqual(area.payload, { targetLeafId: null, viewKey: 'explorer', instanceKey: null })
  // 栏内嵌 view 不能进自由页签
  assert.equal(resolveCommand({ type: 'open-view', viewKey: 'search' }).reasonKey, 'barViewNotInTabs')
  // 抽屉
  const drawer = resolveCommand({ type: 'open-view', viewKey: 'explorer', target: 'drawer', drawerId: 'd1' })
  assert.equal(drawer.topic, LfEventNames.drawerOpen)
  assert.deepEqual(drawer.payload, { drawerId: 'd1', viewKey: 'explorer', instanceKey: null })
  assert.equal(
    resolveCommand({ type: 'open-view', viewKey: 'explorer', target: 'drawer' }).reasonKey,
    'drawerNeedId',
  )
  // target = 已知 drawerId
  const byId = resolveCommand(
    { type: 'open-view', viewKey: 'explorer', target: 'drawer_files' },
    { hasDrawer: (id) => id === 'drawer_files' },
  )
  assert.equal(byId.topic, LfEventNames.drawerOpen)
  assert.equal(byId.payload.drawerId, 'drawer_files')
  // 对话框目标：一期无 host:'dialog' 区块 → 拒绝
  assert.equal(resolveCommand({ type: 'open-dialog', viewKey: 'settings-llm' }).reasonKey, 'noDialogHost')
  // 未知 view
  assert.equal(resolveCommand({ type: 'open-view', viewKey: 'unknown-key' }).reasonKey, 'unknownTarget')
  assert.equal(resolveCommand({ type: 'open-view' }).reasonKey, 'missingView')
  assert.equal(resolveCommand(null).reasonKey, 'missingCommand')
  // close-tabs-active → 既有「关激活页签」（§3.5）
  const close = resolveCommand({ type: 'close-tabs-active' })
  assert.equal(close.topic, EventNames.previewTabCloseThis)
})

test('② mq 驱动 + 监听断言：命令落到既有主题、payload 与既有消费面一致', async () => {
  const seen = []
  const offs = [
    mq.on(EventNames.previewTabOpen, (p) => seen.push(['preview-tab-open', p])),
    mq.on(EventNames.fileOpen, (p) => seen.push(['file-open', p])),
    mq.on(LfEventNames.tabsOpen, (p) => seen.push([LfEventNames.tabsOpen, p])),
  ]
  try {
    // open-dialog（对话框目标）在一期**无 dialog 宿主区块 → 拒绝**（§3.5），故这里用 open-tabs
    executeCommand({ type: 'open-tabs', viewKey: 'settings-llm', targetLeafId: null })
    executeCommand({ type: 'open-tabs', viewKey: 'settings-project', targetLeafId: null })
    executeCommand({ type: 'open-view', viewKey: FILE_TARGET, instanceKey: 'x.md' })
    executeCommand({ type: 'open-view', viewKey: 'chat' })
    executeCommand({ type: 'open-dialog', viewKey: 'settings-llm' }) // 拒绝：不应产生任何消息
    await mq.emit(EventNames.previewTabOpen, { kind: 'noop' }) // 让本地分发跑完（同一次 tick）
  } finally {
    offs.forEach((off) => off())
  }
  assert.deepEqual(seen, [
    ['preview-tab-open', { kind: 'settings-llm' }],
    ['preview-tab-open', { kind: 'settings-project' }],
    ['file-open', { path: 'x.md', temporary: true }],
    [LfEventNames.tabsOpen, { targetLeafId: null, viewKey: 'chat', instanceKey: null }],
    ['preview-tab-open', { kind: 'noop' }],
  ])
})

test('② 拒绝原因走注入的 i18n（`layoutFree.command.reject.*`）；摘要同源', () => {
  setLayoutTranslator((key) => `T:${key}`)
  const rej = resolveCommand({ type: 'open-view', viewKey: 'search' })
  assert.equal(rej.reason, 'T:command.reject.barViewNotInTabs')
  assert.equal(describeCommand({ type: 'open-tabs', viewKey: 'explorer' }), 'T:command.describe.openView')
  assert.equal(describeCommand({ type: 'close-tabs-active' }), 'T:command.describe.closeActive')
  assert.equal(describeCommand({}), 'T:command.describe.none')
})

test('② 零新增主题：commands.js 只用既有 EventNames（无裸 kebab 主题字面量）', () => {
  const src = SRC('layout/commands.js')
  assert.doesNotMatch(src, /emit\(\s*'/, '不得用裸主题字面量（须走 EventNames / LfEventNames）')
  assert.match(src, /from '\.\.\/events\/event-names'/, '宿主落点用宿主 EventNames')
  assert.match(src, /from '\.\.\/layout-free\/events\/event-names'/, '机制层落点用机制层 EventNames')
  // 不 import `deps`/`stores`（适配层只做映射，执行经注入的 mq）
  assert.doesNotMatch(src, /from '\.\.\/layout-free\/(stores|config)\//, '标注为不依赖机制层状态')
})

/* ═══════════════ ③ 持久化（§3.9 · P3③） ═══════════════ */

test('③ 默认文档 = 现布局等价结构（row[column[row[explorer|preview], row[session-tree|session-chat]] | chat]）', () => {
  const doc = defaultLayoutDoc()
  assert.equal(doc.version, DOC_VERSION)
  assert.equal(doc.layout.type, 'split')
  assert.equal(doc.layout.direction, 'row')
  const [left, right] = doc.layout.children
  assert.equal(left.direction, 'column')
  assert.deepEqual(
    left.children.map((c) => c.children.map((l) => l.view.viewKey)),
    [
      ['explorer', 'preview'],
      ['session-tree', 'session-chat'],
    ],
  )
  assert.equal(right.view.viewKey, 'chat')
  // 所有引用都在注册表内（否则渲染占位 / 校验报错）
  const refs = []
  const walk = (n) => {
    if (n.type === 'leaf') refs.push(n.view?.viewKey)
    ;(n.children || []).forEach(walk)
  }
  walk(doc.layout)
  // 布局区域只用 5 个区块（task-detail / dsl-steps 留给抽屉 / 自由页签；栏视图条目不在布局里）
  assert.deepEqual(refs.filter(Boolean).sort(), ['chat', 'explorer', 'preview', 'session-chat', 'session-tree'].sort())
  assert.equal(EXPECTED_VIEWS.includes(refs.find((k) => k && !VIEW_DEFINITIONS.some((d) => d.key === k))), false)
  // 每个容器占比和 = 1；区域尺寸约束合法（min ≤ max）
  const check = (n) => {
    if (n.type === 'split') {
      const sum = n.sizes.reduce((a, b) => a + b, 0)
      assert.ok(Math.abs(sum - 1) < 0.01, `${n.id} 占比和 ≠ 1`)
      assert.equal(n.sizes.length, n.children.length)
      n.children.forEach(check)
    } else {
      if (n.minWidth) assert.ok(n.maxWidth ? n.maxWidth >= n.minWidth : true)
    }
  }
  check(doc.layout)
})

test('③ 默认文档：栏 / 自定义工具栏 / 抽屉非空，含编辑器入口条目（P4①③）', () => {
  const doc = defaultLayoutDoc()
  assert.ok(doc.topToolbar.items.length >= 10, `顶部工具栏条目应条目化（实际 ${doc.topToolbar.items.length}）`)
  assert.ok(doc.statusbar.items.length >= 4, `状态栏条目应条目化（实际 ${doc.statusbar.items.length}）`)
  assert.ok(doc.toolbars.length >= 1, '自定义工具栏非空（开放「自定义工具栏」页签）')
  assert.ok(doc.drawers.length >= 1, '抽屉示例非空（P4③）')
  // 编辑器入口条目：发布机制层既有主题 `lf-editor-open`
  const all = [
    ...doc.topToolbar.items,
    ...doc.statusbar.items,
    ...doc.toolbars.flatMap((t) => t.items),
  ]
  const flat = all.flatMap((it) => [it, ...(it.children ?? [])])
  assert.ok(
    flat.some((it) => it.command?.type === 'publish-mq' && it.command.topic === 'lf-editor-open'),
    '默认栏必须含「打开内容设置编辑器」的条目（lf-editor-open）',
  )
  // 抽屉示例可开 / 关：含 toggle-drawer 命令
  assert.ok(
    flat.some((it) => it.command?.type === 'publish-mq' && it.command.topic === 'lf-layout-toggle-drawer'),
    '默认栏必须含抽屉开 / 关条目',
  )
  // 「发布 MQ 动作」只能引用既有主题：默认文档里的 topic 必须都是既有常量
  const LfTopics = new Set(Object.values(LfEventNames))
  const HostTopics = new Set(Object.values(EventNames))
  for (const it of flat) {
    if (it.command?.type !== 'publish-mq') continue
    assert.ok(LfTopics.has(it.command.topic) || HostTopics.has(it.command.topic), `非既有主题：${it.command.topic}`)
  }
})

test('③ 旧 7 字段迁移：只承接尺寸类 4 项（显隐三键忽略，§3.9 更正注）', () => {
  const withVisibility = migrateLegacyDoc({ filetreeOpen: false, taskOpen: false, chatOpen: false })
  assert.deepEqual(withVisibility, migrateLegacyDoc({}), '三显隐键必须被忽略（三区已常显）')
  const small = migrateLegacyDoc({ filetreeWidth: 100, chatWidth: 100, sessiontreeWidth: 100, filetreeHeight: 300 })
  const big = migrateLegacyDoc({ filetreeWidth: 1200, chatWidth: 2000, sessiontreeWidth: 1200 })
  // clamp 下界/上界生效（与既有 MainLayout 的阈值同源）
  assert.equal(small.layout.children[0].children[0].children[0].maxWidth, 600)
  assert.notDeepEqual(small.layout.sizes, big.layout.sizes, '尺寸类字段必须反映到占比')
  // 结构不变（只有占比/尺寸不同）
  const shape = (d) => JSON.stringify(d.layout, (k, v) => (k === 'sizes' ? undefined : k === 'id' ? undefined : v))
  assert.equal(shape(small), shape(big))
})

test('③ parseFreeDoc / extractFreeDoc：合法解析、非法回落 null（§6 不静默丢库）', () => {
  const doc = defaultLayoutDoc()
  assert.ok(parseFreeDoc(JSON.stringify(doc)))
  assert.ok(parseFreeDoc(doc), '对象形态亦接受（桥侧可能是对象）')
  assert.equal(parseFreeDoc('{bad json'), null)
  assert.equal(parseFreeDoc('{"nope":1}'), null, '缺 layout → null')
  assert.equal(parseFreeDoc(''), null)
  assert.equal(parseFreeDoc(null), null)
  assert.ok(extractFreeDoc({ free: JSON.stringify(doc), filetreeWidth: 320 }), '从 init-data 的 layout 子对象取 free')
  assert.equal(extractFreeDoc({ filetreeWidth: 320 }), null, '无 free → null（调用方回落 legacy）')
  assert.equal(extractFreeDoc(null), null)
  assert.deepEqual(JSON.parse(toFreeString(doc)), doc)
  assert.equal(toFreeString(null), '')
})

test('③ applyDocToConfig：live/draft/savedJson 三处同步、清 dirty 与错误列表', () => {
  const config = { live: null, draft: null, savedJson: '', dirty: true, messages: ['x'] }
  assert.equal(applyDocToConfig(config, defaultLayoutDoc()), true)
  assert.ok(config.live.layout)
  assert.notEqual(config.live, config.draft, 'live / draft 必须是两份（编辑草稿不污染生效态）')
  assert.equal(config.dirty, false)
  assert.deepEqual(config.messages, [])
  assert.ok(JSON.parse(config.savedJson).layout)
  assert.equal(applyDocToConfig(config, null), false)
})

test('③ createLayoutPersist：save 写 `{free}` 子键（零新增字段名）；失败返回 ok:false 不抛', async () => {
  const calls = []
  const persist = createLayoutPersist({ save: async (state) => calls.push(state) })
  assert.deepEqual(await persist.save(defaultLayoutDoc()), { ok: true })
  assert.equal(calls.length, 1)
  assert.deepEqual(Object.keys(calls[0]), ['free'], '只写 layout.free 子键（§3.9）')
  assert.ok(JSON.parse(calls[0].free).layout)

  const failing = createLayoutPersist({
    save: async () => {
      throw new Error('backend down')
    },
  })
  assert.deepEqual(await failing.save(defaultLayoutDoc()), { ok: false, error: 'backend down' })
  assert.deepEqual(await createLayoutPersist().save(defaultLayoutDoc()), { ok: true }, '未注入 IO = no-op')

  // load / adopt / adoptDefault
  const loaded = createLayoutPersist({ load: async () => ({ free: toFreeString(defaultLayoutDoc()) }) })
  assert.ok((await loaded.load()).layout)
  const config = { live: null, draft: null, savedJson: '', dirty: true, messages: [] }
  assert.equal(loaded.adopt(config, { free: toFreeString(defaultLayoutDoc()) }), true)
  assert.equal(loaded.adopt(config, { filetreeWidth: 320 }), false, '无 free 文档 → 不采用（保持 legacy）')
  assert.equal(loaded.adoptDefault(config), true)
  assert.equal(await createLayoutPersist().load(), null, '未注入 load → null')
})

test('③ 落库形状守卫：persist.js 不含三显隐键、只读尺寸类旧字段', () => {
  const src = CODE('layout/persist.js')
  for (const k of ['filetreeOpen', 'taskOpen', 'chatOpen']) {
    assert.doesNotMatch(src, new RegExp(k), `${k} 已失去语义，不得出现在迁移里（§3.9 更正注）`)
  }
  for (const k of ['filetreeWidth', 'filetreeHeight', 'chatWidth', 'sessiontreeWidth']) {
    assert.match(src, new RegExp(k), `尺寸类字段 ${k} 须承接`)
  }
})

/* ═══════════════ ④ 布局形态判定（§3.9 / §3.13） ═══════════════ */

test('④ parseLayoutParam：只认 legacy|free，其它忽略', () => {
  assert.equal(parseLayoutParam('?layout=legacy'), LAYOUT_MODE_LEGACY)
  assert.equal(parseLayoutParam('?layout=free'), LAYOUT_MODE_FREE)
  assert.equal(parseLayoutParam('?layout=nope'), '')
  assert.equal(parseLayoutParam('?other=1'), '')
  assert.equal(parseLayoutParam(''), '')
  assert.equal(parseLayoutParam(), '')
})

test('④ resolveLayoutMode：默认 free；`?layout=legacy` 回退旧分支（P4⑤ 裁决 3）', () => {
  assert.equal(resolveLayoutMode({ forced: '' }), LAYOUT_MODE_FREE)
  assert.equal(resolveLayoutMode({}), LAYOUT_MODE_FREE)
  assert.equal(resolveLayoutMode({ forced: 'free' }), LAYOUT_MODE_FREE)
  assert.equal(resolveLayoutMode({ forced: 'legacy' }), LAYOUT_MODE_LEGACY)
  assert.equal(resolveLayoutMode({ forced: 'legacy', hasFreeDoc: true }), LAYOUT_MODE_LEGACY)
  assert.equal(resolveLayoutMode({ forced: '', hasFreeDoc: true }), LAYOUT_MODE_FREE)
  assert.equal(resolveLayoutMode({ forced: 'legacy', hasFreeDoc: false }), LAYOUT_MODE_LEGACY)
})

/* ═══════════════ ⑤ 装配落点与主题（§3.10 / P3④⑤） ═══════════════ */

test('⑤ main.js：theme.css 在宿主 global.css 之后；引擎装配在 pinia/mqPlugin 之后、mount 之前', () => {
  const main = SRC('main.js')
  const iGlobal = main.indexOf("import './assets/styles/global.css'")
  const iTheme = main.indexOf("import './layout/theme.css'")
  const iPinia = main.indexOf('app.use(createPinia())')
  const iMq = main.indexOf('app.use(mqPlugin)')
  const iEngine = main.indexOf('setupLayoutEngine(app)')
  const iMount = main.indexOf("app.mount('#app')")
  assert.ok(iGlobal >= 0 && iTheme > iGlobal, '主题映射须在宿主全局样式之后载入')
  assert.ok(iPinia >= 0 && iMq > iPinia, 'mqPlugin 在 pinia 之后（既有顺序不变）')
  assert.ok(iEngine > iMq, '引擎装配须在 mqPlugin 之后（v-mq 复用 + store 可用）')
  assert.ok(iMount > iEngine, '引擎装配须在 mount 之前')
  assert.match(main, /import \{ setupLayoutEngine \} from '\.\/layout'/)
})

test('⑤ MainLayout：双分支（容器 / 旧布局），旧渲染路径保留可回退；free 形态顶栏 / 状态栏条目化', () => {
  const ml = SRC('views/layout/MainLayout.vue')
  assert.match(ml, /<ChonkLayout v-if="freeMode" \/>/, '容器装配点')
  assert.match(ml, /<SplitPanel v-else direction="horizontal"/, '旧布局分支必须保留（可回退/对比）')
  assert.match(ml, /const freeMode = ref\(layoutForced !== LAYOUT_MODE_LEGACY\)/, 'P4⑤：默认 free')
  assert.match(ml, /resolveLayoutMode\(\{ forced: layoutForced \}\)/, 'P4⑤：形态判定不再依赖 hasFreeDoc')
  assert.match(ml, /applyFreeLayout\(r\[GuiInitDataKeys\.layout\] \|\| null\)/, '启动恢复走 init-data')
  assert.match(ml, /import ChonkLayout from '\.\.\/\.\.\/layout\/ChonkLayout\.vue'/)
  // P4①：free 形态顶栏 / 状态栏由条目化外壳渲染（legacy 分支保留原组件）
  assert.match(ml, /<ChonkTopBar v-if="freeMode" \/>/, 'free 形态顶栏外壳')
  assert.match(ml, /<ChonkStatusBar v-if="freeMode" \/>/, 'free 形态状态栏外壳')
  assert.match(ml, /<Toolbar v-else \/>/, 'legacy 顶栏保留')
  assert.match(ml, /<StatusBar v-else \/>/, 'legacy 状态栏保留')
  assert.match(ml, /<LfDrawerHost \/>/, '容器运行期外壳：具名抽屉')
  assert.match(ml, /<LfContentSettingsDialog \/>/, '容器运行期外壳：内容设置编辑器')
  // 自由形态不写旧 7 字段（尺寸落 layout.free）
  const deb = ml.match(/function saveLayoutDebounced\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(deb, '未找到 saveLayoutDebounced')
  assert.match(deb[0], /if \(freeMode\.value\) return/, '自由形态不得回写旧尺寸字段')
  // beacon 兜底按形态投递
  assert.match(ml, /\{ free: toFreeString\(/)
  // 既有口径未被破坏（P2 收尾成果）
  assert.match(ml, /\{ id: 'chat', size: effChatWidth\.value, min: 280, max: 800 \}/)
  assert.match(ml, /visible: !narrow\.value/)
  assert.doesNotMatch(CODE('views/layout/MainLayout.vue'), /(chatOpen|filetreeOpen|taskOpen)/, '三区显隐须保持已移除')
})

test('⑤ 主题映射与去重：--lf-* → 宿主 token；机制层不再声明宿主级全局规则', () => {
  const theme = SRC('layout/theme.css')
  assert.match(theme, /@import '\.\.\/layout-free\/styles\/global\.css'/)
  for (const [lf, host] of [
    ['--lf-bg', '--bg-primary'],
    ['--lf-panel', '--bg-secondary'],
    ['--lf-border', '--border'],
    ['--lf-text', '--text-primary'],
    ['--lf-text-weak', '--text-muted'],
    ['--lf-accent', '--accent'],
    ['--lf-bar', '--toolbar-bg'],
    ['--lf-tree-hover', '--bg-hover'],
  ]) {
    assert.match(theme, new RegExp(`${lf}:\\s*var\\(${host}\\)`), `${lf} 须映射到 ${host}`)
  }
  // 去重（§3.10）：宿主的全局外观（html/body/滚动条）不得被子系统样式覆盖
  const lfGlobal = CODE('layout-free/styles/global.css')
  assert.doesNotMatch(lfGlobal, /^(html,\s*)?body\s*\{/m, '子系统不得再声明 body 全局规则')
  assert.doesNotMatch(lfGlobal, /::-webkit-scrollbar/, '子系统不得再声明全局滚动条规则')
})

test('⑤ 装配入口：layout/index.js 只注入宿主实现（mq/views/persist/i18n/icons/dialog）', () => {
  const idx = SRC('layout/index.js')
  for (const token of [
    'createLayoutEngine',
    'createViewRegistry',
    'createLayoutTranslator',
    'createLayoutPersist',
    'loadInitData',
    'saveLayoutState',
    "from '../components/dialog'",
    "from '../components/icon'",
    "from '../components/split'",
    'engine.install(app)',
  ]) {
    assert.ok(idx.includes(token), `layout/index.js 缺：${token}`)
  }
  // ChonkLayout：只传数据（layout / preview / selectedId），交互全走 mq（§3.2）
  const chonk = SRC('layout/ChonkLayout.vue')
  assert.match(chonk, /<LfFreeLayoutContainer/)
  assert.match(chonk, /:layout="renderLayout"/)
  assert.doesNotMatch(chonk, /@click|@change/, '容器组件不得在装配点写交互')
})

/* ═══════════════ ⑥ 端到端装配（真引擎 + 真 store + 真默认文档） ═══════════════ */

test('⑥ 集成：注册表 + 默认文档 → 引擎装配成功、渲染树可用、保存前校验零错误', async () => {
  setActivePinia(createPinia())
  const { createLayoutEngine } = await import('../src/layout-free/engine.js')
  const fake = Object.fromEntries(EXPECTED_VIEWS.map((k) => [k, { name: `Fake_${k}` }]))
  const persisted = []
  const engine = createLayoutEngine({
    views: buildViewRegistry((k) => k, fake),
    persist: createLayoutPersist({ save: async (s) => persisted.push(s) }),
    i18n: (key) => key,
  })
  // 最小 app 桩：install 只用到 指令已装检测 / component 注册（v-mq 复用宿主）
  const app = { _context: { directives: { mq: () => {} } }, component() {}, use() {} }
  engine.install(app)

  // 默认文档灌进布局域 store（等价 MainLayout 的 `?layout=free` / 首次采用路径）
  assert.equal(engine.state.config.live.layout.type, 'split', '装配后 store 可用')
  applyDocToConfig(engine.state.config, defaultLayoutDoc())

  const tree = engine.renderLayout
  assert.equal(tree.type, 'split')
  const leaves = []
  const walk = (n) => {
    if (n.type === 'leaf') leaves.push(n)
    ;(n.children || []).forEach(walk)
  }
  walk(tree)
  assert.deepEqual(
    leaves.map((l) => l.view.viewKey).sort(),
    ['chat', 'explorer', 'preview', 'session-chat', 'session-tree'],
    '默认文档的 5 个区块都能被注册表解析',
  )
  assert.deepEqual(engine.state.config.validate(), [], '默认文档必须通过保存前校验（引用/单例/尺寸）')
  assert.equal(engine.editing, false)
  assert.equal(engine.selectedNodeId, '')

  // 落库：容器把当前文档交给注入的 persist（`gui.ui.save { layout: { free } }`）
  engine.state.config.saveLiveLayout()
  await new Promise((r) => setTimeout(r, 0)) // 落库是 fire-and-forget（失败只提示，不阻断交互）
  assert.equal(persisted.length, 1, 'saveLiveLayout → persist.save 一次')
  assert.deepEqual(Object.keys(persisted[0]), ['free'])
  assert.ok(JSON.parse(persisted[0].free).layout)
})
