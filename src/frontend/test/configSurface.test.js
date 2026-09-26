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

// ── ⑤b MCP 编辑对话框重构（2026-09-26：两页签 + 字段说明 tooltip + 去「分类」）──
test('⑤b MCP 编辑弹窗：Tabs 两页签，字段按「基本信息 / 运行信息」分组', () => {
  const dlg = read('views/config/EditMCPDialog.vue')
  assert.match(dlg, /<Tabs v-model="activeTab" :tabs="tabItems"/, '须用 Tabs 组件承载两页签')
  assert.match(dlg, /name: 'basic', label: t\('config\.mcp\.tabBasic'\)/, '缺「基本信息」页签')
  assert.match(dlg, /name: 'runtime', label: t\('config\.mcp\.tabRuntime'\)/, '缺「运行信息」页签')
  assert.match(dlg, /<template #basic>/, '缺 basic 具名插槽')
  assert.match(dlg, /<template #runtime>/, '缺 runtime 具名插槽')
  // 运行信息：isolate / sandbox / runtime / args / env / cwd / hotTools 均在 runtime 插槽内
  const rt = dlg.slice(dlg.indexOf('<template #runtime>'), dlg.indexOf('</Tabs>'))
  for (const key of ['config.mcp.runtime', 'config.mcp.args', 'config.mcp.env', 'config.mcp.cwd',
    'config.mcp.hotTools', 'config.mcp.hotToolsSet', 'config.mcp.isolate', 'config.mcp.sandbox']) {
    assert.ok(rt.includes(key), `runtime 页签缺 ${key}`)
  }
  // 基本信息：name / enabled / transport / url / namespace / timeout / description / headers
  const basic = dlg.slice(dlg.indexOf('<template #basic>'), dlg.indexOf('<template #runtime>'))
  for (const key of ['config.mcp.name', 'config.mcp.enabled', 'config.mcp.transport',
    'config.mcp.serverUrl', 'config.mcp.namespace', 'config.mcp.timeout',
    'config.mcp.description', 'config.mcp.headers']) {
    assert.ok(basic.includes(key), `基本信息页签缺 ${key}`)
  }
  // 保存载荷不因页签变化：handleSave 一次性提交两页字段
  assert.match(dlg, /const connectorOK = transport\.value === 'stdio'/, 'handleSave 校验须保留')
  assert.match(dlg, /emit\('save', \{ \.\.\.localData \}, props\.editIndex\)/, 'handleSave 须一次性提交全部字段')
})

test('⑤b 字段说明 tooltip：传输方式 ? tooltip + 新 help 图标；「分类」输入已移除', () => {
  const dlg = read('views/config/EditMCPDialog.vue')
  // 传输方式：? tooltip 承载 transportHint（不再恒显 form-hint）
  assert.match(dlg, /<Tooltip :content="\$t\('config\.mcp\.transportHint'\)"/, '传输方式说明须改为 tooltip')
  assert.doesNotMatch(dlg, /class="form-hint">\{\{ \$t\('config\.mcp\.transportHint'\) \}\}/, '不应再恒显 transportHint')
  assert.match(dlg, /<Icon name="help"/, '须用 help 图标做 ? 入口')
  // help 图标（问号圆）已加入图标表
  assert.match(read('components/icon/icons.js'), /'help':\s*'<svg/, 'icons.js 缺 help 图标')
  // 「分类」输入项已从弹窗与 DEFAULT_MCP 移除
  assert.doesNotMatch(dlg, /config\.mcp\.category/, '弹窗不应再有「分类」输入')
  const defaults = read('config/defaults.js')
  assert.doesNotMatch(defaults, /category:/, 'DEFAULT_MCP 不应再有 category')
  assert.match(defaults, /sandbox: null/, 'DEFAULT_MCP 须补 sandbox: null（三态）')
})

// ── ⑤c MCP 高频工具（2026-09-26：文本框 → 「摘要 + 设置」行 + 独立弹窗）────
test('⑤c MCP 高频工具：运行信息行为「摘要 + 设置」，独立弹窗按别名勾选并写库原名', () => {
  const dlg = read('views/config/EditMCPDialog.vue')
  // 原「高频工具」逗号分隔文本输入框已移除
  assert.doesNotMatch(dlg, /hotToolsText/, '不应再有 hotToolsText 文本输入')
  assert.doesNotMatch(dlg, /config\.mcp\.hotToolsPlaceholder/, '不应再引用 hotToolsPlaceholder')
  // 同一位置改为「高频工具」行：摘要文字 + 「设置」按钮
  const rt = dlg.slice(dlg.indexOf('<template #runtime>'), dlg.indexOf('</Tabs>'))
  assert.match(rt, /data-hot-tools-set/, '运行信息页签缺「设置」按钮')
  assert.match(rt, /config\.mcp\.hotToolsSet/, '「设置」按钮须用 i18n 文案')
  assert.match(rt, /hotToolsSummary/, '缺高频工具摘要文字')
  // 设置按钮打开独立弹窗并回填 hot_tools（不改「保存才落库」的时机）
  assert.match(dlg, /function openHotTools\(\)/, '缺 openHotTools')
  assert.match(dlg, /SetMCPHotToolsDialog/, '须打开 SetMCPHotToolsDialog')
  assert.match(dlg, /localData\.hot_tools = \[\.\.\.hotTools\.value\]/, 'handleSave 须提交 hot_tools')

  // 新弹窗：数据源 = 既有 tools-list（零新增消息面）、按 _meta.server 归属、写库原名、全部/逐项
  const sub = read('views/config/SetMCPHotToolsDialog.vue')
  assert.match(sub, /mq\.emit\('tools-list'/, '须读既有 tools-list（零新增消息面）')
  assert.match(sub, /srv\.alias !== name && srv\.node !== name/, '须按 _meta.server.alias/node 归属当前 server')
  assert.match(sub, /stripToolPrefix\(/, '写库前须做暴露名 → 原名转换')
  assert.match(sub, /data-hot-all/, '缺「全部工具」复选框')
  assert.match(sub, /data-hot-confirm/, '缺「确定」按钮')
  assert.match(sub, /data-hot-cancel/, '缺「取消」按钮')
  assert.match(sub, /\['\*'\]/, '「全部」须写 "*"（gateway isHot 语义）')
  assert.match(sub, /hotToolsEmpty/, '缺空态提示')
  assert.doesNotMatch(sub, /\bwatch(Effect)?\s*\(/, '弹窗不得使用 watch/watchEffect')
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

test('⑥ 沙箱页：executor 级行 + 第三方/http·sse 边界说明；async 页：dir 行 hard_timeout 标注并禁用', () => {
  const sandbox = read('views/config/SettingsToolSandboxPage.vue')
  assert.match(sandbox, /:data-exec="row\.category"/, '须按 executor 类别渲染行')
  assert.match(sandbox, /thirdPartyHint/, '须给第三方 / http·sse 边界说明（不可隔离原因）')
  assert.match(sandbox, /:disabled="!dirty"/, '无改动时保存按钮禁用')
  // executor 级不再逐工具判定第三方（第三方沙箱归 MCP 弹窗「运行信息」页签）
  assert.doesNotMatch(read('composables/useToolSandbox.js'), /utils\/toolSource/, 'executor 级不再逐工具判定来源')

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
    mcp: ['toolSandboxSummary', 'gotoToolSandbox', 'hotTools', 'hotToolsSet', 'hotToolsNone',
      'hotToolsAll', 'hotToolsCount', 'hotToolsTitle', 'hotToolsHint', 'hotToolsAllLabel',
      'hotToolsEmpty', 'hotToolsLoading'],
    toolAsync: ['naBadge', 'hardTimeoutNotApplicable'],
    toolSandbox: ['serverSummary', 'gotoMcp', 'thirdPartyHint', 'execCore'],
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
