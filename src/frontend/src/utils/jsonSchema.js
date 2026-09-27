// JSON Schema 编辑器共通逻辑（P3-C2，2026-09-24）：
// 知识库「参数」页签（[parameters]/[arguments] 存 JSON Schema 文本）与**后续其它配置**复用的
// 共通控件逻辑 —— 纯函数、无 Vue 依赖（node:test 可直测）。能力范围：
//   ① 解析：JSON 优先；失败时按 mcp 契约模板形态的**最小 YAML 子集**（映射 / 序列 / 标量）解析；
//   ② 校验：Schema 文档结构校验（类型关键字、properties/required/items/enum 形状…）；
//   ③ 树形编辑：按路径（`$` 根 / `/properties/<name>` / `/items`）取节点、改节点、增删属性、
//      改名（同步 `required`）、切换必填、增删枚举值、增删关键字（= 补全）；
//   ④ 序列化：规范 JSON（缩进 2）——保存时以文本回写 [parameters]/[arguments] 区。

// SCHEMA_TYPES JSON Schema 的 type 取值候选（控件的类型下拉 = 补全来源）。
export const SCHEMA_TYPES = ['string', 'number', 'integer', 'boolean', 'object', 'array', 'null']

// KEYWORDS_BY_TYPE 各类型可补全的关键字（顺序即展示顺序）。
const KEYWORDS_BY_TYPE = {
  common: ['title', 'description', 'default'],
  string: ['enum', 'format', 'minLength', 'maxLength', 'pattern'],
  number: ['enum', 'minimum', 'maximum', 'multipleOf'],
  integer: ['enum', 'minimum', 'maximum', 'multipleOf'],
  boolean: [],
  array: ['items', 'minItems', 'maxItems', 'uniqueItems'],
  object: ['properties', 'required', 'additionalProperties'],
  null: [],
}

// keywordSuggestions 该类型可补全的关键字列表（含通用关键字）。
export function keywordSuggestions(type) {
  return [...KEYWORDS_BY_TYPE.common, ...(KEYWORDS_BY_TYPE[type] || [])]
}

function isPlainObject(v) {
  return !!v && typeof v === 'object' && !Array.isArray(v)
}

// inferType 节点类型：显式 type 优先；否则按 properties/items 推断；都没有 → 空。
export function inferType(node) {
  if (!isPlainObject(node)) return ''
  if (typeof node.type === 'string' && node.type) return node.type
  if (isPlainObject(node.properties)) return 'object'
  if (isPlainObject(node.items)) return 'array'
  return ''
}

// ── 解析（JSON → 最小 YAML 子集）─────────────────────────────────

function parseScalar(raw) {
  const s = String(raw).trim()
  if (s === '') return ''
  if (s === 'null' || s === '~') return null
  if (s === 'true') return true
  if (s === 'false') return false
  if (/^-?\d+$/.test(s)) return parseInt(s, 10)
  if (/^-?\d*\.\d+$/.test(s)) return parseFloat(s)
  if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) return s.slice(1, -1)
  if ((s.startsWith('[') && s.endsWith(']')) || (s.startsWith('{') && s.endsWith('}'))) {
    try { return JSON.parse(s) } catch (_) { return s }
  }
  return s
}

