/**
 * 原语「基本信息」（meta）**按类型专用表单**：字段定义 + 值转换 + 落点守卫（2026-10-15）。
 *
 * 用户口径：meta 不能是通用 KV 网格——「有些值不能乱填」，须按类型给出专用编辑表单
 *（枚举 → 下拉、布尔 → 开关、数字 → number、文本 → 输入框；name 由文件名决定，只读）。
 * 契约零变更：字段 = 契约 meta 键（docs/spec/70-conventions/72-工具开发规范.md §2），
 * 未覆盖的键（用户自建）走「自定义键」KV 区原样回写 → load → form → save 往返保真。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  META_FIELDS, metaFieldsOf, isSchemaKey, fieldValueOf, fieldToMetaOf,
} from '../src/utils/metaForm.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

const scalar = (tok) => metaFieldsOf(tok).map((f) => f.key)

// ── 字段清单对齐契约 ───────────────────────────────────────────────

test('metaForm：tool 字段 = 契约 meta 键（72-工具开发规范 §2）', () => {
  // 契约 tool 专属键（除 name/title）：runtime/category/async/async-threshold/timeout/idempotent/hot/args/output
  assert.deepEqual(scalar('tool').sort(), [
    'args', 'async', 'async-threshold', 'category', 'hot', 'idempotent', 'output', 'runtime', 'timeout',
  ])
  // 绝不包含 name/title（name 由表单只读行承载，title 在顶部标题区）
  assert.ok(!scalar('tool').includes('name') && !scalar('tool').includes('title'), 'name/title 不进字段表单')
  assert.equal(metaFieldsOf('tool').length, scalar('tool').length, '字段 key 不重复')
})

test('metaForm：skill/prompt 无专属键，resource = path，agent 由 AgentEditor 承载', () => {
  assert.deepEqual(scalar('skill'), [])
  assert.deepEqual(scalar('prompt'), [])
  assert.deepEqual(scalar('resource'), ['path'])
  assert.equal(metaFieldsOf('resource')[0].type, 'text', 'resource.path = 文本（外部内容文件）')
  // agent 的 roletag/ismain/llm 由 AgentEditor 表单承载（本面板不渲染 meta 表单，避免重复实现）
  assert.deepEqual(scalar('agent'), [])
  assert.deepEqual(metaFieldsOf('unknown-token'), [], '未知 token → 空（不抛错）')
})

test('metaForm：控件类型与枚举取值受约束（不能乱填）', () => {
  const byKey = Object.fromEntries(metaFieldsOf('tool').map((f) => [f.key, f]))

  assert.equal(byKey.runtime.type, 'text')
  assert.equal(byKey.args.type, 'text')
  assert.equal(byKey.category.type, 'enum')
  assert.deepEqual(byKey.category.options.map((o) => o.value), ['core', 'desktop', 'browser', 'server'])
  assert.equal(byKey.async.type, 'enum')
  assert.deepEqual(byKey.async.options.map((o) => o.value), ['auto', 'always', 'never', 'manual'])
  assert.equal(byKey.output.type, 'enum')
  assert.deepEqual(byKey.output.options.map((o) => o.value), ['stdout', 'code', 'file'])
  assert.equal(byKey.timeout.type, 'number')
  assert.equal(byKey['async-threshold'].type, 'number')
  assert.equal(byKey.idempotent.type, 'bool')
  assert.equal(byKey.hot.type, 'bool')

  // 每个字段都有 i18n label；enum 每个取值都有 i18n label
  for (const f of metaFieldsOf('tool')) {
    assert.ok(f.labelKey, f.key + ' 缺 labelKey')
    assert.ok(['text', 'number', 'enum', 'bool'].includes(f.type), f.key + ' 类型未知：' + f.type)
    for (const o of f.options || []) assert.ok(o.labelKey, f.key + ' 的取值 ' + o.value + ' 缺 labelKey')
  }
})

// ── 值转换（控件值 ↔ meta 字符串值）────────────────────────────────

test('metaForm：值转换往返语义（bool / 枚举 / 数字 / 文本）', () => {
  const byKey = Object.fromEntries(metaFieldsOf('tool').map((f) => [f.key, f]))

  // bool：显式 true/false → 控件 Boolean；控件 false → 清键（契约「缺省/未声明即 false」）
  assert.equal(fieldValueOf(byKey.idempotent, { idempotent: 'true' }), true)
  assert.equal(fieldValueOf(byKey.idempotent, { idempotent: 'false' }), false)
  assert.equal(fieldValueOf(byKey.idempotent, {}), false, '缺键 = false（安全默认）')
  assert.equal(fieldToMetaOf(byKey.idempotent, true), 'true')
  assert.equal(fieldToMetaOf(byKey.idempotent, false), '')

  // 枚举：显式取值原样保留（'auto' 不得被当作「未设置」清掉）
  assert.equal(fieldValueOf(byKey.async, { async: 'auto' }), 'auto')
  assert.equal(fieldToMetaOf(byKey.async, 'auto'), 'auto')
  assert.equal(fieldToMetaOf(byKey.async, ''), '', '空 = 未设置 → 清键')
  assert.equal(fieldValueOf(byKey.output, {}), '', '缺键 → 空（Select 显示「未设置」）')

  // 数字：'0' / '-1' 是合法值（显式 0/-1 = 无上限），不得当空值丢掉
  assert.equal(fieldToMetaOf(byKey.timeout, '0'), '0')
  assert.equal(fieldToMetaOf(byKey.timeout, '-1'), '-1')
  assert.equal(fieldToMetaOf(byKey.timeout, '300'), '300')
  assert.equal(fieldToMetaOf(byKey.timeout, ''), '')
  assert.equal(fieldValueOf(byKey.timeout, { timeout: '0' }), '0')

  // 文本：路径 / 模板原样
  assert.equal(fieldValueOf(byKey.runtime, { runtime: '../../executors/chonkpilot-core-executor.exe' }),
    '../../executors/chonkpilot-core-executor.exe')
  assert.equal(fieldToMetaOf(byKey.args, 'script_run --input={RAW-INPUT-FILE}'), 'script_run --input={RAW-INPUT-FILE}')
  assert.equal(fieldValueOf(byKey.runtime, {}), '', '缺键 → 空输入框')
  // resource.path（外部内容文件）
  assert.equal(fieldToMetaOf(metaFieldsOf('resource')[0], 'config.json'), 'config.json')
})

test('metaForm：isSchemaKey = 表单已覆盖的键（含 name）→ 自定义键区过滤依据', () => {
  assert.equal(isSchemaKey('tool', 'name'), true, 'name 由表单只读行承载')
  assert.equal(isSchemaKey('tool', 'runtime'), true)
  assert.equal(isSchemaKey('tool', 'async-threshold'), true)
  assert.equal(isSchemaKey('tool', '  timeout  '), true, '键比较去空白')
  assert.equal(isSchemaKey('tool', 'my-key'), false, '自建键 → 进自定义键区（原样保留）')
  assert.equal(isSchemaKey('skill', 'anything'), false, 'skill 无专属键 → 全走自定义键区')
  assert.equal(isSchemaKey('resource', 'path'), true)
  assert.equal(isSchemaKey('tool', ''), false, '空键不算（新行可编辑）')
})

// ── 落点守卫（PrimitivePanel.vue）─────────────────────────────────

test('PrimitivePanel：基本信息 tab = 按类型表单（Select/Switch/number Input），name 只读', () => {
  const panel = read('views/knowledge/PrimitivePanel.vue')
  assert.match(panel, /import \{ Input, Textarea, Button, Tag, Select, Switch, message, confirm \} from '\.\.\/\.\.\/components\/ui'/,
    '表单控件来自共通 ui 组件库')
  assert.match(panel, /import \{ metaFieldsOf, isSchemaKey, fieldValueOf, fieldToMetaOf \} from '\.\.\/\.\.\/utils\/metaForm'/,
    '字段定义走 utils/metaForm 纯模块')

  // 字段区：按 token 字段清单渲染（不再是无类型 KV 网格）
  assert.match(panel, /const metaFields = computed\(\(\) => metaFieldsOf\(token\.value\)\)/, '字段随类型计算')
  assert.match(panel, /<div v-for="f in metaFields" :key="f\.key" class="pf-field">/, '字段逐项渲染')
  assert.match(panel, /v-if="f\.type === 'bool'"[\s\S]*?Switch/, '布尔 → Switch')
  assert.match(panel, /v-else-if="f\.type === 'enum'"[\s\S]*?Select/, '枚举 → Select')
  assert.match(panel, /:type="f\.type === 'number' \? 'number' : 'text'"/, '数字 → number 输入框')
  assert.match(panel, /placeholder-selectable/, '枚举空值可选中（＝未设置）')
  assert.match(panel, /knowledgeList\.field_unset/, '枚举空值文案')
  assert.equal((panel.match(/@update:model-value="\(v\) => setMetaField\(f, v\)"/g) || []).length, 3,
    '三类控件写回统一走 setMetaField（读 metaMap / 写 setMeta）')
  assert.match(panel, /function setMetaField\(f, v\) \{\s*\n\s*setMeta\(f\.key, fieldToMetaOf\(f, v\)\)/, 'setMetaField = 字段值 → meta 字符串')
  assert.match(panel, /function metaFieldValue\(f\) \{\s*\n\s*return fieldValueOf\(f, metaMap\(\)\)/, '字段值读 metaMap（metaRows 仍是唯一真源）')
  assert.match(panel, /function metaFieldOptions\(f\)/, '枚举选项 label 走 i18n')
  assert.match(panel, /labelKey/, '字段文案走 i18n 键')

  // name：只读、不可编辑（= 文件名）
  assert.match(panel, /<Input :model-value="form\.name" size="small" disabled \/>/, 'name 只读')
  assert.match(panel, /knowledgeList\.field_name_hint/, 'name 锁定说明')

  // 自定义键区：仅渲染表单未覆盖的键（metaRows 行引用 → v-model 原样写回）
  assert.match(panel, /const customRows = computed\(\(\) => form\.metaRows\.filter\(\(r\) => !isSchemaKey\(token\.value, r\.k\)\)\)/,
    '自定义键 = metaRows 中非 schema 键')
  assert.match(panel, /v-for="\(row, i\) in customRows"[\s\S]*?v-model="row\.k"[\s\S]*?v-model="row\.v"/, '自定义键 KV 可增删改')
  assert.match(panel, /function removeCustomRow\(row\)[\s\S]*?form\.metaRows\.indexOf\(row\)/, '删行按行引用定位（不重排真源）')
  assert.match(panel, /knowledgeList\.custom_keys_hint/, '自定义键区说明')
  assert.match(panel, /knowledgeList\.meta_no_fields/, '无专属键类型给提示（skill/prompt）')
  assert.match(panel, /knowledgeList\.meta_form_hint/, '表单口径说明')

  // 锁定键语义订正：值可清空、键不可删（bool 关掉 = 清键，不再残留旧值）
  assert.match(panel, /else if \(idx >= 0\) form\.metaRows\[idx\]\.v = ''/, '锁定键值可清空')

  assert.doesNotMatch(panel, /watch\(|watchEffect\(/, '组件规则：禁止 watch 监听 props/store')
})

test('PrimitivePanel：基本信息表单 i18n 文案齐备（zh-CN / en-US 全量对齐）', () => {
  const need = new Set(['meta_form_hint', 'meta_no_fields', 'custom_keys_hint', 'field_name', 'field_name_hint', 'field_unset'])
  for (const tok of Object.keys(META_FIELDS)) {
    for (const f of metaFieldsOf(tok)) {
      need.add(f.labelKey)
      if (f.hintKey) need.add(f.hintKey)
      for (const o of f.options || []) need.add(o.labelKey)
    }
  }
  for (const loc of ['zh-CN', 'en-US']) {
    const kl = JSON.parse(read('locales/' + loc + '/knowledgeList.json'))
    const missing = [...need].filter((k) => !kl[k])
    assert.deepEqual(missing, [], loc + ' 缺基本信息表单文案：' + missing.join(', '))
  }
})
