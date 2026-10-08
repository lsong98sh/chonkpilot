/**
 * 状态栏「索引 / codebase 状态区」前端守卫（2026-09-27）。
 *
 * 覆盖：
 *  ① 纯逻辑：`src/utils/indexStatus.js` 的状态 → 文案/tone 映射（全状态覆盖 + 解析失败兜底）；
 *  ② 源码守卫：StatusBar.vue 读的是 4 个既有 prj 键、订阅既有 `data-prj-config-refresh`、
 *     徽标 = [图标] + 状态文本（不再用 Tooltip / tip），悬停原生 `title`，
 *     点击走既有 `EventNames.projectConfigOpen` 且带 `tab`（**零新增 MQ 主题**）、无 `watch`/`watchEffect`；
 *  ③ 跳页签链：StatusBar → MainLayout → CodeView → ProjectConfig（含 tab 非法兜底）；
 *  ④ i18n：新增键 zh/en 齐备。
 * 真机行为（徽标文案 / `title` / 点击跳页签）由 L4 `test_statusbar.py::SB6` 覆盖。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { indexBadge, parseStatus } from '../src/utils/indexStatus.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

// t 解析器：直接取 zh-CN statusBar 文案（顺带校验 i18n 集成）
const zh = readLocale('zh-CN', 'statusBar.json')
const t = (k) => (k.startsWith('statusBar.') ? (zh[k.slice('statusBar.'.length)] ?? k) : k)

const badge = (over = {}) => indexBadge({
  enabledRaw: 'true',
  statusRaw: '',
  t,
  ...over,
})

test('indexStatus：解析失败/缺键 → null（不抛错）', () => {
  assert.equal(parseStatus(undefined), null)
  assert.equal(parseStatus(''), null)
  assert.equal(parseStatus('not-json'), null)
  assert.equal(parseStatus('[1,2]'), null)
  assert.deepEqual(parseStatus('{"state":"ready"}'), { state: 'ready' })
})

test('indexStatus：开关非 "true"/无键 → 未启用（disabled）', () => {
  for (const raw of [undefined, '', 'false', 'TRUE', 'yes']) {
    const m = badge({ enabledRaw: raw, statusRaw: '{"state":"ready","progressDone":1,"progressTotal":2}' })
    assert.equal(m.text, '未启用', 'enabledRaw=' + String(raw))
    assert.equal(m.tone, 'disabled')
  }
})

test('indexStatus：state → 文案/tone 映射齐全', () => {
  const cases = [
    ['{"state":"indexing","phase":"index","progressDone":3,"progressTotal":8}', '索引中 3/8', 'progress'],
    ['{"state":"indexing","progressTotal":0}', '索引中', 'progress'],
    ['{"state":"ready","progressDone":8,"progressTotal":8}', '完成', 'ready'],
    ['{"state":"error","message":"boom"}', '失败', 'error'],
    ['{"state":"not_initialized"}', '未初始化', 'disabled'],
    ['{"state":""}', '未初始化', 'disabled'],
  ]
  for (const [raw, text, tone] of cases) {
    const m = badge({ statusRaw: raw })
    assert.equal(m.text, text, raw)
    assert.equal(m.tone, tone, raw)
  }
  // 状态未落定但已见进度/阶段 → 判定为索引中（引擎在建索引的中途快照）
  assert.equal(badge({ statusRaw: '{"state":"not_initialized","progressTotal":5,"progressDone":2}' }).text, '索引中 2/5')
  assert.equal(badge({ statusRaw: '{"state":"","phase":"configure"}' }).text, '索引中')
  // 解析失败但开关为 true → 未初始化（不谎报完成/失败）
  assert.equal(badge({ statusRaw: 'garbage' }).text, '未初始化')
  assert.equal(badge({ statusRaw: undefined }).text, '未初始化')
})

test('indexStatus：模型只返回 {tone, text}（不再产出 tip 明细）', () => {
  const m = badge({ statusRaw: '{"state":"indexing","progressDone":3,"progressTotal":8}' })
  assert.deepEqual(Object.keys(m).sort(), ['text', 'tone'])
})

test('StatusBar：索引状态区读既有 4 键 + 既有广播刷新 + computed 派生', () => {
  const sb = read('views/statusbar/StatusBar.vue')
  for (const key of ['enable-codegraph', 'codegraph.status', 'enable-vfts', 'vfts.status']) {
    assert.ok(sb.includes(`'${key}'`), '状态栏应读 prj 键 ' + key)
  }
  assert.match(sb, /getAllConfig\(\)/, '应经既有 getAllConfig 取数')
  assert.match(sb, /onDataRefresh\('prj-config'/, '应订阅既有 data-prj-config-refresh')
  assert.match(sb, /computed\(/, '显示值应用 computed 派生')
  assert.match(sb, /sb-index/, '应有索引状态区容器')
  assert.match(sb, /data-engine="codegraph"/, '应有 codegraph 徽标')
  assert.match(sb, /data-engine="vfts"/, '应有 vfts 徽标')
  assert.doesNotMatch(sb, /\bwatch\s*\(|\bwatchEffect\s*\(/, '不得使用 watch/watchEffect')
})

test('StatusBar：徽标 = [图标] + 状态文本，悬停原生 title（不再用 Tooltip/tip）', () => {
  const sb = read('views/statusbar/StatusBar.vue')
  // 图标在左、状态文本紧贴其右：两枚徽标各含一个 Icon + 一个 .sb-idx-state
  const icons = sb.match(/<Icon\b/g) || []
  const states = sb.match(/class="sb-idx-state"/g) || []
  assert.equal(icons.length, 2, '两枚徽标应各有一个 <Icon>')
  assert.equal(states.length, 2, '两枚徽标应各有一个状态文本')
  assert.match(sb, /<Icon\s+name="[a-z0-9-]+"/, '图标须取自既有图标集（name 短名）')
  // 不再用 Tooltip 组件 / tip 明细 / 引擎名文本（icon 取代）
  assert.doesNotMatch(sb, /\bTooltip\b/, '不应用 Tooltip 组件')
  assert.doesNotMatch(sb, /\.tip\b|idx_tip_/, '不应保留 tip 明细字段')
  assert.doesNotMatch(sb, /sb-idx-name/, '不应再渲染引擎名文本（由图标取代）')
  // 悬停原生 title（i18n 键齐备），文案指向"点击设置"
  assert.match(sb, /:title="\$t\('statusBar\.idx_open_codegraph'\)"/, 'codegraph 徽标应有原生 title')
  assert.match(sb, /:title="\$t\('statusBar\.idx_open_vfts'\)"/, 'vfts 徽标应有原生 title')
})

test('StatusBar：点击走既有 projectConfigOpen（带 tab），且不新增 MQ 主题', () => {
  const sb = read('views/statusbar/StatusBar.vue')
  assert.match(sb, /EventNames\.projectConfigOpen/, '点击应走既有 project-config-open')
  // 两枚徽标各自映射页签：codegraph → tab:'codegraph'；vfts → tab:'vfts'
  assert.match(sb, /projectConfigOpen\]\.click="\{ tab: 'codegraph' \}"/, 'codegraph 徽标点击应带 tab=codegraph')
  assert.match(sb, /projectConfigOpen\]\.click="\{ tab: 'vfts' \}"/, 'vfts 徽标点击应带 tab=vfts')
  // 允许的既有主题：点击类（projectConfigOpen / guiDevToolsOpen）+ 队列状态消费（toolNotify，I-128）——
  // 三者均为**既有**主题，本节仍守「状态栏不新增 MQ 主题」。
  const used = [...sb.matchAll(/EventNames\.(\w+)/g)].map(m => m[1])
  assert.deepEqual([...new Set(used)].sort(), ['guiDevToolsOpen', 'projectConfigOpen', 'toolNotify'],
    '状态栏只允许复用既有主题（projectConfigOpen / guiDevToolsOpen / toolNotify）')
  // 不得出现"裸"主题字面量（新增主题的典型形态）
  assert.doesNotMatch(sb, /mq\.emit\(\s*['"]/, '不得出现裸主题字面量')
  const names = read('events/event-names.js')
  assert.doesNotMatch(names, /idxIndex|indexStatus|statusbarIndex|index-status-badge/, 'event-names 不得新增索引状态主题')
})

test('跳页签链：MainLayout 透传 tab → CodeView 落到功能页 → ProjectConfig 激活页签', () => {
  const ml = read('views/layout/MainLayout.vue')
  assert.match(ml, /mq\.on\(EventNames\.projectConfigOpen, handleOpenProjectConfig\)/, '应订阅 project-config-open')
  assert.match(ml, /event\.tab/, 'handleOpenProjectConfig 应读取可选 tab')
  assert.match(ml, /if \(tab\) payload\.tab = tab/, '仅在带 tab 时透传（缺省不带 → 行为不变）')

  const cv = read('views/codeview/CodeView.vue')
  assert.match(cv, /openSpecialTab\(kind, event\.title, event\.tab\)/, 'preview-tab-open 的 tab 应透传给 openSpecialTab')
  assert.match(cv, /tab\.innerTabNonce = \(tab\.innerTabNonce \|\| 0\) \+ 1/, '已开页签应递增 nonce 触发页内切换')
  assert.match(cv, /tab\.innerTabNonce = 1/, '新建页签应带 nonce=1')
  assert.match(cv, /innerTab: tab\.innerTab \|\| ''/, 'specialProps 应只对项目配置注入 innerTab')
  assert.match(cv, /if \(tab\.kind === 'settings-project'\)/, '仅项目配置页注入页签 props')

  const pc = read('views/preview/ProjectConfig.vue')
  assert.match(pc, /innerTab: \{ type: String, default: '' \}/, 'ProjectConfig 应声明可选 innerTab')
  assert.match(pc, /innerTabNonce: \{ type: Number, default: 0 \}/, 'ProjectConfig 应声明 innerTabNonce')
  assert.match(pc, /TAB_NAMES\.value\.includes\(props\.innerTab\)/, '应校验 tab 合法（非法 → 不激活）')
  assert.match(pc, /props\.innerTabNonce > appliedNonce\.value/, '仅新 nonce 请求生效（无需 watch）')
  assert.match(pc, /return selection\.value/, '缺省/非法 → 回落用户选择（默认页签）')
  assert.doesNotMatch(pc, /\bwatch\s*\(|\bwatchEffect\s*\(/, '不得使用 watch/watchEffect')
})

test('i18n：索引状态区键 zh/en 齐备', () => {
  const keys = [
    'idx_disabled', 'idx_uninitialized', 'idx_indexing', 'idx_ready', 'idx_error',
    'idx_open_codegraph', 'idx_open_vfts',
  ]
  for (const loc of LOCALES) {
    const sb = readLocale(loc, 'statusBar.json')
    for (const k of keys) {
      assert.equal(typeof sb[k], 'string', `${loc} 缺键 ${k}`)
      assert.ok(sb[k].length > 0, `${loc} 键 ${k} 为空`)
    }
    // 已删的死键不得残留
    for (const k of ['index_codegraph', 'index_vfts', 'idx_tip_state', 'idx_tip_open']) {
      assert.equal(sb[k], undefined, `${loc} 不应残留 ${k}`)
    }
    // 既有键不得被删
    assert.ok(sb.memoryTotal && sb.memoryTotalLabel, loc + ' 既有记忆键应保留')
    assert.equal(sb.openDevTools !== undefined, true, loc + ' 既有 DevTools 键应保留')
  }
  assert.equal(zh.idx_open_codegraph, '点击设置：codegraph')
  assert.equal(zh.idx_open_vfts, '点击设置：Vfts 全文索引')
  const en = readLocale('en-US', 'statusBar.json')
  assert.equal(en.idx_open_codegraph, 'Click to configure: codegraph')
  assert.equal(en.idx_open_vfts, 'Click to configure: Vfts full-text index')
})
