/**
 * 用户视角缺陷「批 2」收尾（2026-09-20）·A–D 四小项前端单测：
 *   A `ChatPanel.vue` 三处硬编码英文 → 既有 i18n 键（task_running / task_failed_suffix / llm_retry）
 *   B 残留静默加载（SettingsMCP/LLM/ToolAsync 共 5 处）→ 用户可见失败提示（与批 2 ④ 同口径）
 *   C usr 三项（responseTimeout/streamTimeout/retryCount）前置校验
 *     —— 对齐后端 `loadLLMRuntimeConfig`：retryCount 用 `n >= 0`（显式 0 合法），其余 `n > 0`
 *   D server 级 `mcps[].sandbox` 空信任目录 = **不施加隔离**（非「全拒」）→ 补说明文案，不加警告
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯函数直调（settingsValidation）+ i18n 实键校验 + `*.vue` 源码守卫 + Go 源码语义核实。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  validatePositiveInt, positiveIntErrorText,
  validateNonNegativeInt, nonNegativeIntErrorText,
} from '../src/utils/settingsValidation.js'
import { loadFailedText } from '../src/utils/settingsFeedback.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoRoot = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readRepo = (rel) => readFileSync(join(repoRoot, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

/** 极简 i18n 替身：从真实 locales JSON 取键 + `{name}` 插值（与 uxBatch2Settings 同法） */
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
const chatI18n = (loc) => mkI18n({ chat: readLocale(loc, 'chat.json') })
const cfgI18n = (loc) => mkI18n({ config: readLocale(loc, 'config.json') })

/** 抽取 `{name}` 占位符名（顺序去重） */
const placeholders = (s) => [...String(s).matchAll(/\{(\w+)\}/g)].map((m) => m[1])

// ═══════════════════════════════════════════════════════════════
// A ChatPanel 三处硬编码英文 → i18n
// ═══════════════════════════════════════════════════════════════
test('A·ChatPanel：三处进度/重试文案改用既有 i18n 键，且无裸英文残留', () => {
  const src = read('views/chat/ChatPanel.vue')
  assert.match(src, /import \{ useI18n \} from 'vue-i18n'/, '脚本须用 i18n（与 :652 同源）')
  assert.match(src, /t\('chat\.task_running', \{ done: data\.completed \|\| 0, total: data\.total \|\| '\?' \}\)/,
    '运行任务文案须走 chat.task_running（参数 done/total）')
  assert.match(src, /t\('chat\.task_failed_suffix', \{ failed: data\.failed \}\)/,
    '失败后缀须走 chat.task_failed_suffix（参数 failed）')
  assert.match(src, /t\('chat\.llm_retry', \{ attempt, max: maxRetries, wait: waitSec \}\)/,
    '重试文案须走 chat.llm_retry（参数 attempt/max/wait）')
  // 旧裸英文模板串不得残留
  assert.doesNotMatch(src, /`Running task \$\{/, '不得残留 `Running task ${…}`')
  assert.doesNotMatch(src, /\$\{data\.failed\} failed/, '不得残留 `… failed` 拼接')
  assert.doesNotMatch(src, /`LLM retry \$\{/, '不得残留 `LLM retry ${…}`')
})

test('A·i18n：三键占位符与调用参数一致，且渲染后无残留占位符（zh/en）', () => {
  const expect = { task_running: ['done', 'total'], task_failed_suffix: ['failed'], llm_retry: ['attempt', 'max', 'wait'] }
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    for (const [k, ph] of Object.entries(expect)) {
      assert.deepEqual(placeholders(chat[k]), ph, `${loc} chat.${k} 占位符须为 ${ph.join('/')}`)
    }
    const i18n = chatI18n(loc)
    const running = i18n.t('chat.task_running', { done: 3, total: 10 })
    assert.doesNotMatch(running, /\{\w+\}/, `${loc} task_running 渲染后残留占位符`)
    const failed = running + i18n.t('chat.task_failed_suffix', { failed: 2 })
    assert.doesNotMatch(failed, /\{\w+\}/, `${loc} 组合进度串残留占位符`)
    const retry = i18n.t('chat.llm_retry', { attempt: 1, max: 2, wait: 5 })
    assert.doesNotMatch(retry, /\{\w+\}/, `${loc} llm_retry 渲染后残留占位符`)
    assert.match(retry, /1/, `${loc} llm_retry 须含 attempt`)
  }
  assert.match(chatI18n('zh-CN').t('chat.task_running', { done: 3, total: 10 }), /运行任务/)
  assert.match(chatI18n('en-US').t('chat.task_running', { done: 3, total: 10 }), /Running task/)
})

