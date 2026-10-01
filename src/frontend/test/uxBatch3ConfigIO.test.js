/**
 * 用户视角缺陷「批 3」⑯ 配置 导入/导出 + 恢复出厂 前端单测（2026-09-20）。
 *
 * 覆盖：
 *   A 导出序列化（快照信封 + 时间戳文件名 + 密钥默认包含）
 *   B 排除密钥（llms[].apiKey 剔除 + 计数 + 不改入参）
 *   C 导入结构校验（JSON 可解析 + 键白名单；未知键忽略并计数）+ 往返可回读
 *   D 键白名单与后端权威源（persist_userconfig.go）逐键一致 —— 防前后端漂移
 *   E 页面守卫（三区 + 二次确认 + 自动备份 + 禁 watch + 不打印内容）
 *   F 接线（api/config.js 数据源与 gui.file.save、i18n 注册、双语齐备、死键已清）
 *   G 桥实现（gui.file.save 注册 + mode=backup 落 <dataDir>/backup + 无内容日志）
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯函数直调（utils/configIO）+ 源码守卫 + Go 源码核实（与 uxBatch2* 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  EXPORT_APP, EXPORT_SCHEMA, EXPORT_SCOPE,
  SNAPSHOT_SCALAR_KEYS, SNAPSHOT_FREE_KEYS, SNAPSHOT_COLLECTION_KEYS, SNAPSHOT_KEYS,
  RESTART_KEYS, SECRET_NAME_RE,
  timestamp, exportFileName, backupFileName,
  redactSecrets, buildSnapshot, serializeSnapshot,
  parseImportText, filterImport, restartKeysOf,
} from '../src/utils/configIO.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoRoot = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readRepo = (rel) => readFileSync(join(repoRoot, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

/** 样例 usr 主库视图（形状对齐 data-user-config-list 的 list[0]） */
function sampleConfig() {
  return {
    id: 'user-config',
    theme: 'dark',
    locale: 'zh-CN',
    chromePath: 'C:\\chrome.exe',
    responseTimeout: 120,
    defaultLLM: 'gpt-local',
    llms: [
      { name: 'gpt-local', model: 'gpt-4o', baseUrl: 'http://127.0.0.1:8080/v1', apiKey: 'sk-secret-1' },
      { name: 'echo', apiKey: '' },
    ],
    tool_async: '{"demo":{"mode":"auto"}}',
    recent_dirs: '["D:\\\\proj"]',
  }
}

// ═══════════════════════════════════════════════════════════════
// A 导出序列化
// ═══════════════════════════════════════════════════════════════
test('A · 导出快照：信封外形 + 时间戳文件名 + 默认包含密钥', () => {
  const now = new Date(2026, 8, 20, 10, 11, 12) // 2026-09-20 10:11:12（本地时区）
  const snap = serializeSnapshot(sampleConfig(), { now })

  assert.equal(timestamp(now), '20260920-101112')
  assert.equal(exportFileName(now), 'chonkpilot-config-20260920-101112.json', '导出名须含时间戳')
  assert.equal(snap.fileName, 'chonkpilot-config-20260920-101112.json')

  const env = JSON.parse(snap.json)
  assert.equal(env.app, EXPORT_APP)
  assert.equal(env.schema, EXPORT_SCHEMA)
  assert.equal(env.scope, EXPORT_SCOPE, '导出范围 = usr 全局配置')
  assert.equal(env.exportedAt, now.toISOString())
  assert.equal(env.secretsExcluded, false, '默认包含密钥（便于完整迁移）')
  assert.equal(env.data.id, undefined, '域 id 不应写入快照')
  assert.equal(env.data.theme, 'dark')
  assert.equal(env.data.llms[0].apiKey, 'sk-secret-1', '默认导出含密钥')
  assert.equal(snap.removedSecrets, 0)
  assert.equal(snap.keyCount, Object.keys(env.data).length, '项数统计 = 快照 data 键数')
})

test('A · 备份文件名与导出一致口径（时间戳 + 同格式）', () => {
  const now = new Date(2026, 0, 2, 3, 4, 5)
  assert.equal(backupFileName(now), 'chonkpilot-config-backup-20260102-030405.json')
  assert.notEqual(backupFileName(now), exportFileName(now), '备份名须可与导出名区分')
})

