/**
 * chat 面「prompt 选择注入」单测（[25-MCP与场景分层模型] §7 · T5 ②）。
 *
 * 口径（25 §7）：用户从**知识库 prompt**（capability 三级根 `prompts/*.prompt.md`）中选择 →
 *   ① 发送时把 prompt 内容**并入 user 消息文本**（**零契约变更**：llm-start / message payload 不加字段）；
 *   ② HTML 上以 **`/<prompt-name>`** 的 tag 显示（可移除）；
 *   ③ 移除 tag → 内容不再注入。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 用「纯函数行为 + 源码守卫」双保险：
 *   - 纯函数/组合式（utils/chatPrompt.js · composables/useChatPrompts.js）直接行为断言；
 *   - Vue 内渲染与发送链路以源码守卫锁定（tag 模板 / serialize 注入点 / 消息链路各环节）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  promptNameOf,
  promptTagOf,
  isPromptEntry,
  collectPromptEntries,
  mergePromptEntries,
  composeUserText,
} from '../src/utils/chatPrompt.js'
import { useChatPrompts } from '../src/composables/useChatPrompts.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

// ── 知识库列举桩（三级根；某一级 getKnowledgeRoot 抛错 = 该级不存在）──
function fakeApi(byDir, roots) {
  return {
    getKnowledgeRoot: async (kind) => {
      if (!(kind in roots)) throw new Error('no level: ' + kind)
      return { root: roots[kind] }
    },
    listPrimitives: async (dir) => {
      const hit = byDir[dir]
      if (!hit) throw new Error('no dir: ' + dir)
      return hit
    },
    readPrimitive: async (path) => ({ doc: { content: `PROMPT BODY ${path}` }, source: 'SOURCE' }),
  }
}

// app：summary（与 project 同名 → project 胜出）；user：code-review + 一层子目录 nested
const DIRS = {
  'app-root/prompts': {
    dirs: [],
    files: [
      { name: 'summary.prompt.md', path: 'app-root/prompts/summary.prompt.md', type: 'prompt' },
      { name: 'notes.md', path: 'app-root/prompts/notes.md', type: 'file' },
    ],
  },
  'user-root/prompts': {
    dirs: [{ name: 'sub', path: 'user-root/prompts/sub' }],
    files: [{ name: 'code-review.prompt.md', path: 'user-root/prompts/code-review.prompt.md', type: 'prompt' }],
  },
  'user-root/prompts/sub': {
    dirs: [],
    files: [{ name: 'nested.prompt.md', path: 'user-root/prompts/sub/nested.prompt.md', type: 'prompt' }],
  },
  'project-root/prompts': {
    dirs: [],
    files: [{ name: 'summary.prompt.md', path: 'project-root/prompts/summary.prompt.md', type: 'prompt' }],
  },
}
const ROOTS = { app: 'app-root', user: 'user-root', project: 'project-root' }

// ① 选 prompt → 发送时 user 消息**包含** prompt 内容（并入 user 文本，不新增字段）
test('① 选中 prompt → 发送文本包含 prompt 内容（并入 user 消息）', async () => {
  const c = useChatPrompts({ api: fakeApi(DIRS, ROOTS) })
  await c.load()
  assert.deepEqual(c.available.value.map(p => p.name).sort(), ['code-review', 'nested', 'summary'])

  // 选中 = 读该 prompt 正文（data-knowledge-read）
  const ok = await c.pick(c.available.value.find(p => p.name === 'code-review'))
  assert.equal(ok, true)
  assert.equal(c.selected.value.content, 'PROMPT BODY user-root/prompts/code-review.prompt.md')

  // 发送文本 = InputBox.serialize（附件标记 + 用户输入）→ composeUserText(…, 已选 prompt)
  const typed = '帮我 review 这段代码'
  const sent = composeUserText(typed, c.selected.value)
  assert.ok(sent.includes('PROMPT BODY user-root/prompts/code-review.prompt.md'), 'user 消息须包含 prompt 内容')
  assert.ok(sent.includes(typed), 'user 消息须保留用户输入')
})

// ① 链路守卫：InputBox.serialize 注入 → emit('send') → ChatPanel payload.text → llm-start.q
test('① 发送链路守卫：注入点唯一且零契约变更（llm-start 不加字段）', () => {
  const inputBox = read('views/chat/InputBox.vue')
  assert.match(inputBox, /import \{ useChatPrompts \} from '\.\.\/\.\.\/composables\/useChatPrompts'/)
  assert.match(inputBox, /import \{ composeUserText, promptTagOf \} from '\.\.\/\.\.\/utils\/chatPrompt'/)
  // 注入点：序列化末尾并入已选 prompt（唯一注入处）
  assert.match(inputBox, /return composeUserText\(parts\.join\('\\n'\), selectedPrompt\.value\)/)
  // 发送 / 入队均出自同一 serialize（两条路径都被注入覆盖）
  assert.match(inputBox, /emit\('send', serialize\(\)\)/)
  assert.match(inputBox, /emit\('queue', serialize\(\)\)/)

  const chatPanel = read('views/chat/ChatPanel.vue')
  // ChatPanel 只透传文本（不带 prompt 字段）
  assert.match(chatPanel, /mq\.emit\(EventNames\.messageSend, buildMessagePayload\(sid, text\)\)/)
  assert.match(chatPanel, /text: text,/)
  const payloadBlock = chatPanel.match(/function buildMessagePayload\(sid, text\) \{[\s\S]*?\n\}/)
  assert.ok(payloadBlock, '须能定位 buildMessagePayload')
  assert.doesNotMatch(payloadBlock[0], /prompt/, 'message payload 不得新增 prompt 字段（零契约变更）')

  const messageList = read('views/chat/MessageList.vue')
  assert.match(messageList, /publishLLMStart\(\s*\n\s*sid,\s*\n\s*batch\.text,/, '文本经 batch.text 进 llm-start')

  const chatApi = read('api/chat.js')
  assert.match(chatApi, /q: text,/, 'user 消息文本 = llm-start.q（既有字段）')
  assert.doesNotMatch(chatApi, /prompt/i, 'api/chat.js 不得为 prompt 新增载荷字段')
  // 不新造消息主题：prompt 列举/读取全走既有 data 层接口
  const composable = read('composables/useChatPrompts.js')
  assert.match(composable, /import \{ getKnowledgeRoot, listPrimitives, readPrimitive \} from '\.\.\/api\/knowledge\.js'/)
  assert.doesNotMatch(composable, /mq\.emit|EventNames/, '不得新造 / 直发消息主题')
})

// ② tag 渲染为 `/<prompt-name>`
test('② tag 渲染为 /<prompt-name>', () => {
  assert.equal(promptTagOf('code-review'), '/code-review')
  assert.equal(promptTagOf('  summary  '), '/summary')
  assert.equal(promptTagOf(''), '')
  assert.equal(promptTagOf(null), '')
  // 名来自文件名（去 `.prompt.md`）
  assert.equal(promptNameOf('code-review.prompt.md'), 'code-review')
  assert.equal(promptNameOf('D:\\kb\\prompts\\summary.prompt.md'), 'summary')
  assert.equal(promptNameOf('SUMMARY.PROMPT.MD'), 'SUMMARY')
  // 仅剥离 `.prompt.md` 后缀（非 prompt 文件原样 —— 实践中只对 prompt 条目调用）
  assert.equal(promptNameOf('notes.md'), 'notes.md')

  const inputBox = read('views/chat/InputBox.vue')
  // 输入框上方 tag 行（可移除）+ tag 文本 = `/<name>`
  assert.match(inputBox, /<div v-if="selectedPrompt" class="prompt-tags">/)
  assert.match(inputBox, /\{\{ promptTagOf\(selectedPrompt\.name\) \}\}/)
  // 选择器按钮 / 列表项也展示同一 tag 形式
  assert.match(inputBox, /selectedPrompt \? promptTagOf\(selectedPrompt\.name\) : \$t\('chat\.prompt_pick'\)/)
  assert.match(inputBox, /<span class="prompt-item-name">\{\{ promptTagOf\(p\.name\) \}\}<\/span>/)
})

// ②′ 控件位置：选择器在输入框**上方**一行（prompt-row），已从下方工具栏移出
test('②′ 选择器位于输入框上方一行（非下方工具栏）', () => {
  const inputBox = read('views/chat/InputBox.vue')
  // 输入区上方一行容器（常驻；未选中时也显示选择器按钮，不整行隐藏）
  assert.match(inputBox, /<div class="prompt-row">/, '须有输入区上方一行容器 prompt-row')
  const rowIdx = inputBox.indexOf('class="prompt-row"')
  const pickIdx = inputBox.indexOf('prompt-pick-btn')
  const inputIdx = inputBox.indexOf('ref="editRef"')
  assert.ok(rowIdx >= 0 && pickIdx >= 0 && inputIdx >= 0, '须能定位 prompt-row / 选择器 / 输入区')
  assert.ok(rowIdx < pickIdx && pickIdx < inputIdx, '选择器须位于输入框（richtext-input）上方')
  // 上方一行同时承载已选 tag（含 ✕）
  const rowBlock = inputBox.slice(rowIdx, inputIdx)
  assert.match(rowBlock, /class="prompt-tags"/, '上方一行须含已选 tag 行')
  assert.match(rowBlock, /@click\.stop="removePrompt"/, '已选 tag 须可移除')
  // 下方工具栏（input-actions-left）不再包含提示词选择器（保留 controls 插槽 + 队列指示器）
  const leftBlock = inputBox.slice(
    inputBox.indexOf('class="input-actions-left"'),
    inputBox.indexOf('class="input-actions-right"'),
  )
  assert.ok(leftBlock, '须能定位 input-actions-left')
  assert.doesNotMatch(leftBlock, /prompt-pick-btn/, '提示词选择器须移出下方工具栏')
})

// ②″ Popover 顶部「无（不使用提示词）」项：单选清除 + 未选中高亮
test('②″ Popover 含「无」项（单选清除，未选中高亮）', () => {
  const inputBox = read('views/chat/InputBox.vue')
  assert.match(inputBox, /class="prompt-item prompt-item-none"/, '「无」项须存在')
  assert.match(inputBox, /\$t\('chat\.prompt_none'\)/, '「无」项文案走 i18n chat.prompt_none')
  assert.match(inputBox, /:class="\{ active: !selectedPrompt \}"/, '未选择时「无」项为 active 高亮')
  assert.match(inputBox, /function onPickNone\(\)/, '「无」项点击处理须存在')
  assert.match(inputBox, /onPickNone[\s\S]{0,120}removePrompt\(\)/, '「无」项等价 remove（清除已选）')
  // 「无」项位于可选列表最上方（先于 v-for 列表项）
  assert.ok(
    inputBox.indexOf('prompt-item-none') < inputBox.indexOf('v-for="p in promptOptions"'),
    '「无」项须在列表最上方',
  )
})

// ②‴ 单选唯一性：selected 为单值；连续 pick 替换而非累积（不引入多选）
test('②‴ 单选唯一性：selected 单值 + 连续 pick 替换', async () => {
  const composable = read('composables/useChatPrompts.js')
  assert.match(composable, /const selected = ref\(null\)/, 'selected 为单值 ref（非数组）')
  const c = useChatPrompts({ api: fakeApi(DIRS, ROOTS) })
  await c.load()
  await c.pick(c.available.value.find(p => p.name === 'code-review'))
  assert.equal(c.selected.value.name, 'code-review')
  await c.pick(c.available.value.find(p => p.name === 'summary'))
  assert.equal(c.selected.value.name, 'summary', '再选一个 → 替换原选中（单选）')
  assert.ok(!Array.isArray(c.selected.value), 'selected 不得为数组（不引入多选）')
})

// ③ 移除 tag → 内容不再注入
test('③ 移除 tag（未选/移除）→ 内容不再注入 user 消息', async () => {
  const c = useChatPrompts({ api: fakeApi(DIRS, ROOTS) })
  await c.load()
  await c.pick(c.available.value.find(p => p.name === 'nested'))
  assert.ok(composeUserText('hi', c.selected.value).includes('PROMPT BODY'))

  c.remove()
  assert.equal(c.selected.value, null)
  assert.equal(composeUserText('hi', c.selected.value), 'hi', '移除后文本原样（内容不再并入）')

  // 边界：空内容 / 纯空白 prompt 也不注入
  assert.equal(composeUserText('hi', { name: 'x', content: '' }), 'hi')
  assert.equal(composeUserText('hi', { name: 'x', content: '   \n  ' }), 'hi')
  // 纯 prompt（未输入文本）也可发送：发送文本即 prompt 内容
  assert.equal(composeUserText('', { name: 'x', content: 'BODY' }), 'BODY')

  const inputBox = read('views/chat/InputBox.vue')
  assert.match(inputBox, /@click\.stop="removePrompt"/, 'tag 上须有移除入口')
  assert.match(inputBox, /removePrompt\(\) \/\/ 已选 prompt 为\*\*一次性注入\*\*/, '发送/入队后清空已选 prompt')
  assert.match(inputBox, /const ok = await pickPrompt\(p\)/, '选中走 composable.pick（读正文）')
})

