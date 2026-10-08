/**
 * OP-12（提示词变量插入统一组件）+ OP-03（压缩进度指示）前端接线守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 纯函数行为 + 源码/文案守卫。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { insertAtCursor } from '../src/utils/promptInsert.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const LOCALES = ['zh-CN', 'en-US']

// ── OP-12 ────────────────────────────────────────────────────────────────

test('insertAtCursor：光标处插入 / 选区替换 / 无 DOM 追加', () => {
  // 光标处插入（selection 起点 = 终点）
  const mid = insertAtCursor({ selectionStart: 3, selectionEnd: 3 }, 'abcdef', '{{x}}')
  assert.equal(mid.value, 'abc{{x}}def')
  assert.equal(mid.caret, 8)
  // 有选区 → 替换（caret = 起点 + 插入串长度）
  const rep = insertAtCursor({ selectionStart: 1, selectionEnd: 3 }, 'abcdef', '{{x}}')
  assert.equal(rep.value, 'a{{x}}def')
  assert.equal(rep.caret, 1 + '{{x}}'.length)
  // 无 DOM（null）→ 追加到末尾
  const app = insertAtCursor(null, 'abc', '{{y}}')
  assert.equal(app.value, 'abc{{y}}')
  assert.equal(app.caret, 3 + '{{y}}'.length)
})

test('api/config.js：getPromptVariables 走 gui.prompt-vars（单源 = 后端常量）', () => {
  const c = read('api/config.js')
  assert.match(c, /export async function getPromptVariables\(\)/, '须导出 getPromptVariables')
  assert.match(c, /guiReq\('prompt-vars', \{\}\)/, '须经 gui.prompt-vars 本地只读面')
  assert.match(c, /GuiPromptVarsKeys\.groups/, '结果须取 groups（键常量来自 schema）')
})

test('usePromptVariables：清单缓存 + dslOnly 过滤 + useVariableInsert 光标恢复', () => {
  const s = read('composables/usePromptVariables.js')
  assert.match(s, /let loadPromise = null/, '清单须缓存（模块级 Promise）')
  assert.match(s, /filter\(\(it\) => dsl \|\| !it\.dslOnly\)/, '非 DSL 编辑器须过滤 dslOnly 项')
  assert.match(s, /export function useVariableInsert\(/, '须导出 useVariableInsert')
  assert.match(s, /requestAnimationFrame\(/, '插入后须下一帧恢复焦点/光标')
})

test('PromptVariablesButton：popup 分组 + tooltip 说明 + emit insert', () => {
  const v = read('components/common/PromptVariablesButton.vue')
  assert.match(v, /<Popover/, '须用 popup 承载分组清单')
  assert.match(v, /<Tooltip[^>]*:content="it\.desc"/, '变量项须带说明 tooltip（desc）')
  assert.match(v, /\$emit\('insert', it\.key\)/, '点击须 emit insert(key)')
  assert.match(v, /props\.dsl/, '须按 dsl 开关过滤 {{env.*}}')
  // 复用 i18n 残留键（OP-12：纳入本组件使用，不再是无实现残留）
  assert.match(v, /\$t\('scenario\.insert_variable'\)/, '按钮文案须用 scenario.insert_variable')
  assert.match(v, /\$t\('scenario\.available_vars'\)/, 'popup 标题须用 scenario.available_vars')
})

test('挂载点：TextEditDialog / AgentEditor / PrimitivePanel / ScenarioWizardDialog / ContextConfig', () => {
  // TextEditDialog：variables 开关 + 变量按钮 + 光标插入
  const t = read('components/common/TextEditDialog.vue')
  assert.match(t, /variables: \{ type: Boolean, default: false \}/, 'TextEditDialog 须有 variables 开关')
  assert.match(t, /<PromptVariablesButton[\s\S]*?@insert="insert"/, 'TextEditDialog 须挂变量按钮')
  assert.match(t, /const \{ insert \} = useVariableInsert\(/, 'TextEditDialog 须接 useVariableInsert')

  // AgentEditor：prompt 页签工具条
  const a = read('views/scenario/AgentEditor.vue')
  assert.match(a, /<PromptVariablesButton :disabled="optimizing" @insert="insertVariable" \/>/, 'AgentEditor prompt 页签须挂变量按钮')
  assert.match(a, /const \{ insert: insertVariable \} = useVariableInsert\(/, 'AgentEditor 须接 useVariableInsert')

  // PrimitivePanel：description + content 两处
  const p = read('views/knowledge/PrimitivePanel.vue')
  const buttons = p.match(/<PromptVariablesButton @insert="(insertDescription|insertContent)" \/>/g) || []
  assert.equal(buttons.length, 2, 'PrimitivePanel 须在 description 与 content 各挂一个变量按钮')
  assert.match(p, /const \{ insert: insertDescription \} = useVariableInsert\(/, 'PrimitivePanel 须接 description 插入')
  assert.match(p, /const \{ insert: insertContent \} = useVariableInsert\(/, 'PrimitivePanel 须接 content 插入')

  // ScenarioWizardDialog：agent 提示词 + 记忆提示词
  const w = read('views/scenario/ScenarioWizardDialog.vue')
  assert.match(w, /@insert="insertAgentVar"/, 'ScenarioWizardDialog 须在 agent 提示词挂变量按钮')
  assert.match(w, /@insert="insertMemoryVar"/, 'ScenarioWizardDialog 须在记忆提示词挂变量按钮')

  // ContextConfig：总结提示词 + 记忆提示词（各 TextEditDialog 传 variables:true）
  const cc = read('views/settings/ContextConfig.vue')
  const flags = cc.match(/variables: true/g) || []
  assert.equal(flags.length, 2, 'ContextConfig 须在总结提示词与记忆提示词两处开启变量按钮')
})

test('i18n：scenario.insert_variable / available_vars / chat.compressing 中英齐备', () => {
  for (const loc of LOCALES) {
    const sc = JSON.parse(read(`locales/${loc}/scenario.json`))
    assert.ok(typeof sc.insert_variable === 'string' && sc.insert_variable.trim(), `${loc} 缺 scenario.insert_variable`)
    assert.ok(typeof sc.available_vars === 'string' && sc.available_vars.trim(), `${loc} 缺 scenario.available_vars`)
    const chat = JSON.parse(read(`locales/${loc}/chat.json`))
    assert.ok(typeof chat.compressing === 'string' && chat.compressing.trim(), `${loc} 缺 chat.compressing`)
  }
})

// ── OP-03 ────────────────────────────────────────────────────────────────

test('useCompressStatus：compress-start/done 驱动会话级进行中集合', () => {
  const s = read('composables/useCompressStatus.js')
  assert.match(s, /data\.notice === 'compress-start'/, '须识别 compress-start')
  assert.match(s, /data\.notice === 'compress-done'/, '须识别 compress-done')
  assert.match(s, /session_id/, '须按 session_id 归属会话（会话级）')
})

test('ChatPanel：compress-* 分流为进度指示（不弹 toast）并渲染指示条', () => {
  const c = read('views/chat/ChatPanel.vue')
  assert.match(c, /import \{ useCompressStatus \} from '\.\.\/\.\.\/composables\/useCompressStatus'/)
  assert.match(c, /if \(data\.notice === 'compress-start' \|\| data\.notice === 'compress-done'\)/, '须在 tool-notify 分派 compress-*')
  assert.match(c, /handleCompressNotice\(data\)[\s\S]*?return/, 'compress-* 处理须提前 return（不走 toast）')
  assert.match(c, /<div v-if="compressing" class="compress-progress-bar">\{\{ \$t\('chat\.compressing'\) \}\}<\/div>/, '须渲染压缩指示条')
})

test('项目规则：本次改动的 Vue 文件不得用 watch / watchEffect', () => {
  for (const rel of [
    'components/common/PromptVariablesButton.vue',
    'components/common/TextEditDialog.vue',
    'views/scenario/AgentEditor.vue',
    'views/knowledge/PrimitivePanel.vue',
    'views/scenario/ScenarioWizardDialog.vue',
    'views/settings/ContextConfig.vue',
    'views/chat/ChatPanel.vue',
  ]) {
    assert.doesNotMatch(read(rel), /\bwatchEffect\(|\bwatch\(/, `${rel} 不得用 watch/watchEffect`)
  }
})
