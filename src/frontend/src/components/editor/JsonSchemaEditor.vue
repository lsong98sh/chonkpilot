<template>
  <!-- JSON Schema 编辑器（共通控件，P3-C2）：树形层级 + 校验 + 关键字/类型补全。
       供知识库「参数」页签（[parameters]/[arguments] = JSON Schema 文本）与后续其它配置复用；
       全部逻辑走 utils/jsonSchema.js（纯函数）。仅在用户操作时 emit（不因初始化/重解析回写）。 -->
  <div class="jse">
    <div class="jse-status">
      <span v-if="parseError" class="jse-bad">{{ errText(parseError) }}</span>
      <span v-else-if="errors.length > 0" class="jse-bad">{{ $t('jsonSchema.invalid') }}（{{ errors.length }}）</span>
      <span v-else class="jse-ok">{{ $t('jsonSchema.valid') }}</span>
      <span class="jse-hint">{{ $t('jsonSchema.hint') }}</span>
      <Button v-if="parseError" size="small" text class="jse-rewrite" @click="rewriteEmpty">
        {{ $t('jsonSchema.rewrite') }}
      </Button>
    </div>

    <ul v-if="!parseError && errors.length > 0" class="jse-errors">
      <li v-for="(e, i) in errors" :key="i">{{ errText(e) }}</li>
    </ul>

    <div v-if="!parseError" class="jse-tree">
      <div
        v-for="row in rows"
        :key="row.path"
        class="jse-row"
        :style="{ paddingLeft: (row.depth * 14 + 4) + 'px' }"
      >
        <span v-if="row.depth === 0" class="jse-name jse-root">$</span>
        <Input
          v-else-if="row.name !== 'items'"
          class="jse-name-input"
          size="small"
          :model-value="row.name"
          @change="onRename(row, $event.target.value)"
        />
        <span v-else class="jse-name">{{ row.name }}</span>

        <select class="jse-type" :value="row.type" @change="onType(row, $event.target.value)">
          <option value="">—</option>
          <option v-for="t in SCHEMA_TYPES" :key="t" :value="t">{{ t }}</option>
        </select>

        <label v-if="row.depth > 0 && row.name !== 'items'" class="jse-req">
          <input type="checkbox" :checked="row.required" @change="onRequired(row, $event.target.checked)" />
          {{ $t('jsonSchema.required') }}
        </label>

        <span v-if="row.type === 'object'" class="jse-actions">
          <Button size="small" text class="jse-act" @click="onAddProperty(row)">
            {{ $t('jsonSchema.add_property') }}
          </Button>
        </span>
        <span v-if="isEnumType(row.type)" class="jse-actions">
          <Button size="small" text class="jse-act" @click="onEditEnum(row)">
            {{ $t('jsonSchema.add_enum') }}
          </Button>
        </span>

        <select
          v-if="remainingKeywords(row).length > 0"
          class="jse-kw"
          :value="''"
          @change="onAddKeyword(row, $event.target.value, $event)"
        >
          <option value="">{{ $t('jsonSchema.complete_keyword') }}</option>
          <option v-for="k in remainingKeywords(row)" :key="k" :value="k">{{ k }}</option>
        </select>

        <Button v-if="row.depth > 0" size="small" text type="danger" class="jse-act" @click="onRemove(row)">
          ✕
        </Button>

        <!-- 关键字 chip：文案只留**关键字名**，值一律由 Tooltip 承载（悬停可见，支持换行）。 -->
        <span v-if="presentKeywords(row).length > 0" class="jse-enums">
          <Tooltip
            v-for="k in presentKeywords(row)"
            :key="k"
            :content="chipTooltip(row, k)"
          >
            <span
              class="jse-enum"
              :class="{ 'jse-enum-editable': isEditableKeyword(k) }"
              @click="onKeywordChip(row, k)"
            >
              <span class="jse-enum-text">{{ k }}</span>
              <button type="button" class="jse-enum-x" :title="$t('jsonSchema.remove_keyword')" @click.stop="onRemoveKeyword(row, k)">✕</button>
            </span>
          </Tooltip>
        </span>
        <!-- 枚举（结构性关键字）→ 单个 `enum` chip：枚举值一律由 tooltip 承载（每行一个），点击开行编辑弹框。 -->
        <span v-if="row.node.enum && row.node.enum.length" class="jse-enums">
          <Tooltip :content="chipTooltip(row, 'enum')">
            <span class="jse-enum jse-enum-editable" @click="onEditEnum(row)">
              <span class="jse-enum-text">enum</span>
              <button type="button" class="jse-enum-x" :title="$t('jsonSchema.remove_keyword')" @click.stop="onRemoveKeyword(row, 'enum')">✕</button>
            </span>
          </Tooltip>
        </span>
      </div>
    </div>

    <Textarea class="jse-preview" :model-value="serialized" :rows="8" readonly />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input, Textarea, Button, Tooltip, promptInput, promptList } from '../../components/ui'
