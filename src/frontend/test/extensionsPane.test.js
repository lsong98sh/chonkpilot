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

// ── 2026-10-15 扩展页维护改造（req1~req12）前端守卫 ──────────────────────────
// 同法：`*.vue` 源码守卫（无组件级测试运行器）。

test('扩展树 req1：单类型子 tab 隐去类型目录层 + 根节点显示真实路径', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  // 单类型 → 树根 = 类型目录本身（不再多显示一层）
  assert.match(tree, /const singleTypeRel = computed\(/, '应计算单类型相对类型目录')
  assert.match(tree, /if \(singleTypeRel\.value\) r = r \+ '\/' \+ singleTypeRel\.value/, '树根应下钻到类型目录')
  // 根节点 title = 真实路径
  assert.match(tree, /_tooltip: root\.value/, '根节点应带真实路径 tooltip')
})

test('扩展树 req3/req4：新建先选类型（md 契约 / 其他任意类型文件）', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.match(tree, /resourceScope/, '应识别「资源」子树（唯一支持任意类型文件）')
  assert.match(tree, /newDlg = ref\(\{ open: false, dir: '', kind: 'md', name: '' \}\)/, 'newDlg 状态（默认 md）')
  assert.match(tree, /fileTree\.new_resource_md/, '新建弹框含「知识（md 契约）」选项')
  assert.match(tree, /fileTree\.new_resource_other/, '新建弹框含「其他（任意类型文件）」选项')
  assert.match(tree, /fileTree\.name_immutable_hint/, '新建应提示「名称不可变」')
  // md 走 createPrimitive、other 走 importPrimitive（空内容 → 原样落盘）
  assert.match(tree, /await doCreateTypeFile\(dir, 'resource', name\)/, 'md 分支走 data-knowledge-create')
  assert.match(tree, /await importPrimitive\(dir, \[\{ name, content: '' \}\]\)/, 'other 分支走 data-knowledge-import（空内容）')
})

test('扩展树 req5：目录右键「导入」多文件（data-knowledge-import）', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.match(tree, /key: 'import', label: t\('fileTree\.import_files'\)/, '右键应有「导入」项')
  assert.match(tree, /async function doImport\(dirPath\)/, '应有 doImport')
  assert.match(tree, /el\.multiple = true/, '文件选择应支持多选')
  assert.match(tree, /readFileBase64/, '应读为 base64（对齐 payload content）')
  const api = read('api/knowledge.js')
  assert.match(api, /export function importPrimitive\(dir, files\)/, 'api 应导出 importPrimitive')
  assert.match(api, /knowledgeReq\('import', \{ dir, files \}\)/, '走 data-knowledge-import 主题')
})

test('扩展树 req6：目录/文件右键 = 资源管理器显示；文件 = 打开 / 打开方式', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.match(tree, /revealInExplorer, openWithDefault, openWithDialog/, '应引入 gui 三个系统能力封装')
  assert.match(tree, /key: 'reveal', label: t\('fileTree\.reveal_in_explorer'\)/, '应有「在资源管理器显示」')
  assert.match(tree, /key: 'open', label: t\('fileTree\.open'\)/, '文件应有「打开」')
  assert.match(tree, /key: 'openWith', label: t\('fileTree\.open_with'\)/, '文件应有「打开方式」')
})

test('扩展树 req4：其他类型文件（type=file）上树 + 单击仅选中 / 双击系统默认打开', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  // 放行 type='file'（仅资源子树）
  assert.match(tree, /f\.type === 'file' && resourceScope\.value/, 'loadChildren 应放行 raw 文件（资源子树）')
  assert.match(tree, /if \(raw\.type === 'file'\) return resourceScope\.value/, 'fileInScope 应按资源子树放行')
  // 交互：单击仅选中、双击系统默认打开（应用内不预览）
  assert.match(tree, /if \(node\.type === 'file'\) return/, 'raw 文件单击仅选中')
  assert.match(tree, /if \(node\.type === 'file'\) \{[\s\S]{0,80}doOpen\(node\.path\)/, 'raw 文件双击系统默认打开')
})

test('扩展树 req2：删除后本地广播 filesys.changed 关闭对应页签', () => {
  const tree = read('views/filetree/KnowledgeTree.vue')
  assert.match(tree, /function notifyRemoved\(path\)/, '应有 notifyRemoved')
  assert.match(tree, /mq\.emitRemote\(\{ type: EventNames\.fileChanged, payload: \{ path: p, operation: 'remove' \} \}\)/,
    '删除后应本地广播 filesys.changed（仅本地分发）')
  assert.match(tree, /notifyRemoved\(node\.path\)/, 'removeNode 应调用 notifyRemoved')
})

test('i18n：req2/req4/req5 新词条在 zh + en 齐备', () => {
  for (const loc of LOCALES) {
    const ft = readLocale(loc, 'fileTree.json')
    for (const k of ['name_immutable_hint', 'new_resource_title', 'new_resource_md', 'new_resource_other',
      'other_file_name_ph', 'import_files', 'import_done', 'import_partial', 'import_failed',
      'import_failed_detail', 'import_no_files', 'open', 'open_with', 'reveal_in_explorer']) {
      assert.ok(ft[k], loc + ' 缺 ' + k)
    }
  }
})

test('req12：主会话列表改就地改名（inplace-editor，非 promptInput 弹框）', () => {
  const sp = read('views/sessions/SessionsPane.vue')
  assert.match(sp, /class="session-title-input"/, '标题行应就地渲染 Input')
  assert.match(sp, /@keyup\.enter="confirmRename"/, 'Enter 提交')
  assert.match(sp, /@keyup\.escape="cancelRename"/, 'Esc 取消')
  assert.match(sp, /function startRename\(session\)/, '应有 startRename')
  assert.match(sp, /sessionApi\.updateSessionTitle\(id, val\)/, '提交仍走 updateSessionTitle')
  assert.doesNotMatch(sp, /promptInput/, '不得再用 promptInput（弹框改名）')
})
