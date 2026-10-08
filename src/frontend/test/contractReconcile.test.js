/**
 * B0 契约面试点 · 前端消费端静态对账（2026-10-05）。
 *
 * 目标：以 `docs/spec/60-reference/61-messages.schema.json`（从 61-消息一览 抽取的机器可读
 * 契约）为基准，**静态扫描**前端对某主题响应的键读取，报出「读了契约未声明的键」。
 *
 * 方法（务实近似，非类型化 AST）：
 *   - 对一组**人工圈定**的消费点（file + 响应变量名 + 锚点），截取响应对象的读取片段，
 *     正则收集 `<var>.<key>` / `<var>?.<key>` 的键，与契约 result/event 声明比对；
 *   - 契约声明的键 ∪ 通用 `error` 为允许集合；其余 = 红（无豁免机制：契约=61，须改契约或改代码）。
 *
 * 覆盖边界（局限，务必知悉）：
 *   - **仅覆盖人工圈定的消费点**（gui.* / filesys.* / data-* 的 api 适配层 + init-data 三处 Vue 消费）；
 *     组件内 `const r = ...` 的任意命名无法自动反查主题 → 未自动全量扫描，未覆盖处不误报也不漏判为通过；
 *   - 正则近似：不解析作用域/解构/别名，同名字段（如 layout 内层）不展开；
 *   - **data-* 数据面局限（B1）**：只对账「消费点变量 = 某主题**顶层 result**对象」的读取；
 *     经适配层再下钻的**嵌套**字段（如 utils/sessionMessages.js 读 `res.messages.turns`——res 已是
 *     `data-session-history` result 内层对象）无法用顶层键比对 → 不纳入（避免误报），见 §8.5；
 *   - 契约覆盖 = §1 gui + §2 filesys + §3 data + §4/§5/§6.1。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const feSrc = join(here, '..', 'src')
const contractPath = join(here, '..', '..', '..', 'docs', 'spec', '60-reference', '61-messages.schema.json')
const read = (rel) => readFileSync(join(feSrc, rel), 'utf8')

const contract = JSON.parse(readFileSync(contractPath, 'utf8'))
const byTopic = new Map(contract.topics.map((t) => [t.topic, t]))

// schema clientTopic（前端 type ↔ 相对主题 的键；genmsg 生成 MsgClientTopics）；与契约主题同属
// 「禁止字面量」集合（须以 MsgClientTopics.* 引用，见 50 §8.8）。
const clientTopics = new Set(contract.topics.map((t) => t.clientTopic).filter(Boolean))

// msgkeys.js（genmsg 生成）导出的常量对象：<ConstObj>.<prop> -> 字段名字符串。
// 用于把「常量下标访问」r[GuiInitDataKeys.workDir] 映射回契约字段名 workDir。
const msgkeysSrc = readFileSync(join(feSrc, 'events', 'msgkeys.js'), 'utf8')
const constMaps = new Map()
for (const m of msgkeysSrc.matchAll(/export const (\w+) = \{([\s\S]*?)\n\}/g)) {
  const map = new Map()
  for (const mm of m[2].matchAll(/'?([\w$]+)'?:\s*'([^']*)'/g)) map.set(mm[1], mm[2])
  if (map.size) constMaps.set(m[1], map)
}

/** 字符串字面量读取的键集合：`<var>.<key>` / `<var>?.<key>` / `<var>['key']`。 */
function scanVarLiteralKeys(text, varName) {
  const keys = new Set()
  const reProp = new RegExp(`(?<![\\w$.:])${varName}\\s*\\??\\.\\s*([A-Za-z_$][\\w$]*)`, 'g')
  for (const m of text.matchAll(reProp)) keys.add(m[1])
  const reLit = new RegExp(`(?<![\\w$.:])${varName}\\s*\\??\\.?\\[\\s*['"]([^'"]+)['"]\\s*\\]`, 'g')
  for (const m of text.matchAll(reLit)) keys.add(m[1])
  return keys
}

