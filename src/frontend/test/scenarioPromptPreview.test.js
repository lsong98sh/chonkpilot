/**
 * 场景编辑器「组合后系统提示词」预览页签单测（[25-MCP与场景分层模型] §3 · T5 ①）。
 *
 * 口径（25 §3）：系统提示词按 **全局层 → 场景层 → agent 层** 拼接：
 *   全局 | 身份 / 运行环境 | **后端代码写死**（前端不得编造 → 只标注「以后端为准」）
 *   场景 | `scenario.description` + 代码按 agents 自动拼接的成员段（名字 + roleTag + 描述）
 *   agent| 当前 agent 的 prompt（主 agent = `main.agent.md`）
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 纯函数行为 + 源码/文案守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { composeScenarioMembers, composeScenarioLayer, buildPromptLayers } from '../src/utils/promptLayers.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

const AGENTS = [
  { name: '主脑', roleTag: 'main', description: '统筹全局', isMain: true, prompt: 'MAIN PROMPT' },
  { name: '审查员', roleTag: 'reviewer', description: '代码审查', prompt: 'SUB PROMPT' },
]

// ④ 预览页签按三层渲染
test('④ 三层渲染：全局层（后端为准）/ 场景层（description + 成员段）/ agent 层（当前 agent prompt）', () => {
  const layers = buildPromptLayers({ description: '我们是一个团队', agents: AGENTS })

  // 全局层：后端代码写死 → 前端取不到，标注 available=false（不编造文案）
  assert.deepEqual(layers.global, { available: false, content: '' })

  // 场景层 = 「【场景】描述」+「【团队成员】成员段」（**与后端 turn.go scenarioLayer 同款格式**）
  assert.equal(
    layers.scenario.content,
    '【场景】我们是一个团队\n【团队成员】\n- 主脑（main）：统筹全局\n- 审查员（reviewer）：代码审查',
  )

  // agent 层：未显式给 agent → 取主 agent
  assert.deepEqual(layers.agent, { name: '主脑', content: 'MAIN PROMPT' })

  // 显式给 agent（编辑器左侧选中项）→ agent 层跟随；空 description → 只余成员段
  const picked = buildPromptLayers({ description: '', agents: AGENTS, agent: AGENTS[1] })
  assert.deepEqual(picked.agent, { name: '审查员', content: 'SUB PROMPT' })
  assert.equal(
    picked.scenario.content,
    '【团队成员】\n- 主脑（main）：统筹全局\n- 审查员（reviewer）：代码审查',
  )
})

test('④ 成员段拼装：名字 + roleTag + 描述（缺项降级、空项过滤）', () => {
  assert.equal(composeScenarioMembers([{ name: 'A' }]), '- A')
  assert.equal(composeScenarioMembers([{ name: 'A', roleTag: 'r' }]), '- A（r）')
  assert.equal(composeScenarioMembers([{ name: 'A', description: 'd' }]), '- A：d')
  assert.equal(composeScenarioMembers([{ roleTag: 'r', description: 'd' }]), '- （r）：d')
  // 字段各自 trim（与后端 membersSegment 同口径）；全空条目 / 非对象 / 非数组 → 过滤
  assert.equal(composeScenarioMembers([{ name: ' A ', roleTag: ' r ', description: ' d ' }]), '- A（r）：d')
  assert.equal(composeScenarioMembers([{}, null, undefined, 'x', { name: '   ' }]), '')
  assert.equal(composeScenarioMembers(null), '')
  // 场景层：空 description / 空 agents → 空段省略（不产生多余前缀或空行）
  assert.equal(composeScenarioLayer({ description: '  ', agents: [] }), '')
  assert.equal(composeScenarioLayer({ description: 'd' }), '【场景】d')
  assert.equal(composeScenarioLayer({ description: '  d  ' }), '【场景】d')
  assert.equal(composeScenarioLayer(null), '')
  // 无场景（通用模式）→ 只有全局层（场景层 / agent 层为空）
  const empty = buildPromptLayers({})
  assert.equal(empty.scenario.content, '')
  assert.deepEqual(empty.agent, { name: '', content: '' })
})

// 格式一致性守卫：前端预览复刻后端拼接格式（后端 = 唯一注入实现，25 §8.1 #3）
test('格式一致性：场景层格式与后端 turn.go（scenarioLayer / membersSegment）同款', () => {
  const go = readFileSync(join(here, '..', '..', '..', 'src', 'lib', 'llm', 'server', 'turn.go'), 'utf8')
  assert.match(go, /"【场景】"/, '后端场景层前缀须为「【场景】」（前端预览同款）')
  assert.match(go, /"【团队成员】\\n"/, '后端成员段前缀须为「【团队成员】\\n」（前端预览同款）')
  assert.match(go, /line := "- " \+ name/, '成员行须以 "- " + 名字 起（前端预览同款）')
  assert.match(go, /line \+= "（" \+ role \+ "）"/, 'roleTag 须以全角括号包裹（前端预览同款）')
  assert.match(go, /line \+= "：" \+ desc/, '描述须以全角冒号分隔（前端预览同款）')
  // 全局层：后端代码写死（前端不编造）——2026-09-26 起由常量改为方法（用户正式文案口径）
  assert.match(go, /func \(s \*Server\) globalLayerPrompt\(/, '全局层须由后端代码写死（前端不编造）')
  assert.match(go, /一个全能智能体/, '全局层文案须含「一个全能智能体」')
  assert.match(go, /你运行在 /, '全局层文案须含「你运行在 … 中」')
  assert.match(go, /defaultAgentName = "肥猫"/, '通用模式（无场景）身份名须为「肥猫」')
})

// 预览页签接线：只读、三层可见、全局层标注以后端为准（不硬编码业务文案）
test('预览页签接线守卫：只读展示 + 全局层以后端为准 + 三层标题', () => {
  const preview = read('views/scenario/CombinedPromptPreview.vue')
  assert.match(preview, /import \{ buildPromptLayers \} from '\.\.\/\.\.\/utils\/promptLayers'/)
  assert.match(preview, /const layers = computed\(\(\) => buildPromptLayers\(\{/)
  // 三层标题 + 全局层「以后端为准」标注
  for (const key of ['global_layer', 'global_backend_note', 'scenario_layer', 'agent_layer']) {
    assert.match(preview, new RegExp(`scenario\\.preview\\.${key}`), `预览须含 ${key}`)
  }
  // 只读：无输入控件（input / textarea / contenteditable）
  assert.doesNotMatch(preview, /<input|<textarea|contenteditable/)

  const dialog = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(dialog, /import CombinedPromptPreview from '\.\/CombinedPromptPreview\.vue'/)
  assert.match(dialog, /<Tabs class="scenario-right-tabs" :tabs="rightTabs" v-model="rightTab">/)
  assert.match(dialog, /<template #combined>/)
  assert.match(dialog, /:description="form\.description"/)
  assert.match(dialog, /:agents="agents"/)
  assert.match(dialog, /:agent="selectedAgent"/)
  assert.match(dialog, /\{ name: 'combined', label: t\('scenario\.preview\.tab'\) \}/)
})

// i18n：中英双语补齐（scenario.preview.* / chat.prompt_*）
test('i18n 补齐：中英双语键齐备', () => {
  const zhScenario = JSON.parse(read('locales/zh-CN/scenario.json'))
  const enScenario = JSON.parse(read('locales/en-US/scenario.json'))
  const zhChat = JSON.parse(read('locales/zh-CN/chat.json'))
  const enChat = JSON.parse(read('locales/en-US/chat.json'))

  const previewKeys = ['tab', 'hint', 'global_layer', 'global_backend_note', 'scenario_layer', 'agent_layer', 'empty']
  for (const k of previewKeys) {
    assert.ok(zhScenario.preview && zhScenario.preview[k], `zh-CN scenario.preview.${k} 缺失`)
    assert.ok(enScenario.preview && enScenario.preview[k], `en-US scenario.preview.${k} 缺失`)
  }
  for (const k of ['prompt_pick', 'prompt_none', 'prompt_empty', 'prompt_remove', 'prompt_load_failed']) {
    assert.ok(zhChat[k], `zh-CN chat.${k} 缺失`)
    assert.ok(enChat[k], `en-US chat.${k} 缺失`)
  }
})

// 项目规则：不得用 watch / watchEffect 监听 props / store（新增代码同守）
test('项目规则：新增/改动的前端代码无 watch / watchEffect', () => {
  for (const rel of [
    'views/scenario/ScenarioEditDialog.vue',
    'views/scenario/CombinedPromptPreview.vue',
    'views/chat/InputBox.vue',
    'composables/useChatPrompts.js',
    'utils/promptLayers.js',
    'utils/chatPrompt.js',
  ]) {
    assert.doesNotMatch(read(rel), /\bwatchEffect\(|\bwatch\(/, `${rel} 不得使用 watch / watchEffect`)
  }
})
