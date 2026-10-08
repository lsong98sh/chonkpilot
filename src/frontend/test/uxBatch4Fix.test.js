/**
 * 用户视角缺陷「批 4」修复守卫（2026-09-24 用户口径）：
 *   A1 记忆类别内容 / 用户偏好 → **弹框**编辑 + 保存即关 + 关闭后刷新预估 token + 弹框自带优化；
 *   A2 编辑总结提示词 → 同范式（弹框 + 保存即关 + 内嵌优化 + 恢复优化）；
 *   A3 记忆沉淀成功 → **静默**（不弹成功提示）；
 *   A4 底部「记忆总 token 数」→ 记忆分类列表 → 选中项弹框显示内容（可编辑、可保存）；
 *   A5 编辑类弹框内容**撑满**、底部**不留白**；
 *   A6 非激活 Tab 文字与激活态**可见差别**（走主题 token，逐 scheme 定义）；
 *   A7 「继续」= **文本按钮** + **左对齐** + 正常回复后/会话进行中不显示；
 *   A8 会话 drawer 的 fork → 点击显示「敬请期待」（占位，不实现真 fork）；
 *   B2 底部「打开（全局）配置」→ 展开**全部配置**菜单（与工具栏「设置」同源清单）；
 *   B3 屏蔽 F12/DevTools 快捷键（宿主层）+ 底部调试图标经 gui.devtools.open 打开 DevTools；
 *   A9 上下文管理「记忆库」关闭 → 其子项**逐个**联动禁用；保存期间不被自身广播重载冲回（2026-09-28）。
 *
 * 前端无组件级运行器（`npm test` = node:test 直跑）→ `*.vue`/`.js` 源码守卫 +
 * i18n 实键校验 + **跨端字面量核对**（Go 宿主/桥/WebView2 侧）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoDir = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readRepo = (rel) => readFileSync(join(repoDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const PAGE = 'views/settings/ContextConfig.vue'

/** 取函数体（`function name(...) {` 到首个顶格 `}`） */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