/** 常量下标读取的键集合：`<var>[<ConstObj>.<prop>]` -> 映射回字段名字符串。 */
function scanVarConstKeys(text, varName) {
  const keys = new Set()
  const reIdx = new RegExp(
    `(?<![\\w$.:])${varName}\\s*\\??\\.?\\[\\s*([A-Za-z_$][\\w$]*)\\s*\\.\\s*([A-Za-z_$][\\w$]*)\\s*\\]`,
    'g',
  )
  for (const m of text.matchAll(reIdx)) {
    const map = constMaps.get(m[1])
    keys.add(map && map.has(m[2]) ? map.get(m[2]) : m[2])
  }
  return keys
}

/** 收集 text 中从响应里读取的键集合（字符串字面量 ∪ 常量下标）。 */
function scanVarKeys(text, varName) {
  return new Set([...scanVarLiteralKeys(text, varName), ...scanVarConstKeys(text, varName)])
}

/** 取文件里 `export (async) function <name>(...) {...}` 的函数体（列 0 的收尾 } 为准）。 */
function fnBody(file, name) {
  const re = new RegExp(`export\\s+(?:async\\s+)?function\\s+${name}\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}`)
  const m = file.match(re)
  assert.ok(m, `未找到函数 ${name}`)
  return m[1]
}

/** 以 anchor 为锚，取前后窗口（用于组件内无法用 fnBody 截取的消费片段）。 */
function windowAround(content, anchor, before, after) {
  const idx = content.search(anchor)
  assert.ok(idx >= 0, `未找到锚点 ${anchor}`)
  return content.slice(Math.max(0, idx - before), idx + after)
}

/**
 * 断言：某消费点从响应里读的键 ⊆ 契约声明。
 * kind = 'result' | 'event'。
 */
function assertConsumer({ topic, kind = 'result', text, varName }) {
  const ts = byTopic.get(topic)
  assert.ok(ts, `契约未收录主题 ${topic}`)
  const declared = new Set(Object.keys(ts[kind] || {}))
  const readKeys = scanVarKeys(text, varName)
  assert.ok(readKeys.size > 0, `${topic}: 消费点未解析出任何键读取（表达式或被误改）`)
  const bad = [...readKeys].filter((k) => k !== 'error' && !declared.has(k))
  assert.deepEqual(
    bad,
    [],
    `${topic}: 前端读了契约未声明的键 ${JSON.stringify(bad)}（契约声明=${JSON.stringify([...declared])}）`,
  )
  // 反向校验：已声明字段不得再用字符串字面量读取（须用 <Topic>Keys.<key> 常量下标）。
  const lit = scanVarLiteralKeys(text, varName)
  const litDeclared = [...lit].filter((k) => k !== 'error' && declared.has(k))
  assert.deepEqual(
    litDeclared,
    [],
    `${topic}: 已声明字段 ${JSON.stringify(litDeclared)} 仍以字符串字面量读取 —— 请改用常量下标（如 ${varName}[XxxKeys.key]）`,
  )
}

test('契约可加载且收录 gui/filesys/事件面主题', () => {
  assert.ok(contract.topics.length > 0, '契约未收录任何主题')
  for (const topic of ['gui.init-data', 'gui.toolchain.detect', 'gui.system.builtins', 'gui.pick-executable', 'filesys.list', 'filesys.content']) {
    assert.ok(byTopic.has(topic), `契约缺主题 ${topic}`)
  }
})

test('gui.* 消费（api/config.js 适配层）读键 ⊆ 契约', () => {
  const config = read('api/config.js')
  assertConsumer({ topic: 'gui.toolchain.detect', text: fnBody(config, 'detectToolchains'), varName: 'res' })
  assertConsumer({ topic: 'gui.system.builtins', text: fnBody(config, 'getSystemBuiltins'), varName: 'res' })
  assertConsumer({ topic: 'gui.pick-executable', text: fnBody(config, 'pickExecutable'), varName: 'res' })
})

