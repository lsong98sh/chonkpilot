/**
 * 智能体（agent）与场景引用（P4，2026-10-01）前端守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = `*.vue` 源码守卫 + 纯逻辑断言。
 *
 * 覆盖：
 *  1) agent 成为能力面原语：primitive.js PRIMITIVE_TOKENS 含 agent；*.agent.md → renderType primitive
 *  2) CodeView/PrimitivePanel 对 *.agent.md 渲染**智能体编辑器**（复用 AgentEditor，禁止复制实现）
 *  3) AgentEditor 可复用（showCopy 可控；场景侧行为不变）
 *  4) 场景编辑：级别选择器（四级）+ agent 列表改为从 agents/ 选择 + **子 agent = 可编辑引用**
 *     （选中读被引文件 → 编辑 → 保存写回；ref 不变）
 *  5) 场景 agent 选择受级别矩阵约束（与工具矩阵同一集合）
 *  6) ref 前缀 → 级别根 → 绝对路径解析 + 契约文档 ↔ agent 模型互转（纯函数）
 *  7) i18n：scenario.level.prjusr / pick_agent / ref_label / ref_edit_hint 在 zh + en 齐备
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { PRIMITIVE_TOKENS, primitiveTokenOf, isPrimitiveFile } from '../src/utils/primitive.js'
import { resolveAgentRefAbs, agentModelFromDoc, agentDocOf } from '../src/utils/agentRef.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

test('agent 原语：token 齐备 + *.agent.md 识别为原语文件', () => {
  assert.ok(PRIMITIVE_TOKENS.includes('agent'), 'PRIMITIVE_TOKENS 应含 agent')
  assert.equal(primitiveTokenOf('a/b/UX 设计师.agent.md'), 'agent')
  assert.equal(isPrimitiveFile('/x/capability/agents/foo.agent.md'), true)
  assert.equal(isPrimitiveFile('/x/capability/agents/foo.md'), false)
})

test('CodeView：*.agent.md → renderType primitive（走 PrimitivePanel → 智能体编辑器）', () => {
  const cv = read('views/codeview/CodeView.vue')
  assert.match(cv, /isPrimitiveFile\(path\)\) return 'primitive'/, '*.agent.md 应判定为 primitive')
  assert.match(cv, /\.agent\.md/, '注释应含 *.agent.md（智能体编辑器）')
})

test('PrimitivePanel：token=agent → 渲染 AgentEditor（复用，禁复制实现）', () => {
  const panel = read('views/knowledge/PrimitivePanel.vue')
  assert.match(panel, /from '\.\.\/scenario\/AgentEditor\.vue'/, '应复用场景侧 AgentEditor')
  assert.match(panel, /const isAgent = computed\(\(\) => token\.value === 'agent'\)/, 'isAgent 判定')
  assert.match(panel, /v-if="isAgent"[\s\S]{0,400}<AgentEditor/, '智能体模式渲染 AgentEditor')
  assert.match(panel, /@update:agent="onAgentUpdate"/, '编辑回写契约文档 form')
  assert.match(panel, /@optimize="onAgentOptimize"/, '优化提示词回写正文')
  assert.match(panel, /:show-copy="false"/, '原语编辑不显示「复制」按钮')
  // 保存仍走原语保存通道（data-knowledge-save）
  assert.match(panel, /savePrimitive\(props\.path/, '保存仍走 savePrimitive（data-knowledge-save）')
  // 工具/LLM 装载与场景编辑共用（零重复实现）
  assert.match(panel, /from '\.\.\/\.\.\/utils\/agentAssets'/, '复用 utils/agentAssets 装载')
  assert.doesNotMatch(panel, /mq\.emit\(\s*['"]/, '不得用裸主题字面量')
})

test('AgentEditor：showCopy 可控（场景子 agent 复制 / 原语编辑隐藏）', () => {
  const ae = read('views/scenario/AgentEditor.vue')
  assert.match(ae, /showCopy:\s*\{[\s\S]{0,60}default:\s*true/, 'showCopy prop 默认 true')
  assert.match(ae, /v-if="!agent\.isMain && showCopy"/, '复制按钮受 showCopy 门控')
})

test('场景编辑：级别选择器（四级，替代硬编码 level:user）', () => {
  const dlg = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(dlg, /const LEVELS = \['app', 'user', 'project', 'prjusr'\]/, '四级级别常量')
  assert.match(dlg, /:options="levelOptions"[\s\S]{0,80}@update:modelValue="onLevelChange"/, '级别选择器绑定变更')
  assert.match(dlg, /scenario\.level\./, '级别文案走 i18n')
})

test('场景编辑：子 agent = 可编辑引用（读被引文件 → 编辑 → 保存写回，ref 不变）', () => {
  const dlg = read('views/scenario/ScenarioEditDialog.vue')
  // 选择器（从 agents/ 列出）+ 引用追加
  assert.match(dlg, /async function loadAgentOptions\(/, '应列可选智能体')
  assert.match(dlg, /listPrimitives/, '经知识库门面列 agents/')
  assert.match(dlg, /ref: abs/, '选中项以绝对路径作 ref（保存时后端归一为引用路径）')
  assert.match(dlg, /async function onPickAgent\(/, '选中即追加引用 agent')
  // 可编辑引用：读被引文件 → 复用 AgentEditor 编辑（禁复制实现）
  assert.match(dlg, /import AgentEditor from '\.\/AgentEditor\.vue'/, '复用 AgentEditor')
  assert.match(dlg, /readPrimitive\(/, '选中引用 agent 时读被引文件（data-knowledge-read）')
  assert.match(dlg, /async function loadRefAgent\(/, '载入被引文件正文')
  assert.match(dlg, /resolveAgentRefAbs/, '按变量前缀解析 ref → 绝对路径')
  // 写回：保存时走知识库写盘，内容未变则跳过；场景 ref 不变
  assert.match(dlg, /savePrimitive\(/, '保存时写回被引文件（data-knowledge-save）')
  assert.match(dlg, /async function writeBackRefAgents\(/, '保存前写回被编辑的引用文件')
  assert.match(dlg, /_refOrig/, '以载入基准判定内容是否变更（未变不写）')
  assert.match(dlg, /class="ref-agent-banner"/, '引用路径横幅（可编辑，非只读面板）')
  assert.doesNotMatch(dlg, /class="ref-agent-view"/, '旧的「只读引用视图」已移除')
  assert.doesNotMatch(dlg, /ref-prompt/, '旧的只读提示词块已移除')
  // 不再有「添加子 Agent」按钮 / 复制实现
  assert.doesNotMatch(dlg, /scenarioAddSubAgent/, '不再用「添加子 Agent」入口')
  assert.doesNotMatch(dlg, /function addSubAgent\(/, '不再新建内联子 agent')
  assert.doesNotMatch(dlg, /watch\(|watchEffect\(/, '不得用 watch/watchEffect')
})

test('agentRef：ref 前缀 → 级别根 → 绝对路径（四级前缀）', () => {
  const roots = {
    app: 'E:/app/capability',
    user: 'C:/u/.chonkpilot/capability',
    project: 'P:/proj/.chonkpilot/capability',
    prjusr: 'C:/u/data/pid/capability',
  }
  assert.equal(
    resolveAgentRefAbs('${exeDir}/capability/agents/UX 设计师.agent.md', roots),
    'E:/app/capability/agents/UX 设计师.agent.md',
  )
  assert.equal(
    resolveAgentRefAbs('${usrDir}/capability/agents/a.agent.md', roots),
    'C:/u/.chonkpilot/capability/agents/a.agent.md',
  )
  assert.equal(
    resolveAgentRefAbs('${workDir}/.chonkpilot/capability/agents/b.agent.md', roots),
    'P:/proj/.chonkpilot/capability/agents/b.agent.md',
  )
  assert.equal(
    resolveAgentRefAbs('${dataDir}/capability/agents/c.agent.md', roots),
    'C:/u/data/pid/capability/agents/c.agent.md',
  )
  // 已是绝对路径（新建引用尚未落盘）→ 原样
  assert.equal(resolveAgentRefAbs('E:/x/capability/agents/y.agent.md', roots), 'E:/x/capability/agents/y.agent.md')
  // 未知前缀 / 该级根不可解析 → ''
  assert.equal(resolveAgentRefAbs('${nope}/capability/agents/z.agent.md', roots), '')
  assert.equal(resolveAgentRefAbs('${exeDir}/capability/agents/z.agent.md', { app: '' }), '')
})

test('agentRef：ref 型 agent 可读 → 编辑 → 写回（格式保真、ref 不变）', () => {
  const refForm = '${exeDir}/capability/agents/代码审查.agent.md'
  // 读：契约文档 → 可编辑模型（未知 meta 保留在 _refMeta）
  const doc = { title: '代码审查', meta: { roletag: '审查', custom: 'keep' }, description: '审查', content: '旧提示词' }
  const agent = { name: '代码审查', ref: refForm, ...agentModelFromDoc(doc) }
  assert.equal(agent.prompt, '旧提示词')
  assert.equal(agent.roleTag, '审查')
  assert.equal(agent._refMeta.custom, 'keep')
  // 编辑：改提示词 + 勾选工具
  agent.prompt = '新提示词'
  agent.tools = ['mcp__x__read']
  // 写回：生成契约文档（未知 meta 保留；tools 以 JSON 串落 meta）
  const out = agentDocOf(agent)
  assert.equal(out.title, '代码审查')
  assert.equal(out.content, '新提示词')
  assert.equal(out.meta.custom, 'keep')
  assert.equal(out.meta.roletag, '审查')
  assert.equal(out.meta.tools, '["mcp__x__read"]')
  // ref 不变：写回文档不含 ref，场景 agent 的 ref 字段未被改写
  assert.equal('ref' in out, false)
  assert.equal(agent.ref, refForm)
})

test('场景编辑：agent 选择受级别矩阵约束（与工具矩阵同一集合，单源 agentAssets）', () => {
  const dlg = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(dlg, /from '\.\.\/\.\.\/utils\/agentAssets'/, '级别矩阵单源复用 agentAssets')
  assert.doesNotMatch(dlg, /const AGENT_LEVEL_MATRIX/, '矩阵不得在场景编辑内重复定义（单源）')
  assert.match(dlg, /allowedLevels\(form\.value\.level/, '按场景级别取可用集合')
})

test('i18n：level.prjusr / pick_agent / ref_* 在 zh + en 齐备', () => {
  for (const loc of LOCALES) {
    const sc = readLocale(loc, 'scenario.json')
    assert.ok(sc.level && sc.level.prjusr, loc + ' 缺 level.prjusr')
    for (const k of ['app', 'user', 'project']) assert.ok(sc.level[k], loc + ' 缺 level.' + k)
    for (const k of ['pick_agent', 'ref_label', 'ref_edit_hint', 'ref_load_failed', 'ref_save_failed', 'ref_unresolved', 'agent_already_added']) {
      assert.ok(sc[k], loc + ' 缺 ' + k)
    }
  }
})
