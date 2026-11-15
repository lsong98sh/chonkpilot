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
import { readSrc } from './support/feSource.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readSrc(rel)
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
  // INPUTVAL-1：值域改由 utils/fieldRules 常量提供（OUT_TOKEN_MIN=256 / OUT_TOKEN_MAX=1000000，见 fieldRules.test.js）。
  assert.match(
    src,
    /v-model\.number="localData\.maxOutputToken"\s+:min="OUT_TOKEN_MIN"\s+:max="OUT_TOKEN_MAX"\s+step="256"/,
    'maxOutputToken 须绑定新名且值域 256~1000000/step256',
  )
  // 上下文窗口（新增）：min=0 / max=1e9 / step=1，0 = 不启用兜底（带 hint）。
  assert.match(src, /config\.llm\.maxContextToken/, '须有「上下文窗口」标签（i18n）')
  assert.match(
    src,
    /v-model\.number="localData\.maxContextToken"\s+min="0"\s+:max="CTX_TOKEN_MAX"\s+step="1"/,
    'maxContextToken 须绑定新名且值域 0~1e9/step1',
  )
  assert.match(src, /config\.llm\.maxContextTokenHint/, '须有 0 = 不启用兜底的 hint（i18n）')
  // 旧字段名不再出现（写入只用新名）。
  assert.doesNotMatch(src, /localData\.maxTokens\b/, '不得再绑定旧名 maxTokens')
  assert.doesNotMatch(src, /config\.llm\.maxTokens\b/, '不得再引用旧词条 config.llm.maxTokens')
})

test('① 位置：基本页 6 行 × 2 列（名称/协议 → 地址/模型 → 密钥/上下文 → 输出/迭代 → 推理/识图 → 思考/温度）', () => {
  const src = read(DIALOG)
  // 必填两列与实际字段绑定顺序（源码顺序 = 行优先：从左到右、从上到下）。
  const order = [
    'localData.name', 'localData.protocol',
    'localData.baseUrl', 'localData.model',
    'localData.apiKey', 'localData.maxContextToken',
    'localData.maxOutputToken', 'localData.maxToolIterations',
    'localData.capability.reasoning', 'localData.capability.vision',
    'localData.reasoningEffort', 'localData.temperature',
  ]
  let prev = -1
  for (const key of order) {
    const idx = src.indexOf(key)
    assert.ok(idx > 0, `字段 ${key} 须存在`)
    assert.ok(idx > prev, `字段顺序错误：${key} 须在上一个字段之后`)
    prev = idx
  }
  // 各字段所在 form-item 块为半行宽（一行两列）。
  for (const key of order) {
    const idx = src.indexOf(key)
    const block = src.slice(src.lastIndexOf('<div class="form-item', idx), idx)
    assert.match(block, /form-item-12/, `${key} 须为半行宽 form-item-12`)
  }
  // 必填标记：名称/协议/接口地址/模型/API 密钥/上下文窗口各带 *。
  for (const label of ['name', 'protocol', 'baseUrl', 'model', 'apiKey', 'maxContextToken']) {
    const idx = src.indexOf(`config.llm.${label}`)
    assert.ok(src.slice(idx, idx + 320).includes('required-mark'), `${label} 须有必填标记 *`)
  }
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
  assert.equal(zh.maxContextToken, '上下文窗口大小', 'zh 上下文窗口大小 须逐字一致')
})