// parseYamlish 解析 mcp 契约模板形态的 YAML 最小子集：
//   缩进映射（`key:` 换行缩进子块 / `key: value` 标量）+ 序列（`- item`，项可为标量或映射）。
// 仅覆盖模板/手写参数的常见写法；复杂 YAML（锚点、多行标量等）不支持 → 交由调用方报错。
function parseYamlish(text) {
  const items = []
  for (const ln of String(text).replace(/\r\n?/g, '\n').split('\n')) {
    if (!ln.trim() || ln.trim().startsWith('#')) continue
    const indent = ln.length - ln.replace(/^\s+/, '').length
    items.push({ indent: indent, text: ln.trim() })
  }
  let pos = 0
  const isSeq = (it) => !!it && it.text.startsWith('- ')

  function nextDeeper(indent) {
    if (pos < items.length && items[pos].indent > indent) return parseBlock(items[pos].indent)
    return null
  }

  function parseMapEntries(map, indent) {
    while (pos < items.length && items[pos].indent === indent && !isSeq(items[pos])) {
      const t = items[pos].text
      const ci = t.indexOf(':')
      if (ci < 0) { pos++; continue }
      const k = t.slice(0, ci).trim()
      const rest = t.slice(ci + 1).trim()
      pos++
      if (rest === '') map[k] = nextDeeper(indent)
      else map[k] = parseScalar(rest)
    }
    return map
  }

  function parseSeq(indent) {
    const arr = []
    while (pos < items.length && items[pos].indent === indent && isSeq(items[pos])) {
      const body = items[pos].text.slice(2).trim()
      pos++
      if (body === '') { arr.push(nextDeeper(indent)); continue }
      const ci = body.indexOf(':')
      if (ci > 0 && (ci === body.length - 1 || body[ci + 1] === ' ')) {
        // 序列项 = 映射：行内首键 + 之后更深缩进的同项其余键
        const m = {}
        const k = body.slice(0, ci).trim()
        const rest = body.slice(ci + 1).trim()
        if (rest === '') m[k] = nextDeeper(indent)
        else m[k] = parseScalar(rest)
        while (pos < items.length && items[pos].indent > indent && !isSeq(items[pos])) {
          const t = items[pos].text
          const i2 = t.indexOf(':')
          const ind2 = items[pos].indent
          if (i2 < 0) { pos++; continue }
          const k2 = t.slice(0, i2).trim()
          const r2 = t.slice(i2 + 1).trim()
          pos++
          if (r2 === '') m[k2] = (pos < items.length && items[pos].indent > ind2) ? parseBlock(items[pos].indent) : null
          else m[k2] = parseScalar(r2)
        }
        arr.push(m)
      } else {
        arr.push(parseScalar(body))
      }
    }
    return arr
  }

  function parseBlock(indent) {
    if (isSeq(items[pos])) return parseSeq(indent)
    return parseMapEntries({}, indent)
  }

  if (items.length === 0) return null
  return parseBlock(items[0].indent)
}

// parseSchemaText 解析参数文本 → { schema, error }。
//   空文本 → schema = null、error = ''（= 该原语无参数，编辑器展示空 schema）；
//   形如 JSON（`{`/`[` 开头）但语法错误 → 直接报错（不退回 YAML 猜测：用户本就按 JSON 写，错误须可见）；
//   其余（mcp 契约模板的 YAML 子集）→ 走 YAML 解析；解析不出 → error 给可见原因。
export function parseSchemaText(text) {
  const s = String(text === undefined || text === null ? '' : text).trim()
  if (s === '') return { schema: null, error: '' }
  try {
    return { schema: JSON.parse(s), error: '' }
  } catch (e) {
    if (s[0] === '{' || s[0] === '[') {
      return { schema: null, error: 'JSON 解析失败：' + ((e && e.message) || String(e)) }
    }
  }
  try {
    const v = parseYamlish(s)
    if (v === null) return { schema: null, error: '' }
    return { schema: v, error: '' }
  } catch (e) {
    return { schema: null, error: 'YAML 解析失败：' + ((e && e.message) || String(e)) }
  }
}

// serializeSchema 规范 JSON 文本（缩进 2）；null/undefined → '{}'。
export function serializeSchema(schema) {
  return JSON.stringify(schema === undefined || schema === null ? {} : schema, null, 2)
}

// ── 结构校验（schema 文档形状）───────────────────────────────────

