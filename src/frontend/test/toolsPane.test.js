/**
 * 「工具」页签（工具从知识库分离，2026-09-29）前端守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = `*.vue` 源码守卫 + 纯逻辑断言
 * （与 memoryTree / sessionNavCopy 同法）。
 *
 * 覆盖：
 *  1) ExplorerPane 第 5 分段「工具」位于「会话」右侧；两个 v-show body 分别渲染 KnowledgeTree
 *  2) 类型范围：知识库 = skill/prompt/resource（不含 tool）；工具 = 仅 tool
 *  3) 编辑层级与知识库一致（同一 KnowledgeTree：三级根 / 右键菜单 / 编辑面板）
 *  4) 工具的新建/重命名/删除菜单判定沿用 nearestTypeToken（tool 目录 → 「新建工具」）
 *  5) 零新增 MQ 主题（复用 kb-ctx-action / kb-level-select / file-open / filetree-mode-*）
 *  6) i18n mode_tools / tools_empty（zh + en 齐备）
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { nearestTypeToken, TYPE_DIR_REL, PRIMITIVE_TOKENS } from '../src/utils/primitive.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

test('工具页签：位于「会话」右侧 + setMode 受理 tools + i18n', () => {
  const pane = read('views/filetree/ExplorerPane.vue')
  const idxSessions = pane.indexOf("mode === 'sessions'")
  const idxTools = pane.indexOf("mode === 'tools'")
  assert.ok(idxSessions >= 0 && idxTools > idxSessions, '「工具」分段应在「会话」右侧')
  // 五个分段按钮（v-show 同显 5 体）
  assert.match(pane, /fileTree\.mode_tools/, '工具分段文案应走 i18n')
  assert.match(pane, /m === 'tools'/, 'setMode 应受理 tools')
  // 刷新图标在 tools 模式也显示
  assert.match(pane, /mode === 'tools'"\s*name="refresh"/, 'tools 模式应显示刷新图标')
  assert.match(pane, /mode\.value === 'tools'[\s\S]{0,60}toolsTreeRef\.value\?\.reload\(\)/,
    'toolbar 刷新在 tools 模式应刷新工具树')
  // 保留既有语义：toggle 仍是 project ↔ knowledge（休眠态）
  assert.match(pane, /mode\.value === 'project' \? 'knowledge' : 'project'/,
    'filetreeModeToggle 休眠态语义不变（仍只切 project↔knowledge）')
})

test('工具面板：复用 KnowledgeTree（kinds 类型范围），知识库不含 tool', () => {
  const pane = read('views/filetree/ExplorerPane.vue')
  // 两个 KnowledgeTree 实例：scope 区分 + kinds 过滤
  assert.match(pane, /KB_KINDS = \['skill', 'prompt', 'resource'\]/, '知识库类型范围 = skill/prompt/resource')
  assert.match(pane, /TOOL_KINDS = \['tool'\]/, '工具类型范围 = tool')
  assert.match(pane, /scope="knowledge"[\s\S]{0,120}:kinds="KB_KINDS"/, '知识库实例传 KB_KINDS')
  assert.match(pane, /scope="tools"[\s\S]{0,120}:kinds="TOOL_KINDS"/, '工具实例传 TOOL_KINDS')
  assert.match(pane, /title-key="fileTree\.mode_tools"/, '工具实例标题走 mode_tools')
  // 不得复制粘贴实现（工具面板不新造树逻辑）
  assert.doesNotMatch(pane, /<ToolsPane/, '应直接复用 KnowledgeTree，不引入复制实现')
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.match(tree, /defineProps\(\{[\s\S]*?kinds:[\s\S]*?scope:[\s\S]*?titleKey:[\s\S]*?emptyKey:/,
    'KnowledgeTree 应声明 kinds/scope/titleKey/emptyKey props')
  assert.match(tree, /filter\(n => dirInScope\(n\.path\)\)/, '目录按类型范围过滤')
  assert.match(tree, /filter\(fileInScope\)/, '文件按类型范围过滤')
  assert.match(tree, /nearestTypeToken, TYPE_DIR_REL/, '过滤依赖 TYPE_DIR_REL')
  // 组件名不变（多实例复用/回归定位）
  assert.match(tree, /defineOptions\(\{ name: 'KnowledgeTree' \}\)/, 'defineOptions name 保持')
  // 三级根 + reload 暴露保持与知识库一致
  assert.match(tree, /defineExpose\(\{ reload, loadRoot \}\)/, 'reload/loadRoot 暴露保持')
  // 多实例共存：非可见实例不接管 document keydown（避免隐藏树被 F2 改名）
  assert.match(tree, /rootRef\.value\.offsetParent === null/, '非可见树不接管键盘')
})

test('工具树：右键菜单判定（新建工具/重命名/删除）沿用 nearestTypeToken', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  // dirItems：类型目录（含 tools）→ 「新建{类型}」；文件 → 重命名/删除
  assert.match(tree, /function dirItems\(node\)[\s\S]*?nearestTypeToken\(node\.path\)[\s\S]*?newType/,
    '类型目录右键应含「新建{类型}」（工具目录 → 新建工具）')
  assert.match(tree, /function fileItems\(node\)[\s\S]*?'rename'[\s\S]*?'delete'/, '文件右键 = 重命名/删除')
  assert.match(tree, /else if \(item\.key === 'newType'\) createTypeFile\(d\.path, nearestTypeToken\(d\.path\)\)/,
    'newType 走 createTypeFile（后端按 type 生成契约模板）')
  assert.match(tree, /type_tool/, '类型显示名含 工具')
})

test('工具树：编辑层级与知识库一致（三级根 app/user/project）', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.match(tree, /kind: 'app'[\s\S]*?kind: 'user'[\s\S]*?kind: 'project'/, '三级根：app/user/project')
  assert.match(tree, /EventNames\.kbLevelSelect/, '级别切换复用 kb-level-select')
  // 系统级只读（可写性规则与知识库一致）
  assert.match(tree, /function isKbReadonly\(\)[\s\S]*?kbLevel\.value === 'app'/, 'app 级只读规则保持')
})

test('零新增 MQ 主题：工具页签复用既有主题，无裸主题字面量', () => {
  for (const f of ['views/filetree/ExplorerPane.vue', 'views/filetree/KnowledgeTree.vue']) {
    const src = read(f)
    assert.doesNotMatch(src, /mq\.(emit|on)\(\s*['"]/, `${f} 不得用裸主题字面量（须走 EventNames）`)
  }
  const names = read('events/event-names.js')
  // 复用既有主题：右键动作仍为 kb-ctx-action（新增 scope 字段，非新主题）
  assert.match(names, /kbCtxAction: 'kb-ctx-action',[\s\S]*?scope/, 'kb-ctx-action payload 增 scope（多实例分流）')
  assert.match(read('views/filetree/KnowledgeTree.vue'), /\{ key: item\.key, scope \}/,
    '右键菜单动作携带 scope')
  assert.match(read('views/filetree/KnowledgeTree.vue'), /if \(s && s !== props\.scope\) return/,
    '订阅按 scope 过滤，避免多实例串扰')
})

test('工具树空态/类型范围纯逻辑（TYPE_DIR_REL / nearestTypeToken）', () => {
  assert.deepEqual(TYPE_DIR_REL, {
    tool: 'tools',
    skill: 'knowledge/skills',
    prompt: 'knowledge/prompts',
    resource: 'knowledge/resources',
  })
  // token 集合齐备（四类）
  assert.deepEqual(PRIMITIVE_TOKENS, ['tool', 'skill', 'prompt', 'resource'])
  const base = '/x/.chonkpilot/capability'
  assert.equal(nearestTypeToken(base + '/tools'), 'tool')
  assert.equal(nearestTypeToken(base + '/tools/core'), 'tool')
  assert.equal(nearestTypeToken(base + '/knowledge'), '') // 通用容器：无类型 token
  assert.equal(nearestTypeToken(base + '/knowledge/skills'), 'skill')
  assert.equal(nearestTypeToken(base), '') // 能力根：无类型 token
})

test('i18n：mode_tools / tools_empty 在 zh + en 齐备', () => {
  for (const loc of LOCALES) {
    const ft = readLocale(loc, 'fileTree.json')
    assert.ok(ft.mode_tools, loc + ' 缺 mode_tools')
    assert.ok(ft.tools_empty, loc + ' 缺 tools_empty')
    assert.ok(ft.mode_sessions, loc + ' 缺 mode_sessions（既有）')
  }
})
