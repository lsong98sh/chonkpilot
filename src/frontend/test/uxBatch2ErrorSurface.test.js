/**
 * 用户视角缺陷「批 2」前端单测（2026-09-20）– 错误呈现面：
 *   A 错误「人话化」分类映射（`src/utils/errorMessage.js`）：逐类断言人话 key + 原始详情保留
 *     + 未知兜底；覆盖仓库实际会出现的错误形态（grep 核实）：
 *       · `*LLMError.Error()` = `[<kind>] <msg>`（chonkpilot-llm/server/llm.go:44-64）
 *       · JSON-RPC 码 -32700/-32602/-32601/-32000（chonkpilot-mcp-gateway/gateway/mcpgateway.go）
 *       · `exit status N`（Go exec.ExitError；mcp-tools/internal/scriptrun/exec.go:176）
 *       · 轮次错误码 DB_ERROR / TOOL_LOOP_LIMIT / EMPTY_REPLY（llm/server/turn.go、server.go）
 *   B 应用点：会话错误气泡（sessionMessages.handleError）+ 工具卡失败结果（MessageItem）
 *   C 上滑加载更多失败 → 用户可见轻提示（原先只 console）
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * 纯函数直调（errorMessage）+ i18n 实键校验 + `*.vue`/`*.js` 源码守卫（与 uxBatch1 同法）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { classifyError, ERROR_KEYS } from '../src/utils/errorMessage.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

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
const zhChat = mkI18n({ chat: readLocale('zh-CN', 'chat.json') })
const enChat = mkI18n({ chat: readLocale('en-US', 'chat.json') })

/** 逐类样例：`raw` = 仓库实际会出现的原始错误串（含来源注释），`key` = 期望人话 i18n 键 */
const CASES = [
  // *LLMError{Kind:timeout}：Post "http://…": context deadline exceeded（router：adaptor 传输/超时归类）
  { name: '超时', raw: '[timeout] Post "http://127.0.0.1:11434/v1/chat/completions": context deadline exceeded', key: 'chat.error_timeout' },
  { name: '超时（i/o timeout）', raw: 'dial tcp 10.0.0.5:443: i/o timeout', key: 'chat.error_timeout' },
  // *LLMError{Kind:network}：连接被拒（llm.go:45）
  { name: '连接失败', raw: '[network] Post "http://127.0.0.1:11434/v1/chat/completions": dial tcp 127.0.0.1:11434: connect: connection refused', key: 'chat.error_network' },
  { name: '连接失败（reset）', raw: 'read tcp 127.0.0.1:52001->127.0.0.1:11434: read: connection reset by peer', key: 'chat.error_network' },
  { name: '域名解析失败', raw: 'Post "https://api.example.com/v1/chat/completions": dial tcp: lookup api.example.com: no such host', key: 'chat.error_network' },
  // *LLMError{Kind:auth}：llm http 401（router canonical：canon.KindFromStatus → adaptor.StatusError）
  { name: '鉴权失败', raw: '[auth] llm http 401: {"error":{"message":"Incorrect API key provided"}}', key: 'chat.error_auth' },
  { name: '鉴权失败（403）', raw: 'llm http 403: forbidden', key: 'chat.error_auth' },
  // *LLMError{Kind:rate_limit}：429
  { name: '限流', raw: '[rate_limit] llm http 429: Too Many Requests', key: 'chat.error_rate_limit' },
  // *LLMError{Kind:server}：5xx
  { name: '服务端错误', raw: '[server] llm http 502: <html>bad gateway</html>', key: 'chat.error_server' },
  // 磁盘 / 权限（filesys/脚本类工具底层 OS 错误）
  { name: '磁盘空间不足', raw: '写入失败: write /data/x.bin: no space left on device', key: 'chat.error_disk' },
  { name: '权限不足', raw: '错误: open C:\\Windows\\system32\\drivers\\etc\\hosts: permission denied', key: 'chat.error_permission' },
  // 进程退出（scriptrun/exec.go:176 → `脚本执行失败（exit 1）：exit status 1`）
  { name: '进程退出', raw: '错误: 脚本执行失败（exit 1）：exit status 1', key: 'chat.error_process' },
  // JSON-RPC 协议/参数（mcpgateway.go methodError）
  { name: '协议错误', raw: '错误: -32602: invalid params', key: 'chat.error_protocol' },
  { name: '方法不存在', raw: 'methodError -32601: method not found', key: 'chat.error_protocol' },
  // 轮次错误码（无 message 时按 code 归类）
  { name: 'DB 错误', raw: 'disk I/O error', code: 'DB_ERROR', key: 'chat.error_db' },
  { name: '工具循环超限', raw: '', code: 'TOOL_LOOP_LIMIT', key: 'chat.error_tool_loop' },
  { name: '空回复', raw: 'LLM 返回空回复', code: 'EMPTY_REPLY', key: 'chat.empty_reply' },
  // 未知兜底
  { name: '未知兜底', raw: 'boom', key: 'chat.error_unknown' },
]