test('filesys.* 消费（api/file.js 适配层）读键 ⊆ 契约', () => {
  const file = read('api/file.js')
  assertConsumer({ topic: 'filesys.list', text: fnBody(file, 'getFileTree'), varName: 'p' })
  assertConsumer({ topic: 'filesys.list', text: fnBody(file, 'getFileTreeChildren'), varName: 'p' })
  assertConsumer({ topic: 'filesys.content', text: fnBody(file, 'readFile'), varName: 'p' })
})

test('gui.init-data 消费（CodeView / LogConfig / MainLayout）读键 ⊆ 契约', () => {
  const codeView = read('views/codeview/CodeView.vue')
  assertConsumer({
    topic: 'gui.init-data',
    text: windowAround(codeView, /loadInitData\(\)\.then\(r\s*=>/, 0, 420),
    varName: 'r',
  })

  const logConfig = read('views/settings/LogConfig.vue')
  assertConsumer({
    topic: 'gui.init-data',
    text: windowAround(logConfig, /await loadInitData\(\)/, 0, 160),
    varName: 'r',
  })

  const mainLayout = read('views/layout/MainLayout.vue')
  assertConsumer({
    topic: 'gui.init-data',
    text: windowAround(mainLayout, /r\[GuiInitDataKeys\.wizard_required\]/, 0, 220),
    varName: 'r',
  })
})

test('回归：filesys.content 消费不得再读未声明的 output_file', () => {
  const file = read('api/file.js')
  assert.ok(!/output_file/.test(file), 'filesys.content 返回不含 output_file（61 §2.1），消费侧不得再读（I-142）')
})

// ── §3 数据面消费端对账（B1）────────────────────────────────────────────────

test('契约已收录 §3 数据面主题', () => {
  for (const topic of [
    'data-user-config-load', 'data-prj-config-list', 'data-session-history',
    'data-session-content', 'data-knowledge-root', 'data-scenario-list', 'data-mcp-list',
  ]) {
    assert.ok(byTopic.has(topic), `契约缺数据面主题 ${topic}`)
  }
})

test('data-* 消费（api/config.js 适配层）读键 ⊆ 契约', () => {
  const config = read('api/config.js')
  assertConsumer({ topic: 'data-user-config-load', text: fnBody(config, 'getUserConfig'), varName: 'reply' })
  assertConsumer({ topic: 'data-user-config-list', text: fnBody(config, 'getUserConfigMain'), varName: 'reply' })
  assertConsumer({ topic: 'data-prj-config-list', text: fnBody(config, 'getAllConfig'), varName: 'reply' })
  assertConsumer({ topic: 'data-prj-config-load', text: fnBody(config, 'getConfigValue'), varName: 'reply' })
  assertConsumer({ topic: 'data-prompt-load', text: fnBody(config, 'getPrompt'), varName: 'reply' })
  assertConsumer({ topic: 'data-mcp-list', text: fnBody(config, 'listMcpServers'), varName: 'reply' })
  assertConsumer({ topic: 'data-prj-security-list', text: fnBody(config, 'getProjectSecurity'), varName: 'reply' })
})

test('data-* 消费（api/session.js 适配层）读键 ⊆ 契约', () => {
  const s = read('api/session.js')
  assertConsumer({ topic: 'data-session-list', text: fnBody(s, 'listSessions'), varName: 'r' })
  assertConsumer({ topic: 'data-session-get', text: fnBody(s, 'getSession'), varName: 'r' })
  assertConsumer({ topic: 'data-session-history', text: fnBody(s, 'getTurnsPaginated'), varName: 'r' })
  assertConsumer({ topic: 'data-session-load-messages', text: fnBody(s, 'getTurnMessages'), varName: 'r' })
  assertConsumer({ topic: 'data-session-content', text: fnBody(s, 'getMessageContent'), varName: 'p' })
})

test('data-* 消费（api/knowledge.js 适配层）读键 ⊆ 契约', () => {
  const k = read('api/knowledge.js')
  assertConsumer({ topic: 'data-knowledge-root', text: fnBody(k, 'getKnowledgeRoot'), varName: 'r' })
  assertConsumer({ topic: 'data-knowledge-list', text: fnBody(k, 'listPrimitives'), varName: 'r' })
  assertConsumer({ topic: 'data-knowledge-read', text: fnBody(k, 'readPrimitive'), varName: 'r' })
  assertConsumer({ topic: 'data-knowledge-create', text: fnBody(k, 'createPrimitive'), varName: 'r' })
})

test('data-* 消费（api/scenario.js 适配层）读键 ⊆ 契约', () => {
  const sc = read('api/scenario.js')
  assertConsumer({ topic: 'data-scenario-list', text: fnBody(sc, 'getScenarioList'), varName: 'reply' })
})

test('回归：data-user-config-load 消费不得再读未声明的 config 键', () => {
  const config = read('api/config.js')
  assert.ok(
    !/reply\.config\b/.test(config),
    'data-user-config-load 结果 = {data}（61 §3.1）；旧 /call 的 {config} 形态不存在，消费侧不得再读',
  )
})

// ── §4/§5 服务端/网关面消费端对账（B2）──────────────────────────────────────
// 存在级（正向）扫描：断言消费点读的键 ⊆ 契约声明。**不含**「已声明字段不得用字面量」反向校验
// —— §4/§5 消费点（向导 / LLM 测试连接）仍以属性名读取（未常量下标化），反向校验待后续批次；
// 本处只做「读未声明键 = 红」的存在级校验（局限见 §8.5）。

/** 只做正向（读键 ⊆ 契约）对账；kind = 'result' | 'event'。 */
function assertConsumerSubset({ topic, kind = 'result', text, varName }) {
  const ts = byTopic.get(topic)
  assert.ok(ts, `契约未收录主题 ${topic}`)
  const declared = new Set(Object.keys(ts[kind] || {}))
  const readKeys = scanVarKeys(text, varName)
  assert.ok(readKeys.size > 0, `${topic}: 消费点未解析出任何键读取（表达式或被误改）`)
  const bad = [...readKeys].filter((k) => k !== 'error' && !declared.has(k))
  assert.deepEqual(
    bad,
    [],
    `${topic}: 前端读了契约未声明的键 ${JSON.stringify(bad)}（契约声明=${JSON.stringify([...declared])}）`,
  )
}

test('契约已收录 §4 服务端 / §5 网关面主题（B2）', () => {
  for (const topic of [
    'llm-start', 'llm-send', 'llm-cancel', 'ask-user-reply', 'tool-retry', 'task-stop', 'task-background',
    'prompt-optimise', 'task-verify', 'llm-simple', 'llm.test-connection', 'memory.flush',
    'agent-wizard', 'agent-wizard-probe', 'agent-wizard-compose', 'agent-wizard-generate', 'agent-wizard-skip',
    'session-receive', 'session-complete', 'session-compress', 'session-ask', 'session-turn-start', 'session-new',
    'task-started', 'task-updated', 'task-done', 'server-starting', 'prompt-optimised', 'tool-notify',
    'instance-claim', 'instance-register', 'instance-heartbeat', 'instance-exit',
    'login-register', 'login-in', 'login-out',
    'mcp-tools-list', 'mcp-tools-call', 'mcp-tools-wait', 'mcp-tools-register', 'mcp-tools-unregister',
    'mcp-prompts-list', 'mcp-prompts-get', 'mcp-resources-list', 'mcp-resources-read',
    'mcp-servers-list', 'mcp-servers-get', 'mcp-gateway-check', 'mcp-gateway-reload',
    'mcp-tasks-report', 'mcp-tools-timeout', 'mcp-gateway-changed',
  ]) {
    assert.ok(byTopic.has(topic), `契约缺 §4/§5 主题 ${topic}`)
  }
  // clientTopic（schema 声明）由 genmsg 生成 MsgClientTopics —— 断言生成物存在且值一致。
  for (const [key, val] of Object.entries({ toolsList: 'tools-list', promptsList: 'prompts-list', resourcesList: 'resources-list' })) {
    assert.match(msgkeysSrc, new RegExp(`export const MsgClientTopics = \\{[\\s\\S]*\\b${key}: '${val}'`), `msgkeys.js 缺 MsgClientTopics.${key}`)
  }
})

// ── §5.4 检索/引擎面（codegraph/vfts 工具回调）对账（B3）────────────────────
// 该面主题的**生产者 = gateway、注册方 = codegraph/vfts 插件**（Go 侧），非前端消费；
// 前端仅经 prj 配置键 `codegraph.status`/`vfts.status` 面（data-prj-config-load）间接消费。
// 此处断言契约已收录两回调主题 + genmsg 已生成 MsgTopics 常量（供守卫/未来消费）。

test('契约已收录检索/引擎面（B3）回调主题 + msgkeys 常量', () => {
  for (const topic of ['codegraph-tool-call', 'vfts-tool-call']) {
    assert.ok(byTopic.has(topic), `契约缺 B3 主题 ${topic}`)
  }
  for (const [key, val] of Object.entries({ codegraphToolCall: 'codegraph-tool-call', vftsToolCall: 'vfts-tool-call' })) {
    assert.match(msgkeysSrc, new RegExp(`export const MsgTopics = \\{[\\s\\S]*\\b${key}: '${val}'`), `msgkeys.js 缺 MsgTopics.${key}`)
  }
})

test('agent-wizard-generate 消费（ScenarioWizardDialog）读键 ⊆ 契约', () => {
  const dlg = read('views/scenario/ScenarioWizardDialog.vue')
  assertConsumerSubset({
    topic: 'agent-wizard-generate',
    text: windowAround(dlg, /async function doGenerate\(\)/, 0, 700),
    varName: 'res',
  })
})

test('llm.test-connection 消费（EditLLMDialog）读键 ⊆ 契约', () => {
  const dlg = read('views/config/EditLLMDialog.vue')
  assertConsumerSubset({
    topic: 'llm.test-connection',
    text: windowAround(dlg, /const r = testResult\.value/, 0, 320),
    varName: 'r',
  })
})

// ── §1 gui.* / §6.1 前端面消费端对账（B4）────────────────────────────────────
// GUI/前端面 = gui.* 本地面 + 文件/窗口/检索/上传/截图/登录 + 前端本地事件。
// 消费点（api 适配层 + 组件）读键 ⊆ 契约；已声明字段按常量下标读取者做反向校验，
// 仍以属性名读取者只做存在级（正向），见 §8.5。

test('gui.* 消费（FileTree / Toolbar）读键 ⊆ 契约', () => {
  // gui.vcs.info（FileTree.vue fetchVCSInfo；常量下标）
  const ft = read('views/filetree/FileTree.vue')
  assertConsumer({
    topic: 'gui.vcs.info',
    text: windowAround(ft, /async function fetchVCSInfo\(\)/, 0, 420),
    varName: 'r',
  })

  // gui.window.status（Toolbar.vue syncMaximizeState；属性名读取 → 存在级）
  const tb = read('views/toolbar/Toolbar.vue')
  assertConsumerSubset({
    topic: 'gui.window.status',
    text: windowAround(tb, /async function syncMaximizeState\(\)/, 0, 340),
    varName: 'r',
  })
  // gui.search（Toolbar.vue searchFiles；常量下标）
  assertConsumer({
    topic: 'gui.search',
    text: windowAround(tb, /async function searchFiles\(q\)/, 0, 340),
    varName: 'r',
  })
})

test('gui.upload / gui.capture 消费（ChatPanel.vue）读键 ⊆ 契约', () => {
  const cp = read('views/chat/ChatPanel.vue')
  assertConsumer({
    topic: 'gui.upload',
    text: windowAround(cp, /async function onScreenshotDone\(dataUrl\)/, 0, 560),
    varName: 'res',
  })
  assertConsumer({
    topic: 'gui.capture',
    text: windowAround(cp, /MsgTopics\.guiCapture/, 0, 380),
    varName: 'res',
  })
})

test('gui.upload 消费（InputBox.vue 附件上传）读键 ⊆ 契约（存在级）', () => {
  const ib = read('views/chat/InputBox.vue')
  assertConsumerSubset({
    topic: 'gui.upload',
    text: windowAround(ib, /mq\.emit\(MsgTopics\.guiUpload/, 0, 420),
    varName: 'res',
  })
})

test('gui.window.list / open-chat 消费（useChatWindows）读键 ⊆ 契约（存在级）', () => {
  const cw = read('composables/useChatWindows.js')
  assertConsumerSubset({ topic: 'gui.window.list', text: fnBody(cw, 'refreshWindows'), varName: 'r' })
  assertConsumerSubset({ topic: 'gui.window.open-chat', text: fnBody(cw, 'openChat'), varName: 'r' })
})

test('login-in / login-register 消费（useAuth.js callLogin）读键 ⊆ 契约', () => {
  const auth = read('composables/useAuth.js')
  const body = windowAround(auth, /async function callLogin\(topic, username, password\)/, 0, 900)
  assertConsumer({ topic: 'login-in', text: body, varName: 'result' })
  assertConsumer({ topic: 'login-register', text: body, varName: 'result' })
})

test('契约已收录 §6.1 前端本地事件（B4）', () => {
  for (const topic of ['project-config-open', 'preview-tab-open']) {
    assert.ok(byTopic.has(topic), `契约缺前端本地事件 ${topic}`)
  }
})

// ── §4 主题/客户端topic 字面量守卫（JS 侧，2026-10-05）──────────────────────
// 反向校验扩展：JS 消费端**不得以字符串字面量**书写契约主题名（应用 MsgTopics 常量）
// 与 schema `clientTopic`（应用 MsgClientTopics 常量，如 tools-list）。
// 近似说明：正则扫描 `mq.on('X')` / `mq.emit('X')`；先剥离注释（HTML/块/行）避免文档注释误报；
// 只判**完整名字**（契约主题 ∪ clientTopic）；桥内构造的其它前端 type（如 llm-receive）不判。

/** 递归列出 src 下 .js / .vue 文件。 */
function listSrcFiles(dir) {
  const out = []
  for (const ent of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, ent.name)
    if (ent.isDirectory()) out.push(...listSrcFiles(p))
    else if (/\.(js|vue)$/.test(ent.name)) out.push(p)
  }
  return out
}

/** 剥离注释（HTML → 块 → 行）。近似：行内字符串含 // 时可能截断（只影响命中，不引入误报）。 */
function stripComments(src) {
  return src
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/[^\n]*/g, '')
}

test('前端 mq 的契约主题/客户端topic 不得用字符串字面量（须 MsgTopics/MsgClientTopics 常量）', () => {
  const files = listSrcFiles(feSrc).filter(
    (f) => !/[\\/]events[\\/](msgkeys|event-names)\.js$/.test(f),
  )
  assert.ok(files.length > 0, '未扫描到前端源文件')
  const offenders = []
  for (const f of files) {
    const text = stripComments(readFileSync(f, 'utf8'))
    for (const m of text.matchAll(/mq\.(?:on|emit)\(\s*'([^']+)'/g)) {
      if (byTopic.has(m[1]) || clientTopics.has(m[1])) {
        offenders.push(`${f.slice(feSrc.length + 1)}: ${m[1]}`)
      }
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `契约主题/客户端topic 字面量须改用 MsgTopics/MsgClientTopics 常量（50 §8.7）：${JSON.stringify(offenders)}`,
  )
})