import {
  SCHEMA_TYPES, keywordSuggestions, inferType, parseSchemaText, serializeSchema, validateSchema,
  removeNode, addProperty, renameProperty, setNodeType, toggleRequired,
  setEnumValues, addKeyword, removeKeyword, setKeywordValue, keywordTooltipText, schemaRows,
} from '../../utils/jsonSchema'

const props = defineProps({
  // 参数文本（JSON 或 mcp 契约模板形态的 YAML 子集）；用户操作时以规范 JSON 回写。
  modelValue: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

const { t } = useI18n()

const schema = ref({})
const parseError = ref('')

// 解析（挂载时；父组件经 :key 变更在重载/恢复后重建本控件 → 不需要 watch props）
function reparse() {
  const res = parseSchemaText(props.modelValue)
  parseError.value = res.error
  schema.value = res.schema === null || res.schema === undefined ? {} : res.schema
}
onMounted(reparse)

const rows = computed(() => schemaRows(schema.value))
const errors = computed(() => (parseError.value ? [] : validateSchema(schema.value)))
const serialized = computed(() => serializeSchema(schema.value))

// errText 错误描述符 → 本地化文案（解析/校验错误均以 { code, ...params } 描述，code 为 jsonSchema 命名空间键名）。
function errText(d) {
  return d ? t('jsonSchema.' + d.code, d) : ''
}

function apply(next) {
  schema.value = next
  emit('update:modelValue', serializeSchema(next))
}

function isEnumType(type) {
  return type === 'string' || type === 'number' || type === 'integer'
}

// 关键字补全候选 = 该类型建议关键字中尚未使用的（按类型 + 通用）
function remainingKeywords(row) {
  const node = row.node || {}
  const type = row.type || inferType(node)
  return keywordSuggestions(type).filter(k => !(k in node))
}

// 已补全的可选关键字（结构关键字由类型下拉 / 必填勾选 / 子行表达 → 不重复列出）
const STRUCTURAL_KEYWORDS = ['type', 'properties', 'required', 'items', 'enum']
function presentKeywords(row) {
  const node = row.node || {}
  const type = row.type || inferType(node)
  return keywordSuggestions(type).filter(k => (k in node) && STRUCTURAL_KEYWORDS.indexOf(k) < 0)
}

// 可就地设值的字符串关键字（title/description 多行，format/pattern 单行）；其余 chip 仅可移除。
const STRING_KEYWORDS = ['title', 'description', 'format', 'pattern']
function isEditableKeyword(keyword) {
  return STRING_KEYWORDS.indexOf(keyword) >= 0
}

// chipTooltip chip 的 tooltip 文案 = 该关键字的值（enum = 各枚举值每行一个，支持换行）；
// 空值（未设置 / 空串 / 空枚举）→ 占位文案，避免悬停出空白 tip。
function chipTooltip(row, keyword) {
  return keywordTooltipText(row.node, keyword) || t('jsonSchema.empty_value')
}

// onKeywordChip 点击字符串关键字 chip → 录入/修改值（description 多行 Textarea，其余单行 Input）。
async function onKeywordChip(row, keyword) {
  if (!isEditableKeyword(keyword)) return
  const raw = row.node ? row.node[keyword] : undefined
  const cur = raw === undefined || raw === null ? '' : String(raw)
  let value
  try {
    value = await promptInput(t('jsonSchema.set_keyword', { keyword }), cur, { multiline: keyword === 'description' })
  } catch (_) { return }
  if (value === null || value === undefined) return
  apply(setKeywordValue(schema.value, row.path, keyword, String(value)))
}

function onRemoveKeyword(row, keyword) {
  apply(removeKeyword(schema.value, row.path, keyword))
}

function onRename(row, value) {
  const parentPath = row.path.slice(0, row.path.lastIndexOf('/properties/'))
  apply(renameProperty(schema.value, parentPath || '$', row.name, value))
}

function onType(row, type) {
  apply(setNodeType(schema.value, row.path, type))
}

function onRequired(row, checked) {
  const parentPath = row.path.slice(0, row.path.lastIndexOf('/properties/'))
  apply(toggleRequired(schema.value, parentPath || '$', row.name, checked))
}

async function onAddProperty(row) {
  let name
  try {
    name = await promptInput(t('jsonSchema.new_property_name'), '')
  } catch (_) { return }
  if (name === null || name === undefined) return
  const key = String(name).trim()
  if (!key) return
  apply(addProperty(schema.value, row.path, key, 'string'))
}

// onEditEnum 打开枚举值行编辑弹框（KeyValueEditor list 模式，复用于 promptList）；确定 → 批量写回 enum。
async function onEditEnum(row) {
  const current = Array.isArray(row.node && row.node.enum) ? row.node.enum : []
  let values
  try {
    values = await promptList(t('jsonSchema.enum_dialog_title'), current, {
      valuePlaceholder: t('jsonSchema.enum_value'),
      addLabel: t('jsonSchema.enum_add_row'),
      deleteLabel: t('jsonSchema.remove_enum'),
    })
  } catch (_) { return }
  if (values === null || values === undefined) return
  apply(setEnumValues(schema.value, row.path, values))
}

function onAddKeyword(row, keyword, event) {
  if (!keyword) return
  apply(addKeyword(schema.value, row.path, keyword, row.type))
  if (event && event.target) event.target.value = '' // 复位下拉（同一关键字可重复补全其它节点）
}

function onRemove(row) {
  apply(removeNode(schema.value, row.path))
}

// 解析失败时允许以空 Schema 重写（丢掉无法解析的旧文本；其余内容视图不动）
function rewriteEmpty() {
  parseError.value = ''
  apply({ type: 'object', properties: {} })
}
</script>

<style scoped>
.jse {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
}
.jse-status {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.jse-ok {
  color: var(--success, #67c23a);
}
.jse-bad {
  color: var(--danger, #f56c6c);
}
.jse-hint {
  flex: 1;
  min-width: 0;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.jse-errors {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--danger, #f56c6c);
  max-height: 120px;
  overflow: auto;
}
.jse-tree {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px 4px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 46vh;
  overflow: auto;
}
.jse-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.jse-name {
  font-size: 12px;
  font-family: var(--font-mono, Consolas, monospace);
  color: var(--text-primary);
  flex-shrink: 0;
}
.jse-root {
  font-weight: 700;
}
.jse-name-input {
  width: 160px;
  flex-shrink: 0;
}
.jse-type,
.jse-kw {
  font-size: 12px;
  padding: 2px 4px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  background: var(--bg-primary);
  color: var(--text-primary);
  flex-shrink: 0;
}
.jse-req {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 11px;
  color: var(--text-muted);
  flex-shrink: 0;
}
.jse-actions {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
}
.jse-act {
  font-size: 12px;
}
.jse-enums {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  min-width: 0;
}
.jse-enum {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 0 4px;
  border: 1px solid var(--border);
  border-radius: 10px;
  font-size: 11px;
  color: var(--text-secondary);
  max-width: 220px;
  min-width: 0;
}
.jse-enum-editable {
  cursor: pointer;
}
.jse-enum-editable:hover {
  border-color: var(--accent);
  color: var(--text-primary);
}
.jse-enum-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.jse-enum-x {
  border: none;
  background: none;
  padding: 0;
  cursor: pointer;
  color: var(--text-muted);
  font-size: 10px;
}
.jse-preview :deep(textarea) {
  font-family: var(--font-mono, Consolas, monospace);
  font-size: 12px;
  resize: vertical;
}
</style>
