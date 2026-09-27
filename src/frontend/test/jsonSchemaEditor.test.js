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
  addEnumValue, removeEnumValue, setEnumValues, addKeyword, removeKeyword, setKeywordValue,
  keywordTooltipText, removeNode, defaultKeywordValue,
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

test('JsonSchema：枚举弹框批量写回（过滤空行/去重/保序；number·integer 节点写数字）', () => {
  // 过滤空白项、去重（与 addEnumValue 同口径）、保持输入顺序
  let s = setEnumValues(SCHEMA, '$/properties/name', [' a ', 'b', 'a', '', '  ', 'c'])
  assert.deepEqual(s.properties.name.enum, ['a', 'b', 'c'])
  // 全空 → 摘除 enum 关键字（与 removeEnumValue 清空口径一致）
  s = setEnumValues(s, '$/properties/name', ['', '   '])
  assert.equal(s.properties.name.enum, undefined)
  // number 节点：可转数字的输入写回**数字**（'1'→1、'2.5'→2.5）；不可转者保持字符串（'x'）；
  // 末位 '1' 与首位 1 转换后同值 → 去重（2026-09-27 用户口径：做数字转换）
  let num = addProperty(SCHEMA, '$', 'num', 'number')
  num = setEnumValues(num, '$/properties/num', ['1', '2.5', 'x', '1'])
  assert.deepEqual(num.properties.num.enum, [1, 2.5, 'x'], 'number 节点数值写回数字、不可转者保留字符串')
  assert.equal(typeof num.properties.num.enum[0], 'number')
  assert.equal(typeof num.properties.num.enum[2], 'string')
  // integer 节点：仅整数写回数字（'2.5' 非整数 → 保留字符串）
  let int = addProperty(SCHEMA, '$', 'cnt', 'integer')
  int = setEnumValues(int, '$/properties/cnt', ['1', '-2', '2.5'])
  assert.deepEqual(int.properties.cnt.enum, [1, -2, '2.5'], 'integer 节点整数写数字、非整数保留字符串')
  // 不改动既有非 enum 关键字
  s = setEnumValues(SCHEMA, '$/properties/name', ['a'])
  assert.equal(s.properties.name.description, '名称')
  assert.deepEqual(s.properties.name.enum, ['a'])
})

test('JsonSchema：关键字 chip 设值（setKeywordValue 就地写入）', () => {
  let s = setKeywordValue(SCHEMA, '$/properties/name', 'title', '名称标题')
  assert.equal(s.properties.name.title, '名称标题')
  s = setKeywordValue(s, '$/properties/name', 'description', '多行\n描述')
  assert.equal(s.properties.name.description, '多行\n描述', '覆盖既有值')
  assert.equal(SCHEMA.properties.name.description, '名称', '不可变：原对象不受影响')
})

