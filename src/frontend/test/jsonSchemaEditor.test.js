/**
 * JSON Schema 编辑器（共通控件，P3-C2，2026-09-24）纯逻辑 + 落点守卫。
 *
 * 能力范围（用户口径：参数编辑器**就是** JSON Schema 编辑器）：
 *   解析（JSON → mcp 契约模板形态的最小 YAML 子集）/ 结构校验 / 树形层级编辑 / 类型·关键字补全 /
 *   序列化为 JSON 文本（回写 [parameters]|[arguments] 分区）。
 * 真机（知识库编辑页四页签 + 参数页签树渲染）由 L4 `run_explore_kb.py::C4/C5` 覆盖。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  SCHEMA_TYPES, keywordSuggestions, inferType, parseSchemaText, serializeSchema, validateSchema,
  schemaGet, schemaRows, addProperty, renameProperty, setNodeType, toggleRequired,
  addEnumValue, removeEnumValue, addKeyword, removeKeyword, removeNode, defaultKeywordValue,
} from '../src/utils/jsonSchema.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

// ── 解析 ──────────────────────────────────────────────────────────

test('JsonSchema：解析 JSON 与 mcp 契约模板形态（YAML 子集）', () => {
  const j = parseSchemaText('{"type":"object","properties":{"a":{"type":"string"}}}')
  assert.equal(j.error, '')
  assert.equal(j.schema.properties.a.type, 'string')

  // 模板形态（capfs.Template 的 tool 参数）
  const yamlText = 'properties:\n    arg1:\n        type: string\n        description: 参数描述\nrequired:\n    - arg1'
  const y = parseSchemaText(yamlText)
  assert.equal(y.error, '')
  assert.equal(y.schema.properties.arg1.type, 'string')
  assert.equal(y.schema.properties.arg1.description, '参数描述')
  assert.deepEqual(y.schema.required, ['arg1'])

  // 空文本 = 无参数（不报错）
  assert.deepEqual(parseSchemaText('   '), { schema: null, error: '' })
  // 无法解析 → 可见错误
  assert.ok(parseSchemaText('{{ not: valid').error.length > 0)
})

test('JsonSchema：结构校验（类型/properties/required/items/enum）', () => {
  assert.deepEqual(validateSchema({ type: 'object', properties: { a: { type: 'string' } }, required: ['a'] }), [])
  assert.ok(validateSchema({ type: 'str' }).length === 1, 'type 取值须在候选内')
  assert.ok(validateSchema({ properties: [] }).length === 1, 'properties 必须是对象')
  assert.ok(validateSchema({ type: 'object', required: [1] }).length === 1, 'required 必须是字符串数组')
  assert.ok(validateSchema({ type: 'array', items: [] }).length === 1, 'items 必须是对象')
  assert.ok(validateSchema({ type: 'string', enum: 'x' }).length === 1, 'enum 必须是数组')
  assert.ok(validateSchema({ type: 'string', minLength: 'x' }).length === 1, '数值关键字必须是数字')
  assert.ok(validateSchema({ type: 'object', properties: { a: { type: 'bad' } } }).length === 1, '递归校验子 schema')
})

// ── 树 / 路径 / 操作 ──────────────────────────────────────────────

const SCHEMA = {
  type: 'object',
  properties: {
    name: { type: 'string', description: '名称' },
    tags: { type: 'array', items: { type: 'object', properties: { id: { type: 'integer' } } } },
  },
  required: ['name'],
}

test('JsonSchema：树形行模型（深度 + 路径 + 必填）', () => {
  const rows = schemaRows(SCHEMA)
  const byPath = Object.fromEntries(rows.map(r => [r.path, r]))
  assert.equal(rows.length, 5, '根 + name + tags + tags/items + tags/items 下的 id')
  assert.equal(rows[0].name, '$')
  assert.equal(rows[0].depth, 0)
  assert.equal(byPath['$/properties/name'].required, true, 'name 必填')
  assert.equal(byPath['$/properties/tags'].depth, 1)
  assert.equal(byPath['$/properties/tags/items'].name, 'items')
  assert.equal(byPath['$/properties/tags/items'].depth, 2)
  assert.equal(byPath['$/properties/tags/items/properties/id'].depth, 3, '深层递归')
  assert.equal(byPath['$/properties/tags/items/properties/id'].type, 'integer')
  assert.equal(schemaGet(SCHEMA, '$/properties/tags/items').type, 'object')
})

test('JsonSchema：增删改属性（同步 required）', () => {
  let s = addProperty(SCHEMA, '$', 'age', 'integer')
  assert.equal(s.properties.age.type, 'integer')
  assert.ok(schemaRows(s).some(r => r.path === '$/properties/age'))
  assert.deepEqual(s.required, ['name'], '新增属性不自动必填')

  s = toggleRequired(s, '$', 'age', true)
  assert.deepEqual(s.required, ['name', 'age'])
  s = toggleRequired(s, '$', 'age', false)
  assert.deepEqual(s.required, ['name'])

  s = renameProperty(s, '$', 'age', 'years')
  assert.equal(s.properties.age, undefined)
  assert.equal(s.properties.years.type, 'integer')
  s = toggleRequired(s, '$', 'years', true)
  s = renameProperty(s, '$', 'years', 'old')
  assert.deepEqual(s.required, ['name', 'old'], '改名同步 required')

  s = removeNode(s, '$/properties/old')
  assert.equal(s.properties.old, undefined)
  assert.deepEqual(s.required, ['name'], '删除同步 required')
})

test('JsonSchema：改类型清理不适用关键字；枚举增删；关键字补全/移除', () => {
  let s = setNodeType(SCHEMA, '$/properties/name', 'array')
  assert.equal(s.properties.name.type, 'array')
  assert.equal(s.properties.name.description, '名称', '通用关键字保留')
  s = setNodeType({ type: 'object', properties: {}, required: ['x'] }, '$', 'string')
  assert.equal(s.properties, undefined)
  assert.equal(s.required, undefined)

  s = addEnumValue(SCHEMA, '$/properties/name', 'a')
  s = addEnumValue(s, '$/properties/name', 'b')
  assert.deepEqual(s.properties.name.enum, ['a', 'b'])
  s = removeEnumValue(s, '$/properties/name', 0)
  assert.deepEqual(s.properties.name.enum, ['b'])

  assert.ok(keywordSuggestions('string').includes('pattern'))
  assert.ok(keywordSuggestions('object').includes('properties'))
  assert.equal(keywordSuggestions('string').includes('items'), false, '按类型给候选')
  assert.deepEqual(keywordSuggestions('nope'), ['title', 'description', 'default'], '未知类型仅通用候选')
  assert.equal(SCHEMA_TYPES.includes('object'), true)
  assert.deepEqual(defaultKeywordValue('enum'), [])
  assert.deepEqual(defaultKeywordValue('items'), { type: 'string' })

  s = addKeyword(SCHEMA, '$/properties/tags', 'minItems', 'array')
  assert.equal(s.properties.tags.minItems, 0)
  s = addKeyword(s, '$/properties/tags', 'minItems', 'array')
  assert.equal(s.properties.tags.minItems, 0, '重复补全幂等')
  s = removeKeyword(s, '$/properties/tags', 'minItems')
  assert.equal(s.properties.tags.minItems, undefined)
  assert.equal(inferType({ properties: {} }), 'object')
  assert.equal(inferType({ items: { type: 'string' } }), 'array')
})

test('JsonSchema：序列化为 JSON 文本（可回写 [parameters]）', () => {
  const text = serializeSchema(SCHEMA)
  const back = JSON.parse(text)
  assert.deepEqual(back, SCHEMA)
  assert.equal(serializeSchema(null), '{}')
  assert.ok(text.includes('\n  '), '缩进 2 的可读 JSON')
})

// ── 落点守卫（知识库编辑页四页签 + 共通控件） ────────────────────

test('JsonSchema：知识库编辑页 = 顶部标题 / 四页签 Tabs / 底部 保存·恢复', () => {
  const panel = read('views/knowledge/PrimitivePanel.vue')
  assert.match(panel, /prim-head/, '顶部标题区')
  assert.match(panel, /from '\.\.\/\.\.\/components\/ui\/Tabs\.vue'/, '中间用 Tabs 撑满')
  for (const [name, key] of [['meta', 'tab_meta'], ['description', 'tab_description'], ['parameters', 'tab_parameters'], ['content', 'tab_content']]) {
    assert.match(panel, new RegExp("name: '" + name + "'"), '页签 ' + name)
    assert.match(panel, new RegExp('knowledgeList\\.' + key))
  }
  assert.match(panel, /<template #parameters>[\s\S]*JsonSchemaEditor/, '参数页签 = JSON Schema 编辑器')
  assert.match(panel, /prim-footer[\s\S]*common\.save[\s\S]*common\.restore/, '底部 保存·恢复')
  assert.doesNotMatch(panel, /source_tab|edit_tab/, '源码/编辑子页签已移除')
})

test('JsonSchema：编辑器为共通控件（独立于知识库，逻辑纯函数、无 watch）', () => {
  const comp = read('components/editor/JsonSchemaEditor.vue')
  assert.match(comp, /from '\.\.\/\.\.\/utils\/jsonSchema'/, '逻辑走共享纯函数模块')
  assert.doesNotMatch(comp, /watch\(|watchEffect\(/, '组件规则：禁止 watch/watcheffect 监听 props')
  assert.match(comp, /jse-row/, '树形层级行')
  assert.match(comp, /complete_keyword/, '关键字补全入口')
  assert.match(comp, /SCHEMA_TYPES/, '类型候选 = 补全来源')
  // i18n 命名空间独立（后续其它配置可复用）
  for (const loc of ['zh-CN', 'en-US']) {
    const js = JSON.parse(read('locales/' + loc + '/jsonSchema.json'))
    assert.ok(js.valid && js.invalid && js.complete_keyword, loc + ' 应有 jsonSchema 文案')
  }
  const i18n = read('plugins/i18n.js')
  assert.match(i18n, /jsonSchema: zhJsonSchema/)
  assert.match(i18n, /jsonSchema: enJsonSchema/)
})