// ═══════════════════════════════════════════════════════════════
// B 排除密钥
// ═══════════════════════════════════════════════════════════════
test('B · redactSecrets：llms.apiKey 剔除 + 计数，且不改入参', () => {
  const cfg = sampleConfig()
  const snapshot = JSON.stringify(cfg)
  const { data, removed } = redactSecrets(cfg)

  assert.equal(JSON.stringify(cfg), snapshot, 'redactSecrets 必须纯函数（不改入参）')
  assert.equal(data.llms[0].apiKey, undefined, 'LLM apiKey 须剔除')
  assert.equal(data.llms[1].apiKey, undefined)
  assert.equal(data.llms[0].model, 'gpt-4o', '非密钥字段保留')
  assert.equal(data.theme, 'dark', '其它配置项不受影响')
  // 2 个 apiKey = 2
  assert.equal(removed, 2, `剔除计数应为 2，实际 ${removed}`)
})

test('B · 排除密钥导出：secretsExcluded=true 且全文不含密钥字面量', () => {
  const snap = serializeSnapshot(sampleConfig(), { excludeSecrets: true })
  const env = JSON.parse(snap.json)
  assert.equal(env.secretsExcluded, true)
  assert.equal(snap.removedSecrets, 2)
  assert.ok(!snap.json.includes('sk-secret-1'), '排除密钥后不应出现 sk-secret-1')
  assert.match(snap.json, /gpt-local/, '非密钥内容仍须完整导出')
})

test('B · 密钥名判定覆盖常见字段（apiKey / token / secret / password / authorization）', () => {
  for (const n of ['apiKey', 'api_key', 'APIKEY', 'accessToken', 'client_secret', 'password', 'Authorization']) {
    assert.ok(SECRET_NAME_RE.test(n), `${n} 应判为密钥名`)
  }
  for (const n of ['name', 'model', 'baseUrl', 'X-Trace', 'PATH', 'timeout']) {
    assert.ok(!SECRET_NAME_RE.test(n), `${n} 不应判为密钥名`)
  }
})

// ═══════════════════════════════════════════════════════════════
// C 导入结构校验（解析 + 白名单 + 往返）
// ═══════════════════════════════════════════════════════════════
test('C · parseImportText：空 / 非法 JSON / 非对象 三种失败态', () => {
  assert.deepEqual(parseImportText(''), { ok: false, reason: 'empty' })
  assert.deepEqual(parseImportText('   '), { ok: false, reason: 'empty' })
  const bad = parseImportText('{bad json')
  assert.equal(bad.ok, false)
  assert.equal(bad.reason, 'json')
  assert.ok(bad.detail && bad.detail.length > 0, '须带原始详情（可展开）')
  assert.equal(parseImportText('[1,2,3]').reason, 'shape')
  assert.equal(parseImportText('"str"').reason, 'shape')
  assert.equal(parseImportText('null').reason, 'shape')
})

test('C · parseImportText 接受信封与裸对象两种外形', () => {
  const env = parseImportText(serializeSnapshot(sampleConfig()).json)
  assert.equal(env.ok, true)
  assert.equal(env.format, 'envelope')
  assert.equal(env.meta.app, EXPORT_APP)
  assert.equal(env.data.theme, 'dark')

  const plain = parseImportText('{"theme":"nord","bogus":1}')
  assert.equal(plain.ok, true)
  assert.equal(plain.format, 'plain')
  assert.equal(plain.data.theme, 'nord')
})

test('C · filterImport：白名单内 applied / 白名单外 ignored（含 id 跳过）', () => {
  const { data, applied, ignored } = filterImport({
    id: 'user-config',
    theme: 'nord',
    responseTimeout: 300,
    llms: [{ name: 'a' }],
    bogus_key: 1,
    '__proto__x': 2,
  })
  assert.deepEqual(applied.sort(), ['llms', 'responseTimeout', 'theme'].sort())
  assert.deepEqual(ignored.sort(), ['__proto__x', 'bogus_key'].sort())
  assert.equal(data.id, undefined, '域 id 不进导入载荷')
  assert.equal(data.theme, 'nord')
  assert.deepEqual(data.llms, [{ name: 'a' }])
  assert.equal(data.bogus_key, undefined)
})