test('JsonSchema：chip 只显关键字名、值一律走 tooltip（enum 多行 / 空值占位）', () => {
  const node = { type: 'string', title: '标题', description: '第一行\n第二行', format: 'date', pattern: '^a$', enum: ['a', 'b'] }
  // 各关键字的 tooltip 内容口径 = 值原文（description 原样保留换行）
  assert.equal(keywordTooltipText(node, 'title'), '标题')
  assert.equal(keywordTooltipText(node, 'description'), '第一行\n第二行', 'description 原样（含换行）')
  assert.equal(keywordTooltipText(node, 'format'), 'date')
  assert.equal(keywordTooltipText(node, 'pattern'), '^a$')
  // enum → 各枚举值每行一个（\n 连接，配合 Tooltip pre-wrap 保留换行）
  assert.equal(keywordTooltipText(node, 'enum'), 'a\nb')
  // 数值 / 布尔关键字 → String（与 chip 上的值一致口径）
  assert.equal(keywordTooltipText({ minimum: 0 }, 'minimum'), '0')
  assert.equal(keywordTooltipText({ additionalProperties: false }, 'additionalProperties'), 'false')
  assert.equal(keywordTooltipText({ default: {} }, 'default'), '{}', '对象值走 JSON')
  // 空值（未设置 / 空串 / 空枚举）→ ''（组件侧给「（空）」占位）
  assert.equal(keywordTooltipText({ title: '' }, 'title'), '')
  assert.equal(keywordTooltipText({}, 'description'), '')
  assert.equal(keywordTooltipText({ enum: [] }, 'enum'), '')
  assert.equal(keywordTooltipText(null, 'title'), '')

  // 组件落点：chip 只渲染关键字名；Tooltip 承载值；点击设值入口保留；enum 单独成 chip
  const comp = read('components/editor/JsonSchemaEditor.vue')
  assert.match(comp, /import \{ Input, Textarea, Button, Tooltip, promptInput, promptList \}/, '须引入 Tooltip')
  assert.match(comp, /:content="chipTooltip\(row, k\)"/, '关键字 chip 须由 Tooltip 承载值')
  assert.match(comp, /<span class="jse-enum-text">\{\{ k \}\}<\/span>/, 'chip 文本只渲染关键字名（不含值）')
  assert.match(comp, /<span class="jse-enum-text">enum<\/span>/, '枚举为单个 `enum` chip（值不落 chip）')
  assert.match(comp, /:content="chipTooltip\(row, 'enum'\)"/, 'enum chip tooltip = 全部枚举值（多行）')
  assert.doesNotMatch(comp, /keywordChipText/, '不再拼接「关键字: 值」到 chip')
  assert.doesNotMatch(comp, /\{\{ v \}\}/, '不再逐值渲染枚举 chip')
  assert.doesNotMatch(comp, /onRemoveEnum/, '逐值删除已并入枚举弹框（无死代码）')
  assert.match(comp, /keywordTooltipText/, 'tooltip 文案走共享纯函数')
  assert.match(comp, /jsonSchema\.empty_value/, '空值 tooltip 须给占位文案')
  assert.match(comp, /@click="onKeywordChip\(row, k\)"/, '点击 chip 设值入口须保留')
  assert.match(comp, /@click="onEditEnum\(row\)"/, 'enum chip 点击开行编辑弹框')
  // Tooltip 须能保留换行（pre-wrap）+ 仍折行
  const tip = read('components/ui/Tooltip.vue')
  const block = tip.match(/\.b-tooltip__popper \{[\s\S]*?\n\}/)
  assert.ok(block && /white-space: pre-wrap/.test(block[0]), 'tooltip 须 white-space: pre-wrap（保留换行）')
  assert.ok(block && /max-width: \d+px/.test(block[0]), 'tooltip 仍须 max-width 折行')
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
  // 描述页签「从正文提取」按钮 + 提取逻辑走 utils 纯函数
  assert.match(panel, /#description[\s\S]*knowledgeList\.extract_from_content/, '描述页签含「从正文提取」')
  assert.match(panel, /from '\.\.\/\.\.\/utils\/descriptionExtract'/, '提取逻辑走 utils 纯函数')
  // 描述/正文 Textarea 撑满（容器 flex + textarea flex 撑高）；修掉失效选择器、移除固定 :rows
  assert.match(panel, /\.prim-tab-body \.pf-area/, 'pf-area 直接命中 textarea 并 flex 撑满')
  assert.doesNotMatch(panel, /pf-area :deep\(textarea\)/, '修掉恒不命中的 :deep(textarea) 选择器')
  assert.doesNotMatch(panel, /:rows="1[26]"/, '描述/正文不再用固定 :rows')
})

test('JsonSchema：编辑器为共通控件（独立于知识库，逻辑纯函数、无 watch）', () => {
  const comp = read('components/editor/JsonSchemaEditor.vue')
  assert.match(comp, /from '\.\.\/\.\.\/utils\/jsonSchema'/, '逻辑走共享纯函数模块')
  assert.doesNotMatch(comp, /watch\(|watchEffect\(/, '组件规则：禁止 watch/watcheffect 监听 props')
  assert.match(comp, /jse-row/, '树形层级行')
  assert.match(comp, /complete_keyword/, '关键字补全入口')
  assert.match(comp, /SCHEMA_TYPES/, '类型候选 = 补全来源')
  // 枚举值 = 弹框行编辑（复用 KeyValueEditor list 模式的 promptList）+ 批量写回
  assert.match(comp, /promptList/, '枚举弹框走 promptList（复用 KeyValueEditor list 模式）')
  assert.match(comp, /setEnumValues/, '枚举批量写回')
  assert.match(comp, /onEditEnum/, '枚举弹框入口')
  assert.doesNotMatch(comp, /addEnumValue/, '单值 promptInput 口径已由弹框取代')
  // 字符串关键字 chip 可点击设值
  assert.match(comp, /setKeywordValue/, '字符串关键字 chip 设值')
  assert.match(comp, /STRING_KEYWORDS/, '仅字符串关键字可点击设值')
  assert.match(comp, /set_keyword/, 'chip 设值提示文案')
  // i18n 命名空间独立（后续其它配置可复用）
  for (const loc of ['zh-CN', 'en-US']) {
    const js = JSON.parse(read('locales/' + loc + '/jsonSchema.json'))
    assert.ok(js.valid && js.invalid && js.complete_keyword, loc + ' 应有 jsonSchema 文案')
    assert.ok(js.enum_dialog_title && js.enum_add_row && js.remove_enum && js.set_keyword && js.empty_value, loc + ' 应有枚举弹框/chip 设值/空值占位文案')
  }
  const i18n = read('plugins/i18n.js')
  assert.match(i18n, /jsonSchema: zhJsonSchema/)
  assert.match(i18n, /jsonSchema: enJsonSchema/)
})
