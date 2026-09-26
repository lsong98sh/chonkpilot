/**
 * agentbox 沙箱「配置面」接线守卫（[42 §2 (104)/(109)]）
 *
 * 覆盖三条硬要求：
 *   ① mcp-server 配置页「是否隔离」开关 → 写 usr `mcps[].sandbox`（三态，未设 = 不隔离）；
 *   ② 新页签「扫描到的工具」→ 写 usr `tool_sandbox`（未设 = 不隔离）；
 *   ③ **仅 stdio 可操作**：http/sse（及缺省 url 推断为 http）行控件禁用 + i18n 原因。
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

test('MCP 配置页：sandbox 列 + Switch + 拨动写 mcps[].sandbox（三态）', () => {
  const vue = readSrc('views/config/SettingsMCPPage.vue')
  // 列：prop = 'sandbox'（供 Table 具名插槽）
  assert.match(vue, /\{\s*label:\s*t\('config\.mcp\.sandbox'\),\s*prop:\s*'sandbox'/, '缺 sandbox 列')
  // 行内 Switch，绑定 sandbox === true（未设置/关均为 off）
  assert.match(vue, /<Switch[\s\S]*?:model-value="mcpServers\[index\]\.sandbox === true"/, 'Switch 未绑定 sandbox 三态')
  // 拨动写库：只写本 server 的 sandbox 键，仍走 saveUserConfig({ mcpServers })
  const m = vue.match(/async function onSandboxToggle\s*\([\s\S]*?\n\}/)
  assert.ok(m, '未找到 onSandboxToggle')
  assert.match(m[0], /s\.sandbox\s*=\s*!!v/, 'onSandboxToggle 须写 s.sandbox = true/false')
  assert.match(m[0], /await saveNow\(\)/, 'onSandboxToggle 须落盘（saveNow → saveUserConfig({mcpServers})）')
  assert.match(vue, /saveUserConfig\(\{\s*mcpServers:\s*mcpServers\.value\s*\}\)/, '落盘通道须仍是 usr mcps')
  // 未设置 = 缺键（不写 null/false 占位）——三态语义
  assert.match(vue, /sandboxUnset/, '缺「未设置」态展示')
})

test('MCP 配置页：仅 stdio 可操作，http/sse 禁用并给 i18n 原因', () => {
  const vue = readSrc('views/config/SettingsMCPPage.vue')
  // 传输归一：显式 transport 优先；缺省 url → http，否则 stdio（与 gateway TransportName 一致）
  const tn = vue.match(/function transportName\s*\([\s\S]*?\n\}/)
  assert.ok(tn, '未找到 transportName')
  assert.match(tn[0], /return\s+tp\.toLowerCase\(\)/, '显式 transport 须归一')
  assert.match(tn[0], /\?\s*'http'\s*:\s*'stdio'/, '缺省须按 url 推断 http/stdio')
  // 可操作判据 = stdio
  const se = vue.match(/function sandboxEditable\s*\([\s\S]*?\n\}/)
  assert.ok(se, '未找到 sandboxEditable')
  assert.match(se[0], /transportName\(s\)\s*===\s*'stdio'/, 'sandboxEditable 须仅 stdio 为真')
  // 控件禁用 + 原因提示
  assert.match(vue, /:disabled="!sandboxEditable\(mcpServers\[index\]\)"/, 'http/sse 行须禁用 Switch')
  assert.match(vue, /$t\('config\.mcp\.sandboxStdioOnly'\)|t\('config\.mcp\.sandboxStdioOnly'\)/, '须给禁用原因（i18n）')
})

test('新页签：useToolSandbox composable 写 usr tool_sandbox，且只列 runtime 工具', () => {
  const js = readSrc('composables/useToolSandbox.js')
  assert.match(js, /export const TOOL_SANDBOX_KEY = 'tool_sandbox'/, '键名须为 tool_sandbox')
  assert.match(js, /export const RUNTIME_CATEGORIES = \['core', 'desktop', 'browser'\]/, 'runtime 类别须为 core/desktop/browser')
  assert.match(js, /if \(!RUNTIME_CATEGORIES\.includes\(category\)\) continue/, '须按 _meta.category 过滤')
  assert.match(js, /saveUserConfig\(\{\s*\[TOOL_SANDBOX_KEY\]:\s*userMap\.value\s*\}\)/, '须经 usr 配置面写该键')
  assert.match(js, /mq\.emit\('tools-list'/, '工具清单须取既有能力面 tools-list')
  // 过滤键名不得臆造：读的是 _meta.category / _meta.server
  assert.match(js, /meta\.category/, '须读 _meta.category')
  assert.match(js, /meta\.server/, '须读 _meta.server 作为分组')
  // 未设置 = 缺键（恢复默认删该键项，最后一项删整键）
  assert.match(js, /resetUserKey\(TOOL_SANDBOX_KEY\)/, '最后一项须删整键')
})

test('新页签：页面用 composable + Switch，且不出现 watch（项目规范）', () => {
  const vue = readSrc('views/config/SettingsToolSandboxPage.vue')
  assert.match(vue, /useToolSandbox\(\)/, '页面须用 useToolSandbox composable')
  assert.match(vue, /<Switch[\s\S]*?@update:model-value="\(v\) => onToggle\(row, v\)"/, '工具行须有隔离 Switch')
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
    for (const k of ['sandbox', 'sandboxOn', 'sandboxOff', 'sandboxUnset', 'sandboxUnsupported', 'sandboxHint', 'sandboxStdioOnly']) {
      assert.ok(j.mcp && typeof j.mcp[k] === 'string' && j.mcp[k].length > 0, `${loc} 缺 config.mcp.${k}`)
    }
    assert.ok(j.toolSandbox && typeof j.toolSandbox === 'object', `${loc} 缺 toolSandbox 段`)
    for (const k of ['pageHint', 'empty', 'switchHint', 'stateOn', 'stateOff', 'stateUnset', 'restoreDefault']) {
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

