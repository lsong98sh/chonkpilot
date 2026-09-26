/**
 * 用户视角缺陷「批 1」前端单测（2026-09-20）：
 *   A 无可用 LLM（口径变更 2026-09-20：移除常驻引导 → 改为**发送失败时**的可行动提示
 *     「去配置 LLM」，复用 previewTabOpen{kind:'settings-llm'}；再变更（2026-09-20 收口）：
 *     内置 `echo` 视为可用 LLM → 门槛改为**本回合失败即提示**，`isLLMSetupMissing` 删除）
 *   B 深色主题可读性（inline code / 工具卡头 / 状态徽标 / .td-error / --text-muted）
 *   C 文件树删除/移动失败回显（不再只 console.error，且不呈现"已成功"）
 *   D 批 1 收口（2026-09-20）：残留硬编码浅色清零（.arbitration-hint / .stop-btn:hover /
 *     SessionTreeNode 裁决条与停止按钮 / TaskDetailView 状态圆点）+ 全仓扫描
 *   ⑤ 批 1 残留小项收口（2026-09-20）：A `echo` 算可用（失败即提示）/ B 配置刷新即清除提示
 *     / C 插件失败提示前端按 key 映射 i18n（I-117，zh-CN + en-US）
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯逻辑直调（llmSetup / pluginNotice / refreshUserConfig / fileOpErrorText）
 * + 对比度实测（解析 variables.css 各主题 token）+ `*.vue` 源码守卫（与 configSurface / toolStopWiring 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { needsLLMConfigHint } from '../src/utils/llmSetup.js'
import { pluginFailureText } from '../src/utils/pluginNotice.js'
import { refreshUserConfig } from '../src/utils/llmConfigRefresh.js'
import { fileOpErrorText } from '../src/utils/fileOpError.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

/** 去掉 HTML / CSS / JS 注释后再做"硬编码字面量"扫描（注释里引用旧色值是允许的） */
function stripComments(s) {
  return s
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}
/** 递归列出扫描范围内的源码文件（与 §D 扫描范围一致） */
function walkSrc(dir, out = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name)
    if (e.isDirectory()) walkSrc(p, out)
    else if (/\.(vue|js|ts|css)$/.test(e.name)) out.push(p)
  }
  return out
}

