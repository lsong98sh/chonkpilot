/**
 * 用户视角缺陷「批 2」设置面前端单测（2026-09-20）：
 *   ① 路径键「需重启生效」标注（usr 路径键需重启；prj 路径热生效）——对照口径 = 既有 logLevel「即时生效」
 *   ② tool_sandbox 开启但信任目录为空 → 显著内联预警 + 直达可读/可写目录 + 配置后即时消失
 *   ③ timeout_sec / max_concurrency 前置校验（非法值不写库 + 明确错误；对齐后端 Atoi+n>0 口径）
 *   ④ 设置页加载/保存失败必须可见（不再仅 console.*）
 *   ⑤ 保存反馈统一（即时生效/需重启）+ dirty 标记（仅显示，不改保存时机、不拦截）
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯逻辑直调（settingsFeedback / settingsValidation / sandboxTrust）
 * + 后端语义核实（读 Go 源码断言回落条件）+ `*.vue` 源码守卫（与 configSurface 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  APPLY_INSTANT, APPLY_RESTART, savedKey, savedText,
  errorReason, saveFailedText, loadFailedText,
} from '../src/utils/settingsFeedback.js'
import { validatePositiveInt, positiveIntErrorText } from '../src/utils/settingsValidation.js'
import { countTrustDirs, needsTrustDirsWarning } from '../src/utils/sandboxTrust.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoRoot = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readRepo = (rel) => readFileSync(join(repoRoot, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

/** 极简 i18n 替身：从真实 locales JSON 取键 + `{name}` 插值（与 uxBatch1 同法） */
function mkI18n(messages) {
  const dig = (key) => key.split('.').reduce((o, p) => (o == null ? undefined : o[p]), messages)
  return {
    te: (key) => dig(key) !== undefined,
    t: (key, named) => {
      const v = dig(key)
      if (typeof v !== 'string') return key
      if (!named) return v
      let out = v
      for (const [k, val] of Object.entries(named)) out = out.split('{' + k + '}').join(String(val))
      return out
    },
  }
}
const feedbackI18n = (loc) => mkI18n({ config: readLocale(loc, 'config.json') })

// ═══════════════════════════════════════════════════════════════
// ① 路径键「需重启生效」标注
// ═══════════════════════════════════════════════════════════════
test('① savedText：需重启 / 即时生效 的口径（与既有 logLevel「即时生效」范式一致）', () => {
  assert.equal(savedKey(APPLY_RESTART), 'config.feedback.savedRestart')
  assert.equal(savedKey(APPLY_INSTANT), 'config.feedback.savedInstant')
  assert.equal(savedKey(undefined), 'config.feedback.saved')

  const zh = feedbackI18n('zh-CN')
  assert.match(savedText(zh.t, APPLY_RESTART), /重启/, 'zh 需重启文案须点明重启')
  assert.match(savedText(zh.t, APPLY_INSTANT), /即时生效/, 'zh 即时生效文案')
  const en = feedbackI18n('en-US')
  assert.match(savedText(en.t, APPLY_RESTART), /restart/i, 'en 需重启文案')
  assert.match(savedText(en.t, APPLY_INSTANT), /immediately/i, 'en 即时生效文案')

  // 对照口径：既有 logLevel 即时生效提示仍在（未被本批改动破坏）
  assert.match(readLocale('zh-CN', 'projectConfig.json').log_saved, /即时生效/)
  assert.match(read('views/settings/LogConfig.vue'), /message\.success\(t\('projectConfig\.log_saved'\)\)/)
})

