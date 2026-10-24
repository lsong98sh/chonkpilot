/**
 * STB-1：长任务「状态栏 view + preview 详情页」前端守卫。
 *
 * 覆盖：
 *  ① 状态映射：空闲 / 未启用 / 排队 / 进行中 / 完成 / 失败（条目仍可见；失败 → 错误色 + 错误原文）
 *  ② 点击条目 → 统一命令 `open-view`（目标 = 对应详情 view；经既有 `preview-tab-open` 落 preview）
 *  ③ 详情页注册（views.registry / view-definitions / CodeView 功能页）+ 详情页含「去设置」入口
 *  ④ 去 toast（源码契约）：记忆 / 压缩进度 / 完成 / 失败不再走 `sendNotify` 轻提示
 *  ⑤ 索引详情：进度 / 文件 / 符号 / chunk / 最近索引时间 / 错误文本（不编造字段）
 *  ⑥ i18n：zh-CN / en-US 键齐备
 */
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { register } from 'node:module'

import { parseNotice, reduceTask } from '../src/composables/useQueueStatus.js'
import { taskEntry, taskTone } from '../src/utils/taskStatus.js'
import { indexDetail, statusTime } from '../src/utils/indexStatus.js'
import { EventNames } from '../src/events/event-names.js'
import { VIEW_DEFINITIONS } from '../src/layout/view-definitions.js'

// `layout/commands.js` 依赖链含**无扩展名**相对导入（打包器口径）→ 先挂 SFC/补全钩子再动态 import
// （同 layoutAdapter.test.js 口径；静态 import 会在钩子注册前解析）。
register('./support/vue-sfc-hooks.mjs', import.meta.url)
await new Promise((r) => setTimeout(r, 0))
const { openStatusDetail, STATUS_DETAIL_VIEWS } = await import('../src/layout/statusDetail.js')
const { PREVIEW_TAB_TARGETS } = await import('../src/layout/commands.js')

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

// t：直接取 zh-CN statusBar 文案（同时校验 i18n 集成）
const zh = readLocale('zh-CN', 'statusBar.json')
const zhDetail = zh.detail || {}
const t = (k) => {
  if (k.startsWith('statusBar.detail.')) return zhDetail[k.slice('statusBar.detail.'.length)] ?? k
  if (k.startsWith('statusBar.')) return zh[k.slice('statusBar.'.length)] ?? k
  return k
}

const realFetch = globalThis.fetch
before(() => {
  // `mq.emit` 会经 `/publish` 发后端（Node 下无同源服务）→ 桩掉 fetch
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ ok: true, result: {}, errors: [] }) })
})
after(() => {
  globalThis.fetch = realFetch
})

/* ═══════════ ① 状态映射 ═══════════ */

test('状态映射：空闲 / 未启用 / 排队 / 进行中 / 完成 / 失败', () => {
  // 空闲（记忆启用）→ 中性色「记忆 · 空闲」
  assert.deepEqual(
    (({ tone, text }) => ({ tone, text }))(taskEntry({ kind: 'memory', task: null, enabled: true, t })),
    { tone: 'disabled', text: '记忆 · 空闲' },
  )
  // 未启用 → 「记忆 · 未启用」（条目仍显示 = 每种任务都有一个 view）
  const disabled = taskEntry({ kind: 'memory', task: null, enabled: false, t })
  assert.equal(disabled.text, '记忆 · 未启用')
  assert.equal(disabled.tone, 'disabled')
  // 排队 / 进行中 → progress；完成 → ready；失败 → error + 错误原文进 title
  assert.equal(taskEntry({ kind: 'memory', task: { state: 'queued' }, enabled: true, t }).tone, 'progress')
  assert.equal(taskEntry({ kind: 'memory', task: { state: 'queued' }, enabled: true, t }).text, '记忆沉淀排队中')
  assert.equal(taskEntry({ kind: 'compress', task: { state: 'running' }, enabled: true, t }).text, '压缩中…')
  assert.equal(taskEntry({ kind: 'compress', task: { state: 'running' }, enabled: true, t }).tone, 'progress')
  assert.equal(taskEntry({ kind: 'compress', task: { state: 'done' }, enabled: true, t }).tone, 'ready')
  const failed = taskEntry({ kind: 'compress', task: { state: 'error', error: 'llm down' }, enabled: true, t })
  assert.equal(failed.tone, 'error')
  assert.equal(failed.text, '压缩 · 失败')
  assert.match(failed.title, /llm down/)
  // 失败态在「未启用」时也不被压掉（任务真实在跑 / 失败）
  assert.equal(taskTone('memory', { state: 'error' }, false), 'error')
})

