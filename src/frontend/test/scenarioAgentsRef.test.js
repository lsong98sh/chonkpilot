/**
 * 智能体（agent）与场景引用（P4，2026-10-01）前端守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = `*.vue` 源码守卫 + 纯逻辑断言。
 *
 * 覆盖：
 *  1) agent 成为能力面原语：primitive.js PRIMITIVE_TOKENS 含 agent；*.agent.md → renderType primitive
 *  2) CodeView/PrimitivePanel 对 *.agent.md 渲染**智能体编辑器**（复用 AgentEditor，禁止复制实现）
 *  3) AgentEditor 可复用（showCopy 可控；场景侧行为不变）
 *  4) 场景编辑：级别选择器（四级）+ agent 列表改为从 agents/ 选择（只读显示已选）+ 引用只读视图
 *  5) 场景 agent 选择受级别矩阵约束（与工具矩阵同一集合）
 *  6) i18n：scenario.level.prjusr / pick_agent / ref_label / ref_edit_hint 在 zh + en 齐备
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { PRIMITIVE_TOKENS, primitiveTokenOf, isPrimitiveFile } from '../src/utils/primitive.js'

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

test('场景编辑：agent 列表改为从 agents/ 选择（只读显示已选）+ 引用只读视图', () => {
  const dlg = read('views/scenario/ScenarioEditDialog.vue')
  // 选择器（从 agents/ 列出）+ 引用追加
  assert.match(dlg, /async function loadAgentOptions\(/, '应列可选智能体')
  assert.match(dlg, /getKnowledgeRoot\(kind\)[\s\S]{0,200}listPrimitives/, '经知识库门面列 agents/')
  assert.match(dlg, /ref: abs/, '选中项以绝对路径作 ref（保存时后端归一为引用路径）')
  assert.match(dlg, /function onPickAgent\(/, '选中即追加引用 agent')
  // 子 agent 只读（引用视图）
  assert.match(dlg, /class="ref-agent-view"/, '子 agent 引用只读视图')
  assert.match(dlg, /selectedAgent\.ref/, '只读视图展示引用路径')
  assert.match(dlg, /scenario\.ref_edit_hint/, '提示到「扩展 · 智能体」页编辑')
  // 不再有「添加子 Agent」按钮 / 复制实现
  assert.doesNotMatch(dlg, /scenarioAddSubAgent/, '不再用「添加子 Agent」入口')
  assert.doesNotMatch(dlg, /function addSubAgent\(/, '不再新建内联子 agent')
  assert.doesNotMatch(dlg, /watch\(|watchEffect\(/, '不得用 watch/watchEffect')
})

test('场景编辑：agent 选择受级别矩阵约束（与工具矩阵同一集合，单源 agentAssets）', () => {
  const dlg = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(dlg, /from '\.\.\/\.\.\/utils\/agentAssets'/, '级别矩阵单源复用 agentAssets')
  assert.doesNotMatch(dlg, /const AGENT_LEVEL_MATRIX/, '矩阵不得在场景编辑内重复定义（单源）')
  assert.match(dlg, /allowedLevels\(form\.value\.level/, '按场景级别取可用集合')
})

test('i18n：level.prjusr / pick_agent / ref_label / ref_edit_hint 在 zh + en 齐备', () => {
  for (const loc of LOCALES) {
    const sc = readLocale(loc, 'scenario.json')
    assert.ok(sc.level && sc.level.prjusr, loc + ' 缺 level.prjusr')
    for (const k of ['app', 'user', 'project']) assert.ok(sc.level[k], loc + ' 缺 level.' + k)
    for (const k of ['pick_agent', 'ref_label', 'ref_edit_hint', 'agent_already_added']) {
      assert.ok(sc[k], loc + ' 缺 ' + k)
    }
  }
})
