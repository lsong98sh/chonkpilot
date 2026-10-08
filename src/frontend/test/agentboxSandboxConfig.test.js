/**
 * agentbox 沙箱「配置面」接线守卫（[42 §2 (104)/(109)]）
 *
 * 覆盖三条硬要求（2026-09-26 起 sandbox 编辑入口收敛到 EditMCPDialog「运行信息」页签）：
 *   ① MCP 编辑弹窗「运行信息」页签「沙箱」开关 → 保存写 usr `mcps[].sandbox`（三态，未设 = 不隔离）；
 *   ② 页签「工具沙箱配置」→ 按 **executor 类别**（core/desktop/browser）写 usr `tool_sandbox`
 *      （未设 = 不隔离）；拨动只改本地待保存态，点【保存】才落库，无改动时保存按钮禁用；
 *   ③ **仅 stdio 可操作**：http/sse（及缺省 url 推断为 http）控件禁用 + i18n 原因。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 用**源码守卫**
 * 锁定接线契约，防回归到「改了 UI 但没写对配置键」或「http/sse 项可误勾」。
 *
 * 注：本文件放在 src 之外（frontend/test/），以免 node 内置模块导入进入 tsconfig 对 src 下
 * js/vue 的类型检查范围。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const readSrc = (rel) => readFileSync(join(srcDir, rel), 'utf8')

test('MCP 编辑弹窗：运行信息页签 sandbox Switch + 三态 + 保存写 mcps[].sandbox', () => {
  const vue = readSrc('views/config/EditMCPDialog.vue')
  // Switch 绑定沙箱三态开关（未显式拨动 = off 展示；拨动才写库）
  assert.match(vue, /<Switch\s+v-model="sandboxToggle"/, '缺 sandbox 三态 Switch')
  // 三态：显式记录 + 值；保存时未显式拨动则删键
  assert.match(vue, /const sandboxExplicit = ref\(/, '缺 sandboxExplicit 三态标记')
  assert.match(vue, /if \(sandboxExplicit\.value\) localData\.sandbox = !!sandboxValue\.value/,
    'handleSave 须在显式拨动时写 localData.sandbox')
  assert.match(vue, /else delete localData\.sandbox/, '未拨动须删键（缺键 = 未设置）')
  // 落盘通道（2026-10-01 起）= **mcp 域四级文件化**（由列表页 saveOne 承担，不再整表替换 usr mcps）
  assert.match(readSrc('views/config/SettingsMCPPage.vue'),
    /saveMcpServer\(/, '落盘通道须为 mcp 域（四级文件化配置）')
})

test('MCP 编辑弹窗：仅 stdio 可操作，http/sse 禁用并给 i18n 原因', () => {
  const vue = readSrc('views/config/EditMCPDialog.vue')
  // 传输归一：显式 transport 优先；缺省/auto 有 url → http，否则 stdio（与 gateway TransportName 一致）
  const tn = vue.match(/const sandboxTransportName = computed\([\s\S]*?\n\}\)/)
  assert.ok(tn, '未找到 sandboxTransportName')
  assert.match(tn[0], /\(localData\.url \|\| ''\)\.trim\(\) \? 'http' : 'stdio'/, '缺省须按 url 推断 http/stdio')
  // 可操作判据 = stdio + 控件禁用
  assert.match(vue, /const sandboxApplicable = computed\(\(\) => sandboxTransportName\.value === 'stdio'\)/,
    'sandboxApplicable 须仅 stdio 为真')
  assert.match(vue, /:disabled="!sandboxApplicable"/, 'http/sse 须禁用 Switch')
  // 禁用原因 tooltip（i18n）
  assert.match(vue, /t\('config\.mcp\.sandboxStdioOnly'\)/, '须给禁用原因（i18n）')
})

test('MCP 列表页：沙箱列已移除（编辑入口唯一 = 对话框运行信息页签）', () => {
  const vue = readSrc('views/config/SettingsMCPPage.vue')
  assert.doesNotMatch(vue, /prop:\s*'sandbox'/, '列表页不应再有 sandbox 列')
  assert.doesNotMatch(vue, /onSandboxToggle/, '列表页不应再有行内拨动')
  // 空信任目录语义说明保留（跨页提示）
  assert.match(vue, /config\.mcp\.sandboxEmptyHint/, '须保留 server 级语义说明')
})

test('新页签：useToolSandbox composable 按 executor 类别写 usr tool_sandbox，手动保存', () => {
  const js = readSrc('composables/useToolSandbox.js')
  assert.match(js, /export const TOOL_SANDBOX_KEY = 'tool_sandbox'/, '键名须为 tool_sandbox')
  assert.match(js, /export const EXECUTOR_CATEGORIES = \['core', 'desktop', 'browser'\]/, 'executor 类别须为 core/desktop/browser')
  assert.match(js, /if \(!EXECUTOR_CATEGORIES\.includes\(category\)\) continue/, '须按 _meta.category 过滤')
  assert.match(js, /saveUserConfig\(\{\s*\[TOOL_SANDBOX_KEY\]:\s*next\s*\}\)/, '须经 usr 配置面写该键（executor 级表）')
  assert.match(js, /mq\.emit\(MsgClientTopics\.toolsList/, '工具清单须取既有能力面 tools-list')
  // 过滤键名不得臆造：读的是 _meta.category / _meta.server
  assert.match(js, /meta\.category/, '须读 _meta.category')
  assert.match(js, /meta\.server/, '须读 _meta.server（展示剥前缀用）')
  // 三态 + 手动保存：拨动只改待保存态，无改动不写；全部未设置 → 删整键
  assert.match(js, /const dirty = computed\(/, '须派生「未保存改动」')
  assert.match(js, /if \(!dirty\.value\) return false/, '无改动须短路（不落库）')
  assert.match(js, /resetUserKey\(TOOL_SANDBOX_KEY\)/, '全部未设置须删整键')
})

test('新页签：页面用 composable + Switch，手动保存按钮，且不出现 watch（项目规范）', () => {
  const vue = readSrc('views/config/SettingsToolSandboxPage.vue')
  assert.match(vue, /useToolSandbox\(\)/, '页面须用 useToolSandbox composable')
  assert.match(vue, /<Switch[\s\S]*?@update:model-value="\(v\) => onToggle\(row, v\)"/, 'executor 行须有隔离 Switch')
  // 三个 executor 行 + 只读工具清单
  assert.match(vue, /v-for="row in rows"[\s\S]*?:data-exec="row\.category"/, '须按 executor 类别渲染行')
  assert.match(vue, /:data-tool="t\.name"/, '只读工具清单须带完整暴露名')
  // 手动保存：按钮 + 无改动禁用 + 未保存标记
  assert.match(vue, /data-sandbox-save/, '缺保存按钮')
  assert.match(vue, /:disabled="!dirty"/, '无改动时保存按钮须禁用')
  assert.match(vue, /data-sandbox-unsaved/, '缺「未保存」标记')
  assert.doesNotMatch(vue, /\bwatch\(/, '禁止 watch 监听（项目规范）')
  const js = readSrc('composables/useToolSandbox.js')
  assert.doesNotMatch(js, /\bwatch\(/, 'composable 禁止 watch')
})

test('页签注册：设置菜单 + CodeView 组件/标题', () => {
  // 设置菜单清单与状态栏配置入口共用（utils/configMenu.js）；Toolbar 经 configMenuItems 引用。
  const menu = readSrc('utils/configMenu.js')
  assert.match(menu, /kind:\s*'settings-tool-sandbox'/, '设置菜单缺 settings-tool-sandbox 项')
  const toolbar = readSrc('views/toolbar/Toolbar.vue')
  assert.match(toolbar, /configMenuItems\(t\)/, 'Toolbar 须引用共享设置菜单清单')
  const codeview = readSrc('views/codeview/CodeView.vue')
  assert.match(codeview, /'settings-tool-sandbox':\s*defineAsyncComponent/, 'CodeView 缺页签组件注册')
  assert.match(codeview, /case 'settings-tool-sandbox':\s*return t\('config\.page\.toolSandbox'\)/, 'CodeView 缺标题映射')
})

test('i18n：zh-CN / en-US 双语文案齐备', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const j = JSON.parse(readSrc('locales/' + loc + '/config.json'))
    assert.ok(j.page && typeof j.page.toolSandbox === 'string' && j.page.toolSandbox.length > 0, `${loc} 缺 config.page.toolSandbox`)
    for (const k of ['sandbox', 'sandboxHint', 'sandboxStdioOnly']) {
      assert.ok(j.mcp && typeof j.mcp[k] === 'string' && j.mcp[k].length > 0, `${loc} 缺 config.mcp.${k}`)
    }
    assert.ok(j.toolSandbox && typeof j.toolSandbox === 'object', `${loc} 缺 toolSandbox 段`)
    for (const k of ['pageHint', 'empty', 'execCore', 'execDesktop', 'execBrowser', 'supportedTools',
      'noTools', 'switchHint', 'stateOn', 'stateOff', 'stateUnset', 'restoreDefault', 'saved',
      'thirdPartyHint', 'serverSummary', 'gotoMcp', 'trustDirWarning', 'trustDirWarningRow', 'gotoTrustDirs']) {
      assert.ok(typeof j.toolSandbox[k] === 'string' && j.toolSandbox[k].length > 0, `${loc} 缺 config.toolSandbox.${k}`)
    }
  }
})

test('图标：新增 shield（页签菜单用）', () => {
  const icons = readSrc('components/icon/icons.js')
  assert.match(icons, /'shield':\s*'<svg/, 'icons.js 缺 shield 图标')
})

test('安全页提示已随 agentbox 落地订正（不再声称"尚未落地"）', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const j = JSON.parse(readSrc('locales/' + loc + '/security.json'))
    const v = j.not_enforced_hint
    assert.ok(typeof v === 'string' && v.includes('agentbox'), `${loc} 提示须说明 agentbox 已承担`)
    assert.doesNotMatch(v, /尚未落地|not implemented|not enforced yet/, `${loc} 提示不得再声称未落地`)
  }
})