/** 去掉 HTML / CSS / JS 注释后再做「禁用形态」扫描（注释里提到 watch 是允许的） */
function stripComments(s) {
  return s
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

// ═══════════════════════════════════════════════════════════════
// A1/A2/A4 记忆与提示词：弹框编辑 + 保存即关 + 刷新预估 token + 内嵌优化
// ═══════════════════════════════════════════════════════════════
test('A1 通用编辑弹框：TextEditDialog 多行 + 保存回调 + 内嵌优化 + 恢复优化', () => {
  const vue = read('components/common/TextEditDialog.vue')
  assert.match(vue, /defineProps\(\{/, '须为受控组件（props 驱动）')
  assert.match(vue, /onSave: \{ type: Function, required: true \}/, '保存由父组件负责（onSave 必填）')
  assert.match(vue, /await props\.onSave\(text\.value\)/, '保存须调用父回调（落库）')
  assert.match(vue, /v-if="optimize"/, '优化按钮随 optimize 入参显隐（弹框自带）')
  assert.match(vue, /optimizeAgentPrompt\(/, '优化须复用既有提示词优化链路')
  assert.match(vue, /v-if="optimize && optimize\.recover && optimizeSnapshot !== ''"/, '「恢复优化」随快照存在显隐')
  // 优化完成 → 直接落库（与既有总结提示词优化同口径：done 后 handleSave）
  assert.match(vue, /text\.value = prompt \|\| text\.value[\s\S]*?await handleSave\(\)/, '优化完成须落库')
})

test('A1 记忆类别/用户偏好编辑：读内容 → 弹框 → 保存即关 + 刷新清单（预估 token）', () => {
  const src = read(PAGE)
  // P1（2026-09-24）：读/弹框/保存逻辑抽入共享 composable（状态栏「记忆总 token 数」入口同源）
  assert.match(src, /import \{ useMemoryCategories \} from '\.\.\/\.\.\/composables\/useMemoryCategories'/, '须复用共享 composable（OP-04 起不再导入镜像常量）')
  assert.match(src, /openContentEditor,/, '行内「编辑内容」仍走同一弹框实现（来自 composable）')
  assert.match(src, /@click="openContentEditor\(row\)"/, '项目类别行「编辑内容」须走弹框')
  assert.match(src, /@click="openContentEditor\(userPref\)"/, '用户偏好「编辑内容」须走弹框')
  const comp = read('composables/useMemoryCategories.js')
  assert.match(comp, /import TextEditDialog from '\.\.\/components\/common\/TextEditDialog\.vue'/, '须复用通用编辑弹框')
  const openFn = fnBody(comp, 'openContentEditor')
  assert.ok(openFn, '未找到 openContentEditor')
  assert.match(openFn, /dataRequest\('memory', 'read', \{ data: \{ category: c\.category \} \}\)/, '须先读该类别内容')
  assert.match(openFn, /message\.error\([\s\S]*?\n\s*return/, '读失败须可见提示且不打开弹框')
  const showFn = fnBody(comp, 'showContentEditor')
  assert.ok(showFn, '未找到 showContentEditor')
  assert.match(showFn, /dialog\.show\(h\(TextEditDialog, \{/, '须经 DialogManager 弹出通用编辑框')
  assert.match(showFn, /optimize: \{/, '弹框须自带优化（传 optimize 入参）')
  assert.match(showFn, /await dataClient\.save\('memory', \{ category, content: text \}\)/, '保存须落 data-memory-save')
  assert.match(showFn, /message\.success\([\s\S]*?handle\.close\(\)/, '保存后须**自动关闭**弹框')
  assert.match(showFn, /handle\.close\(\)[\s\S]*?await load\(\)/, '关闭后须重读清单（刷新预估 token）')
})

test('A1b 记忆类别提示词编辑：每类别两入口（提示词/内容）+ 提示词弹框（来源提示/恢复默认/优化）', () => {
  const src = read(PAGE)
  // 两个编辑入口并存（项目类别行 + 用户偏好行）——文案清晰区分（不是同一按钮）
  // 2026-09-27：类别表改用自研 Table（具名插槽），行内插槽作用域变量 = row
  assert.match(src, /@click="openPromptEditor\(row\)"/, '项目类别行须有「编辑提示词」入口')
  assert.match(src, /@click="openPromptEditor\(userPref\)"/, '用户偏好行须有「编辑提示词」入口')
  assert.match(src, /@click="openContentEditor\(row\)"/, '项目类别行须有「编辑内容」入口')
  assert.match(src, /@click="openContentEditor\(userPref\)"/, '用户偏好行须有「编辑内容」入口')
  assert.match(src, /projectConfig\.memory_edit_prompt/, '「编辑提示词」须走 i18n（不硬编码中文）')
  assert.match(src, /projectConfig\.memory_edit_content/, '「编辑内容」须走 i18n')
  // 提示词弹框（OP-04 文件化）：回填后端下发值 + 来源提示 + 恢复默认 + 内嵌优化；
  // 保存/恢复走 prompt 域**文件面**（data-prompt-save/delete），前端不再持镜像常量。
  const fn = fnBody(src, 'openPromptEditor')
  assert.ok(fn, '未找到 openPromptEditor')
  assert.match(fn, /dialog\.show\(h\(TextEditDialog, \{/, '提示词须弹框编辑（复用 TextEditDialog）')
  assert.match(fn, /content: c\.prompt \|\| ''/, '须回填后端下发的提示词有效值（不再持前端镜像常量）')
  assert.match(fn, /hint: c\.prompt_override \? t\('projectConfig\.memory_prompt_source_custom'\) : t\('projectConfig\.memory_prompt_source_default'\)/, '来源提示须据后端下发的 prompt_override')
  assert.match(fn, /reset: c\.prompt_override \? \{/, '已自定义须提供「恢复默认」入口（未自定义 → 不显示）')
  assert.match(fn, /label: t\('projectConfig\.memory_prompt_reset'\)/, '「恢复默认」须走 i18n')
  assert.match(fn, /optimize: \{[\s\S]*?memory_prompt_optimize_title/, '弹框须自带优化（复用既有优化链路）')
  assert.match(fn, /bodyClass: 'text-edit-dialog-body'/, '提示词弹框同款 bodyClass（撑满/不留白）')
  // 保存（setPrompt）/ 恢复默认（dataClient.remove）→ 均走 prompt 域文件面 + 重读清单刷新下发值
  assert.match(fn, /await setPrompt\(key, text\)/, '保存须走 prompt 域文件面（data-prompt-save）')
  assert.match(fn, /dataClient\.remove\('prompt', key\)/, '恢复默认须删 prompt 域键（删覆盖文件回落继承）')
  assert.match(fn, /await reloadMemoryCategories\(\)/, '保存/恢复后须重读类别清单（刷新后端下发的有效值）')
  assert.match(src, /const MEMORY_PROMPT_PREFIX = 'memory_prompt\.'/, '提示词文件化键前缀须与 Go 侧 memoryPromptPrefix 一致')
  assert.match(src, /function memoryPromptKey\(category\)/, '须集中构造提示词键（memory_prompt.<类别名>）')
  // 变更广播订阅：prompt 域变更 → 重读类别清单（沿用既有 data-prompt-refresh 面）
  assert.match(src, /onDataRefresh\('prompt', reloadMemoryCategories\)/, '提示词变更须经既有 data-prompt-refresh 广播重载')
  // 规范：无 watch
  assert.doesNotMatch(stripComments(src), /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect')
  // 键载体已删（OP-04）：前端不再持镜像常量 / 不再直读旧键
  assert.doesNotMatch(src, /DEFAULT_MEMORY_PROMPT|memory_prompts|'memory\.prompt\.'/, '旧键载体/镜像常量须已删除')
  const comp = read('composables/useMemoryCategories.js')
  assert.doesNotMatch(stripComments(comp), /DEFAULT_MEMORY_PROMPT/, '前端镜像常量 DEFAULT_MEMORY_PROMPT 须已删除')

  // TextEditDialog：hint / reset 为**可选**扩展（默认不显示，不影响既有调用）
  const te = read('components/common/TextEditDialog.vue')
  assert.match(te, /hint: \{ type: String, default: '' \}/, 'TextEditDialog 须支持可选 hint')
  assert.match(te, /reset: \{ type: Object, default: null \}/, 'TextEditDialog 须支持可选 reset')
  assert.match(te, /v-if="hint" class="text-edit-hint"/, 'hint 须渲染（来源提示）')
  assert.match(te, /v-if="reset"/, 'reset 按钮须按需渲染')
  assert.match(te, /reset\.onClick/, 'reset 须回调父组件（落库）')

  // OP-04 出厂提示词文件：每预置类别一份 capability/system/memory/<类别名>.md（内容 = 原内置默认原文，
  // 行为等价）；Go 侧**不再留** defaultRewriteSystemPrompt 常量副本。
  const segs = [
    '你是记忆库沉淀器。给定某个记忆类别的现有全文与本轮对话的新增信息，',
    '请把两者合并后重写该类别全文（累加 + 更新：修正过时内容、去重、条理化、不臆造）。',
    '只输出重写后的 markdown 全文，不要任何解释或代码块围栏。',
  ]
  for (const cat of ['项目概要', '共同库', '开发规范', '构建发布规则', '接口库', '测试规范', '典型参照', '用户决策', '用户偏好']) {
    const doc = readRepo('initdata/capability/system/memory/' + cat + '.md')
    for (const s of segs) {
      assert.ok(doc.includes(s), `出厂提示词文件 ${cat}.md 缺段（与内置默认不一致）：` + s)
    }
  }
  const go = readRepo('plugins/plugin-memory/memory.go')
  // 仅扫**代码**（`//` 注释内允许留「旧键已删除」的说明文字）
  const goCode = go.replace(/^\s*\/\/.*$/gm, '')
  assert.doesNotMatch(goCode, /defaultRewriteSystemPrompt/, 'Go 侧须已删除内置默认提示词常量（不留代码内副本）')
  assert.doesNotMatch(goCode, /memory\.prompt\.|memory_prompts/, 'Go 侧旧键载体须已删除')
})

test('A1b 提示词弹框 i18n：新增键 zh/en 齐备且可插值', () => {
  const KEYS = ['memory_edit_prompt', 'memory_edit_content', 'memory_prompt_edit',
    'memory_prompt_placeholder', 'memory_prompt_source_default', 'memory_prompt_source_custom',
    'memory_prompt_reset', 'memory_prompt_reset_done',
    'memory_prompt_optimize_title', 'memory_prompt_optimize_use_case']
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'projectConfig.json')
    for (const k of KEYS) {
      assert.ok(typeof j[k] === 'string' && j[k].trim().length > 0, `${loc} 缺 projectConfig.${k}`)
    }
    assert.match(j.memory_prompt_optimize_title, /\{name\}/, `${loc} memory_prompt_optimize_title 须含 {name}`)
    // 两个入口文案须可区分（不得同文）
    assert.notEqual(j.memory_edit_prompt, j.memory_edit_content, `${loc} 两个编辑入口文案须区分`)
  }
})

test('A2 总结提示词：编辑改弹框 + 保存即关 + 内嵌优化（可恢复优化）', () => {
  const src = read(PAGE)
  assert.match(src, /@click="openSummaryEditor"/, '总结提示词须改为「编辑」按钮开弹框')
  const fn = fnBody(src, 'openSummaryEditor')
  assert.ok(fn, '未找到 openSummaryEditor')
  assert.match(fn, /dialog\.show\(h\(TextEditDialog, \{/, '须弹框编辑')
  assert.match(fn, /recover: true/, '总结提示词弹框须提供「恢复优化」')
  assert.match(fn, /await setPrompt\('summary_prompt', text\)/, '保存须写 summary_prompt（既有语义）')
  assert.match(fn, /message\.success\([\s\S]*?handle\.close\(\)/, '保存成功后须关闭弹框')
  // 旧的页内优化/恢复按钮已移除（不重复入口）
  assert.doesNotMatch(src, /contextOptimizePrompt/, '页内优化入口应已移除（改弹框内嵌）')
  // 2026-09-26：只读展示移除（内容仅在弹框内查看）；「编辑」按钮与记忆类别行同款（text）
  assert.doesNotMatch(src, /class="prompt-editor"/, '只读总结提示词 textarea 应已移除')
  assert.match(src, /<Button size="small" text @click="openSummaryEditor">/, '「编辑」须为文本按钮（与类别行一致）')
  assert.match(src, /summary_prompt_source_(override|inherit)/, '来源标注仍保留')
  assert.match(src, /@click="resetSummaryOverride"/, '「恢复默认」入口仍保留')
})

test('A4 记忆总 token 数：主入口在状态栏底部 → 分类列表 → 选中内容弹框（可编辑、可保存）', () => {
  // P1-1（2026-09-24 用户口径）：主入口迁至**窗口状态栏底部**；上下文管理页仅保留只读总量
  const sb = read('views/statusbar/StatusBar.vue')
  assert.match(sb, /class="sb-section sb-mem"/, '状态栏须有记忆总量入口')
  assert.match(sb, /@click="openCategoryList"/, '状态栏总量须可点击 → 分类列表')
  assert.match(sb, /\$t\('statusBar\.memoryTotalLabel'\)/, '须显示标签')
  assert.match(sb, /memoryTotal/, '须显示总量数值')

  const comp = read('composables/useMemoryCategories.js')
  const totalLine = comp.match(/const total = computed\(\(\) =>[^\n]*\n/)
  assert.ok(totalLine && /Number\(c\.tokens\)/.test(totalLine[0]), '总量 = 各启用类别 tokens 之和')
  const listFn = fnBody(comp, 'openCategoryList')
  assert.ok(listFn, '未找到 openCategoryList')
  assert.match(listFn, /MemoryCategoryListDialog/, '须弹分类列表')
  assert.match(listFn, /onPick[\s\S]*?openContentEditor\(c\)/, '选中项 → 打开该类别内容编辑弹框')
  const dlg = read('views/settings/MemoryCategoryListDialog.vue')
  assert.match(dlg, /v-for="c in categories"/, '列表须按类别渲染')
  assert.match(dlg, /@click="onPick\(c\)"/, '点击项回调父组件')
  // 上下文管理页：只读展示，不留重复入口
  const ctx = read(PAGE)
  assert.match(ctx, /class="mem-total"/, '上下文管理页保留只读总量展示')
  assert.doesNotMatch(ctx, /class="mem-total" @click/, '上下文管理页不得再挂主入口（避免重复）')
})

test('A1/A2/A4 i18n：新增键 zh/en 齐备且可插值', () => {
  const KEYS = ['memory_total_tokens', 'memory_total_hint', 'memory_categories_title',
    'memory_optimize_title', 'memory_optimize_use_case', 'recover_before_optimize']
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'projectConfig.json')
    for (const k of KEYS) {
      assert.ok(typeof j[k] === 'string' && j[k].trim().length > 0, `${loc} 缺 projectConfig.${k}`)
    }
    assert.match(j.memory_optimize_title, /\{name\}/, `${loc} memory_optimize_title 须含 {name}`)
  }
})

// ═══════════════════════════════════════════════════════════════
// P1（2026-09-25 口径 W）：保留完整对话两项阈值的取值语义提示（0 = 不启用；<0 = 非法显式提示）
// ═══════════════════════════════════════════════════════════════
test('P1 上下文阈值取值语义：非法值（<0）前端显式提示 + 0 语义提示（zh/en 齐备）', () => {
  const KEYS = ['keep_full_max_turns_hint', 'keep_full_max_turns_invalid',
    'keep_full_max_tokens_hint', 'keep_full_max_tokens_invalid', 'keep_full_max_illegal_warning']
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'projectConfig.json')
    for (const k of KEYS) {
      assert.ok(typeof j[k] === 'string' && j[k].trim().length > 0, `${loc} 缺 projectConfig.${k}`)
    }
  }
  const src = read(PAGE)
  // 两项输入放开 0（原 :min="1"/":min="1000" 会阻止「不启用」）
  assert.match(src, /v-model\.number="keepFullMaxTurns"[\s\S]*?:min="0"/, 'keepFullMaxTurns 须允许 0')
  assert.match(src, /v-model\.number="keepFullMaxTokens"[\s\S]*?:min="0"/, 'keepFullMaxTokens 须允许 0')
  // 非法值 → 显式提示（不静默）：三元切换提示 + 标红 class
  assert.match(src, /keepFullMaxTurnsInvalid \? \$t\('projectConfig\.keep_full_max_turns_invalid'\)/, 'turns 非法值须显式提示')
  assert.match(src, /keepFullMaxTokensInvalid \? \$t\('projectConfig\.keep_full_max_tokens_invalid'\)/, 'tokens 非法值须显式提示')
  assert.match(src, /'hint-error': keepFullMaxTurnsInvalid/, 'turns 非法须标红')
  assert.match(src, /'hint-error': keepFullMaxTokensInvalid/, 'tokens 非法须标红')
  // 判定与保存前警示接线
  assert.match(src, /const keepFullMaxTurnsInvalid = computed\(\(\) => Number\(keepFullMaxTurns\.value\) < 0\)/, 'turns 非法 = <0')
  assert.match(src, /const keepFullMaxTokensInvalid = computed\(\(\) => Number\(keepFullMaxTokens\.value\) < 0\)/, 'tokens 非法 = <0')
  // 2026-09-27（手动保存口径）：数值项不再 blur 落库；非法值警示仅在【保存】路径统一给出
  assert.doesNotMatch(src, /@blur="handleChange"/, '数值项 blur 不再落库（改手动保存，无 saveNumbers）')
  assert.match(fnBody(src, 'handleSave'), /warnIllegalBounds\(\)/, '保存按钮路径须警示非法值')
})

// ═══════════════════════════════════════════════════════════════
// A9 上下文管理「记忆库」联动禁用（2026-09-28 缺陷修复）
//   缺陷（L4 A2 实机抓到）：记忆库关闭后其子项未保持禁用。真因 = 保存路径把本地未提交的开关态
//   冲回「开」：handleSave 写 prj 键（改前逐键 setConfig、改后一次批量写 setConfigs）引发
//   data-prj-config-refresh → loadConfig 读到「尚含旧值」的快照 → 本地「关」被冲回「开」→
//   子项随之解禁、落库值也错成 true。
//   口径：记忆库关闭 → 子项**逐个** disabled（`:disabled="!memoryEnabled"`）；开启 → 恢复可编辑。
// ═══════════════════════════════════════════════════════════════
test('A9 记忆库开关：关闭 → 子项逐个禁用（输入框/开关/按钮），开启 → 恢复可编辑', () => {
  const src = read(PAGE)
  // 两个数值输入（关库即灰化、不可编辑）
  assert.match(src, /v-model\.number="memoryMinTurnTokens"[\s\S]{0,200}?:disabled="!memoryEnabled"/,
    '沉淀最小 Token 须随记忆库开关联动禁用')
  assert.match(src, /v-model\.number="memoryCategoryMaxTokens"[\s\S]{0,200}?:disabled="!memoryEnabled"/,
    '单类别告警阈值须随记忆库开关联动禁用')
  // 与记忆库同级的「用户偏好」开关（关库即灰化）
  assert.match(src, /:model-value="userPrefEnabled" :disabled="!memoryEnabled"/,
    '用户偏好开关须随记忆库开关联动禁用')
  // 立即沉淀（另叠加进行中态，防重复点击）
  assert.match(src, /:disabled="!memoryEnabled \|\| flushing"/, '立即沉淀须随记忆库开关联动禁用')
  // 子项逐个联动：min/catMax（2）+ 新增类别 + 类别行开关 + 4 个行内按钮 + 用户偏好开关 + 3 个行内按钮 = 12
  const gated = (src.match(/:disabled="!memoryEnabled"/g) || []).length
  assert.ok(gated >= 12, `记忆区子项须逐个联动禁用（实得 ${gated} 处 :disabled="!memoryEnabled"）`)
})

test('A9 保存期间不被自身广播重载冲回（记忆库「开 → 关 → 保存」不得回弹）', () => {
  const src = read(PAGE)
  // handleSave 一次批量写（setConfigs）→ 后端整批广播 1 条 data-prj-config-refresh；统一机制须：保存期间跳过 +
  // 合并突发广播为 1 次重载（读最终快照，消除中间快照窗口）
  assert.match(src, /usePrjConfigRefresh\(\{[\s\S]*?reload: loadConfig,[\s\S]*?isSaving: \(\) => saving\.value/,
    'prj-config 广播重载须走统一机制（保存期间跳过，否则本地未提交的开关态被冲回旧值）')
  assert.doesNotMatch(src, /onDataRefresh\('prj-config'/,
    '不得直接订阅 prj-config 广播（须走统一机制，防「关 → 保存」被冲回「开」，子项随之解禁且落库值错成 true）')
  // 保存收尾仍以本地态为新「已保存态」（迟到的广播重载不会把状态判脏）
  const save = fnBody(src, 'handleSave')
  assert.ok(save, '未找到 handleSave')
  assert.ok(save.indexOf('savedState = currentState()') > 0 && save.indexOf('markSaved()') > 0,
    '保存成功后须以本地态重置已保存态')
  // 项目规范：不得引入 watch / watchEffect
  assert.doesNotMatch(stripComments(src), /\bwatch(Effect)?\s*\(/, '禁 watch/watchEffect（用显式调用重算）')
})

// ═══════════════════════════════════════════════════════════════
// A9b 同型竞态推广（2026-09-28）：4 个设置页统一走 usePrjConfigRefresh（I-138 收敛）
//   同型根因：自身「保存」逐键 setConfig → 后端逐键广播 data-prj-config-refresh →
//   loadConfig 读到「尚含旧中间值」的快照，与本页正在提交的本地态打架。
//   统一机制（零消息契约变更）：按键过滤 + 突发合并为 1 次重载（读最终快照）+ 保存期间（saving）跳过。
//   （手动保存页均有 saving 态；「开关即存」路径 saving=false，仍照常重载。）
// ═══════════════════════════════════════════════════════════════
test('A9b 引擎/历史页：prj-config 广播重载须走统一机制（同 ContextConfig I-138 口径）', () => {
  for (const file of [
    'views/settings/ContextConfig.vue',
    'views/settings/CodegraphConfig.vue',
    'views/settings/VftsConfig.vue',
    'views/settings/HistoryConfig.vue',
  ]) {
    const src = read(file)
    assert.match(src, /import \{ usePrjConfigRefresh \} from '\.\.\/\.\.\/composables\/usePrjConfigRefresh'/,
      `${file} 须复用统一机制 composable`)
    // 统一机制：键过滤 + 突发合并 + 保存期间跳过（isSaving 守卫）
    assert.match(src, /usePrjConfigRefresh\(\{[\s\S]*?reload: loadConfig,[\s\S]*?isSaving: \(\) => saving\.value/,
      `${file} prj-config 广播重载须走统一机制并在保存期间跳过`)
    assert.doesNotMatch(src, /onDataRefresh\('prj-config'/,
      `${file} 不得直接订阅 prj-config 广播（须走统一机制）`)
    // 退订仍走既有 onUnmounted/unsubs 机制；不得引入 watch/watchEffect
    assert.match(src, /onUnmounted\(\(\) => \{[\s\S]*?unsubs\.forEach/, `${file} 须在卸载时退订`)
    assert.doesNotMatch(stripComments(src), /\bwatch(Effect)?\s*\(/, `${file} 禁 watch/watchEffect`)
  }
})

test('A5 弹框布局：bodyClass 命中全局去内距 + 组件 flex:1 撑满', () => {
  const css = read('assets/styles/global.css')
  const block = css.match(/\.dialog-body\.scenario-edit-dialog-body,\s*\n\.dialog-body\.text-edit-dialog-body\s*\{([\s\S]*?)\n\}/)
  assert.ok(block, 'global.css 须有 .dialog-body.text-edit-dialog-body 规则')
  assert.match(block[1], /padding:\s*0/, '须去 body 内距（底部不留白）')
  assert.match(block[1], /flex-direction:\s*column/, '须纵向 flex 撑满')
  assert.match(block[1], /overflow:\s*hidden/, '须由内容自滚（不外溢）')
  const editor = read('components/common/TextEditDialog.vue')
  assert.match(editor, /\.text-edit-body \{[\s\S]*?flex: 1;[\s\S]*?\}/, '内容体须 flex:1 撑满')
  assert.match(editor, /\.text-edit-input \{[\s\S]*?flex: 1;[\s\S]*?\}/, '编辑区须 flex:1 撑满')
  // 页面侧确实传了 bodyClass（否则规则不生效）
  assert.match(read(PAGE), /bodyClass: 'text-edit-dialog-body'/, '记忆/提示词弹框须传 bodyClass')
})

// ═══════════════════════════════════════════════════════════════
// A6 非激活 Tab 配色：逐 scheme 定义 + 与激活态有差别
// ═══════════════════════════════════════════════════════════════
test('A6 非激活 Tab 文字：--tab-inactive-fg 转引语义次要色 --fg-secondary（三 scheme 齐备）', () => {
  const vars = read('assets/styles/variables.css')
  const hits = vars.match(/--tab-inactive-fg:/g) || []
  assert.ok(hits.length >= 3, `--tab-inactive-fg 须在 :root/dark/nord 各定义（实得 ${hits.length}）`)
  // 2026-09-27 用户口径：不再 color-mix 逐 scheme 调和，直接绑语义次要色（暖灰偏黄）
  const inactive = vars.match(/--tab-inactive-fg: var\(--fg-secondary\);/g) || []
  assert.equal(inactive.length, 3, `三主题均须绑 var(--fg-secondary)（实得 ${inactive.length}）`)
  const fgSec = vars.match(/--fg-secondary:/g) || []
  assert.equal(fgSec.length, 3, `--fg-secondary 须在 :root/dark/nord 各定义（实得 ${fgSec.length}）`)
  // 消费方：TabBar（主 tab + 更多菜单）+ 通用 Tabs
  assert.match(read('components/tabs/TabBar.vue'), /\.tb-tab \{[\s\S]*?color: var\(--tab-inactive-fg\)/, 'TabBar 主页签须走该 token')
  assert.match(read('components/ui/Tabs.vue'), /color: var\(--tab-inactive-fg\)/, 'Tabs 须走该 token')
})

// ═══════════════════════════════════════════════════════════════
// C1 弹窗 / 工具页 UI 口径（2026-09-27 用户口径）
//   Tooltip 长文案**折行且保留换行**（pre-wrap）；MCP 底栏「启用」**最左**；页签内容与分割线留 0.5em；
//   工具异步页「工具内容介绍」按 **200px 宽度**截断；滚动由**页面**承担（横向条贴底部，非贴表格底）。
// ═══════════════════════════════════════════════════════════════
test('C1 Tooltip 长文案折行 + 保留换行（pre-wrap，不再 nowrap）', () => {
  const css = read('components/ui/Tooltip.vue')
  const block = css.match(/\.b-tooltip__popper \{[\s\S]*?\n\}/)
  assert.ok(block, '未找到 .b-tooltip__popper 规则')
  assert.match(block[0], /white-space: pre-wrap/, '长文案须换行且保留文案内换行（pre-wrap，不能 nowrap）')
  assert.match(block[0], /overflow-wrap: anywhere/, '超长无空白串（路径/URL）须强制断行')
  assert.match(block[0], /max-width: \d+px/, '须有 max-width 约束折行宽度')
  assert.doesNotMatch(block[0], /white-space: nowrap/, '不应再按单行渲染')
})

test('C1 MCP 底栏「启用」最左 + 页签内容与分割线留 0.5em', () => {
  const src = read('views/config/EditMCPDialog.vue')
  assert.match(src, /\.footer-enabled \{[\s\S]*?margin-right: auto;[\s\S]*?\}/, '「启用」须固定在底栏最左侧')
  assert.match(src, /\.mcp-tabs :deep\(\.b-tabs-body\) \{[\s\S]*?padding-top: 0\.5em;[\s\S]*?\}/, '页签内容须与分割线留 0.5em')
  // 底栏其余按钮仍右对齐
  assert.match(src, /\.edit-footer \{[\s\S]*?justify-content: flex-end;/, '「取消/保存」保持右对齐')
})

test('C1 工具异步页：介绍按 200px 截断 + 两轴滚动由页面承担', () => {
  const src = read('views/config/SettingsToolAsyncPage.vue')
  assert.match(src, /\.tool-desc \{[\s\S]*?max-width: 200px;[\s\S]*?\}/, '工具内容介绍须按 200px 宽度截断')
  // 页面承担两轴滚动 → 横向滚动条始终贴 preview 区底部（而非表格底边）
  assert.match(src, /\.page-body \{[\s\S]*?overflow: auto;[\s\S]*?\}/, '.page-body 须承担两轴滚动')
  assert.match(src, /:deep\(\.b-table-wrapper\) \{[\s\S]*?overflow-x: visible;[\s\S]*?\}/, '表格不再自建横向滚动容器')
  assert.match(src, /\.tool-group \{[\s\S]*?min-width: max-content;[\s\S]*?\}/, '卡片须随表格内容变宽')
})

test('C1 滚动范式铺开：4 个设置页正文承担两轴滚动、表格不自建横向滚动容器', () => {
  // 正文滚动容器（该页「正文区」）：头部工具条 / 页签保持固定，滚动归正文（两轴）。
  const bodies = [
    ['views/config/SettingsToolSandboxPage.vue', /\.page-body \{([\s\S]*?)\n\}/],
    ['views/settings/ContextConfig.vue', /\.form-layout \{([\s\S]*?)\n\}/],
    ['views/settings/SecurityConfig.vue', /\.table-wrap \{([\s\S]*?)\n\}/],
    ['views/config/SettingsLLMPage.vue', /\.page-body \{([\s\S]*?)\n\}/],
  ]
  for (const [f, re] of bodies) {
    const src = read(f)
    const m = src.match(re)
    assert.ok(m, `${f} 未找到正文滚动容器规则`)
    assert.match(m[1], /overflow: auto;/, `${f} 正文容器须承担两轴滚动（overflow: auto）`)
    assert.doesNotMatch(m[1], /overflow-y:/, `${f} 正文容器不应残留 overflow-y（两轴归正文）`)
  }
  // 含 Table 的页：表格不再自建横向滚动容器 → 溢出交给正文
  for (const f of ['views/settings/ContextConfig.vue', 'views/settings/SecurityConfig.vue', 'views/config/SettingsLLMPage.vue']) {
    assert.match(read(f), /:deep\(\.b-table-wrapper\) \{[\s\S]*?overflow-x: visible;[\s\S]*?\}/, `${f} 表格不得自建横向滚动容器`)
  }
  // ContextConfig 既有口径保留：长表单正文整体滚一次，压缩内容记录列表仍自身内滚
  assert.match(read('views/settings/ContextConfig.vue'),
    /\.compress-records \{[\s\S]*?overflow-y: auto;[\s\S]*?\}/, '压缩记录列表须自身内滚（既有口径保留）')
  // SecurityConfig 表头吸顶随正文滚动容器保留
  assert.match(read('views/settings/SecurityConfig.vue'),
    /\.table-wrap :deep\(\.b-table-th\) \{[\s\S]*?position: sticky;[\s\S]*?\}/, '表头吸顶须保留')
})

// ═══════════════════════════════════════════════════════════════
// D1 输入框内按键不得被文件树 / 知识树接管（2026-09-27 用户报 bug：
//    在 chat 输入框按 Delete 会触发文件树删除）
// ═══════════════════════════════════════════════════════════════
test('D1 全局 keydown 必须排除 input / textarea / contenteditable', () => {
  for (const f of ['views/filetree/FileTree.vue', 'views/filetree/KnowledgeTree.vue']) {
    const src = read(f)
    const fn = src.match(/function onKeyDown\(e\) \{[\s\S]*?\n\}/)
    assert.ok(fn, `${f} 未找到 onKeyDown`)
    assert.match(fn[0], /isContentEditable/, `${f} 的 onKeyDown 必须排除 contenteditable（输入框内按 Del 不得删节点）`)
    assert.match(fn[0], /'input'/, `${f} 须排除 input`)
    assert.match(fn[0], /'textarea'/, `${f} 须排除 textarea`)
  }
})

// ═══════════════════════════════════════════════════════════════
// A7 继续按钮：文本按钮 + 左侧 + 正常回复后/进行中不显示
// ═══════════════════════════════════════════════════════════════
test('A7 继续按钮：文本按钮 + 左对齐', () => {
  const panel = read('views/chat/ChatPanel.vue')
  const btn = panel.match(/<Button v-if="messageListRef\?\.showContinue"[^>]*>/)
  assert.ok(btn, '未找到「继续」按钮')
  assert.match(btn[0], /\btext\b/, '「继续」须为文本按钮（无底色/描边）')
  assert.doesNotMatch(btn[0], /type="primary"/, '「继续」不应是主色实心按钮')
  const ta = panel.match(/\.turn-actions \{([\s\S]*?)\n\}/)
  assert.ok(ta, '未找到 .turn-actions 样式')
  assert.match(ta[1], /justify-content:\s*flex-start/, '操作区须左对齐')
})

test('A7 显示条件：会话进行中不显示；正常回复完成即收起；仅需继续时显示', () => {
  const ml = read('views/chat/MessageList.vue')
  // 正常回复完成（有输出的 complete）→ 收起
  const complete = ml.match(/if \(emptyReply\) \{[\s\S]*?\n\}/)
  assert.ok(complete, '未找到 onLlmComplete 空回复/正常分支')
  assert.match(complete[0], /else if \(!wasError && !wasInterrupted\) \{[\s\S]*?showContinue\.value = false/, '正常回复后须收起「继续」')
  // 新一轮/续写起点即收起（会话进行中不显示）
  const sendFn = fnBody(ml, 'doSend')
  assert.ok(sendFn, '未找到 doSend')
  assert.match(sendFn, /showContinue\.value = false/, '发送即收起「继续」')
  assert.match(sendFn, /emptyReplyHint\.value = false/, '发送即收起空回复提示')
  const contFn = fnBody(ml, 'sendSameTurnContinue')
  assert.ok(contFn, '未找到 sendSameTurnContinue')
  assert.match(contFn, /showContinue\.value = false/, '续写起点须收起「继续」')
  // 面板侧兜底：loading 时不渲染
  assert.match(read('views/chat/ChatPanel.vue'), /&& !messageListRef\?\.isLoading/, '会话进行中须不显示操作区')
})

// ═══════════════════════════════════════════════════════════════
// A8 会话导航（P3-C1 起为左侧「会话」页签）fork 占位
// ═══════════════════════════════════════════════════════════════
test('A8 fork：占位按钮 → 提示「敬请期待」（不实现真 fork）', () => {
  const src = read('views/sessions/SessionsPane.vue')
  assert.match(src, /@click\.stop="handleFork"/, '须有 fork 按钮（阻止冒泡到选中）')
  assert.match(src, /<Icon name="fork" :size="13" \/>/, 'fork 按钮须用 fork 图标')
  const fn = fnBody(src, 'handleFork')
  assert.ok(fn, '未找到 handleFork')
  assert.match(fn, /message\.info\(t\('chat\.fork_coming_soon'\)\)/, '点击须提示「敬请期待」')
  assert.doesNotMatch(src, /forkSession|createFork|session_fork/, '不得实现真 fork（本轮仅占位）')
  // 图标 + i18n
  assert.match(read('components/icon/icons.js'), /'fork':\s*'<svg/, 'icons.js 缺 fork 图标')
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'chat.json')
    assert.ok(typeof j.fork_coming_soon === 'string' && j.fork_coming_soon.trim().length > 0, `${loc} 缺 chat.fork_coming_soon`)
  }
  assert.equal(readLocale('zh-CN', 'chat.json').fork_coming_soon, '敬请期待', 'zh 文案须为「敬请期待」')
})

// ═══════════════════════════════════════════════════════════════
// B2 状态栏配置入口：**已整体移除**（用户口径 2026-09-24）
// ═══════════════════════════════════════════════════════════════
test('B2 状态栏配置入口已移除（无图标/无菜单/无死代码）', () => {
  const sb = read('views/statusbar/StatusBar.vue')
  assert.doesNotMatch(sb, /sb-menu|configMenuItems|Popover/, '状态栏不得再有配置菜单/弹层')
  assert.doesNotMatch(sb, /openGlobalConfig/, '不得再引用已移除的 i18n 键')
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'statusBar.json')
    assert.equal(j.openGlobalConfig, undefined, `${loc} 应回收 statusBar.openGlobalConfig`)
  }
  // 配置入口仍在工具栏「设置」下拉（共用清单 → utils/configMenu.js 保留）
  const menu = read('utils/configMenu.js')
  for (const kind of ['settings-llm', 'settings-mcp', 'settings-tool-async', 'settings-tool-sandbox',
    'settings-paths', 'settings-params', 'settings-project']) {
    assert.match(menu, new RegExp("kind:\\s*'" + kind + "'"), `配置菜单缺 ${kind}`)
  }
  assert.match(menu, /export function configMenuItems\(t\)/, '须导出 configMenuItems(t)')
  assert.match(read('views/toolbar/Toolbar.vue'), /const settingsItems = computed\(\(\) => configMenuItems\(t\)\)/, '工具栏须复用同一清单')
})

// ═══════════════════════════════════════════════════════════════
// B3 屏蔽 F12 + 调试图标打开 DevTools
// ═══════════════════════════════════════════════════════════════
test('B3 前端：底部调试图标 → gui.devtools.open', () => {
  const sb = read('views/statusbar/StatusBar.vue')
  assert.match(sb, /v-mq:\[EventNames\.guiDevToolsOpen\]\.click/, '调试图标须发 gui.devtools.open')
  assert.match(sb, /\$t\('statusBar\.openDevTools'\)/, '须有 DevTools 提示文案')
  const en = read('events/event-names.js')
  assert.match(en, /guiDevToolsOpen: 'gui\.devtools\.open'/, '事件名缺失')
  for (const loc of LOCALES) {
    const j = readLocale(loc, 'statusBar.json')
    assert.ok(typeof j.openDevTools === 'string' && j.openDevTools.trim().length > 0, `${loc} 缺 statusBar.openDevTools`)
  }
})

test('B3 宿主：屏蔽用户 DevTools 入口 + 桥接 gui.devtools.open', () => {
  // WebView2 选项：DevTools 用户入口关闭（不影响宿主程序化打开）
  const wv = readRepo('lib/go-webview2/webview.go')
  assert.match(wv, /DevToolsDisabled bool/, 'go-webview2 须有 DevToolsDisabled 选项')
  assert.match(wv, /PutAreDevToolsEnabled\(options\.Debug && !options\.DevToolsDisabled\)/, '须按选项关闭用户 DevTools')
  // 宿主传参 + 注入打开能力（2026-09-25 订正：多窗口改造把窗口创建/WebView2 选项从
  // main.go 迁至 window.go —— 逐窗口一份 → 断言对两处取并集，避免绑定文件位置）
  const host = readRepo('lib/gui/main.go') + readRepo('lib/gui/window.go')
  assert.match(host, /DevToolsDisabled: true/, '宿主须屏蔽用户 DevTools 入口')
  assert.match(host, /br\.SetDevToolsOpener\(/, '宿主须注入程序化打开能力')
  assert.match(host, /w\.Dispatch\(func\(\) \{ dt\.OpenDevToolsWindow\(\) \}\)/, '打开须派发到 WebView2 UI 线程')
  // 桥：能力注入 + 分派
  assert.match(readRepo('lib/gui/bridge/bridge.go'), /func \(b \*Bridge\) SetDevToolsOpener\(fn func\(\)\)/, '桥缺 SetDevToolsOpener')
  const gui = readRepo('lib/gui/bridge/guimsg.go')
  assert.match(gui, /case "devtools\.open":/, 'guiDo 缺 devtools.open 分支')
  assert.match(gui, /b\.openDevTools\(\)/, '须调用宿主注入的打开能力')
})
