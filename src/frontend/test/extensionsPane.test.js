/**
 * 「扩展」页（原「知识库 + 工具」两分段合并，P3，2026-10-01）前端守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = `*.vue` 源码守卫 + 纯逻辑断言
 * （与 memoryTree / sessionNavCopy 同法）。
 *
 * 覆盖：
 *  1) ExplorerPane 四分段「项目 / 会话 / 记忆 / 扩展」（顺序 + 内容体）；原「知识库」「工具」分段已删
 *  2) 扩展页 5 子 tab（知识 resources / 技能 skills / 工具 tools / 命令 prompts / 智能体 agents）
 *  3) 右侧【级别】popup（Popover + kb-level-select，四级）
 *  4) 级别状态由扩展页统一持有并下发给 KnowledgeTree（props.level）
 *  5) 四级均可写（撤掉 isKbReadonly / kb_readonly 文案）
 *  6) 零新增 MQ 主题（复用 filetree-mode-select / kb-level-select / kb-ctx-action / file-open）
 *  7) primitive.js agent token（PRIMITIVE_TOKENS / TYPE_DIR_MAP / TYPE_DIR_REL）
 *  8) i18n：mode_extensions / ext_tab_* / kb_level_prjusr / type_agent 在 zh + en 齐备
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { nearestTypeToken, TYPE_DIR_REL, TYPE_DIR_MAP, PRIMITIVE_TOKENS } from '../src/utils/primitive.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

test('扩展页：位于 ExplorerPane 第 4 分段「扩展」，内容体 = ExtensionsPane', () => {
  const pane = read('views/filetree/ExplorerPane.vue')
  const idxSessions = pane.indexOf("mode === 'sessions'")
  const idxExtensions = pane.indexOf("mode === 'extensions'")
  assert.ok(idxSessions >= 0 && idxExtensions > idxSessions, '「扩展」分段应在「会话」右侧')
  assert.match(pane, /fileTree\.mode_extensions/, '扩展分段文案应走 i18n')
  assert.match(pane, /m === 'extensions'/, 'setMode 应受理 extensions')
  assert.match(pane, /<ExtensionsPane/, '扩展分段内容体 = ExtensionsPane')
  // 「知识库」「工具」两个一级分段已删除
  assert.doesNotMatch(pane, /mode === 'knowledge'/, '「知识库」分段已删除')
  assert.doesNotMatch(pane, /mode === 'tools'/, '「工具」分段已删除')
  // 刷新图标在 extensions 模式也显示 + 刷新扩展页
  assert.match(pane, /mode === 'memory' \|\| mode === 'extensions'/, 'extensions 模式应显示刷新图标')
  assert.match(pane, /mode\.value === 'extensions'[\s\S]{0,60}extPaneRef\.value\?\.reload\(\)/,
    'toolbar 刷新在 extensions 模式应刷新扩展页')
})

test('扩展页：5 子 tab（知识/技能/工具/命令/智能体）+ kinds 映射', () => {
  const pane = read('views/extensions/ExtensionsPane.vue')
  // 子 tab 顺序：resource → skill → tool → prompt → agent
  const idxResource = pane.indexOf("key: 'resource'")
  const idxSkill = pane.indexOf("key: 'skill'")
  const idxTool = pane.indexOf("key: 'tool'")
  const idxPrompt = pane.indexOf("key: 'prompt'")
  const idxAgent = pane.indexOf("key: 'agent'")
  assert.ok(idxResource >= 0 && idxSkill > idxResource && idxTool > idxSkill && idxPrompt > idxTool && idxAgent > idxPrompt,
    '子 tab 顺序应为 知识 → 技能 → 工具 → 命令 → 智能体')
  // kinds 映射：每子 tab 单类型
  for (const [k, kind] of [['resource', 'resource'], ['skill', 'skill'], ['tool', 'tool'], ['prompt', 'prompt'], ['agent', 'agent']]) {
    assert.match(pane, new RegExp(`key: '${k}'[\\s\\S]{0,120}kinds: \\['${kind}'\\]`), `${k} 子 tab kinds=[${kind}]`)
  }
  // 复用 KnowledgeTree（不复制粘贴实现）
  assert.match(pane, /from '\.\.\/filetree\/KnowledgeTree\.vue'/, '应复用 KnowledgeTree')
  assert.match(pane, /<KnowledgeTree[\s\S]*?:kinds="activeTab\.kinds"[\s\S]*?:level="level"/,
    'KnowledgeTree 应接 kinds + level')
  assert.doesNotMatch(pane, /<ExtTree/, '不得引入复制实现的树组件')
  // 组件名
  assert.match(pane, /defineOptions\(\{ name: 'ExtensionsPane' \}\)/, 'defineOptions name = ExtensionsPane')
})

test('扩展页：右侧【级别】popup（Popover + kb-level-select，四级）', () => {
  const pane = read('views/extensions/ExtensionsPane.vue')
  assert.match(pane, /<Popover/, '级别选择器应用仓库既有 Popover')
  // 四级级别齐备
  assert.match(pane, /kind: 'app'[\s\S]*?kind: 'user'[\s\S]*?kind: 'project'[\s\S]*?kind: 'prjusr'/,
    '级别列表 = app/user/project/prjusr')
  assert.match(pane, /EventNames\.kbLevelSelect/, '级别切换复用 kb-level-select')
  assert.match(pane, /kb_level_/, '级别文案走 i18n（kb_level_*）')
  // 级别状态由扩展页持有并下发（KnowledgeTree 经 props.level 读取）
  assert.match(pane, /const level = ref\(/, '扩展页持有级别状态')
  // 不得含内联按钮组（原内联级别按钮组已移出到本页 popup）
  assert.doesNotMatch(pane, /class="[^"]*level-switch/, '不得沿用原内联按钮组（level-switch 类名）')
})

test('扩展页：子 tab 复用 filetree-mode-select（payload 增可选 ext 字段，零新增主题）', () => {
  const pane = read('views/extensions/ExtensionsPane.vue')
  assert.match(pane, /filetreeModeSelect\]\.click="\{ mode: 'extensions', ext: tab\.key \}"/,
    '子 tab 复用 filetree-mode-select（携带 ext）')
  assert.match(pane, /mq\.on\(EventNames\.filetreeModeSelect[\s\S]{0,120}d\.ext/, '扩展页受理 ext')
  const names = read('events/event-names.js')
  assert.match(names, /filetreeModeSelect[\s\S]{0,200}ext/, 'event-names 注释登记 ext 字段')
  // 零裸主题字面量
  for (const f of ['views/extensions/ExtensionsPane.vue', 'views/filetree/ExplorerPane.vue', 'views/filetree/KnowledgeTree.vue']) {
    const src = read(f)
    assert.doesNotMatch(src, /mq\.(emit|on)\(\s*['"]/, `${f} 不得用裸主题字面量（须走 EventNames）`)
  }
})

test('扩展树：四级均可写（撤掉 isKbReadonly + kb_readonly 文案）', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.doesNotMatch(tree, /isKbReadonly/, 'app 级只读规则已撤掉')
  assert.doesNotMatch(tree, /kb_readonly/, 'kb_readonly 拦截文案已撤掉')
  // 四级均可写：残留的拖拽只拦截根节点（不再按级别拦截）
  assert.match(tree, /if \(node\.path === root\.value\) \{[\s\S]{0,120}preventDefault/, '拖拽只拦截根节点')
  // 级别经 props.level 下发（不再自持 kbLevel）
  assert.match(tree, /level: \{ type: String, default: 'app' \}/, 'KnowledgeTree 应声明 level prop')
  assert.match(tree, /getKnowledgeRoot\(props\.level\)/, 'getKnowledgeRoot 用 props.level')
  assert.doesNotMatch(tree, /kbLevel\.value/, '不得再自持 kbLevel')
  assert.doesNotMatch(tree, /switchLevel/, '不得再自持级别切换逻辑')
  // i18n 不再含 kb_readonly
  for (const loc of LOCALES) {
    const ft = readLocale(loc, 'fileTree.json')
    assert.equal(ft.kb_readonly, undefined, loc + ' 应移除 kb_readonly 文案')
  }
})

test('扩展树：复用 KnowledgeTree（kinds/scope/titleKey/emptyKey/level props），定义名不变', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.match(tree, /defineProps\(\{[\s\S]*?kinds:[\s\S]*?scope:[\s\S]*?titleKey:[\s\S]*?emptyKey:[\s\S]*?level:/,
    'KnowledgeTree 应声明 kinds/scope/titleKey/emptyKey/level props')
  assert.match(tree, /filter\(n => dirInScope\(n\.path\)\)/, '目录按类型范围过滤')
  assert.match(tree, /filter\(fileInScope\)/, '文件按类型范围过滤')
  assert.match(tree, /nearestTypeToken, TYPE_DIR_REL/, '过滤依赖 TYPE_DIR_REL')
  assert.match(tree, /defineOptions\(\{ name: 'KnowledgeTree' \}\)/, 'defineOptions name 保持')
  assert.match(tree, /defineExpose\(\{ reload, loadRoot \}\)/, 'reload/loadRoot 暴露保持')
  assert.match(tree, /rootRef\.value\.offsetParent === null/, '非可见树不接管键盘')
  // agent 类型显示名
  assert.match(tree, /type_agent/, '类型显示名含 智能体')
})

test('原语工具：agent token 齐备（PRIMITIVE_TOKENS / TYPE_DIR_MAP / TYPE_DIR_REL）', () => {
  assert.deepEqual(PRIMITIVE_TOKENS, ['tool', 'skill', 'prompt', 'resource', 'agent'])
  assert.deepEqual(TYPE_DIR_MAP, { tools: 'tool', skills: 'skill', prompts: 'prompt', resources: 'resource', agents: 'agent' })
  assert.deepEqual(TYPE_DIR_REL, {
    tool: 'tools',
    skill: 'skills',
    prompt: 'prompts',
    resource: 'resources',
    agent: 'agents',
  })
  const base = '/x/.chonkpilot/capability'
  assert.equal(nearestTypeToken(base + '/agents'), 'agent') // 智能体目录 → agent
  assert.equal(nearestTypeToken(base + '/skills'), 'skill')
  assert.equal(nearestTypeToken(base), '') // 能力根：无类型 token
})

test('i18n：mode_extensions / ext_tab_* / kb_level_prjusr / type_agent 在 zh + en 齐备', () => {
  for (const loc of LOCALES) {
    const ft = readLocale(loc, 'fileTree.json')
    for (const k of ['mode_extensions', 'ext_tab_resource', 'ext_tab_skill', 'ext_tab_tool', 'ext_tab_prompt', 'ext_tab_agent', 'kb_level_prjusr', 'level_label', 'type_agent']) {
      assert.ok(ft[k], loc + ' 缺 ' + k)
    }
    assert.ok(ft.mode_sessions && ft.mode_memory, loc + ' 缺既有 mode_sessions/mode_memory')
  }
})