// ═══════════════════════════════════════════════════════════════
// B 残留静默加载 → 可见
// ═══════════════════════════════════════════════════════════════
const SILENT_SITES = [
  { file: 'views/config/SettingsMCPPage.vue', tag: "console.warn('[SettingsMCP] load mcp servers failed:'" },
  { file: 'views/config/SettingsLLMPage.vue', tag: "console.warn('[SettingsLLM] load user config failed:'" },
  { file: 'views/config/SettingsToolAsyncPage.vue', tag: "console.warn('[SettingsToolAsync] load tools failed:'" },
  { file: 'views/config/SettingsToolAsyncPage.vue', tag: "console.warn('[SettingsToolAsync] load user config failed:'" },
]

test('B·各加载失败点不再只 console：紧随其后必须给用户可见提示（统一文案）', () => {
  for (const s of SILENT_SITES) {
    const src = read(s.file)
    const idx = src.indexOf(s.tag)
    assert.ok(idx >= 0, `${s.file} 未找到调试日志点：${s.tag}`)
    const after = src.slice(idx, idx + 260)
    assert.match(after, /message\.(error|warning)\(loadFailedText\(t, t\('config\.[^']+'\), e\)\)/,
      `${s.file} 加载失败须可见（message.error/warning + loadFailedText）`)
  }
  for (const f of [...new Set(SILENT_SITES.map((s) => s.file))]) {
    // 引入来自统一失败文案模块即可（同模块还可一并引入 savedText/saveFailedText/APPLY_INSTANT 等）
    assert.match(read(f), /import \{[^}]*\bloadFailedText\b[^}]*\} from '\.\.\/\.\.\/utils\/settingsFeedback'/,
      `${f} 须引入统一失败文案`)
  }
})

test('B·可见提示文案：指明哪一项 + 原因摘要；item 标签 i18n 齐备（zh/en）', () => {
  const zh = cfgI18n('zh-CN')
  const s = loadFailedText(zh.t, zh.t('config.page.mcp'), new Error('backend unreachable'))
  assert.match(s, /MCP 配置/, '须指明是哪一项')
  assert.match(s, /backend unreachable/, '须含失败原因摘要')
  assert.equal(loadFailedText(zh.t, 'X', undefined), zh.t('config.feedback.loadFailedUnknown', { item: 'X' }))

  const items = ['page.mcp', 'page.llm', 'page.toolAsync', 'toolAsync.sourceUser']
  for (const loc of LOCALES) {
    const cfg = readLocale(loc, 'config.json')
    const dig = (k) => k.split('.').reduce((o, p) => (o == null ? undefined : o[p]), cfg)
    for (const k of items) {
      assert.ok(typeof dig(k) === 'string' && dig(k).trim().length > 0, `${loc} 缺 config.${k}（失败提示的 item 标签）`)
    }
  }
})