// ── 对比度工具（WCAG 2.1 相对亮度 + 对比度）─────────────────────────
function srgbToLin(c) {
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}
function relLum(hex) {
  const h = hex.replace('#', '')
  const [r, g, b] = [0, 2, 4].map(i => srgbToLin(parseInt(h.slice(i, i + 2), 16) / 255))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
function contrast(a, b) {
  const x = relLum(a)
  const y = relLum(b)
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
}
/** color-mix(in srgb, fg p%, bg) 的 sRGB 线性插值结果 */
function mixSrgb(fgHex, bgHex, p) {
  const f = fgHex.replace('#', '')
  const b = bgHex.replace('#', '')
  let out = '#'
  for (let i = 0; i < 6; i += 2) {
    const v = Math.round(p * parseInt(f.slice(i, i + 2), 16) + (1 - p) * parseInt(b.slice(i, i + 2), 16))
    out += v.toString(16).padStart(2, '0')
  }
  return out
}
/** 从 variables.css 抽取某主题块的 token（--name → 值） */
function themeTokens(css, selector) {
  const re = new RegExp(selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\s*\\{([^}]*)\\}')
  const m = css.match(re)
  assert.ok(m, 'variables.css 缺少 ' + selector + ' 块')
  const tokens = {}
  for (const line of m[1].split('\n')) {
    const mm = line.match(/(--[\w-]+)\s*:\s*([^;]+);/)
    if (mm) tokens[mm[1]] = mm[2].trim()
  }
  return tokens
}
const varsCss = read('assets/styles/variables.css')
const THEMES = {
  light: themeTokens(varsCss, ':root'),
  dark: themeTokens(varsCss, '[data-theme="dark"]'),
  nord: themeTokens(varsCss, '[data-theme="nord"]'),
}

// ═══════════════════════════════════════════════════════════════
// ① 发送失败提示（口径 2026-09-20：`echo` 视为可用 → 本回合失败即提示）+ 默认 LLM 明示
// ═══════════════════════════════════════════════════════════════
test('① needsLLMConfigHint：本回合失败即提示（不再看 usr llms）；空回复 / 非错误终态不提示', () => {
  assert.equal(needsLLMConfigHint({ status: 'error', code: 'LLM_REQUEST_FAILED' }), true, 'LLM 终态错误 → 提示')
  assert.equal(needsLLMConfigHint({ status: 'error' }), true, '缺 code 的错误 → 提示')
  assert.equal(needsLLMConfigHint({ status: 'error', code: 'EMPTY_REPLY' }), false, '空回复非配置问题')
  assert.equal(needsLLMConfigHint({ status: 'complete' }), false, '正常完成不提示')
  assert.equal(needsLLMConfigHint({ status: 'interrupted' }), false, '取消/中断不提示')
  assert.equal(needsLLMConfigHint(null), false, '空事件不提示')
  assert.equal(needsLLMConfigHint(undefined), false, '缺事件不提示')
  // 判据签名只收事件：`echo`（kind=builtin，确实可用）视为可用 LLM → 不得再以 usr llms 作门槛
  const src = read('utils/llmSetup.js')
  assert.match(src, /export function needsLLMConfigHint\(evt\)/, '判据只收 llm-complete 事件（不再收 llms）')
  assert.doesNotMatch(stripComments(src), /isLLMSetupMissing/, 'isLLMSetupMissing 已无消费方，须删除（注释里的历史说明不算）')
})

test('① ChatPanel：无常驻引导；失败后可行动提示 + CTA 跳「设置 → LLM」；无 watch', () => {
  const src = read('views/chat/ChatPanel.vue')
  assert.match(src, /useLLMSetup\(/, '配置刷新须封装在 useLLMSetup composable')
  // 常驻引导（进入即见）必须已移除
  assert.doesNotMatch(src, /llm-setup-guide/, '不得再渲染常驻引导条')
  assert.doesNotMatch(src, /needsSetup/, '不得再依赖常驻引导判定')
  assert.doesNotMatch(src, /chat\.llm_setup_title|chat\.llm_setup_desc|chat\.llm_setup_cta/, '不得再引用已删引导键')
  // 失败时的可行动提示：按判定显示 + CTA 直达设置
  assert.match(src, /v-if="llmSetupFailed"/, '提示须按失败判定显示/隐藏')
  assert.match(src, /chat\.llm_setup_failed_hint/, '须有失败提示文案（i18n）')
  assert.match(src, /chat\.llm_setup_failed_cta/, '须有 CTA 文案（i18n）')
  assert.match(src, /EventNames\.previewTabOpen/, 'CTA 须走既有 previewTabOpen 打开设置页')
  assert.match(src, /kind: 'settings-llm'/, 'CTA 须跳设置 LLM 页')
  // 判定来源 = 统一判据（只按本回合终态，不按 usr llms）
  assert.match(src, /needsLLMConfigHint\(d\)/, '须复用 needsLLMConfigHint（只传事件）')
  assert.doesNotMatch(src, /needsLLMConfigHint\(d, llms/, '判定不得再传 llms（echo 视为可用）')
  assert.match(src, /EventNames\.llmComplete/, '须在 llm-complete 终态上判定失败')
  // 同一次失败只提示一次（按 turn 去重）
  assert.match(src, /llmSetupHintKey/, '须对同一次失败去重（不连点连弹）')
  // 2026-09-26：聊天输入框选择器只列 usr providers ——「系统默认（启动参数）」选项与其
  // 常驻说明、前端哨兵一并移除（`gui.system.builtins` 不再下发 `builtinLLMs`）
  assert.doesNotMatch(src, /llm-item-hint/, '不应再有「系统默认」常驻说明')
  assert.doesNotMatch(src, /SYSTEM_DEFAULT_LLM/, '不应再有「系统默认」前端哨兵')
  assert.doesNotMatch(src, /getSystemBuiltins/, '选择器不应再读 builtinLLMs')
  // 不阻断发送：原有发送前校验保留
  assert.match(src, /llmConfigReady\(\)/, '发送前校验须保留（提示不阻断）')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect')
})

test('① useLLMSetup：订阅既有配置刷新并把最新 usr llms 交给回调，无 watch', () => {
  const comp = read('composables/useLLMSetup.js')
  assert.match(comp, /onDataRefresh\('user-config'/, '须订阅 data-user-config-refresh')
  assert.match(comp, /EventNames\.configRefresh/, '须订阅 IDE config-refresh')
  assert.match(comp, /refreshUserConfig\(getUserConfig, onConfigRefresh\)/, '刷新动作须复用统一工厂并接回调')
  const util = read('utils/llmConfigRefresh.js')
  assert.match(util, /onConfigRefresh\(llms\)/, '刷新完成须回调调用方（供提示即时清除）')
  assert.match(util, /export function refreshUserConfig\(/, '刷新动作须为可直测纯工厂（无组件实例依赖）')
  assert.doesNotMatch(comp, /\bwatch(Effect)?\s*\(/, 'composable 不得用 watch/watchEffect')
})

test('① i18n：引导键已删、失败提示键齐备（zh-CN / en-US）', () => {
  const removed = ['llm_setup_title', 'llm_setup_desc', 'llm_setup_cta']
  const added = ['llm_setup_failed_hint', 'llm_setup_failed_cta']
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    for (const k of removed) {
      assert.equal(chat[k], undefined, `${loc} 不应再有 chat.${k}（常驻引导已移除）`)
    }
    for (const k of added) {
      assert.ok(typeof chat[k] === 'string' && chat[k].trim().length > 0, `${loc} 缺 chat.${k}`)
    }
  }
})

// ═══════════════════════════════════════════════════════════════
// ② 深色主题可读性
// ═══════════════════════════════════════════════════════════════
test('② inline code 不再硬编码 black，改用主题 token（三主题 ≥4.5:1）', () => {
  const msg = read('views/chat/MessageItem.vue')
  assert.doesNotMatch(msg, /color:\s*black/, 'inline code 不得硬编码 black')
  const rule = msg.match(/\.message-content :deep\(code\)\s*\{[\s\S]*?\n\}/)
  assert.ok(rule, '未找到 inline code 规则')
  assert.match(rule[0], /color:\s*var\(--text-primary\)/, '前景须走 --text-primary')
  assert.match(rule[0], /background:\s*var\(--bg-tertiary\)/, '底色须走 --bg-tertiary')

  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    const c = contrast(tk['--text-primary'], tk['--bg-tertiary'])
    nums[name] = c.toFixed(2)
    assert.ok(c >= 4.5, `${name} inline code 对比度 ${c.toFixed(2)} < 4.5`)
  }
  console.log('[contrast] inline code fg/bg-tertiary =', JSON.stringify(nums))
})

test('② 工具卡头：底色/文字走 token（三主题 ≥4.5:1）', () => {
  const msg = read('views/chat/MessageItem.vue')
  const hdr = msg.match(/\.section-header\s*\{[\s\S]*?\n\}/)
  assert.ok(hdr, '未找到 .section-header 规则')
  assert.match(hdr[0], /background:\s*var\(--bg-tertiary\)/, '卡头底色须走 --bg-tertiary')
  assert.match(hdr[0], /color:\s*var\(--text-secondary\)/, '卡头文字须走 --text-secondary')
  assert.doesNotMatch(msg, /color:\s*#(b8860b|1565c0|2e7d32|7b1fa2)/i, '卡头不得再硬编码浅色文字')

  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    const c = contrast(tk['--text-secondary'], tk['--bg-tertiary'])
    nums[name] = c.toFixed(2)
    assert.ok(c >= 4.5, `${name} 卡头对比度 ${c.toFixed(2)} < 4.5`)
  }
  console.log('[contrast] section-header fg/bg-tertiary =', JSON.stringify(nums))
})

test('② 状态徽标：文字走 --text-primary、底色 color-mix 语义 token（三主题 ≥4.5:1）', () => {
  const msg = read('views/chat/MessageItem.vue')
  const base = msg.match(/\.status-badge\s*\{[\s\S]*?\n\}/)
  assert.ok(base, '未找到 .status-badge 基础规则')
  assert.match(base[0], /color:\s*var\(--text-primary\)/, '徽标文字须走 --text-primary')
  assert.doesNotMatch(base[0], /^\s*color:\s*(#|rgb)/m, '徽标基础规则不得硬编码文字色')

  const rules = msg.match(/\.status-badge[^{}]*\{[^}]*\}/g) || []
  const tints = []
  for (const r of rules) {
    assert.doesNotMatch(r, /^\s*color:\s*(#|rgb)/m, '徽标不得再硬编码浅色文字：' + r.slice(0, 40))
    assert.doesNotMatch(r, /^\s*background:\s*rgba?\(/m, '徽标底色须走 token：' + r.slice(0, 40))
    const m = r.match(/color-mix\(in srgb,\s*var\((--[\w-]+)\)\s*(\d+)%,\s*var\((--[\w-]+)\)\)/)
    if (m) tints.push({ fg: m[1], pct: Number(m[2]) / 100, bg: m[3] })
  }
  assert.ok(tints.length >= 6, `徽标底色应全部走 color-mix(token)，实测 ${tints.length} 条`)
  const nums = {}
  for (const t of tints) {
    for (const [name, tk] of Object.entries(THEMES)) {
      const bg = mixSrgb(tk[t.fg] || t.fg, tk[t.bg] || t.bg, t.pct)
      const c = contrast(tk['--text-primary'], bg)
      nums[`${t.fg}@${name}`] = c.toFixed(2)
      assert.ok(c >= 4.5, `${name} 徽标 ${t.fg} 对比度 ${c.toFixed(2)} < 4.5`)
    }
  }
  console.log('[contrast] status-badge fg/bg =', JSON.stringify(nums))
})

test('② .td-error 走 --danger 语义 token（三主题 ≥4.5:1，light 不回归）', () => {
  const src = read('components/task/TaskDetailView.vue')
  const rule = src.match(/\.td-error\s*\{[\s\S]*?\n\}/)
  assert.ok(rule, '未找到 .td-error 规则')
  assert.match(rule[0], /color:\s*color-mix\(in srgb,\s*var\(--danger\)/, '须复用 --danger token')
  assert.doesNotMatch(rule[0], /^\s*color:\s*(#|rgb)/m, '.td-error 不得再硬编码文字色')
  assert.doesNotMatch(rule[0], /^\s*background:\s*rgba?\(/m, '.td-error 底色须走 token')

  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    // 文字 = --danger 70% + --text-primary 30%；底 = --danger 12% 淡底叠在最亮面板底上（最不利）
    const fg = mixSrgb(tk['--danger'], tk['--text-primary'], 0.7)
    const panel = relLum(tk['--bg-primary']) > relLum(tk['--bg-secondary'])
      ? tk['--bg-primary'] : tk['--bg-secondary']
    const bg = mixSrgb(tk['--danger'], panel, 0.12)
    const c = contrast(fg, bg)
    nums[name] = c.toFixed(2)
    assert.ok(c >= 4.5, `${name} .td-error 对比度 ${c.toFixed(2)} < 4.5`)
  }
  // light：新值须优于改动前（#f5222d on 8% 同色淡底/白底）
  const before = contrast('#f5222d', mixSrgb('#f5222d', '#ffffff', 0.08))
  assert.ok(Number(nums.light) > before, `light 须提升（${before.toFixed(2)} → ${nums.light}）`)
  console.log('[contrast] td-error fg/bg =', JSON.stringify(nums), 'light-before=', before.toFixed(2))
})

test('② --text-muted：三主题对最亮面板底 ≥4.5:1', () => {
  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    assert.match(tk['--text-muted'], /^#[0-9a-f]{6}$/i, `${name} --text-muted 须为 hex`)
    const c = contrast(tk['--text-muted'], tk['--bg-primary'])
    nums[name] = c.toFixed(2)
    assert.ok(c >= 4.5, `${name} --text-muted/bg-primary 对比度 ${c.toFixed(2)} < 4.5`)
  }
  console.log('[contrast] --text-muted/bg-primary =', JSON.stringify(nums))
})

// ═══════════════════════════════════════════════════════════════
// ③ 文件树删除/移动失败回显
// ═══════════════════════════════════════════════════════════════
test('③ fileOpErrorText：失败给原因；用户取消静默；无原因用兜底', () => {
  assert.equal(fileOpErrorText('cancel', 'fb'), '', 'confirm 取消（reject cancel）不得提示')
  assert.equal(fileOpErrorText(new Error('EPERM: operation not permitted'), 'fb'),
    'EPERM: operation not permitted', '须回显后端失败原因')
  assert.equal(fileOpErrorText({ code: 'exists' }, 'fb'), 'exists', '无 message 时退 code')
  assert.equal(fileOpErrorText(undefined, 'fb'), 'fb', '无任何原因时用兜底文案')
})

test('③ FileTree：删除失败 → 可见提示（不再只 console.error）', () => {
  const src = read('views/filetree/FileTree.vue')
  assert.match(src, /fileOpErrorText/, '须用统一失败原因提取')
  const del = src.match(/async function doDelete\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(del, '未找到 doDelete')
  assert.doesNotMatch(del[0], /console\.error\(/, 'doDelete 不得只 console.error')
  assert.match(del[0], /message\.error\(t\('fileTree\.delete_failed_detail'/, '失败须可见提示（含原因）')
  // 不呈现"已成功"：成功提示在后端删除之后
  assert.ok(
    del[0].indexOf('await deleteFilePath(path)') < del[0].indexOf("message.success(t('fileTree.deleted'"),
    '成功提示必须在 deleteFilePath 之后'
  )
})

test('③ FileTree：移动/覆盖失败 → 可见提示（不呈现"已移动"）', () => {
  const src = read('views/filetree/FileTree.vue')
  const mv = src.match(/async function doMove\s*\([^)]*\)\s*\{[\s\S]*?\n\}/)
  assert.ok(mv, '未找到 doMove')
  assert.doesNotMatch(mv[0], /console\.error\(/, 'doMove 不得只 console.error')
  const errs = mv[0].match(/message\.error\(t\('fileTree\.move_failed_detail'/g) || []
  assert.equal(errs.length, 2, '同名覆盖失败 + 其它失败都必须回显')
  // 两次 moved 成功提示都紧跟成功的 moveFile 之后（失败路径无 success）
  const successAfterFirst = mv[0].indexOf('await moveFile(srcNode.path, newPath)') <
    mv[0].indexOf("message.success(t('fileTree.moved'")
  const successAfterOverwrite = mv[0].indexOf('await moveFile(srcNode.path, newPath, true)') <
    mv[0].lastIndexOf("message.success(t('fileTree.moved'")
  assert.ok(successAfterFirst && successAfterOverwrite, '成功提示须在 moveFile 成功之后')
})

test('③ i18n：失败回显键 zh-CN / en-US 齐备且含 {name}/{error}', () => {
  for (const loc of LOCALES) {
    const ft = readLocale(loc, 'fileTree.json')
    for (const k of ['delete_failed_detail', 'move_failed_detail']) {
      const v = ft[k]
      assert.ok(typeof v === 'string' && v.length > 0, `${loc} 缺 fileTree.${k}`)
      assert.ok(v.includes('{name}') && v.includes('{error}'), `${loc} ${k} 须含 {name}/{error}`)
    }
    assert.ok(typeof ft.unknown_error === 'string' && ft.unknown_error.length > 0, `${loc} 缺 unknown_error`)
  }
})

// ═══════════════════════════════════════════════════════════════
// ④ 批 1 收口：残留硬编码浅色清零（2026-09-20）
//    扫描范围 = `chonkpilot-frontend/src/**/*.{vue,js,ts,css}`（去 HTML/CSS/JS 注释后）
//    保留项（不在扫描/清理范围，理由见 20-gui §12.8）：
//      · `#fff`（实心强调色填充上的文字，§12.8 专条口径）
//      · `#67c23a` / `#bfbfbf`（`useTaskStatus.js` 的 4 态映射，既有单测「逐值不变」锁定）
//      · `rgba(0,0,0,·)` / `rgba(255,193,7,·)` 一类装饰性叠底与色条（不承载文字对比度）
// ═══════════════════════════════════════════════════════════════
test('④ .arbitration-hint 走 --warning token（三主题 ≥4.5:1，且优于改前 #e65100）', () => {
  const msg = read('views/chat/MessageItem.vue')
  const rule = msg.match(/\.arbitration-hint\s*\{[\s\S]*?\n\}/)
  assert.ok(rule, '未找到 .arbitration-hint 规则')
  assert.match(rule[0], /color:\s*color-mix\(in srgb,\s*var\(--warning\)/, '须复用 --warning 语义 token')
  assert.doesNotMatch(rule[0], /^\s*color:\s*(#|rgb)/m, '.arbitration-hint 不得硬编码文字色')
  assert.match(msg, /\[data-theme="dark"\]\s*\.arbitration-hint/, 'dark 须覆写回 --warning 原色')
  assert.match(msg, /\[data-theme="nord"\]\s*\.arbitration-hint/, 'nord 须覆写回 --warning 原色')

  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    // light = --warning 55% + --text-primary 45%（压深）；dark/nord = --warning 原色
    const fg = name === 'light' ? mixSrgb(tk['--warning'], tk['--text-primary'], 0.55) : tk['--warning']
    const c = contrast(fg, tk['--bg-primary'])
    const before = contrast('#e65100', tk['--bg-primary'])
    nums[name] = c.toFixed(2)
    assert.ok(c >= 4.5, `${name} arbitration-hint 对比度 ${c.toFixed(2)} < 4.5`)
    assert.ok(c > before, `${name} 须优于改前（${before.toFixed(2)} → ${c.toFixed(2)}）`)
  }
  console.log('[contrast] arbitration-hint =', JSON.stringify(nums))
})

test('④ .stop-btn:hover 走 --danger token（图标 ≥3:1，三主题）', () => {
  const msg = read('views/chat/MessageItem.vue')
  const rule = stripComments(msg).match(/\.stop-btn:hover\s*\{[\s\S]*?\n\}/)
  assert.ok(rule, '未找到 .stop-btn:hover 规则')
  assert.match(rule[0], /color:\s*var\(--danger\)/, '须走 --danger token')
  assert.doesNotMatch(rule[0], /#|rgba?\(/, '不得再有硬编码色值')

  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    // hover 底 = --danger 12% 淡底叠在卡头 --bg-tertiary 上
    const bg = mixSrgb(tk['--danger'], tk['--bg-tertiary'], 0.12)
    const c = contrast(tk['--danger'], bg)
    const before = contrast('#ff4d4f', mixSrgb('#ff4d4f', tk['--bg-tertiary'], 0.12))
    nums[name] = c.toFixed(2)
    assert.ok(c >= 3, `${name} 停止图标对比度 ${c.toFixed(2)} < 3`)
    assert.ok(c > before, `${name} 须优于改前（${before.toFixed(2)} → ${c.toFixed(2)}）`)
  }
  console.log('[contrast] stop-btn:hover icon =', JSON.stringify(nums))
})

test('④ SessionTreeNode：裁决条取消键（文字 ≥4.5）与停止图标（≥3）走 --danger token', () => {
  const raw = read('views/tasks/SessionTreeNode.vue')
  const src = stripComments(raw)
  assert.doesNotMatch(src, /#(ff4d4f|f5222d|e65100|52c41a)/i, '不得再硬编码浅色')

  const dangerBtn = src.match(/\.await-btn-danger\s*\{[\s\S]*?\n\}/)
  assert.ok(dangerBtn, '未找到 .await-btn-danger 规则')
  assert.match(dangerBtn[0], /color:\s*color-mix\(in srgb,\s*var\(--danger\)/, '取消键文字须走 --danger 混合')
  assert.doesNotMatch(dangerBtn[0], /^\s*color:\s*(#|rgb)/m, '取消键不得硬编码文字色')

  const stop = src.match(/\.node-stop\s*\{[\s\S]*?\n\}/)
  assert.ok(stop, '未找到 .node-stop 规则')
  assert.match(stop[0], /color:\s*var\(--danger\)/, '停止图标须走 --danger token')

  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    const textFg = mixSrgb(tk['--danger'], tk['--text-primary'], 0.7)
    const ct = contrast(textFg, tk['--bg-primary'])
    const ci = contrast(tk['--danger'], tk['--bg-primary'])
    nums[name] = { 文字: ct.toFixed(2), 图标: ci.toFixed(2) }
    assert.ok(ct >= 4.5, `${name} 取消键文字对比度 ${ct.toFixed(2)} < 4.5`)
    assert.ok(ci >= 3, `${name} 停止图标对比度 ${ci.toFixed(2)} < 3`)
    assert.ok(ct > contrast('#ff4d4f', tk['--bg-primary']), `${name} 须优于改前 #ff4d4f`)
  }
  console.log('[contrast] session-tree danger =', JSON.stringify(nums))
})

test('④ TaskDetailView 状态圆点走主题 token（图形 ≥3:1，三主题）', () => {
  const src = stripComments(read('components/task/TaskDetailView.vue'))
  assert.doesNotMatch(src, /#(52c41a|f5222d|bfbfbf)/i, '状态圆点不得再硬编码色值')
  assert.match(src, /\.td-status\.is-done\s*\{\s*background:\s*var\(--success\)/, 'done 圆点须走 --success')
  assert.match(src, /\.td-status\.is-error\s*\{\s*background:\s*var\(--danger\)/, 'error 圆点须走 --danger')
  assert.match(src, /\.td-status\.is-stopped\s*\{\s*background:\s*var\(--text-muted\)/, 'stopped 圆点须走 --text-muted')

  const nums = {}
  for (const [name, tk] of Object.entries(THEMES)) {
    // 最不利底 = --bg-primary / --bg-secondary 取低者
    for (const token of ['--success', '--danger', '--text-muted']) {
      const c = Math.min(contrast(tk[token], tk['--bg-primary']), contrast(tk[token], tk['--bg-secondary']))
      nums[`${token}@${name}`] = c.toFixed(2)
      assert.ok(c >= 3, `${name} 状态圆点 ${token} 对比度 ${c.toFixed(2)} < 3`)
    }
  }
  console.log('[contrast] td-status dots =', JSON.stringify(nums))
})

test('④ 全仓扫描：批 1 清理过的硬编码浅色不再出现（src/**/*.{vue,js,ts,css}）', () => {
  const BANNED = ['#e65100', '#ff4d4f', '#f5222d', '#52c41a']
  const files = walkSrc(srcDir)
  assert.ok(files.length >= 80, `扫描范围异常（仅 ${files.length} 个文件）`)
  const hits = []
  for (const p of files) {
    const code = stripComments(readFileSync(p, 'utf8')).toLowerCase()
    for (const b of BANNED) if (code.includes(b)) hits.push(`${p} → ${b}`)
  }
  assert.deepEqual(hits, [], '发现硬编码浅色（应改走主题 token）：\n' + hits.join('\n'))
  console.log(`[scan] 无硬编码浅色：${files.length} 个文件 / 禁用字面量 ${BANNED.join(' ')}`)
})

// ═══════════════════════════════════════════════════════════════
// ⑤ 批 1 残留小项收口（2026-09-20）
//    A `echo`（kind=builtin）算可用 LLM → 门槛改「本回合失败即提示」（见 ① 段）
//    B 配置刷新（data-user-config-refresh / config-refresh）→ 失败提示立即清除
//    C 插件失败提示（tool-notify{notice:plugin-failure}）前端按 key 映射 i18n（I-117）
// ═══════════════════════════════════════════════════════════════
test('⑤ B · refreshUserConfig：配置刷新到达 → 通知调用方（提示即时清除），读失败亦通知', async () => {
  let notified = 0
  let snapshot = null
  const refresh = refreshUserConfig(
    async () => ({ config: { llms: [{ name: 'gpt-local' }] } }),
    (llms) => { notified++; snapshot = llms },
  )
  await refresh()
  assert.equal(notified, 1, '配置刷新须回调一次（ChatPanel 借此清除失败提示）')
  assert.deepEqual(snapshot, [{ name: 'gpt-local' }], '回调须带最新 usr llms 快照')

  // 读配置失败仍须通知（事件本身即「配置已变」信号 → 提示照清）
  const refreshErr = refreshUserConfig(async () => { throw new Error('boom') }, () => { notified++ })
  await refreshErr()
  assert.equal(notified, 2, '读失败仍须回调（不吞掉清除时机）')

  // 无回调（可选参）不得抛
  await refreshUserConfig(async () => ({}))()
  assert.equal(notified, 2, '未传回调不得额外通知/抛错')
})

test('⑤ B · ChatPanel：配置变化即清除失败提示（useLLMSetup 回调，无 watch/watchEffect）', () => {
  const src = read('views/chat/ChatPanel.vue')
  assert.match(src, /useLLMSetup\(\(\) => \{ llmSetupFailed\.value = false \}\)/,
    '配置刷新须立即清除失败提示（不必等下一次发送）')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect 监听配置')
})

/** 极简 i18n 替身：从真实 locales JSON 取键 + `{name}` 插值（供 pluginFailureText 直测） */
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

test('⑤ C · pluginFailureText：已知 plugin/kind 命中 i18n（zh/en 各一）；未登记回落宿主原文', () => {
  const zh = mkI18n({ pluginFailure: readLocale('zh-CN', 'pluginFailure.json') })
  const en = mkI18n({ pluginFailure: readLocale('en-US', 'pluginFailure.json') })
  const payload = { plugin: 'memory', kind: 'llm', reason: '连接超时', message: '⚠️ 宿主中文兜底' }

  assert.equal(pluginFailureText(payload, zh.t, zh.te),
    '⚠️ 记忆沉淀失败（模型调用）：连接超时（本轮对话未受影响；详情见日志文件）', 'zh-CN 命中本地化文案')
  assert.equal(pluginFailureText(payload, en.t, en.te),
    '⚠️ Memory consolidation failed (model call): 连接超时 (this turn was not affected; see the log file for details)',
    'en-US 命中本地化文案')
  // 另一插件 / 另一类别（宿主实际上报的取值：memory 6 类、compress 2 类）
  assert.equal(pluginFailureText({ plugin: 'compress', kind: 'save', reason: 'r' }, zh.t, zh.te),
    '⚠️ 上下文压缩失败（保存）：r（本轮对话未受影响；详情见日志文件）')
  // 未登记插件 / 缺 plugin → 回落宿主原文（不伪造文案）
  assert.equal(pluginFailureText({ plugin: 'foo', kind: 'llm', reason: 'r', message: '⚠️ 宿主原文' }, zh.t, zh.te),
    '⚠️ 宿主原文', '未登记插件回落 message')
  assert.equal(pluginFailureText({ kind: 'llm', reason: 'r', message: '⚠️ 宿主原文' }, zh.t, zh.te),
    '⚠️ 宿主原文', '缺 plugin 亦回落')
  assert.equal(pluginFailureText({ plugin: 'foo', text: 'fallback' }, zh.t, zh.te), 'fallback', '回落 text 字段')
  // 未登记类别 → 省略阶段片段（不显示原始键名）；映射缺失（无 message）→ 空串
  assert.equal(pluginFailureText({ plugin: 'memory', kind: 'weird', reason: 'r' }, zh.t, zh.te),
    '⚠️ 记忆沉淀失败：r（本轮对话未受影响；详情见日志文件）')
  assert.equal(pluginFailureText({ plugin: 'foo' }, zh.t, zh.te), '', '无宿主原文 → 空串（不伪造）')
  // 缺 reason → i18n 兜底文案
  assert.equal(pluginFailureText({ plugin: 'memory', kind: 'read' }, zh.t, zh.te),
    '⚠️ 记忆沉淀失败（读取）：原因未知（详见日志文件）（本轮对话未受影响；详情见日志文件）')
})

test('⑤ C · i18n 键齐备（zh-CN + en-US）：宿主实际上报的插件/类别全覆盖', () => {
  // 类别来源 = 宿主实际上报点（chonkpilot-plugin-memory/memory.go 的 6 处 + chonkpilot-plugin-compress/compress.go 的 2 处）
  const kinds = ['llm', 'save', 'read', 'config', 'messages', 'categories']
  for (const loc of LOCALES) {
    const pf = readLocale(loc, 'pluginFailure.json')
    for (const k of ['message', 'phase', 'reason_unknown']) {
      assert.ok(typeof pf[k] === 'string' && pf[k].trim().length > 0, `${loc} 缺 pluginFailure.${k}`)
    }
    assert.ok(pf.message.includes('{what}') && pf.message.includes('{reason}'),
      `${loc} pluginFailure.message 须含 {what}/{reason}`)
    assert.ok(pf.phase.includes('{phase}'), `${loc} pluginFailure.phase 须含 {phase}`)
    for (const p of ['memory', 'compress']) {
      assert.ok(typeof pf.what[p] === 'string' && pf.what[p].trim().length > 0, `${loc} 缺 what.${p}`)
    }
    for (const k of kinds) {
      assert.ok(typeof pf.kind[k] === 'string' && pf.kind[k].trim().length > 0, `${loc} 缺 kind.${k}`)
    }
  }
})

test('⑤ C · 接线：i18n 注册 pluginFailure；ChatPanel 按 key 映射并回落 data.message', () => {
  const i18nSrc = read('plugins/i18n.js')
  assert.match(i18nSrc, /pluginFailure: zhPluginFailure/, 'zh-CN 须注册 pluginFailure 命名空间')
  assert.match(i18nSrc, /pluginFailure: enPluginFailure/, 'en-US 须注册 pluginFailure 命名空间')

  const src = read('views/chat/ChatPanel.vue')
  assert.match(src, /import \{ pluginFailureText \}/, '须复用 pluginFailureText（统一映射）')
  assert.match(src, /data\.notice === 'plugin-failure'/, '只在 plugin-failure 通知上映射')
  assert.match(src, /pluginFailureText\(data, t, te\)/, '须传 i18n 的 t/te（te 用于回落判定）')
  assert.match(src, /data\.message \|\| data\.text/, '非插件失败通知仍走既有 message/text')

  const util = read('utils/pluginNotice.js')
  assert.match(util, /te\(NS \+ '\.what\.' \+ plugin\)/, '未登记插件须以 te 判定并回落')
  assert.match(util, /return fallback/, '映射缺失须回落宿主原文')
})