// ═══════════════════════════════════════════════════════════════
// A 分类映射（纯函数）
// ═══════════════════════════════════════════════════════════════
test('A·分类：逐类命中人话 key（超时/连接/鉴权/限流/服务端/磁盘/权限/进程/协议/码/兜底）', () => {
  for (const c of CASES) {
    const input = c.code != null ? { message: c.raw, code: c.code } : c.raw
    const cls = classifyError(input)
    assert.equal(cls.key, c.key, `${c.name} 应映射到 ${c.key}（实际 ${cls.key}，原文 ${JSON.stringify(c.raw)}）`)
  }
})

test('A·原始详情不被丢弃：detail 与入参原文逐字一致（含 `错误: ` 前缀、多行）', () => {
  for (const c of CASES) {
    const input = c.code != null ? { message: c.raw, code: c.code } : c.raw
    const cls = classifyError(input)
    const expect = c.raw || c.code // 原文为空时回落 code（不伪造）
    assert.equal(cls.detail, expect, `${c.name} 原始详情必须原样保留`)
  }
  // 多行 / 长串不裁剪
  const multi = '错误: 脚本执行失败（exit 2）：exit status 2\nstdout: partial output\nstderr: boom'
  assert.equal(classifyError(multi).detail, multi, '多行原始串不得裁剪')
})

test('A·params：鉴权/限流/服务端带状态码，其余为空对象（不留空占位）', () => {
  assert.deepEqual(classifyError('[auth] llm http 401: Incorrect API key').params, { status: '401' })
  assert.deepEqual(classifyError('llm http 403: forbidden').params, { status: '403' })
  // `[auth]` 命中但无 4xx 码 → 有意义的兜底（不是空串）
  assert.deepEqual(classifyError('[auth] invalid api key').params, { status: '401/403' })
  assert.deepEqual(classifyError('[rate_limit] llm http 429').params, { status: '429' })
  assert.deepEqual(classifyError('[server] llm http 503: unavailable').params, { status: '503' })
  // `[server]` 命中但无 http 码 → 5xx 兜底
  assert.deepEqual(classifyError('[server] upstream failed').params, { status: '5xx' })
  assert.deepEqual(classifyError('context deadline exceeded').params, {}, '无参类别返回空对象')
  assert.deepEqual(classifyError('boom').params, {}, '未知兜底返回空对象')
})

test('A·入参形态：string / Error / {message,code} / null 均可用且不抛', () => {
  assert.equal(classifyError(new Error('connection refused')).key, 'chat.error_network')
  assert.equal(classifyError(new Error('connection refused')).detail, 'connection refused')
  assert.equal(classifyError({ message: 'context deadline exceeded' }).key, 'chat.error_timeout')
  assert.equal(classifyError(null).key, 'chat.error_unknown')
  assert.equal(classifyError(undefined).detail, '')
  assert.equal(classifyError({}).key, 'chat.error_unknown', '空对象 → 未知兜底（不抛）')
  assert.equal(classifyError(12345).detail, '12345', '非串入参不抛')
})

test('A·优先级：超时先于网络、磁盘先于协议（组合串不被误吃）', () => {
  // 既含 dial 又含 timeout → 超时（超时是更可行动的信息）
  assert.equal(classifyError('dial tcp 1.2.3.4:443: i/o timeout').key, 'chat.error_timeout')
  // 同时含 JSON-RPC 码与磁盘错误 → 磁盘（数字码只是噪音）
  assert.equal(classifyError('-32000: write x.bin: no space left on device').key, 'chat.error_disk')
})

// ═══════════════════════════════════════════════════════════════
// A i18n：键齐备 + 人话文案可渲染（zh-CN / en-US 双语齐备，无硬编码）
// ═══════════════════════════════════════════════════════════════
test('A·i18n：ERROR_KEYS 全键 zh/en 齐备、非空、且插值后无残留占位符', () => {
  for (const loc of LOCALES) {
    const chat = readLocale(loc, 'chat.json')
    for (const k of ERROR_KEYS) {
      const short = k.replace('chat.', '')
      const v = chat[short]
      assert.ok(typeof v === 'string' && v.trim().length > 0, `${loc} 缺 chat.${short}`)
    }
    assert.ok(typeof chat.error_detail_label === 'string' && chat.error_detail_label.length > 0,
      `${loc} 缺 chat.error_detail_label（原始错误折叠标签）`)
  }
})

