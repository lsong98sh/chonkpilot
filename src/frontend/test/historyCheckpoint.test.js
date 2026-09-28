/**
 * 文件历史（检查点）前端口径 —— 设置项（保留个数/天数）+ 只读时间轴视图守护（2026-09-28）。
 *
 * 后端契约（已冻结，前端只照抄）：
 *   - prj 键 history.enabled（"true"/"false"，默认关）/ history.checkpoint_keep（默认 500）
 *     / history.checkpoint_ttl_days（默认 7）；
 *   - **会话级内部键** history.status.<slug> / history.timeline.<slug>（slug = 根会话；只读 JSON；
 *     I-135 —— 按会话、多会话并发互不覆盖）+ history.clear（写入口，值 = JSON {ts, session}；I-136）。
 *   - 刷新订阅既有广播 data-prj-config-refresh（= onDataRefresh('prj-config', …)），零新增 MQ 主题。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯逻辑直调（utils/historyTimeline.js）+ `*.vue` / locale 源码守卫（与 configSurface 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  DEFAULT_CHECKPOINT_KEEP, DEFAULT_CHECKPOINT_TTL_DAYS,
  parseStatus, parseTimeline, relativeNumber, statusMode, statusEnabled,
  formatBytes, formatTime, retentionValue,
  chainSlug, historyStatusKey, historyTimelineKey,
  HISTORY_STATUS_PREFIX, HISTORY_TIMELINE_PREFIX,
} from '../src/utils/historyTimeline.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

const HISTORY_VUE = 'views/settings/HistoryConfig.vue'

// ═══════════════════════════════════════════════════════════════
// ① 纯逻辑：JSON 解析兜底（失败/缺失 → 未启用/无数据，不抛错）
// ═══════════════════════════════════════════════════════════════
test('① parseStatus：非法/缺失/非对象 → null（不抛错）', () => {
  assert.deepEqual(parseStatus('{"enabled":true,"mode":"active"}'), { enabled: true, mode: 'active' })
  assert.equal(parseStatus(''), null)
  assert.equal(parseStatus('not-json'), null)
  assert.equal(parseStatus('[1,2]'), null, '数组不是 status 对象')
  assert.equal(parseStatus('123'), null)
  assert.equal(parseStatus(null), null)
  assert.equal(parseStatus(undefined), null)
  assert.equal(parseStatus({ enabled: true }), null, '非字符串一律 null')
  assert.doesNotThrow(() => parseStatus('{bad json'))
})

test('① parseTimeline：非法/缺失 → []，并过滤非对象项', () => {
  assert.deepEqual(parseTimeline('[{"n":-1},{"n":-2}]'), [{ n: -1 }, { n: -2 }])
  assert.deepEqual(parseTimeline('[]'), [])
  assert.deepEqual(parseTimeline('not-json'), [])
  assert.deepEqual(parseTimeline('{"n":-1}'), [], '对象不是数组')
  assert.deepEqual(parseTimeline(null), [])
  assert.deepEqual(parseTimeline(undefined), [])
  assert.deepEqual(parseTimeline('[1,"x",null,{"n":-1}]'), [{ n: -1 }], '过滤非对象项')
  assert.doesNotThrow(() => parseTimeline('[bad'))
})

// ═══════════════════════════════════════════════════════════════
// ② 纯逻辑：相对编号（-1 恒为最新）、模式、体积、时间、保留值
// ═══════════════════════════════════════════════════════════════
test('② relativeNumber：最新一步 = -1（按数组顺序归一）', () => {
  assert.equal(relativeNumber(0), -1, '索引 0（最新在前）→ -1')
  assert.equal(relativeNumber(1), -2)
  assert.equal(relativeNumber(9), -10)
})

test('② statusMode / statusEnabled：active|fused|off 归一', () => {
  assert.equal(statusMode({ mode: 'active' }), 'active')
  assert.equal(statusMode({ mode: 'fused' }), 'fused')
  assert.equal(statusMode({ mode: 'off' }), 'off')
  assert.equal(statusMode({}), 'off')
  assert.equal(statusMode(null), 'off')
  assert.equal(statusMode({ mode: 'weird' }), 'off')

  assert.equal(statusEnabled({ enabled: true, mode: 'active' }), true)
  assert.equal(statusEnabled({ enabled: true, mode: 'fused' }), true, '熔断放行仍算已启用')
  assert.equal(statusEnabled({ enabled: true, mode: 'off' }), false)
  assert.equal(statusEnabled({ enabled: false, mode: 'active' }), false)
  assert.equal(statusEnabled(null), false)
})

test('② formatBytes：B/KB/MB/GB 与非法值兜底', () => {
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(512), '512 B')
  assert.equal(formatBytes(2048), '2 KB')
  assert.equal(formatBytes(1024 * 1024 * 3), '3 MB')
  assert.equal(formatBytes(-1), '0 B')
  assert.equal(formatBytes('abc'), '0 B')
  assert.equal(formatBytes(undefined), '0 B')
})

test('② formatTime：ISO → 本地时间串；非法原样/缺失空串', () => {
  assert.match(formatTime('2026-09-28T10:00:00Z'), /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
  assert.equal(formatTime('not-a-date'), 'not-a-date')
  assert.equal(formatTime(''), '')
  assert.equal(formatTime(null), '')
  assert.equal(formatTime(undefined), '')
})

test('② retentionValue / 默认值：缺失或空 → 默认 500 / 7', () => {
  assert.equal(DEFAULT_CHECKPOINT_KEEP, 500)
  assert.equal(DEFAULT_CHECKPOINT_TTL_DAYS, 7)
  assert.equal(retentionValue('', DEFAULT_CHECKPOINT_KEEP), '500')
  assert.equal(retentionValue('   ', DEFAULT_CHECKPOINT_KEEP), '500')
  assert.equal(retentionValue(undefined, DEFAULT_CHECKPOINT_TTL_DAYS), '7')
  assert.equal(retentionValue('100', DEFAULT_CHECKPOINT_KEEP), '100')
})

test('② chainSlug / 会话级键：与后端 chainSlug 同口径（I-135）', () => {
  // 会话 id 为 ASCII（uuid）→ 恒等
  assert.equal(chainSlug('3f2a1b4c-5d6e-7f80-9a1b-2c3d4e5f6071'), '3f2a1b4c-5d6e-7f80-9a1b-2c3d4e5f6071')
  assert.equal(chainSlug('hist-12345'), 'hist-12345')
  // 非安全字符折为 '_'；空白/空/'.'/'..' → default
  assert.equal(chainSlug('a b/c'), 'a_b_c')
  assert.equal(chainSlug('  padded  '), 'padded')
  assert.equal(chainSlug(''), 'default')
  assert.equal(chainSlug('   '), 'default')
  assert.equal(chainSlug(null), 'default')
  assert.equal(chainSlug('.'), 'default')
  assert.equal(chainSlug('..'), 'default')
  // 键名 = 前缀 + slug（与后端 statusKeyPrefix / timelineKeyPrefix 对齐）
  assert.equal(HISTORY_STATUS_PREFIX, 'history.status.')
  assert.equal(HISTORY_TIMELINE_PREFIX, 'history.timeline.')
  assert.equal(historyStatusKey('s1'), 'history.status.s1')
  assert.equal(historyTimelineKey('s1'), 'history.timeline.s1')
})

// ═══════════════════════════════════════════════════════════════
// ③ 设置页：2 个设置项（键名/默认值/保存路径）
// ═══════════════════════════════════════════════════════════════
test('③ HistoryConfig：保留个数/天数读 prj 键 + 默认 500/7', () => {
  const src = read(HISTORY_VUE)
  assert.match(src, /DEFAULT_CHECKPOINT_KEEP/, '须引用保留个数默认值 500')
  assert.match(src, /DEFAULT_CHECKPOINT_TTL_DAYS/, '须引用保留天数默认值 7')
  assert.match(src, /retentionValue\(c\['history\.checkpoint_keep'\], DEFAULT_CHECKPOINT_KEEP\)/,
    '保留个数读 history.checkpoint_keep（缺失回落默认）')
  assert.match(src, /retentionValue\(c\['history\.checkpoint_ttl_days'\], DEFAULT_CHECKPOINT_TTL_DAYS\)/,
    '保留天数读 history.checkpoint_ttl_days（缺失回落默认）')
})

test('③ HistoryConfig：保存一次批量写 history.checkpoint_keep / history.checkpoint_ttl_days（非法不写库）', () => {
  const src = read(HISTORY_VUE)
  const fn = src.match(/async function handleRetentionSave\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 handleRetentionSave')
  assert.match(fn[0], /setConfigs\(\{[\s\S]*?'history\.checkpoint_keep': String\(kr\.value\)/, '须一次批量写 history.checkpoint_keep')
  assert.match(fn[0], /'history\.checkpoint_ttl_days': String\(tr\.value\)/, '须一次批量写 history.checkpoint_ttl_days')
  assert.match(fn[0], /validatePositiveInt\(/, '须前置正整数校验')
  // 批量写 = 1 次保存请求（→ 后端 1 条 refresh）；不得再逐键 setConfig
  assert.doesNotMatch(fn[0], /await setConfig\(/, '保留策略须一次批量写（不得逐键 setConfig）')
  const writes = (fn[0].match(/setConfigs\(/g) || []).length
  assert.equal(writes, 1, '保留策略须恰好 1 次批量写请求')
  // 非法值在校验处 return（不写库）
  const invalidIdx = fn[0].indexOf('if (!kr.ok || !tr.ok) return')
  const writeIdx = fn[0].indexOf('setConfigs({')
  assert.ok(invalidIdx >= 0 && invalidIdx < writeIdx, '非法值须在校验处短路（不写库）')
})

// ═══════════════════════════════════════════════════════════════
// ④ 只读时间轴：数据来源 / 订阅 / 状态条字段 / 空态
// ═══════════════════════════════════════════════════════════════
test('④ HistoryConfig：按**当前会话**读 status/timeline；订阅 prj-config；无 watch', () => {
  const src = read(HISTORY_VUE)
  assert.match(src, /sessionSlug\.value = await activeSessionSlug\(\)/,
    '须先取当前会话 slug（活动会话；I-135 按会话）')
  assert.match(src, /getActiveSessionID\(\)/, '当前会话取 data-session-active-get（既有会话域只读面）')
  assert.match(src, /parseStatus\(c\[historyStatusKey\(sessionSlug\.value\)\]\)/,
    '状态读会话级键 history.status.<slug>（安全解析）')
  assert.match(src, /parseTimeline\(c\[historyTimelineKey\(sessionSlug\.value\)\]\)/,
    '列表读会话级键 history.timeline.<slug>（安全解析）')
  // 2026-09-28：统一机制 usePrjConfigRefresh（I-138 收敛）——键过滤 + 突发合并 + 保存期间跳过；
  // 会话级键走**前缀**匹配（slug 是运行期变量，不能列成精确键）
  assert.match(src, /usePrjConfigRefresh\(\{[\s\S]*?prefixes: \[HISTORY_STATUS_PREFIX, HISTORY_TIMELINE_PREFIX\][\s\S]*?reload: loadConfig,[\s\S]*?isSaving: \(\) => saving\.value/,
    '须订阅既有 data-prj-config-refresh（统一机制，会话级键按前缀过滤，保存期间跳过）')
  assert.doesNotMatch(src, /onDataRefresh\('prj-config'/, '不得再直接订阅 prj-config 广播（须走统一机制）')
  assert.match(src, /onUnmounted\(\(\) => \{[\s\S]*?unsubs\.forEach/, '须在卸载时退订')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '禁止 watch/watchEffect（用 computed 派生）')
  assert.doesNotMatch(src, /console\.error/, '解析失败不得刷 console.error')
})

test('④ 状态条：模式（含 fused）/ 检查点数 / 体积 / 最近打点 / 耗时 / 失败次数 / dirty / lastError', () => {
  const src = read(HISTORY_VUE)
  assert.match(src, /data-history-status/, '须有状态条容器')
  for (const k of ['status_mode', 'status_count', 'status_bytes', 'status_last',
    'status_last_duration', 'status_fail']) {
    assert.ok(src.includes(`historyConfig.${k}`), `状态条缺字段 ${k}`)
  }
  // 模式 = statusMode 派生（active/fused/off）
  assert.match(src, /statusMode\(status\.value\)/, '模式须由 statusMode 派生')
  assert.match(src, /formatBytes\(status\.value\?\.bytes\)/, '体积须人类可读（formatBytes）')
  assert.match(src, /formatTime\(status\.value\?\.lastCheckpointAt\)/, '最近打点须格式化时间')
  assert.match(src, /status\.value\?\.lastDurationMs/, '耗时读 lastDurationMs')
  assert.match(src, /status\.value\?\.failCount/, '失败次数读 failCount')
  // dirty 提示
  assert.match(src, /const statusDirty = computed\(\(\) => status\.value\?\.dirty === true\)/, 'dirty 派生')
  assert.match(src, /v-if="statusDirty"/, 'dirty 时须有提示')
  assert.ok(src.includes('historyConfig.status_dirty'), 'dirty 提示文案键')
  // lastError 红字一行（有则显示）
  assert.match(src, /const lastError = computed\(/, '须派生 lastError')
  assert.match(src, /v-if="lastError"/, 'lastError 非空须渲染')
  assert.ok(src.includes('historyConfig.status_last_error'), 'lastError 文案键')

  // locale：fused 有专门文案（zh 明确「已熔断放行」）
  assert.match(readLocale('zh-CN', 'historyConfig.json').mode_fused, /熔断/)
  assert.match(readLocale('en-US', 'historyConfig.json').mode_fused, /fused/i)
})

test('④ 列表：-1 为最新一步（编号派生、顺序不反转）；delta 展示 +n −m', () => {
  const src = read(HISTORY_VUE)
  assert.match(src, /relativeNumber\(index\)/, '相对编号须由数组索引派生（-1 恒为最新）')
  assert.doesNotMatch(src, /\.reverse\(\)/, '列表最新在前，不得反转')
  assert.match(src, /row\.added/, '须展示新增数')
  assert.match(src, /row\.removed/, '须展示删除数')
  assert.match(src, /formatTime\(row\.ts\)/, '行时间须格式化')
  for (const c of ['col_n', 'col_time', 'col_tool', 'col_session', 'col_files', 'col_delta']) {
    assert.ok(src.includes(`historyConfig.${c}`), `列表缺列 ${c}`)
  }
})

test('④ 空态可区分（未启用 vs 无检查点）', () => {
  const src = read(HISTORY_VUE)
  assert.match(src, /const timelineEmptyText = computed\(/, '空态文案须派生')
  assert.match(src, /statusEnabled\(status\.value\)/, '空态按是否启用分流')
  assert.ok(src.includes('historyConfig.empty_disabled'), '未启用空态键')
  assert.ok(src.includes('historyConfig.empty_none'), '无检查点空态键')
  const zh = readLocale('zh-CN', 'historyConfig.json')
  assert.notEqual(zh.empty_disabled, zh.empty_none, '两种空态文案须可区分')
})

// ═══════════════════════════════════════════════════════════════
// ⑤ 清空历史：写 history.clear + 二次确认；零新增 MQ 主题
// ═══════════════════════════════════════════════════════════════
test('⑤ 清空历史：确认后写 history.clear（JSON {ts,session}）并可见反馈', () => {
  const src = read(HISTORY_VUE)
  const fn = src.match(/async function handleClear\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 handleClear')
  assert.match(fn[0], /confirm\(t\('historyConfig\.clear_confirm'\)/, '须二次确认')
  assert.match(fn[0], /catch \(_\) \{\s*\n\s*return/, '取消须直接返回（不清空）')
  // I-136：只清**本会话** → 值 = JSON {ts, session}（session = 当前会话 slug）
  assert.match(fn[0], /setConfig\('history\.clear', JSON\.stringify\(\{ ts: new Date\(\)\.toISOString\(\), session: sessionSlug\.value \}\)\)/,
    '写入口 = history.clear + JSON {ts, session}（只清本会话链）')
  assert.match(fn[0], /message\.success\(/, '清空后须可见反馈')
  assert.match(src, /data-history-clear/, '须有清空按钮')
  // UI 文案须为「清空本会话历史」（不再是仓库粒度）
  const zh = readLocale('zh-CN', 'historyConfig.json')
  const en = readLocale('en-US', 'historyConfig.json')
  assert.match(zh.clear, /本会话/, 'zh 按钮文案须写明「本会话」')
  assert.match(zh.clear_confirm, /当前会话/, 'zh 确认文案须写明「当前会话」')
  assert.match(en.clear, /this session/i, 'en 按钮文案须写明 this session')
  assert.match(en.clear_confirm, /current session/i, 'en 确认文案须写明 current session')
})

test('⑤ 零新增 MQ 主题：event-names.js 不得新增 history 通道', () => {
  const ev = read('events/event-names.js')
  assert.doesNotMatch(ev, /history/i, '不得在 event-names.js 新增 history 相关事件')
})

// ═══════════════════════════════════════════════════════════════
// ⑥ i18n zh/en 齐备 + 页签标签（语义已从「自动提交」改为「文件历史」）
// ═══════════════════════════════════════════════════════════════
test('⑥ i18n：historyConfig zh-CN / en-US 键集一致且非空', () => {
  const zh = readLocale('zh-CN', 'historyConfig.json')
  const en = readLocale('en-US', 'historyConfig.json')
  assert.deepEqual(Object.keys(zh).sort(), Object.keys(en).sort(), 'zh/en 键集须一致')
  for (const k of Object.keys(zh)) {
    assert.ok(typeof zh[k] === 'string' && zh[k].trim().length > 0, `zh 缺 ${k}`)
    assert.ok(typeof en[k] === 'string' && en[k].trim().length > 0, `en 缺 ${k}`)
  }
  // 新增键齐备（设置项 + 时间轴）
  for (const k of ['retention_label', 'keep_label', 'ttl_label', 'retention_hint',
    'timeline_title', 'status_mode', 'mode_active', 'mode_fused', 'mode_off',
    'status_count', 'status_bytes', 'status_last', 'status_last_duration', 'status_fail',
    'status_dirty', 'status_last_error', 'col_n', 'col_time', 'col_tool', 'col_session',
    'col_files', 'col_delta', 'empty_disabled', 'empty_none',
    'clear', 'clear_confirm_title', 'clear_confirm', 'cleared']) {
    assert.ok(k in zh, `zh 缺新键 ${k}`)
  }
  // 保留口径文案（zh）：任一超限即修剪 + 锚点 = 最新检查点时间 + 不再向分支提交
  assert.match(zh.desc_retention, /任一/, '须说明「任一超限即修剪」')
  assert.match(zh.desc_retention, /最新检查点时间/, '须说明天数窗口锚点')
  assert.match(zh.desc_no_commit, /不.*提交/, '须说明不再向 git 分支提交')
  assert.match(zh.desc_perf, /慎重|效率/, '须提示大工程影响效率')
  assert.match(zh.desc_checkpoint, /工具调用前|checkpoint/, '须说明工具调用前打点')
  assert.match(en.desc_retention, /either/i, 'en 须说明 either 超限即修剪')
  assert.match(en.desc_retention, /latest checkpoint/i, 'en 须说明锚点')
  assert.match(en.desc_no_commit, /No commits/i, 'en 须说明不向分支提交')
})

test('⑥ 页签标签：projectConfig.history 语义为「文件历史」（旧「自动提交」已废弃）', () => {
  const zh = readLocale('zh-CN', 'projectConfig.json')
  const en = readLocale('en-US', 'projectConfig.json')
  assert.match(zh.history, /文件历史/, 'zh 页签须为「文件历史」')
  assert.match(en.history, /File History/i, 'en 页签须为「File History」')
})
