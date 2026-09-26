/**
 * 「前端配置面」缺口修复批 单测（2026-09-19）。
 *
 * 覆盖清单 5/6/7前端(/8/9) 与 I-82 边界标注：
 *   ① prj `logLevel` 写入与反馈（LogConfig.vue + ProjectConfig「日志」页签）
 *   ② Chrome 未检测告警（Toolbar；真实信号 = gui.toolchain.detect ∪ usr chromePath）
 *   ③ 日志目录入口 logDir 存在/缺失两种表现（utils/logDirView.js）
 *   ④ 引擎运行态真实可得口径（utils/engineStatus.js；注册态，非伪造进程态）
 *   ⑤ 沙箱两页状态互见摘要与跳转（utils/sandboxSummary.js + 两页）
 *   ⑥ I-82/I-109 不适用标注（utils/toolSource.js；含「无法判定则不标」）
 *   + 通用守卫：i18n 双语齐备、无 watch/watchEffect。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯逻辑直调 + `*.vue` 源码守卫（与 dirPicker / instanceScope 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { isChromeMissing } from '../src/utils/chromeStatus.js'
import { logDirView } from '../src/utils/logDirView.js'
import { hasEngineTools } from '../src/utils/engineStatus.js'
import { countServerSandboxOn, countToolSandboxOn } from '../src/utils/sandboxSummary.js'
import { isDirNode, isThirdPartyProvider, isBuiltinRuntimeProvider } from '../src/utils/toolSource.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) =>
  JSON.parse(readFileSync(join(srcDir, 'locales', loc, name), 'utf8'))

const LOCALES = ['zh-CN', 'en-US']

// ── ① prj logLevel 前端入口 ──────────────────────────────
test('① LogConfig 读写 prj logLevel 并给「即时生效」反馈', () => {
  const src = read('views/settings/LogConfig.vue')
  assert.match(src, /getAllConfig\(\)/, '须读 prj 配置（getAllConfig）')
  assert.match(src, /c\['logLevel'\]/, '须读 prj logLevel 键')
  assert.match(src, /setConfig\('logLevel',/, '须写 prj logLevel 键')
  assert.match(src, /message\.success\(t\('projectConfig\.log_saved'\)\)/, '保存后须给明确反馈')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, 'LogConfig 不得使用 watch/watchEffect')
  // 项目级设置页沿用既有分页模式（ProjectConfig 新「日志」页签）
  const pc = read('views/preview/ProjectConfig.vue')
  assert.match(pc, /import LogConfig from '\.\.\/settings\/LogConfig\.vue'/, '须引入 LogConfig')
  assert.match(pc, /name: 'log'/, '须新增「日志」页签')
})

// ── ② Chrome 未检测告警 ──────────────────────────────────
test('② isChromeMissing：探测/配置任一非空即不告警', () => {
  assert.equal(isChromeMissing({ detectedPath: '', userChromePath: '' }), true)
  assert.equal(isChromeMissing({}), true)
  assert.equal(isChromeMissing({ detectedPath: 'C:/c/chrome.exe' }), false)
  assert.equal(isChromeMissing({ userChromePath: 'C:/c/chrome.exe' }), false)
  assert.equal(isChromeMissing({ detectedPath: '  ', userChromePath: '  ' }), true, '空白视为未配置')
})

test('② Toolbar 按真实后端信号显示/隐藏告警并跳路径配置', () => {
  const src = read('views/toolbar/Toolbar.vue')
  assert.match(src, /detectToolchains\(\)/, '须读 gui.toolchain.detect 探测结果')
  assert.match(src, /getUserConfig\(\)/, '须读 usr 配置（chromePath 覆盖）')
  assert.match(src, /cfg\.chromePath/, '须用 chromePath 覆盖探测结果')
  assert.match(src, /isChromeMissing\(/, '须走统一判据')
  assert.match(src, /v-if="chromeMissing"/, '告警图标须按判定显示/隐藏')
  assert.match(src, /kind: 'settings-paths'/, '点击须跳转路径配置')
  assert.match(src, /onDataRefresh\('user-config'/, 'usr 配置变更须重新判定（接线）')
})

// ── ③ 日志目录入口（logDir 存在/缺失）────────────────────
test('③ logDirView：缺失不显示空路径；GUI 打开 / browser 复制', () => {
  const missing = logDirView({ logDir: '', isBrowser: false })
  assert.equal(missing.show, false)
  assert.equal(missing.path, '', '缺失时不得显示空路径')
  assert.equal(missing.hintKey, 'projectConfig.log_dir_unavailable')

  const gui = logDirView({ logDir: 'E:/d/logs', isBrowser: false })
  assert.equal(gui.show, true)
  assert.equal(gui.path, 'E:/d/logs')
  assert.equal(gui.canOpen, true)
  assert.equal(gui.canCopy, false)
  assert.equal(gui.hintKey, '')

  const browser = logDirView({ logDir: 'E:/d/logs', isBrowser: true })
  assert.equal(browser.show, true)
  assert.equal(browser.canOpen, false, 'browser 形态 native 不可用')
  assert.equal(browser.canCopy, true)
  assert.equal(browser.hintKey, 'projectConfig.log_dir_browser_hint')
})

test('③ LogConfig 消费 init-data 的 logDir（无空路径）+ 无 watch', () => {
  const src = read('views/settings/LogConfig.vue')
  assert.match(src, /loadInitData\(\)/, '须读 gui.init-data')
  assert.match(src, /r\.logDir|logDir/, '须取 logDir 字段')
  assert.match(src, /logDirView\(/, '须走 logDirView 视图模型')
  assert.match(src, /revealInExplorer\(/, 'GUI 形态须用既有 native reveal 打开目录')
  assert.match(src, /navigator\.clipboard\.writeText/, 'browser 形态须提供复制')
})

// ── ④ 引擎运行态（真实可得口径）──────────────────────────
test('④ hasEngineTools：按工具面注册名判定（含 self_ 前缀暴露名）', () => {
  // 实测暴露名带 self_ 前缀（gateway applyPrefix；L4 run_index_gate.py 断言）
  assert.equal(hasEngineTools([{ name: 'self_codegraph_symbol_search' }], 'codegraph'), true)
  assert.equal(hasEngineTools([{ name: 'self_vfts_query' }], 'vfts'), true)
  // 裸名亦接受（前缀口径变化时仍可用）
  assert.equal(hasEngineTools([{ name: 'codegraph_symbol_search' }], 'codegraph'), true)
  assert.equal(hasEngineTools([{ name: 'vfts_query' }], 'vfts'), true)
  assert.equal(hasEngineTools([{ name: 'self_vfts_query' }], 'codegraph'), false)
  assert.equal(hasEngineTools([{ name: 'self_vfts_query' }], 'vfts'), true)
  assert.equal(hasEngineTools([], 'codegraph'), false)
  assert.equal(hasEngineTools(null, 'vfts'), false)
})

test('④ 引擎页补「工具面注册态」（真实可得，不伪造进程态）+ 无 watch', () => {
  for (const { file, engine } of [
    { file: 'views/settings/CodegraphConfig.vue', engine: 'codegraph' },
    { file: 'views/settings/VftsConfig.vue', engine: 'vfts' },
  ]) {
    const src = read(file)
    assert.match(src, /mq\.emit\('tools-list'/, `${file} 须读工具面（tools-list）`)
    assert.match(src, new RegExp(`hasEngineTools\\(tools, '${engine}'\\)`), `${file} 须判引擎注册态`)
    assert.match(src, /projectConfig\.engine_tool_state/, `${file} 须回显引擎工具面状态`)
    assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, `${file} 不得使用 watch/watchEffect`)
  }
})

// ── ⑤ 沙箱两页状态互见 ───────────────────────────────────
test('⑤ 摘要计数：server 级仅 stdio 计入；工具级计 true', () => {
  assert.equal(countServerSandboxOn([
    { sandbox: true },                       // 无 url/transport → stdio → 计
    { sandbox: true, transport: 'stdio' },   // 计
    { sandbox: true, url: 'http://x' },      // http → 不计
    { sandbox: true, transport: 'http' },    // 不计
    { sandbox: false, transport: 'stdio' },  // 关 → 不计
  ]), 2)
  assert.equal(countServerSandboxOn(null), 0)

  assert.equal(countToolSandboxOn({ a: true, b: false, c: true }), 2)
  assert.equal(countToolSandboxOn('{"a":true,"b":false}'), 1, 'JSON 字符串形态须可解析')
  assert.equal(countToolSandboxOn(null), 0)
})

test('⑤ 两页各补摘要 + 一键跳转（既有 previewTabOpen）', () => {
  const toolPage = read('views/config/SettingsToolSandboxPage.vue')
  assert.match(toolPage, /config\.toolSandbox\.serverSummary/, '工具级页须显示 server 级摘要')
  assert.match(toolPage, /kind: 'settings-mcp'/, '工具级页须可跳 server 级页')

  const mcpPage = read('views/config/SettingsMCPPage.vue')
  assert.match(mcpPage, /config\.mcp\.toolSandboxSummary/, 'server 级页须显示工具级摘要')
  assert.match(mcpPage, /kind: 'settings-tool-sandbox'/, 'server 级页须可跳工具级页')
})

// ── ⑥ I-82/I-109 不适用标注 ──────────────────────────────
test('⑥ 来源判定：self/dir=builtin；第三方=非 self/dir；无 server → 不判定', () => {
  assert.equal(isDirNode({ server: { category: 'dir' } }), true)
  assert.equal(isDirNode({ server: { category: 'core', alias: 'self' } }), false)
  assert.equal(isDirNode({}), false)

  assert.equal(isBuiltinRuntimeProvider({ server: { alias: 'self', category: 'core' } }), true)
  assert.equal(isBuiltinRuntimeProvider({ server: { category: 'dir' } }), true)
  assert.equal(isBuiltinRuntimeProvider({ server: { alias: 'ext', category: 'core' } }), false)
  assert.equal(isBuiltinRuntimeProvider({}), null, '无 _meta.server → 无法判定')

  assert.equal(isThirdPartyProvider({ server: { alias: 'self' } }), false)
  assert.equal(isThirdPartyProvider({ server: { category: 'dir' } }), false)
  assert.equal(isThirdPartyProvider({ server: { alias: 'slow3p' } }), true, '非 self/dir → 第三方')
  assert.equal(isThirdPartyProvider({}), null, '无 _meta.server → 不标注（宁缺勿错）')
})

test('⑥ 沙箱页：第三方行标注并禁用；async 页：dir 行 hard_timeout 标注并禁用', () => {
  const sandbox = read('views/config/SettingsToolSandboxPage.vue')
  assert.match(sandbox, /row\.thirdParty === true/, '第三方行须按判定标注')
  assert.match(sandbox, /thirdPartyNotApplicable/, '须给不适用原因')
  assert.match(sandbox, /:disabled="row\.thirdParty === true"/, '第三方行开关须禁用（避免配了不生效）')
  // 来源判定落在 composable（页面只消费 row.thirdParty）
  assert.match(read('composables/useToolSandbox.js'), /utils\/toolSource/, '须用统一来源判定')

  const asyncPage = read('views/config/SettingsToolAsyncPage.vue')
  assert.match(asyncPage, /isDirNode\(meta\)/, 'async 页须判定 dir 节点')
  assert.match(asyncPage, /hardTimeoutNotApplicable/, '须给 hard_timeout 不适用原因')
  assert.match(asyncPage, /:disabled="row\.dirNode === true"/, 'dir 行 hard_timeout 须禁用')
  // 不写库守卫：dir 行不得把 hard_timeout 写入 usr 配置
  assert.match(asyncPage, /if \(!row\.dirNode && row\.hardTimeout/, 'dir 行不写 hard_timeout（避免误导）')
})

// ── 通用：i18n 双语齐备 ──────────────────────────────────
test('i18n：新增键 zh-CN / en-US 齐备', () => {
  const projectKeys = [
    'log', 'log_level_label', 'log_level_hint', 'log_level_debug', 'log_level_info',
    'log_level_warn', 'log_level_error', 'log_saved', 'log_dir_label', 'log_dir_open',
    'log_dir_copy', 'log_dir_copied', 'log_dir_open_failed', 'log_dir_unavailable',
    'log_dir_browser_hint', 'engine_tool_state', 'engine_registered', 'engine_unregistered',
    'engine_tool_state_hint',
  ]
  const configKeys = {
    top: ['chromeTip'],
    mcp: ['toolSandboxSummary', 'gotoToolSandbox'],
    toolAsync: ['naBadge', 'hardTimeoutNotApplicable'],
    toolSandbox: ['naBadge', 'thirdPartyNotApplicable', 'serverSummary', 'gotoMcp'],
  }
  for (const loc of LOCALES) {
    const pc = readLocale(loc, 'projectConfig.json')
    for (const k of projectKeys) assert.ok(pc[k], `${loc} projectConfig 缺 ${k}`)
    const cfg = readLocale(loc, 'config.json')
    for (const k of configKeys.top) assert.ok(cfg[k], `${loc} config 缺 ${k}`)
    for (const k of configKeys.mcp) assert.ok(cfg.mcp[k], `${loc} config.mcp 缺 ${k}`)
    for (const k of configKeys.toolAsync) assert.ok(cfg.toolAsync[k], `${loc} config.toolAsync 缺 ${k}`)
    for (const k of configKeys.toolSandbox) assert.ok(cfg.toolSandbox[k], `${loc} config.toolSandbox 缺 ${k}`)
  }
})
