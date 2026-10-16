// 原语「基本信息」（meta）的**按类型专用表单**定义与值转换（纯数据 + 纯函数，可单测）。
//
// 字段 = 契约里该类型的专属 meta 键（契约源见 docs/spec/70-conventions/72-工具开发规范.md §2 与
// 20-modules/25-mcp-server.md §3.1）：
//   tool     ：runtime / category / async / async-threshold / timeout / idempotent / hot / args / output
//   resource ：path（外部内容文件）；skill / prompt：无专属键；agent：由 AgentEditor 承载（本面板不渲染表单）
// 未在表单中的键（用户自建 / 未来新增）归「自定义键」KV 区维护 → load → form → save 往返保真。

// 字段 def：
//   key      契约 meta 键（原名，不翻译）
//   type     'text' | 'number' | 'enum' | 'bool' → 控件类型
//   labelKey i18n 键（knowledgeList.<labelKey>）
//   hintKey  可选，字段说明（knowledgeList.<hintKey>）
//   options  仅 enum：[{ value, labelKey }]
export const META_FIELDS = {
  tool: [
    { key: 'runtime', type: 'text', labelKey: 'field_runtime', hintKey: 'field_runtime_hint' },
    {
      key: 'category', type: 'enum', labelKey: 'field_category', options: [
        { value: 'core', labelKey: 'field_category_core' },
        { value: 'desktop', labelKey: 'field_category_desktop' },
        { value: 'browser', labelKey: 'field_category_browser' },
        { value: 'server', labelKey: 'field_category_server' },
      ],
    },
    {
      key: 'async', type: 'enum', labelKey: 'field_async', hintKey: 'field_async_hint', options: [
        { value: 'auto', labelKey: 'field_async_auto' },
        { value: 'always', labelKey: 'field_async_always' },
        { value: 'never', labelKey: 'field_async_never' },
        { value: 'manual', labelKey: 'field_async_manual' },
      ],
    },
    { key: 'async-threshold', type: 'number', labelKey: 'field_async_threshold', hintKey: 'field_async_threshold_hint' },
    { key: 'timeout', type: 'number', labelKey: 'field_timeout', hintKey: 'field_timeout_hint' },
    { key: 'idempotent', type: 'bool', labelKey: 'field_idempotent', hintKey: 'field_idempotent_hint' },
    { key: 'hot', type: 'bool', labelKey: 'field_hot', hintKey: 'field_hot_hint' },
    { key: 'args', type: 'text', labelKey: 'field_args', hintKey: 'field_args_hint' },
    {
      key: 'output', type: 'enum', labelKey: 'field_output', hintKey: 'field_output_hint', options: [
        { value: 'stdout', labelKey: 'field_output_stdout' },
        { value: 'code', labelKey: 'field_output_code' },
        { value: 'file', labelKey: 'field_output_file' },
      ],
    },
  ],
  skill: [],
  prompt: [],
  resource: [
    { key: 'path', type: 'text', labelKey: 'field_path' },
  ],
  agent: [],
}

// 该类型的表单字段清单（未知 token → 空）。
export function metaFieldsOf(token) {
  return META_FIELDS[token] || []
}

// 是否为表单已覆盖的键（含 `name`：由表单顶部只读「名称」行承载）→ 不进「自定义键」区。
export function isSchemaKey(token, key) {
  const k = String(key || '').trim()
  if (!k) return false
  return k === 'name' || metaFieldsOf(token).some((f) => f.key === k)
}

// meta 字符串值 → 控件值（bool → Boolean；其余 → 原字符串；缺键 → ''/false）。
export function fieldValueOf(field, meta) {
  const raw = meta ? meta[field.key] : undefined
  const s = raw === undefined || raw === null ? '' : String(raw)
  if (field.type === 'bool') return s.toLowerCase() === 'true'
  return s
}

// 控件值 → meta 字符串值（'' = 清空该键；bool：true → 'true'、false → ''，与 buildContractMd 的空值省略一致）。
export function fieldToMetaOf(field, value) {
  if (field.type === 'bool') return value ? 'true' : ''
  if (value === null || value === undefined) return ''
  return String(value)
}