// prompt 列举：三级合并 + 同名具体级优先 + 非 prompt 排除 + 一层子目录递归 + 失败级跳过
test('prompt 列举：三级合并 / 具体级优先 / 过滤非 prompt / 递归一层 / 失败级跳过', async () => {
  const c = useChatPrompts({ api: fakeApi(DIRS, ROOTS) })
  await c.load()
  const names = c.available.value.map(p => p.name)
  assert.deepEqual(names, ['code-review', 'nested', 'summary'], '按 name 升序')
  // 同名 summary 跨 app / project → 取更具体的 project
  assert.equal(c.available.value.find(p => p.name === 'summary').level, 'project')
  // 非 prompt 文件（type=file）不出现在可选列表
  assert.ok(!names.includes('notes'))
  // 一层子目录内的 prompt 被递归收集
  assert.equal(c.available.value.find(p => p.name === 'nested').level, 'user')

  // 某级 getKnowledgeRoot 抛错 → 跳过该级，不阻断其它级
  const partial = useChatPrompts({
    api: fakeApi(
      { 'project-root/prompts': DIRS['project-root/prompts'] },
      { project: 'project-root' }, // app / user 不存在
    ),
  })
  await partial.load()
  assert.deepEqual(partial.available.value.map(p => p.name), ['summary'])
  assert.equal(partial.error.value, '')
})