// validateSchema 逐节点校验 Schema 文档结构 → 错误消息数组（空 = 合法）。
export function validateSchema(schema) {
  const errors = []
  function walk(node, path) {
    if (!isPlainObject(node)) {
      errors.push(path + '：不是对象（JSON Schema 节点必须是对象）')
      return
    }
    if (node.type !== undefined) {
      if (typeof node.type !== 'string' || SCHEMA_TYPES.indexOf(node.type) < 0) {
        errors.push(path + '.type：取值须为 ' + SCHEMA_TYPES.join('/') + ' 之一')
      }
    }
    if (node.properties !== undefined) {
      if (!isPlainObject(node.properties)) errors.push(path + '.properties：必须是对象（属性名 → 子 schema）')
      else for (const k of Object.keys(node.properties)) walk(node.properties[k], path + '/properties/' + k)
    }
    if (node.required !== undefined) {
      if (!Array.isArray(node.required) || node.required.some(x => typeof x !== 'string')) {
        errors.push(path + '.required：必须是字符串数组')
      }
    }
    if (node.items !== undefined) {
      if (!isPlainObject(node.items)) errors.push(path + '.items：必须是对象（单项 schema；数组形式的元组未支持）')
      else walk(node.items, path + '/items')
    }
    if (node.enum !== undefined && !Array.isArray(node.enum)) {
      errors.push(path + '.enum：必须是数组')
    }
    for (const key of ['title', 'description', 'format', 'pattern']) {
      if (node[key] !== undefined && typeof node[key] !== 'string') errors.push(path + '.' + key + '：必须是字符串')
    }
    for (const key of ['minimum', 'maximum', 'multipleOf', 'minLength', 'maxLength', 'minItems', 'maxItems']) {
      if (node[key] !== undefined && typeof node[key] !== 'number') errors.push(path + '.' + key + '：必须是数字')
    }
  }
  if (schema === null || schema === undefined) return errors
  walk(schema, '$')
  return errors
}

// ── 路径寻址（$ / properties/<name> / items）─────────────────────

function parsePath(path) {
  const segs = String(path || '$').split('/').filter(s => s !== '' && s !== '$')
  const steps = []
  for (let i = 0; i < segs.length; i++) {
    if (segs[i] === 'properties') { steps.push({ kind: 'property', name: segs[++i] }) }
    else if (segs[i] === 'items') { steps.push({ kind: 'items' }) }
    // 其它段（关键字等）不参与节点寻址 → 忽略（容错）
  }
  return steps
}

// updateNode 按路径**不可变地**更新节点（沿途容器自动补建）。
export function updateNode(root, path, fn) {
  const steps = parsePath(path)
  function rec(node, i) {
    const cur = isPlainObject(node) ? { ...node } : {}
    if (i >= steps.length) return (fn ? fn(cur) : cur) || {}
    const st = steps[i]
    if (st.kind === 'items') {
      cur.items = rec(cur.items, i + 1)
      return cur
    }
    const props = isPlainObject(cur.properties) ? { ...cur.properties } : {}
    props[st.name] = rec(props[st.name], i + 1)
    cur.properties = props
    return cur
  }
  return rec(root, 0)
}

// schemaGet 按路径取节点（不存在 → undefined）。
export function schemaGet(root, path) {
  let node = root
  for (const st of parsePath(path)) {
    if (!isPlainObject(node)) return undefined
    node = st.kind === 'items' ? node.items : (isPlainObject(node.properties) ? node.properties[st.name] : undefined)
  }
  return node
}

// removeNode 删除树上的节点：属性 → 从 properties 摘除并同步 required；items → 摘除 items。
export function removeNode(root, path) {
  const steps = parsePath(path)
  if (steps.length === 0) return root // 根不可删
  const last = steps[steps.length - 1]
  const parentPath = '$' + steps.slice(0, -1).map(s => (s.kind === 'items' ? '/items' : '/properties/' + s.name)).join('')
  return updateNode(root, parentPath, (node) => {
    if (last.kind === 'items') {
      const next = { ...node }
      delete next.items
      return next
    }
    const props = isPlainObject(node.properties) ? { ...node.properties } : {}
    delete props[last.name]
    const next = { ...node, properties: props }
    if (Array.isArray(node.required)) {
      const req = node.required.filter(x => x !== last.name)
      if (req.length > 0) next.required = req
      else delete next.required
    }
    return next
  })
}

