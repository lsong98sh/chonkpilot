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

// ── ⑤b MCP 编辑对话框重排（2026-09-27：三页签 + 底栏「启用」+ 字段说明 tooltip）──
test('⑤b MCP 编辑弹窗：Tabs 三页签，字段按「基本信息 / 运行信息 / 工具」分组', () => {
  const dlg = read('views/config/EditMCPDialog.vue')
  assert.match(dlg, /<Tabs v-model="activeTab" :tabs="tabItems"/, '须用 Tabs 组件承载页签')
  assert.match(dlg, /name: 'basic', label: t\('config\.mcp\.tabBasic'\)/, '缺「基本信息」页签')
  assert.match(dlg, /name: 'runtime', label: t\('config\.mcp\.tabRuntime'\)/, '缺「运行信息」页签')
  assert.match(dlg, /name: 'tools', label: t\('config\.mcp\.tabTools'\)/, '缺「工具」页签')
  assert.match(dlg, /<template #basic>/, '缺 basic 具名插槽')
  assert.match(dlg, /<template #runtime>/, '缺 runtime 具名插槽')
  assert.match(dlg, /<template #tools>/, '缺 tools 具名插槽')
  // 运行信息：runtime / cwd / args / env / isolate / sandbox（高频工具已移「工具」页签）
  const rt = dlg.slice(dlg.indexOf('<template #runtime>'), dlg.indexOf('<template #tools>'))
  for (const key of ['config.mcp.runtime', 'config.mcp.args', 'config.mcp.env', 'config.mcp.cwd',
    'config.mcp.isolate', 'config.mcp.sandbox']) {
    assert.ok(rt.includes(key), `runtime 页签缺 ${key}`)
  }
  // 基本信息：name / transport / url / namespace / timeout / description / headers（「启用」已移底栏）
  const basic = dlg.slice(dlg.indexOf('<template #basic>'), dlg.indexOf('<template #runtime>'))
  for (const key of ['config.mcp.name', 'config.mcp.transport',
    'config.mcp.serverUrl', 'config.mcp.namespace', 'config.mcp.timeout',
    'config.mcp.description', 'config.mcp.headers']) {
    assert.ok(basic.includes(key), `基本信息页签缺 ${key}`)
  }
  assert.doesNotMatch(basic, /config\.mcp\.enabled/, '「启用」应移出基本信息页签（至底栏）')
  // 保存载荷不因页签变化：handleSave 一次性提交三页字段
  assert.match(dlg, /const connectorOK = transport\.value === 'stdio'/, 'handleSave 校验须保留')
  assert.match(dlg, /emit\('save', \{ \.\.\.localData \}, props\.editIndex\)/, 'handleSave 须一次性提交全部字段')
})

test('⑤b 底栏：右对齐「启用 / 取消 / 保存」，启用为 label + Switch 同行', () => {
  const dlg = read('views/config/EditMCPDialog.vue')
  // 底栏为滚动区之外的自适应高元素，不随内容滚动
  assert.match(dlg, /\.edit-footer \{[\s\S]*?flex-shrink: 0;[\s\S]*?\}/, '底栏须 flex-shrink:0（固定不滚动）')
  const iEnabled = dlg.indexOf('class="footer-enabled"')
  const iCancel = dlg.indexOf('EventNames.editMcpCancel')
  const iSave = dlg.indexOf('EventNames.editMcpSave')
  assert.ok(iEnabled > 0 && iEnabled < iCancel && iCancel < iSave, '底栏顺序须为 启用 → 取消 → 保存')
  assert.match(dlg, /class="footer-enabled">\s*<label class="form-label">\{\{ \$t\('config\.mcp\.enabled'\) \}\}<\/label>\s*<Switch v-model="localData\.enabled"/,
    '底栏「启用」须为 label + Switch 同行')
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

// ── ⑤c MCP 高频工具（2026-09-27：独立弹窗 → 「工具」页签 + 加载工具按钮）────
test('⑤c MCP 高频工具：「工具」页签【加载工具】勾选，写库原名，随主对话框保存', () => {
  const dlg = read('views/config/EditMCPDialog.vue')
  // 旧「高频工具」行（摘要 + 独立【设置】按钮 / 独立弹窗）已整体摘除
  assert.doesNotMatch(dlg, /hotToolsText/, '不应再有 hotToolsText 文本输入')
  assert.doesNotMatch(dlg, /hotToolsSummary/, '不应再有行摘要')
  assert.doesNotMatch(dlg, /data-hot-tools-set/, '不应再有独立【设置】按钮')
  assert.doesNotMatch(dlg, /SetMCPHotToolsDialog/, '不应再打开独立弹窗')
  // 新「工具」页签：加载按钮 + 初始提示 + 全部/逐项勾选 + 加载中/空态
  const tools = dlg.slice(dlg.indexOf('<template #tools>'), dlg.indexOf('</Tabs>'))
  assert.match(tools, /data-load-tools/, '工具页签缺【加载工具】按钮')
  assert.match(tools, /config\.mcp\.loadTools/, '【加载工具】须用 i18n 文案')
  assert.match(tools, /config\.mcp\.loadToolsHint/, '缺初始提示（点击加载）')
  assert.match(tools, /data-hot-all/, '缺「全部工具」复选框')
  assert.match(tools, /config\.mcp\.hotToolsAllLabel/, '「全部」须用 i18n 文案')
  assert.match(tools, /hot-tool-item/, '缺逐项工具行')
  assert.match(tools, /config\.mcp\.hotToolsEmpty/, '缺空态提示')
  assert.match(tools, /config\.mcp\.hotToolsLoading/, '缺加载中提示')
  // 数据源 = 既有 tools-list（零新增消息面）、按 _meta.server 归属、写库原名、全部/逐项
  assert.match(dlg, /mq\.emit\('tools-list'/, '须读既有 tools-list（零新增消息面）')
  assert.match(dlg, /srv\.alias !== name && srv\.node !== name/, '须按 _meta.server.alias/node 归属当前 server')
  assert.match(dlg, /stripToolPrefix\(/, '写库前须做暴露名 → 原名转换')
  assert.match(dlg, /\['\*'\]/, '「全部」须写 "*"（gateway isHot 语义）')
  assert.match(dlg, /localData\.hot_tools = \[\.\.\.hotTools\.value\]/, 'handleSave 须提交 hot_tools')
  assert.doesNotMatch(dlg, /\bwatch(Effect)?\s*\(/, '弹窗不得使用 watch/watchEffect')
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

// ⑦ 工具异步页「超时」占位：**未设置** → 「回落全局」；**0 / -1 = 无上限**（用户口径 2026-09-27）。
test('⑦ 超时列占位「回落全局」+ 0/-1 = 无上限口径（cTimeout 空时生效）', () => {
  assert.equal(readLocale('zh-CN', 'config.json').toolAsync.hardTimeoutPlaceholder, '回落全局')
  assert.equal(readLocale('en-US', 'config.json').toolAsync.hardTimeoutPlaceholder, 'Fall back to global')

  const asyncPage = read('views/config/SettingsToolAsyncPage.vue')
  assert.match(
    asyncPage,
    /row\.cTimeout === '' \? \$t\('config\.toolAsync\.hardTimeoutPlaceholder'\)/,
    '超时列占位须取该词条（cTimeout 空 = 契约未声明硬上限 → 回落全局）',
  )
  // 0 / -1 = 无上限的合法值判定（阈值 / 硬上限同一口径）
  assert.match(asyncPage, /function isLimitValue\(v\)/, '须有 isLimitValue 合法值判定')
  assert.match(asyncPage, /n >= 0 \|\| n === -1/, '0 与 -1 须判为合法（无上限）')
  assert.match(asyncPage, /isLimitValue\(raw\)/, 'onNumberBlur 须用 isLimitValue 判定合法值')
  // 占位语义与 0/-1 口径写入表头 tooltip（i18n）
  assert.match(asyncPage, /advancedHint/, '超时列表头须带 0/-1 口径 tooltip')
  const hint = readLocale('zh-CN', 'config.json').toolAsync.advancedHint
  assert.match(hint, /0 \/ -1/, 'zh tooltip 须写明 0 / -1 = 无上限')
  assert.match(hint, /回落全局/, 'zh tooltip 须写明留空 = 回落全局')
})

// ── ⑧ 索引页「叠加 gitignore」（2026-09-27）──────────────
test('⑧ 索引页：label「排除的目录和文件」+「叠加 gitignore」勾选（勾选不禁用输入框）', () => {
  const zh = readLocale('zh-CN', 'projectConfig.json')
  assert.equal(zh.exclude_paths, '排除的目录和文件', 'zh label 须为「排除的目录和文件」')
  assert.equal(zh.stack_gitignore, '叠加 gitignore', 'zh 勾选文案须为「叠加 gitignore」')

  const cases = [
    { file: 'views/settings/CodegraphConfig.vue', key: 'codegraph.stack-gitignore', ref: 'cgStackGitignore' },
    { file: 'views/settings/VftsConfig.vue', key: 'vfts.stack-gitignore', ref: 'vfStackGitignore' },
  ]
  for (const c of cases) {
    const src = read(c.file)
    // label 文案走 i18n，旧硬编码「排除目录」不得残留
    assert.match(src, /projectConfig\.exclude_paths/, `${c.file} 须用 i18n「排除的目录和文件」label`)
    assert.doesNotMatch(src, /排除目录/, `${c.file} 残留旧文案「排除目录」`)
    assert.match(src, /projectConfig\.stack_gitignore/, `${c.file} 须用 i18n「叠加 gitignore」`)
    assert.match(src, /projectConfig\.exclude_paths_hint/, `${c.file} 须有 i18n hint（含叠加语义）`)
    // label 行右侧勾选（原生 checkbox + 既有 .b-checkbox 口径）
    assert.match(src, /class="form-label-row"/, `${c.file} 须有 label 行容器`)
    assert.match(src, /<input type="checkbox" v-model="[a-zA-Z]+" \/>/, `${c.file} 须有勾选框`)
    // 勾选后输入框仍可编辑（禁止 disable/readonly 排除输入框）
    assert.doesNotMatch(src, /:disabled="[^"]*[Ss]tack/, `${c.file} 勾选不得禁用输入框`)
    assert.doesNotMatch(src, /:readonly/, `${c.file} 输入框不得 readonly`)
    // 保存同时写该键（既有 setConfig 字符串口径）
    assert.match(src, new RegExp(`setConfig\\('${c.key.replace('.', '\\.')}'`), `${c.file} 保存须写 ${c.key}`)
    // dirty（unsaved）须含勾选状态；加载回填（缺省 false）
    assert.match(src, new RegExp(`${c.ref}\\.value !== \\(origStackGitignore\\.value === 'true'\\)`), `${c.file} dirty 须含勾选状态`)
    assert.match(src, new RegExp(`${c.ref}\\.value = rawStack === 'true'`), `${c.file} 加载须回填勾选`)
    assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, `${c.file} 不得使用 watch/watchEffect`)
  }
})

// ── 通用：i18n 双语齐备 ──────────────────────────────────
test('i18n：新增键 zh-CN / en-US 齐备', () => {
  const projectKeys = [
    'log', 'log_level_label', 'log_level_hint', 'log_level_debug', 'log_level_info',
    'log_level_warn', 'log_level_error', 'log_saved', 'log_dir_label', 'log_dir_open',
    'log_dir_copy', 'log_dir_copied', 'log_dir_open_failed', 'log_dir_unavailable',
    'log_dir_browser_hint', 'engine_tool_state', 'engine_registered', 'engine_unregistered',
    'engine_tool_state_hint',
    'index_exts_label', 'index_exts_hint', 'exclude_paths', 'stack_gitignore', 'exclude_paths_hint',
  ]
  const configKeys = {
    top: ['chromeTip'],
    mcp: ['toolSandboxSummary', 'gotoToolSandbox', 'tabTools', 'loadTools', 'loadToolsHint',
      'kvAdd', 'kvDelete', 'kvKey', 'kvValue', 'hotToolsAllLabel',
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