test('C · filterImport：集合键非数组 → 空数组（口径同 persist writeCollection）', () => {
  const { data, ignored } = filterImport({ llms: 'oops', mcpServers: { a: 1 } })
  assert.deepEqual(data.llms, [])
  assert.equal(data.mcpServers, undefined, 'mcpServers 已非白名单键（旧 usr KV 废弃）→ 忽略')
  assert.ok(ignored.includes('mcpServers'))
})

test('C · 导出 → 导入往返：键集合一致，值原样回读', () => {
  const snap = serializeSnapshot(sampleConfig(), { excludeSecrets: true })
  const parsed = parseImportText(snap.json)
  const { data, applied, ignored } = filterImport(parsed.data)

  assert.deepEqual(ignored, [], '导出文件不含白名单外键')
  assert.deepEqual(applied.sort(), Object.keys(data).sort())
  assert.equal(data.theme, 'dark')
  assert.equal(data.defaultLLM, 'gpt-local')
  assert.equal(data.tool_async, '{"demo":{"mode":"auto"}}', '自由键按字符串原样回读')
  assert.equal(data.llms[0].apiKey, undefined)
})

test('C · 需重启键判定 = usr 工具链路径子集（批 2 APPLY_RESTART 口径）', () => {
  assert.deepEqual(restartKeysOf(['theme', 'chromePath', 'goPath', 'responseTimeout']),
    ['chromePath', 'goPath'])
  assert.deepEqual(restartKeysOf(undefined), [])
  assert.equal(RESTART_KEYS.length, 7, '七个工具链路径键（含 chromePath）')
  for (const k of ['theme', 'locale', 'llms', 'defaultLLM', 'tool_async']) {
    assert.ok(!RESTART_KEYS.includes(k), `${k} 不应列为需重启键`)
  }
})

// ═══════════════════════════════════════════════════════════════
// D 白名单 ↔ 后端权威源逐键一致（防漂移）
// ═══════════════════════════════════════════════════════════════
test('D · 键白名单与 config 域实现 userconfig.go 逐键一致', () => {
  // 阶段 4 internal 下沉：config 域实现已由 persist 下沉 internal/config（判定逻辑不变）。
  const goPath = 'lib/data/internal/config/userconfig.go'
  const goSrc = readRepo(goPath)
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/[^\n]*/g, '')

  const keysAfter = (marker) => {
    const i = goSrc.indexOf(marker)
    assert.ok(i >= 0, `${goPath} 缺少 ${marker}`)
    const start = goSrc.indexOf('{', i)
    const end = goSrc.indexOf('\n}', start)
    assert.ok(start >= 0 && end > start, `${marker} 字面量解析失败`)
    return [...goSrc.slice(start, end).matchAll(/"([A-Za-z_][A-Za-z0-9_]*)":/g)].map((m) => m[1])
  }

  assert.deepEqual([...SNAPSHOT_SCALAR_KEYS].sort(),
    keysAfter('var userConfigKeyKinds = map[string]string').sort(), '标量键清单须与 userConfigKeyKinds 一致')
  assert.deepEqual([...SNAPSHOT_FREE_KEYS].sort(),
    keysAfter('var userConfigFreeKeys = map[string]bool').sort(), '自由键清单须与 userConfigFreeKeys 一致')
  assert.deepEqual([...SNAPSHOT_COLLECTION_KEYS].sort(),
    keysAfter('var collectionKeys = map[string]string').sort(), '集合键须与 collectionKeys 一致')
  assert.equal(SNAPSHOT_KEYS.length, SNAPSHOT_SCALAR_KEYS.length + SNAPSHOT_FREE_KEYS.length + SNAPSHOT_COLLECTION_KEYS.length)
})