// addProperty 在对象节点下新增属性（type 空 → 空 schema）。
export function addProperty(root, parentPath, name, type) {
  const key = String(name || '').trim()
  if (!key) return root
  const node = updateNode(root, parentPath, (n) => {
    const props = isPlainObject(n.properties) ? { ...n.properties } : {}
    props[key] = type ? { type } : {}
    return { ...n, properties: props, type: n.type || 'object' }
  })
  return node
}

// renameProperty 属性改名（同步 `required` 中的旧名）。
export function renameProperty(root, parentPath, oldName, newName) {
  const to = String(newName || '').trim()
  if (!to || to === oldName) return root
  return updateNode(root, parentPath, (n) => {
    const props = isPlainObject(n.properties) ? { ...n.properties } : {}
    if (!(oldName in props)) return n
    const next = {}
    for (const k of Object.keys(props)) next[k === oldName ? to : k] = props[k]
    const out = { ...n, properties: next }
    if (Array.isArray(n.required) && n.required.indexOf(oldName) >= 0) {
      out.required = n.required.map(x => (x === oldName ? to : x))
    }
    return out
  })
}

// setNodeType 设置节点类型（保留其它关键字）。
export function setNodeType(root, path, type) {
  return updateNode(root, path, (n) => {
    const out = { ...n, type }
    if (type !== 'object') delete out.properties
    if (type !== 'object') delete out.required
    if (type !== 'array') delete out.items
    if (type !== 'string' && type !== 'number' && type !== 'integer') delete out.enum
    return out
  })
}

// toggleRequired 切换对象属性是否必填（required 为空则删该关键字）。
export function toggleRequired(root, parentPath, name, on) {
  return updateNode(root, parentPath, (n) => {
    const cur = Array.isArray(n.required) ? n.required.slice() : []
    const has = cur.indexOf(name) >= 0
    const out = { ...n }
    if (on && !has) cur.push(name)
    if (!on && has) cur.splice(cur.indexOf(name), 1)
    if (cur.length > 0) out.required = cur
    else delete out.required
    return out
  })
}

// addEnumValue / removeEnumValue 维护 enum 数组。
export function addEnumValue(root, path, value) {
  return updateNode(root, path, (n) => {
    const list = Array.isArray(n.enum) ? n.enum.slice() : []
    if (list.indexOf(value) >= 0) return n
    list.push(value)
    return { ...n, enum: list }
  })
}

export function removeEnumValue(root, path, index) {
  return updateNode(root, path, (n) => {
    const list = Array.isArray(n.enum) ? n.enum.slice() : []
    list.splice(index, 1)
    const out = { ...n }
    if (list.length > 0) out.enum = list
    else delete out.enum
    return out
  })
}

// toEnumNumber 枚举文本 → 数字：**number** 节点接受十进制数值（含小数，不支持 1e3/0x10 等非 JSON 写法）、
// **integer** 节点仅接受整数；不可转换 → 原样字符串（不静默改变用户输入）。
const INT_TEXT_RE = /^-?\d+$/
const NUM_TEXT_RE = /^-?\d+(\.\d+)?$/
function toEnumNumber(s, type) {
  if (type === 'integer') return INT_TEXT_RE.test(s) ? parseInt(s, 10) : s
  return NUM_TEXT_RE.test(s) ? Number(s) : s
}

