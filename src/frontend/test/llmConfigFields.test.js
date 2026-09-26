/**
 * LLM 配置字段拆分（口径 Z1/Z2/Z3，2026-09-25）：
 *   Z1 `maxToken` → **`maxOutputToken`**（最大输出 token = 请求体 max_tokens；由旧 `maxTokens` 改名）；
 *      新增 **`maxContextToken`**（上下文窗口大小，兜底判定来源）。
 *   Z2 摘要目标 = `maxOutputToken` / 2；Z3 兜底判定用真窗口 `maxContextToken`（不再用输出上限代理）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ `*.vue` 源码守卫 + 词条实键校验
 * （与 uxBatch1/2/3 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const DIALOG = 'views/config/EditLLMDialog.vue'
const DEFAULTS = 'config/defaults.js'

// ═══════════════════════════════════════════════════════════════
// ① 字段拆分：maxOutputToken（旧 maxTokens 改名）+ 新增 maxContextToken
// ═══════════════════════════════════════════════════════════════
test('① 对话框：两项字段存在、绑定新名、值域正确（maxContextToken 0 = 不启用兜底）', () => {
  const src = read(DIALOG)
  // 最大输出 Token：沿用现值域 256~1000000/step256（上界随 Token 快捷值 1M 放开），名称/绑定改为 maxOutputToken。
  assert.match(src, /config\.llm\.maxOutputToken/, '须有「最大输出 Token」标签（i18n）')
  assert.match(
    src,
    /v-model\.number="localData\.maxOutputToken"\s+min="256"\s+max="1000000"\s+step="256"/,
    'maxOutputToken 须绑定新名且值域 256~1000000/step256',
  )
  // 上下文窗口（新增）：min=0 / max=1e9 / step=1，0 = 不启用兜底（带 hint）。
  assert.match(src, /config\.llm\.maxContextToken/, '须有「上下文窗口」标签（i18n）')
  assert.match(
    src,
    /v-model\.number="localData\.maxContextToken"\s+min="0"\s+max="1000000000"\s+step="1"/,
    'maxContextToken 须绑定新名且值域 0~1e9/step1',
  )
  assert.match(src, /config\.llm\.maxContextTokenHint/, '须有 0 = 不启用兜底的 hint（i18n）')
  // 旧字段名不再出现（写入只用新名）。
  assert.doesNotMatch(src, /localData\.maxTokens\b/, '不得再绑定旧名 maxTokens')
  assert.doesNotMatch(src, /config\.llm\.maxTokens\b/, '不得再引用旧词条 config.llm.maxTokens')
})

test('① 位置：「上下文窗口」在「最大输出 Token」右侧（同半行宽，前者在后）', () => {
  const src = read(DIALOG)
  // 两项同用 form-item-12（半行宽，一行两列精确合排）；顺序 = 最大输出 Token → 上下文窗口（右侧）。
  const outIdx = src.indexOf('localData.maxOutputToken')
  const ctxIdx = src.indexOf('localData.maxContextToken')
  assert.ok(outIdx > 0 && ctxIdx > 0, '两项字段均须存在')
  assert.ok(outIdx < ctxIdx, '「上下文窗口」须位于「最大输出 Token」右侧（源码顺序在后）')

  // 各自所在 form-item 块须为 form-item-12（半行宽）。
  const outBlock = src.slice(src.lastIndexOf('<div class="form-item', outIdx), outIdx)
  const ctxBlock = src.slice(src.lastIndexOf('<div class="form-item', ctxIdx), ctxIdx)
  assert.match(outBlock, /form-item-12/, '最大输出 Token 须为半行宽 form-item-12')
  assert.match(ctxBlock, /form-item-12/, '上下文窗口须为半行宽 form-item-12')
})

// ═══════════════════════════════════════════════════════════════
// ② 默认值：defaults.js 用新名 + 新增 maxContextToken（不硬编码模型→窗口映射）
// ═══════════════════════════════════════════════════════════════
test('② 默认值：maxOutputToken=4096 / maxContextToken=128000，且无旧名 maxTokens', () => {
  const src = read(DEFAULTS)
  assert.match(src, /maxOutputToken:\s*4096/, 'DEFAULT_LLM 须含 maxOutputToken: 4096')
  assert.match(src, /maxContextToken:\s*128000/, 'DEFAULT_LLM 须含 maxContextToken: 128000')
  assert.doesNotMatch(src, /maxTokens\s*:/, 'DEFAULT_LLM 不得再含旧键 maxTokens')
})

// ═══════════════════════════════════════════════════════════════
// ③ i18n：词条双语齐备；zh 逐字
// ═══════════════════════════════════════════════════════════════
test('③ i18n：maxOutputToken / maxContextToken / maxContextTokenHint 双语齐备', () => {
  for (const loc of LOCALES) {
    const llm = readLocale(loc, 'config.json').llm
    for (const k of ['maxOutputToken', 'maxContextToken', 'maxContextTokenHint']) {
      assert.ok(typeof llm[k] === 'string' && llm[k].trim().length > 0, `${loc} 缺 config.llm.${k}`)
    }
    assert.ok(!('maxTokens' in llm), `${loc} config.llm 不应残留旧词条 maxTokens`)
  }
  const zh = readLocale('zh-CN', 'config.json').llm
  assert.equal(zh.maxOutputToken, '最大输出 Token', 'zh 最大输出 Token 须逐字一致')
  assert.equal(zh.maxContextToken, '上下文窗口', 'zh 上下文窗口 须逐字一致')
})

// ═══════════════════════════════════════════════════════════════
// ④ Token 快捷值（128K/256K/512K/1M）+ 思考模式布局 + 模型能力（多选）
// ═══════════════════════════════════════════════════════════════
test('④ Token 快捷值：两项字段各挂 128K/256K/512K/1M 按钮，点击即写对应字段', () => {
  const src = read(DIALOG)
  assert.match(src, /const tokenPresets = \[/, '须有 tokenPresets 常量表')
  for (const v of ['128000', '256000', '512000', '1000000']) {
    assert.ok(src.includes(`value: ${v}`), `须含快捷值 ${v}`)
  }
  for (const l of ['128K', '256K', '512K', '1M']) {
    assert.ok(src.includes(`label: '${l}'`), `须含按钮文案 ${l}`)
  }
  // 两项字段各自的 Label 行内挂同一组按钮（共用 tokenPresets）
  assert.match(src, /setToken\('maxOutputToken', p\.value\)/, '最大输出 Token 须挂快捷值按钮')
  assert.match(src, /setToken\('maxContextToken', p\.value\)/, '上下文窗口须挂快捷值按钮')
  assert.match(src, /function setToken\(field, value\)/, '须有写值函数')
})

test('④ 布局：思考模式开关与标签同行（靠右）、推理强度下拉（无 label）在其下方', () => {
  const src = read(DIALOG)
  const thinkIdx = src.indexOf('config.llm.thinking')
  const selectIdx = src.indexOf('<Select v-model="localData.reasoningEffort"')
  assert.ok(thinkIdx > 0 && selectIdx > 0, '思考模式标签与推理强度下拉均须存在')
  assert.ok(thinkIdx < selectIdx, '推理强度下拉须在思考模式之后（下方）')
  // 思考模式：标签 + 开关同行（label-switch-row），开关在标签之后；同一 form-item 块内下方为推理强度下拉
  const block = src.slice(src.lastIndexOf('<div class="form-item', thinkIdx), src.indexOf('config.llm.maxToolIterations'))
  assert.match(block, /label-switch-row/, '思考模式须用「标签 + 开关同行」布局')
  assert.match(block, /<Switch v-model="localData\.thinking" \/>/, '开关须绑定思考模式')
  assert.match(block, /<Select v-model="localData\.reasoningEffort"/, '推理强度下拉须与思考模式同块（下方）')
  // 2026-09-26：去掉「推理强度」label（下拉保留 + placeholder 词条保留）；同步回收 protocolHint 词条
  assert.doesNotMatch(block, /config\.llm\.reasoningEffort/, '不得再有「推理强度」label')
  assert.match(block, /config\.llm\.reasoningPlaceholder/, '推理强度下拉须保留 placeholder 词条')
  assert.doesNotMatch(src, /config\.llm\.protocolHint/, '不得再引用「函数协议」说明词条')
  for (const loc of LOCALES) {
    const llm = readLocale(loc, 'config.json').llm
    assert.equal(llm.reasoningEffort, undefined, `${loc} 应回收 config.llm.reasoningEffort（label 已移除）`)
    assert.equal(llm.protocolHint, undefined, `${loc} 应回收 config.llm.protocolHint（说明已移除）`)
    assert.ok(typeof llm.reasoningPlaceholder === 'string' && llm.reasoningPlaceholder.trim().length > 0,
      `${loc} 缺 config.llm.reasoningPlaceholder`)
  }
})

test('④ 模型能力：checkbox 多选（推理 / 图形），绑定 localData.capabilities', () => {
  const src = read(DIALOG)
  assert.match(src, /config\.llm\.capabilities/, '须有「模型能力」标签（i18n）')
  assert.match(src, /config\.llm\.capReasoning/, '须有「推理」能力项（i18n）')
  assert.match(src, /config\.llm\.capVision/, '须有「图形」能力项（i18n）')
  assert.match(src, /type="checkbox" value="reasoning" v-model="localData\.capabilities"/, '推理须为 checkbox 多选绑定')
  assert.match(src, /type="checkbox" value="vision" v-model="localData\.capabilities"/, '图形须为 checkbox 多选绑定')
  assert.match(src, /if \(!Array\.isArray\(localData\.capabilities\)\) localData\.capabilities = \[\]/, '旧记录须回落空数组')
  // 默认值：DEFAULT_LLM 含 capabilities（空数组）
  assert.match(read(DEFAULTS), /capabilities:\s*\[\]/, 'DEFAULT_LLM 须含 capabilities 初值')
  // i18n 双语齐备
  for (const loc of LOCALES) {
    const llm = readLocale(loc, 'config.json').llm
    for (const k of ['capabilities', 'capReasoning', 'capVision', 'capabilitiesHint']) {
      assert.ok(typeof llm[k] === 'string' && llm[k].trim().length > 0, `${loc} 缺 config.llm.${k}`)
    }
  }
})

// ═══════════════════════════════════════════════════════════════
// ⑤ 能力消费方：聊天窗口「图形」未声明 → 禁用截图
// ═══════════════════════════════════════════════════════════════
test('⑤ 聊天窗口：所选 LLM 未声明「图形」→ 截图按钮禁用 + 说明 tooltip + 事件兜底', () => {
  const src = read('views/chat/ChatPanel.vue')
  assert.match(src, /const selectedLLMCaps = computed\(/, '须取所选 LLM 的 capabilities')
  assert.match(src, /capabilities\) \? rec\.capabilities : \[\]/, '未声明须回落空数组')
  assert.match(src, /const canScreenshot = computed\(\(\) => selectedLLMCaps\.value\.includes\('vision'\)\)/, '截图前提 = 声明 vision')
  assert.match(src, /:disabled="!canScreenshot"/, '截图按钮须按能力禁用')
  assert.match(src, /chat\.screenshot_no_vision/, '禁用须给说明 tooltip（i18n）')
  assert.match(src, /if \(!canScreenshot\.value\) return/, '事件处理须兜底（防旁路）')
  // 配置变更（增删/改名/改能力）后须重解析默认 LLM，否则选择器与能力判定停在旧值（须重载才生效）
  assert.match(src, /if \(!llmOptions\.value\.some\(o => o\.value === selectedLLM\.value\)\)/, 'config-refresh 须重解析已失效/未选中的 LLM')
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    assert.ok(typeof chat.screenshot_no_vision === 'string' && chat.screenshot_no_vision.trim().length > 0,
      `${loc} 缺 chat.screenshot_no_vision`)
  }
})