test('notice → 任务状态：进度取值 + 失败（plugin-failure）+ 失败优先于 done', () => {
  assert.deepEqual(parseNotice({ notice: 'memory-start' }), { kind: 'memory', state: 'running', error: '', errorKind: '' })
  assert.deepEqual(parseNotice({ notice: 'compress-queued' }), { kind: 'compress', state: 'queued', error: '', errorKind: '' })
  assert.equal(parseNotice({ notice: 'completion' }), null)
  assert.equal(parseNotice({ notice: 'plugin-failure', plugin: 'vfts' }), null, '其它插件失败不归长任务')
  const fail = parseNotice({ notice: 'plugin-failure', plugin: 'memory', kind: 'save', reason: 'disk full', message: '⚠️ x' })
  assert.equal(fail.kind, 'memory')
  assert.equal(fail.state, 'error')
  assert.equal(fail.error, 'disk full')
  assert.equal(fail.errorKind, 'save')

  const started = reduceTask(null, { kind: 'memory', state: 'running' }, { session_id: 's1', turn_id: 't1' }, 1000)
  assert.equal(started.state, 'running')
  assert.equal(started.startedAt, 1000)
  const errored = reduceTask(started, fail, { message_id: 'm1' }, 2000)
  assert.equal(errored.state, 'error')
  assert.equal(errored.startedAt, 1000, '同一任务保留开始时间')
  // worker 失败后仍补发 *-done → 不覆盖错误态
  assert.equal(reduceTask(errored, { kind: 'memory', state: 'done' }, {}, 3000), null)
  // 下一轮 queued → 新任务（清错误、重置开始时间）
  const nextRound = reduceTask(errored, { kind: 'memory', state: 'queued' }, {}, 4000)
  assert.equal(nextRound.state, 'queued')
  assert.equal(nextRound.error, '')
  assert.equal(nextRound.startedAt, 4000)
})

test('索引详情：状态 / 进度 / 文件·符号·chunk / 最近索引时间 / 错误文本', () => {
  const cg = indexDetail({
    enabledRaw: 'true',
    statusRaw: JSON.stringify({
      state: 'indexing', phase: 'index', progressDone: 3, progressTotal: 8,
      indexedFiles: 12, indexedSymbols: 34, lastIndexedAt: 1700000000000000000,
    }),
    t,
  })
  assert.equal(cg.stateText, '索引中')
  assert.equal(cg.tone, 'progress')
  assert.equal(cg.done, 3)
  assert.equal(cg.total, 8)
  assert.equal(cg.hasProgress, true)
  assert.equal(cg.files, 12)
  assert.equal(cg.symbols, 34)
  assert.equal(cg.phase, 'index')
  assert.ok(statusTime(cg.lastIndexedAt).length > 0, 'UnixNano 应可格式化为本地时间')
  const vf = indexDetail({
    enabledRaw: 'true',
    statusRaw: JSON.stringify({ state: 'error', message: 'boom', chunkCount: 9 }),
    t,
  })
  assert.equal(vf.tone, 'error')
  assert.equal(vf.error, 'boom')
  assert.equal(vf.chunks, 9)
  // 未启用 / 解析失败：不编造进度 / 时间
  const off = indexDetail({ enabledRaw: 'false', statusRaw: 'garbage', t })
  assert.equal(off.stateText, '未启用')
  assert.equal(off.hasProgress, false)
  assert.equal(off.lastIndexedAt, 0)
  assert.equal(statusTime(0), '')
})