// setEnumValues 批量写回 enum（枚举弹框的行编辑）：过滤空白项、按首次出现去重（与 addEnumValue 同口径），
// 保持输入顺序；**number / integer 节点**：可转数字的输入写回**数字**（弹框输入均为文本，此处做数字转换），
// 不可转换者保持字符串；**type 缺失或其它类型**：一律字符串原文（2026-09-27 用户口径：做数字转换，
// 但前端 chip 不显示值 —— 值一律走 Tooltip）。
export function setEnumValues(root, path, values) {
  return updateNode(root, path, (n) => {
    const numeric = n.type === 'number' || n.type === 'integer'
    const list = []
    for (const raw of (Array.isArray(values) ? values : [])) {
      const s = String(raw === undefined || raw === null ? '' : raw).trim()
      if (s === '') continue
      const val = numeric ? toEnumNumber(s, n.type) : s
      if (list.indexOf(val) >= 0) continue
      list.push(val)
    }
    const out = { ...n }
    if (list.length > 0) out.enum = list
    else delete out.enum
    return out
  })
}

// defaultKeywordValue 关键字补全时的默认值（点选建议即写入该值，随后可就地调整）。
export function defaultKeywordValue(keyword, type) {
  switch (keyword) {
    case 'properties': return {}
    case 'required': return []
    case 'items': return { type: 'string' }
    case 'enum': return []
    case 'additionalProperties': return true
    case 'uniqueItems': return false
    case 'title': case 'description': case 'format': case 'pattern': return ''
    case 'minimum': case 'maximum': case 'multipleOf':
    case 'minLength': case 'maxLength': case 'minItems': case 'maxItems': return 0
    case 'default': return type === 'number' || type === 'integer' ? 0 : (type === 'boolean' ? false : '')
    default: return ''
  }
}

// addKeyword 关键字补全：节点上尚无该关键字时按默认值写入。
export function addKeyword(root, path, keyword, type) {
  return updateNode(root, path, (n) => {
    if (keyword in n) return n
    return { ...n, [keyword]: defaultKeywordValue(keyword, type || inferType(n)) }
  })
}

// removeKeyword 摘除关键字（properties/required 等结构性关键字也可摘；用于"清空补全"）。
export function removeKeyword(root, path, keyword) {
  return updateNode(root, path, (n) => {
    const out = { ...n }
    delete out[keyword]
    return out
  })
}

// setKeywordValue 就地设置/更新节点上的关键字值（关键字 chip 点击设值）。
export function setKeywordValue(root, path, keyword, value) {
  return updateNode(root, path, (n) => ({ ...n, [keyword]: value }))
}

// keywordTooltipText 关键字 chip 的 tooltip 文本（chip 上仅显示关键字名，值一律悬停可见）：
//   enum → 各枚举值每行一个（`\n` 连接，配合 Tooltip 的 pre-wrap 保留换行）；
//   其余关键字 → 值原文（字符串原样 / 数字·布尔 String / 对象 JSON）；
//   未设置或空串 → ''（由调用方给「（空）」占位，避免空白 tip）。
export function keywordTooltipText(node, keyword) {
  const n = isPlainObject(node) ? node : {}
  if (keyword === 'enum') {
    const list = Array.isArray(n.enum) ? n.enum : []
    return list.map(v => (v === undefined || v === null ? '' : String(v))).join('\n')
  }
  const raw = n[keyword]
  if (raw === undefined || raw === null) return ''
  if (typeof raw === 'object') return JSON.stringify(raw)
  return String(raw)
}

// schemaRows 展平为树形行模型（控件渲染用）：根行 + 每个属性/数组项一行（含深度）。
export function schemaRows(schema) {
  const rows = []
  function walk(node, path, name, depth, requiredList) {
    const type = inferType(node)
    rows.push({
      path, name, depth, type,
      required: requiredList.indexOf(name) >= 0,
      node: isPlainObject(node) ? node : {},
    })
    if (type === 'object' && isPlainObject(node.properties)) {
      const req = Array.isArray(node.required) ? node.required : []
      for (const key of Object.keys(node.properties)) {
        walk(node.properties[key], path + '/properties/' + key, key, depth + 1, req)
      }
    } else if (type === 'array' && isPlainObject(node.items)) {
      walk(node.items, path + '/items', 'items', depth + 1, [])
    }
  }
  walk(isPlainObject(schema) ? schema : {}, '$', '$', 0, [])
  return rows
}
