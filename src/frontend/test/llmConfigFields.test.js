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
  // 最大输出 Token：沿用现值域 256~65536/step256，名称/绑定改为 maxOutputToken。
  assert.match(src, /config\.llm\.maxOutputToken/, '须有「最大输出 Token」标签（i18n）')
  assert.match(
    src,
    /v-model\.number="localData\.maxOutputToken"\s+min="256"\s+max="65536"\s+step="256"/,
    'maxOutputToken 须绑定新名且值域 256~65536/step256',
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