// ═══════════════════════════════════════════════════════════════
// E 页面守卫（views/config/SettingsConfigIOPage.vue）
// ═══════════════════════════════════════════════════════════════
test('E · 页面：导出（含密钥警示 + 排除密钥默认关）/ 导入 / 恢复出厂 三区齐备', () => {
  const src = read('views/config/SettingsConfigIOPage.vue')

  assert.match(src, /class="io-section io-export"/, '须有导出区')
  assert.match(src, /class="io-section io-import"/, '须有导入区')
  assert.match(src, /class="io-section io-reset"/, '须有恢复厂区')
  assert.match(src, /\$t\('configIO\.exportSecretWarn'\)/, '导出区须显著警示「含 API Key」')
  assert.match(src, /const excludeSecrets = ref\(false\)/, '「排除密钥」默认关（= 默认包含）')
  assert.match(src, /class="io-switch-row io-exclude-switch"/, '「排除密钥」开关须可操作')
  assert.match(src, /type="file"/, '导入须支持选择 JSON 文件')
  assert.match(src, /accept="\.json,application\/json"/)
  assert.match(src, /class="io-paste"/, '导入须支持粘贴 JSON（browser 形态降级路径）')
})

test('E · 页面：导出落盘 / 备份 / 恢复出厂走既有消息面，且恢复出厂为二次确认', () => {
  const src = read('views/config/SettingsConfigIOPage.vue')

  assert.match(src, /saveConfigFile\(snap\.fileName, snap\.json\)/, '导出须经 gui.file.save（系统另存为）')
  assert.match(src, /saveConfigFile\(name, json, 'backup'\)/, '导入/恢复出厂前须先写备份（mode=backup）')
  assert.match(src, /const backup = await backupCurrent\(\)/, '导入前须先备份')
  const resetFn = src.slice(src.indexOf('async function onReset'))
  assert.equal((resetFn.match(/askConfirm\(t\('configIO\.resetConfirm/g) || []).length, 2,
    '恢复出厂须两次确认')
  assert.match(resetFn, /const backup = await backupCurrent\(\)[\s\S]*await clearUserConfig\(\)/,
    '恢复出厂须「先备份再清空」')
  assert.match(src, /clearUserConfig\(\)/, '清空走既有 data-user-config-delete（不带 key）')
  assert.match(src, /onDataRefresh\('user-config', reload\)/, '刷新走既有 refresh 广播')
})

test('E · 页面：浏览器降级（下载/剪贴板）、禁 watch、不打印快照内容', () => {
  const src = read('views/config/SettingsConfigIOPage.vue')

  assert.match(src, /downloadText\(snap\.fileName, snap\.json\)/, '宿主不可用 → 下载降级')
  assert.match(src, /navigator\.clipboard\.writeText/, '提供复制到剪贴板路径')
  assert.match(src, /classifyError/, '失败提示复用批 2 分类口径')
  assert.match(src, /<details v-if="importError\.detail">[\s\S]*io-detail/, '原始详情可展开')
  assert.ok(!/watch\(|watchEffect\(/.test(src), '禁 watch/watchEffect')
  assert.ok(!/console\.(log|debug|info)\(/.test(src), '禁打印快照内容（可能含密钥）')
})

test('E · 页面：需重启键按批 2 口径标注', () => {
  const src = read('views/config/SettingsConfigIOPage.vue')
  assert.match(src, /restartKeysOf\(applied\)/)
  assert.match(src, /\$t\('configIO\.restartTag'\)/)
  assert.match(src, /configIO\.importRestartNote/)
})

// ═══════════════════════════════════════════════════════════════
// F 接线（api / i18n / locales / 死键）
// ═══════════════════════════════════════════════════════════════
test('F · api/config.js：数据源 = usr 主库视图；清空/落盘接口齐备', () => {
  const src = read('api/config.js')
  assert.match(src, /export async function getUserConfigMain\(\)[\s\S]*dataClient\.list\('user-config'\)/,
    '导出/备份数据源须走 data-user-config-list（usr 主库视图，不含项目层覆盖）')
  assert.match(src, /export function clearUserConfig\(\)[\s\S]*dataClient\.remove\('user-config'\)/,
    '恢复出厂 = 不带 key 的 delete（清空整份 usr 配置）')
  assert.match(src, /export function saveConfigFile\(name, content, mode\)[\s\S]*guiReq\('file\.save'/,
    '落盘须走新 native 消息 gui.file.save')
})

test('F · ProjectConfig 页签 + i18n 注册 + configIO 双语齐备', () => {
  const pc = read('views/preview/ProjectConfig.vue')
  assert.match(pc, /<template #configIO>/, '项目配置页须有 configIO 页签插槽')
  assert.match(pc, /name: 'configIO'/, '页签名 configIO')
  assert.match(pc, /t\('configIO\.tabTitle'\)/, '页签文案走 configIO.tabTitle')

  const i18nSrc = read('plugins/i18n.js')
  assert.match(i18nSrc, /import zhConfigIO from '\.\.\/locales\/zh-CN\/configIO\.json'/)
  assert.match(i18nSrc, /import enConfigIO from '\.\.\/locales\/en-US\/configIO\.json'/)
  assert.match(i18nSrc, /configIO: zhConfigIO/)
  assert.match(i18nSrc, /configIO: enConfigIO/)

  const zh = readLocale('zh-CN', 'configIO.json')
  const en = readLocale('en-US', 'configIO.json')
  const zhKeys = Object.keys(zh).sort()
  assert.deepEqual(Object.keys(en).sort(), zhKeys, 'zh/en 键集须一致')
  for (const k of zhKeys) {
    assert.ok(typeof zh[k] === 'string' && zh[k].trim(), `zh 缺 ${k}`)
    assert.ok(typeof en[k] === 'string' && en[k].trim(), `en 缺 ${k}`)
  }
  for (const k of ['tabTitle', 'exportSecretWarn', 'excludeSecrets', 'exportToFile', 'copyToClipboard',
    'importBadJson', 'importNoKeys', 'importConfirmBody', 'importRestartNote', 'restartTag',
    'resetScope', 'resetConfirm1', 'resetConfirm2', 'resetBackup']) {
    assert.ok(k in zh, `configIO 缺键 ${k}`)
  }
  // 占位符口径一致（zh 有的 {x}，en 必须有）
  for (const k of zhKeys) {
    const ph = (s) => [...String(s).matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
    assert.deepEqual(ph(en[k]), ph(zh[k]), `${k} 占位符须双语一致`)
  }
})

test('F · 死键 reset_confirm 已清除（无消费方 → 本批删除）', () => {
  for (const loc of LOCALES) {
    const cfg = readLocale(loc, 'config.json')
    assert.equal(cfg.reset_confirm, undefined, `${loc}/config.json 的 reset_confirm 死键须删除`)
  }
  // 本批新文案落新文件，避免与并行任务抢 config.json
  const zhIO = readLocale('zh-CN', 'configIO.json')
  assert.match(zhIO.resetConfirm1, /清空全部用户级全局配置/)
  assert.match(zhIO.resetScope, /不含项目级配置/)
})

// ═══════════════════════════════════════════════════════════════
// G 桥实现（chonkpilot-gui/bridge）
// ═══════════════════════════════════════════════════════════════
test('G · gui.file.save 已注册；backup 落 <prjusr 数据根>/backup；不记录内容', () => {
  const guimsg = readRepo('lib/gui/bridge/guimsg.go')
  assert.match(guimsg, /case "file\.save":/, 'gui.file.save 须在 guiDo 分派中注册')

  const impl = readRepo('lib/gui/bridge/configfile.go')
  assert.match(impl, /backupSubdir = "backup"/)
  assert.match(impl, /filepath\.Join\(b\.uploadRoot\(\), backupSubdir, name\)/, '备份固定落 <prjusr 数据根>/backup/<name>')
  assert.match(impl, /filepath\.Base\(strings\.TrimSpace\(req\.Name\)\)/, '文件名须净化（防穿越）')
  assert.match(impl, /if req\.Mode == "backup"/, 'backup 模式不弹框')
  assert.match(impl, /pickSaveFileName\(/, 'dialog 模式走系统另存为')
  assert.ok(!/(log\.|slog\.|fmt\.Print)/.test(impl), '内容可能含密钥 → 桥侧不得打印任何日志')

  const dlg = readRepo('lib/gui/bridge/savefile_windows.go')
  assert.match(dlg, /GetSaveFileNameW/)
  assert.match(dlg, /ofnOverwritePrompt/, '另存为须有覆盖提示')
})