/* ═══════════ ② 点击条目 → open-view ═══════════ */

test('点击条目 → open-view（目标 = 对应详情 view）→ 既有 preview-tab-open', async () => {
  // 详情 view 已登记进命令解析的「功能页」白名单
  for (const key of Object.values(STATUS_DETAIL_VIEWS)) {
    assert.ok(PREVIEW_TAB_TARGETS.includes(key), `${key} 应登记为 preview 功能页目标`)
  }
  for (const [kind, viewKey] of Object.entries(STATUS_DETAIL_VIEWS)) {
    const seen = []
    const r = openStatusDetail(kind, { bus: { emit: (topic, payload) => seen.push([topic, payload]) } })
    assert.equal(r.ok, true, `${kind} 应可解析`)
    assert.equal(r.topic, EventNames.previewTabOpen)
    assert.deepEqual(r.payload, { kind: viewKey })
    assert.deepEqual(seen, [[EventNames.previewTabOpen, { kind: viewKey }]])
  }
  assert.equal(openStatusDetail('nope').ok, false, '未知任务类 → 拒绝')
})

/* ═══════════ ③ 详情页注册 + 「去设置」 ═══════════ */

test('详情页注册：view 注册表 + 组件映射 + CodeView 功能页', () => {
  const keys = VIEW_DEFINITIONS.map((d) => d.key)
  for (const key of Object.values(STATUS_DETAIL_VIEWS)) {
    assert.ok(keys.includes(key), `view 注册表缺 ${key}`)
    assert.equal(VIEW_DEFINITIONS.find((d) => d.key === key).designable, false, `${key} 为数据驱动（设计态不挂载）`)
  }
  const reg = read('layout/views.registry.js')
  assert.match(reg, /\{ key: 'status-memory', component: MemoryStatusDetail \}/, '组件注册缺 status-memory')
  assert.match(reg, /\{ key: 'status-compress', component: CompressStatusDetail \}/, '组件注册缺 status-compress')
  assert.match(reg, /\{ key: 'status-index', component: IndexStatusDetail \}/, '组件注册缺 status-index')
  const cv = read('views/codeview/CodeView.vue')
  assert.match(cv, /'status-memory': defineAsyncComponent/, 'CodeView 功能页缺 status-memory')
  assert.match(cv, /'status-compress': defineAsyncComponent/, 'CodeView 功能页缺 status-compress')
  assert.match(cv, /'status-index': defineAsyncComponent/, 'CodeView 功能页缺 status-index')
  assert.match(cv, /case 'status-memory': return t\('statusBar\.detail\.memoryTitle'\)/, '页签标题应走 i18n')
})

test('详情页含「去设置」入口（既有 project-config-open，配置仍可达）', () => {
  const shell = read('views/status/TaskDetailLayout.vue')
  assert.match(shell, /data-action="go-settings"/, '详情页壳应有「去设置」按钮')
  assert.match(shell, /EventNames\.projectConfigOpen/, '「去设置」走既有 project-config-open')
  assert.match(shell, /tab: settingsTab/, '「去设置」应带目标页签')
  assert.match(shell, /statusBar\.detail\.goSettings/)
  // 记忆 / 压缩 → context 页签；索引 → codegraph / vfts 页签
  assert.match(read('views/status/MemoryStatusDetail.vue'), /settings-tab="'context'"/)
  assert.match(read('views/status/CompressStatusDetail.vue'), /settings-tab="'context'"/)
  const idx = read('views/status/IndexStatusDetail.vue')
  assert.match(idx, /tab: 'codegraph'/, '索引详情应可去 CodeGraph 设置')
  assert.match(idx, /tab: 'vfts'/, '索引详情应可去 Vfts 设置')
  assert.match(idx, /statusBar\.detail\.astNote/, 'AST 口径应如实说明（并入索引阶段，不编造独立阶段）')
})