test('① SettingsPathsPage：usr 保存反馈标注「需重启」+ 页内提示；显式保存（页头右上角【保存】）', () => {
  const src = read('views/config/SettingsPathsPage.vue')
  const su = src.match(/async function saveUserTab\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(su, '未找到 saveUserTab')
  assert.match(su[0], /savedText\(t, APPLY_RESTART\)/, 'usr 路径保存成功须标注「需重启」')
  assert.doesNotMatch(su[0], /savedText\(t, APPLY_INSTANT\)/, 'usr 路径不得误标即时生效')

  const sp = src.match(/async function saveProjectTab\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(sp, '未找到 saveProjectTab')
  assert.match(sp[0], /savedText\(t, APPLY_INSTANT\)/, 'prj 路径为运行期热生效（即时）')

  assert.match(src, /config\.page\.pathsUserRestartHint/, '须有页内「需重启」提示（常驻）')
  // 2026-10-06 统一口径：表单型页面改显式保存（编辑只改本地待保存态，点【保存】才落库）
  assert.doesNotMatch(src, /@blur="saveUser/, '不再失焦即存')
  assert.match(src, /data-paths-save-user/, 'usr 页签须有【保存】按钮')
  assert.match(src, /data-paths-save-project/, 'prj 页签须有【保存】按钮')
  assert.match(src, /:disabled="!userDirty"/, 'usr 页签无改动时保存按钮禁用')
  assert.match(src, /:disabled="!prjDirty"/, 'prj 页签无改动时保存按钮禁用')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch')

  // 后端核实：usr 路径键不重跑 loadExecConfig（仅 prj 执行配置热生效）
  const go = readRepo('lib/llm/server/server.go')
  assert.match(go, /var prjExecConfigKeys = map\[string\]bool\{/, '后端须存在 prj 热生效键表')
  assert.match(go, /"javaPath":\s+true/, 'prj 路径键在热生效表内')
  // prj 刷新链路才重跑 loadExecConfig（→ prj 路径即时生效）
  const prjRefresh = go.match(/func \(s \*Server\) onPrjConfigRefresh[\s\S]*?\n\}/)
  assert.ok(prjRefresh, '未找到 onPrjConfigRefresh')
  assert.match(prjRefresh[0], /loadExecConfig\(id\)/, 'prj 刷新须重跑 loadExecConfig（热生效）')
  // usr 刷新链路（gateway_servers.go）只对账 mcps / 工具异步与沙箱，**不重跑** loadExecConfig
  const gw = readRepo('lib/llm/server/gateway_servers.go')
  const usrRefresh = gw.match(/func \(s \*Server\) onUserConfigRefresh[\s\S]*?\n\}/)
  assert.ok(usrRefresh, '未找到 onUserConfigRefresh')
  assert.doesNotMatch(usrRefresh[0], /loadExecConfig/, 'usr 配置刷新不重跑 loadExecConfig（→ usr 路径键需重启）')
})

// ═══════════════════════════════════════════════════════════════
// ② 空信任目录 = 全拒（工具级沙箱）
// ═══════════════════════════════════════════════════════════════
test('② needsTrustDirsWarning / countTrustDirs：判定与「配置后消失」联动', () => {
  assert.equal(countTrustDirs([{ dir: 'C:/a' }, { dir: ' ' }, {}, null]), 1, '仅目录名非空白计入')
  assert.equal(countTrustDirs({ a: { dir: '/x' }, b: { dir: '' } }), 1, '对象 map 亦可')
  assert.equal(countTrustDirs([]), 0)
  assert.equal(countTrustDirs(null), 0)
  assert.equal(countTrustDirs(undefined), 0)

  assert.equal(needsTrustDirsWarning({ sandboxOn: true, trustDirCount: 0 }), true, '开+空 → 预警')
  assert.equal(needsTrustDirsWarning({ sandboxOn: true, trustDirCount: 2 }), false, '配置目录后预警消失')
  assert.equal(needsTrustDirsWarning({ sandboxOn: false, trustDirCount: 0 }), false, '未开启不预警')
  assert.equal(needsTrustDirsWarning({}), false)
  assert.equal(needsTrustDirsWarning(), false)
})

test('② 后端语义核实：工具级空允许集 = 全拒（严格语义）', () => {
  const cfg = readRepo('lib/mcp-server/server/config.go')
  assert.match(cfg, /空允许集 → 执行器侧全拒的严格语义/, 'config.go 须载明空集→全拒语义')
  assert.match(cfg, /return "\[\]" \/\/ 无允许目录 → 空集（执行器侧解释为全拒）/, 'SecurityDirs 为 nil → 下发 "[]"')
  const ab = readRepo('lib/core/agentbox/agentbox.go')
  assert.match(ab, /空允许集 = 全拒/, 'agentbox 须载明空允许集=全拒')
})

test('② useToolSandbox：派生预警 + 读信任目录 + 手动保存（无 watch，写库通道带 dirty 短路）', () => {
  const js = read('composables/useToolSandbox.js')
  assert.match(js, /needsTrustDirsWarning\(/, '须复用统一判定')
  assert.match(js, /const trustWarning = computed\(/, '须以 computed 派生预警（无 watch）')
  assert.match(js, /anySandboxOn = computed\(/, '须派生「已开启沙箱」')
  assert.match(js, /getProjectSecurity\(\)/, '信任目录取既有 prj-security 面')
  assert.match(js, /countTrustDirs\(res\.entries\)/, '按有效条目数判定')
  assert.doesNotMatch(js, /\bwatch(Effect)?\s*\(/, 'composable 禁止 watch')
  // 手动保存：拨动只改本地待保存态（不落库）；无改动短路；落库仍走 usr 配置面
  assert.match(js, /function setSandbox\(category, on\)/, '拨动须改待保存态')
  assert.match(js, /const dirty = computed\(/, '须派生未保存改动')
  assert.match(js, /if \(!dirty\.value\) return false/, '无改动须短路（不落库）')
  assert.match(js, /saveUserConfig\(\{ \[TOOL_SANDBOX_KEY\]: next \}\)/)
  assert.match(js, /resetUserKey\(TOOL_SANDBOX_KEY\)/)
})

test('② SettingsToolSandboxPage：常驻内联预警 + 行内提示 + 直达信任目录 + 订阅即时消失', () => {
  const vue = read('views/config/SettingsToolSandboxPage.vue')
  // 显著内联（非一闪而过的 toast）：role=alert 的常驻块
  assert.match(vue, /v-if="trustWarning"[\s\S]{0,200}role="alert"/, '页级须为常驻内联预警块')
  assert.match(vue, /config\.toolSandbox\.trustDirWarning/, '页级警告文案（说明会被拒绝）')
  assert.match(vue, /v-if="trustWarning && row\.on"/, '对应 executor 行须内联提示')
  assert.match(vue, /config\.toolSandbox\.trustDirWarningRow/, '行内提示文案')
  // 直达「可读/可写目录」：既有 previewTabOpen（settings-project 默认落「安全」页签）
  assert.match(vue, /function gotoTrustDirs/, '须有直达入口')
  assert.match(vue, /kind: 'settings-project'/, '直达项目安全（信任目录）页')
  assert.match(vue, /config\.toolSandbox\.gotoTrustDirs/)
  // 联动：订阅既有配置刷新事件（无 watch）→ 配置后警告即时消失
  assert.match(vue, /onDataRefresh\('prj-security', loadTrustDirs\)/, '须订阅 data-prj-security-refresh')
  assert.match(vue, /onMounted\(\(\) => \{[\s\S]*?reload\(\)/, '须首次加载（否则警告无从判定）')
  assert.doesNotMatch(vue, /\bwatch\(/, '禁止 watch')
  // 拨动不阻断：仅【保存】按钮按 dirty 禁用，开关不因空目录被禁用
  assert.match(vue, /:disabled="!dirty"/, '保存按钮须按 dirty 禁用')
})

test('② 空目录预警文案（zh/en）明确「该工具所有文件操作被拒绝」', () => {
  const zh = readLocale('zh-CN', 'config.json')
  assert.match(zh.toolSandbox.trustDirWarning, /拒绝/, 'zh 页级文案须说明被拒绝')
  assert.match(zh.toolSandbox.trustDirWarningRow, /拒绝/, 'zh 行内文案须说明被拒绝')
  assert.match(zh.toolSandbox.gotoTrustDirs, /目录/, 'zh 入口可行动')
  const en = readLocale('en-US', 'config.json')
  assert.match(en.toolSandbox.trustDirWarning, /rejected/i, 'en 页级文案须说明被拒绝')
  assert.match(en.toolSandbox.trustDirWarningRow, /rejected/i, 'en 行内文案须说明被拒绝')
})

// ═══════════════════════════════════════════════════════════════
// ③ timeout_sec / max_concurrency 校验
// ═══════════════════════════════════════════════════════════════
test('③ validatePositiveInt：对齐后端 Atoi 成功且 n>0 的口径', () => {
  for (const v of ['1', '300', '16', ' 5 ', '+7', '007']) {
    const r = validatePositiveInt(v)
    assert.equal(r.ok, true, `${JSON.stringify(v)} 应通过`)
    assert.ok(Number.isInteger(r.value) && r.value > 0, `${JSON.stringify(v)} 值须为正整数`)
  }
  for (const v of ['0', '-5', '0.5', 'abc', '1e3', '15s', '', '  ', null, undefined]) {
    assert.equal(validatePositiveInt(v).ok, false, `${JSON.stringify(v)} 应拒绝`)
  }
  assert.equal(validatePositiveInt('abc').reason, 'notInteger')
  assert.equal(validatePositiveInt('1.5').reason, 'notInteger')
  assert.equal(validatePositiveInt('0').reason, 'notPositive')
  assert.equal(validatePositiveInt('-5').reason, 'notPositive')
  assert.equal(validatePositiveInt('').reason, 'empty')

  // 后端核实：仅 strconv.Atoi 成功且 n > 0 才生效（否则静默回落默认 300/16）
  const go = readRepo('lib/llm/server/server.go')
  assert.match(go, /strconv\.Atoi\(v\); err == nil && n > 0/, '后端生效条件 = Atoi 成功且 n>0')
  const defCfg = readRepo('lib/mcp-server/server/config.go')
  assert.match(defCfg, /TimeoutSec/, '后端默认值定义在 DefaultConfig')
})

test('③ 非法值不写库 + 明确错误；合法值保存成功（保存时机不变）', () => {
  const zh = feedbackI18n('zh-CN')
  assert.match(positiveIntErrorText(zh.t, 'notInteger'), /整数/)
  assert.match(positiveIntErrorText(zh.t, 'empty'), /整数/)

  const src = read('views/config/SettingsParamsPage.vue')
  assert.match(src, /validatePositiveInt\(/, '须前置校验')
  assert.equal((src.match(/:type="f\.int \? 'number' : 'text'"/g) || []).length, 1, '数值项须为数字输入')
  assert.match(src, /:error="!!prjErrors\[f\.key\]"/, '非法值须内联报错')

  const sp = src.match(/async function commitProject\(f\)\s*\{[\s\S]*?\n\}/)
  assert.ok(sp, '未找到 commitProject')
  const invalidIdx = sp[0].indexOf('if (!r.ok)')
  const retIdx = sp[0].indexOf('return', invalidIdx)
  const writeIdx = sp[0].indexOf('await setConfig(key, String(r.value))')
  assert.ok(invalidIdx >= 0, '须有非法值分支')
  assert.ok(retIdx > invalidIdx && retIdx < writeIdx, '非法值须在校验处 return（不写库）')
  assert.match(sp[0], /message\.error\(text\)/, '非法值须给明确错误')
  assert.match(src, /message\.success\(savedText\(t, APPLY_INSTANT\)\)/, '合法值保存后成功反馈')
  // 2026-10-06 统一口径：表单型页面改显式保存（点【保存】才落库）
  assert.doesNotMatch(src, /@blur="saveProject/, '不再失焦即存')
  assert.match(src, /data-params-save-project/, 'prj 页签须有【保存】按钮')

  // 数值项集合 = timeout_sec / max_concurrency（skip_dirs 保持文本）
  assert.match(src, /key: 'timeout_sec'[\s\S]{0,120}int: true/)
  assert.match(src, /key: 'max_concurrency'[\s\S]{0,120}int: true/)
  assert.match(src, /key: 'skip_dirs'[\s\S]{0,120}int: false/)
})

// ═══════════════════════════════════════════════════════════════
// ④ 静默失败 → 可见
// ═══════════════════════════════════════════════════════════════
test('④ 可见失败文案：含项目（哪一项）+ 原因摘要；无原因给兜底', () => {
  const zh = feedbackI18n('zh-CN')
  const s = loadFailedText(zh.t, '上下文管理', new Error('backend unreachable'))
  assert.match(s, /上下文管理/, '须指明哪一项')
  assert.match(s, /backend unreachable/, '须含原因摘要')
  assert.equal(loadFailedText(zh.t, 'X', undefined), zh.t('config.feedback.loadFailedUnknown', { item: 'X' }))
  assert.match(saveFailedText(zh.t, { code: 'E_PERM' }), /E_PERM/, '无 message 时退 code')
  assert.match(saveFailedText(zh.t, undefined), /原因未知/)

  assert.equal(errorReason('boom'), 'boom')
  assert.equal(errorReason({ message: 'm', code: 'c' }), 'm')
  assert.equal(errorReason({ code: 'c' }), 'c')
  assert.equal(errorReason(null), '')
  assert.equal(errorReason({}), '')
})

test('④ 设置页加载/保存失败不再静默（逐文件清零旧 console 文案）', () => {
  const checks = [
    {
      file: 'views/settings/ContextConfig.vue',
      bad: [/\[ContextConfig\] getAllConfig error/, /\[ContextConfig\] data-memory-list error/,
        /\[ContextConfig\] getPrompt summary_prompt error/, /\[ContextConfig\] setConfig error/],
    },
    { file: 'views/settings/HistoryConfig.vue', bad: [/\[HistoryConfig\] loadConfig error/, /\[HistoryConfig\] Failed to save/] },
    { file: 'views/settings/VftsConfig.vue', bad: [/\[VftsConfig\] loadConfig error/, /\[VftsConfig\] Failed to save/] },
    { file: 'views/settings/CodegraphConfig.vue', bad: [/\[CodegraphConfig\] loadConfig error/, /\[CodegraphConfig\] Failed to save/] },
    { file: 'views/config/SettingsPathsPage.vue', bad: [/\[SettingsPaths\] load user failed/, /\[SettingsPaths\] load project failed/] },
    { file: 'views/config/SettingsParamsPage.vue', bad: [/\[SettingsParams\] load user failed/, /\[SettingsParams\] load project failed/] },
    { file: 'views/settings/SecurityConfig.vue', bad: [/catch \(_\) \{ entries\.value = \[\] \}/] },
    { file: 'views/settings/LogConfig.vue', bad: [/\[LogConfig\] loadConfig error/] },
  ]
  for (const c of checks) {
    const src = read(c.file)
    assert.match(src, /message\.error\(/, `${c.file} 须有可见失败提示`)
    assert.match(src, /loadFailedText\(/, `${c.file} 加载失败须用统一文案`)
    for (const re of c.bad) assert.doesNotMatch(src, re, `${c.file} 残留静默失败：${re}`)
  }
  // 成功路径不得被改成失败（成功提示仍在）
  assert.match(read('views/settings/ContextConfig.vue'), /message\.success\(/)
  assert.match(read('views/settings/VftsConfig.vue'), /message\.success\(/)
  assert.match(read('views/settings/CodegraphConfig.vue'), /message\.success\(/)
})

// ═══════════════════════════════════════════════════════════════
// ⑤ 反馈统一 + dirty（保存时机不变）
// ═══════════════════════════════════════════════════════════════
test('⑤ 反馈统一：即时生效 / 需重启 标注 + 保存中防重', () => {
  // 即时生效（prj 执行配置 / 引擎 / history 实时同步 / agentbox 目录）
  for (const f of [
    'views/config/SettingsParamsPage.vue',
    'views/settings/HistoryConfig.vue',
    'views/settings/SecurityConfig.vue',
    'views/settings/VftsConfig.vue',
    'views/settings/CodegraphConfig.vue',
  ]) {
    assert.match(read(f), /savedText\(t, APPLY_INSTANT\)/, `${f} 须用统一即时生效反馈`)
  }
  // 需重启（usr 路径键）
  assert.match(read('views/config/SettingsPathsPage.vue'), /savedText\(t, APPLY_RESTART\)/)
  // 失败反馈统一走 saveFailedText
  for (const f of [
    'views/config/SettingsPathsPage.vue',
    'views/config/SettingsParamsPage.vue',
    'views/settings/ContextConfig.vue',
    'views/settings/HistoryConfig.vue',
    'views/settings/SecurityConfig.vue',
    'views/settings/VftsConfig.vue',
    'views/settings/CodegraphConfig.vue',
  ]) {
    assert.match(read(f), /saveFailedText\(/, `${f} 失败反馈须统一`)
  }
  // 保存中禁用重复提交（按钮页）
  const ctx = read('views/settings/ContextConfig.vue')
  assert.match(ctx, /:loading="saving"/, '保存按钮须 loading 防重复提交')
  assert.match(ctx, /if \(saving\.value\) return/, '保存中须短路')
})

test('⑤ dirty 标记：显示用（无 watch、无离开拦截），保存时机不变', () => {
  const withMark = [
    'views/config/SettingsPathsPage.vue',
    'views/config/SettingsParamsPage.vue',
    'views/settings/ContextConfig.vue',
    'views/settings/HistoryConfig.vue',
    'views/settings/SecurityConfig.vue',
  ]
  for (const f of withMark) {
    const src = read(f)
    assert.match(src, /useUnsavedMark\(\)/, `${f} 须用 useUnsavedMark composable`)
    // 参数 / 路径页为「按页签各自 dirty」（userDirty / prjDirty），其余为单一 dirty
    assert.match(src, /v-if="(user|prj)?[Dd]irty"/, `${f} 须渲染 dirty 标记`)
    assert.match(src, /config\.feedback\.unsaved/, `${f} 须用统一「未保存」文案`)
    assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, `${f} 不得用 watch`)
    assert.doesNotMatch(src, /beforeunload|onBeforeRouteLeave/, `${f} 不做离开拦截`)
  }
  // 按钮保存页：dirty 由 isPristine 派生（不引入 watch）
  for (const f of ['views/settings/VftsConfig.vue', 'views/settings/CodegraphConfig.vue']) {
    const src = read(f)
    assert.match(src, /const unsaved = computed\(/, `${f} 须有未保存派生`)
    assert.match(src, /v-if="unsaved"/, `${f} 须渲染 dirty 标记`)
    assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, `${f} 不得用 watch`)
    assert.doesNotMatch(src, /beforeunload|onBeforeRouteLeave/, `${f} 不做离开拦截`)
  }
  const comp = read('composables/useUnsavedMark.js')
  assert.match(comp, /export function useUnsavedMark\(\)/, 'composable 导出形态')
  assert.doesNotMatch(comp, /\bwatch(Effect)?\s*\(/, 'composable 禁止 watch')
})

test('⑤ 保存时机（列表/对话框页即改即存；参数 / 路径页 2026-10-06 改显式保存）', () => {
  // 2026-10-06 统一口径：表单型页面（参数 / 路径）改「编辑只改本地态 + 页头右上角【保存】」
  for (const f of ['views/config/SettingsPathsPage.vue', 'views/config/SettingsParamsPage.vue']) {
    const src = read(f)
    assert.doesNotMatch(src, /@blur="saveUser/, `${f} 不再失焦即存`)
    assert.match(src, /:disabled="!(user|prj)Dirty"/, `${f} 保存按钮须按 dirty 禁用`)
    assert.match(src, /:loading="saving(User|Prj)"/, `${f} 保存按钮须 loading 防重复提交`)
  }
  assert.match(read('views/settings/LogConfig.vue'), /@update:model-value="onLevelChange"/, '日志页仍选择即存')
  assert.match(read('views/settings/HistoryConfig.vue'), /@update:model-value="handleChange"/, 'history 仍开关即存')
  // 安全页：编辑 / 勾选 / 增删只改本地态，点【保存】才落库；无改动时保存按钮禁用（改手动保存）
  const sec = read('views/settings/SecurityConfig.vue')
  assert.match(sec, /data-security-save/, '安全页须有【保存】按钮')
  assert.match(sec, /:disabled="!dirty"/, '安全页无改动时保存按钮须禁用')
  assert.match(sec, /@click="save"/, '安全页点【保存】才落库')
  assert.doesNotMatch(sec, /@change="save"/, '安全页编辑不再即时落库（改手动保存）')
  assert.match(read('views/settings/VftsConfig.vue'), /:loading="saving" @click="handleIndexSave"/, 'vfts 仍按钮保存')
  assert.match(read('views/settings/CodegraphConfig.vue'), /:loading="saving" @click="handleIndexSave"/, 'codegraph 仍按钮保存')
  assert.match(read('views/settings/ContextConfig.vue'), /v-mq:\[EventNames\.contextSave\]\.click/, '上下文仍保存按钮')
})

// ═══════════════════════════════════════════════════════════════
// i18n 双语齐备
// ═══════════════════════════════════════════════════════════════
test('i18n：批 2 新增键 zh-CN / en-US 齐备（含插值占位符）', () => {
  const feedbackKeys = ['saved', 'savedInstant', 'savedRestart', 'unsaved', 'saveFailed',
    'saveFailedUnknown', 'loadFailed', 'loadFailedUnknown', 'requiredInt', 'invalidPositiveInt']
  const pageKeys = ['pathsUserRestartHint', 'pathsProjectInstantHint']
  const sandboxKeys = ['trustDirWarning', 'trustDirWarningRow', 'gotoTrustDirs']
  for (const loc of LOCALES) {
    const cfg = readLocale(loc, 'config.json')
    assert.ok(cfg.feedback && typeof cfg.feedback === 'object', `${loc} 缺 config.feedback`)
    for (const k of feedbackKeys) {
      assert.ok(typeof cfg.feedback[k] === 'string' && cfg.feedback[k].trim().length > 0, `${loc} 缺 config.feedback.${k}`)
    }
    for (const k of pageKeys) {
      assert.ok(typeof cfg.page[k] === 'string' && cfg.page[k].trim().length > 0, `${loc} 缺 config.page.${k}`)
    }
    for (const k of sandboxKeys) {
      assert.ok(typeof cfg.toolSandbox[k] === 'string' && cfg.toolSandbox[k].trim().length > 0, `${loc} 缺 config.toolSandbox.${k}`)
    }
    assert.ok(cfg.feedback.saveFailed.includes('{error}'), `${loc} feedback.saveFailed 须含 {error}`)
    assert.ok(cfg.feedback.loadFailed.includes('{item}') && cfg.feedback.loadFailed.includes('{error}'),
      `${loc} feedback.loadFailed 须含 {item}/{error}`)
  }
})