test('A·i18n：逐类样例渲染出人话文案（含状态码插值；绝不等于原始串）', () => {
  for (const c of CASES) {
    const input = c.code != null ? { message: c.raw, code: c.code } : c.raw
    const cls = classifyError(input)
    for (const [loc, i18n] of [['zh-CN', zhChat], ['en-US', enChat]]) {
      const text = i18n.t(cls.key, cls.params)
      assert.notEqual(text, cls.key, `${loc} ${c.name} 文案键未命中 i18n`)
      assert.ok(text.length > 0, `${loc} ${c.name} 文案为空`)
      assert.doesNotMatch(text, /\{\w+\}/, `${loc} ${c.name} 文案残留占位符：${text}`)
      if (cls.detail) assert.notEqual(text, cls.detail, `${loc} ${c.name} 文案不得等于原始串`)
    }
  }
  // 人话文案与原始串的直观对照（便于人工核对）
  console.log('[err] 超时 →', zhChat.t('chat.error_timeout'),
    '| 原文:', '[timeout] … context deadline exceeded')
  console.log('[err] 协议 →', zhChat.t('chat.error_protocol'), '| 原文:', '-32602 invalid params')
  console.log('[err] 鉴权 →', zhChat.t('chat.error_auth', { status: '401' }))
})

// ═══════════════════════════════════════════════════════════════
// B 应用点 1：会话错误气泡（sessionMessages.handleError）
// ═══════════════════════════════════════════════════════════════
test('B·sessionMessages：错误气泡走分类映射（errorKey/errorParams）+ 原始串保留在 content', () => {
  const src = read('utils/sessionMessages.js')
  const fn = src.match(/function handleError\(data\)\s*\{[\s\S]*?\n  \}/)
  assert.ok(fn, '未找到 handleError')
  assert.match(src, /import \{ classifyError \} from '\.\/errorMessage'/, '须引入统一映射（不得内联分类逻辑）')
  assert.match(fn[0], /classifyError\(data\)/, '须调用分类映射')
  assert.match(fn[0], /type: 'error'/, '错误气泡须走专用类型（展示层据此渲染人话文案）')
  assert.match(fn[0], /errorKey: cls\.key/, '须带人话 i18n 键')
  assert.match(fn[0], /errorParams: cls\.params/, '须带文案参数')
  assert.match(fn[0], /content: cls\.detail/, '原始错误串须原样保留（诊断/复制）')
  // 直出原始串的旧形态必须已清除
  assert.doesNotMatch(src, /Error: \$\{/, '不得再拼接 `Error: ${原始串}`')
  assert.doesNotMatch(src, /'Unknown error'/, '不得再用 `Unknown error` 兜底')
})

test('B·MessageList：错误终态把 message+code 一并交给映射（缺 message 时仍能按 code 归类）', () => {
  const src = read('views/chat/MessageList.vue')
  assert.match(src, /handleError\(\{ message: d\.message, code: d\.code \}\)/,
    '错误气泡须把终态 message 与 code 一并下传（按 code 兜底归类）')
  assert.doesNotMatch(src, /handleError\(\{ message: d\.message \|\| d\.code \|\| 'LLM turn failed' \}\)/,
    '原「message || code || 硬编码英文」拼串须移除')
})

// ═══════════════════════════════════════════════════════════════
// B 应用点 2：工具卡失败结果（MessageItem）
// ═══════════════════════════════════════════════════════════════
test('B·MessageItem：错误气泡 = 人话文案 + 可展开原始错误（原始串不丢弃、仍可复制）', () => {
  const src = read('views/chat/MessageItem.vue')
  assert.match(src, /import \{ classifyError \} from '\.\.\/\.\.\/utils\/errorMessage'/, '须引入统一映射')
  const branch = src.match(/v-else-if="message\.type === 'error'"[\s\S]*?\n    <\/div>/)
  assert.ok(branch, '未找到 error 气泡分支')
  assert.match(branch[0], /\{\{ errorText \}\}/, '主文案须走映射后的人话文案')
  assert.match(branch[0], /chat\.error_detail_label/, '须有「原始错误」折叠入口（i18n）')
  assert.match(branch[0], /v-if="errorDetailOpen"/, '原始串须可按需展开')
  assert.match(branch[0], /\{\{ message\.content \}\}/, '展开内容 = 原样保留的原始串')
  // 文案计算：errorKey 存在 → i18n；兼容无分类旧形态
  assert.match(src, /props\.message\.errorKey, props\.message\.errorParams/, 'errorText 须用 i18n 渲染 key+params')
  assert.match(src, /props\.message\.errorKey === 'chat\.error_unknown'/, '未知兜底默认展开（原串是唯一线索）')
  // 原始串为空时复制仍可用（回落人话文案）
  assert.match(src, /localContent\.value \|\| props\.message\.content \|\| errorText\.value \|\| ''/, '复制须回落人话文案')
})

test('B·MessageItem：工具卡失败在原始结果上方叠加人话原因（原始结果照常展示）', () => {
  const src = read('views/chat/MessageItem.vue')
  assert.match(src, /const resultErrorText = computed/, '未找到工具失败的人话原因计算')
  assert.match(src, /props\.message\.status !== 'failed' && props\.message\.result_success !== false/,
    '只在失败结果上叠加（成功工具不得加提示）')
  assert.match(src, /classifyError\(props\.message\.result \|\| ''\)/, '须用工具原始结果做分类')
  assert.match(src, /cls\.key === 'chat\.error_unknown'\) return ''/, '未识别类别不叠加（工具自身文案已可读）')
  assert.match(src, /<div v-if="resultErrorText" class="pair-error-hint">/, '人话原因须渲染在结果区')
  // 原始结果仍在同一 pair-section 内原样渲染（保留可查详情）
  const pair = src.match(/<div v-if="resultErrorText" class="pair-error-hint">[\s\S]*?<\/div>\s*\n\s*<pre class="force-wrap">\{\{ localResult \}\}<\/pre>/)
  assert.ok(pair, '人话原因须紧邻原始结果 <pre>（两者并存，不互相替换）')
})

