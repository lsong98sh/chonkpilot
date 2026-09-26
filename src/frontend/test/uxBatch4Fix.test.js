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
 *   B3 屏蔽 F12/DevTools 快捷键（宿主层）+ 底部调试图标经 gui.devtools.open 打开 DevTools。
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
  assert.match(src, /import \{ useMemoryCategories \} from '\.\.\/\.\.\/composables\/useMemoryCategories'/, '须复用共享 composable')
  assert.match(src, /openContentEditor,/, '行内「编辑」仍走同一弹框实现（来自 composable）')
  assert.match(src, /@click="openContentEditor\(c\)"/, '项目类别行「编辑」须走弹框')
  assert.match(src, /@click="openContentEditor\(userPref\)"/, '用户偏好「编辑」须走弹框')
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
  assert.match(fnBody(src, 'saveNumbers'), /warnIllegalBounds\(\)/, 'blur 保存路径须警示非法值')
  assert.match(fnBody(src, 'handleSave'), /warnIllegalBounds\(\)/, '保存按钮路径须警示非法值')
})

// ═══════════════════════════════════════════════════════════════
// A5 编辑类弹框：内容撑满、底部不留白
// ═══════════════════════════════════════════════════════════════
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
test('A6 非激活 Tab 文字：--tab-inactive-fg 三 scheme 齐备且与激活色不同源', () => {
  const vars = read('assets/styles/variables.css')
  const hits = vars.match(/--tab-inactive-fg:/g) || []
  assert.ok(hits.length >= 3, `--tab-inactive-fg 须在 :root/dark/nord 各定义（实得 ${hits.length}）`)
  assert.match(vars, /:root \{[\s\S]*?--tab-inactive-fg: color-mix\(in srgb, var\(--text-primary\)/, ':root（light）须调淡')
  assert.match(vars, /\[data-theme="dark"\][\s\S]*?--tab-inactive-fg: color-mix\(in srgb, var\(--text-primary\)/, 'dark 须覆写')
  assert.match(vars, /\[data-theme="nord"\][\s\S]*?--tab-inactive-fg: color-mix\(in srgb, var\(--text-primary\) 78%, var\(--bg-primary\)\)/, 'nord 须覆写（改向 primary 保证更暗）')
  // 消费方：TabBar（主 tab + 更多菜单）+ 通用 Tabs
  assert.match(read('components/tabs/TabBar.vue'), /\.tb-tab \{[\s\S]*?color: var\(--tab-inactive-fg\)/, 'TabBar 主页签须走该 token')
  assert.match(read('components/ui/Tabs.vue'), /color: var\(--tab-inactive-fg\)/, 'Tabs 须走该 token')
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
