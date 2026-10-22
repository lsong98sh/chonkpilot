/**
 * AICFG-1 配置页 / 工具原语「AI 智能填充」（2026-10-16）：
 *   自然语言描述 → LLM 产出结构化字段建议 → **只回填表单待保存态**（标「AI 建议」，不落库）
 *   → 复用既有校验（LLM【测试连接】/ 工具保存后契约生效）→ 用户【保存】；apiKey 绝不代填。
 *
 * 覆盖：① 消息面登记（61 schema + msgkeys + Go 消费/订阅 + browser 白名单）；
 *       ② R3/R4/R5 前端落点（字段映射 + 「AI 建议」标注 + 零 watch）；
 *       ③ §4 白名单（可填/禁填）在 Go 与前端两侧一致。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ `*.vue` 源码守卫 + 契约/schema 实键校验
 * （与 uxBatch3LLMTest / contractReconcile 同法）。
 * 消息面消费端对账（mq 驱动 + 监听断言）= Go 侧 configsuggest_test.go。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { MsgTopics } from '../src/events/msgkeys.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoDir = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const readGo = (rel) => readFileSync(join(repoDir, 'lib', rel), 'utf8')
const readRepo = (rel) => readFileSync(join(repoDir, rel), 'utf8')
const LOCALES = ['zh-CN', 'en-US']

const contract = JSON.parse(readRepo(join('..', 'docs', 'spec', '60-reference', '61-messages.schema.json')))

// ═══════════════════════════════════════════════════════════════
// ① 消息面：契约 + msgkeys + Go 消费/订阅 + browser 白名单
// ═══════════════════════════════════════════════════════════════
test('① 契约：config.suggest 已登记（payload/result 精确形状）', () => {
  const topic = contract.topics.find((t) => t.topic === 'config.suggest')
  assert.ok(topic, '契约缺 config.suggest 主题')
  assert.equal(topic.face, 'gui')
  assert.equal(topic.direction, 'client->server')
  assert.deepEqual(Object.keys(topic.payload).sort(), ['current', 'instance_id', 'kind', 'text'])
  assert.deepEqual(Object.keys(topic.result).sort(), ['error', 'fields', 'notes', 'ok'])
  assert.equal(topic.payload.instance_id.required, true)
  assert.equal(topic.payload.kind.required, true)
  assert.equal(topic.payload.text.required, true)
  assert.equal(topic.payload.current.required, false)
  assert.equal(topic.result.ok.required, true)
  assert.equal(topic.result.fields.required, false)
})

test('① msgkeys：genmsg 已生成 MsgTopics.configSuggest = config.suggest', () => {
  assert.equal(MsgTopics.configSuggest, 'config.suggest')
  assert.ok(read('events/msgkeys.js').includes("configSuggest: 'config.suggest'"), 'msgkeys.js 缺 configSuggest')
})

test('① Go 后端：主题常量 + onConfigSuggest 订阅', () => {
  const go = readGo('llm/server/configsuggest.go')
  assert.match(go, /SubjectConfigSuggest = msgkeys\.TopicConfigSuggest/, 'Go 服务端主题须用生成键常量')
  assert.match(go, /func \(s \*Server\) onConfigSuggest\(/, '缺 onConfigSuggest handler')
  assert.match(go, /s\.promptOptimiseSpec\(/, 'provider 须现读 llm.promptOptimise（与 prompt-optimise 同口径）')
  assert.match(go, /v\.Result = configSuggestFail\(/, '失败须恒走 result 信封（不抛总线错误）')
  const serverGo = readGo('llm/server/server.go')
  assert.match(serverGo, /\{SubjectConfigSuggest, s\.onConfigSuggest\}/, 'server.go Start 须订阅 config.suggest')
  // PRODSIM-1：browser 形态（httpapi 上行白名单）已删 → 该白名单守卫随文件移除。
})

test('① 前端消费：api/config.js suggestConfig 用 MsgTopics.configSuggest（禁字面量）', () => {
  const cfg = read('api/config.js')
  assert.match(cfg, /export function suggestConfig\(kind, text, current\)/, '缺 suggestConfig')
  assert.match(cfg, /mq\.emit\(MsgTopics\.configSuggest, body, \{ timeout: 90000 \}\)/, '须经 MsgTopics 常量发送（非字面量）+ 超时裕量')
  assert.doesNotMatch(cfg, /mq\.emit\(\s*'config\.suggest'/, '不得用主题字面量（契约主题须走 MsgTopics）')
})

// ═══════════════════════════════════════════════════════════════
// ② §4 白名单（可填/禁填）——Go 侧与前端侧一致
// ═══════════════════════════════════════════════════════════════
test('② Go 白名单：三 kind 逐项对照设计稿 §4；apiKey/level/沙箱等禁填字段不在白名单', () => {
  const go = readGo('llm/server/configsuggest.go')
  const m = go.match(/configSuggestWhitelist = map\[string\]\[\]string\{([\s\S]*?)\n\}/)
  assert.ok(m, '未找到 configSuggestWhitelist')
  const wl = m[1]
  // llm：✅ name/model/protocol/thinking/reasoningEffort/capabilities + ⚠️ baseUrl/maxContextToken/maxOutputToken/temperature/maxToolIterations
  for (const f of ['"name"', '"model"', '"protocol"', '"baseUrl"', '"maxContextToken"', '"maxOutputToken"',
    '"thinking"', '"reasoningEffort"', '"capabilities"', '"temperature"', '"maxToolIterations"']) {
    assert.ok(wl.includes(f), `llm 白名单缺 ${f}`)
  }
  // mcp：✅ name/transport/description/url + ⚠️ runtime/args/env/namespace/timeout
  for (const f of ['"transport"', '"description"', '"url"', '"runtime"', '"args"', '"env"', '"namespace"', '"timeout"']) {
    assert.ok(wl.includes(f), `mcp 白名单缺 ${f}`)
  }
  // tool：✅ title/description/parameters/content + meta/refs/delegate
  for (const f of ['"title"', '"description"', '"parameters"', '"content"', '"meta"', '"refs"', '"delegate"']) {
    assert.ok(wl.includes(f), `tool 白名单缺 ${f}`)
  }
  // 禁填字段**绝不出现在任何白名单**（apiKey / level / enabled / isolate / sandbox / hot_tools）
  for (const f of ['"apiKey"', '"level"', '"enabled"', '"isolate"', '"sandbox"', '"hot_tools"']) {
    assert.ok(!wl.includes(f), `禁填字段 ${f} 不得在白名单`)
  }
})

test('② R3 LLM：AI 回填白名单 = 与后端一致，apiKey 不在内', () => {
  const src = read('views/config/EditLLMDialog.vue')
  const m = src.match(/const AI_LLM_FIELDS = \[([\s\S]*?)\]/)
  assert.ok(m, '缺 AI_LLM_FIELDS')
  for (const f of ['name', 'model', 'protocol', 'baseUrl', 'maxContextToken', 'maxOutputToken',
    'thinking', 'reasoningEffort', 'capabilities', 'temperature', 'maxToolIterations']) {
    assert.ok(m[1].includes("'" + f + "'"), `AI_LLM_FIELDS 缺 ${f}`)
  }
  assert.ok(!m[1].includes("'apiKey'"), 'apiKey 绝不代填（不在 AI 回填白名单）')
})

test('② R4 MCP：AI 回填白名单含 transport 联动；level/enabled/沙箱/hot_tools 禁填', () => {
  const src = read('views/config/EditMCPDialog.vue')
  const m = src.match(/const AI_MCP_FIELDS = \[([\s\S]*?)\]/)
  assert.ok(m, '缺 AI_MCP_FIELDS')
  for (const f of ['name', 'transport', 'description', 'url', 'runtime', 'args', 'env', 'namespace', 'timeout']) {
    assert.ok(m[1].includes("'" + f + "'"), `AI_MCP_FIELDS 缺 ${f}`)
  }
  for (const f of ['level', 'enabled', 'isolate', 'sandbox', 'hot_tools']) {
    assert.ok(!m[1].includes("'" + f + "'"), `禁填字段 ${f} 不得在 AI 回填白名单`)
  }
  // transport 联动的显隐由既有 showUrl/showSpawn 承担（回填写 transport ref 即自动联动）
  assert.match(src, /transport\.value = tv/, 'transport 回填须写 transport ref（联动 showUrl/showSpawn）')
  assert.match(src, /const ALLOWED_MCP_TRANSPORT = \['stdio', 'http', 'sse'\]/, 'transport 取值须校验')
})

// ═══════════════════════════════════════════════════════════════
// ③ R3/R4 UI 落点 + 「AI 建议」标注 + 零 watch + 不落库
// ═══════════════════════════════════════════════════════════════
test('③ R3 LLM 弹窗：AI 填充入口 + 「AI 建议」标注 + 用户手填即清除', () => {
  const src = read('views/config/EditLLMDialog.vue')
  assert.match(src, /import \{ suggestConfig \} from '\.\.\/\.\.\/api\/config'/, '须复用 suggestConfig')
  assert.match(src, /suggestConfig\('llm', text, current\)/, '须发 kind=llm')
  assert.match(src, /config\.llm\.aiFill/, '缺「AI 填充」入口（i18n）')
  assert.match(src, /v-if="aiMark\('model'\)"/, '缺「AI 建议」字段标记')
  assert.match(src, /function aiMark\(key\)/, '缺 aiMark')
  assert.match(src, /@input="clearAiMarks"/, '用户手填须清「AI 建议」标记（零 watch）')
  assert.match(src, /const aiLoading = ref\(false\)/, '缺进行中状态（防重复点击）')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect')
  assert.doesNotMatch(src, /saveUserConfig|data-user-config-save/, 'AI 填充不落库（只改待保存态）')
  assert.doesNotMatch(src, /gui\.|window\.go\./, '不得绕过 mq 直调宿主')
})

test('③ R4 MCP 弹窗：AI 填充入口 + 「AI 建议」标注 + 零 watch', () => {
  const src = read('views/config/EditMCPDialog.vue')
  assert.match(src, /import \{ suggestConfig \} from '\.\.\/\.\.\/api\/config'/, '须复用 suggestConfig')
  assert.match(src, /suggestConfig\('mcp', text, current\)/, '须发 kind=mcp')
  assert.match(src, /config\.mcp\.aiFill/, '缺「AI 填充」入口（i18n）')
  assert.match(src, /v-if="aiMark\('transport'\)"/, '缺「AI 建议」字段标记')
  assert.match(src, /@input="clearAiMarks"/, '用户手填须清「AI 建议」标记（零 watch）')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect')
})

// ═══════════════════════════════════════════════════════════════
// ④ R5 工具原语：升级为整份契约 + 落库仍走既有保存通道
// ═══════════════════════════════════════════════════════════════
test('④ R5 原语：整份契约回填（title/description/parameters/content/meta/refs/delegate）+ 不改写盘通道', () => {
  const src = read('views/knowledge/PrimitivePanel.vue')
  assert.match(src, /suggestConfig\('tool', text, \{/, '须发 kind=tool')
  assert.match(src, /function applyContractSuggest\(fields\)/, '缺整份契约回填')
  for (const f of ['fields.title', 'fields.description', 'fields.parameters', 'fields.content', 'fields.meta', 'fields.refs', 'fields.delegate']) {
    assert.ok(src.includes(f), `整份契约回填缺 ${f}`)
  }
  assert.match(src, /setMeta\(k,/, 'meta 须写回基本信息表单')
  assert.match(src, /savePrimitive\(props\.path, currentDoc\(\)\)/, '落库仍走既有 data-knowledge-save（savePrimitive）')
  assert.match(src, /ai_applied_note/, '缺「AI 建议」提示（i18n）')
  assert.match(src, /@input="clearAiApplied"/, '用户手填须清「AI 建议」提示（零 watch）')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect')
  // 落库仍由【保存】触发，AI 生成过程不动落库
  assert.match(src, /async function handleGenerate\(\)[\s\S]*?suggestConfig\('tool'/, 'handleGenerate 须走 config.suggest')
})

// ═══════════════════════════════════════════════════════════════
// ⑤ i18n：新增键双语齐备 + 占位符可插值
// ═══════════════════════════════════════════════════════════════
test('⑤ i18n：config.llm / config.mcp / knowledgeList 新增键 zh/en 齐备', () => {
  const LLM_KEYS = ['aiFill', 'aiFillPlaceholder', 'aiFillHint', 'aiGenerating', 'aiSuggested', 'aiFillInvalid', 'aiFillNote']
  const MCP_KEYS = ['aiFill', 'aiFillPlaceholder', 'aiFillHint', 'aiGenerating', 'aiSuggested', 'aiFillInvalid', 'aiFillNote']
  const KL_KEYS = ['ai_generate', 'ai_generate_title', 'ai_generate_hint', 'ai_generate_input', 'ai_generate_confirm',
    'ai_generate_done', 'ai_generate_input_required', 'ai_applied_note']
  for (const loc of LOCALES) {
    const cfg = readLocale(loc, 'config.json')
    for (const k of LLM_KEYS) {
      assert.ok(typeof cfg.llm[k] === 'string' && cfg.llm[k].trim().length > 0, `${loc} 缺 config.llm.${k}`)
    }
    for (const k of MCP_KEYS) {
      assert.ok(typeof cfg.mcp[k] === 'string' && cfg.mcp[k].trim().length > 0, `${loc} 缺 config.mcp.${k}`)
    }
    const kl = readLocale(loc, 'knowledgeList.json')
    for (const k of KL_KEYS) {
      assert.ok(typeof kl[k] === 'string' && kl[k].trim().length > 0, `${loc} 缺 knowledgeList.${k}`)
    }
    assert.match(cfg.llm.aiFillNote, /\{notes\}/, `${loc} config.llm.aiFillNote 须含 {notes}`)
    assert.match(cfg.mcp.aiFillNote, /\{notes\}/, `${loc} config.mcp.aiFillNote 须含 {notes}`)
    assert.match(kl.ai_applied_note, /\{fields\}/, `${loc} knowledgeList.ai_applied_note 须含 {fields}`)
  }
})

// ═══════════════════════════════════════════════════════════════
// ⑥ ok=false 错误分支渲染（api 分流 + 三处就地提示，禁静默失败）+
//    三处零 watch / 无 window.go.* 直调
// ═══════════════════════════════════════════════════════════════
test('⑥ 错误分支：api 按 result.ok 分流（e.kind）+ 三处就地渲染可读错误（不静默失败）', () => {
  // api/config.js：失败 reject，error.kind 透传供调用方分流（invalid 等）
  const cfg = read('api/config.js')
  assert.match(cfg, /if \(!result\.ok\)/, 'suggestConfig 须据 result.ok 分流')
  assert.match(cfg, /e\.kind = result\.error && result\.error\.kind/, 'Error.kind 须取自 result.error.kind')
  // R3/R4：错误就地渲染（.ai-fill-error）+ invalid 专用文案 + 其余复用 classifyError 分类映射
  for (const [file, ns] of [
    ['views/config/EditLLMDialog.vue', 'config.llm'],
    ['views/config/EditMCPDialog.vue', 'config.mcp'],
  ]) {
    const src = read(file)
    assert.match(src, /<div v-if="aiError" class="ai-fill-error">\{\{ aiError \}\}<\/div>/, `${ns} 缺错误就地渲染`)
    assert.match(src, new RegExp(`aiError\\.value = t\\('${ns.replace('.', '\\.')}\\.aiFillInvalid'\\)`), `${ns} 缺 invalid 分支文案`)
    assert.match(src, /const cls = classifyError\(msg\)/, `${ns} 缺 classifyError 分类映射`)
    assert.match(src, /aiError\.value = t\(cls\.key, cls\.params\)/, `${ns} 错误文案须按分类映射`)
  }
  // R5：弹框内就地渲染（.pf-ai-error）+ invalid 专用文案 + 其余 classifyError
  const prim = read('views/knowledge/PrimitivePanel.vue')
  assert.match(prim, /<div v-if="aiError" class="pf-ai-error">\{\{ aiError \}\}<\/div>/, 'R5 缺错误就地渲染')
  assert.match(prim, /aiError\.value = t\('knowledgeList\.ai_generate_input_required'\)/, 'R5 缺 invalid 分支文案')
  assert.match(prim, /aiError\.value = t\(cls\.key, cls\.params\)/, 'R5 错误文案须按分类映射')
})

test('⑥ 三处均零 watch（含 Effect）+ 无 window.go.* 直调', () => {
  for (const f of [
    'views/config/EditLLMDialog.vue',
    'views/config/EditMCPDialog.vue',
    'views/knowledge/PrimitivePanel.vue',
  ]) {
    const src = read(f)
    assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, `${f} 不得用 watch/watchEffect`)
    assert.doesNotMatch(src, /window\.go\./, `${f} 不得直调 window.go.*（须走 utils/*.js → mq）`)
  }
})
