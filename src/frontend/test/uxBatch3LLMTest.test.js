/**
 * 用户视角缺陷「批 3」⑮ – LLM 测试连接（2026-09-20）：
 *   问题：设置 → LLM 原先只有「取消 / 保存」，用户配完 provider 无法确认能否连通，只能发消息试错。
 *   交付：编辑/新增对话框内「测试连接」= 用**当前表单配置**真实探活一次（只读：不落库、不改生效
 *   配置）→ 进行中 loading 防重复点击；成功显示延迟 + 回显模型名；失败显示人话文案（复用批 2
 *   的 `utils/errorMessage.js` 分类映射）+ 可展开原始详情。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * `*.vue` 源码守卫（与 uxBatch1/2 同法）+ 纯函数直调（errorMessage）+ i18n 实键校验
 * + **跨端主题字面量核对**（Go 服务端订阅点 chonkpilot-llm/server/llm_testconn.go）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { classifyError } from '../src/utils/errorMessage.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoDir = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const DIALOG = 'views/config/EditLLMDialog.vue'

/** 去掉 HTML / CSS / JS 注释后再做"字面量/禁用形态"扫描（注释里引用主题名是允许的） */
function stripComments(s) {
  return s
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

/** 极简 i18n 替身：从真实 locales JSON 取键 + `{name}` 插值 */
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

// ═══════════════════════════════════════════════════════════════
// ① 交互落点：按钮 / 加载态 / 必填禁用 / 防重复点击
// ═══════════════════════════════════════════════════════════════
test('⑮ 按钮：对话框内提供「测试连接」；进行中 loading、否则按必填项禁用', () => {
  const src = read(DIALOG)
  assert.match(src, /config\.llm\.testConnection/, '须有「测试连接」按钮文案（i18n，不得硬编码中文）')
  assert.match(src, /@click="testConnection"/, '按钮须触发探活')
  assert.match(src, /:loading="testing"/, '进行中须有 loading 态')
  assert.match(src, /:disabled="!canTest"/, '必填项缺失须禁用')
  assert.match(src, /const canTest = computed\(/, '须有必填项判定（baseUrl / model）')
  assert.match(src, /const testing = ref\(false\)/, '须有进行中状态')
  // 防重复点击：入口先判「进行中 / 未填必填项」
  const fn = src.match(/async function testConnection\(\)\s*\{[\s\S]*?\n\}/)
  assert.ok(fn, '未找到 testConnection')
  assert.match(fn[0], /if \(testing\.value \|\| !canTest\.value\) return/, '进行中 / 未填必填项不得重入')
  assert.match(fn[0], /finally \{\s*\n\s*testing\.value = false/, '须在 finally 复位（异常不卡 loading）')
})

// ═══════════════════════════════════════════════════════════════
// ② 主题与只读语义：点分相对主题直通总线；payload = 临时配置，不落库
// ═══════════════════════════════════════════════════════════════
test('⑮ 主题：点分相对主题 llm.test-connection，前端字面量 = Go 服务端订阅点', () => {
  const src = read(DIALOG)
  assert.match(src, /const TEST_CONN_TOPIC = 'llm\.test-connection'/, '须用点分相对主题（桥/服务端直通总线）')
  assert.match(src, /mq\.emit\(TEST_CONN_TOPIC, \{/, '须经统一 mq.emit 发送（禁直调 window.go.*）')

  const go = readFileSync(join(repoDir, 'lib', 'llm', 'server', 'llm_testconn.go'), 'utf8')
  assert.match(go, /SubjectLLMTestConnection = "llm\.test-connection"/, 'Go 服务端主题字面量须与前端一致')
  const goServer = readFileSync(join(repoDir, 'lib', 'llm', 'server', 'server.go'), 'utf8')
  assert.match(goServer, /\{SubjectLLMTestConnection, s\.onLLMTestConnection\}/, '服务端须在 Start 订阅该主题')
  // 只读探测：一次性 provider（LR-11 起 = 唯一名登记 → 探测后注销；不入 usr 配置、不触碰运行中 provider）
  assert.match(go, /router\.Spec\{[\s\S]*?BaseURL:\s+req\.BaseURL[\s\S]*?APIKey:\s+req\.APIKey[\s\S]*?DefaultModel:\s+req\.Model/, '须用给定配置构造一次性 provider（router.Spec）')
  assert.match(go, /s\.rt\.Unregister\(spec\.Name\)/, '一次性 provider 须在探测后注销（零残留）')
  assert.doesNotMatch(go, /llmClientFor\(/, '探活不得复用/写入服务端客户端缓存')
})

test('⑮ 只读：payload 只带当前表单配置；探活不保存配置', () => {
  const src = read(DIALOG)
  assert.match(
    src,
    /baseUrl: localData\.baseUrl\.trim\(\),[\s\S]*?model: localData\.model\.trim\(\),[\s\S]*?apiKey: localData\.apiKey \|\| '',[\s\S]*?protocol: localData\.protocol \|\| DEFAULT_LLM_PROTOCOL/,
    'payload 须为表单里的临时 provider 配置'
  )
  assert.doesNotMatch(src, /saveUserConfig/, '测试连接不得落库（只增探测，不写 usr 配置）')
})

// ═══════════════════════════════════════════════════════════════
// ③ 结果呈现：成功（延迟 + 回显模型）/ 失败（人话 + 原始详情）
// ═══════════════════════════════════════════════════════════════
test('⑮ 成功：显示延迟；上游回显模型名时一并显示（无回显则省略该段）', () => {
  const src = read(DIALOG)
  const fn = src.match(/const testOkText = computed\(\(\) => \{[\s\S]*?\n\}\)/)
  assert.ok(fn, '未找到 testOkText')
  assert.match(fn[0], /r\.model_echo\s*\n?\s*\?\s*t\('config\.llm\.testOkModel', \{ model: r\.model_echo, latency: r\.latency_ms \}\)/, '有回显 → 模型名 + 延迟')
  assert.match(fn[0], /:\s*t\('config\.llm\.testOk', \{ latency: r\.latency_ms \}\)/, '无回显 → 仅延迟')
  assert.match(src, /class="test-conn-ok"/, '成功文案须渲染')
  assert.match(src, /v-if="testing"/, '进行中须有独立呈现（不经成功/失败分支）')
})

test('⑮ 失败：人话文案（复用 errorMessage 分类映射）+ 可展开原始详情（原始串不丢弃）', () => {
  const src = read(DIALOG)
  assert.match(src, /import \{ classifyError \} from '\.\.\/\.\.\/utils\/errorMessage'/, '须复用批 2 的分类映射（不另造一套）')
  const fn = src.match(/const testFailText = computed\(\(\) => \{[\s\S]*?\n\}\)/)
  assert.ok(fn, '未找到 testFailText')
  assert.match(fn[0], /classifyError\(r\.error \? r\.error\.message : ''\)/, '须对后端原始错误串分类')
  assert.match(fn[0], /t\(cls\.key, cls\.params\)/, '须渲染人话 i18n 文案')
  assert.match(fn[0], /kind === 'invalid'[\s\S]*config\.llm\.testInvalid/, '入参不合法单独给文案（前置检查）')
  assert.match(src, /class="test-conn-fail"/, '失败文案须渲染')
  assert.match(src, /chat\.error_detail_label/, '须有「原始错误」折叠入口（i18n）')
  assert.match(src, /v-if="testDetailOpen"/, '原始详情须按需展开')
  assert.match(src, /class="test-conn-raw">\{\{ testDetail \}\}/, '展开内容 = 原样保留的原始串')
  // 后端无应答（桥/服务端未接）→ 明确失败，不假成功
  const body = src.match(/const env = await mq\.emit\(TEST_CONN_TOPIC, \{[\s\S]*?\n  \}/)
  assert.ok(body, '未找到探活主体')
  assert.match(body[0], /\(backend && backend\.result\) \|\| \{/, '无结果信封时须构造失败（不得呈现成功）')
  assert.match(body[0], /ok: false/, '无应答 = 失败')
})

// ═══════════════════════════════════════════════════════════════
// ④ 分类映射口径（纯函数）：逐类命中人话 key + 原始详情不丢弃 + 双语可渲染
// ═══════════════════════════════════════════════════════════════
test('⑮ 分类：LLM 探活失败串逐类命中人话 key（401/连接/超时/限流/服务端/协议）', () => {
  const CASES = [
    // 后端 llmTestConnFail 形态：`[<kind>] <msg>`（llm.go LLMError.Error()）
    ['[auth] llm http 401: {"error":{"message":"Incorrect API key provided: sk-***"}}', 'chat.error_auth'],
    ['[network] Post "http://127.0.0.1:11434/v1/chat/completions": dial tcp 127.0.0.1:11434: connect: connection refused', 'chat.error_network'],
    ['[timeout] 探活超时（15s）', 'chat.error_timeout'],
    ['[rate_limit] llm http 429: Too Many Requests', 'chat.error_rate_limit'],
    ['[server] llm http 503: upstream unavailable', 'chat.error_server'],
    ['[protocol] llm http 404: model not found', 'chat.error_protocol'],
    ['[protocol] 探活无响应（流已结束，未返回任何事件）', 'chat.error_protocol'],
  ]
  for (const [raw, key] of CASES) {
    const cls = classifyError(raw)
    assert.equal(cls.key, key, `「${raw}」应映射到 ${key}（实际 ${cls.key}）`)
    assert.equal(cls.detail, raw, '原始详情须原样保留（可展开核对）')
    for (const loc of LOCALES) {
      const i18n = mkI18n({ chat: readLocale(loc, 'chat.json') })
      const text = i18n.t(cls.key, cls.params)
      assert.notEqual(text, cls.key, `${loc} 文案键未命中 i18n：${cls.key}`)
      assert.doesNotMatch(text, /\{\w+\}/, `${loc} 文案残留占位符：${text}`)
      assert.notEqual(text, cls.detail, `${loc} 人话文案不得等于原始串`)
    }
  }
  // 脱敏后的鉴权串仍分类为 auth（Key 不入文案）
  const cls = classifyError('[auth] llm http 401: Incorrect API key provided: sk-***')
  assert.equal(cls.key, 'chat.error_auth')
  assert.deepEqual(cls.params, { status: '401' })
  assert.doesNotMatch(mkI18n({ chat: readLocale('zh-CN', 'chat.json') }).t(cls.key, cls.params), /sk-/, '人话文案不得带任何 Key 片段')
})

// ═══════════════════════════════════════════════════════════════
// ⑤ i18n：新增键双语齐备 + 占位符可插值；分类映射依赖键齐备
// ═══════════════════════════════════════════════════════════════
test('⑮ i18n：config.llm 新增键 zh/en 齐备且占位符可插值', () => {
  const KEYS = ['testConnection', 'testing', 'testOk', 'testOkModel', 'testInvalid']
  for (const loc of LOCALES) {
    const llm = readLocale(loc, 'config.json').llm
    for (const k of KEYS) {
      assert.ok(typeof llm[k] === 'string' && llm[k].trim().length > 0, `${loc} 缺 config.llm.${k}`)
    }
    assert.match(llm.testOk, /\{latency\}/, `${loc} testOk 须含 {latency}`)
    assert.match(llm.testOkModel, /\{model\}/, `${loc} testOkModel 须含 {model}`)
    assert.match(llm.testOkModel, /\{latency\}/, `${loc} testOkModel 须含 {latency}`)
  }
  const zh = mkI18n({ config: readLocale('zh-CN', 'config.json') })
  const en = mkI18n({ config: readLocale('en-US', 'config.json') })
  assert.match(zh.t('config.llm.testOkModel', { model: 'gpt-4o', latency: 12 }), /gpt-4o/, 'zh 模型名须插值')
  assert.match(en.t('config.llm.testOkModel', { model: 'gpt-4o', latency: 12 }), /gpt-4o/, 'en 模型名须插值')
  for (const [loc, i18n] of [['zh-CN', zh], ['en-US', en]]) {
    const text = i18n.t('config.llm.testOkModel', { model: 'gpt-4o', latency: 12 })
    assert.doesNotMatch(text, /\{\w+\}/, `${loc} 文案残留占位符：${text}`)
    assert.match(text, /12/, `${loc} 延迟须插值`)
  }

  // 分类映射消费的 chat.error_* 键（跨命名空间依赖）双语齐备
  const USED = ['error_auth', 'error_network', 'error_timeout', 'error_rate_limit', 'error_server', 'error_protocol', 'error_unknown', 'error_detail_label']
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    for (const k of USED) {
      assert.ok(typeof chat[k] === 'string' && chat[k].trim().length > 0, `${loc} 缺 chat.${k}`)
    }
  }
})

// ═══════════════════════════════════════════════════════════════
// ⑥ 安全 / 前端规范
// ═══════════════════════════════════════════════════════════════
test('⑮ 安全：API Key 不渲染、不打印；样式走主题 token；无 watch/watchEffect', () => {
  const raw = read(DIALOG)
  const src = stripComments(raw)
  assert.doesNotMatch(src, /\{\{[^}]*localData\.apiKey/, 'API Key **值**不得插值到模板（仅密码输入框 + 探活 payload）')
  assert.doesNotMatch(src, /console\.(log|info|debug)\(/, '不得打印配置载荷（含 Key）')
  assert.match(src, /type="password"/, 'API Key 输入仍为密码框')
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect（项目规则）')
  assert.doesNotMatch(src, /gui\.|window\.go\./, '不得绕过 mq 直调宿主（统一 mq.emit）')
  for (const sel of ['.test-conn-ok', '.test-conn-fail', '.test-conn-raw']) {
    const rule = raw.match(new RegExp(sel.replace('.', '\\.') + '\\s*\\{[\\s\\S]*?\\n\\}'))
    assert.ok(rule, `未找到 ${sel} 规则`)
    assert.doesNotMatch(rule[0], /#[0-9a-f]{3,8}|rgba?\(/i, `${sel} 不得硬编码色值（走主题 token）`)
  }
})