// ═══════════════════════════════════════════════════════════════
// ④ Token 快捷值（128K/256K/512K/1M）+ 思考模式布局 + 模型能力（多选）
// ═══════════════════════════════════════════════════════════════
test('④ Token 快捷值：两项字段各挂 128K/256K/384K/512K/1M 按钮，点击即写对应字段', () => {
  const src = read(DIALOG)
  assert.match(src, /const tokenPresets = \[/, '须有 tokenPresets 常量表')
  for (const v of ['128000', '256000', '384000', '512000', '1000000']) {
    assert.ok(src.includes(`value: ${v}`), `须含快捷值 ${v}`)
  }
  for (const l of ['128K', '256K', '384K', '512K', '1M']) {
    assert.ok(src.includes(`label: '${l}'`), `须含按钮文案 ${l}`)
  }
  // 两项字段各自的 Label 行内挂同一组按钮（共用 tokenPresets）
  assert.match(src, /setToken\('maxOutputToken', p\.value\)/, '最大输出 Token 须挂快捷值按钮')
  assert.match(src, /setToken\('maxContextToken', p\.value\)/, '上下文窗口须挂快捷值按钮')
  assert.match(src, /function setToken\(field, value\)/, '须有写值函数')
})

test('④ 布局：思考模式为 low/high/max 下拉（绑定 reasoningEffort），不再有开关', () => {
  const src = read(DIALOG)
  const thinkIdx = src.indexOf('config.llm.thinking')
  const selectIdx = src.indexOf('<Select v-model="localData.reasoningEffort"')
  assert.ok(thinkIdx > 0 && selectIdx > 0, '思考模式标签与思考下拉均须存在')
  assert.ok(thinkIdx < selectIdx, '思考下拉须在思考模式标签之后')
  // 思考模式 = 单个 Select（low/high/max），与温度同排两列；旧「标签 + 开关」已移除。
  const block = src.slice(src.lastIndexOf('<div class="form-item', thinkIdx), src.indexOf('config.llm.temperature'))
  assert.match(block, /<Select v-model="localData\.reasoningEffort"/, '思考模式须为下拉')
  assert.doesNotMatch(block, /<Switch/, '思考模式不得再用开关')
  assert.doesNotMatch(src, /<Switch v-model="localData\.thinking"/, '不得再保留 thinking 开关')
  for (const lv of ['low', 'high', 'max']) {
    assert.match(src, new RegExp(`value: '${lv}'`), `思考下拉须含 ${lv} 档位`)
  }
  // 保存语义：选了档位 → thinking=true（兼容后端 thinking 键）；字段本身不清除。
  assert.match(src, /if \(effort\) localData\.thinking = true/, '思考档位非空须置 thinking=true')
  for (const loc of LOCALES) {
    const llm = readLocale(loc, 'config.json').llm
    assert.equal(llm.reasoningEffort, undefined, `${loc} 应回收 config.llm.reasoningEffort（label 已移除）`)
    assert.equal(llm.protocolHint, undefined, `${loc} 应回收 config.llm.protocolHint（说明已移除）`)
    for (const k of ['reasoningPlaceholder', 'reasoningLow', 'reasoningHigh', 'reasoningMax']) {
      assert.ok(typeof llm[k] === 'string' && llm[k].trim().length > 0, `${loc} 缺 config.llm.${k}`)
    }
  }
})

test('④ 能力双维度：两个数值输入（推理 / 识图）+ 旧声明式 capabilities 派生写入', () => {
  const src = read(DIALOG)
  assert.match(src, /config\.llm\.capabilityReasoning/, '须有「推理能力」标签（i18n）')
  assert.match(src, /config\.llm\.capabilityVision/, '须有「识图能力」标签（i18n）')
  // 两项各为档位下拉（优秀5/良好4/一般3/低2/较弱1/不支持0），分别绑定 capability.reasoning / capability.vision。
  for (const dim of ['reasoning', 'vision']) {
    assert.match(src, new RegExp(`v-model="localData\\.capability\\.${dim}"`), `${dim} 须绑定双维度字段`)
  }
  assert.match(src, /const CAPABILITY_TIERS = \[5, 4, 3, 2, 1, 0\]/, '档位下拉须含 5/4/3/2/1/0')
  assert.match(src, /:options="capabilityOptions\(localData\.capability\.reasoning\)"/, '推理能力须用档位下拉')
  assert.match(src, /:options="capabilityOptions\(localData\.capability\.vision\)"/, '识图能力须用档位下拉')
  // 旧记录：只有 capabilities 时按「具备 = 中位档」映射为双维度（不置空）。
  assert.match(src, /capabilityFromLegacyCaps/, '旧记录须按 capabilities 映射双维度')
  assert.match(src, /if \(!Array\.isArray\(localData\.capabilities\)\) localData\.capabilities = \[\]/, '旧记录须回落空数组')
  // 保存时派生 capabilities（截图门控等旧读侧契约）。
  assert.match(src, /localData\.capabilities = deriveCapabilities\(localData\.capability\)/, '保存须派生 capabilities')
  // 旧 checkbox 多选已由档位输入取代。
  assert.doesNotMatch(src, /type="checkbox" value="vision"/, '不得再保留旧「图形」checkbox')
  // 默认值：DEFAULT_LLM 含双维度初值 + capabilities 占位。
  const defaults = read(DEFAULTS)
  assert.match(defaults, /capability:\s*\{\s*reasoning:\s*3,\s*vision:\s*0\s*\}/, 'DEFAULT_LLM 须含 capability 双维度初值')
  assert.match(defaults, /capabilities:\s*\[\]/, 'DEFAULT_LLM 须含 capabilities 初值（派生占位）')
  // i18n 双语齐备
  for (const loc of LOCALES) {
    const llm = readLocale(loc, 'config.json').llm
    for (const k of ['capabilityReasoning', 'capabilityVision', 'capabilityReasoningHint', 'capabilityVisionHint', 'capabilityZeroHint']) {
      assert.ok(typeof llm[k] === 'string' && llm[k].trim().length > 0, `${loc} 缺 config.llm.${k}`)
    }
    // 旧单一「能力值 / 模型能力」词条已随 UI 回收
    for (const k of ['capability', 'capabilities', 'capReasoning', 'capVision']) {
      assert.equal(llm[k], undefined, `${loc} 应回收 config.llm.${k}`)
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
  // 🆕 2026-10-09：兜底守卫并入「防二次点击」（screenshotting 期间忽略）
  assert.match(src, /if \(!canScreenshot\.value \|\| screenshotting\.value\) return/, '事件处理须兜底（防旁路）+ 防二次点击')
  assert.match(src, /await captureWindow\('chat-screenshot'\)/, '截图须走两段式 gui.capture → gui.captured')
  // 配置变更（增删/改名/改能力）后须重解析默认 LLM，否则选择器与能力判定停在旧值（须重载才生效）
  assert.match(src, /if \(!llmOptions\.value\.some\(o => o\.value === selectedLLM\.value\)\)/, 'config-refresh 须重解析已失效/未选中的 LLM')
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    assert.ok(typeof chat.screenshot_no_vision === 'string' && chat.screenshot_no_vision.trim().length > 0,
      `${loc} 缺 chat.screenshot_no_vision`)
  }
})

// ═══════════════════════════════════════════════════════════════
// ⑥ 能力双维度（D-65，2026-11-04）：0~5 一位小数（步进 0.1），0 = 不具备；默认值 + 双语词条 + auto 选项
// ═══════════════════════════════════════════════════════════════
test('⑥ 能力双维度：档位下拉 + 0 提示 + 保存校验 + 默认值 + 双语词条 + auto 选项', () => {
  const src = read(DIALOG)
  assert.match(src, /capabilityOptions/, '档位须用下拉选项函数')
  assert.match(src, /capabilityExcellent/, '档位下拉须有优秀档文案')
  assert.match(src, /capabilityNone/, '档位下拉须有不支持（0）文案')
  assert.match(src, /capabilityZeroHint/, '0 = 不具备须就地提示（i18n）')
  assert.match(src, /reasoningUnavailable/, '推理 0 须就地提示')
  assert.match(src, /visionUnavailable/, '识图 0 须就地提示')
  // 两维度各自保存前校验。
  assert.match(src, /if \(!isValidCapability\(localData\.capability\.reasoning\)\)/, '保存前须校验推理档位')
  assert.match(src, /if \(!isValidCapability\(localData\.capability\.vision\)\)/, '保存前须校验识图档位')

  const rules = read('utils/fieldRules.js')
  assert.match(rules, /export const LLM_CAPABILITY_MAX = 5/, '档位上限 = 5')
  assert.match(rules, /export function isValidCapability\(/, '须有 isValidCapability 校验函数')
  assert.match(rules, /export function deriveCapabilities\(/, '须有 capabilities 派生函数')
  assert.match(rules, /export function capabilityFromLegacyCaps\(/, '须有旧声明式 → 双维度映射')

  // 列表页：展示两项档位（推理 / 识图），不再是单一「能力值」列。
  const list = read('views/config/SettingsLLMPage.vue')
  assert.match(list, /capabilityReasoning/, '列表页须展示推理档位')
  assert.match(list, /capabilityVision/, '列表页须展示识图档位')
  assert.doesNotMatch(list, /prop: 'capability'/, '列表页不得再以单一 capability 列展示')

  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    assert.ok(typeof chat.auto_llm === 'string' && chat.auto_llm.trim().length > 0,
      `${loc} 缺 chat.auto_llm（auto 选项文案）`)
  }

  // 聊天选择器出现 auto 选项（D-65：llm-start.llm = auto）。
  const chatSrc = read('views/chat/ChatPanel.vue')
  assert.match(chatSrc, /const AUTO_LLM = 'auto'/, '须定义 auto 保留名')
  assert.match(chatSrc, /\{ value: AUTO_LLM, label: t\('chat\.auto_llm'\)/, '选择器须前置 auto 选项')
})