// 选中失败 / 空条目：不选中、明确回报（不静默）
test('pick 异常：读取失败 → 不选中 + 回报 false + 记录 error', async () => {
  const base = fakeApi(DIRS, ROOTS)
  const c = useChatPrompts({
    api: { ...base, readPrimitive: async () => { throw new Error('boom') } },
  })
  assert.equal(await c.pick({ name: 'x', path: 'x.prompt.md' }), false)
  assert.equal(c.selected.value, null)
  assert.equal(c.error.value, 'boom')
  // 空条目直接拒绝
  assert.equal(await c.pick(null), false)
  assert.equal(await c.pick({ name: 'x', path: '' }), false)
})

// 纯函数面：文件名 / 类型识别 / 三级去重
test('纯函数：文件识别 · 条目收集 · 三级去重', () => {
  assert.equal(isPromptEntry({ name: 'a.prompt.md', type: 'prompt' }), true)
  assert.equal(isPromptEntry({ name: 'a.prompt.md', type: 'file' }), true)
  assert.equal(isPromptEntry({ name: 'a.md', type: 'file' }), false)
  assert.equal(isPromptEntry(null), false)

  const entries = collectPromptEntries([
    { name: 'a.prompt.md', path: 'p/a.prompt.md', type: 'prompt', description: 'd1' },
    { name: '.prompt.md', path: 'p/.prompt.md', type: 'prompt' },
    { name: 'b.md', path: 'p/b.md', type: 'file' },
  ], 'user')
  assert.deepEqual(entries, [{ name: 'a', path: 'p/a.prompt.md', level: 'user', description: 'd1' }])

  const merged = mergePromptEntries(
    [{ name: 's', path: 'app', level: 'app' }],
    [{ name: 's', path: 'user', level: 'user' }, { name: 't', path: 'user2', level: 'user' }],
  )
  assert.equal(merged.find(e => e.name === 's').level, 'user')
  assert.equal(merged.length, 2)
  // 反向顺序（更具体先来、app 后到）→ 仍保留更具体
  const merged2 = mergePromptEntries(
    [{ name: 's', path: 'user', level: 'user' }],
    [{ name: 's', path: 'app', level: 'app' }],
  )
  assert.equal(merged2.find(e => e.name === 's').path, 'user')
})