/* ═══════════ ④ 去 toast ═══════════ */

test('去 toast：记忆 / 压缩长任务不再走轻提示（失败落条目错误态 + 详情页）', () => {
  const chat = read('views/chat/ChatPanel.vue')
  // 长任务分流在 sessionGuard 之前（状态栏是项目级）；命中即 return（不进入 sendNotify / message.info）
  assert.match(chat, /const isLongTask = data\.plugin === 'memory' \|\| data\.plugin === 'compress'/, '应识别 memory/compress 长任务')
  assert.match(chat, /handleLongTaskNotice\(data\)/, '长任务 notice 应交由 useQueueStatus 归并')
  const block = chat.match(/if \(isLongTask\) \{[\s\S]*?\n    \}/)
  assert.ok(block, '未找到长任务分流块')
  assert.match(block[0], /return/, '长任务命中后必须提前 return（不弹 toast）')
  assert.doesNotMatch(block[0], /sendNotify|message\./, '长任务分流块内不得出现轻提示')
  // 压缩进度仍是内联指示（非 toast）
  assert.match(block[0], /handleCompressNotice\(data\)/, '压缩进度应继续驱动内联指示')
  // 失败态在条目 / 详情页可见（状态栏条目 tone=error + 详情页错误块）
  const entry = read('layout/bar-views/MemoryStatusView.vue')
  assert.match(entry, /taskEntry/, '记忆条目应走状态映射（失败 → error 色）')
  assert.match(read('views/status/TaskDetailLayout.vue'), /td-error-text/, '详情页应有错误文本块')
  // 其余插件失败仍保留可见提示（不误删）
  assert.match(chat, /plugin-failure'\s*\n?\s*\? pluginFailureText/, '其它插件失败提示链路保留')
})

/* ═══════════ ⑤ i18n ═══════════ */

test('i18n：状态条目 + 详情页键 zh/en 齐备', () => {
  const keys = [
    'task_memory_idle', 'task_compress_idle', 'task_memory_disabled', 'task_memory_error', 'task_compress_error',
    'task_open_memory', 'task_open_compress',
    'detail.memoryTitle', 'detail.compressTitle', 'detail.indexTitle', 'detail.goSettings', 'detail.error',
    'detail.lastTask', 'detail.state', 'detail.updatedAt', 'detail.startedAt', 'detail.session', 'detail.turn',
    'detail.noRecord', 'detail.timeline', 'detail.noTimeline', 'detail.codegraph', 'detail.vfts',
    'detail.enabled', 'detail.yes', 'detail.no', 'detail.progress', 'detail.files', 'detail.symbols',
    'detail.chunks', 'detail.lastIndexed', 'detail.loaded', 'detail.astNote', 'detail.phaseConfigure',
    'detail.phaseIndex', 'detail.engineSummary',
  ]
  for (const loc of LOCALES) {
    const sb = readLocale(loc, 'statusBar.json')
    for (const k of keys) {
      const v = k.includes('.') ? (sb[k.split('.')[0]] || {})[k.split('.')[1]] : sb[k]
      assert.equal(typeof v, 'string', `${loc} 缺键 ${k}`)
      assert.ok(v.length > 0, `${loc} 键 ${k} 为空`)
    }
    const lf = readLocale(loc, 'layoutFree.json').view
    for (const k of ['memoryStatus', 'compressStatus', 'statusMemory', 'statusCompress', 'statusIndex']) {
      assert.equal(typeof lf[k], 'string', `${loc} layoutFree.view.${k} 缺失`)
    }
  }
})