test('B·样式：错误文案/底色走主题 token（无硬编码色值）', () => {
  const src = read('views/chat/MessageItem.vue')
  for (const sel of ['.error-bubble', '.error-text', '.pair-error-hint']) {
    const rule = src.match(new RegExp(sel.replace('.', '\\.') + '\\s*\\{[\\s\\S]*?\\n\\}'))
    assert.ok(rule, `未找到 ${sel} 规则`)
    assert.doesNotMatch(rule[0], /#|rgba?\(/, `${sel} 不得硬编码色值`)
    assert.match(rule[0], /var\(--(danger|bg-tertiary|text-primary|text-secondary)\)/, `${sel} 须走主题 token`)
  }
})

// ═══════════════════════════════════════════════════════════════
// C 上滑加载更多失败 → 可见提示
// ═══════════════════════════════════════════════════════════════
test('C·loadMoreMessages：失败从「只 console」改为可见轻提示（i18n，可重试）', () => {
  const src = read('utils/sessionMessages.js')
  const fn = src.match(/async function loadMoreMessages\(\)\s*\{[\s\S]*?\n  \}/)
  assert.ok(fn, '未找到 loadMoreMessages')
  const cat = fn[0].match(/catch \(e\)\s*\{[\s\S]*?\}/)
  assert.ok(cat, '未找到 catch 分支')
  assert.match(cat[0], /message\.warning\(i18n\.global\.t\('chat\.load_more_failed'\)\)/,
    '失败须可见提示（轻提示 + i18n 文案）')
  assert.match(cat[0], /console\.warn\(/, '调试日志保留（不丢诊断）')
  // 与首屏失败同口径：都经 i18n + 可见提示
  assert.match(src, /message\.error\(i18n\.global\.t\('chat\.load_messages_failed'\)/, '首屏失败提示须仍在')
})

test('C·i18n：load_more_failed zh/en 齐备且含重试指引', () => {
  for (const loc of LOCALES) {
    const v = readLocale(loc, 'chat.json').load_more_failed
    assert.ok(typeof v === 'string' && v.trim().length > 0, `${loc} 缺 chat.load_more_failed`)
  }
  assert.match(readLocale('zh-CN', 'chat.json').load_more_failed, /重试/, 'zh 须含可重试指引')
  assert.match(readLocale('en-US', 'chat.json').load_more_failed, /retry/i, 'en 须含可重试指引')
})

test('C·无 watch/watchEffect（项目规则）：本批改动文件不得引入', () => {
  for (const rel of ['utils/errorMessage.js', 'utils/sessionMessages.js', 'views/chat/MessageItem.vue', 'views/chat/MessageList.vue']) {
    assert.doesNotMatch(read(rel), /\bwatch(Effect)?\s*\(/, `${rel} 不得用 watch/watchEffect`)
  }
})