// ═══════════════════════════════════════════════════════════════
// C usr 四项前置校验
// ═══════════════════════════════════════════════════════════════
test('C·后端语义核实：retryCount 用 n>=0（0 合法），其余三项用 n>0', () => {
  const go = readRepo('lib/llm/server/server.go')
  const fn = go.match(/func \(s \*Server\) loadLLMRuntimeConfig[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 loadLLMRuntimeConfig')
  assert.match(fn[0], /configInt\(d, "responseTimeout"\); ok && n > 0/, 'responseTimeout 生效条件 = n>0')
  assert.match(fn[0], /configInt\(d, "streamTimeout"\); ok && n > 0/, 'streamTimeout 生效条件 = n>0')
  assert.match(fn[0], /configInt\(d, "retryCount"\); ok && n >= 0/, 'retryCount 生效条件 = n>=0（显式 0 = 不重试）')
  // persist 侧（config 域实现）系统默认（回落终点）
  // 阶段 4 internal 下沉：config 域实现已由 persist 下沉 internal/config。
  const p = readRepo('lib/data/internal/config/userconfig.go')
  assert.match(p, /"responseTimeout": 120/, 'persist 默认 responseTimeout=120')
  assert.match(p, /"retryCount":\s+2/, 'persist 默认 retryCount=2')
})

test('C·validateNonNegativeInt：0 合法、负数/非整数/空拒绝', () => {
  assert.deepEqual(validateNonNegativeInt('0'), { ok: true, value: 0 }, '0 合法（不重试）')
  assert.equal(validateNonNegativeInt('5').ok, true)
  assert.equal(validateNonNegativeInt(' 7 ').value, 7)
  assert.equal(validateNonNegativeInt('-1').ok, false)
  assert.equal(validateNonNegativeInt('-1').reason, 'negative')
  assert.equal(validateNonNegativeInt('1.5').reason, 'notInteger')
  assert.equal(validateNonNegativeInt('abc').reason, 'notInteger')
  assert.equal(validateNonNegativeInt('').reason, 'empty')
  assert.equal(validateNonNegativeInt(null).reason, 'empty')
  // 正整数口径（其余三项）不受影响
  assert.equal(validatePositiveInt('0').reason, 'notPositive', '正整数口径 0 非法')
  assert.equal(validatePositiveInt('-5').reason, 'notPositive')
  assert.equal(validatePositiveInt('120').ok, true)
})

test('C·错误文案：非负整数项与正整数项文案可区分（zh/en）', () => {
  const zh = cfgI18n('zh-CN')
  assert.match(nonNegativeIntErrorText(zh.t, 'negative'), /不小于 0/)
  assert.match(positiveIntErrorText(zh.t, 'notPositive'), /大于 0/)
  assert.equal(nonNegativeIntErrorText(zh.t, 'notInteger'), zh.t('config.feedback.requiredInt'))
  const en = cfgI18n('en-US')
  assert.match(nonNegativeIntErrorText(en.t, 'negative'), /0 or greater/i)
  assert.match(positiveIntErrorText(en.t, 'notPositive'), /greater than 0/i)
})

test('C·SettingsParamsPage：usr 数值项前置校验（非法不写库 / 清空删键 / 内联报错）', () => {
  const src = read('views/config/SettingsParamsPage.vue')
  // 数值项集合：allowZero 仅 retryCount
  assert.match(src, /key: 'retryCount'[\s\S]{0,120}allowZero: true/, 'retryCount 须标 0 合法')
  for (const k of ['responseTimeout', 'streamTimeout']) {
    assert.doesNotMatch(src, new RegExp(`key: '${k}'[^\\n]*allowZero`), `${k} 不得允许 0（后端口径 n>0）`)
  }

  const su = src.match(/async function commitUser\(f\)\s*\{[\s\S]*?\n\}/)
  assert.ok(su, '未找到 commitUser')
  const body = su[0]
  assert.match(body, /f\.allowZero \? validateNonNegativeInt\(rawStr\) : validatePositiveInt\(rawStr\)/,
    '须按 allowZero 分派两种校验')
  const invIdx = body.indexOf('if (!r.ok)')
  const retIdx = body.indexOf('return', invIdx)
  const writeIdx = body.indexOf('await saveUserConfig({ [key]: r.value })')
  assert.ok(invIdx >= 0, '须有非法值分支')
  assert.ok(retIdx > invIdx && retIdx < writeIdx, '非法值须在校验处 return（不写库）')
  assert.match(body, /message\.error\(text\)/, '非法值须给明确错误')
  assert.match(body, /await resetUserKey\(key\)/, '清空 = 删键回落默认（非错误）')
  assert.match(src, /message\.success\(savedText\(t, APPLY_INSTANT\)\)/, '合法值保存成功反馈')
  // 内联报错 + 显式保存（2026-10-06 统一口径）+ 无 watch
  assert.match(src, /:error="!!userErrors\[f\.key\]"/, 'usr 非法值须内联报错')
  assert.match(src, /v-if="userErrors\[f\.key\]"/, '内联错误文案须可见')
  assert.doesNotMatch(src, /@blur="saveUser/, '不再失焦即存')
  assert.match(src, /data-params-save-user/, 'usr 页签须有【保存】按钮')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect')
  // 上限：后端无约束 → 校验函数不设上限（超大值仍通过）
  assert.equal(validatePositiveInt('999999999').ok, true, '后端无上限 → 前端不设上限')
  assert.equal(validateNonNegativeInt('999999999').ok, true, '后端无上限 → 前端不设上限')
})

// ═══════════════════════════════════════════════════════════════
// D server 级空沙箱目录：只补说明（不是「全拒」）
// ═══════════════════════════════════════════════════════════════
test('D·i18n：server 级「未配置目录 = 不施加隔离」说明齐备（zh/en），与工具级全拒区分', () => {
  for (const loc of LOCALES) {
    const v = readLocale(loc, 'config.json').mcp.sandboxEmptyHint
    assert.ok(typeof v === 'string' && v.trim().length > 0, `${loc} 缺 config.mcp.sandboxEmptyHint`)
  }
  const zh = readLocale('zh-CN', 'config.json').mcp.sandboxEmptyHint
  assert.match(zh, /不施加隔离/, 'zh 须点明「不施加隔离」')
  assert.match(zh, /不是/, 'zh 须显式否定「全拒」')
  const en = readLocale('en-US', 'config.json').mcp.sandboxEmptyHint
  assert.match(en, /no isolation/i, 'en 须点明 no isolation')
  assert.match(en, /NOT/i, 'en 须显式否定 all-reject')
})

test('D·SettingsMCPPage：常驻可见说明（非 tooltip），且不加「全拒」警告', () => {
  const src = read('views/config/SettingsMCPPage.vue')
  assert.match(src, /<div class="sandbox-note">\{\{ \$t\('config\.mcp\.sandboxEmptyHint'\) \}\}<\/div>/,
    '须有可见说明块（不是仅 title tooltip）')
  assert.doesNotMatch(src, /trustDirWarning/, 'server 级不得复用工具级「全拒」警告')
  assert.doesNotMatch(src, /role="alert"/, 'server 级不做告警样式')
})
