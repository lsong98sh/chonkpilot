/**
 * agentbox 沙箱「配置面」接线守卫（[42 §2 (104)/(109)]）
 *
 * 覆盖三条硬要求（2026-11-14 起沙箱开关来源收敛到「设置 → 工具沙箱」页 usr `tool_sandbox`）：
 *   ① MCP 编辑弹窗**不再提供沙箱 Switch**、保存也不再写 usr `mcps[].sandbox`（旧字段仅由
 *      gateway 读取时迁移）；原位只留「请到工具沙箱页按服务器名配置」提示；
 *   ② 页签「工具沙箱配置」→ 按执行体（executor exe 基名 / MCP 条目名）写 usr `tool_sandbox`
 *      （未设 = 不隔离）；拨动只改本地待保存态，点【保存】才落库，无改动时保存按钮禁用；
 *   ③ 对话框仅保留「按实例隔离」（isolate）三态开关；http/sse 的 stdio-only 判定已随沙箱开关移除。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 用**源码守卫**
 * 锁定接线契约，防回归到「改了 UI 但没写对配置键」或「沙箱开关又被塞回对话框」。
 *
 * 注：本文件放在 src 之外（frontend/test/），以免 node 内置模块导入进入 tsconfig 对 src 下
 * js/vue 的类型检查范围。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { resolveSrc } from './support/feSource.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const readSrc = (rel) => readFileSync(resolveSrc(rel), 'utf8')

test('MCP 编辑弹窗：沙箱 Switch 已移除，不再写 mcps[].sandbox（旧字段仅读取迁移）', () => {
  const vue = readSrc('views/config/EditMCPDialog.vue')
  // 三态开关整体退场（含显式标记 / 值 / computed 开关）
  assert.doesNotMatch(vue, /sandboxToggle|sandboxExplicit|sandboxValue/, '对话框不应再有 sandbox 三态开关')
  // 保存逻辑不得再写 / 删该键（旧配置值原样透传，供 gateway 读取时迁移）
  assert.doesNotMatch(vue, /localData\.sandbox\s*=/, 'handleSave 不应再写 localData.sandbox')
  assert.doesNotMatch(vue, /else delete localData\.sandbox/, 'handleSave 不应再删该键')
  // 原位提示：开关入口在「设置 → 工具沙箱」按服务器名配置（附说明 tooltip）
  assert.match(vue, /config\.mcp\.sandboxMovedHint/, '缺「沙箱开关已移至工具沙箱页」提示')
  assert.match(vue, /config\.mcp\.sandboxMovedTip/, '缺说明 tooltip（i18n）')
  // 落盘通道（2026-10-01 起）= **mcp 域四级文件化**（由列表页 saveOne 承担，不再整表替换 usr mcps）
  assert.match(readSrc('views/config/SettingsMCPPage.vue'),
    /saveMcpServer\(/, '落盘通道须为 mcp 域（四级文件化配置）')
})

test('MCP 编辑弹窗：仅保留「按实例隔离」三态；沙箱可操作性判定已移除', () => {
  const vue = readSrc('views/config/EditMCPDialog.vue')
  assert.doesNotMatch(vue, /sandboxTransportName|sandboxApplicable/, '沙箱传输判定应随开关一并移除')
  assert.doesNotMatch(vue, /sandboxStdioOnly/, '对话框不应再引用 stdio-only 禁用原因')
  // 按实例隔离三态保留（未拨动 = 删键 → gateway 按 transport 推断）
  assert.match(vue, /<Switch v-model="isolateToggle" \/>/, '缺「按实例隔离」三态 Switch')
  assert.match(vue, /if \(isolateExplicit\.value\) localData\.isolate = !!isolateValue\.value/,
    '显式拨动须写 isolate')
  assert.match(vue, /else delete localData\.isolate/, '未拨动须删键')
  // 提示与 isolate 同排两列（移除开关后行仍整齐）
  const rt = vue.slice(vue.indexOf('<template #runtime>'), vue.indexOf('<template #tools>'))
  assert.ok(rt.indexOf('config.mcp.isolate') < rt.indexOf('config.mcp.sandboxMovedHint'),
    '沙箱提示须紧随「按实例隔离」')
})

test('MCP 列表页：沙箱列已移除（编辑入口唯一 = 对话框运行信息页签）', () => {
  const vue = readSrc('views/config/SettingsMCPPage.vue')
  assert.doesNotMatch(vue, /prop:\s*'sandbox'/, '列表页不应再有 sandbox 列')
  assert.doesNotMatch(vue, /onSandboxToggle/, '列表页不应再有行内拨动')
  // 空信任目录语义说明保留（跨页提示）
  assert.match(vue, /config\.mcp\.sandboxEmptyHint/, '须保留 server 级语义说明')
})

test('新页签：useToolSandbox composable 按「被 spawn 的执行体」写 usr tool_sandbox，手动保存', () => {
  const js = readSrc('composables/useToolSandbox.js')
  assert.match(js, /export const TOOL_SANDBOX_KEY = 'tool_sandbox'/, '键名须为 tool_sandbox')
  // 单位 = 内置 executor（exe 基名）+ 外部 MCP；executors 子表按 exe 基名，mcps 子表按 MCP 名
  assert.match(js, /export const EXECUTOR_UNITS = \[/, '缺内置 executor 单位清单')
  // 2026-10-31（D-72）：内置 executor 全部退场（`src/lib/mcp-tools` 删除）→ 清单为空。
  assert.match(js, /export const EXECUTOR_UNITS = \[\]/, '内置 executor 应已全部退场（空清单）')
  assert.match(js, /listMcpServers\(\)/, '外部 MCP 清单须取既有 data-mcp-list')
  assert.match(js, /saveUserConfig\(\{\s*\[TOOL_SANDBOX_KEY\]:\s*next\s*\}\)/, '须经 usr 配置面写该键（结构化 executors/mcps）')
  assert.match(js, /mq\.emit\(MsgClientTopics\.toolsList/, '工具清单须取既有能力面 tools-list')
  // 过滤键名不得臆造：读的是 _meta.category / _meta.server
  assert.match(js, /meta\.category/, '须读 _meta.category')
  assert.match(js, /meta\.server/, '须读 _meta.server（外部 MCP 归组 / 展示剥前缀用）')
  // 三态 + 手动保存：拨动只改待保存态，无改动不写；全部未设置 → 删整键
  assert.match(js, /const dirty = computed\(/, '须派生「未保存改动」')
  assert.match(js, /if \(!dirty\.value\) return false/, '无改动须短路（不落库）')
  assert.match(js, /resetUserKey\(TOOL_SANDBOX_KEY\)/, '全部未设置须删整键')
  assert.match(js, /parseToolSandboxMap/, '须经 parseToolSandboxMap 解析')
})

test('新页签：页面用 composable + Switch，手动保存按钮，且不出现 watch（项目规范）', () => {
  const vue = readSrc('views/config/SettingsToolSandboxPage.vue')
  assert.match(vue, /useToolSandbox\(\)/, '页面须用 useToolSandbox composable')
  assert.match(vue, /<Switch[\s\S]*?@update:model-value="\(v\) => onToggle\(row, v\)"/, '执行体行须有隔离 Switch')
  // 按执行体单位渲染行（内置 exe / 外部 MCP）+ 只读工具清单
  assert.match(vue, /v-for="row in rows"[\s\S]*?:data-exec="row\.key"/, '须按执行体单位渲染行')
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
  // 设置菜单由自由布局默认顶栏**声明式**定义（`layout/persist.js`）；原 `utils/configMenu.js` 已删除。
  const persist = readSrc('layout/persist.js')
  assert.match(persist, /'settings-tool-sandbox'/, '默认布局须有「工具沙箱」设置项')
  const codeview = readSrc('views/codeview/CodeView.vue')
  assert.match(codeview, /'settings-tool-sandbox':\s*defineAsyncComponent/, 'CodeView 缺页签组件注册')
  assert.match(codeview, /case 'settings-tool-sandbox':\s*return t\('config\.page\.toolSandbox'\)/, 'CodeView 缺标题映射')
})

test('i18n：zh-CN / en-US 双语文案齐备', () => {
  for (const loc of ['zh-CN', 'en-US']) {
    const j = JSON.parse(readSrc('locales/' + loc + '/config.json'))
    assert.ok(j.page && typeof j.page.toolSandbox === 'string' && j.page.toolSandbox.length > 0, `${loc} 缺 config.page.toolSandbox`)
    for (const k of ['sandbox', 'sandboxHint', 'sandboxStdioOnly', 'sandboxMovedHint', 'sandboxMovedTip']) {
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

